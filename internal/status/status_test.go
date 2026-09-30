package status

import "testing"

func TestConfig(t *testing.T) {
	tests := []struct {
		name string
		in   ConfigInput
		want ConfigStatus
	}{
		{"untested", ConfigInput{Applicable: 31},
			ConfigStatus{Verdict: Untested, Applicable: 31, Counts: Counts{Untested: 31}}},
		{"out of scope keeps its verdict", ConfigInput{Excluded: "Xserve", Applicable: 25},
			ConfigStatus{Verdict: Untested, Excluded: "Xserve", Applicable: 25, Counts: Counts{Untested: 25}}},
		{"hard blocker", ConfigInput{HardBlocker: "32-bit CPU", Applicable: 30},
			ConfigStatus{Verdict: NotCompatible, Reason: "32-bit CPU", Applicable: 30, Counts: Counts{Untested: 30}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Config(tt.in); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	sup := func(n int, stale bool) ConfigStatus {
		return ConfigStatus{Verdict: Supported, Applicable: n, Tested: n, Counts: Counts{Supported: n}, Stale: stale}
	}
	all := []ConfigStatus{
		Config(ConfigInput{HardBlocker: "yonah", Applicable: 30}),                       // excluded from N
		Config(ConfigInput{Applicable: 30}),                                             // untested
		Config(ConfigInput{Excluded: "Released before 2009", Applicable: 30}),           // out of scope
		Config(ConfigInput{HardBlocker: "yonah", Excluded: "pre-2009", Applicable: 30}), // counted as not compatible only
		sup(20, false), // verified
		sup(20, true),  // verified, stale
		{Verdict: Supported, Applicable: 20, Tested: 15, Counts: Counts{Supported: 15, Untested: 5}},          // tested, not verified
		{Verdict: Partial, Applicable: 20, Tested: 20, Counts: Counts{Supported: 20}, Conflicts: 1},           // conflict: not verified
		{Verdict: Partial, Applicable: 20, Tested: 2, Counts: Counts{Supported: 1, Partial: 1, Untested: 18}}, // tested
	}
	got := Summarize(all)
	want := Coverage{Eligible: 6, NotCompatible: 2, OutOfScope: 1, Verified: 2, StaleVerified: 1, Tested: 5}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got.VerifiedPct() <= 33.3 || got.VerifiedPct() >= 33.4 {
		t.Errorf("VerifiedPct = %v", got.VerifiedPct())
	}
	if (Coverage{}).TestedPct() != 0 {
		t.Error("empty coverage must be 0%, not NaN")
	}
}

func TestVerdictText(t *testing.T) {
	for _, v := range []Verdict{NotCompatible, Untested, Unsupported, Partial, Supported} {
		if v.Glyph() == "" || v.Label() == "" {
			t.Errorf("%s: missing glyph or label", v)
		}
	}
	if Verdict("bogus").Label() != "Untested" {
		t.Error("unknown verdicts render as Untested")
	}
}
