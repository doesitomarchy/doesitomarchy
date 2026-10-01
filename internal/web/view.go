package web

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/search"
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

// Options are startup facts the pages show.
type Options struct {
	Version     string
	CatalogHash string // short content hash of the catalog
	CatalogDate string // last catalog change (YYYY-MM-DD), stamped at build time
	// CatalogCommit is the git commit of that change; "" links to main.
	CatalogCommit string
	Demo          bool // design-review mode: a few configs carry made-up results
}

type site struct {
	Coverage    status.Coverage
	Macs        int
	Releases    int
	Configs     int
	Components  int
	Criteria    int
	Exclusions  []exclusion
	Themes      []themeChoice
	Lines       []lineStat
	CatalogHash string
	CatalogDate string
	CatalogURL  string // GitHub tree of data/ at the catalog's commit
	Demo        bool
}

type exclusion struct {
	Reason string
	Count  int
}

type lineStat struct {
	Key, Name     string
	Macs, Configs int
}

type macView struct {
	Identifier    string
	Slug          string
	LineKey       string
	LineName      string
	Icon          string // product-line icon in icons.svg (m-<Icon>)
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
	AllVerified   bool             // every config passed every applicable test: earns the Omarchy badge
	Tested        int              // configs with at least one result
	Matrix        *matrixView
}

type releaseView struct {
	ID, Name, Announced, Discontinued string
	ModelNumbers, EMC                 []string
	Configs                           []*configView
	Mac                               *macView
}

type configView struct {
	ID           string
	Letter       string
	Diff         string // what sets this config apart from its siblings ("HD 6490M", "17-inch")
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
	Verified     bool // every applicable capability passed, no conflicts
	OutOfScope   string
	Notes        string
	Uncertain    []uncertainView
	Mac          *macView
}

type compView struct {
	ID        string
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
	Blocking       bool
	Caps           []capView
	Passed         int
}

// Problem reports a failed, given-up or partial capability: such categories
// start open so the reason is visible.
func (c categoryView) Problem() bool {
	for _, x := range c.Caps {
		if x.Verdict == status.Failed || x.Verdict == status.Unsupported || x.Verdict == status.Partial {
			return true
		}
	}
	return false
}

// Pct is the share of the category's capabilities that passed (0–100).
func (c categoryView) Pct() string { return fmt.Sprintf("%.2f", pctOf(c.Passed, len(c.Caps))) }

type capView struct {
	ID, Name, Description string
	Verdict               status.Verdict // Untested until Phase 7 (or demo data)
	Error                 string         // why it failed, as reported by the doioma test tool
	Fix                   string         // who is working on it / where it's tracked (Phase 7 design)
}

// matrixView is a criteria × configurations table (model page and /criteria).
type matrixView struct {
	Cols      []matrixCol
	Groups    []matrixGroup
	HeadClass string // stepped header height for the slanted labels
}

type matrixCol struct {
	ID, Label, Title, Href string
	Verified               bool
	OutOfScope             bool
}

type matrixGroup struct {
	Name, Icon    string
	Blocking      bool
	Rows          []matrixRow
	Passed, Total int // applicable cells in the group, and how many have passed
}

// Pct is the share of the group's applicable cells that passed (0–100).
func (g matrixGroup) Pct() string { return fmt.Sprintf("%.2f", pctOf(g.Passed, g.Total)) }

type matrixRow struct {
	ID, Name, Description string
	Cells                 []matrixCell
	Count                 int
}

type matrixCell struct {
	Applies bool
	Verdict status.Verdict
}

// catalogView holds everything the pages render.
type catalogView struct {
	site       site
	macs       []*macView
	bySlug     map[string]*macView    // lower-case slug → mac
	configs    map[string]*configView // config ID → config
	components []componentUse
	states     search.States
}

// componentUse is one component and the configs that use it (/components).
type componentUse struct {
	ID, Kind, KindName, Name, Vendor, Role, Driver string
	IDs                                            []string
	Configs                                        int
	Macs                                           []*macView
}

