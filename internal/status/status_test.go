package status

import "testing"

// caps is a small config: two Boot capabilities, then audio and Wi-Fi.
var caps = []Capability{
	{ID: "boot.installer-efi64", Label: "Boot → Installer", Blocking: true},
	{ID: "boot.install", Label: "Boot → Install", Blocking: true},
	{ID: "audio.speakers", Label: "Audio → Speakers"},
	{ID: "network.wifi", Label: "Networking → Wi-Fi"},
}

func v(s string) Version {
	ver, err := ParseVersion(s)
	if err != nil {
		panic(err)
	}
	return ver
}

func item(cap string, verdict Verdict, ver, on string, id int64) Item {
	return Item{Capability: cap, Verdict: verdict, Method: "automatic", Omarchy: v(ver), TestedAt: on, ResultID: id}
}

// all returns an item for every capability with the same verdict.
func all(verdict Verdict, ver, on string, id int64) []Item {
	var out []Item
	for _, c := range caps {
		out = append(out, item(c.ID, verdict, ver, on, id))
	}
	return out
}

func TestConfigVerdicts(t *testing.T) {
	tests := []struct {
		name      string
		in        ConfigInput
		verdict   Verdict
		blocker   string
		counts    Counts
		tested    int
		conflicts int
		stale     bool
		verified  bool
	}{
		{name: "no results: untested",
			in:      ConfigInput{Caps: caps},
			verdict: Untested, counts: Counts{Untested: 4}},
		{name: "hard blocker wins over results",
			in:      ConfigInput{HardBlocker: "32-bit CPU", Caps: caps, Items: all(Supported, "4.0", "2026-10-01", 1)},
			verdict: NotCompatible, counts: Counts{Supported: 4}, tested: 4},
		{name: "everything passed: supported and verified",
			in:      ConfigInput{Caps: caps, Items: all(Supported, "4.0.4", "2026-10-01", 1), CurrentMajor: 4},
			verdict: Supported, counts: Counts{Supported: 4}, tested: 4, verified: true},
		{name: "some passed, rest untested: supported, not verified",
			in:      ConfigInput{Caps: caps, Items: []Item{item("boot.install", Supported, "4.0", "2026-10-01", 1)}},
			verdict: Supported, counts: Counts{Supported: 1, Untested: 3}, tested: 1},
		{name: "a non-boot failure: partial, blocked by it",
			in: ConfigInput{Caps: caps, Items: append(all(Supported, "4.0", "2026-10-01", 1)[:2],
				item("audio.speakers", Failed, "4.0", "2026-10-01", 1))},
			verdict: Partial, blocker: "Audio → Speakers", counts: Counts{Supported: 2, Failed: 1, Untested: 1}, tested: 3},
		{name: "a partial capability: partial",
			in:      ConfigInput{Caps: caps, Items: []Item{item("network.wifi", Partial, "4.0", "2026-10-01", 1)}},
			verdict: Partial, blocker: "Networking → Wi-Fi", counts: Counts{Partial: 1, Untested: 3}, tested: 1},
		{name: "a boot failure: failed",
			in: ConfigInput{Caps: caps, Items: []Item{item("boot.installer-efi64", Supported, "4.0", "2026-10-01", 1),
				item("boot.install", Failed, "4.0", "2026-10-01", 1), item("audio.speakers", Failed, "4.0", "2026-10-01", 1)}},
			verdict: Failed, blocker: "Boot → Install", counts: Counts{Supported: 1, Failed: 2, Untested: 1}, tested: 3},
		{name: "a boot capability given up on: unsupported, over a boot failure",
			in: ConfigInput{Caps: caps, Items: []Item{item("boot.install", Failed, "4.0", "2026-10-01", 1)},
				Unsupported: map[string]string{"boot.installer-efi64": "firmware refuses the image"}},
			verdict: Unsupported, blocker: "Boot → Installer", counts: Counts{Unsupported: 1, Failed: 1, Untested: 2}, tested: 2},
		{name: "unsupported overrides a pass",
			in: ConfigInput{Caps: caps, Items: all(Supported, "4.0", "2026-10-01", 1),
				Unsupported: map[string]string{"audio.speakers": "no open driver"}},
			verdict: Partial, blocker: "Audio → Speakers", counts: Counts{Supported: 3, Unsupported: 1}, tested: 4},
		{name: "fixed in a newer Omarchy: the newer result wins",
			in: ConfigInput{Caps: caps[2:3], Items: []Item{item("audio.speakers", Failed, "4.0", "2026-10-01", 1),
				item("audio.speakers", Supported, "4.1", "2026-09-01", 2)}},
			verdict: Supported, counts: Counts{Supported: 1}, tested: 1, verified: true},
		{name: "4.10 is newer than 4.9",
			in: ConfigInput{Caps: caps[2:3], Items: []Item{item("audio.speakers", Supported, "4.9", "2026-12-01", 1),
				item("audio.speakers", Failed, "4.10", "2026-11-01", 2)}},
			verdict: Partial, blocker: "Audio → Speakers", counts: Counts{Failed: 1}, tested: 1},
		{name: "regressed in a newer Omarchy",
			in: ConfigInput{Caps: caps[2:3], Items: []Item{item("audio.speakers", Supported, "4.0", "2026-10-01", 1),
				item("audio.speakers", Failed, "4.1", "2026-09-30", 2)}},
			verdict: Partial, blocker: "Audio → Speakers", counts: Counts{Failed: 1}, tested: 1},
		{name: "same version disagrees: conflict, shown partial, not verified",
			in: ConfigInput{Caps: caps[2:3], Items: []Item{item("audio.speakers", Supported, "4.0.4", "2026-10-01", 1),
				item("audio.speakers", Failed, "4.0.4", "2026-09-30", 2)}},
			verdict: Partial, blocker: "Audio → Speakers", counts: Counts{Partial: 1}, tested: 1, conflicts: 1},
		{name: "same version, different patch: no conflict, newest patch wins",
			in: ConfigInput{Caps: caps[2:3], Items: []Item{item("audio.speakers", Failed, "4.0.3", "2026-09-30", 1),
				item("audio.speakers", Supported, "4.0.4", "2026-10-01", 2)}},
			verdict: Supported, counts: Counts{Supported: 1}, tested: 1, verified: true},
		{name: "same version, same verdict twice: no conflict",
			in: ConfigInput{Caps: caps[2:3], Items: []Item{item("audio.speakers", Supported, "4.0", "2026-10-01", 1),
				item("audio.speakers", Supported, "4.0", "2026-09-30", 2)}},
			verdict: Supported, counts: Counts{Supported: 1}, tested: 1, verified: true},
		{name: "an older conflict is settled by a newer version",
			in: ConfigInput{Caps: caps[2:3], Items: []Item{item("audio.speakers", Supported, "4.0", "2026-10-01", 1),
				item("audio.speakers", Failed, "4.0", "2026-09-30", 2), item("audio.speakers", Supported, "4.1", "2026-10-03", 3)}},
			verdict: Supported, counts: Counts{Supported: 1}, tested: 1, verified: true},
		{name: "results from an older major are stale but still verified",
			in:      ConfigInput{Caps: caps, Items: all(Supported, "3.2", "2025-12-01", 1), CurrentMajor: 4},
			verdict: Supported, counts: Counts{Supported: 4}, tested: 4, stale: true, verified: true},
		{name: "an item for an unlisted capability is ignored",
			in:      ConfigInput{Caps: caps[:1], Items: []Item{item("ports.sd-card", Failed, "4.0", "2026-10-01", 1)}},
			verdict: Untested, counts: Counts{Untested: 1}},
		{name: "the blocker is the first problem in order, not the worst",
			in: ConfigInput{Caps: caps, Items: []Item{item("audio.speakers", Partial, "4.0", "2026-10-01", 1),
				item("network.wifi", Failed, "4.0", "2026-10-01", 1)}},
			verdict: Partial, blocker: "Audio → Speakers", counts: Counts{Partial: 1, Failed: 1, Untested: 2}, tested: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Config(tt.in)
			if got.Verdict != tt.verdict || got.Blocker != tt.blocker || got.Counts != tt.counts || got.Tested != tt.tested ||
				got.Conflicts != tt.conflicts || got.Stale != tt.stale || got.Verified() != tt.verified {
				t.Fatalf("got verdict=%s blocker=%q counts=%+v tested=%d conflicts=%d stale=%v verified=%v\nwant verdict=%s blocker=%q counts=%+v tested=%d conflicts=%d stale=%v verified=%v",
					got.Verdict, got.Blocker, got.Counts, got.Tested, got.Conflicts, got.Stale, got.Verified(),
					tt.verdict, tt.blocker, tt.counts, tt.tested, tt.conflicts, tt.stale, tt.verified)
			}
		})
	}
}

