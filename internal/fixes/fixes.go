package fixes

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

func errorsAs(err error, target any) bool { return errors.As(err, target) }

// Labels that link an issue to what it fixes, and mark a proposed fix.
const (
	LabelCriterion   = "criterion:"
	LabelComponent   = "component:"
	LabelConfig      = "config:"
	LabelProposed    = "fix-proposed"
	LabelUnsupported = "unsupported"
)

// State is how a fix stands, derived from its issue.
type State string

const (
	Open       State = "open"        // nobody has claimed it
	Claimed    State = "claimed"     // assigned, with recent activity
	Stale      State = "stale"       // assigned, but quiet for StaleAfter: reads as open
	Proposed   State = "proposed"    // a fix is proposed (label or open linked pull request)
	Fixed      State = "fixed"       // closed as completed
	NotPlanned State = "not-planned" // closed as not planned: no fix (shown in /admin only)
	Duplicate  State = "duplicate"   // closed as a duplicate: another issue tracks the fix (ignored)
)

// StaleAfter is how long a claim lasts without activity (PLAN §20.1).
const StaleAfter = 60 * 24 * time.Hour

// closedFixed reports whether an issue closed with this reason means the fix
// landed. GitHub's reasons are completed, not_planned and duplicate; issues
// closed before reasons existed have none, and count as completed.
func closedFixed(reason string) bool { return reason == "completed" || reason == "" }

// StateOf derives a fix's state at a time.
func StateOf(f store.Fix, now time.Time) State {
	if !f.Open {
		switch {
		case closedFixed(f.StateReason):
			return Fixed
		case f.StateReason == "duplicate":
			return Duplicate
		}
		return NotPlanned
	}
	if f.Proposed {
		return Proposed
	}
	if f.Assignee != "" {
		if last, err := time.Parse(time.RFC3339, f.LastActivity); err == nil && now.Sub(last) > StaleAfter {
			return Stale
		}
		return Claimed
	}
	return Open
}

// Label reads as people do.
func (s State) Label() string {
	switch s {
	case Claimed:
		return "Being worked on"
	case Stale:
		return "Open (stale claim)"
	case Proposed:
		return "Fix proposed"
	case Fixed:
		return "Fixed"
	case NotPlanned:
		return "Closed without a fix"
	case Duplicate:
		return "Closed as a duplicate"
	default:
		return "Open"
	}
}

// FromIssue makes the site's copy of an issue, or ok=false when the issue
// isn't a fix (no criterion: label, or neither a component: nor config:).
func FromIssue(is Issue, timeline []Event, repo string) (f store.Fix, ok bool) {
	f = store.Fix{Issue: is.Number, Title: is.Title, URL: is.HTMLURL, Open: is.State == "open", StateReason: is.StateReason}
	for _, l := range is.Labels {
		switch {
		case strings.HasPrefix(l.Name, LabelCriterion):
			f.Capability = strings.TrimPrefix(l.Name, LabelCriterion)
		case strings.HasPrefix(l.Name, LabelComponent):
			f.Component = strings.TrimPrefix(l.Name, LabelComponent)
		case strings.HasPrefix(l.Name, LabelConfig):
			f.Config = strings.TrimPrefix(l.Name, LabelConfig)
		case l.Name == LabelProposed:
			f.Proposed = true
		}
	}
	if f.Capability == "" || (f.Component == "") == (f.Config == "") {
		return f, false
	}
	if is.Assignee != nil {
		f.Assignee = is.Assignee.Login
	}
	last := is.UpdatedAt
	if is.ClosedAt != nil {
		f.ClosedAt = is.ClosedAt.UTC().Format(time.RFC3339)
	}
	var mergedPR, closingCommit string
	for _, ev := range timeline {
		if ev.CreatedAt.After(last) {
			last = ev.CreatedAt
		}
		switch ev.Event {
		case "cross-referenced":
			if ev.Source == nil || ev.Source.Issue == nil || ev.Source.Issue.PullRequest == nil {
				continue
			}
			pr := ev.Source.Issue
			if pr.State == "open" {
				f.Proposed = true
			}
			if pr.PullRequest.MergedAt != nil {
				mergedPR = pr.HTMLURL
			}
		case "closed":
			if ev.CommitID != "" {
				closingCommit = commitHTML(ev.CommitURL, ev.CommitID, repo)
			}
		}
	}
	f.LastActivity = last.UTC().Format(time.RFC3339)
	if !f.Open {
		f.Proposed = false
		if closedFixed(f.StateReason) {
			f.FixLink = closingCommit
			if f.FixLink == "" {
				f.FixLink = mergedPR
			}
		}
	}
	return f, true
}

