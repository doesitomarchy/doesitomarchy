package web

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/search"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// preset is one of the quick searches under the home search bar (PLAN §17.7).
type preset struct {
	Label, Query, Sort string
}

var presets = []preset{
	{"All Compatible", "status:supported", ""},
	{"MacBook Pros", "line:macbook-pro", ""},
	{"iMacs", "line:imac", ""},
	{"MacBook", "line:macbook", ""},
	{"MacBook Airs", "line:macbook-air", ""},
	{"iMac Pros", "line:imac-pro", ""},
	{"Mac Minis", "line:mac-mini", ""},
	{"Mac Pros", "line:mac-pro", ""},
	{"Latest with Test", "tested:yes", "recent"},
}

// URL is the preset's search link.
func (p preset) URL() string {
	u := "/search?q=" + url.QueryEscape(p.Query)
	if p.Sort != "" {
		u += "&sort=" + p.Sort
	}
	return u
}

// maxQuery bounds the query length; longer input is cut, never an error.
const maxQuery = 200

func queryParam(r *http.Request) string {
	q := r.URL.Query().Get("q")
	if rs := []rune(q); len(rs) > maxQuery {
		q = string(rs[:maxQuery])
	}
	return q
}

// isHTMX reports an HTMX request, which gets the partial instead of the page.
func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// ── home ──────────────────────────────────────────────────────────────────

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "home", page{Title: "Does it Omarchy?", Data: searchData{Presets: presets}})
}

// ── search ────────────────────────────────────────────────────────────────

type resultView struct {
	search.Result
	Mac *macView
	// Scoped lists the matched configs with their view (for scope tags).
	Scoped []*configView
}

type searchData struct {
	Q         string
	Sort      string
	Resp      search.Response
	Results   []resultView
	Presets   []preset
	NoResults bool // the preset asks about test results and none exist yet
	// OnSearchPage: typing updates the address bar (not on the home page).
	OnSearchPage bool
}

func (s *Server) runSearch(q, sortBy string) searchData {
	resp := s.data().index.Search(q)
	if sortBy == "recent" {
		search.SortRecent(resp.Results)
	}
	d := searchData{Q: q, Sort: sortBy, Resp: resp, Presets: presets}
	for _, res := range resp.Results {
		rv := resultView{Result: res, Mac: s.data().view.bySlug[strings.ToLower(res.Slug)]}
		for _, hit := range res.Configs {
			rv.Scoped = append(rv.Scoped, s.data().view.configs[hit.ID])
		}
		d.Results = append(d.Results, rv)
	}
	lq := strings.ToLower(q)
	if len(resp.Results) == 0 && s.data().view.site.Coverage.Tested == 0 && (strings.Contains(lq, "tested:") || strings.Contains(lq, "status:")) {
		d.NoResults = true
	}
	return d
}

// search serves GET /search?q=: a full page, or the results partial for HTMX.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := queryParam(r)
	d := s.runSearch(q, r.URL.Query().Get("sort"))
	w.Header().Set("Vary", "HX-Request")
	if isHTMX(r) {
		// The fragment and the full page share this URL. Cloudflare ignores
		// Vary, so a cached fragment would be served as an unstyled page.
		w.Header().Set("Cache-Control", "private, no-store")
		s.renderPartial(w, r, "search", "results", d)
		return
	}
	// Enter on a query with exactly one match goes straight to that Mac,
	// and to the configuration when the query pinpoints one of several.
	if q != "" && r.URL.Query().Get("sort") == "" && len(d.Results) == 1 && d.Results[0].Mac != nil {
		m := d.Results[0].Mac
		target := "/mac/" + url.PathEscape(m.Slug)
		if sc := d.Results[0].Scoped; len(sc) == 1 && sc[0] != nil && len(m.Configs) > 1 {
			target += "#cfg-" + sc[0].ID
		}
		w.Header().Set("Cache-Control", "public, max-age=0, s-maxage=300")
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	d.OnSearchPage = true
	title := "Search"
	if q != "" {
		title = q + " · search"
	}
	canonical := BaseURL + "/search"
	if q != "" {
		canonical += "?q=" + url.QueryEscape(q) // each query is its own page
	}
	s.render(w, r, http.StatusOK, "search", page{Title: title, Canonical: canonical, Data: d})
}