func TestCapLatestAndReason(t *testing.T) {
	in := ConfigInput{Caps: caps[2:4], CurrentMajor: 4,
		Items:       []Item{item("audio.speakers", Failed, "4.0", "2026-10-01", 7), item("audio.speakers", Failed, "4.0", "2026-10-05", 9)},
		Unsupported: map[string]string{"network.wifi": "no driver"}}
	got := Config(in)
	sp := got.Caps["audio.speakers"]
	if sp.Latest == nil || sp.Latest.ResultID != 9 {
		t.Fatalf("latest should be the later test date (result 9): %+v", sp.Latest)
	}
	if w := got.Caps["network.wifi"]; w.Verdict != Unsupported || w.Reason != "no driver" || w.Latest != nil {
		t.Fatalf("wifi: %+v", w)
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]Version{"4": {4, 0, 0}, "4.0.4": {4, 0, 4}, "v4.1": {4, 1, 0}, " 4.10.2 ": {4, 10, 2}} {
		if got, err := ParseVersion(in); err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "four", "4.x", "4.0.1.2", "-1.0"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) should fail", bad)
		}
	}
}

func TestSummarize(t *testing.T) {
	untested := Config(ConfigInput{Caps: caps})
	all4 := Config(ConfigInput{Caps: caps, Items: all(Supported, "4.0", "2026-10-01", 1), Results: 1, CurrentMajor: 4})
	stale := Config(ConfigInput{Caps: caps, Items: all(Supported, "3.2", "2025-10-01", 2), Results: 2, CurrentMajor: 4})
	conflict := Config(ConfigInput{Caps: caps[2:3], Results: 2, Items: []Item{item("audio.speakers", Supported, "4.0", "2026-10-01", 3),
		item("audio.speakers", Failed, "4.0", "2026-09-30", 4)}})
	onlySkips := Config(ConfigInput{Caps: caps, Results: 1}) // a result whose items were all not tested
	statuses := []ConfigStatus{
		Config(ConfigInput{HardBlocker: "yonah", Caps: caps}), // excluded from N
		untested, // counted, untested
		Config(ConfigInput{Excluded: "Released before 2009", Caps: caps}),           // out of scope
		Config(ConfigInput{HardBlocker: "yonah", Excluded: "pre-2009", Caps: caps}), // not compatible only
		all4, stale, conflict, onlySkips,
		Config(ConfigInput{Excluded: "Xserve", Caps: caps, Items: all(Supported, "4.0", "2026-10-01", 5), Results: 1}), // verified but out of scope
	}
	got := Summarize(statuses)
	want := Coverage{Eligible: 5, NotCompatible: 2, OutOfScope: 2, Verified: 2, StaleVerified: 1, Tested: 4, Results: 6}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got.VerifiedPct() != 40 || got.TestedPct() != 80 {
		t.Errorf("pct: %v %v", got.VerifiedPct(), got.TestedPct())
	}
	if (Coverage{}).TestedPct() != 0 {
		t.Error("empty coverage must be 0%, not NaN")
	}
}

