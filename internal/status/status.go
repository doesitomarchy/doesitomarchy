// Package status turns what is known about a configuration into the verdict,
// counts, blocker and site-wide coverage defined in PLAN.md §6, §20.3 and
// §21.4. It is pure: callers pass catalog facts and accepted result items in,
// and get verdicts out.
package status

import (
	"fmt"
	"regexp"
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

// Version is an Omarchy build, as /etc/os-release's VERSION_ID names it
// (PLAN §28.1): a release (4.0.4), a pre-release (4.0.0rc2, 4.0.0beta3) or an
// edge build (4.0.0.r6713.ga85e29a: the branch's commit count and commit).
type Version struct {
	Major, Minor, Patch int
	Pre                 string // "rc2", "beta3"; "" for a release or an edge build
	Rev                 int    // edge build: the branch's total commit count
	Hash                string // edge build: the abbreviated commit
}

// Channels a result can come from. Dev runs edge packages from a git
// checkout, so its version reads like edge; it's only known when reported.
const (
	Stable = "stable"
	RC     = "rc"
	Beta   = "beta"
	Edge   = "edge"
	Dev    = "dev"
)

// Every form Omarchy versions take: an optional "v", major[.minor[.patch]],
// a pre-release attached or after "-"/"." (git tags write v4.0.0-beta3), an
// edge build's .rN.gHASH, and pacman's trailing release ("-1").
var versionRe = regexp.MustCompile(`(?i)^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:[-.]?(rc|beta)\.?(\d+))?(?:\.r(\d+)\.g([0-9a-f]{7,40}))?(?:-\d+)?$`)

// ParseVersion reads any form an Omarchy version is written in (PLAN §28.1)
// into the canonical one: "v4.0.4-1" is 4.0.4, "4.0.0-RC1" is 4.0.0rc1.
func ParseVersion(s string) (Version, error) {
	s = strings.TrimSpace(s)
	if low := strings.ToLower(s); low == "dev" || strings.HasPrefix(low, "dev ") || strings.HasPrefix(low, "dev(") {
		return Version{}, fmt.Errorf("version %q: a dev build names no version; give /etc/os-release's VERSION_ID (it reads like an edge build) and the channel dev", s)
	}
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("version %q: want an Omarchy version such as 4.0.4, 4.0.4rc2 or 4.0.0.r6713.ga85e29a", s)
	}
	num := func(x string) int { n, _ := strconv.Atoi(x); return n }
	v := Version{Major: num(m[1]), Minor: num(m[2]), Patch: num(m[3]), Rev: num(m[6]), Hash: strings.ToLower(m[7])}
	if m[4] != "" {
		v.Pre = strings.ToLower(m[4]) + strconv.Itoa(num(m[5]))
	}
	return v, nil
}

// String is the canonical form, shown everywhere.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d%s", v.Major, v.Minor, v.Patch, v.Pre)
	if v.Hash != "" {
		s += fmt.Sprintf(".r%d.g%s", v.Rev, v.Hash)
	}
	return s
}

// Channel is the channel a version comes from, by its form; a dev build
// reads as edge.
func (v Version) Channel() string {
	switch {
	case v.Hash != "":
		return Edge
	case strings.HasPrefix(v.Pre, "rc"):
		return RC
	case v.Pre != "":
		return Beta
	}
	return Stable
}

// ValidChannel checks a reported channel against a version: only dev can be
// told apart from what the version says, and only for an edge build.
func ValidChannel(v Version, channel string) (string, error) {
	switch channel {
	case "", v.Channel():
		return v.Channel(), nil
	case Dev:
		if v.Channel() == Edge {
			return Dev, nil
		}
		return "", fmt.Errorf("channel dev: %s isn't an edge-style version (a dev install's /etc/os-release reads like 4.0.0.r6713.ga85e29a)", v)
	}
	return "", fmt.Errorf("channel %q: %s is a %s version; the channel can only be given as dev, for an edge-style version", channel, v, v.Channel())
}

// Item is one accepted result item for an applicable capability. Items that
// were not tested, or whose capability does not apply, are never passed in.
type Item struct {
	Capability string
	Connector  string  // the physical connector, for per-connector port items; "" otherwise
	Verdict    Verdict // Supported, Partial or Failed
	Method     string  // automatic | observed | fixture | challenge (no method weighs more than another)
	Omarchy    Version
	Channel    string // stable | rc | beta | edge | dev
	// BuiltAt is when the Omarchy code tested was committed (RFC 3339, UTC);
	// the test time when that isn't known, since the build can't be newer.
	BuiltAt  string
	TestedAt string // RFC 3339, UTC (so later timestamps sort later as strings)
	ResultID int64
	Evidence string // why it failed, as reported
}