// suggest serves GET /search/suggest?q=&cursor=: an HTML list of completions.
// Each entry is a plain link, so it also works without JavaScript.
func (s *Server) suggest(w http.ResponseWriter, r *http.Request) {
	q := queryParam(r)
	cursor := -1
	if c, err := strconv.Atoi(r.URL.Query().Get("cursor")); err == nil {
		cursor = c
	}
	s.renderPartial(w, r, "suggest", "suggestions", s.data().index.Suggest(q, cursor))
}

// ── model page ────────────────────────────────────────────────────────────

type macData struct {
	Mac    *macView
	Matrix bool
}

func (s *Server) mac(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m := s.data().view.bySlug[strings.ToLower(strings.ReplaceAll(id, ",", "-"))]
	if m == nil {
		s.notFound(w, r)
		return
	}
	if id != m.Slug { // "MacBookPro5,1", "macbookpro5-1" → /mac/MacBookPro5-1
		target := "/mac/" + url.PathEscape(m.Slug)
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}
	desc := fmt.Sprintf("%s (%s): Omarchy compatibility for %d configuration%s, %s.", m.Identifier, m.Title,
		len(m.Configs), map[bool]string{true: "", false: "s"}[len(m.Configs) == 1], m.Years)
	s.render(w, r, http.StatusOK, "mac", page{Title: m.Identifier + " · " + m.Title, Description: desc, Canonical: BaseURL + "/mac/" + url.PathEscape(m.Slug),
		Data: macData{Mac: m, Matrix: r.URL.Query().Get("view") == "matrix"}})
}

// ── browse ────────────────────────────────────────────────────────────────

type macsData struct {
	Q       string
	Sort    string
	Rows    []*macView
	Grouped bool // default order: product line headings
	Errors  []string
	Total   int
}

var sortKeys = map[string]bool{"": true, "id": true, "year": true, "-year": true, "configs": true, "-configs": true}

func (s *Server) macs(w http.ResponseWriter, r *http.Request) {
	q := queryParam(r)
	sortBy := r.URL.Query().Get("sort")
	if !sortKeys[sortBy] {
		sortBy = ""
	}
	d := macsData{Q: q, Sort: sortBy, Total: len(s.data().view.macs)}
	rows := s.data().view.macs
	if strings.TrimSpace(q) != "" {
		resp := s.data().index.Search(q)
		d.Errors = resp.Errors
		keep := map[string]bool{}
		for _, res := range resp.Results {
			keep[res.Identifier] = true
		}
		rows = nil
		for _, m := range s.data().view.macs {
			if keep[m.Identifier] {
				rows = append(rows, m)
			}
		}
	}
	rows = append([]*macView{}, rows...)
	switch sortBy {
	case "id":
		sort.SliceStable(rows, func(i, j int) bool { return lessIdentifier(rows[i].Identifier, rows[j].Identifier) })
	case "year", "-year":
		sort.SliceStable(rows, func(i, j int) bool {
			if sortBy == "-year" {
				return rows[i].FirstYear > rows[j].FirstYear
			}
			return rows[i].FirstYear < rows[j].FirstYear
		})
	case "configs", "-configs":
		sort.SliceStable(rows, func(i, j int) bool {
			if sortBy == "-configs" {
				return len(rows[i].Configs) > len(rows[j].Configs)
			}
			return len(rows[i].Configs) < len(rows[j].Configs)
		})
	default:
		d.Grouped = true
	}
	d.Rows = rows
	s.render(w, r, http.StatusOK, "macs", page{Title: "All Intel Macs", Nav: "macs", Data: d})
}

// ── stats ─────────────────────────────────────────────────────────────────

type countRow struct {
	Label string
	Count int
	Max   int
	URL   string // the matching configurations
}

