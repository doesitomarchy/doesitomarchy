// Package search is the in-memory catalog search engine (PLAN.md §7, §16):
// a query parser with field:value syntax and aliases, shape recognition for
// identifiers and part numbers, typo tolerance, and ranking. Results are
// grouped by model identifier with the matching configurations.
package search

import (
	"sort"
	"strconv"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
)

// Index is immutable after Build and safe for concurrent use.
type Index struct {
	docs       []*doc
	macs       []*macInfo
	aliases    map[string]string          // normalized phrase → query fragment
	ids        map[string]bool            // normalized identifiers ("macbookpro5,1")
	idFamilies map[string]bool            // "macbookpro5", "imac14" (identifier before the comma)
	keys       map[string]map[string]bool // key and number-ID fields: field → values present
	dict       []string                   // every free-text word, for typo tolerance
	dictSet    map[string]bool
	suggest    map[string][]suggestion // field → values, most frequent first
}

type macInfo struct {
	Identifier string
	Slug       string
	Line       string
	LineName   string
	configs    int
}

// doc is one configuration.
type doc struct {
	mac         *macInfo
	ConfigID    string
	Label       string
	ReleaseName string
	Announced   string
	year        float64
	sizes       []float64
	cores       []float64
	id          string                // normalized identifier
	keys        map[string][]string   // key / number-ID fields
	text        map[string][][]string // text fields: field → phrases (word lists)
	bag         [][]string            // free-text phrases
	names       map[string][]string   // display values per field, for suggestions
}

// Verdicts gives each config's current verdict; Build falls back to the
// catalog-only status (Untested / Not compatible) for configs not in it.
type Verdicts map[string]status.Verdict

// Build indexes the catalog. Aliases come from c.Aliases.
func Build(c *catalog.Catalog, verdicts Verdicts) *Index {
	ix := &Index{
		aliases: map[string]string{}, ids: map[string]bool{}, idFamilies: map[string]bool{},
		keys: map[string]map[string]bool{}, dictSet: map[string]bool{}, suggest: map[string][]suggestion{},
	}
	for _, a := range c.Aliases {
		for _, m := range a.Match {
			ix.aliases[catalog.NormalizePhrase(m)] = a.Means
		}
	}
	for _, m := range c.Macs {
		line := c.Vocab.Lines[m.Line]
		mi := &macInfo{Identifier: m.Identifier, Slug: catalog.FileSlug(m.Identifier), Line: m.Line, LineName: line.Name}
		ix.macs = append(ix.macs, mi)
		id := normID(m.Identifier)
		ix.ids[id] = true
		ix.idFamilies[id[:strings.IndexByte(id, ',')]] = true
		for ri := range m.Releases {
			r := &m.Releases[ri]
			for ci := range r.Configs {
				cfg := &r.Configs[ci]
				mi.configs++
				v, ok := verdicts[cfg.ID]
				if !ok {
					v = status.Config(status.ConfigInput{HardBlocker: m.HardBlocker}).Verdict
				}
				ix.docs = append(ix.docs, ix.newDoc(c, m, mi, r, cfg, line, v))
			}
		}
	}
	for _, d := range ix.docs {
		for f, vs := range d.keys {
			if ix.keys[f] == nil {
				ix.keys[f] = map[string]bool{}
			}
			for _, v := range vs {
				ix.keys[f][v] = true
			}
		}
		for _, ph := range d.bag {
			for _, w := range ph {
				if !ix.dictSet[w] {
					ix.dictSet[w] = true
					ix.dict = append(ix.dict, w)
				}
			}
		}
	}
	sort.Strings(ix.dict)
	ix.buildSuggestions()
	return ix
}

// portGroups adds friendly port names a query can use.
var portGroups = map[string][]string{
	"firewire-400": {"firewire"}, "firewire-800": {"firewire"},
	"thunderbolt-1": {"thunderbolt"}, "thunderbolt-2": {"thunderbolt"}, "thunderbolt-3": {"thunderbolt", "usb-c"},
	"usb-a-2": {"usb-a", "usb"}, "usb-a-3": {"usb-a", "usb"}, "usb-c": {"usb"},
	"sd-card": {"sd"}, "ethernet-fast": {"ethernet"}, "ethernet-gbe": {"ethernet"}, "ethernet-10gbe": {"ethernet"},
	"mini-displayport": {"displayport"}, "expresscard-34": {"expresscard"},
}

func releaseSeason(name string) string {
	for _, w := range words(name) {
		switch w {
		case "early", "mid", "late":
			return w
		}
	}
	return ""
}

