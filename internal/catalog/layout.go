package catalog

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Port layouts (PLAN.md §25): data/layouts/<Identifier>.yaml gives each
// release's physical connectors. They must add up to every configuration's
// `ports` counts, which were reviewed with the catalog.

var reConnectorID = regexp.MustCompile(`^(left|right|back|front|top)-([1-9][0-9]?)$`)

// Sides in display order.
var Sides = []string{"left", "right", "back", "front", "top"}

// ethernetType is a layout connector type that stands for whichever Ethernet
// class the configuration has (Gigabit or 10Gb options in the same place).
const ethernetType = "ethernet"

func (l *loader) validateLayouts() {
	c := l.cat
	caps := map[string]bool{}
	for _, cp := range c.Capabilities {
		caps[cp.ID] = true
	}
	for name, p := range c.Vocab.Ports {
		for _, t := range p.Tests {
			if !caps[t] {
				l.errf("vocabulary.yaml", "ports.%s.tests: unknown capability %q", name, t)
			}
		}
	}
	macs := map[string]*Mac{}
	for _, m := range c.Macs {
		macs[m.Identifier] = m
	}
	seen := map[string]bool{}
	for _, lf := range c.Layouts {
		p := lf.File
		m := macs[lf.Identifier]
		switch {
		case m == nil:
			l.errf(p, "identifier %q is not in the catalog", lf.Identifier)
			continue
		case path.Base(p) != FileSlug(lf.Identifier)+".yaml":
			l.errf(p, "file name must be %s.yaml", FileSlug(lf.Identifier))
		case seen[lf.Identifier]:
			l.errf(p, "a second layout file for %s", lf.Identifier)
			continue
		}
		seen[lf.Identifier] = true
		for rid, rl := range lf.Releases {
			ri := -1
			for i := range m.Releases {
				if m.Releases[i].ID == rid {
					ri = i
				}
			}
			if ri < 0 {
				l.errf(p, "release %q is not a release of %s", rid, lf.Identifier)
				continue
			}
			where := fmt.Sprintf("releases.%s", rid)
			if len(rl.Sources) == 0 {
				l.errf(p, "%s: sources are required", where)
			}
			for _, u := range rl.Sources {
				if !reURL.MatchString(u) {
					l.errf(p, "%s: source %q is not a URL", where, u)
				}
			}
			l.checkConnectors(p, where, rl.Connectors)
			r := &m.Releases[ri]
			for cid := range rl.Configs {
				found := false
				for _, cfg := range r.Configs {
					found = found || cfg.ID == cid
				}
				if !found {
					l.errf(p, "%s.configs: %q is not a configuration of this release", where, cid)
				} else {
					l.checkConnectors(p, where+".configs."+cid, rl.Configs[cid])
				}
			}
			for ci := range r.Configs {
				cfg := &r.Configs[ci]
				conns := rl.Connectors
				if own, ok := rl.Configs[cfg.ID]; ok {
					conns = own
				}
				if len(conns) == 0 {
					continue
				}
				if msg := l.countsMatch(conns, cfg.Ports); msg != "" {
					l.errf(p, "%s: the layout doesn't match %s's ports: %s", where, cfg.ID, msg)
					continue
				}
				cfg.Connectors, cfg.LayoutSources, cfg.LayoutNote = conns, rl.Sources, rl.Note
			}
		}
	}
}

