// Package status turns what is known about a configuration into the verdict,
// counts, blocker and site-wide coverage defined in PLAN.md §6, §20.3 and
// §21.4. It is pure: callers pass catalog facts and accepted result items in,
// and get verdicts out.
package status

import (
	"fmt"
	"strconv"
	"strings"
)

// Verdict is a configuration's overall status, and also a single
// capability's status (every value except NotCompatible applies to both).
//
// Failed is what a failing test produces: something to fix, in keeping with
// #WeCanFixEverything. Unsupported is a last resort that only maintainers
// set, after repeated attempts to fix a failure have made no progress.
type Verdict string

const (
	NotCompatible Verdict = "not-compatible"
	Untested      Verdict = "untested"
	Unsupported   Verdict = "unsupported"
	Failed        Verdict = "failed"
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
	case Failed:
		return "✕"
	case Unsupported:
		return "⚑"
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
	case Failed:
		return "Failed"
	case Unsupported:
		return "Unsupported"
	default:
		return "Untested"
	}
}

// Version is an Omarchy release, compared numerically.
type Version struct{ Major, Minor, Patch int }

// ParseVersion reads "4", "4.1" or "4.1.2" (a leading "v" is allowed).
func ParseVersion(s string) (Version, error) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(s), "v"), ".")
	if len(parts) < 1 || len(parts) > 3 || parts[0] == "" {
		return Version{}, fmt.Errorf("version %q: want major.minor[.patch]", s)
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return Version{}, fmt.Errorf("version %q: want major.minor[.patch]", s)
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, nil
}

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less orders versions numerically (4.10 > 4.9).
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

// Item is one accepted result item for an applicable capability. Items that
// were not tested, or whose capability does not apply, are never passed in.
type Item struct {
	Capability string
	Verdict    Verdict // Supported, Partial or Failed
	Method     string  // automatic | observed | fixture
	Omarchy    Version
	TestedAt   string // YYYY-MM-DD
	ResultID   int64
	Evidence   string // why it failed, as reported
}

// newer reports whether a should win over b as a capability's latest result:
// the newest Omarchy version, then the latest test date, then the later result.
func newer(a, b Item) bool {
	if a.Omarchy != b.Omarchy {
		return b.Omarchy.Less(a.Omarchy)
	}
	if a.TestedAt != b.TestedAt {
		return a.TestedAt > b.TestedAt
	}
	return a.ResultID > b.ResultID
}

// Capability is an applicable capability, in display order.
type Capability struct {
	ID       string
	Label    string // "Category → Name", for the blocker
	Blocking bool   // its category's failure blocks install (Boot)
}

// ConfigInput is what the engine knows about one configuration.
type ConfigInput struct {
	HardBlocker  string            // non-empty: architectural blocker (32-bit CPU)
	Excluded     string            // non-empty: out of coverage scope, with the reason
	Caps         []Capability      // applicable capabilities, in category then catalog order
	Items        []Item            // accepted, applicable, tested items for this config
	Unsupported  map[string]string // capability ID → maintainer's reason (the white flag)
	Results      int               // accepted results for this config, whatever they contain
	LatestResult string            // newest accepted result's test date
	CurrentMajor int               // current Omarchy major; older results are stale
}

// CapStatus is one capability's current status on a configuration.
type CapStatus struct {
	Verdict  Verdict
	Latest   *Item  // the winning result item; nil when untested
	Conflict bool   // accepted results on the same latest Omarchy version disagree
	Stale    bool   // the latest result predates the current Omarchy major
	Reason   string // the maintainer's reason when Unsupported
}

// Counts tallies applicable capabilities by their current status.
type Counts struct {
	Supported, Partial, Failed, Unsupported, Untested int
}

// ConfigStatus is the rollup for one configuration.
type ConfigStatus struct {
	Verdict    Verdict
	Reason     string // hard-blocker text for Not compatible
	Excluded   string // coverage-scope reason; the verdict is unaffected
	Blocker    string // first failing capability, "Category → Name"
	Counts     Counts
	Applicable int
	Tested     int    // applicable capabilities with a current result
	Conflicts  int    // capabilities with conflicting reports
	Stale      bool   // at least one current result predates the current Omarchy major
	Results    int    // accepted results for this config
	Latest     string // newest accepted result's test date ("" when none)
	Caps       map[string]CapStatus
}

