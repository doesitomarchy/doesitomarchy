package fixes

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// Label colours in the fix repo.
const (
	colorCriterion = "1d76db"
	colorScope     = "5319e7"
	colorProposed  = "0e8a16"
	colorGivenUp   = "6a1b1b"
)

// Scope is what a fix covers: a component, or a single configuration.
type Scope struct {
	Component, Config string
	Name              string // for people: "Cirrus Logic CS8409" or "MacBook Pro (13-inch, 2020) · Core i5"
}

func (sc Scope) label() string {
	if sc.Component != "" {
		return LabelComponent + sc.Component
	}
	return LabelConfig + sc.Config
}

// AlreadyOpenError is OpenIssue's answer when the fix repo already has an
// open issue for the criterion and scope.
type AlreadyOpenError struct {
	Issue Issue
}

func (e *AlreadyOpenError) Error() string {
	return fmt.Sprintf("issue #%d is already open for this: %s", e.Issue.Number, e.Issue.HTMLURL)
}

// opening lets one fix issue be opened at a time in this process, so a
// double click can't race past the check for an open one.
var opening sync.Mutex

// OpenIssue creates a fix issue for a criterion and records it. If the fix
// repo already has an open issue with the same labels (opened from /admin,
// the CLI or by hand), it returns an *AlreadyOpenError instead.
func OpenIssue(ctx context.Context, c *Client, st *store.Store, capability, criterionName string, sc Scope, affected []Affected, who string) (store.Fix, error) {
	if (sc.Component == "") == (sc.Config == "") {
		return store.Fix{}, fmt.Errorf("a fix covers a component or a configuration")
	}
	labels := []struct{ name, color, desc string }{
		{LabelCriterion + capability, colorCriterion, criterionName},
		{sc.label(), colorScope, sc.Name},
	}
	opening.Lock()
	defer opening.Unlock()
	open, err := c.OpenIssuesLabelled(ctx, []string{labels[0].name, labels[1].name})
	if err != nil {
		return store.Fix{}, err
	}
	if len(open) > 0 {
		return store.Fix{}, &AlreadyOpenError{open[0]}
	}
	for _, l := range labels {
		if err := c.EnsureLabel(ctx, l.name, l.color, truncate(l.desc, 100)); err != nil {
			return store.Fix{}, err
		}
	}
	title, body := IssueText(criterionName, sc.Name, affected)
	is, err := c.CreateIssue(ctx, title, body, []string{labels[0].name, labels[1].name})
	if err != nil {
		return store.Fix{}, err
	}
	// GitHub's own time, so the webhook's sync of the new issue isn't older.
	last := is.UpdatedAt
	if last.IsZero() {
		last = time.Now()
	}
	f := store.Fix{Issue: is.Number, Capability: capability, Component: sc.Component, Config: sc.Config, Title: is.Title,
		URL: is.HTMLURL, Open: true, LastActivity: last.UTC().Format(time.RFC3339), OpenedBy: who}
	if _, err := st.UpsertFix(ctx, f); err != nil {
		return f, err
	}
	return f, nil
}

// GiveUp records the maintainer's white flag on the fix's issue: a comment
// with the reason, the unsupported label, and closed as not planned.
func GiveUp(ctx context.Context, c *Client, issue int, reason, who string) error {
	if err := c.EnsureLabel(ctx, LabelUnsupported, colorGivenUp, "A maintainer marked this Unsupported after attempts to fix it"); err != nil {
		return err
	}
	if err := c.Comment(ctx, issue, fmt.Sprintf("Marked **Unsupported** on doesitomarchy.com by %s: %s\n\nIf a fix appears, reopen this issue and a maintainer will lift the flag.", who, reason)); err != nil {
		return err
	}
	if err := c.AddLabels(ctx, issue, []string{LabelUnsupported}); err != nil {
		return err
	}
	return c.Close(ctx, issue, "not_planned")
}

// GiveUpAll closes every open fix issue for a criterion and scope as not
// planned, with the maintainer's reason (PLAN §26), and syncs each one back
// so the site shows it closed. It returns the issues it closed.
func GiveUpAll(ctx context.Context, sy *Syncer, capability, component, config, reason, who string) ([]int, error) {
	all, err := sy.Store.Fixes(ctx) // not a snapshot: an issue opened a moment ago counts
	if err != nil {
		return nil, err
	}
	var closed []int
	for _, f := range all {
		if f.Capability != capability || f.Component != component || f.Config != config || !f.Open {
			continue
		}
		if err := GiveUp(ctx, sy.Client, f.Issue, reason, who); err != nil {
			return closed, fmt.Errorf("closing issue #%d: %w", f.Issue, err)
		}
		closed = append(closed, f.Issue)
		if _, err := sy.SyncIssue(ctx, f.Issue); err != nil {
			return closed, fmt.Errorf("issue #%d closed, but reading it back failed: %w", f.Issue, err)
		}
	}
	return closed, nil
}

// ScopeName names what a fix covers, for people: the component's name, or
// the configuration's release and label.
func ScopeName(c *catalog.Catalog, component, config string) string {
	if component != "" {
		if comp := c.Components[component]; comp != nil {
			return comp.Name
		}
		return component
	}
	for _, m := range c.Macs {
		for _, r := range m.Releases {
			for _, cfg := range r.Configs {
				if cfg.ID == config {
					return r.Name + " · " + cfg.Label
				}
			}
		}
	}
	return config
}

// AffectedConfigs lists the configurations a fix would cover, for the
// issue's body: each with whether the criterion currently fails or only
// partly works there (as the site shows it), and if so the latest report's
// evidence and link. baseURL is the public site.
func AffectedConfigs(c *catalog.Catalog, ru *store.Rollup, capability, component, config, baseURL string) []Affected {
	scope := store.Fix{Capability: capability, Component: component, Config: config}
	var out []Affected
	for _, m := range c.Macs {
		for ri := range m.Releases {
			r := &m.Releases[ri]
			excl := c.CoverageExclusion(m, r)
			for ci := range r.Configs {
				cfg := &r.Configs[ci]
				if !Covers(scope, capability, cfg.ID, cfg.Components) {
					continue
				}
				applicable := c.Applicable(m, cfg)
				applies := false
				for _, cp := range applicable {
					applies = applies || cp.ID == capability
				}
				if !applies {
					continue
				}
				cs := status.Config(ru.StatusInput(c, m, cfg, excl, applicable)).Caps[capability]
				a := Affected{ConfigID: cfg.ID, Name: m.Identifier + " · " + r.Name + " · " + cfg.Label,
					URL:    baseURL + "/mac/" + url.PathEscape(catalog.FileSlug(m.Identifier)) + "#cfg-" + cfg.ID,
					Failed: cs.Verdict == status.Failed || cs.Verdict == status.Partial}
				if a.Failed && cs.Latest != nil {
					a.Evidence = cs.Latest.Evidence
					for _, rs := range ru.Accepted[cfg.ID] {
						if rs.ID == cs.Latest.ResultID {
							a.Report = baseURL + "/report/" + rs.Code
						}
					}
				}
				out = append(out, a)
			}
		}
	}
	return out
}

// EnsureProposedLabel makes sure the fix-proposed label exists, so
// contributors can add it.
func EnsureProposedLabel(ctx context.Context, c *Client) error {
	return c.EnsureLabel(ctx, LabelProposed, colorProposed, "A fix is proposed: a pull request, patch or package to try")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