func (l *loader) checkConnectors(p, where string, conns []Connector) {
	v := &l.cat.Vocab
	ids := map[string]bool{}
	perSide := map[string][]int{}
	for i, cn := range conns {
		at := fmt.Sprintf("%s.connectors[%d]", where, i)
		m := reConnectorID.FindStringSubmatch(cn.ID)
		if m == nil {
			l.errf(p, "%s: id %q must be <side>-<n> (left, right, back, front or top)", at, cn.ID)
			continue
		}
		if ids[cn.ID] {
			l.errf(p, "%s: id %q appears twice", at, cn.ID)
		}
		ids[cn.ID] = true
		var n int
		fmt.Sscan(m[2], &n)
		perSide[m[1]] = append(perSide[m[1]], n)
		_, port := v.Ports[cn.Type]
		_, power := v.PowerConn[cn.Type]
		if !port && !power && cn.Type != ethernetType {
			l.errf(p, "%s: type %q is not a port class, power connector or %q", at, cn.Type, ethernetType)
		}
		for _, a := range cn.Also {
			if _, ok := v.Ports[a]; !ok {
				l.errf(p, "%s: also %q is not a port class", at, a)
			}
		}
	}
	for side, ns := range perSide {
		sort.Ints(ns)
		for i, n := range ns {
			if n != i+1 {
				l.errf(p, "%s: %s connectors must be numbered 1 to %d", where, side, len(ns))
				break
			}
		}
	}
}

// countsMatch compares a layout with a configuration's port counts; it
// returns "" when they agree, else what differs.
func (l *loader) countsMatch(conns []Connector, ports map[string]int) string {
	got := map[string]int{}
	ethernet := ""
	for p := range ports {
		if strings.HasPrefix(p, "ethernet-") {
			ethernet = p
		}
	}
	for _, cn := range conns {
		t := cn.Type
		if _, power := l.cat.Vocab.PowerConn[t]; power {
			continue
		}
		if t == ethernetType {
			t = ethernet
			if t == "" {
				t = "ethernet (none in ports)"
			}
		}
		got[t]++
		for _, a := range cn.Also {
			got[a]++
		}
	}
	var diffs []string
	keys := map[string]bool{}
	for k := range got {
		keys[k] = true
	}
	for k := range ports {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	for _, k := range sorted {
		if got[k] != ports[k] {
			diffs = append(diffs, fmt.Sprintf("%s %d in the layout, %d in ports", k, got[k], ports[k]))
		}
	}
	return strings.Join(diffs, "; ")
}

// ConnectorCriteria returns the criteria one connector is tested for on a
// configuration: its port classes' tests that apply to the configuration,
// plus USB-C charging where the configuration has it.
func (c *Catalog) ConnectorCriteria(m *Mac, cfg *Config, cn Connector) []string {
	applies := map[string]bool{}
	for _, cp := range c.Applicable(m, cfg) {
		applies[cp.ID] = true
	}
	classes := append([]string{cn.Type}, cn.Also...)
	var out []string
	add := func(id string) {
		if !applies[id] {
			return
		}
		for _, x := range out {
			if x == id {
				return
			}
		}
		out = append(out, id)
	}
	for _, cl := range classes {
		if cl == ethernetType {
			add("network.ethernet")
			continue
		}
		for _, t := range c.Vocab.Ports[cl].Tests {
			add(t)
		}
		if cl == "usb-c" || cl == "thunderbolt-3" {
			add("ports.usb-c-charging")
		}
	}
	return out
}

// CriterionConnectors maps each per-connector criterion of a configuration
// to the IDs of the connectors it's tested on, in layout order. It's empty
// when the configuration has no layout.
func (c *Catalog) CriterionConnectors(m *Mac, cfg *Config) map[string][]string {
	out := map[string][]string{}
	for _, cn := range cfg.Connectors {
		for _, id := range c.ConnectorCriteria(m, cfg, cn) {
			out[id] = append(out[id], cn.ID)
		}
	}
	return out
}

// ConnectorName is a connector's type in words ("Thunderbolt 2", "MagSafe 2 power").
func (c *Catalog) ConnectorName(cn Connector) string {
	if p, ok := c.Vocab.Ports[cn.Type]; ok {
		return p.Name
	}
	if p, ok := c.Vocab.PowerConn[cn.Type]; ok {
		return p.Name
	}
	if cn.Type == ethernetType {
		return "Ethernet"
	}
	return cn.Type
}