// capStatus works out one capability from its accepted items.
func capStatus(items []Item, unsupported string, hasUnsupported bool, currentMajor int) CapStatus {
	var cs CapStatus
	if len(items) > 0 {
		best := items[0]
		for _, it := range items[1:] {
			if newer(it, best) {
				best = it
			}
		}
		cs.Latest, cs.Verdict = &best, best.Verdict
		cs.Stale = currentMajor > 0 && best.Omarchy.Major < currentMajor
		for _, it := range items {
			if it.Omarchy == best.Omarchy && it.Verdict != best.Verdict {
				cs.Conflict, cs.Verdict = true, Partial
				break
			}
		}
	} else {
		cs.Verdict = Untested
	}
	if hasUnsupported {
		cs.Verdict, cs.Reason = Unsupported, unsupported
	}
	return cs
}

// Config computes one configuration's status.
func Config(in ConfigInput) ConfigStatus {
	st := ConfigStatus{Verdict: Untested, Excluded: in.Excluded, Applicable: len(in.Caps), Results: in.Results,
		Latest: in.LatestResult, Caps: make(map[string]CapStatus, len(in.Caps))}
	byCap := map[string][]Item{}
	for _, it := range in.Items {
		byCap[it.Capability] = append(byCap[it.Capability], it)
	}
	bootUnsupported, bootFailed, problem := false, false, false
	for _, c := range in.Caps {
		reason, flagged := in.Unsupported[c.ID]
		cs := capStatus(byCap[c.ID], reason, flagged, in.CurrentMajor)
		st.Caps[c.ID] = cs
		if cs.Latest != nil || flagged {
			st.Tested++
		}
		if cs.Conflict {
			st.Conflicts++
		}
		if cs.Stale {
			st.Stale = true
		}
		switch cs.Verdict {
		case Supported:
			st.Counts.Supported++
		case Partial:
			st.Counts.Partial++
		case Failed:
			st.Counts.Failed++
		case Unsupported:
			st.Counts.Unsupported++
		default:
			st.Counts.Untested++
		}
		if cs.Verdict == Failed || cs.Verdict == Unsupported || cs.Verdict == Partial {
			problem = true
			if st.Blocker == "" {
				st.Blocker = c.Label
			}
			if c.Blocking && cs.Verdict == Unsupported {
				bootUnsupported = true
			}
			if c.Blocking && cs.Verdict == Failed {
				bootFailed = true
			}
		}
	}
	switch {
	case in.HardBlocker != "":
		st.Verdict, st.Reason = NotCompatible, in.HardBlocker
	case st.Tested == 0:
		st.Verdict = Untested
	case bootUnsupported:
		st.Verdict = Unsupported
	case bootFailed:
		st.Verdict = Failed
	case problem:
		st.Verdict = Partial
	default:
		st.Verdict = Supported
	}
	return st
}

// Verified reports whether every applicable capability passed, with no
// conflicts: the Omarchy badge and the compatibility-coverage numerator.
func (s ConfigStatus) Verified() bool {
	return s.Verdict != NotCompatible && s.Applicable > 0 && s.Counts.Supported == s.Applicable && s.Conflicts == 0
}

// Coverage holds the two home-page metrics (PLAN §6.1).
type Coverage struct {
	Eligible      int // N: configs that are neither Not compatible nor out of scope
	NotCompatible int // excluded from N, reported separately
	OutOfScope    int // excluded from N by data/coverage.yaml (not also Not compatible)
	Verified      int // every applicable capability supported, no conflicts
	StaleVerified int // verified configs relying on results from an older Omarchy major
	Tested        int // configs with at least one accepted result
	Results       int // accepted results over the eligible configs
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
		if s.Excluded != "" {
			c.OutOfScope++
			continue
		}
		c.Eligible++
		c.Results += s.Results
		if s.Results > 0 || s.Tested > 0 {
			c.Tested++
		}
		if s.Verified() {
			c.Verified++
			if s.Stale {
				c.StaleVerified++
			}
		}
	}
	return c
}