// state is a config's current status plus per-capability verdicts and,
// for failures, the reported error and fix tracking.
type state struct {
	status status.ConfigStatus
	caps   map[string]status.Verdict
	errors map[string]string
	fixes  map[string]string
}

func buildView(c *catalog.Catalog, opt Options) *catalogView {
	v := &catalogView{bySlug: map[string]*macView{}, configs: map[string]*configView{}, states: search.States{}}
	var statuses []status.ConfigStatus
	lineStats := map[string]*lineStat{}
	stateOf := func(m *catalog.Mac, cfg *catalog.Config, excl string, applicable []catalog.Capability) state {
		if opt.Demo {
			if st, ok := demoState(m, cfg, excl, applicable); ok {
				return st
			}
		}
		return state{status: status.Config(status.ConfigInput{HardBlocker: m.HardBlocker, Excluded: excl, Applicable: len(applicable)})}
	}
	for _, m := range c.Macs {
		mv := buildMac(c, m, stateOf)
		v.macs = append(v.macs, mv)
		v.bySlug[strings.ToLower(mv.Slug)] = mv
		ls := lineStats[m.Line]
		if ls == nil {
			ls = &lineStat{Key: m.Line, Name: c.Vocab.Lines[m.Line].Name}
			lineStats[m.Line] = ls
		}
		ls.Macs++
		for _, cv := range mv.Configs {
			statuses = append(statuses, cv.Status)
			v.configs[cv.ID] = cv
			ls.Configs++
			v.states[cv.ID] = search.State{Verdict: cv.Status.Verdict, Tested: cv.Status.Tested > 0}
		}
	}
	sort.SliceStable(v.macs, func(i, j int) bool { return lessMac(v.macs[i], v.macs[j]) })

	s := &v.site
	s.Coverage = status.Summarize(statuses)
	st := c.Stats()
	s.Macs, s.Releases, s.Configs, s.Components, s.Criteria = st.Macs, st.Releases, st.Configs, st.Components, st.Capabilities
	s.Themes, s.CatalogHash, s.CatalogDate, s.Demo = themeChoices, opt.CatalogHash, opt.CatalogDate, opt.Demo
	ref := opt.CatalogCommit
	if ref == "" {
		ref = "main"
	}
	s.CatalogURL = RepoURL + "/tree/" + ref + "/data"
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
	v.components = buildComponents(c, v)
	return v
}

// Made-up results shown with -demo (design review only). They exercise every
// verdict: Supported, Partial (with a Failed and an Unsupported capability),
// Failed (a Boot test failed) and Unsupported (a Boot test given up on).
var (
	demoVerified = map[string]bool{"MacBookPro8,2": true, "imac12-2-27-mid-2011-a": true}
	demoCases    = map[string]struct {
		verdict status.Verdict
		caps    map[string]status.Verdict
		errors  map[string]string
		fixes   map[string]string
		blocker string
	}{
		"MacBookPro15,1": {status.Partial,
			map[string]status.Verdict{"audio.speakers": status.Failed, "bridge.touch-bar-camera": status.Unsupported},
			map[string]string{"audio.speakers": "snd_hda_intel 0000:00:1f.3: no codecs found (T2 audio needs the apple-bce aaudio driver)"},
			map[string]string{"audio.speakers": "Nobody has claimed this fix yet.", "bridge.touch-bar-camera": "Marked unsupported after 3 fix attempts made no progress."},
			"Audio → Built-in speakers"},
		"MacBookPro15,2": {status.Failed,
			map[string]status.Verdict{"boot.install": status.Failed},
			map[string]string{"boot.install": "nvme0n1 not found: the installer kernel has no apple-bce module, so the T2 SSD is invisible"},
			map[string]string{"boot.install": "Being worked on by @example in issue #42."},
			"Boot → Internal storage detected and installation completes"},
		"MacBookPro16,2": {status.Unsupported,
			map[string]status.Verdict{"boot.installer-efi64": status.Unsupported},
			map[string]string{"boot.installer-efi64": "Firmware refuses the installer image (Secure Boot policy cannot be relaxed on this unit)"},
			map[string]string{"boot.installer-efi64": "Marked unsupported after 4 fix attempts made no progress."},
			"Boot → Stock installer boots via 64-bit EFI"},
	}
)

