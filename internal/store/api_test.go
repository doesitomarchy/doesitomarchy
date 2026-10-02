package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/results"
)

func TestSourceKeys(t *testing.T) {
	ctx := context.Background()
	st, _ := synced(t)
	if _, err := st.AddSource(ctx, "manual", "x", ""); err == nil {
		t.Error("manual is reserved")
	}
	if _, err := st.AddSource(ctx, "Bad ID", "x", ""); err == nil {
		t.Error("source IDs are lower-case slugs")
	}
	key, err := st.AddSource(ctx, "omacdiag", "OmacDiag", "https://example.com/omacdiag")
	if err != nil || !strings.HasPrefix(key, KeyPrefix) || len(key) != len(KeyPrefix)+32 {
		t.Fatalf("key %q: %v", key, err)
	}
	if _, err := st.AddSource(ctx, "omacdiag", "again", ""); err == nil {
		t.Error("duplicate source")
	}
	var stored string
	st.db.QueryRowContext(ctx, "SELECT key_hash FROM sources WHERE id = 'omacdiag'").Scan(&stored)
	if stored == "" || strings.Contains(stored, key[len(KeyPrefix):]) {
		t.Error("only the key's hash is stored")
	}
	if src, err := st.SourceByKey(ctx, key); err != nil || src.ID != "omacdiag" || src.Trust != "pending" {
		t.Fatalf("by key: %+v %v", src, err)
	}
	for _, bad := range []string{"", "doi_", "doi_00000000000000000000000000000000", "nokey"} {
		if _, err := st.SourceByKey(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("key %q: %v", bad, err)
		}
	}
	key2, err := st.RotateSourceKey(ctx, "omacdiag")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SourceByKey(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Error("the old key stops working after rotation")
	}
	if err := st.SetSourceTrust(ctx, "omacdiag", "trusted"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSourceTrust(ctx, "omacdiag", "sorta"); err == nil {
		t.Error("trust is pending or trusted")
	}
	if err := st.RevokeSource(ctx, "omacdiag"); err != nil {
		t.Fatal(err)
	}
	if src, err := st.SourceByKey(ctx, key2); !errors.Is(err, ErrRevoked) || src == nil {
		t.Errorf("revoked: %v", err)
	}
	if _, err := st.RotateSourceKey(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Error("unknown source")
	}
}

func TestMaintainers(t *testing.T) {
	ctx := context.Background()
	st, _ := synced(t)
	if err := st.AddMaintainer(ctx, "Carl", "carl@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMaintainer(ctx, "x", "not-an-email"); err == nil {
		t.Error("needs an e-mail address")
	}
	if m, err := st.MaintainerByEmail(ctx, "CARL@example.com"); err != nil || m.Handle != "carl" {
		t.Fatalf("by e-mail (any case): %+v %v", m, err)
	}
	if err := st.RemoveMaintainer(ctx, "carl"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MaintainerByEmail(ctx, "carl@example.com"); !errors.Is(err, ErrNotFound) {
		t.Error("a removed maintainer has no access")
	}
	if err := st.AddMaintainer(ctx, "carl", "carl@example.com"); err != nil {
		t.Fatal("re-adding restores access")
	}
	if ms, _ := st.Maintainers(ctx); len(ms) != 1 {
		t.Errorf("maintainers: %+v", ms)
	}
}

func TestAmbiguousReport(t *testing.T) {
	ctx := context.Background()
	st, c := synced(t)
	f, _ := results.Parse([]byte(`schema: doesitomarchy/report/v1
identifier: MacBookPro8,2
hardware: { pci: ["1002:6741"] }
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4" }
items:
  boot.install: { status: supported, method: observed }
  graphics.discrete: { status: supported, method: automatic }
`))
	r, err := results.Validate(f, c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.InsertResult(ctx, r, nil, results.SchemaV1, "carl")
	if err != nil {
		t.Fatal(err)
	}
	d, _ := st.Result(ctx, id)
	if len(d.Candidates) != 2 || d.ConfigID != d.Candidates[0] {
		t.Fatalf("candidates: %v (config %s)", d.Candidates, d.ConfigID)
	}
	if err := st.SetResultState(ctx, id, Accepted, "", "carl"); !errors.Is(err, ErrState) || !strings.Contains(err.Error(), "pick one") {
		t.Fatalf("accepting an ambiguous report must be refused: %v", err)
	}
	if err := st.SetResultConfig(ctx, id, "macbookpro15-2-13-2018-4tb3-a", nil, "carl"); err == nil {
		t.Error("the configuration must belong to the same Mac")
	}
	// Pick the late-2011 config; pretend graphics.discrete doesn't apply to see flags refresh.
	if err := st.SetResultConfig(ctx, id, "macbookpro8-2-15-late-2011-a", map[string]bool{"boot.install": true}, "carl"); err != nil {
		t.Fatal(err)
	}
	d, _ = st.Result(ctx, id)
	open := 0
	for _, fl := range d.Flags {
		if fl.ResolvedAt == "" {
			open++
			if fl.Kind != "inapplicable_item" {
				t.Errorf("unexpected open flag %+v", fl)
			}
		}
	}
	if d.ConfigID != "macbookpro8-2-15-late-2011-a" || open != 1 || d.Events[len(d.Events)-1].Action != "config-set" {
		t.Fatalf("after picking: config %s, %d open flags, last event %+v", d.ConfigID, open, d.Events[len(d.Events)-1])
	}
	if err := st.SetResultState(ctx, id, Accepted, "", "carl"); err != nil {
		t.Fatalf("accept after picking: %v", err)
	}
	if err := st.SetResultConfig(ctx, id, "macbookpro8-2-15-early-2011-b", nil, "carl"); !errors.Is(err, ErrState) {
		t.Error("an accepted report's configuration is fixed")
	}
}

func TestDuplicateAndRate(t *testing.T) {
	ctx := context.Background()
	st, c := synced(t)
	r, raw := fixture(t, c)
	first, _ := st.InsertResult(ctx, r, raw, results.SchemaV1, "carl")
	second, _ := st.InsertResult(ctx, r, raw, results.SchemaV1, "carl")
	d1, _ := st.Result(ctx, first)
	d2, _ := st.Result(ctx, second)
	if len(d1.Flags) != 0 || len(d2.Flags) != 1 || d2.Flags[0].Kind != "duplicate" || !strings.Contains(d2.Flags[0].Detail, d1.Code) {
		t.Fatalf("duplicate flag: %+v / %+v", d1.Flags, d2.Flags)
	}
	if n, err := st.RecentSubmissions(ctx, "manual", time.Now().Add(-time.Hour)); err != nil || n != 2 {
		t.Errorf("recent: %d %v", n, err)
	}
	if n, _ := st.RecentSubmissions(ctx, "manual", time.Now().Add(time.Hour)); n != 0 {
		t.Errorf("nothing after now: %d", n)
	}
}