type statsData struct {
	Lines []countRow
	Years []countRow
	Top   []topList
	// Test results (empty until the first accepted result).
	Results    int
	MostTested []countRow
	MostFailed []countRow
	Recent     []store.ResultSummary
}

type topList struct {
	Title string
	Rows  []countRow
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	var d statsData
	maxLine := 0
	for _, l := range s.data().view.site.Lines {
		maxLine = max(maxLine, l.Configs)
	}
	for _, l := range s.data().view.site.Lines {
		d.Lines = append(d.Lines, countRow{l.Name, l.Configs, maxLine, "/configs?line=" + url.QueryEscape(l.Key)})
	}
	years := map[int]int{}
	comps := map[string]map[string]int{}
	for _, m := range s.data().view.macs {
		for _, rel := range m.Releases {
			y, _ := strconv.Atoi(rel.Announced[:4])
			years[y] += len(rel.Configs)
		}
		for _, c := range m.Configs {
			seen := map[string]bool{}
			for _, comp := range c.Components {
				if (comp.Kind == "gpu" || comp.Kind == "wifi" || comp.Kind == "audio") && !seen[comp.Name] {
					seen[comp.Name] = true
					if comps[comp.Kind] == nil {
						comps[comp.Kind] = map[string]int{}
					}
					comps[comp.Kind][comp.Name]++
				}
			}
		}
	}
	var ys []int
	maxYear := 0
	for y, n := range years {
		ys = append(ys, y)
		maxYear = max(maxYear, n)
	}
	sort.Ints(ys)
	for _, y := range ys {
		d.Years = append(d.Years, countRow{strconv.Itoa(y), years[y], maxYear, "/configs?year=" + strconv.Itoa(y)})
	}
	for _, k := range []struct{ kind, title string }{{"gpu", "Most common GPUs"}, {"wifi", "Most common Wi-Fi chips"}, {"audio", "Most common audio codecs"}} {
		var rows []countRow
		for name, n := range comps[k.kind] {
			rows = append(rows, countRow{Label: name, Count: n, URL: "/configs?" + k.kind + "=" + url.QueryEscape(name)})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Count != rows[j].Count {
				return rows[i].Count > rows[j].Count
			}
			return rows[i].Label < rows[j].Label
		})
		if len(rows) > 8 {
			rows = rows[:8]
		}
		for i := range rows {
			rows[i].Max = rows[0].Count
		}
		d.Top = append(d.Top, topList{k.title, rows})
	}
	s.resultStats(&d)
	s.render(w, r, http.StatusOK, "stats", page{Title: "Stats", Nav: "stats", Data: d})
}

// resultStats fills the "Test results" panel: totals, the most-tested Macs,
// the capabilities that fail on the most configurations, and recent results.
func (s *Server) resultStats(d *statsData) {
	v := s.data().view
	perMac, failing := map[string]int{}, map[string]int{}
	var recent []store.ResultSummary
	names := map[string]string{}
	for _, cp := range s.cat.Capabilities {
		names[cp.ID] = cp.Name
	}
	for _, m := range v.macs {
		for _, cv := range m.Configs {
			d.Results += cv.Status.Results
			perMac[m.Identifier] += cv.Status.Results
			for id, cs := range cv.Status.Caps {
				if cs.Verdict == status.Failed {
					failing[id]++
				}
			}
			for _, x := range cv.Results {
				if x.State == store.Accepted {
					recent = append(recent, x)
				}
			}
		}
	}
	top := func(counts map[string]int, label func(string) string, url func(string) string) []countRow {
		var rows []countRow
		for k, n := range counts {
			if n > 0 {
				rows = append(rows, countRow{Label: label(k), Count: n, URL: url(k)})
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Count != rows[j].Count {
				return rows[i].Count > rows[j].Count
			}
			return rows[i].Label < rows[j].Label
		})
		if len(rows) > 8 {
			rows = rows[:8]
		}
		for i := range rows {
			rows[i].Max = rows[0].Count
		}
		return rows
	}
	d.MostTested = top(perMac, func(k string) string { return k },
		func(k string) string { return "/mac/" + url.PathEscape(catalog.FileSlug(k)) })
	d.MostFailed = top(failing, func(k string) string { return names[k] },
		func(k string) string { return "/search?q=" + url.QueryEscape("status:failed") })
	sort.Slice(recent, func(i, j int) bool {
		if recent[i].TestedAt != recent[j].TestedAt {
			return recent[i].TestedAt > recent[j].TestedAt
		}
		return recent[i].ID > recent[j].ID
	})
	if len(recent) > 8 {
		recent = recent[:8]
	}
	d.Recent = recent
}

