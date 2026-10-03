package web

import (
	"time"

	"fmt"
	"html/template"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/fixes"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/search"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// The web pages render from view models built from the loaded catalog and
// the accepted results. The catalog is immutable while the process runs;
// results change, and the server rebuilds the views (and swaps them in
// atomically) whenever the store's data version moves.

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
	Demo          bool // design-review mode: a throwaway database seeded with made-up results
	// Cloudflare zone and purge-only token: when set, the cache is purged
	// after results change, so visitors see new verdicts within seconds.
	PurgeZone, PurgeToken string
	// Cloudflare Access team name and /admin application audience tag: the
	// server verifies Access's signed token on every /admin request.
	AccessTeam, AccessAUD string
	// AdminInsecure opens /admin without Access, for local development only
	// (serve refuses it unless listening on a loopback address).
	AdminInsecure bool
	// CatalogFS holds the catalog's data files, for source mappings
	// (data/sources); nil means the catalog built into the binary.
	CatalogFS fs.FS
	// Fix tracking (PLAN §26): the fix repo, a token limited to it, and the
	// webhook's shared secret. Without a token, fixes can't be opened from
	// /admin; without the secret, /hooks/github refuses every delivery.
	FixRepo, GitHubToken, WebhookSecret string
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
	Layout       []layoutSide // the port layout by side, when researched
	LayoutSrc    []string
	LayoutNote   string
	Portmap      template.HTML     // the release's port map drawing, coloured by status (PLAN §27)
	PortmapKey   string            // its key, for /portmap/<key>.svg
	PortSt       map[string]string // each connector's status on the drawing
	Conns        []connInfo        // the same, flat, for the API
	Features     []string
	Categories   []categoryView
	Status       status.ConfigStatus
	Verified     bool                  // every applicable capability passed, no conflicts
	Results      []store.ResultSummary // accepted and retracted results, newest first
	Latest       *store.ResultSummary  // the newest accepted result
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
	Verdict               status.Verdict
	Error                 string   // why it failed or only partly works, as reported (evidence)
	Fix                   *fixView // the fix issue for a failed criterion, if any (PLAN §26)
	Reason                string   // the maintainer's reason, when Unsupported
	// The latest report for this capability (Result 0 when untested).
	Result                int64
	Code                  string // its public code, for /report/{code}
	Date, Omarchy, Method string
	Kernel                string // the report's kernel, when given
	Stale, Conflict       bool
	// FromLatest: the result is the config's latest, already named once at
	// the top of the card, so the row only shows its method.
	FromLatest bool
	// Ports: per-connector criteria (PLAN §25), each connector's status and
	// how many port groups are covered.
	Ports        []capPort
	GroupsPassed int
	GroupsTotal  int
}

type capPort struct {
	ID, Name  string
	Verdict   status.Verdict
	CoveredBy string // untested, covered by a group-mate that passed
	Suspect   bool   // failed while a group-mate passed: possibly a damaged port
}

// statusGroups converts the catalog's port groups for the status engine.
func statusGroups(gs []catalog.PortGroup) []status.Group {
	var out []status.Group
	for _, g := range gs {
		out = append(out, status.Group{ID: g.ID, Connectors: g.Connectors})
	}
	return out
}

// fixView is a fix issue as a criterion row shows it.
type fixView struct {
	Issue                  int
	URL, Title             string
	State                  fixes.State
	Assignee               string
	LastActivity, ClosedAt string // dates
	FixLink                string
	Retest                 bool // fixed after the criterion's latest result: please re-test
}

func newFixView(f store.Fix, st fixes.State) *fixView {
	return &fixView{Issue: f.Issue, URL: f.URL, Title: f.Title, State: st, Assignee: f.Assignee,
		LastActivity: dateOnly(f.LastActivity), ClosedAt: dateOnly(f.ClosedAt), FixLink: f.FixLink}
}

func dateOnly(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}

// layoutSide is one side of a configuration's port layout.
type layoutSide struct {
	Side  string // "Left side", "Back"
	Conns []layoutConn
}

type layoutConn struct {
	ID, Name, Note string
	Num            string // its number on the drawing ("3" for left-3)
	St, StLabel    string // its status on the drawing, when it has results
}

