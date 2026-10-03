package fixes

import (
	"context"
	"fmt"
	"time"

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

// OpenIssue creates a fix issue for a criterion and records it.
func OpenIssue(ctx context.Context, c *Client, st *store.Store, capability, criterionName string, sc Scope, affected []Affected, who string) (store.Fix, error) {
	if (sc.Component == "") == (sc.Config == "") {
		return store.Fix{}, fmt.Errorf("a fix covers a component or a configuration")
	}
	labels := []struct{ name, color, desc string }{
		{LabelCriterion + capability, colorCriterion, criterionName},
		{sc.label(), colorScope, sc.Name},
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
	f := store.Fix{Issue: is.Number, Capability: capability, Component: sc.Component, Config: sc.Config, Title: is.Title,
		URL: is.HTMLURL, Open: true, LastActivity: time.Now().UTC().Format(time.RFC3339), OpenedBy: who}
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
