package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/internal/testdb"

	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// The moderation CLI end to end: import (pending, with a preview), list,
// accept, show, refused moves, retract, and validation errors. Reports are
// named by their code; their number works too.
func TestReportsCLI(t *testing.T) {
	db := testdb.Path(t) // a copy of a database with the catalog already synced
	fixture := filepath.Join("..", "..", "internal", "results", "fixtures", "mbp152-synthetic.yaml")
	t.Setenv("SUDO_USER", "carl")
	run := func(args ...string) (int, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := run(append(args[:1:1], append([]string{args[1], "-db", db}, args[2:]...)...), &out, &errb)
		return code, out.String() + errb.String()
	}

	code, out := run("reports", "import", fixture)
	m := regexp.MustCompile(`report ([0-9a-f]{10}) · pending · MacBookPro15,2`).FindStringSubmatch(out)
	if code != 0 || m == nil {
		t.Fatalf("import: %d\n%s", code, out)
	}
	rc := m[1]
	for _, w := range []string{"Untested (0/30 tested)", "→ Partial (27/30 tested), blocked by Graphics → External display output", "reports accept " + rc} {
		if !strings.Contains(out, w) {
			t.Errorf("import output lacks %q\n%s", w, out)
		}
	}

	steps := []struct {
		args []string
		code int
		want []string
	}{
		{[]string{"reports", "list", "-state", "pending"}, 0, []string{rc, "pending", "MacBookPro15,2", "2026-09-30 18:05 UTC", "@synthetic-fixture"}},
		{[]string{"reports", "accept", rc}, 0, []string{"report " + rc + " accepted"}},
		{[]string{"reports", "accept", rc}, 1, []string{"can't become accepted"}},
		{[]string{"reports", "show", "1"}, 0, []string{"report " + rc + " · accepted", "tested 2026-09-30 18:05 UTC · submitted", "[redacted]", "accepted       carl"}},
		{[]string{"reports", "retract", rc}, 1, []string{"a reason is required"}},
		{[]string{"reports", "retract", rc, "-reason", "synthetic fixture"}, 0, []string{"report " + rc + " retracted"}},
		{[]string{"results", "show", strings.ToUpper(rc)}, 0, []string{"retracted (synthetic fixture)"}}, // the old name and upper case work
		{[]string{"reports", "show", "x"}, 2, []string{"is not a report code"}},
		{[]string{"reports", "show", "0123456789"}, 2, []string{"not found"}},
		{[]string{"reports", "show", "99"}, 1, []string{"not found"}},
		{[]string{"reports", "flags"}, 0, []string{"no flags"}},
		{[]string{"reports", "frobnicate"}, 2, []string{"unknown command"}},
		{[]string{"sources", "list"}, 0, []string{"manual", "Manual entry"}},
	}
	for _, s := range steps {
		code, out := run(s.args...)
		if code != s.code {
			t.Fatalf("%v: exit %d, want %d\n%s", s.args, code, s.code, out)
		}
		for _, w := range s.want {
			if !strings.Contains(out, w) {
				t.Errorf("%v: output lacks %q\n%s", s.args, w, out)
			}
		}
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	os.WriteFile(bad, []byte("schema: x\nconfig: nope\n"), 0o644)
	code, out = run("reports", "import", bad)
	if code != 1 || !strings.Contains(out, "schema:") || !strings.Contains(out, "config:") {
		t.Errorf("an invalid file lists every problem: %d\n%s", code, out)
	}
	if code, out := run("reports", "help"); code != 0 || !strings.Contains(out, "reports import") {
		t.Errorf("help: %d %s", code, out)
	}
}

// Sources, maintainers, and accepting an ambiguous report with -config.
func TestSourcesCLI(t *testing.T) {
	dir := t.TempDir()
	db := testdb.Path(t) // a copy of a database with the catalog already synced
	t.Setenv("SUDO_USER", "carl")
	run := func(args ...string) (int, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := run(append(args[:1:1], append([]string{args[1], "-db", db}, args[2:]...)...), &out, &errb)
		return code, out.String() + errb.String()
	}
	keyRe := regexp.MustCompile(`doi_[0-9a-f]{32}`)
	code, out := run("sources", "add", "omacdiag", "-name", "OmacDiag", "-homepage", "https://example.com")
	key := keyRe.FindString(out)
	if code != 0 || key == "" {
		t.Fatalf("add: %d\n%s", code, out)
	}
	code, out = run("sources", "rotate", "omacdiag")
	if code != 0 || keyRe.FindString(out) == "" || keyRe.FindString(out) == key {
		t.Fatalf("rotate: %d\n%s", code, out)
	}
	ambiguous := filepath.Join(dir, "amb.yaml")
	os.WriteFile(ambiguous, []byte(`schema: doesitomarchy/report/v1
identifier: MacBookPro8,2
hardware: { pci: ["1002:6741"] }
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4" }
items: { boot.install: { status: supported, method: observed } }
`), 0o644)
	code, out = run("reports", "import", ambiguous)
	m := regexp.MustCompile(`report ([0-9a-f]{10})`).FindStringSubmatch(out)
	if code != 0 || m == nil {
		t.Fatalf("import: %d\n%s", code, out)
	}
	rc := m[1]
	for _, s := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"sources", "add", "manual", "-name", "x"}, 1, "reserved"},
		{[]string{"sources", "add", "Bad ID", "-name", "x"}, 1, "lower-case"},
		{[]string{"sources", "add", "crh-DevTestTool", "-name", "Dev"}, 0, `"crh-DevTestTool" → "crh-devtesttool"`},
		{[]string{"sources", "trust", "CRH-devtesttool", "trusted"}, 0, "crh-devtesttool is trusted"},
		{[]string{"sources", "add", "nameless"}, 2, "give -name"},
		{[]string{"sources", "trust", "omacdiag", "trusted"}, 0, "omacdiag is trusted"},
		{[]string{"sources", "trust", "omacdiag", "maybe"}, 1, "pending or trusted"},
		{[]string{"sources", "revoke", "omacdiag"}, 0, "revoked"},
		{[]string{"sources", "list"}, 0, "https://example.com"},
		{[]string{"sources", "frob"}, 2, "unknown command"},
		{[]string{"maintainers", "add", "carl", "carl@example.com"}, 0, "maintainer carl added"},
		{[]string{"maintainers", "list"}, 0, "carl@example.com"},
		{[]string{"maintainers", "remove", "carl"}, 0, "removed"},
		{[]string{"maintainers", "add", "carl"}, 2, "wrong"},
		{[]string{"reports", "show", rc}, 0, "pick one: doiomad reports accept " + rc + " -config ID"},
		{[]string{"reports", "accept", rc}, 1, "pick one first"},
		{[]string{"reports", "accept", rc, "-config", "macbookpro7-1-13-mid-2010-a"}, 1, ""},
		{[]string{"reports", "accept", rc, "-config", "macbookpro8-2-15-late-2011-a"}, 0, "report " + rc + " accepted"},
	} {
		code, out := run(s.args...)
		if code != s.code || !strings.Contains(out, s.want) {
			t.Errorf("%v: exit %d, want %d and %q\n%s", s.args, code, s.code, s.want, out)
		}
	}
}