func demoState(m *catalog.Mac, cfg *catalog.Config, excl string, applicable []catalog.Capability) (state, bool) {
	st := state{caps: map[string]status.Verdict{}, errors: map[string]string{}, fixes: map[string]string{}}
	n := len(applicable)
	if demoVerified[m.Identifier] || demoVerified[cfg.ID] {
		for _, cp := range applicable {
			st.caps[cp.ID] = status.Supported
		}
		st.status = status.ConfigStatus{Verdict: status.Supported, Excluded: excl, Applicable: n, Tested: n, Counts: status.Counts{Supported: n}}
		return st, true
	}
	dc, ok := demoCases[m.Identifier]
	if !ok {
		return state{}, false
	}
	var c status.Counts
	tested := 0
	for i, cp := range applicable {
		v, special := dc.caps[cp.ID]
		switch {
		case special:
		case i%3 != 2:
			v = status.Supported
		default:
			v = status.Untested
		}
		st.caps[cp.ID] = v
		switch v {
		case status.Supported:
			c.Supported++
		case status.Failed:
			c.Failed++
		case status.Unsupported:
			c.Unsupported++
		default:
			c.Untested++
		}
		if v != status.Untested {
			tested++
		}
	}
	st.errors, st.fixes = dc.errors, dc.fixes
	st.status = status.ConfigStatus{Verdict: dc.verdict, Excluded: excl, Applicable: n, Tested: tested, Blocker: dc.blocker, Counts: c}
	return st, true
}

