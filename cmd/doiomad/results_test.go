package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The moderation CLI end to end: import (pending, with a preview), list,
// accept, show, refused moves, retract, and validation errors.
func TestResultsCLI(t *testing.T) {
	db := filepath.Join(t.TempDir(), "r.db")
	fixture := filepath.Join("..", "..", "internal", "results", "fixtures", "mbp152-synthetic.yaml")
	t.Setenv("SUDO_USER", "carl")
	run := func(args ...string) (int, string) {
		t.Helper()
		var out, errb bytes.Buffer
		code := run(append(args[:1:1], append([]string{args[1], "-db", db}, args[2:]...)...), &out, &errb)
		return code, out.String() + errb.String()
	}
	steps := []struct {
		args []string
		code int
		want []string
	}{
		{[]string{"results", "import", fixture}, 0, []string{"result 1 · pending · MacBookPro15,2", "Untested (0/30 tested)", "→ Partial (27/30 tested), blocked by Graphics → External display output", "results accept 1"}},
		{[]string{"results", "list", "-state", "pending"}, 0, []string{"1", "pending", "MacBookPro15,2", "@synthetic-fixture"}},
		{[]string{"results", "accept", "1"}, 0, []string{"result 1 accepted"}},
		{[]string{"results", "accept", "1"}, 1, []string{"can't become accepted"}},
		{[]string{"results", "show", "#1"}, 0, []string{"result 1 · accepted", "Omarchy 4.0.4", "[redacted]", "accepted       carl"}},
		{[]string{"results", "retract", "1"}, 1, []string{"a reason is required"}},
		{[]string{"results", "retract", "1", "-reason", "synthetic fixture"}, 0, []string{"result 1 retracted"}},
		{[]string{"results", "show", "1"}, 0, []string{"retracted (synthetic fixture)"}},
		{[]string{"results", "show", "x"}, 2, []string{"is not an ID"}},
		{[]string{"results", "show", "99"}, 1, []string{"not found"}},
		{[]string{"results", "flags"}, 0, []string{"no flags"}},
		{[]string{"results", "frobnicate"}, 2, []string{"unknown command"}},
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
	code, out := run("results", "import", bad)
	if code != 1 || !strings.Contains(out, "schema:") || !strings.Contains(out, "config:") {
		t.Errorf("an invalid file lists every problem: %d\n%s", code, out)
	}
	if code, out := run("results", "help"); code != 0 || !strings.Contains(out, "results import") {
		t.Errorf("help: %d %s", code, out)
	}
}
