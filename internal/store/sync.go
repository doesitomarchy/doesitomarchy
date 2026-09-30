package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

// catalogTables are deleted child-first. Result tables (Phase 7) are never listed here.
var catalogTables = []string{
	"config_capabilities", "config_features", "config_ports", "config_components", "config_aliases",
	"configs", "releases", "macs", "component_ids", "components", "capabilities", "categories", "vocab",
}

// SyncCatalog replaces the catalog tables with c in one transaction. hash
// identifies the catalog content (catalog.HashFS); if it matches the last
// sync, nothing is written and changed is false.
//
// Foreign keys are checked at commit (defer_foreign_keys), so rows that other
// tables reference (configs, later results) can be deleted and re-inserted.
func (s *Store) SyncCatalog(ctx context.Context, c *catalog.Catalog, hash string) (changed bool, err error) {
	if prev, err := s.Setting(ctx, "catalog_hash"); err == nil && prev == hash && hash != "" {
		return false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
		return false, err
	}
	for _, t := range catalogTables {
		if _, err = tx.ExecContext(ctx, "DELETE FROM "+t); err != nil {
			return false, fmt.Errorf("clear %s: %w", t, err)
		}
	}
	w := &writer{ctx: ctx, tx: tx, stmts: map[string]*sql.Stmt{}}
	defer w.close()
	w.vocab(&c.Vocab)
	w.capabilities(c)
	w.components(c)
	w.macs(c)
	if w.err != nil {
		return false, w.err
	}
	if _, err = tx.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES ('catalog_hash', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", hash); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("sync commit: %w", err)
	}
	return true, nil
}

// writer keeps the first error so the insert sequence reads straight through,
// and reuses one prepared statement per distinct query.
type writer struct {
	ctx   context.Context
	tx    *sql.Tx
	stmts map[string]*sql.Stmt
	err   error
}

func (w *writer) exec(query string, args ...any) {
	if w.err != nil {
		return
	}
	stmt, ok := w.stmts[query]
	if !ok {
		var err error
		if stmt, err = w.tx.PrepareContext(w.ctx, query); err != nil {
			w.err = fmt.Errorf("prepare %s: %w", strings.Fields(query)[2], err)
			return
		}
		w.stmts[query] = stmt
	}
	if _, err := stmt.ExecContext(w.ctx, args...); err != nil {
		w.err = fmt.Errorf("%s: %w", strings.Fields(query)[2], err)
	}
}

func (w *writer) close() {
	for _, s := range w.stmts {
		s.Close()
	}
}

// js encodes v as JSON text; nil slices become [] so readers never see null.
func js(v any) string {
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // catalog types always marshal
	}
	return string(b)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (w *writer) vocab(v *catalog.Vocabulary) {
	const q = "INSERT INTO vocab (kind, key, name, attrs) VALUES (?, ?, ?, ?)"
	for _, k := range sortedKeys(v.Lines) {
		l := v.Lines[k]
		w.exec(q, "lines", k, l.Name, js(map[string]string{"form": l.Form, "identifier_prefix": l.IdentifierPrefix}))
	}
	for _, k := range sortedKeys(v.SecurityChips) {
		w.exec(q, "security_chips", k, v.SecurityChips[k].Name, "{}")
	}
	for _, k := range sortedKeys(v.ComponentKinds) {
		w.exec(q, "component_kinds", k, v.ComponentKinds[k].Name, "{}")
	}
	for _, k := range sortedKeys(v.CPUCodenames) {
		cn := v.CPUCodenames[k]
		w.exec(q, "cpu_codenames", k, cn.Name, js(map[string]any{"family": cn.Family, "bits": cn.Bits}))
	}
	for _, k := range sortedKeys(v.Ports) {
		w.exec(q, "ports", k, v.Ports[k].Name, js(map[string]bool{"video": v.Ports[k].Video}))
	}
	for _, k := range sortedKeys(v.Features) {
		w.exec(q, "features", k, v.Features[k].Name, "{}")
	}
}

