package web

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
)

// The web pages render from view models built once from the loaded catalog
// at startup (the catalog is immutable while the process runs). Verdicts
// come from the status package; Phase 7 rebuilds them when results change.

// lineOrder is the display order of product lines; unknown lines sort after.
var lineOrder = []string{"macbook", "macbook-air", "macbook-pro", "imac", "imac-pro", "mac-mini", "mac-pro", "xserve"}

// kindOrder is the display order of component kinds on a config card.
var kindOrder = []string{"gpu", "wifi", "bluetooth", "audio", "camera", "ethernet", "storage", "thunderbolt", "card-reader", "firewire", "ir", "input", "bridge"}

// iconFor maps capability-category icon names (capabilities.yaml) to sprite IDs.
var iconFor = map[string]string{"power": "power", "gpu": "gpu", "monitor": "monitor", "keyboard": "keyboard",
	"battery": "battery", "speaker": "speaker", "camera": "camera", "wifi": "wifi", "plug": "plug",
	"disk": "disk", "fan": "fan", "chip": "chip"}

type site struct {
	Coverage   status.Coverage
	Macs       int
	Releases   int
	Configs    int
	Components int
	Criteria   int
	Exclusions []exclusion
	Themes     []themeChoice
	Lines      []lineStat
}

type exclusion struct {
	Reason string
	Count  int
}

type lineStat struct {
	Key, Name     string
	Macs, Configs int
	FirstYear     int
	LastYear      int
}

type macView struct {
	Identifier    string
	Slug          string
	LineKey       string
	LineName      string
	Title         string // newest release name
	Years         string // "2009" or "2009–2010"
	FirstYear     int
	EFI           int
	Chip          string // "", "Apple T1", "Apple T2"
	HardBlocker   string
	ResearchNotes string
	BoardIDs      []string
	Sources       []string
	Uncertain     []uncertainView
	Releases      []*releaseView
	Configs       []*configView
	GPUs          []string
	Arch          []string
	Verdicts      []status.Verdict // distinct, in rank order
	OutOfScope    string           // reason, when every config is out of coverage scope
	Matrix        []matrixRow
}

type releaseView struct {
	ID, Name, Announced, Discontinued string
	ModelNumbers, EMC                 []string
	Configs                           []*configView
}

type configView struct {
	ID           string
	Letter       string
	Label        string
	ReleaseName  string
	OrderNumbers []string
	BTOOnly      bool
	CPU          []string
	CPUBTO       []string
	Codename     string
	Memory       string
	MemoryNote   string
	Storage      string
	Display      string
	Components   []compView
	Ports        []string
	Features     []string
	Categories   []categoryView
	Status       status.ConfigStatus
	OutOfScope   string
	Notes        string
	Uncertain    []uncertainView
}

type compView struct {
	Kind      string
	KindName  string
	Name      string
	Role      string
	IDs       []string
	Driver    string
	BTO       bool
	Uncertain []uncertainView
}

type uncertainView struct{ Field, Note string }

type categoryView struct {
	ID, Name, Icon string
	Caps           []capView
}

type capView struct {
	ID, Name, Description string
	Verdict               status.Verdict // per-capability status: Untested until Phase 7
}

type matrixRow struct {
	Category string
	Icon     string
	Name     string
	ID       string
	Cells    []bool // applicable per config, in macView.Configs order
}

// catalogView holds everything the pages render.
type catalogView struct {
	site    site
	macs    []*macView
	bySlug  map[string]*macView    // lower-case slug → mac
	configs map[string]*configView // config ID → config
}

func buildView(c *catalog.Catalog) *catalogView {
	v := &catalogView{bySlug: map[string]*macView{}, configs: map[string]*configView{}}
	var statuses []status.ConfigStatus
	lineStats := map[string]*lineStat{}
	for _, m := range c.Macs {
		mv := buildMac(c, m)
		v.macs = append(v.macs, mv)
		v.bySlug[strings.ToLower(mv.Slug)] = mv
		ls := lineStats[m.Line]
		if ls == nil {
			ls = &lineStat{Key: m.Line, Name: c.Vocab.Lines[m.Line].Name, FirstYear: 9999}
			lineStats[m.Line] = ls
		}
		ls.Macs++
		for _, cv := range mv.Configs {
			statuses = append(statuses, cv.Status)
			v.configs[cv.ID] = cv
			ls.Configs++
		}
		for _, r := range mv.Releases {
			y, _ := strconv.Atoi(r.Announced[:4])
			ls.FirstYear = min(ls.FirstYear, y)
			ls.LastYear = max(ls.LastYear, y)
		}
	}
	sort.SliceStable(v.macs, func(i, j int) bool { return lessMac(v.macs[i], v.macs[j]) })

	s := &v.site
	s.Coverage = status.Summarize(statuses)
	st := c.Stats()
	s.Macs, s.Releases, s.Configs, s.Components, s.Criteria = st.Macs, st.Releases, st.Configs, st.Components, st.Capabilities
	s.Themes = themeChoices
	counts := map[string]int{}
	for _, mv := range v.macs {
		for _, cv := range mv.Configs {
			if cv.OutOfScope != "" && cv.Status.Verdict != status.NotCompatible {
				counts[cv.OutOfScope]++
			}
		}
	}
	for _, rule := range c.CoverageRules { // file order
		if n := counts[rule.Reason]; n > 0 {
			s.Exclusions = append(s.Exclusions, exclusion{rule.Reason, n})
			delete(counts, rule.Reason)
		}
	}
	for _, k := range orderedLines(lineStats) {
		s.Lines = append(s.Lines, *lineStats[k])
	}
	return v
}