func (ix *Index) newDoc(c *catalog.Catalog, m *catalog.Mac, mi *macInfo, r *catalog.Release, cfg *catalog.Config,
	line catalog.Line, v status.Verdict) *doc {
	d := &doc{
		mac: mi, ConfigID: cfg.ID, Label: cfg.Label, ReleaseName: r.Name, Announced: r.Announced,
		id: normID(m.Identifier), keys: map[string][]string{}, text: map[string][][]string{}, names: map[string][]string{},
	}
	if y, err := strconv.Atoi(r.Announced[:4]); err == nil {
		d.year = float64(y)
	}
	addKey := func(f string, vs ...string) {
		for _, v := range vs {
			if v != "" {
				d.keys[f] = append(d.keys[f], strings.ToLower(v))
			}
		}
	}
	addText := func(f string, s string) {
		if ws := words(s); len(ws) > 0 {
			d.text[f] = append(d.text[f], ws)
		}
	}
	addBag := func(s string) {
		if ws := words(s); len(ws) > 0 {
			d.bag = append(d.bag, ws)
		}
	}

	// Keys.
	addKey("line", m.Line)
	addKey("form", line.Form)
	addKey("chip", m.SecurityChip)
	addKey("efi", strconv.Itoa(m.EFI))
	addKey("release", releaseSeason(r.Name))
	addKey("status", string(v))
	if c.CoverageExclusion(m, r) != "" {
		addKey("scope", "out")
	} else {
		addKey("scope", "in")
	}
	for p := range cfg.Ports {
		addKey("port", p)
		addKey("port", portGroups[p]...)
	}
	addKey("feature", cfg.Features...)
	for _, o := range cfg.OrderNumbers {
		addKey("order", orderKey(o))
	}
	addKey("a", r.ModelNumbers...)
	addKey("emc", r.EMC...)
	addKey("board", m.BoardIDs...)

	// Display.
	if cfg.Display != nil {
		d.sizes = append(d.sizes, cfg.Display.Inches)
		addText("display", cfg.Display.Panel+" "+cfg.Display.Resolution)
	}
	addText("display", r.Name) // "Retina 5K", "Retina 4K"

	// CPU.
	for _, p := range append(append([]catalog.Processor{}, cfg.CPU.Standard...), cfg.CPU.BTO...) {
		addText("cpu", p.Model)
		d.names["cpu"] = append(d.names["cpu"], p.Model)
		d.cores = append(d.cores, float64(p.Cores))
		addBag(p.Model)
	}
	if cn, ok := c.Vocab.CPUCodenames[cfg.CPU.Codename]; ok {
		addText("arch", cfg.CPU.Codename+" "+cn.Name+" "+cn.Family)
		d.names["arch"] = append(d.names["arch"], cfg.CPU.Codename)
		addBag(cn.Name)
	}

	// Components (standard and build-to-order).
	for _, cid := range append(append([]string{}, cfg.Components...), cfg.BTOComponents...) {
		comp := c.Components[cid]
		if comp == nil {
			continue
		}
		desc := comp.Name + " " + comp.Vendor + " " + strings.ReplaceAll(strings.TrimPrefix(cid, comp.Kind+"/"), "-", " ")
		if strings.EqualFold(comp.Vendor, "ATI") {
			desc += " amd" // ATI became AMD; "gpu:amd" should find both
		}
		if comp.Role != "" {
			desc += " " + comp.Role
		}
		addText(comp.Kind, desc)
		d.names[comp.Kind] = append(d.names[comp.Kind], comp.Name)
		for _, hw := range comp.IDs {
			addKey("hw", hw, hw[4:]) // "pci:10de:0647" and "10de:0647"
		}
		if comp.Kind == "gpu" {
			addBag(comp.Name)
		}
	}

	// Free-text bag.
	addBag(m.Identifier + " " + d.id + " " + line.Name + " " + m.Line)
	addBag(r.Name)
	addBag(cfg.Label)
	if cfg.Display != nil {
		addBag(cfg.Display.Panel)
	}
	return d
}

// suggestion is one value offered for a field.
type suggestion struct {
	Value string
	Count int
}

func (ix *Index) buildSuggestions() {
	counts := map[string]map[string]int{}
	add := func(f, v string) {
		if v == "" {
			return
		}
		if counts[f] == nil {
			counts[f] = map[string]int{}
		}
		counts[f][v]++
	}
	for _, d := range ix.docs {
		for f, vs := range d.keys {
			for _, v := range vs {
				add(f, v)
			}
		}
		for f, ns := range d.names {
			for _, n := range ns {
				add(f, n)
			}
		}
		add("year", strconv.Itoa(int(d.year)))
		add("id", d.mac.Identifier)
	}
	for f, m := range counts {
		var s []suggestion
		for v, n := range m {
			s = append(s, suggestion{v, n})
		}
		sort.Slice(s, func(i, j int) bool {
			if s[i].Count != s[j].Count {
				return s[i].Count > s[j].Count
			}
			return s[i].Value < s[j].Value
		})
		ix.suggest[f] = s
	}
}
