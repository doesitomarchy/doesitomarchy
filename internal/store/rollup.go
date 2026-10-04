package store

import (
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
)

// View is which results a status counts (PLAN §28.2).
type View int

const (
	// StableView counts stable releases only: every verdict, coverage figure,
	// list and fix-tracking state.
	StableView View = iota
	// NewestView counts every channel (rc, beta, edge, dev too): the "newer
	// build" line under a criterion whose result differs.
	NewestView
)

// StatusInput is what the status engine needs to judge one configuration in
// a view: its applicable criteria (with their port groups) and its accepted
// results. excl is the release's coverage exclusion
// (catalog.CoverageExclusion). The site, the CLI and fix tracking all build
// their input here, so they agree.
func (r *Rollup) StatusInput(c *catalog.Catalog, m *catalog.Mac, cfg *catalog.Config, excl string, applicable []catalog.Capability, view View) status.ConfigInput {
	cats := make(map[string]catalog.Category, len(c.Categories))
	for _, k := range c.Categories {
		cats[k.ID] = k
	}
	groups := c.CriterionGroups(m, cfg)
	caps := make([]status.Capability, len(applicable))
	for i, cp := range applicable {
		k := cats[cp.Category()]
		var gs []status.Group
		for _, g := range groups[cp.ID] {
			gs = append(gs, status.Group{ID: g.ID, Connectors: g.Connectors})
		}
		caps[i] = status.Capability{ID: cp.ID, Label: k.Name + " → " + cp.Name, Blocking: k.Blocking, Groups: gs}
	}
	in := status.ConfigInput{HardBlocker: m.HardBlocker, Excluded: excl, Caps: caps, Items: r.Items[cfg.ID],
		Unsupported: r.Unsupported[cfg.ID], Results: r.Results[cfg.ID], LatestResult: r.Latest[cfg.ID], CurrentMajor: r.CurrentMajor}
	if view == StableView {
		var stable []status.Item
		for _, it := range in.Items {
			if it.Channel == status.Stable {
				stable = append(stable, it)
			}
		}
		in.Items, in.Results, in.LatestResult = stable, r.StableResults[cfg.ID], r.StableLatest[cfg.ID]
	}
	return in
}