func TestSharesCLI(t *testing.T) {
	db := testdb.Path(t) // a copy of a database with the catalog already synced
	t.Setenv("SUDO_USER", "carl")
	run := func(args ...string) (int, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := run(append(args[:1:1], append([]string{args[1], "-db", db}, args[2:]...)...), &out, &errb)
		return code, out.String() + errb.String()
	}
	if code, out := run("shares", "list"); code != 0 || !strings.Contains(out, "No new shares.") {
		t.Fatalf("empty list: %d\n%s", code, out)
	}
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	st.AddShare(context.Background(), store.Share{Product: "MacBookPro8,2", BoardID: "Mac-94245A3940C91C80", PCI: []string{"1002:6760", "ffff:0001"}, Modified: "no"})
	st.AddShare(context.Background(), store.Share{Product: "MacBookPro18,1"})
	st.Close()
	for _, s := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"shares", "list"}, 0, "pci=1002:6760,ffff:0001*  modified=no"},
		{[]string{"shares", "list"}, 0, "MacBookPro18,1*: 1 shares, 1 new"},
		{[]string{"shares", "review", "MacBookPro18,1"}, 0, "MacBookPro18,1: 1 marked reviewed"},
		{[]string{"shares", "list", "-all"}, 0, "reviewed"},
		{[]string{"shares", "review"}, 2, "wrong arguments"},
	} {
		code, out := run(s.args...)
		if code != s.code || !strings.Contains(out, s.want) {
			t.Errorf("%v: exit %d, want %d and %q\n%s", s.args, code, s.code, s.want, out)
		}
	}
	if _, out := run("shares", "list"); strings.Contains(out, "MacBookPro18,1") {
		t.Errorf("reviewed group still listed:\n%s", out)
	}
}

func TestImportOmacDiag(t *testing.T) {
	db := testdb.Path(t) // a copy of a database with the catalog already synced
	t.Setenv("SUDO_USER", "carl")
	var out, errb bytes.Buffer
	fixture := "../../internal/results/fixtures/omacdiag-mbp113.json"
	if code := run([]string{"reports", "import", "-db", db, "-format", "omacdiag", fixture}, &out, &errb); code == 0 || !strings.Contains(errb.String(), "-omarchy") {
		t.Fatalf("without -omarchy: exit %d\n%s", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	code := run([]string{"reports", "import", "-db", db, "-format", "omacdiag", "-omarchy", "4.0.4", "-tester", "carl", fixture}, &out, &errb)
	if code != 0 || !strings.Contains(out.String(), "macbookpro11-3-15-late-2013-a") || !strings.Contains(out.String(), "flag: driver_missing") {
		t.Fatalf("import: exit %d\n%s%s", code, out.String(), errb.String())
	}
}
