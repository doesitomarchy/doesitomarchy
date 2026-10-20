package review

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
	"github.com/doesitomarchy/doesitomarchy/internal/testdb"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testdb.Cleanup()
	os.Exit(code)
}

// passing is a report every gate passes.
func passing() *Input {
	return &Input{
		R: &store.ResultDetail{
			ResultSummary: store.ResultSummary{Code: "0123456789", State: store.Pending, SourceID: OmaBootSource},
			Hardware:      `{"board_id": "Mac-2E6FAB96566FE58C", "pci": ["8086:0166", "14e4:4353"]}`,
			Items: []store.ResultItem{
				{Capability: "display.brightness", Status: "supported", Method: "challenge", Evidence: "picked pizza"},
				{Capability: "graphics.acceleration", Status: "partial", Method: "automatic", Evidence: "glmark2 412"},
				{Capability: "audio.speakers", Status: "supported", Method: "observed", Note: "heard the chime"},
				{Capability: "boot.install", Status: "not_tested", Reason: "live-limit"},
				{Capability: "wifi.connect", Status: "not_tested", Reason: "other", Note: "no network nearby"},
			},
		},
		Raw:     "schema: doesitomarchy/report/v1\n",
		Source:  &store.Source{ID: OmaBootSource, Trust: "trusted"},
		SameMac: 1,
	}
}

// Each gate passes the passing report, and holds on the one change that
// breaks it, alone.
func TestGates(t *testing.T) {
	if v := Evaluate(passing()); !v.Pass() || len(v.Reasons) != 0 || len(v.Gates) != len(Gates) {
		t.Fatalf("passing report: %+v", v)
	}
	item := func(in *Input, i int) *store.ResultItem { return &in.R.Items[i] }
	cases := []struct {
		gate, want string
		break_     func(in *Input)
	}{
		{"pending", "the report is accepted", func(in *Input) { in.R.State = store.Accepted }},
		{"omaboot", "not an OmaBoot? report: for the owner", func(in *Input) { in.R.SourceID = "omacdiag"; in.Source.ID = "omacdiag" }},
		{"source", "not registered", func(in *Input) { in.Source = nil }},
		{"source", "was revoked", func(in *Input) { in.Source.RevokedAt = "2026-10-01T00:00:00Z" }},
		{"source", "pending, not trusted", func(in *Input) { in.Source.Trust = "pending" }},
		{"one-config", "fits 2 configurations", func(in *Input) { in.R.Candidates = []string{"a", "b"} }},
		{"one-config", "a maintainer picked it", func(in *Input) {
			in.R.Flags = []store.ResultFlag{{ID: 3, Kind: results.FlagAmbiguous, ResolvedAt: "2026-10-01T00:00:00Z"}}
		}},
		{"no-open-flags", "open flags: port_suspect (flag 4)", func(in *Input) { in.R.Flags = []store.ResultFlag{{ID: 4, Kind: results.FlagPortSuspect}} }},
		{"no-console-changes", "force display.native-resolution pass", func(in *Input) {
			in.R.Console = []results.ConsoleEntry{{Command: "rerun graphics.acceleration", Kind: "redo"}, {Command: "force display.native-resolution pass", Kind: "change"}}
		}},
		{"not-vm", "virtual machine", func(in *Input) { in.R.Hardware = `{"pci": ["1234:1111", "1af4:1000"]}` }},
		{"not-duplicate", "duplicates report abcdef0123", func(in *Input) { in.Duplicate = "abcdef0123" }},
		{"per-mac-limit", "6 reports from this Mac", func(in *Input) { in.SameMac = results.ReportsPerDayPerMac + 1 }},
		{"nothing-scrubbed", "in: raw report", func(in *Input) { in.Raw = "hostname: " + results.Redacted }},
		{"nothing-scrubbed", "in: graphics.acceleration", func(in *Input) { item(in, 1).Evidence = "ip " + results.Redacted }},
		{"nothing-scrubbed", "in: hardware", func(in *Input) { in.R.Hardware = `{"owner": "[redacted]"}` }},
		{"evidence", "graphics.acceleration: automatic without evidence", func(in *Input) { item(in, 1).Evidence = " " }},
		{"evidence", "display.brightness: challenge without evidence", func(in *Input) { item(in, 0).Evidence = "" }},
		{"evidence", "audio.speakers: observed without evidence or a note", func(in *Input) { item(in, 2).Note = "" }},
		{"evidence", "a fixture result", func(in *Input) { item(in, 2).Method = "fixture" }},
		{"evidence", "partial without a method", func(in *Input) { item(in, 1).Method = "" }},
		{"evidence", "boot.install: not tested, without a reason", func(in *Input) { item(in, 3).Reason = "" }},
		{"evidence", "unexplained other reason", func(in *Input) { item(in, 4).Note = "" }},
	}
	for _, c := range cases {
		in := passing()
		c.break_(in)
		v := Evaluate(in)
		if v.Pass() || len(v.Reasons) != 1 || !strings.HasPrefix(v.Reasons[0], c.gate+": ") || !strings.Contains(v.Reasons[0], c.want) {
			t.Errorf("%s (%q): %+v", c.gate, c.want, v.Reasons)
		}
	}
}