func (w *writer) capabilities(c *catalog.Catalog) {
	for i, cat := range c.Categories {
		w.exec("INSERT INTO categories (id, name, icon, blocking, ord) VALUES (?, ?, ?, ?, ?)",
			cat.ID, cat.Name, cat.Icon, b2i(cat.Blocking), i)
	}
	for i, cp := range c.Capabilities {
		w.exec("INSERT INTO capabilities (id, category_id, name, description, ord) VALUES (?, ?, ?, ?, ?)",
			cp.ID, cp.Category(), cp.Name, cp.Description, i)
	}
}

func (w *writer) components(c *catalog.Catalog) {
	for _, id := range sortedKeys(c.Components) {
		comp := c.Components[id]
		w.exec(`INSERT INTO components (id, kind, name, vendor, role, driver, notes, sources, uncertain)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			comp.ID, comp.Kind, comp.Name, comp.Vendor, comp.Role, comp.Driver, comp.Notes, js(comp.Sources), js(comp.Uncertain))
		for _, hw := range comp.IDs {
			w.exec("INSERT INTO component_ids (component_id, hw_id) VALUES (?, ?)", comp.ID, hw)
		}
	}
}

func (w *writer) macs(c *catalog.Catalog) {
	cfgOrd := 0
	for mi, m := range c.Macs {
		slug := catalog.FileSlug(m.Identifier)
		w.exec(`INSERT INTO macs (identifier, slug, slug_lc, line, efi, security_chip, hard_blocker, board_ids,
			research_notes, sources, uncertain, ord) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.Identifier, slug, strings.ToLower(slug), m.Line, m.EFI, m.SecurityChip, m.HardBlocker, js(m.BoardIDs),
			m.ResearchNotes, js(m.Sources), js(m.Uncertain), mi)
		for ri := range m.Releases {
			r := &m.Releases[ri]
			w.exec(`INSERT INTO releases (mac_identifier, id, name, announced, discontinued, model_numbers, emc, sources, ord)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				m.Identifier, r.ID, r.Name, r.Announced, r.Discontinued, js(r.ModelNumbers), js(r.EMC), js(r.Sources), ri)
			for ci := range r.Configs {
				w.config(c, m, r, &r.Configs[ci], cfgOrd)
				cfgOrd++
			}
		}
	}
}

func (w *writer) config(c *catalog.Catalog, m *catalog.Mac, r *catalog.Release, cfg *catalog.Config, ord int) {
	var display any
	if cfg.Display != nil {
		display = js(cfg.Display)
	}
	w.exec(`INSERT INTO configs (id, mac_identifier, release_id, label, order_numbers, bto_only, cpu, memory, storage,
		display, notes, sources, uncertain, ord, coverage_excluded) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cfg.ID, m.Identifier, r.ID, cfg.Label, js(cfg.OrderNumbers), b2i(cfg.BTOOnly), js(cfg.CPU), js(cfg.Memory),
		js(cfg.Storage), display, cfg.Notes, js(cfg.Sources), js(cfg.Uncertain), ord, c.CoverageExclusion(m, r))
	for _, a := range cfg.Aliases {
		w.exec("INSERT INTO config_aliases (alias, config_id) VALUES (?, ?)", a, cfg.ID)
	}
	for i, id := range cfg.Components {
		w.exec("INSERT INTO config_components (config_id, component_id, bto, ord) VALUES (?, ?, 0, ?)", cfg.ID, id, i)
	}
	for i, id := range cfg.BTOComponents {
		w.exec("INSERT INTO config_components (config_id, component_id, bto, ord) VALUES (?, ?, 1, ?)", cfg.ID, id, i)
	}
	for _, p := range sortedKeys(cfg.Ports) {
		w.exec("INSERT INTO config_ports (config_id, port, count) VALUES (?, ?, ?)", cfg.ID, p, cfg.Ports[p])
	}
	for _, f := range cfg.Features {
		w.exec("INSERT INTO config_features (config_id, feature) VALUES (?, ?)", cfg.ID, f)
	}
	for _, cp := range c.Applicable(m, cfg) {
		w.exec("INSERT INTO config_capabilities (config_id, capability_id) VALUES (?, ?)", cfg.ID, cp.ID)
	}
}