// ── diagnostic report pages ───────────────────────────────────────────────

type resultData struct {
	R      *store.ResultDetail
	Config *configView
	Groups []resultGroup
	Other  []store.ResultItem // stored but not counted: the capability doesn't apply
	// Portmap is the configuration's drawing, coloured by this report's own
	// per-port items (PLAN §27); PortItems counts those items.
	Portmap   template.HTML
	PortItems int
}

type resultGroup struct {
	Name, Icon string
	Items      []store.ResultItem
}

// report serves /report/{code}: one accepted (or retracted) diagnostic
// report in full. Pending and rejected reports are never public.
func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !store.IsCode(code) {
		s.notFound(w, r)
		return
	}
	id, err := s.store.ResultIDByCode(r.Context(), code)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rd, err := s.store.Result(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && rd.State != store.Accepted && rd.State != store.Retracted) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := resultData{R: rd, Config: s.data().view.configs[rd.ConfigID]}
	icons := map[string]string{}
	for _, k := range s.cat.Categories {
		icons[k.ID] = iconFor[k.Icon]
	}
	for _, it := range rd.Items {
		if !it.Applicable {
			d.Other = append(d.Other, it)
			continue
		}
		if n := len(d.Groups); n == 0 || d.Groups[n-1].Name != it.CategoryName {
			d.Groups = append(d.Groups, resultGroup{Name: it.CategoryName, Icon: icons[it.CategoryID]})
		}
		g := &d.Groups[len(d.Groups)-1]
		g.Items = append(g.Items, it)
	}
	if cv := d.Config; cv != nil && cv.PortmapKey != "" {
		st := reportStatuses(rd.Items)
		d.Portmap, d.PortItems = portmapHTML(s.cat.Portmaps[cv.PortmapKey], "r", st, cv.PortmapHidden), len(st)
	}
	title := fmt.Sprintf("Diagnostic Report %s · %s", rd.Code, rd.Identifier)
	desc := fmt.Sprintf("Omarchy %s diagnostic report for %s, tested %s: %d passed, %d partly, %d failed.", rd.Omarchy, rd.Identifier,
		formatUTC(rd.TestedAt), rd.Supported, rd.Partial, rd.Failed)
	s.render(w, r, http.StatusOK, "report", page{Title: title, Description: desc, Data: d})
}

// ── methodology / contribute ──────────────────────────────────────────────

type methodologyData struct {
	Categories []catalog.Category
	Caps       map[string][]catalog.Capability
	Icons      map[string]string // category ID → sprite icon
}

func (s *Server) methodology(w http.ResponseWriter, r *http.Request) {
	d := methodologyData{Categories: s.cat.Categories, Caps: map[string][]catalog.Capability{}, Icons: map[string]string{}}
	for _, k := range s.cat.Categories {
		d.Icons[k.ID] = iconFor[k.Icon]
	}
	for _, c := range s.cat.Capabilities {
		if !c.Retired {
			d.Caps[c.Category()] = append(d.Caps[c.Category()], c)
		}
	}
	s.render(w, r, http.StatusOK, "methodology", page{Title: "Methodology", Nav: "methodology", Data: d})
}

func (s *Server) contribute(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "contribute", page{Title: "Contribute", Nav: "contribute"})
}

