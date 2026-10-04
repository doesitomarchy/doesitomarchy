package status

import (
	"fmt"
	"testing"
)

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

// item is a stable result. Its build stamp sorts with the version, as if
// each release's code were committed after the last one's.
func item(cap string, verdict Verdict, ver, on string, id int64) Item {
	o := v(ver)
	return Item{Capability: cap, Verdict: verdict, Method: "automatic", Omarchy: o, Channel: o.Channel(), TestedAt: on, ResultID: id,
		BuiltAt: fmt.Sprintf("b%03d.%03d.%03d", o.Major, o.Minor, o.Patch)}
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
	// Every form PLAN §28.1 lists, and its canonical form and channel.
	for in, want := range map[string][2]string{
		"4":                      {"4.0.0", Stable},
		"4.0.4":                  {"4.0.4", Stable},
		"v4.1":                   {"4.1.0", Stable},
		" 4.10.2 ":               {"4.10.2", Stable},
		"4.0.4-1":                {"4.0.4", Stable}, // omarchy-version's pacman release
		"4.0.0rc2":               {"4.0.0rc2", RC},
		"4.0.1-RC3":              {"4.0.1rc3", RC},
		"4.0.0.rc1":              {"4.0.0rc1", RC},
		"v4.0.0-beta3":           {"4.0.0beta3", Beta}, // the git tag
		"4.0.0beta3-2":           {"4.0.0beta3", Beta},
		"4.0.0.r6713.ga85e29a":   {"4.0.0.r6713.ga85e29a", Edge},
		"4.0.0.r6720.g8E02FC8-1": {"4.0.0.r6720.g8e02fc8", Edge},
	} {
		got, err := ParseVersion(in)
		if err != nil || got.String() != want[0] || got.Channel() != want[1] {
			t.Errorf("ParseVersion(%q) = %s (%s), %v; want %s (%s)", in, got, got.Channel(), err, want[0], want[1])
		}
	}
	for _, bad := range []string{"", "four", "4.x", "4.0.1.2", "-1.0", "4.0.0alpha1", "4.0.0.r12", "dev", "dev (a85e29a)"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("ParseVersion(%q) should fail", bad)
		}
	}
	edge, rc := v("4.0.0.r6713.ga85e29a"), v("4.0.0rc2")
	for _, c := range []struct {
		v       Version
		in, out string
		ok      bool
	}{{edge, "", Edge, true}, {edge, Edge, Edge, true}, {edge, Dev, Dev, true}, {rc, Dev, "", false}, {rc, Stable, "", false}, {rc, RC, RC, true}} {
		got, err := ValidChannel(c.v, c.in)
		if (err == nil) != c.ok || got != c.out {
			t.Errorf("ValidChannel(%s, %q) = %q, %v", c.v, c.in, got, err)
		}
	}
}

