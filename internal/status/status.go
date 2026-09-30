// Package status turns what is known about a configuration into the verdict,
// counts, blocker and site-wide coverage defined in PLAN.md §6.
//
// Until test results exist (Phase 7), the only inputs are the catalog: a
// config is ⛔ Not compatible when its Mac has a hard blocker and ⚪ Untested
// otherwise. Phase 7 adds result items to ConfigInput; the output types and
// Coverage stay the same, so the web UI does not change.
package status

// Verdict is a configuration's overall status.
type Verdict string

const (
	NotCompatible Verdict = "not-compatible"
	Untested      Verdict = "untested"
	Unsupported   Verdict = "unsupported"
	Partial       Verdict = "partial"
	Supported     Verdict = "supported"
)

// Glyph is the monotone status glyph (PLAN §6); never shown without Label.
func (v Verdict) Glyph() string {
	switch v {
	case NotCompatible:
		return "⛔"
	case Supported:
		return "●"
	case Partial:
		return "◐"
	case Unsupported:
		return "○"
	default:
		return "·"
	}
}

// Label is the human-readable verdict.
func (v Verdict) Label() string {
	switch v {
	case NotCompatible:
		return "Not compatible"
	case Supported:
		return "Supported"
	case Partial:
		return "Partial"
	case Unsupported:
		return "Unsupported"
	default:
		return "Untested"
	}
}

// Counts tallies applicable capabilities by their current status.
type Counts struct {
	Supported, Partial, Unsupported, Untested int
}

// ConfigInput is what the engine knows about one configuration.
type ConfigInput struct {
	HardBlocker string // non-empty: architectural blocker (32-bit CPU)
	Applicable  int    // number of applicable capabilities
}

// ConfigStatus is the rollup for one configuration.
type ConfigStatus struct {
	Verdict    Verdict
	Reason     string // hard-blocker text for Not compatible
	Blocker    string // first failing capability, "Category → Name" (Phase 7)
	Counts     Counts
	Applicable int
	Tested     int  // applicable capabilities with a current result
	Conflicts  int  // capabilities with conflicting reports
	Stale      bool // at least one current result predates the current Omarchy major
}

// Config computes one configuration's status from catalog facts only.
func Config(in ConfigInput) ConfigStatus {
	st := ConfigStatus{Verdict: Untested, Applicable: in.Applicable, Counts: Counts{Untested: in.Applicable}}
	if in.HardBlocker != "" {
		st.Verdict, st.Reason = NotCompatible, in.HardBlocker
	}
	return st
}

// Coverage holds the two home-page metrics (PLAN §6.1).
type Coverage struct {
	Eligible      int // N: configs that are not Not compatible
	NotCompatible int // excluded from N, reported separately
	Verified      int // every applicable capability supported, no conflicts
	StaleVerified int // verified configs relying on results from an older Omarchy major
	Tested        int // configs with at least one current result
}

// VerifiedPct and TestedPct are percentages of N (0 when N is 0).
func (c Coverage) VerifiedPct() float64 { return pct(c.Verified, c.Eligible) }
func (c Coverage) TestedPct() float64   { return pct(c.Tested, c.Eligible) }

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}

// Summarize computes coverage over every configuration's status.
func Summarize(all []ConfigStatus) Coverage {
	var c Coverage
	for _, s := range all {
		if s.Verdict == NotCompatible {
			c.NotCompatible++
			continue
		}
		c.Eligible++
		if s.Tested > 0 {
			c.Tested++
		}
		if s.Applicable > 0 && s.Counts.Supported == s.Applicable && s.Conflicts == 0 {
			c.Verified++
			if s.Stale {
				c.StaleVerified++
			}
		}
	}
	return c
}