func TestVerdictText(t *testing.T) {
	for _, v := range []Verdict{NotCompatible, Untested, Unsupported, Failed, Partial, Supported} {
		if v.Glyph() == "" || v.Label() == "" {
			t.Errorf("%s: missing glyph or label", v)
		}
	}
	if Verdict("bogus").Label() != "Untested" {
		t.Error("unknown verdicts render as Untested")
	}
}

// Per-connector criteria (PLAN §25): USB-A on two connectors.
func TestConnectorRollup(t *testing.T) {
	pcaps := []Capability{{ID: "boot.install", Label: "Boot → Install", Blocking: true},
		{ID: "ports.usb-a", Label: "Ports → USB-A", Connectors: []string{"left-4", "right-3"}}}
	port := func(conn string, verdict Verdict, id int64) Item {
		it := item("ports.usb-a", verdict, "4.0.4", "2026-10-01", id)
		it.Connector = conn
		return it
	}
	boot := item("boot.install", Supported, "4.0.4", "2026-10-01", 1)
	whole := item("ports.usb-a", Supported, "4.0.4", "2026-10-01", 1)
	tests := []struct {
		name       string
		items      []Item
		verdict    Verdict
		passed     int
		tested     int
		verified   bool
		incomplete int
	}{
		{"untested", []Item{boot}, Untested, 0, 0, false, 1},
		{"a criterion-level pass counts, but isn't every connector", []Item{boot, whole}, Supported, 0, 0, false, 1},
		{"one connector passed, one untested", []Item{boot, port("left-4", Supported, 2)}, Supported, 1, 1, false, 1},
		{"both connectors passed: verified", []Item{boot, port("left-4", Supported, 2), port("right-3", Supported, 3)}, Supported, 2, 2, true, 0},
		{"one passed, one failed: partial", []Item{boot, port("left-4", Supported, 2), port("right-3", Failed, 3)}, Partial, 1, 2, false, 1},
		{"every tested connector failed: failed", []Item{boot, port("left-4", Failed, 2)}, Failed, 0, 1, false, 1},
		{"connectors override a criterion-level item", []Item{boot, whole, port("right-3", Failed, 3)}, Failed, 0, 1, false, 1},
		{"a later pass on the same connector wins", []Item{boot, port("left-4", Failed, 2),
			func() Item { it := port("left-4", Supported, 3); it.Omarchy = v("4.0.5"); return it }(), port("right-3", Supported, 4)}, Supported, 2, 2, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := Config(ConfigInput{Caps: pcaps, Items: tt.items, CurrentMajor: 4})
			cs := st.Caps["ports.usb-a"]
			if cs.Verdict != tt.verdict || cs.PortsPassed != tt.passed || cs.PortsTested != tt.tested || cs.PortsTotal != 2 ||
				st.Verified() != tt.verified || st.Incomplete != tt.incomplete {
				t.Errorf("verdict %s passed %d tested %d of %d, verified %v, incomplete %d", cs.Verdict, cs.PortsPassed, cs.PortsTested, cs.PortsTotal, st.Verified(), st.Incomplete)
			}
		})
	}
}