// appliesText describes a capability's applicability rule in words.
func (s *Server) appliesText(c catalog.Capability) string {
	if c.When == nil {
		return "every configuration"
	}
	var parts []string
	if len(c.When.All) > 0 {
		var xs []string
		for _, t := range c.When.All {
			xs = append(xs, s.tagText(t))
		}
		parts = append(parts, strings.Join(xs, " and "))
	}
	if len(c.When.Any) > 0 {
		var xs []string
		for _, t := range c.When.Any {
			xs = append(xs, s.tagText(t))
		}
		parts = append(parts, strings.Join(xs, " or "))
	}
	if len(c.When.None) > 0 {
		var xs []string
		for _, t := range c.When.None {
			xs = append(xs, s.tagText(t))
		}
		parts = append(parts, "not "+strings.Join(xs, " or "))
	}
	return "configurations with " + strings.Join(parts, ", and ")
}

func (s *Server) tagText(t string) string {
	if t == "video-out" {
		return "a video output"
	}
	ns, v, _ := strings.Cut(t, ":")
	voc := &s.cat.Vocab
	switch ns {
	case "efi":
		return v + "-bit EFI firmware"
	case "chip":
		if v == "none" {
			return "no security chip"
		}
		return "an " + voc.SecurityChips[v].Name + " chip"
	case "form":
		return "a " + v + " form factor"
	case "feature":
		return lowerFirst(voc.Features[v].Name)
	case "port":
		return "a " + voc.Ports[v].Name + " port"
	case "has":
		return article(lowerFirst(voc.ComponentKinds[v].Name))
	case "gpu":
		return article(v + " GPU")
	}
	return t
}

// ── criteria matrix, catalog lists, attribution ───────────────────────────

type lineMatrix struct {
	Key, Name string
	Matrix    *matrixView
}

type criteriaData struct{ Lines []lineMatrix }

// criteria serves /criteria: one review-report-style matrix per product line.
func (s *Server) criteria(w http.ResponseWriter, r *http.Request) {
	byLine := map[string][]*configView{}
	names := map[string]string{}
	for _, m := range s.data().view.macs {
		byLine[m.LineKey] = append(byLine[m.LineKey], m.Configs...)
		names[m.LineKey] = m.LineName
	}
	var d criteriaData
	for _, k := range orderedLines(byLine) {
		d.Lines = append(d.Lines, lineMatrix{k, names[k], buildMatrix(s.cat, byLine[k], true)})
	}
	s.render(w, r, http.StatusOK, "criteria", page{Title: "Criteria matrix", Data: d})
}

func (s *Server) releases(w http.ResponseWriter, r *http.Request) {
	var rows []*releaseView
	for _, m := range s.data().view.macs {
		rows = append(rows, m.Releases...)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Announced > rows[j].Announced })
	s.render(w, r, http.StatusOK, "releases", page{Title: "Releases", Data: rows})
}

type configsData struct {
	Title, Note string
	Rows        []*configView
}