// connInfo is one connector as the API serves it (PLAN §25).
type connInfo struct {
	ID       string   `json:"id"`
	Side     string   `json:"side"`
	Type     string   `json:"type"`
	Also     []string `json:"also,omitempty"`
	Name     string   `json:"name"`
	Note     string   `json:"note,omitempty"`
	Criteria []string `json:"criteria"` // what this connector is tested for; empty for power inlets
}

var sideNames = map[string]string{"left": "Left side", "right": "Right side", "back": "Back", "front": "Front", "top": "Top"}

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
	rollup     *store.Rollup
}

// componentUse is one component and the configs that use it (/components).
type componentUse struct {
	ID, Kind, KindName, Name, Vendor, Role, Driver string
	IDs                                            []string
	Configs                                        int
	Macs                                           []*macView
}

func buildView(c *catalog.Catalog, opt Options, ru *store.Rollup) *catalogView {
	if ru == nil {
		ru = &store.Rollup{}
	}
	v := &catalogView{bySlug: map[string]*macView{}, configs: map[string]*configView{}, states: search.States{}, rollup: ru}
	var statuses []status.ConfigStatus
	lineStats := map[string]*lineStat{}
	cats := map[string]catalog.Category{}
	for _, k := range c.Categories {
		cats[k.ID] = k
	}
	stateOf := func(m *catalog.Mac, cfg *catalog.Config, excl string, applicable []catalog.Capability) status.ConfigStatus {
		caps := make([]status.Capability, len(applicable))
		groups := c.CriterionGroups(m, cfg)
		for i, cp := range applicable {
			k := cats[cp.Category()]
			caps[i] = status.Capability{ID: cp.ID, Label: k.Name + " → " + cp.Name, Blocking: k.Blocking, Groups: statusGroups(groups[cp.ID])}
		}
		return status.Config(status.ConfigInput{HardBlocker: m.HardBlocker, Excluded: excl, Caps: caps,
			Items: ru.Items[cfg.ID], Unsupported: ru.Unsupported[cfg.ID], Results: ru.Results[cfg.ID],
			LatestResult: ru.Latest[cfg.ID], CurrentMajor: ru.CurrentMajor})
	}
	for _, m := range c.Macs {
		mv := buildMac(c, m, stateOf, ru)
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
			v.states[cv.ID] = search.State{Verdict: cv.Status.Verdict, Tested: cv.Status.Results > 0, Latest: cv.Status.Latest}
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

type stateFunc func(*catalog.Mac, *catalog.Config, string, []catalog.Capability) status.ConfigStatus

func buildMac(c *catalog.Catalog, m *catalog.Mac, stateOf stateFunc, ru *store.Rollup) *macView {
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
			cv := buildConfig(c, m, r, cfg, excl, stateOf, ru)
			cv.Mac = mv
			cv.Results = ru.Accepted[cfg.ID]
			for i := range cv.Results {
				if cv.Results[i].State == store.Accepted {
					cv.Latest = &cv.Results[i]
					break
				}
			}
			codes, kernels := map[int64]string{}, map[int64]string{}
			for _, x := range cv.Results {
				codes[x.ID], kernels[x.ID] = x.Code, x.Kernel
			}
			for ci := range cv.Categories {
				for k := range cv.Categories[ci].Caps {
					x := &cv.Categories[ci].Caps[k]
					x.Code, x.Kernel = codes[x.Result], kernels[x.Result]
					x.FromLatest = cv.Latest != nil && x.Result == cv.Latest.ID && !x.Stale && !x.Conflict
				}
			}
			cv.Letter = string(rune('A' + len(mv.Configs)%26))
			rv.Configs = append(rv.Configs, cv)
			mv.Configs = append(mv.Configs, cv)
			raw = append(raw, cfg)
			verdicts[cv.Status.Verdict] = true
			scopes[excl] = true
			if cv.Status.Results > 0 {
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

func buildConfig(c *catalog.Catalog, m *catalog.Mac, r *catalog.Release, cfg *catalog.Config, excl string, stateOf stateFunc, ru *store.Rollup) *configView {
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
	conn := map[string]catalog.Connector{}
	for _, side := range catalog.Sides {
		ls := layoutSide{Side: sideNames[side]}
		for _, cn := range cfg.Connectors {
			conn[cn.ID] = cn
			if cn.Side() == side {
				name := c.ConnectorName(cn)
				for _, a := range cn.Also {
					name += " + " + c.Vocab.Ports[a].Name
				}
				ls.Conns = append(ls.Conns, layoutConn{ID: cn.ID, Name: name, Note: cn.Note})
			}
		}
		if len(ls.Conns) > 0 {
			cv.Layout = append(cv.Layout, ls)
		}
	}
	cv.LayoutSrc, cv.LayoutNote = cfg.LayoutSources, cfg.LayoutNote
	for _, cn := range cfg.Connectors {
		crit := c.ConnectorCriteria(m, cfg, cn)
		if crit == nil {
			crit = []string{}
		}
		cv.Conns = append(cv.Conns, connInfo{cn.ID, cn.Side(), cn.Type, cn.Also, c.ConnectorName(cn), cn.Note, crit})
	}
	for _, f := range cfg.Features {
		cv.Features = append(cv.Features, c.Vocab.Features[f].Name)
	}
	for _, u := range cfg.Uncertain {
		cv.Uncertain = append(cv.Uncertain, uncertainView{u.Field, u.Note})
	}
	applicable := c.Applicable(m, cfg)
	cv.Status = stateOf(m, cfg, excl, applicable)
	cv.Verified = cv.Status.Verified()
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
		cs := cv.Status.Caps[cp.ID]
		x := capView{ID: cp.ID, Name: cp.Name, Description: cp.Description, Verdict: cs.Verdict, Reason: cs.Reason,
			Stale: cs.Stale, Conflict: cs.Conflict}
		if x.Verdict == "" {
			x.Verdict = status.Untested
		}
		if cv.Status.Verdict == status.NotCompatible && cs.Latest == nil {
			x.Verdict = status.NotCompatible
		}
		if l := cs.Latest; l != nil {
			x.Result, x.Date, x.Omarchy, x.Method = l.ResultID, l.TestedAt, l.Omarchy.String(), l.Method
			if l.Verdict != status.Supported {
				x.Error = l.Evidence
			}
		}
		if cs.GroupsTotal > 0 {
			x.GroupsPassed, x.GroupsTotal = cs.GroupsPassed, cs.GroupsTotal
			for _, id := range c.CriterionConnectors(m, cfg)[cp.ID] {
				ps := cs.Ports[id]
				if ps.Verdict == "" {
					ps.Verdict = status.Untested
				}
				x.Ports = append(x.Ports, capPort{id, c.ConnectorName(conn[id]), ps.Verdict, ps.CoveredBy, ps.Suspect})
			}
		}
		if ru != nil && (x.Verdict == status.Failed || x.Verdict == status.Partial) {
			if f, st, ok := fixes.Best(ru.Fixes, cp.ID, cfg.ID, cfg.Components, time.Now()); ok {
				x.Fix = newFixView(f, st)
				x.Fix.Retest = st == fixes.Fixed && f.ClosedAt > x.Date
			}
		}
		if x.Verdict == status.Supported {
			cat.Passed++
		}
		cat.Caps = append(cat.Caps, x)
	}
	for _, k := range c.Categories {
		if cat := byCat[k.ID]; cat != nil {
			cv.Categories = append(cv.Categories, *cat)
		}
	}
	if pm := c.Portmaps[cfg.Portmap]; pm != nil {
		cv.PortmapKey, cv.PortSt = pm.Key, connStatuses(cv)
		cv.Portmap = portmapHTML(pm, cv.ID, cv.PortSt)
		for i := range cv.Layout {
			for j := range cv.Layout[i].Conns {
				lc := &cv.Layout[i].Conns[j]
				_, lc.Num, _ = strings.Cut(lc.ID, "-")
				lc.St = cv.PortSt[lc.ID]
				lc.StLabel = pmLabels[lc.St]
			}
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
			if cp.Category() != k.ID || cp.Retired {
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