// Across channels, the newest build wins by when its code was committed
// (PLAN §28.2): a dev fix beats an older stable failure; a newer stable
// regression beats an older dev pass; the same build agrees with itself.
func TestNewestBuild(t *testing.T) {
	at := func(verdict Verdict, ver, channel, built, tested string, id int64) Item {
		return Item{Capability: "audio.speakers", Verdict: verdict, Omarchy: v(ver), Channel: channel, BuiltAt: built, TestedAt: tested, ResultID: id}
	}
	stableFail := at(Failed, "4.0.4", Stable, "2026-09-15T05:34:12Z", "2026-10-02T10:00:00Z", 1)
	devFix := at(Supported, "4.0.0.r6800.g1a2b3c4", Dev, "2026-10-04T09:00:00Z", "2026-10-04T12:00:00Z", 2)
	cs := Config(ConfigInput{Caps: caps[2:3], Items: []Item{stableFail, devFix}}).Caps["audio.speakers"]
	if cs.Verdict != Supported || cs.Latest.Channel != Dev {
		t.Errorf("dev fix: %s from %s", cs.Verdict, cs.Latest.Channel)
	}
	stableRegress := at(Failed, "4.0.5", Stable, "2026-10-10T00:00:00Z", "2026-10-11T00:00:00Z", 3)
	cs = Config(ConfigInput{Caps: caps[2:3], Items: []Item{devFix, stableRegress}}).Caps["audio.speakers"]
	if cs.Verdict != Failed {
		t.Errorf("newer stable regression: %s", cs.Verdict)
	}
	// An edge build's version stays at the branch base (4.0.0) while stable
	// moves on: only the build date says which is newer.
	edgeOld := at(Supported, "4.0.0.r6500.gaaaaaaa", Edge, "2026-09-01T00:00:00Z", "2026-10-05T00:00:00Z", 4)
	cs = Config(ConfigInput{Caps: caps[2:3], Items: []Item{edgeOld, stableFail}}).Caps["audio.speakers"]
	if cs.Verdict != Failed {
		t.Errorf("stable 4.0.4 (built 2026-09-15) should beat edge built 2026-09-01: %s", cs.Verdict)
	}
	// Two reports on the same build that disagree: a conflict.
	again := stableFail
	again.Verdict, again.TestedAt, again.ResultID = Supported, "2026-10-03T00:00:00Z", 5
	if cs = Config(ConfigInput{Caps: caps[2:3], Items: []Item{stableFail, again}}).Caps["audio.speakers"]; !cs.Conflict {
		t.Error("same build disagreeing: no conflict")
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

// Per-connector criteria judged by port group (PLAN §25.1a).
func TestGroupRollup(t *testing.T) {
	boot := item("boot.install", Supported, "4.0.4", "2026-10-01", 1)
	port := func(cap, conn string, verdict Verdict, id int64) Item {
		it := item(cap, verdict, "4.0.4", "2026-10-01", id)
		it.Connector = conn
		return it
	}
	whole := func(cap string, v Verdict) Item { return item(cap, v, "4.0.4", "2026-10-01", 1) }
	// USB-A: two connectors on one controller (one group).
	usb := []Capability{{ID: "boot.install", Label: "Boot → Install", Blocking: true},
		{ID: "ports.usb-a", Label: "Ports → USB-A", Groups: []Group{{"usb-a-3", []string{"left-4", "right-3"}}}}}
	// Thunderbolt 3: left and right pairs on separate controllers.
	tb := []Capability{{ID: "boot.install", Label: "Boot → Install", Blocking: true},
		{ID: "ports.thunderbolt", Label: "Ports → Thunderbolt", Groups: []Group{{"tb-left", []string{"left-1", "left-2"}}, {"tb-right", []string{"right-1", "right-2"}}}}}
	tests := []struct {
		name       string
		caps       []Capability
		cap        string
		items      []Item
		verdict    Verdict
		groupsPass int
		verified   bool
		covered    string // a connector expected to be covered by another
		suspect    string // a connector expected to be marked suspect
	}{
		{"untested", usb, "ports.usb-a", []Item{boot}, Untested, 0, false, "", ""},
		{"one port passes: its group is covered, verified", usb, "ports.usb-a", []Item{boot, port("ports.usb-a", "left-4", Supported, 2)}, Supported, 1, true, "right-3", ""},
		{"a criterion-level pass covers the only group", usb, "ports.usb-a", []Item{boot, whole("ports.usb-a", Supported)}, Supported, 1, true, "left-4", ""},
		{"a failure beside a pass: a suspect port, still supported", usb, "ports.usb-a",
			[]Item{boot, port("ports.usb-a", "left-4", Supported, 2), port("ports.usb-a", "right-3", Failed, 3)}, Supported, 1, true, "", "right-3"},
		{"the only tested port fails: failed", usb, "ports.usb-a", []Item{boot, port("ports.usb-a", "left-4", Failed, 2)}, Failed, 0, false, "", ""},
		{"one of two groups passes: supported, not verified", tb, "ports.thunderbolt", []Item{boot, port("ports.thunderbolt", "left-2", Supported, 2)}, Supported, 1, false, "left-1", ""},
		{"one group passes, the other fails: partial", tb, "ports.thunderbolt",
			[]Item{boot, port("ports.thunderbolt", "left-2", Supported, 2), port("ports.thunderbolt", "right-1", Failed, 3)}, Partial, 1, false, "", ""},
		{"partial shows the failure even when the pass is newer", tb, "ports.thunderbolt",
			[]Item{boot, port("ports.thunderbolt", "left-2", Supported, 3), port("ports.thunderbolt", "right-1", Failed, 2)}, Partial, 1, false, "", ""},
		{"both groups pass: verified", tb, "ports.thunderbolt",
			[]Item{boot, port("ports.thunderbolt", "left-1", Supported, 2), port("ports.thunderbolt", "right-2", Supported, 3)}, Supported, 2, true, "left-2", ""},
		{"a criterion-level pass with two groups decides the verdict, completes nothing", tb, "ports.thunderbolt",
			[]Item{boot, whole("ports.thunderbolt", Supported)}, Supported, 0, false, "", ""},
		{"connectors override a criterion-level item", tb, "ports.thunderbolt",
			[]Item{boot, whole("ports.thunderbolt", Supported), port("ports.thunderbolt", "right-1", Failed, 3)}, Failed, 0, false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := Config(ConfigInput{Caps: tt.caps, Items: tt.items, CurrentMajor: 4})
			cs := st.Caps[tt.cap]
			if cs.Verdict != tt.verdict || cs.GroupsPassed != tt.groupsPass || st.Verified() != tt.verified {
				t.Errorf("verdict %s, %d of %d groups, verified %v", cs.Verdict, cs.GroupsPassed, cs.GroupsTotal, st.Verified())
			}
			if tt.covered != "" && cs.Ports[tt.covered].CoveredBy == "" {
				t.Errorf("%s isn't covered: %+v", tt.covered, cs.Ports)
			}
			if tt.suspect != "" && !cs.Ports[tt.suspect].Suspect {
				t.Errorf("%s isn't suspect: %+v", tt.suspect, cs.Ports)
			}
			// A criterion that didn't pass shows why, not a passing port.
			if (cs.Verdict == Partial || cs.Verdict == Failed) && (cs.Latest == nil || cs.Latest.Verdict == Supported) {
				t.Errorf("%s criterion's latest result should explain it: %+v", cs.Verdict, cs.Latest)
			}
		})
	}
}
