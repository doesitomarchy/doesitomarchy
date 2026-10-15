package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/internal/store"
	"github.com/doesitomarchy/doesitomarchy/internal/testdb"
)

// The forced command allows its five verbs with exactly their arguments,
// and nothing else.
func TestParseReviewerCommand(t *testing.T) {
	allow := []struct {
		line string
		want reviewerRequest
	}{
		{"list", reviewerRequest{Verb: "list"}},
		{"  show 0123456789 ", reviewerRequest{Verb: "show", Code: "0123456789"}},
		{"check ABCDEF0123", reviewerRequest{Verb: "check", Code: "abcdef0123"}},
		{`accept 0123456789 --note "fits; doesn't conflict"`, reviewerRequest{"accept", "0123456789", "fits; doesn't conflict"}},
		{"accept 0123456789 -note it's fine $(rm -rf /) `x`", reviewerRequest{"accept", "0123456789", "it's fine $(rm -rf /) `x`"}},
		{"hold 0123456789 --note 'a\nsecond line'", reviewerRequest{"hold", "0123456789", "a\nsecond line"}},
	}
	for _, c := range allow {
		got, err := parseReviewerCommand(c.line)
		if err != nil || got != c.want {
			t.Errorf("%q: %+v %v, want %+v", c.line, got, err, c.want)
		}
	}
	deny := []string{
		"", "   ", "help", "reject 0123456789 --note no", "retract 0123456789 --note no", "resolve 3 --note x", "import -",
		"sh", "bash -c id", "list -state accepted", "list; id", "show", "show 12", "show 0123456789 extra",
		"show ../../etc/passwd", "show 0123456789;id", "check 012345678g", "accept 0123456789", "accept 0123456789 --note",
		"accept 0123456789 --note \"\"", "accept 0123456789 note x", "accept 0123456789 --by owner --note x",
		"hold 0123456789 --note x\x00y", "hold 0123456789 --note \x1b[2J", "accept --note x 0123456789",
		"hold 0123456789 --note " + strings.Repeat("x", maxReviewerCommand),
	}
	for _, line := range deny {
		if got, err := parseReviewerCommand(line); err == nil {
			t.Errorf("%q allowed: %+v", line, got)
		}
	}
}