// insertLive stores an OmaBoot? report as boot-live would send it, and returns its ID.
func insertLive(t *testing.T, st *store.Store, extra string) int64 {
	t.Helper()
	c, _ := testdb.Catalog(t)
	raw := []byte(`schema: doesitomarchy/report/v1
identifier: MacBookAir5,2
context: live
source: { id: boot-live, version: 0.7.2 }
hardware: { board_id: Mac-2E6FAB96566FE58C, pci: ["8086:0166", "14e4:4353"] }
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4", image: "omaboot-live dev 0b8738f" }
` + extra + `items:
  boot.install: { status: not_tested, reason: live-limit }
  display.brightness: { status: supported, method: challenge, evidence: picked pizza }
`)
	f, err := results.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	r, err := results.Validate(f, c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.InsertResult(context.Background(), r, raw, results.SchemaV1, "source:boot-live")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// The gates read a stored report: the source's standing, duplicates and the
// per-Mac count come from the database. Accept refuses a held report, shadow
// mode only notes, and the newest note wins.
func TestAcceptStored(t *testing.T) {
	ctx := context.Background()
	st, _ := testdb.Open(t)
	if err := st.EnsureSource(ctx, OmaBootSource, "OmaBoot? Live", "https://omaboot.live"); err != nil {
		t.Fatal(err)
	}
	id := insertLive(t, st, "")
	v, err := Check(ctx, st, id)
	if err != nil || v.Pass() || len(v.Reasons) != 1 || !strings.Contains(v.Reasons[0], "not trusted") {
		t.Fatalf("untrusted source: %+v %v", v, err)
	}
	if _, out, err := Accept(ctx, st, id, "looks right", store.Reviewer, false); !errors.Is(err, ErrHold) || out != NotAccepted {
		t.Fatalf("accept on a held report: %v %v", out, err)
	}
	if d, _ := st.Result(ctx, id); d.State != store.Pending {
		t.Fatalf("held report became %s", d.State)
	}
	if err := st.SetSourceTrust(ctx, OmaBootSource, "trusted"); err != nil {
		t.Fatal(err)
	}
	if v, err := Check(ctx, st, id); err != nil || !v.Pass() {
		t.Fatalf("trusted: %+v %v", v, err)
	}

	// A second report from the same tester and time duplicates the first.
	dup := insertLive(t, st, "")
	if v, _ := Check(ctx, st, dup); v.Pass() || !strings.Contains(strings.Join(v.Reasons, "\n"), "not-duplicate: duplicates report") {
		t.Errorf("duplicate: %+v", v)
	}
	if n, err := st.SameMacReports(ctx, dup, PerMacWindow); err != nil || n != 2 {
		t.Errorf("same Mac: %d %v", n, err)
	}

	// Shadow mode: noted, still pending, nothing counts.
	if _, out, err := Accept(ctx, st, id, "matches the accepted iMac results", store.Reviewer, true); err != nil || out != WouldAccept {
		t.Fatalf("shadow accept: %v %v", out, err)
	}
	d, _ := st.Result(ctx, id)
	if d.State != store.Pending || d.Review.Action != store.ActionWouldAccept || d.Review.Note != "would accept: matches the accepted iMac results" ||
		d.Review.By != store.Reviewer {
		t.Fatalf("shadow: %s %+v", d.State, d.Review)
	}
	if err := Hold(ctx, st, id, "wait for the owner", store.Reviewer); err != nil {
		t.Fatal(err)
	}
	if err := Hold(ctx, st, id, " ", store.Reviewer); err == nil {
		t.Error("a hold without a note")
	}
	list, _ := st.ListResults(ctx, store.ResultFilter{State: store.Pending})
	if len(list) != 2 || list[1].ID != id || list[1].Review.Action != store.ActionHeld || list[1].Review.Note != "wait for the owner" {
		t.Fatalf("the newest note wins: %+v", list)
	}
	if _, _, err := Accept(ctx, st, id, "", store.Reviewer, false); err == nil {
		t.Error("an accept without a note")
	}

	// Out of shadow mode: accepted, by the reviewer, with its note.
	if _, out, err := Accept(ctx, st, id, "fits the evidence policy", store.Reviewer, false); err != nil || out != Accepted {
		t.Fatalf("accept: %v %v", out, err)
	}
	d, _ = st.Result(ctx, id)
	if d.State != store.Accepted || !d.ByReviewer() || d.StateReason != "" || d.Review.Action != store.Accepted || d.Review.Note != "fits the evidence policy" {
		t.Fatalf("accepted: %s by %s %+v", d.State, d.StateBy, d.Review)
	}
	if err := Hold(ctx, st, id, "too late", store.Reviewer); !errors.Is(err, store.ErrState) {
		t.Errorf("a hold on an accepted report: %v", err)
	}
	// Accepted, it no longer passes: it isn't pending.
	if v, _ := Check(ctx, st, id); v.Pass() {
		t.Error("an accepted report passes")
	}
}

// The per-Mac count reads the stored hardware: a sixth report from one Mac
// in 24 hours holds; another Mac doesn't count.
func TestPerMacStored(t *testing.T) {
	ctx := context.Background()
	st, _ := testdb.Open(t)
	st.EnsureSource(ctx, OmaBootSource, "OmaBoot? Live", "https://omaboot.live")
	st.SetSourceTrust(ctx, OmaBootSource, "trusted")
	var last int64
	for i := 0; i <= results.ReportsPerDayPerMac; i++ {
		last = insertLive(t, st, "tester: { handle: t"+string(rune('a'+i))+" }\n") // other testers: not duplicates
	}
	v, err := Check(ctx, st, last)
	if err != nil || v.Pass() || len(v.Reasons) != 1 || !strings.HasPrefix(v.Reasons[0], "per-mac-limit: 6 reports") {
		t.Fatalf("sixth report: %+v %v", v, err)
	}
	if v, _ := Check(ctx, st, last-1); !v.Pass() {
		t.Errorf("fifth report: %+v", v)
	}
}
