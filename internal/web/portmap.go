package web

import (
	"html/template"
	"net/http"
	"regexp"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// Port map drawings on the site (PLAN.md §27). The catalog holds two
// flavours of each release's drawing: the site one is inlined in Mac and
// report pages, with each port coloured by its connector's status; the
// standalone one is served for download.

// Connector statuses, as the data-st attribute the site CSS colours.
const (
	pmSupported = "supported" // every criterion that applies passed on this connector
	pmCovered   = "covered"   // every criterion passed here or on a group-mate (same port group)
	pmPartly    = "partly"    // some passed, none failed, the rest untested
	pmPartial   = "partial"   // a criterion only partly works on it, or some passed and some failed
	pmFailed    = "failed"
	pmSuspect   = "suspect" // failed while a group-mate passed: possibly a damaged port
)

// pmWords names each status as the colour key does (the breakdown under the
// drawing).
var pmWords = map[string]string{
	pmSupported: "passed", pmCovered: "covered by port group", pmPartly: "partly tested",
	pmPartial: "partly works", pmFailed: "failed", pmSuspect: "possible hardware fault",
}

// pmLabels explains each status (list markers' titles, the colour key).
var pmLabels = map[string]string{
	pmSupported: "passed every test", pmCovered: "passed, or covered by a port in the same port group",
	pmPartly: "partly tested, nothing failed", pmPartial: "partly works", pmFailed: "failed",
	pmSuspect: "failed while a port in the same port group passed: possibly a damaged port",
}

// connStatuses folds a configuration's per-connector criterion results
// (PLAN §25) into one status per connector, and lists each criterion's
// result on it ("External display output failed"). Connectors without results, or without
// per-connector criteria (MagSafe, power), are left out: untested. A
// connector that passed some criteria and failed others partly works, as a
// criterion does when some port groups pass and others fail.
func connStatuses(cv *configView) (st map[string]string, results map[string][]string) {
	type tally struct{ total, passed, covered, partial, failed, suspect int }
	t := map[string]*tally{}
	each := map[string][]string{} // connector → "<criterion> <result>"
	for _, cat := range cv.Categories {
		for _, cp := range cat.Caps {
			for _, p := range cp.Ports {
				x := t[p.ID]
				if x == nil {
					x = &tally{}
					t[p.ID] = x
				}
				x.total++
				res := "untested"
				switch {
				case p.Suspect:
					x.suspect++
					res = "failed while its port group passed"
				case p.Verdict == status.Supported:
					x.passed++
					res = "passed"
				case p.CoveredBy != "":
					x.covered++
					res = "covered by " + p.CoveredBy
				case p.Verdict == status.Failed:
					x.failed++
					res = "failed"
				case p.Verdict == status.Partial:
					x.partial++
					res = "partly works"
				}
				each[p.ID] = append(each[p.ID], cp.Name+" "+res)
			}
		}
	}
	st = map[string]string{}
	for id, x := range t {
		switch {
		case x.suspect > 0:
			st[id] = pmSuspect
		case x.failed > 0 && x.passed+x.covered > 0:
			st[id] = pmPartial
		case x.failed > 0:
			st[id] = pmFailed
		case x.partial > 0:
			st[id] = pmPartial
		case x.passed == x.total:
			st[id] = pmSupported
		case x.passed+x.covered == x.total:
			st[id] = pmCovered
		case x.passed+x.covered > 0:
			st[id] = pmPartly
		}
	}
	return st, each
}

// reportStatuses is the same fold for one report's own per-connector items.
func reportStatuses(items []store.ResultItem) map[string]string {
	type tally struct{ total, passed, partial, failed int }
	t := map[string]*tally{}
	for _, it := range items {
		if it.Connector == "" || !it.Applicable {
			continue
		}
		x := t[it.Connector]
		if x == nil {
			x = &tally{}
			t[it.Connector] = x
		}
		x.total++
		switch itemVerdict(it.Status) {
		case status.Supported:
			x.passed++
		case status.Partial:
			x.partial++
		case status.Failed:
			x.failed++
		}
	}
	out := map[string]string{}
	for id, x := range t {
		switch {
		case x.failed > 0 && x.passed > 0:
			out[id] = pmPartial
		case x.failed > 0:
			out[id] = pmFailed
		case x.partial > 0:
			out[id] = pmPartial
		case x.passed == x.total:
			out[id] = pmSupported
		case x.passed > 0:
			out[id] = pmPartly
		}
	}
	return out
}

var reNonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// portmapHTML inlines a drawing's site flavour. Its element IDs (title,
// clip paths, patterns) get a suffix so several drawings can share a page,
// and each port and callout of a connector with a status gets data-st.
func portmapHTML(pm *catalog.Portmap, suffix string, st map[string]string) template.HTML {
	if pm == nil {
		return ""
	}
	s := string(pm.Site)
	if i := strings.Index(s, "<svg"); i > 0 {
		s = s[i:] // nothing before the root element
	}
	uid := "pm-" + reNonSlug.ReplaceAllString(strings.ToLower(pm.Key), "-")
	s = strings.ReplaceAll(s, uid+"-", uid+"-"+suffix+"-")
	for id, v := range st {
		s = strings.ReplaceAll(s, `data-conn="`+id+`"`, `data-conn="`+id+`" data-st="`+v+`"`)
	}
	return template.HTML(strings.TrimSpace(s)) // generated catalog data, validated at load
}

// portmapFragment serves a configuration's drawing, coloured by its results,
// as an HTML fragment: /portmap/<key>/<config ID>. Mac pages fetch it when
// the Hardware details open, so a page with many configurations doesn't
// carry every drawing up front (the 60 KB page budget).
func (s *Server) portmapFragment(w http.ResponseWriter, r *http.Request) {
	cv := s.data().view.configs[r.PathValue("cfg")]
	if cv == nil || cv.PortmapKey == "" || cv.PortmapKey != r.PathValue("key") {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=0, s-maxage=300") // purged with every page when results change
	w.Write([]byte(cv.Portmap))
}

// portmap serves a drawing's standalone flavour: /portmap/<key>.svg.
func (s *Server) portmap(w http.ResponseWriter, r *http.Request) {
	key, ok := strings.CutSuffix(r.PathValue("file"), ".svg")
	pm := s.cat.Portmaps[key]
	if !ok || pm == nil {
		s.notFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml; charset=utf-8")
	// The standalone drawing carries its own <style>; nothing else may run.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	h.Set("Cache-Control", "public, max-age=86400")
	if r.URL.Query().Has("download") {
		h.Set("Content-Disposition", `attachment; filename="`+key+`.svg"`)
	}
	w.Write(pm.Download)
}
