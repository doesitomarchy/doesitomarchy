package report

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

const valid = `schema: doesitomarchy/report/v1
config: macbookpro15-2-13-2018-4tb3-a
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4" }
items:
  boot.install: { status: supported, method: observed }
`

// The checks that need no catalog; internal/results tests the rest.
func TestCheck(t *testing.T) {
	tests := []struct {
		name, yaml string
		want       []string // substrings of the error; none: valid
	}{
		{"valid", valid, nil},
		{"a live report", valid + "context: live\n  audio.speakers: { status: not_tested, reason: live-limit }\n", nil},
		{"installed, said so", valid + "context: installed\n", nil},
		{"challenge counts like observed", valid + "  display.brightness: { status: supported, method: challenge }\n", nil},
		{"fixes and replaced parts", valid + "fixes: [omaboot.x]\nreplaced_parts: [{ kind: wifi, detail: from a 2013 iMac, ids: [\"14e4:43a0\"] }]\n", nil},
		{"unknown context", valid + "context: chroot\n", []string{"context:", "installed, live"}},
		{"live-limit on an installed report", valid + "  audio.speakers: { status: not_tested, reason: live-limit }\n", []string{"items.audio.speakers.reason", "only for live reports"}},
		{"live-limit on a tested item", valid + "context: live\n  audio.speakers: { status: failed, method: observed, reason: live-limit }\n", []string{"only not_tested"}},
		{"bad method", valid + "  audio.speakers: { status: failed, method: guessed }\n", []string{"items.audio.speakers.method", "challenge"}},
		{"a fix twice", valid + "fixes: [omaboot.x, omaboot.x]\n", []string{"fixes[1]", "twice"}},
		{"an empty fix", valid + "fixes: [\"\"]\n", []string{"fixes[0]: empty"}},
		{"unknown part kind", valid + "replaced_parts: [{ kind: ram }]\n", []string{"replaced_parts[0].kind", "wifi, bluetooth"}},
		{"an empty part ID", valid + "replaced_parts: [{ kind: gpu, ids: [\"\"] }]\n", []string{"replaced_parts[0].ids[0]"}},
		{"bad version", strings.Replace(valid, `"4.0.4"`, `"four"`, 1), []string{"omarchy.version"}},
		{"future date", strings.Replace(valid, "2026-10-01T12:00:00Z", "2026-10-03T13:00:00Z", 1), []string{"in the future"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Lines after the items belong to items again; put top-level fields first.
			var top, items []string
			inItems := false
			for _, l := range strings.Split(strings.TrimSpace(tt.yaml), "\n") {
				switch {
				case l == "items:":
					inItems = true
				case strings.HasPrefix(l, "  "):
					items = append(items, l)
				default:
					top = append(top, l)
				}
			}
			if !inItems {
				t.Fatal("no items")
			}
			f, err := Parse([]byte(strings.Join(top, "\n") + "\nitems:\n" + strings.Join(items, "\n") + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			err = Check(f, now)
			if tt.want == nil {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if _, ok := err.(Errors); !ok {
				t.Fatalf("want Errors, got %v", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}
