package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Counts are catalog row counts (status line, tests).
type Counts struct {
	Macs, Releases, Configs, Components, Capabilities, ConfigCapabilities, HardBlockedConfigs int
}

// CatalogCounts counts the synced catalog.
func (s *Store) CatalogCounts(ctx context.Context) (Counts, error) {
	var c Counts
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM macs),
		(SELECT count(*) FROM releases),
		(SELECT count(*) FROM configs),
		(SELECT count(*) FROM components),
		(SELECT count(*) FROM capabilities),
		(SELECT count(*) FROM config_capabilities),
		(SELECT count(*) FROM configs JOIN macs ON macs.identifier = configs.mac_identifier WHERE macs.hard_blocker != '')`).
		Scan(&c.Macs, &c.Releases, &c.Configs, &c.Components, &c.Capabilities, &c.ConfigCapabilities, &c.HardBlockedConfigs)
	return c, err
}

// ConfigSummary is what the status engine needs to know about one config.
type ConfigSummary struct {
	ID          string
	HardBlocker string // from its Mac; non-empty means Not compatible
	Applicable  int    // number of applicable capabilities
}

// ConfigSummaries lists every config in catalog order.
func (s *Store) ConfigSummaries(ctx context.Context) ([]ConfigSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, m.hard_blocker,
			(SELECT count(*) FROM config_capabilities cc WHERE cc.config_id = c.id)
		FROM configs c JOIN macs m ON m.identifier = c.mac_identifier ORDER BY c.ord`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConfigSummary
	for rows.Next() {
		var cs ConfigSummary
		if err := rows.Scan(&cs.ID, &cs.HardBlocker, &cs.Applicable); err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// Mac is one model identifier with its releases and configs, for the model page.
type Mac struct {
	Identifier    string
	Slug          string // canonical URL form, "MacBookPro5-1"
	Line          string
	LineName      string
	EFI           int
	SecurityChip  string
	HardBlocker   string
	ResearchNotes string
	BoardIDs      []string
	Sources       []string
	Releases      []Release
}

type Release struct {
	ID, Name, Announced, Discontinued string
	ModelNumbers                      []string
	Configs                           []Config
}

type Config struct {
	ID           string
	Label        string
	BTOOnly      bool
	OrderNumbers []string
	Summary      ConfigSummary
}

// NormalizeSlug maps any accepted spelling of an identifier ("MacBookPro5,1",
// "macbookpro5-1") to the lower-case lookup key.
func NormalizeSlug(s string) string { return strings.ToLower(strings.ReplaceAll(s, ",", "-")) }

// MacBySlug finds a Mac by identifier or URL slug, case-insensitively.
func (s *Store) MacBySlug(ctx context.Context, slug string) (*Mac, error) {
	m := &Mac{}
	var boards, sources string
	err := s.db.QueryRowContext(ctx, `SELECT m.identifier, m.slug, m.line, coalesce(v.name, m.line), m.efi, m.security_chip,
			m.hard_blocker, m.research_notes, m.board_ids, m.sources
		FROM macs m LEFT JOIN vocab v ON v.kind = 'lines' AND v.key = m.line
		WHERE m.slug_lc = ?`, NormalizeSlug(slug)).
		Scan(&m.Identifier, &m.Slug, &m.Line, &m.LineName, &m.EFI, &m.SecurityChip, &m.HardBlocker, &m.ResearchNotes, &boards, &sources)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := unjson(boards, &m.BoardIDs); err != nil {
		return nil, err
	}
	if err := unjson(sources, &m.Sources); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.name, r.announced, r.discontinued, r.model_numbers,
			c.id, c.label, c.bto_only, c.order_numbers,
			(SELECT count(*) FROM config_capabilities cc WHERE cc.config_id = c.id)
		FROM releases r JOIN configs c ON c.mac_identifier = r.mac_identifier AND c.release_id = r.id
		WHERE r.mac_identifier = ? ORDER BY r.ord, c.ord`, m.Identifier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Release
		var c Config
		var models, orders string
		if err := rows.Scan(&r.ID, &r.Name, &r.Announced, &r.Discontinued, &models,
			&c.ID, &c.Label, &c.BTOOnly, &orders, &c.Summary.Applicable); err != nil {
			return nil, err
		}
		c.Summary.ID, c.Summary.HardBlocker = c.ID, m.HardBlocker
		if err := unjson(orders, &c.OrderNumbers); err != nil {
			return nil, err
		}
		if n := len(m.Releases); n == 0 || m.Releases[n-1].ID != r.ID {
			if err := unjson(models, &r.ModelNumbers); err != nil {
				return nil, err
			}
			m.Releases = append(m.Releases, r)
		}
		last := &m.Releases[len(m.Releases)-1]
		last.Configs = append(last.Configs, c)
	}
	return m, rows.Err()
}

func unjson(s string, v any) error { return json.Unmarshal([]byte(s), v) }
