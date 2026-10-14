// Package match is the server's side of pkg/match: it builds the match
// snapshot from the catalog, so identify_mac, the Identify page, the API's
// /match and report validation match exactly as a test tool does offline
// with GET /api/v1/snapshot.
package match

import (
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	pm "github.com/doesitomarchy/doesitomarchy/pkg/match"
)

// The matching types (pkg/match).
type (
	Probe     = pm.Probe
	Candidate = pm.Candidate
	Result    = pm.Result
	Matcher   = pm.Matcher
)

// New indexes a catalog.
func New(c *catalog.Catalog) *Matcher { return pm.New(Snapshot(c)) }

// NormalizeIDs turns IDs in any common form into "pci:vvvv:dddd" (or the
// given kind), lower-case, without duplicates.
func NormalizeIDs(ids []string, kind string) []string { return pm.NormalizeIDs(ids, kind) }

// ParseProbe extracts what /identify needs from pasted command output.
func ParseProbe(text string) Probe { return pm.ParseProbe(text) }

// Snapshot is what matching needs from a catalog, in catalog order.
func Snapshot(c *catalog.Catalog) pm.Snapshot {
	snap := pm.Snapshot{Macs: []pm.SnapshotMac{}}
	if len(c.Plumbing) > 0 {
		snap.Plumbing = map[string]pm.Plumbing{}
		for id, p := range c.Plumbing {
			snap.Plumbing[id] = pm.Plumbing{Name: p.Name, Class: p.Class}
		}
	}
	for _, m := range c.Macs {
		sm := pm.SnapshotMac{Identifier: m.Identifier, BoardIDs: m.BoardIDs, Releases: []pm.SnapshotRelease{}}
		if m.SecurityChip != "none" {
			sm.SecurityChip = m.SecurityChip
		}
		for _, r := range m.Releases {
			sr := pm.SnapshotRelease{Name: r.Name, BoardIDs: r.BoardIDs, Configs: []pm.SnapshotConfig{}}
			for _, cfg := range r.Configs {
				sc := pm.SnapshotConfig{ID: cfg.ID}
				if cfg.Display != nil {
					sc.Panel = cfg.Display.Resolution
				}
				sc.Ports = snapshotPorts(c, &cfg)
				for _, ref := range cfg.Components {
					if comp := c.Components[ref]; comp != nil && len(comp.IDs) > 0 {
						sc.Parts = append(sc.Parts, pm.SnapshotPart{Kind: comp.Kind, IDs: comp.IDs})
					}
				}
				for _, ref := range cfg.BTOComponents {
					if comp := c.Components[ref]; comp != nil {
						sc.BTO = append(sc.BTO, comp.IDs...)
					}
				}
				for _, p := range append(append([]catalog.Processor{}, cfg.CPU.Standard...), cfg.CPU.BTO...) {
					sc.CPUs = append(sc.CPUs, p.Model)
				}
				sr.Configs = append(sr.Configs, sc)
			}
			sm.Releases = append(sm.Releases, sr)
		}
		snap.Macs = append(snap.Macs, sm)
	}
	return snap
}

// snapshotPorts is a configuration's port map drawing as data, without the
// drawing's ports it lacks (PortmapHidden), and its sides left without any.
func snapshotPorts(c *catalog.Catalog, cfg *catalog.Config) []pm.SnapshotSide {
	drawing := c.Portmaps[cfg.Portmap]
	if cfg.Portmap == "" || drawing == nil {
		return nil
	}
	hidden := map[string]bool{}
	for _, id := range cfg.PortmapHidden {
		hidden[id] = true
	}
	var out []pm.SnapshotSide
	for _, sd := range drawing.Sides {
		ss := pm.SnapshotSide{Side: sd.Side, Length: sd.Length, Height: sd.Height, Start: sd.Start}
		for _, p := range sd.Ports {
			if !hidden[p.Conn] {
				ss.Ports = append(ss.Ports, pm.SnapshotPort{ID: p.Conn, Kind: p.Kind, Name: p.Name, X: p.X, Y: p.Y})
			}
		}
		if len(ss.Ports) > 0 {
			out = append(out, ss)
		}
	}
	return out
}
