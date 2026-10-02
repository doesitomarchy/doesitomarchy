package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
)

// synced opens a temp store with the embedded catalog synced into it.
func synced(t *testing.T) (*Store, *catalog.Catalog) {
	t.Helper()
	st := openTemp(t)
	c, h := embedded(t)
	if _, err := st.SyncCatalog(context.Background(), c, h); err != nil {
		t.Fatal(err)
	}
	return st, c
}

func fixture(t *testing.T, c *catalog.Catalog) (*results.Result, []byte) {
	t.Helper()
	raw, err := os.ReadFile("../results/fixtures/mbp152-synthetic.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f, err := results.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	r, err := results.Validate(f, c, time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return r, raw
}

func TestResultLifecycle(t *testing.T) {
	ctx := context.Background()
	st, c := synced(t)
	r, raw := fixture(t, c)
	v0, _ := st.DataVersion(ctx)

	id, err := st.InsertResult(ctx, r, raw, results.SchemaV1, "carl")
	if err != nil {
		t.Fatal(err)
	}
	// Pending: stored, but nothing counts and running servers aren't told to rebuild.
	if v, _ := st.DataVersion(ctx); v != v0 {
		t.Errorf("a pending result must not bump the data version (%d → %d)", v0, v)
	}
	ru, err := st.RollupData(ctx)
	if err != nil || len(ru.Items) != 0 || ru.Results[r.ConfigID] != 0 {
		t.Fatalf("pending results must not reach the rollup: %v %+v", err, ru)
	}

	d, err := st.Result(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != Pending || d.Identifier != "MacBookPro15,2" || d.SourceName != "Manual entry" || len(d.Items) != 30 ||
		d.Supported != 19 || d.Partial != 3 || d.Failed != 5 || d.NotTested != 3 || len(d.Extras) != 1 || len(d.Events) != 1 {
		t.Fatalf("detail: %+v", d.ResultSummary)
	}
	if d.Items[0].CategoryName != "Boot" || d.ReportVisibility != "private" || d.ReportSize == 0 {
		t.Errorf("items/report: %+v %s %d", d.Items[0], d.ReportVisibility, d.ReportSize)
	}

	// Contact is hashed; the raw report is scrubbed.
	var contact string
	st.db.QueryRowContext(ctx, "SELECT contact_hash FROM results WHERE id = ?", id).Scan(&contact)
	if len(contact) != 64 || strings.Contains(contact, "@") {
		t.Errorf("contact hash = %q", contact)
	}
	_, body, err := st.ResultReport(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"C02XG0FDH7JY", "a4:83:e7:12:34:56", "tester@example.com", "/home/carl", "192.168.1.42"} {
		if strings.Contains(body, leak) {
			t.Errorf("raw report leaks %q", leak)
		}
	}

	// Accept: counts, and the data version moves.
	if err := st.SetResultState(ctx, id, Accepted, "", "carl"); err != nil {
		t.Fatal(err)
	}
	v1, _ := st.DataVersion(ctx)
	if v1 != v0+1 {
		t.Errorf("accept should bump the data version: %d → %d", v0, v1)
	}
	ru, err = st.RollupData(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ru.Items[r.ConfigID]) != 27 || ru.Results[r.ConfigID] != 1 || ru.Latest[r.ConfigID] != "2026-09-30T18:05:00Z" ||
		ru.CurrentMajor != 4 || ru.Version != v1 || len(ru.Accepted[r.ConfigID]) != 1 {
		t.Fatalf("rollup after accept: items %d results %d latest %q major %d version %d",
			len(ru.Items[r.ConfigID]), ru.Results[r.ConfigID], ru.Latest[r.ConfigID], ru.CurrentMajor, ru.Version)
	}

	// Moves the state doesn't allow are refused.
	if err := st.SetResultState(ctx, id, Rejected, "spam", "carl"); !errors.Is(err, ErrState) {
		t.Errorf("accepted → rejected should be refused, got %v", err)
	}
	if err := st.SetResultState(ctx, id, Retracted, " ", "carl"); err == nil {
		t.Error("retracting needs a reason")
	}
	if err := st.SetResultState(ctx, 999, Accepted, "", "carl"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown result: %v", err)
	}

	// Retract: no longer counts, but the row and its history stay.
	if err := st.SetResultState(ctx, id, Retracted, "synthetic fixture", "carl"); err != nil {
		t.Fatal(err)
	}
	ru, _ = st.RollupData(ctx)
	if len(ru.Items[r.ConfigID]) != 0 || ru.Results[r.ConfigID] != 0 || len(ru.Accepted[r.ConfigID]) != 1 {
		t.Fatalf("after retract: %d items, %d results, %d listed", len(ru.Items[r.ConfigID]), ru.Results[r.ConfigID], len(ru.Accepted[r.ConfigID]))
	}
	d, _ = st.Result(ctx, id)
	if d.State != Retracted || d.StateReason != "synthetic fixture" || len(d.Events) != 3 {
		t.Fatalf("retracted detail: %s %q, %d events", d.State, d.StateReason, len(d.Events))
	}
	if list, _ := st.ListResults(ctx, ResultFilter{State: Retracted}); len(list) != 1 {
		t.Errorf("list retracted: %d", len(list))
	}
}

func TestRejectAndFlags(t *testing.T) {
	ctx := context.Background()
	st, c := synced(t)
	f, _ := results.Parse([]byte(`schema: doesitomarchy/report/v1
config: macbookpro15-2-13-2018-4tb3-a
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
	open, _ := st.Flags(ctx, true)
	if len(open) != 1 || open[0].Kind != results.FlagInapplicable || open[0].ResultID != id {
		t.Fatalf("flags: %+v", open)
	}
	if err := st.ResolveFlag(ctx, open[0].ID, "", "carl"); err == nil {
		t.Error("resolving needs a note")
	}
	if err := st.ResolveFlag(ctx, open[0].ID, "stored, not counted; fine", "carl"); err != nil {
		t.Fatal(err)
	}
	if err := st.ResolveFlag(ctx, open[0].ID, "again", "carl"); !errors.Is(err, ErrState) {
		t.Errorf("double resolve: %v", err)
	}
	if open, _ = st.Flags(ctx, true); len(open) != 0 {
		t.Errorf("still open: %+v", open)
	}
	v0, _ := st.DataVersion(ctx)
	if err := st.SetResultState(ctx, id, Rejected, "test", "carl"); err != nil {
		t.Fatal(err)
	}
	if v, _ := st.DataVersion(ctx); v != v0 {
		t.Error("rejecting a pending result changes nothing on the site")
	}
	// Inapplicable items never reach the rollup, even once accepted.
	id2, _ := st.InsertResult(ctx, r, nil, results.SchemaV1, "carl")
	st.SetResultState(ctx, id2, Accepted, "", "carl")
	ru, _ := st.RollupData(ctx)
	for _, it := range ru.Items[r.ConfigID] {
		if it.Capability == "graphics.discrete" {
			t.Error("an inapplicable item reached the rollup")
		}
	}
}

func TestUnknownOrRevokedSource(t *testing.T) {
	ctx := context.Background()
	st, c := synced(t)
	r, raw := fixture(t, c)
	r.SourceID = "omacdiag"
	if _, err := st.InsertResult(ctx, r, raw, results.SchemaV1, "carl"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Errorf("unregistered source: %v", err)
	}
	st.db.ExecContext(ctx, "UPDATE sources SET revoked_at = '2026-10-01' WHERE id = 'manual'")
	r.SourceID = "manual"
	if _, err := st.InsertResult(ctx, r, raw, results.SchemaV1, "carl"); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Errorf("revoked source: %v", err)
	}
}

func TestUnsupportedFlag(t *testing.T) {
	ctx := context.Background()
	st, c := synced(t)
	var component string
	for _, m := range c.Macs {
		if m.Identifier == "MacBookPro15,2" {
			component = m.Releases[0].Configs[0].Components[0]
		}
	}
	if err := st.SetUnsupported(ctx, "audio.speakers", "", "", "x", "carl"); err == nil {
		t.Error("needs a config or a component")
	}
	if err := st.SetUnsupported(ctx, "audio.speakers", "macbookpro15-2-13-2018-4tb3-a", "", "", "carl"); err == nil {
		t.Error("needs a reason")
	}
	if err := st.SetUnsupported(ctx, "audio.speakers", "macbookpro15-2-13-2018-4tb3-a", "", "no open driver", "carl"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUnsupported(ctx, "graphics.integrated", "", component, "no driver for this part", "carl"); err != nil {
		t.Fatal(err)
	}
	ru, err := st.RollupData(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ru.Unsupported["macbookpro15-2-13-2018-4tb3-a"]["audio.speakers"] != "no open driver" {
		t.Errorf("config-level flag: %+v", ru.Unsupported)
	}
	n := 0
	for _, caps := range ru.Unsupported {
		if caps["graphics.integrated"] != "" {
			n++
		}
	}
	if n < 2 {
		t.Errorf("a component-level flag should reach every config with %s, got %d", component, n)
	}
}

// Results survive catalog re-syncs, and a sync that would drop a config that
// results reference fails instead of deleting them.
func TestResultsSurviveSync(t *testing.T) {
	ctx := context.Background()
	st, c := synced(t)
	r, raw := fixture(t, c)
	id, _ := st.InsertResult(ctx, r, raw, results.SchemaV1, "carl")
	st.SetResultState(ctx, id, Accepted, "", "carl")

	_, h := embedded(t)
	if _, err := st.SyncCatalog(ctx, c, h+"-changed"); err != nil {
		t.Fatalf("re-sync with results present: %v", err)
	}
	if d, err := st.Result(ctx, id); err != nil || d.State != Accepted || len(d.Items) != 30 {
		t.Fatalf("result after re-sync: %v", err)
	}

	broken, _ := embedded(t)
	for _, m := range broken.Macs {
		if m.Identifier == "MacBookPro15,2" {
			m.Releases[0].Configs[0].ID = "renamed-config"
		}
	}
	if _, err := st.SyncCatalog(ctx, broken, h+"-broken"); err == nil {
		t.Fatal("a sync dropping a referenced config must fail")
	}
	if d, err := st.Result(ctx, id); err != nil || d.ConfigID != "macbookpro15-2-13-2018-4tb3-a" {
		t.Fatalf("the failed sync must leave results untouched: %v", err)
	}
}

func TestReportCodes(t *testing.T) {
	ctx := context.Background()
	st, c := synced(t)
	r, raw := fixture(t, c)
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		id, err := st.InsertResult(ctx, r, raw, results.SchemaV1, "carl")
		if err != nil {
			t.Fatal(err)
		}
		d, _ := st.Result(ctx, id)
		if !IsCode(d.Code) || seen[d.Code] {
			t.Fatalf("code %q: not a fresh 10-character hex code", d.Code)
		}
		seen[d.Code] = true
		if back, err := st.ResultIDByCode(ctx, d.Code); err != nil || back != id {
			t.Fatalf("lookup by code: %d %v", back, err)
		}
		if d.SubmittedAt == "" || d.SubmittedAt[len(d.SubmittedAt)-1] != 'Z' {
			t.Errorf("submitted_at should be UTC: %q", d.SubmittedAt)
		}
	}
	if _, err := st.ResultIDByCode(ctx, "0000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown code: %v", err)
	}
	for _, bad := range []string{"", "123", "ABCDEF1234", "abcdefghij", "0123456789a"} {
		if IsCode(bad) {
			t.Errorf("IsCode(%q)", bad)
		}
	}
}