func orderedLines[V any](m map[string]V) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if ri, rj := lineRank(keys[i]), lineRank(keys[j]); ri != rj {
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

// machineIcon is a Mac's icon: its product line's, except where one
// identifier looks nothing like the rest of its line.
func machineIcon(m *catalog.Mac) string {
	if m.Identifier == "MacPro6,1" { // the 2013 cylinder, among cheese-grater towers
		return "mac-pro-2013"
	}
	return m.Line
}

type stateFunc func(*catalog.Mac, *catalog.Config, string, []catalog.Capability) state

func buildMac(c *catalog.Catalog, m *catalog.Mac, stateOf stateFunc) *macView {
	mv := &macView{
		Identifier: m.Identifier, Slug: catalog.FileSlug(m.Identifier), LineKey: m.Line, LineName: c.Vocab.Lines[m.Line].Name,
		EFI: m.EFI, HardBlocker: m.HardBlocker, ResearchNotes: m.ResearchNotes, BoardIDs: m.BoardIDs, Sources: m.Sources,
	}
	mv.Icon = machineIcon(m)
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
	var raw []*catalog.Config
	for ri := range m.Releases {
		r := &m.Releases[ri]
		rv := &releaseView{ID: r.ID, Name: r.Name, Announced: r.Announced, Discontinued: r.Discontinued, ModelNumbers: r.ModelNumbers, EMC: r.EMC, Mac: mv}
		y, _ := strconv.Atoi(r.Announced[:4])
		first, last = min(first, y), max(last, y)
		mv.Title = r.Name
		excl := c.CoverageExclusion(m, r)
		for ci := range r.Configs {
			cfg := &r.Configs[ci]
			cv := buildConfig(c, m, r, cfg, excl, stateOf)
			cv.Mac = mv
			cv.Letter = string(rune('A' + len(mv.Configs)%26))
			rv.Configs = append(rv.Configs, cv)
			mv.Configs = append(mv.Configs, cv)
			raw = append(raw, cfg)
			verdicts[cv.Status.Verdict] = true
			scopes[excl] = true
			if cv.Status.Tested > 0 {
				mv.Tested++
			}
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
	for _, v := range []status.Verdict{status.Supported, status.Partial, status.Failed, status.Unsupported, status.Untested, status.NotCompatible} {
		if verdicts[v] {
			mv.Verdicts = append(mv.Verdicts, v)
		}
	}
	if len(scopes) == 1 && !scopes[""] && m.HardBlocker == "" { // hard-blocked Macs count only as not compatible
		for k := range scopes {
			mv.OutOfScope = k
		}
	}
	mv.AllVerified = len(mv.Configs) > 0
	for _, cv := range mv.Configs {
		mv.AllVerified = mv.AllVerified && cv.Verified
	}
	diffLabels(c, mv, raw)
	mv.Matrix = buildMatrix(c, mv.Configs, false)
	return mv
}

func buildConfig(c *catalog.Catalog, m *catalog.Mac, r *catalog.Release, cfg *catalog.Config, excl string, stateOf stateFunc) *configView {
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
			v := compView{ID: comp.ID, Kind: comp.Kind, KindName: c.Vocab.ComponentKinds[comp.Kind].Name, Name: comp.Name, Role: comp.Role,
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
	st := stateOf(m, cfg, excl, applicable)
	cv.Status = st.status
	cv.Verified = cv.Status.Verdict == status.Supported && cv.Status.Applicable > 0 &&
		cv.Status.Counts.Supported == cv.Status.Applicable && cv.Status.Conflicts == 0
	byCat := map[string]*categoryView{}
	for _, cp := range applicable {
		cat := byCat[cp.Category()]
		if cat == nil {
			cat = &categoryView{ID: cp.Category()}
			for _, k := range c.Categories {
				if k.ID == cat.ID {
					cat.Name, cat.Icon, cat.Blocking = k.Name, iconFor[k.Icon], k.Blocking
				}
			}
			byCat[cat.ID] = cat
		}
		v := status.Untested
		if cv.Status.Verdict == status.NotCompatible {
			v = status.NotCompatible
		}
		if x, ok := st.caps[cp.ID]; ok {
			v = x
		}
		if v == status.Supported {
			cat.Passed++
		}
		cat.Caps = append(cat.Caps, capView{ID: cp.ID, Name: cp.Name, Description: cp.Description, Verdict: v, Error: st.errors[cp.ID], Fix: st.fixes[cp.ID]})
	}
	for _, k := range c.Categories {
		if cat := byCat[k.ID]; cat != nil {
			cv.Categories = append(cv.Categories, *cat)
		}
	}
	return cv
}

// ── difference labels ────────────────────────────────────────────────────

var gpuNoise = regexp.MustCompile(`(?i)\b(AMD|ATI|NVIDIA|Intel|Radeon|GeForce|Graphics|Mobility|Apple)\b\s*|\s*\([^)]*\)`)

// shortGPU trims vendor and family words: "AMD Radeon HD 6490M" → "HD 6490M".
func shortGPU(name string) string {
	s := strings.Join(strings.Fields(gpuNoise.ReplaceAllString(name, "")), " ")
	if s == "" {
		return name
	}
	return s
}

// shortRelease takes the season/year part of a release name:
// "MacBook Pro (15-inch, Late 2011)" → "Late 2011".
func shortRelease(name string) string {
	i, j := strings.Index(name, "("), strings.Index(name, ")")
	if i < 0 || j < i {
		return name
	}
	inner := name[i+1 : j]
	if k := strings.LastIndex(inner, ","); k >= 0 {
		inner = inner[k+1:]
	}
	return strings.TrimSpace(inner)
}

// diffLabels names each config of a Mac by what differs from its siblings:
// GPU, display size, release, BTO-only, other components, then the label's
// lead word (e.g. "Server"). Facts are added in that order until every
// config's label is unique; a Mac with one config gets its release.
func diffLabels(c *catalog.Catalog, mv *macView, raw []*catalog.Config) {
	n := len(mv.Configs)
	if n == 1 {
		mv.Configs[0].Diff = shortRelease(mv.Configs[0].ReleaseName)
		return
	}
	facts := make([][]string, 6) // gpu, size, resolution, release, bto, other components
	for i := range facts {
		facts[i] = make([]string, n)
	}
	for i, cv := range mv.Configs {
		var gpus, others []string
		for _, comp := range cv.Components {
			if comp.Kind == "gpu" {
				gpus = append(gpus, shortGPU(comp.Name))
			} else if !comp.BTO {
				w := strings.Fields(comp.Name)
				others = append(others, strings.Join(w[max(0, len(w)-2):], " ")) // "Gigabit Ethernet", "10Gb Ethernet"
			}
		}
		facts[0][i] = strings.Join(gpus, " + ")
		if d := raw[i].Display; d != nil {
			facts[1][i] = num(d.Inches) + "-inch"
			facts[2][i] = strings.ReplaceAll(d.Resolution, "x", "×")
			for _, r := range []string{"Retina 4K", "Retina 5K"} {
				if strings.Contains(cv.ReleaseName, r) {
					facts[2][i] = r
				}
			}
		}
		facts[3][i] = shortRelease(cv.ReleaseName)
		if cv.BTOOnly {
			facts[4][i] = "BTO"
		}
		facts[5][i] = strings.Join(others, "\x00")
	}
	// GPU facts drop the GPU shared by every sibling (the integrated one).
	common := map[string]int{}
	for i := 0; i < n; i++ {
		for _, g := range strings.Split(facts[0][i], " + ") {
			common[g]++
		}
	}
	for i := 0; i < n; i++ {
		var keep []string
		for _, g := range strings.Split(facts[0][i], " + ") {
			if common[g] < n {
				keep = append(keep, g)
			}
		}
		facts[0][i] = strings.Join(keep, " + ")
	}
	// Other components: keep only those not shared by every sibling.
	shared := map[string]int{}
	for i := 0; i < n; i++ {
		for _, o := range strings.Split(facts[5][i], "\x00") {
			shared[o]++
		}
	}
	for i := 0; i < n; i++ {
		var keep []string
		for _, o := range strings.Split(facts[5][i], "\x00") {
			if o != "" && shared[o] < n {
				keep = append(keep, o)
			}
		}
		facts[5][i] = strings.Join(keep, ", ")
	}
	labels := make([][]string, n)
	unique := func() bool {
		seen := map[string]bool{}
		for _, l := range labels {
			k := strings.Join(l, " · ")
			if k == "" || seen[k] {
				return false
			}
			seen[k] = true
		}
		return true
	}
	// distinct scores a labelling: more distinct groups first, then more
	// configs with a non-empty label.
	distinct := func(ls [][]string) int {
		seen := map[string]bool{}
		nonEmpty := 0
		for _, l := range ls {
			seen[strings.Join(l, " · ")] = true
			if len(l) > 0 {
				nonEmpty++
			}
		}
		return len(seen)*1000 + nonEmpty
	}
	// Add a fact only when it separates more configs than before.
	for f := 0; f < len(facts) && !unique(); f++ {
		varies := false
		for i := 1; i < n; i++ {
			varies = varies || facts[f][i] != facts[f][0]
		}
		if !varies {
			continue
		}
		next := make([][]string, n)
		for i := range labels {
			next[i] = append([]string{}, labels[i]...)
			if facts[f][i] != "" {
				next[i] = append(next[i], facts[f][i])
			}
		}
		if distinct(next) > distinct(labels) {
			labels = next
		}
	}
	if !unique() { // fall back to the label's lead phrase; a Server sibling makes the rest "Standard"
		server := false
		for _, cv := range mv.Configs {
			server = server || strings.HasPrefix(cv.Label, "Server")
		}
		for i, cv := range mv.Configs {
			lead, _, _ := strings.Cut(cv.Label, " · ")
			switch {
			case strings.HasPrefix(lead, "Server"):
				lead = "Server"
			case server:
				lead = "Standard"
			}
			labels[i] = append(labels[i], lead)
		}
	}
	for i, cv := range mv.Configs {
		cv.Diff = strings.Join(labels[i], " · ")
		if !unique() || cv.Diff == "" {
			cv.Diff = cv.Label
		}
	}
}

// ── matrix ───────────────────────────────────────────────────────────────

// buildMatrix lists every capability applicable to any of the configs. With
// withMac the column labels carry the identifier (the /criteria page).
func buildMatrix(c *catalog.Catalog, cfgs []*configView, withMac bool) *matrixView {
	mx := &matrixView{}
	longest := 0
	for _, cv := range cfgs {
		label, href := cv.Diff, "#cfg-"+cv.ID
		if withMac {
			label = cv.Mac.Identifier + " · " + cv.Diff
			href = "/mac/" + cv.Mac.Slug + "#cfg-" + cv.ID
		}
		if r := []rune(label); len(r) > 34 {
			label = string(r[:33]) + "…"
		}
		longest = max(longest, len([]rune(label)))
		mx.Cols = append(mx.Cols, matrixCol{ID: cv.ID, Label: label, Title: cv.Label + " (" + cv.ReleaseName + ")", Href: href,
			Verified: cv.Verified, OutOfScope: cv.OutOfScope != "" && cv.Status.Verdict != status.NotCompatible})
	}
	mx.HeadClass = headClass(longest)
	for _, k := range c.Categories {
		g := matrixGroup{Name: k.Name, Icon: iconFor[k.Icon], Blocking: k.Blocking}
		for _, cp := range c.Capabilities {
			if cp.Category() != k.ID {
				continue
			}
			row := matrixRow{ID: cp.ID, Name: cp.Name, Description: cp.Description}
			for _, cv := range cfgs {
				cell := matrixCell{}
				for _, cat := range cv.Categories {
					for _, x := range cat.Caps {
						if x.ID == cp.ID {
							cell = matrixCell{Applies: true, Verdict: x.Verdict}
						}
					}
				}
				if cell.Applies {
					row.Count++
					g.Total++
					if cell.Verdict == status.Supported {
						g.Passed++
					}
				}
				row.Cells = append(row.Cells, cell)
			}
			if row.Count > 0 {
				g.Rows = append(g.Rows, row)
			}
		}
		if len(g.Rows) > 0 {
			mx.Groups = append(mx.Groups, g)
		}
	}
	return mx
}

// headClass picks a header height class for slanted labels of n characters:
// n × 6.6px × sin 60° plus padding, rounded up to a 20px step (site.css
// defines .mh-60 … .mh-260; inline styles are blocked by the CSP).
func headClass(n int) string {
	px := int(float64(n)*6.6*0.866) + 34
	step := ((px + 19) / 20) * 20
	return fmt.Sprintf("mh-%d", min(max(step, 60), 260))
}

// ── components ───────────────────────────────────────────────────────────

func buildComponents(c *catalog.Catalog, v *catalogView) []componentUse {
	uses := map[string]*componentUse{}
	for _, mv := range v.macs {
		seenMac := map[string]bool{}
		for _, cv := range mv.Configs {
			for _, comp := range cv.Components {
				u := uses[comp.ID]
				if u == nil {
					cc := c.Components[comp.ID]
					u = &componentUse{ID: comp.ID, Kind: comp.Kind, KindName: comp.KindName, Name: comp.Name, Vendor: cc.Vendor,
						Role: comp.Role, Driver: comp.Driver, IDs: comp.IDs}
					uses[comp.ID] = u
				}
				u.Configs++
				if !seenMac[comp.ID] {
					seenMac[comp.ID] = true
					u.Macs = append(u.Macs, mv)
				}
			}
		}
	}
	rank := map[string]int{}
	for i, k := range kindOrder {
		rank[k] = i
	}
	var out []componentUse
	for _, u := range uses {
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool {
		if rank[out[i].Kind] != rank[out[j].Kind] {
			return rank[out[i].Kind] < rank[out[j].Kind]
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ── helpers ──────────────────────────────────────────────────────────────

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