// The reviewer end to end on a stored OmaBoot? report: list, show, check,
// accept in shadow mode (noted, still pending), hold, a refused accept
// when a gate holds, and the accept once shadow mode is off. The admin CLI
// shows the notes and runs the same gates.
func TestReviewerCommand(t *testing.T) {
	db := testdb.Path(t)
	t.Setenv("SUDO_USER", "carl")
	t.Setenv("DOIOMAD_REVIEWER_SHADOW", "")
	reviewer := func(line string) (int, string) {
		t.Helper()
		t.Setenv("SSH_ORIGINAL_COMMAND", line)
		var out, errb bytes.Buffer
		code := run([]string{"reviewer", "-db", db}, &out, &errb)
		return code, out.String() + errb.String()
	}
	admin := func(args ...string) (int, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := run(append([]string{args[0], args[1], "-db", db}, args[2:]...), &out, &errb)
		return code, out.String() + errb.String()
	}

	report := filepath.Join(t.TempDir(), "live.yaml")
	os.WriteFile(report, []byte(`schema: doesitomarchy/report/v1
identifier: MacBookAir5,2
context: live
source: { id: boot-live, version: 0.7.2 }
hardware: { board_id: Mac-2E6FAB96566FE58C, pci: ["8086:0166", "14e4:4353"] }
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4", image: "omaboot-live dev 0b8738f" }
items:
  boot.install: { status: not_tested, reason: live-limit }
  display.brightness: { status: supported, method: challenge, evidence: picked pizza }
`), 0o644)
	code, out := admin("reports", "import", report)
	m := regexp.MustCompile(`report ([0-9a-f]{10}) · pending`).FindStringSubmatch(out)
	if code != 0 || m == nil {
		t.Fatalf("import: %d\n%s", code, out)
	}
	rc := m[1]

	if code, out := reviewer("list"); code != 0 || !strings.Contains(out, `"code": "`+rc+`"`) || !strings.Contains(out, `"source": "boot-live"`) {
		t.Fatalf("list: %d\n%s", code, out)
	}
	code, out = reviewer("show " + rc)
	var shown reviewerShowJSON
	if err := json.Unmarshal([]byte(out), &shown); code != 0 || err != nil || shown.Report.Code != rc || len(shown.Report.Items) != 2 ||
		shown.Raw.Body == "" || shown.Effect == nil || shown.ConfigAccepted.Reports == nil {
		t.Fatalf("show: %d %v\n%s", code, err, out)
	}
	if strings.Contains(out, "contact") {
		t.Error("show includes the contact hash")
	}
	// The source isn't trusted yet: the gates hold, and accept refuses.
	if code, out := reviewer("check " + rc); code != 0 || !strings.Contains(out, `"verdict": "hold"`) || !strings.Contains(out, "not trusted") {
		t.Fatalf("check: %d\n%s", code, out)
	}
	if code, out := reviewer("accept " + rc + ` --note "looks right"`); code != 1 || !strings.Contains(out, "a gate holds") {
		t.Fatalf("accept on a hold: %d\n%s", code, out)
	}
	if code, out := admin("reports", "accept", rc, "-by", "reviewer", "-note", "looks right"); code != 1 || !strings.Contains(out, "source: source \"boot-live\" is pending, not trusted") {
		t.Fatalf("CLI accept -by reviewer on a hold: %d\n%s", code, out)
	}
	if code, out := admin("reports", "check", rc); code != 0 || !strings.HasPrefix(out, "hold\n  source: ") {
		t.Fatalf("CLI check: %d\n%s", code, out)
	}
	if code, out := admin("sources", "trust", "boot-live", "trusted"); code != 0 {
		t.Fatalf("trust: %d\n%s", code, out)
	}
	if code, out := admin("reports", "check", rc, "-json"); code != 0 || !strings.Contains(out, `"verdict": "pass"`) {
		t.Fatalf("CLI check -json: %d\n%s", code, out)
	}

	// A person acting for the reviewer from the CLI: shadow mode applies too,
	// and the history names the person.
	if code, out := admin("reports", "accept", rc, "-by", "reviewer", "-note", "checked by hand"); code != 0 || !strings.Contains(out, "would be accepted (shadow mode") {
		t.Fatalf("CLI shadow accept -by reviewer: %d\n%s", code, out)
	}
	if code, out := admin("reports", "show", rc); code != 0 || !strings.Contains(out, "reviewer (via carl)") || !strings.Contains(out, "· pending") {
		t.Fatalf("after a CLI shadow accept: %d\n%s", code, out)
	}

	// Shadow mode (the default): recorded as a note, still pending.
	if code, out := reviewer("accept " + rc + " --note fits the evidence policy"); code != 0 || !strings.Contains(out, "would accept (shadow mode") {
		t.Fatalf("shadow accept: %d\n%s", code, out)
	}
	if code, out := admin("reports", "show", rc); code != 0 || !strings.Contains(out, "· pending") ||
		!strings.Contains(out, "review note (would-accept, reviewer") || !strings.Contains(out, "would accept: fits the evidence policy") {
		t.Fatalf("after a shadow accept: %d\n%s", code, out)
	}
	if code, out := reviewer("hold " + rc + ` --note "the owner should look at the display result"`); code != 0 || !strings.Contains(out, `"outcome": "held"`) {
		t.Fatalf("hold: %d\n%s", code, out)
	}
	if code, out := reviewer("list"); code != 0 || !strings.Contains(out, `"note": "the owner should look at the display result"`) {
		t.Fatalf("list after hold: %d\n%s", code, out)
	}
	for _, line := range []string{"reject " + rc + " --note no", "list -state accepted", "show " + rc + "; id"} {
		if code, _ := reviewer(line); code != 2 {
			t.Errorf("%q: exit %d, want 2", line, code)
		}
	}
	// The bug hunt's key: reads only, refused before the database is opened.
	readOnly := func(line string) (int, string) {
		t.Helper()
		t.Setenv("SSH_ORIGINAL_COMMAND", line)
		var out, errb bytes.Buffer
		code := run([]string{"reviewer", "-read-only", "-db", db}, &out, &errb)
		return code, out.String() + errb.String()
	}
	if code, out := readOnly("check " + rc); code != 0 || !strings.Contains(out, `"verdict"`) {
		t.Errorf("read-only check: %d\n%s", code, out)
	}
	for _, line := range []string{"accept " + rc + " --note ok", "hold " + rc + " --note why"} {
		if code, out := readOnly(line); code != 2 || !strings.Contains(out, "read-only") {
			t.Errorf("read-only %q: %d\n%s", line, code, out)
		}
	}
	if code, out := admin("reports", "reject", rc, "-by", "reviewer", "-reason", "x"); code != 2 || !strings.Contains(out, "never rejects") {
		t.Errorf("CLI reject -by reviewer: %d\n%s", code, out)
	}
	if code, out := admin("reports", "hold", rc, "-by", "someone"); code != 2 {
		t.Errorf("CLI -by someone: %d\n%s", code, out)
	}

	// Shadow mode off: accepted, by the reviewer.
	t.Setenv("DOIOMAD_REVIEWER_SHADOW", "0")
	if code, out := reviewer("accept " + rc + " --note fits the evidence policy"); code != 0 || !strings.Contains(out, `"outcome": "accepted"`) {
		t.Fatalf("accept: %d\n%s", code, out)
	}
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, _ := st.ResultIDByCode(context.Background(), rc)
	d, _ := st.Result(context.Background(), id)
	if d.State != store.Accepted || !d.ByReviewer() || d.Review.Note != "fits the evidence policy" {
		t.Fatalf("accepted: %s by %s, %+v", d.State, d.StateBy, d.Review)
	}
}