// commitHTML turns an API commit URL into the commit's web page.
func commitHTML(apiURL, sha, repo string) string {
	if rest, ok := strings.CutPrefix(apiURL, "https://api.github.com/repos/"); ok {
		if owner, after, ok := strings.Cut(rest, "/"); ok {
			if name, _, ok := strings.Cut(after, "/"); ok {
				return "https://github.com/" + owner + "/" + name + "/commit/" + sha
			}
		}
	}
	return "https://github.com/" + repo + "/commit/" + sha
}

// Covers reports whether a fix applies to a criterion on a configuration
// with the given components.
func Covers(f store.Fix, capability, configID string, components []string) bool {
	if f.Capability != capability {
		return false
	}
	if f.Config != "" {
		return f.Config == configID
	}
	for _, c := range components {
		if c == f.Component {
			return true
		}
	}
	return false
}

// Best picks the fix to show for a criterion on a configuration: one still
// in progress before a fixed one, and the newest of those; never one closed
// as not planned or as a duplicate.
func Best(all []store.Fix, capability, configID string, components []string, now time.Time) (store.Fix, State, bool) {
	rank := map[State]int{Proposed: 5, Claimed: 4, Open: 3, Stale: 3, Fixed: 2}
	var best store.Fix
	var bestState State
	found := false
	for _, f := range all {
		if !Covers(f, capability, configID, components) {
			continue
		}
		st := StateOf(f, now)
		if rank[st] == 0 {
			continue
		}
		if !found || rank[st] > rank[bestState] || (rank[st] == rank[bestState] && f.Issue > best.Issue) {
			best, bestState, found = f, st, true
		}
	}
	return best, bestState, found
}

// NextChange is when the first fix's state changes by time alone (a claim
// going stale), after now; zero if none will. The site rebuilds then, so
// every page shows the same state.
func NextChange(all []store.Fix, now time.Time) time.Time {
	var next time.Time
	for _, f := range all {
		if !f.Open || f.Proposed || f.Assignee == "" {
			continue
		}
		last, err := time.Parse(time.RFC3339, f.LastActivity)
		if err != nil {
			continue
		}
		if at := last.Add(StaleAfter); at.After(now) && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	return next
}

// Affected is one configuration a fix would cover, for the issue's body.
type Affected struct {
	ConfigID, Name, URL string
	Failed              bool   // its latest accepted result for the criterion failed or only partly worked
	Evidence            string // that result's evidence
	Report              string // that result's public URL
}

// Issue text for a new fix.
func IssueText(criterionName, scopeName string, affected []Affected) (title, body string) {
	title = criterionName + " on " + scopeName
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** fails on Omarchy with **%s**.\n\n", criterionName, scopeName)
	var failed, others []Affected
	for _, a := range affected {
		if a.Failed {
			failed = append(failed, a)
		} else {
			others = append(others, a)
		}
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].Name < failed[j].Name })
	sort.Slice(others, func(i, j int) bool { return others[i].Name < others[j].Name })
	if len(failed) > 0 {
		b.WriteString("### Where it failed\n\n")
		for _, a := range failed {
			fmt.Fprintf(&b, "- [%s](%s)", a.Name, a.URL)
			if a.Report != "" {
				fmt.Fprintf(&b, " · [report](%s)", a.Report)
			}
			b.WriteString("\n")
			if ev := strings.TrimSpace(a.Evidence); ev != "" {
				if len(ev) > 1500 {
					ev = ev[:1500] + "…"
				}
				fmt.Fprintf(&b, "\n  ```\n  %s\n  ```\n\n", strings.ReplaceAll(ev, "\n", "\n  "))
			}
		}
		b.WriteString("\n")
	}
	if len(others) > 0 {
		b.WriteString("### Also covered by this fix\n\n")
		for _, a := range others {
			fmt.Fprintf(&b, "- [%s](%s)\n", a.Name, a.URL)
		}
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	b.WriteString("Want to help? Comment here, or ask to be assigned: the assignee is shown on doesitomarchy.com as working on it. ")
	b.WriteString("Link your pull request to this issue; when it closes as completed, the affected Macs are asked to re-test.\n")
	return title, b.String()
}
