package builds

import (
	"fmt"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// Regressions flags items of a new report that fail or only partly work on
// a newer build than the result that currently passes them, in the view the
// report counts toward: stable for a stable release, newest for any other
// channel (PLAN §28.2). A maintainer then decides whether it's a real
// regression or a broken development build.
func Regressions(c *catalog.Catalog, ru *store.Rollup, r *results.Result) []results.Flag {
	m, cfg, excl := find(c, r.ConfigID)
	if cfg == nil {
		return nil
	}
	view := store.StableView
	if r.Channel != status.Stable {
		view = store.NewestView
	}
	now := status.Config(ru.StatusInput(c, m, cfg, excl, c.Applicable(m, cfg), view))
	built := r.BuiltAt
	if built == "" {
		built = r.TestedAt
	}
	names := map[string]string{}
	for _, cp := range c.Capabilities {
		names[cp.ID] = cp.Name
	}
	var flags []results.Flag
	for _, it := range r.Items {
		if !it.Applicable || (it.Status != "failed" && it.Status != "partial") {
			continue
		}
		cs, ok := now.Caps[it.Capability]
		if !ok || cs.Latest == nil || cs.Latest.Verdict != status.Supported || built <= cs.Latest.BuiltAt {
			continue
		}
		was := cs.Latest
		flags = append(flags, results.Flag{Kind: results.FlagRegression, Detail: fmt.Sprintf(
			"%s: %s on Omarchy %s (%s, built %s); passed on Omarchy %s (%s, built %s). Accept if it's a real regression, reject a broken development build",
			names[it.Capability], it.Status, r.Omarchy, r.Channel, day(built), was.Omarchy, was.Channel, day(was.BuiltAt))})
	}
	return flags
}

func find(c *catalog.Catalog, id string) (*catalog.Mac, *catalog.Config, string) {
	for _, m := range c.Macs {
		for ri := range m.Releases {
			for ci := range m.Releases[ri].Configs {
				if cfg := &m.Releases[ri].Configs[ci]; cfg.ID == id {
					return m, cfg, c.CoverageExclusion(m, &m.Releases[ri])
				}
			}
		}
	}
	return nil, nil, ""
}

func day(ts string) string {
	if d, _, ok := strings.Cut(ts, "T"); ok {
		return d
	}
	return ts
}