// configList serves /configs, optionally filtered: ?scope=in, ?status=…,
// ?excluded=<coverage reason>, or ?q= (search syntax).
func (s *Server) configList(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	d := configsData{Title: "All configurations"}
	keep := func(*configView) bool { return true }
	switch {
	case qs.Get("scope") == "in":
		d.Title, d.Note = "Configurations counted in coverage", "These make up N in both coverage figures."
		keep = func(c *configView) bool { return c.OutOfScope == "" && c.Status.Verdict != status.NotCompatible }
	case qs.Get("status") != "":
		v := status.Verdict(qs.Get("status"))
		d.Title = v.Label() + " configurations"
		keep = func(c *configView) bool { return c.Status.Verdict == v }
	case qs.Get("excluded") != "":
		reason := qs.Get("excluded")
		d.Title, d.Note = "Out of coverage scope: "+reason, "Listed and testable, but not counted in the coverage figures."
		keep = func(c *configView) bool { return c.OutOfScope == reason && c.Status.Verdict != status.NotCompatible }
	case qs.Get("line") != "":
		key := qs.Get("line")
		d.Title = "Configurations by product line: " + key
		for _, l := range s.data().view.site.Lines {
			if l.Key == key {
				d.Title = l.Name + " configurations"
			}
		}
		keep = func(c *configView) bool { return c.Mac.LineKey == key }
	case qs.Get("year") != "":
		y := qs.Get("year")
		d.Title = "Configurations released in " + y
		in := map[string]bool{}
		for _, m := range s.data().view.macs {
			for _, rel := range m.Releases {
				if strings.HasPrefix(rel.Announced, y+"-") {
					for _, c := range rel.Configs {
						in[c.ID] = true
					}
				}
			}
		}
		keep = func(c *configView) bool { return in[c.ID] }
	case qs.Get("gpu") != "" || qs.Get("wifi") != "" || qs.Get("audio") != "":
		kind, name := "gpu", qs.Get("gpu")
		if name == "" {
			kind, name = "wifi", qs.Get("wifi")
		}
		if name == "" {
			kind, name = "audio", qs.Get("audio")
		}
		d.Title = "Configurations with " + name
		keep = func(c *configView) bool {
			for _, comp := range c.Components {
				if comp.Kind == kind && comp.Name == name {
					return true
				}
			}
			return false
		}
	case strings.TrimSpace(qs.Get("q")) != "":
		q := queryParam(r)
		d.Title = "Configurations matching " + q
		hit := map[string]bool{}
		for _, res := range s.data().index.Search(q).Results {
			for _, c := range res.Configs {
				hit[c.ID] = true
			}
		}
		keep = func(c *configView) bool { return hit[c.ID] }
	}
	for _, m := range s.data().view.macs {
		for _, c := range m.Configs {
			if keep(c) {
				d.Rows = append(d.Rows, c)
			}
		}
	}
	s.render(w, r, http.StatusOK, "configs", page{Title: d.Title, Data: d})
}

func (s *Server) componentList(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "components", page{Title: "Components", Data: s.data().view.components})
}

func (s *Server) changelog(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "changelog", page{Title: "Catalog changelog", Data: s.cat.Changelog})
}

func (s *Server) attribution(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "attribution", page{Title: "Attribution"})
}

// lowerFirst lower-cases the first letter only ("Switchable graphics (two GPUs)").
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// article prefixes "a" or "an".
func article(s string) string {
	if s != "" && strings.ContainsRune("aeiouAEIOU", rune(s[0])) {
		return "an " + s
	}
	return "a " + s
}

// ── robots / sitemap ──────────────────────────────────────────────────────

func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "User-agent: *\nAllow: /\nDisallow: /search/suggest\nDisallow: /admin\nDisallow: /api/register/\n\nSitemap: %s/sitemap.xml\n", BaseURL)
}

func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, p := range []string{"/", "/macs", "/criteria", "/stats", "/methodology", "/contribute", "/releases", "/configs", "/components", "/attribution", "/changelog", "/identify", "/api", "/api/register", "/privacy"} {
		fmt.Fprintf(&b, "  <url><loc>%s%s</loc></url>\n", BaseURL, p)
	}
	d := s.data()
	for _, m := range d.view.macs {
		fmt.Fprintf(&b, "  <url><loc>%s/mac/%s</loc></url>\n", BaseURL, url.PathEscape(m.Slug))
	}
	for _, rs := range d.view.rollup.Accepted {
		for _, x := range rs {
			if x.State == store.Accepted {
				fmt.Fprintf(&b, "  <url><loc>%s/report/%s</loc></url>\n", BaseURL, x.Code)
			}
		}
	}
	b.WriteString("</urlset>\n")
	w.Write([]byte(b.String()))
}

// ── privacy and API docs (PLAN §22.7, §22.11) ─────────────────────────────

func (s *Server) privacy(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "privacy", page{Title: "Privacy",
		Description: "What DoesItOmarchy does to protect your privacy: no ads, no analytics, no tracking, and personal data removed from diagnostic reports."})
}

// ConsentNotice is the notice test tools must show before submitting (PLAN §22.1).
const ConsentNotice = "This sends your test results to DoesItOmarchy.com. Serial numbers, network and IP addresses, " +
	"computer and user names, and e-mail addresses are removed first. Once a maintainer accepts the report, " +
	"its results, your Mac's hardware details and your handle (if you give one) are public, and the full report, " +
	"with that personal data removed, may be published later. Submitting means you agree. " +
	"More at doesitomarchy.com/privacy."
