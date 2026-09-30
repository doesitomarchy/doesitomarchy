package web

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/search"
)

// preset is one of the quick searches under the home search bar (PLAN §17.7).
type preset struct {
	Label, Query, Sort string
}

var presets = []preset{
	{"MacBook Pros", "line:macbook-pro", ""},
	{"iMacs", "line:imac", ""},
	{"MacBook", "line:macbook", ""},
	{"MacBook Airs", "line:macbook-air", ""},
	{"iMac Pros", "line:imac-pro", ""},
	{"Mac Minis", "line:mac-mini", ""},
	{"Mac Pros", "line:mac-pro", ""},
	{"All Compatible", "status:supported", ""},
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
	resp := s.index.Search(q)
	if sortBy == "recent" {
		search.SortRecent(resp.Results)
	}
	d := searchData{Q: q, Sort: sortBy, Resp: resp, Presets: presets}
	for _, res := range resp.Results {
		rv := resultView{Result: res, Mac: s.view.bySlug[strings.ToLower(res.Slug)]}
		for _, hit := range res.Configs {
			rv.Scoped = append(rv.Scoped, s.view.configs[hit.ID])
		}
		d.Results = append(d.Results, rv)
	}
	lq := strings.ToLower(q)
	if len(resp.Results) == 0 && s.view.site.Coverage.Tested == 0 && (strings.Contains(lq, "tested:") || strings.Contains(lq, "status:")) {
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
		s.renderPartial(w, r, "search", "results", d)
		return
	}
	d.OnSearchPage = true
	title := "Search"
	if q != "" {
		title = q + " · search"
	}
	s.render(w, r, http.StatusOK, "search", page{Title: title, Data: d})
}

// suggest serves GET /search/suggest?q=&cursor=: an HTML list of completions.
// Each entry is a plain link, so it also works without JavaScript.
func (s *Server) suggest(w http.ResponseWriter, r *http.Request) {
	q := queryParam(r)
	cursor := -1
	if c, err := strconv.Atoi(r.URL.Query().Get("cursor")); err == nil {
		cursor = c
	}
	s.renderPartial(w, r, "suggest", "suggestions", s.index.Suggest(q, cursor))
}

// ── model page ────────────────────────────────────────────────────────────

type macData struct {
	Mac    *macView
	Matrix bool
}

func (s *Server) mac(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m := s.view.bySlug[strings.ToLower(strings.ReplaceAll(id, ",", "-"))]
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
	s.render(w, r, http.StatusOK, "mac", page{Title: m.Identifier + " · " + m.Title, Description: desc,
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
	d := macsData{Q: q, Sort: sortBy, Total: len(s.view.macs)}
	rows := s.view.macs
	if strings.TrimSpace(q) != "" {
		resp := s.index.Search(q)
		d.Errors = resp.Errors
		keep := map[string]bool{}
		for _, res := range resp.Results {
			keep[res.Identifier] = true
		}
		rows = nil
		for _, m := range s.view.macs {
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
}

type statsData struct {
	Lines []countRow
	Years []countRow
	Top   []topList
}

type topList struct {
	Title string
	Rows  []countRow
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	var d statsData
	maxLine := 0
	for _, l := range s.view.site.Lines {
		maxLine = max(maxLine, l.Configs)
	}
	for _, l := range s.view.site.Lines {
		d.Lines = append(d.Lines, countRow{l.Name, l.Configs, maxLine})
	}
	years := map[int]int{}
	comps := map[string]map[string]int{}
	for _, m := range s.view.macs {
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
		d.Years = append(d.Years, countRow{strconv.Itoa(y), years[y], maxYear})
	}
	for _, k := range []struct{ kind, title string }{{"gpu", "Most common GPUs"}, {"wifi", "Most common Wi-Fi chips"}, {"audio", "Most common audio codecs"}} {
		var rows []countRow
		for name, n := range comps[k.kind] {
			rows = append(rows, countRow{Label: name, Count: n})
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
	s.render(w, r, http.StatusOK, "stats", page{Title: "Stats", Nav: "stats", Data: d})
}

// ── methodology / contribute ──────────────────────────────────────────────

type methodologyData struct {
	Categories []catalog.Category
	Caps       map[string][]catalog.Capability
}

func (s *Server) methodology(w http.ResponseWriter, r *http.Request) {
	d := methodologyData{Categories: s.cat.Categories, Caps: map[string][]catalog.Capability{}}
	for _, c := range s.cat.Capabilities {
		d.Caps[c.Category()] = append(d.Caps[c.Category()], c)
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
		return strings.ToLower(voc.Features[v].Name)
	case "port":
		return "a " + voc.Ports[v].Name + " port"
	case "has":
		return "a " + strings.ToLower(voc.ComponentKinds[v].Name)
	case "gpu":
		return "a " + v + " GPU"
	}
	return t
}

// ── robots / sitemap ──────────────────────────────────────────────────────

func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "User-agent: *\nAllow: /\nDisallow: /search/suggest\n\nSitemap: %s/sitemap.xml\n", BaseURL)
}

func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, p := range []string{"/", "/macs", "/stats", "/methodology", "/contribute"} {
		fmt.Fprintf(&b, "  <url><loc>%s%s</loc></url>\n", BaseURL, p)
	}
	for _, m := range s.view.macs {
		fmt.Fprintf(&b, "  <url><loc>%s/mac/%s</loc></url>\n", BaseURL, url.PathEscape(m.Slug))
	}
	b.WriteString("</urlset>\n")
	w.Write([]byte(b.String()))
}