// newer reports whether a should win over b as a capability's latest result
// (PLAN §28.2): the newest build, by when its code was committed, whatever
// the channel; then the later test (a fix from a kernel or package update);
// then the later result.
func newer(a, b Item) bool {
	if a.BuiltAt != b.BuiltAt {
		return a.BuiltAt > b.BuiltAt
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
	// Groups, when set, are the port groups this criterion is judged on
	// (PLAN §25.1a): connectors sharing a controller, where one passing
	// connector covers the group.
	Groups []Group
}

// Group is a set of connectors judged together.
type Group struct {
	ID         string
	Connectors []string
}

// PortStatus is one connector's status within a per-connector criterion.
type PortStatus struct {
	Verdict   Verdict // its own latest result; Untested when none
	CoveredBy string  // untested, but a connector in its group passed
	Suspect   bool    // it failed while a connector in its group passed: probably a damaged port
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
	Conflict bool   // accepted results on the same latest Omarchy build disagree
	Stale    bool   // the latest result predates the current Omarchy major
	Reason   string // the maintainer's reason when Unsupported
	// Per-connector criteria: each connector's status, and how many port
	// groups are covered by a passing connector.
	Ports        map[string]PortStatus
	GroupsPassed int
	GroupsTotal  int
	GroupsTested int
}

// Complete reports whether every port group of a per-connector criterion has
// a passing connector (always true for other criteria).
func (cs CapStatus) Complete() bool { return cs.GroupsPassed == cs.GroupsTotal }

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
	Incomplete int    // per-connector criteria with a port group not yet covered
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
			if it.Omarchy == best.Omarchy && it.BuiltAt == best.BuiltAt && it.Verdict != best.Verdict {
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

// groupStatus works out a per-connector criterion by port group (PLAN
// §25.1a). Each connector's latest result stands for that connector. A group
// is Supported when any of its connectors passed; a connector that failed
// while a group-mate passed is marked Suspect (a damaged port, not a driver
// problem). The criterion: all tested groups supported → Supported; some
// supported → Partial; none → Failed. A criterion-level item (no connector)
// stands for the group when there is only one; otherwise it decides the
// verdict only until a connector is tested, and completes nothing. Latest is
// the newest result, except that a Partial or Failed criterion takes it from
// a group that didn't pass, so the card shows why.
func groupStatus(groups []Group, items []Item, unsupported string, hasUnsupported bool, currentMajor int) CapStatus {
	byConn := map[string][]Item{}
	var general []Item
	for _, it := range items {
		if it.Connector == "" {
			general = append(general, it)
		} else {
			byConn[it.Connector] = append(byConn[it.Connector], it)
		}
	}
	cs := CapStatus{Ports: map[string]PortStatus{}, GroupsTotal: len(groups)}
	use := func(l *Item, conflict, stale bool) {
		if l != nil && (cs.Latest == nil || newer(*l, *cs.Latest)) {
			cs.Latest = l
		}
		cs.Conflict = cs.Conflict || conflict
		cs.Stale = cs.Stale || stale
	}
	var supported, failed, partial int
	var why *Item // the newest result on a group that didn't pass
	for _, g := range groups {
		var gv Verdict = Untested
		passedBy, tested := "", false
		own := map[string]CapStatus{}
		for _, cn := range g.Connectors {
			pc := capStatus(byConn[cn], "", false, currentMajor)
			own[cn] = pc
			if pc.Latest == nil {
				continue
			}
			tested = true
			use(pc.Latest, pc.Conflict, pc.Stale)
			switch {
			case pc.Verdict == Supported && passedBy == "":
				passedBy = cn
			case pc.Verdict == Partial && gv != Supported:
				gv = Partial
			case pc.Verdict == Failed && gv == Untested:
				gv = Failed
			}
		}
		if passedBy != "" {
			gv = Supported
		}
		for _, cn := range g.Connectors {
			if l := own[cn].Latest; gv != Supported && l != nil && (why == nil || newer(*l, *why)) {
				why = l
			}
		}
		if !tested && len(groups) == 1 && len(general) > 0 {
			gs := capStatus(general, "", false, currentMajor)
			gv, tested = gs.Verdict, true
			use(gs.Latest, gs.Conflict, gs.Stale)
			if gv == Supported {
				passedBy = "the whole criterion"
			}
		}
		for _, cn := range g.Connectors {
			ps := PortStatus{Verdict: own[cn].Verdict}
			if gv == Supported && ps.Verdict == Untested {
				ps.CoveredBy = passedBy
			}
			ps.Suspect = gv == Supported && (ps.Verdict == Failed || ps.Verdict == Partial)
			cs.Ports[cn] = ps
		}
		if !tested {
			continue
		}
		cs.GroupsTested++
		switch gv {
		case Supported:
			supported++
		case Failed:
			failed++
		default:
			partial++
		}
	}
	cs.GroupsPassed = supported
	switch {
	case cs.GroupsTested == 0:
		g := capStatus(general, "", false, currentMajor)
		cs.Verdict, cs.Latest, cs.Conflict, cs.Stale = g.Verdict, g.Latest, g.Conflict, g.Stale
	case supported == cs.GroupsTested:
		cs.Verdict = Supported
	case supported > 0 || partial > 0:
		cs.Verdict = Partial
	default:
		cs.Verdict = Failed
	}
	// A criterion that didn't pass shows why: the latest result from a group
	// that didn't pass, not a passing port's.
	if (cs.Verdict == Partial || cs.Verdict == Failed) && why != nil {
		cs.Latest = why
	}
	if cs.Conflict && cs.Verdict == Supported {
		cs.Verdict = Partial
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
		var cs CapStatus
		if len(c.Groups) > 0 {
			cs = groupStatus(c.Groups, byCap[c.ID], reason, flagged, in.CurrentMajor)
			if !cs.Complete() {
				st.Incomplete++
			}
		} else {
			cs = capStatus(byCap[c.ID], reason, flagged, in.CurrentMajor)
		}
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

// Verified reports whether every applicable capability passed, in every
// port group it applies to, with no conflicts: the Omarchy badge and the
// compatibility-coverage numerator.
func (s ConfigStatus) Verified() bool {
	return s.Verdict != NotCompatible && s.Applicable > 0 && s.Counts.Supported == s.Applicable && s.Conflicts == 0 && s.Incomplete == 0
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
