package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func embedded(t *testing.T) (*catalog.Catalog, string) {
	t.Helper()
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	h, err := catalog.HashFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	return c, h
}

func TestMigrations(t *testing.T) {
	ms, err := migrations()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "m.db")
	for i := 0; i < 2; i++ { // fresh, then re-open: nothing to do
		st, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		v, err := st.SchemaVersion(ctx)
		if err != nil || v != len(ms) {
			t.Fatalf("open #%d: schema version %d (%v), want %d", i, v, err, len(ms))
		}
		if major, err := st.Setting(ctx, "current_omarchy_major"); err != nil || major != "4" {
			t.Fatalf("current_omarchy_major = %q (%v)", major, err)
		}
		st.Close()
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "n.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "newer than this binary") {
		t.Fatalf("expected newer-schema error, got %v", err)
	}
}

func TestSyncRealCatalog(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	c, hash := embedded(t)

	changed, err := st.SyncCatalog(ctx, c, hash)
	if err != nil || !changed {
		t.Fatalf("first sync: changed=%v err=%v", changed, err)
	}
	got, err := st.CatalogCounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := c.Stats()
	if got.Macs != want.Macs || got.Releases != want.Releases || got.Configs != want.Configs ||
		got.Components != want.Components || got.Capabilities != want.Capabilities {
		t.Fatalf("counts %+v do not match catalog %+v", got, want)
	}
	applicable, blocked := 0, 0
	for _, m := range c.Macs {
		for ri := range m.Releases {
			for ci := range m.Releases[ri].Configs {
				applicable += len(c.Applicable(m, &m.Releases[ri].Configs[ci]))
				if m.HardBlocker != "" {
					blocked++
				}
			}
		}
	}
	if got.ConfigCapabilities != applicable || got.HardBlockedConfigs != blocked {
		t.Fatalf("applicability %d / blocked %d, want %d / %d", got.ConfigCapabilities, got.HardBlockedConfigs, applicable, blocked)
	}

	if changed, err := st.SyncCatalog(ctx, c, hash); err != nil || changed {
		t.Fatalf("re-sync of the same catalog must be a no-op: changed=%v err=%v", changed, err)
	}

	sums, err := st.ConfigSummaries(ctx)
	if err != nil || len(sums) != want.Configs {
		t.Fatalf("summaries: %d (%v)", len(sums), err)
	}
}

// A changed catalog replaces the catalog rows and leaves everything else alone.
func TestSyncChangedCatalog(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	c, hash := embedded(t)
	if _, err := st.SyncCatalog(ctx, c, hash); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "current_omarchy_major", "5"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.CopyFS(dir, data.FS); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "macs", "MacBookPro5-1.yaml")
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	const old = `label: "Core 2 Duo 2.4`
	if !strings.Contains(string(b), old) {
		t.Fatalf("fixture text %q not found", old)
	}
	if err := os.WriteFile(f, []byte(strings.Replace(string(b), old, `label: "EDITED 2.4`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	c2, err := catalog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	hash2, err := catalog.HashFS(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if hash2 == hash {
		t.Fatal("hash did not change")
	}
	if changed, err := st.SyncCatalog(ctx, c2, hash2); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	m, err := st.MacBySlug(ctx, "MacBookPro5-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(m.Releases[0].Configs[0].Label, "EDITED") {
		t.Fatalf("label not updated: %q", m.Releases[0].Configs[0].Label)
	}
	if v, _ := st.Setting(ctx, "current_omarchy_major"); v != "5" {
		t.Fatalf("sync must not touch non-catalog settings; got %q", v)
	}
	n, _ := st.CatalogCounts(ctx)
	if n.Configs != c.Stats().Configs {
		t.Fatalf("config count changed: %d", n.Configs)
	}
}

func TestMacBySlug(t *testing.T) {
	ctx := context.Background()
	st := openTemp(t)
	c, hash := embedded(t)
	if _, err := st.SyncCatalog(ctx, c, hash); err != nil {
		t.Fatal(err)
	}
	for _, in := range []string{"MacBookPro8-2", "MacBookPro8,2", "macbookpro8-2", "MACBOOKPRO8,2"} {
		m, err := st.MacBySlug(ctx, in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if m.Identifier != "MacBookPro8,2" || m.Slug != "MacBookPro8-2" || m.LineName != "MacBook Pro" {
			t.Fatalf("%s: got %s / %s / %s", in, m.Identifier, m.Slug, m.LineName)
		}
	}
	m, _ := st.MacBySlug(ctx, "MacBookPro8-2")
	if len(m.Releases) != 2 || len(m.Releases[0].Configs) != 2 || len(m.Releases[1].Configs) != 2 {
		t.Fatalf("releases/configs not grouped in order: %+v", m.Releases)
	}
	if m.Releases[0].ID != "15-early-2011" || m.Releases[0].Configs[1].ID != "macbookpro8-2-15-early-2011-b" {
		t.Fatalf("order wrong: %s / %s", m.Releases[0].ID, m.Releases[0].Configs[1].ID)
	}
	if m.Releases[0].Configs[0].Summary.Applicable == 0 || len(m.BoardIDs) == 0 || len(m.Sources) == 0 {
		t.Fatal("applicability, board IDs and sources must be populated")
	}

	y, err := st.MacBySlug(ctx, "MacBookPro1,1")
	if err != nil || y.HardBlocker == "" || y.Releases[0].Configs[0].Summary.HardBlocker == "" {
		t.Fatalf("hard blocker not carried to configs: %v", err)
	}

	if _, err := st.MacBySlug(ctx, "Nope9,9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown identifier: %v", err)
	}
}