func orderedLines[V any](m map[string]V) []string {
	rank := map[string]int{}
	for i, k := range lineOrder {
		rank[k] = i
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, iok := rank[keys[i]]
		rj, jok := rank[keys[j]]
		if iok != jok {
			return iok
		}
		if ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	return keys
}

func lineRank(key string) int {
	for i, k := range lineOrder {
		if k == key {
			return i
		}
	}
	return len(lineOrder)
}

// lessMac orders by product line, then identifier naturally (iMac9,1 < iMac10,1).
func lessMac(a, b *macView) bool {
	if ra, rb := lineRank(a.LineKey), lineRank(b.LineKey); ra != rb {
		return ra < rb
	}
	return lessIdentifier(a.Identifier, b.Identifier)
}

func idParts(id string) (string, int, int) {
	i := strings.IndexFunc(id, func(r rune) bool { return r >= '0' && r <= '9' })
	if i < 0 {
		return id, 0, 0
	}
	maj, min, _ := strings.Cut(id[i:], ",")
	a, _ := strconv.Atoi(maj)
	b, _ := strconv.Atoi(min)
	return id[:i], a, b
}

func lessIdentifier(a, b string) bool {
	fa, ma, na := idParts(a)
	fb, mb, nb := idParts(b)
	if fa != fb {
		return fa < fb
	}
	if ma != mb {
		return ma < mb
	}
	return na < nb
}

func buildMac(c *catalog.Catalog, m *catalog.Mac) *macView {
	mv := &macView{
		Identifier: m.Identifier, Slug: catalog.FileSlug(m.Identifier), LineKey: m.Line, LineName: c.Vocab.Lines[m.Line].Name,
		EFI: m.EFI, HardBlocker: m.HardBlocker, ResearchNotes: m.ResearchNotes, BoardIDs: m.BoardIDs, Sources: m.Sources,
	}
	if m.SecurityChip != "none" {
		mv.Chip = c.Vocab.SecurityChips[m.SecurityChip].Name
	}
	for _, u := range m.Uncertain {
		mv.Uncertain = append(mv.Uncertain, uncertainView{u.Field, u.Note})
	}
	first, last := 9999, 0
	gpus, archs := map[string]bool{}, map[string]bool{}
	verdicts := map[status.Verdict]bool{}
	scopes := map[string]bool{}
	letter := 0
	for ri := range m.Releases {
		r := &m.Releases[ri]
		rv := &releaseView{ID: r.ID, Name: r.Name, Announced: r.Announced, Discontinued: r.Discontinued, ModelNumbers: r.ModelNumbers, EMC: r.EMC}
		y, _ := strconv.Atoi(r.Announced[:4])
		first, last = min(first, y), max(last, y)
		mv.Title = r.Name
		excl := c.CoverageExclusion(m, r)
		for ci := range r.Configs {
			cfg := &r.Configs[ci]
			cv := buildConfig(c, m, r, cfg, excl)
			cv.Letter = string(rune('A' + letter%26))
			letter++
			rv.Configs = append(rv.Configs, cv)
			mv.Configs = append(mv.Configs, cv)
			verdicts[cv.Status.Verdict] = true
			scopes[excl] = true
			for _, comp := range cv.Components {
				if comp.Kind == "gpu" && !gpus[comp.Name] {
					gpus[comp.Name] = true
					mv.GPUs = append(mv.GPUs, comp.Name)
				}
			}
			if cv.Codename != "" && !archs[cv.Codename] {
				archs[cv.Codename] = true
				mv.Arch = append(mv.Arch, cv.Codename)
			}
		}
		mv.Releases = append(mv.Releases, rv)
	}
	mv.FirstYear = first
	mv.Years = strconv.Itoa(first)
	if last != first {
		mv.Years = fmt.Sprintf("%d–%d", first, last)
	}
	for _, v := range []status.Verdict{status.Supported, status.Partial, status.Unsupported, status.Untested, status.NotCompatible} {
		if verdicts[v] {
			mv.Verdicts = append(mv.Verdicts, v)
		}
	}
	if len(scopes) == 1 && !scopes[""] && m.HardBlocker == "" { // hard-blocked Macs count only as not compatible
		for k := range scopes {
			mv.OutOfScope = k
		}
	}
	mv.Matrix = buildMatrix(c, mv)
	return mv
}

func buildConfig(c *catalog.Catalog, m *catalog.Mac, r *catalog.Release, cfg *catalog.Config, excl string) *configView {
	cv := &configView{ID: cfg.ID, Label: cfg.Label, ReleaseName: r.Name, OrderNumbers: cfg.OrderNumbers, BTOOnly: cfg.BTOOnly,
		Codename: c.Vocab.CPUCodenames[cfg.CPU.Codename].Name, Notes: cfg.Notes, OutOfScope: excl}
	for _, p := range cfg.CPU.Standard {
		cv.CPU = append(cv.CPU, processor(p))
	}
	for _, p := range cfg.CPU.BTO {
		cv.CPUBTO = append(cv.CPUBTO, processor(p))
	}
	mem := cfg.Memory
	cv.Memory = fmt.Sprintf("%s GB %s · max %s GB", joinNums(mem.StandardGB), mem.Type, num(mem.MaxGB))
	if mem.Soldered {
		cv.Memory += " · soldered"
	}
	cv.MemoryNote = mem.Notes
	cv.Storage = strings.Join(cfg.Storage.Standard, " / ") + " · " + storageName(cfg.Storage.Interface)
	if len(cfg.Storage.BTO) > 0 {
		cv.Storage += " · BTO " + strings.Join(cfg.Storage.BTO, ", ")
	}
	if d := cfg.Display; d != nil {
		cv.Display = fmt.Sprintf("%s\" %s · %s", num(d.Inches), strings.ReplaceAll(d.Resolution, "x", "×"), d.Panel)
	}
	add := func(ids []string, bto bool) {
		for _, id := range ids {
			comp := c.Components[id]
			if comp == nil {
				continue
			}
			v := compView{Kind: comp.Kind, KindName: c.Vocab.ComponentKinds[comp.Kind].Name, Name: comp.Name, Role: comp.Role,
				IDs: comp.IDs, Driver: comp.Driver, BTO: bto}
			for _, u := range comp.Uncertain {
				v.Uncertain = append(v.Uncertain, uncertainView{u.Field, u.Note})
			}
			cv.Components = append(cv.Components, v)
		}
	}
	add(cfg.Components, false)
	add(cfg.BTOComponents, true)
	rank := map[string]int{}
	for i, k := range kindOrder {
		rank[k] = i
	}
	sort.SliceStable(cv.Components, func(i, j int) bool { return rank[cv.Components[i].Kind] < rank[cv.Components[j].Kind] })
	for _, p := range sortedKeys(cfg.Ports) {
		name := c.Vocab.Ports[p].Name
		if n := cfg.Ports[p]; n > 1 {
			name = fmt.Sprintf("%d× %s", n, name)
		}
		cv.Ports = append(cv.Ports, name)
	}
	for _, f := range cfg.Features {
		cv.Features = append(cv.Features, c.Vocab.Features[f].Name)
	}
	for _, u := range cfg.Uncertain {
		cv.Uncertain = append(cv.Uncertain, uncertainView{u.Field, u.Note})
	}
	applicable := c.Applicable(m, cfg)
	cv.Status = status.Config(status.ConfigInput{HardBlocker: m.HardBlocker, Excluded: excl, Applicable: len(applicable)})
	byCat := map[string]*categoryView{}
	for _, cp := range applicable {
		cat := byCat[cp.Category()]
		if cat == nil {
			cat = &categoryView{ID: cp.Category()}
			for _, k := range c.Categories {
				if k.ID == cat.ID {
					cat.Name, cat.Icon = k.Name, iconFor[k.Icon]
				}
			}
			byCat[cat.ID] = cat
		}
		cat.Caps = append(cat.Caps, capView{cp.ID, cp.Name, cp.Description, status.Untested})
	}
	for _, k := range c.Categories {
		if cat := byCat[k.ID]; cat != nil {
			cv.Categories = append(cv.Categories, *cat)
		}
	}
	return cv
}

// buildMatrix lists every capability applicable to any config of the Mac.
func buildMatrix(c *catalog.Catalog, mv *macView) []matrixRow {
	var rows []matrixRow
	for _, k := range c.Categories {
		for _, cp := range c.Capabilities {
			if cp.Category() != k.ID {
				continue
			}
			row := matrixRow{Category: k.Name, Icon: iconFor[k.Icon], Name: cp.Name, ID: cp.ID}
			any := false
			for _, cv := range mv.Configs {
				has := false
				for _, cat := range cv.Categories {
					for _, x := range cat.Caps {
						has = has || x.ID == cp.ID
					}
				}
				row.Cells = append(row.Cells, has)
				any = any || has
			}
			if any {
				rows = append(rows, row)
			}
		}
	}
	return rows
}

func processor(p catalog.Processor) string {
	s := fmt.Sprintf("%s %s GHz", p.Model, num(p.GHz))
	if p.Cores > 2 {
		s += fmt.Sprintf(" · %d-core", p.Cores)
	}
	return s
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func joinNums(fs []float64) string {
	var s []string
	for _, f := range fs {
		s = append(s, num(f))
	}
	return strings.Join(s, " / ")
}

func storageName(iface string) string {
	switch iface {
	case "pata":
		return "PATA"
	case "sata":
		return "SATA"
	case "pcie-ahci":
		return "PCIe (AHCI)"
	case "nvme":
		return "NVMe"
	}
	return iface
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
