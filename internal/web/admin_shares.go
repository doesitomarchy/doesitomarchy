package web

import (
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/match"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// /admin/shares: the hardware IDs visitors shared from Identify my Mac
// (PF-3, PLAN.md §23.3), grouped by the model identifier they reported and
// read against today's catalog. Maintainers turn good ones into catalog PRs.

type shareGroupView struct {
	Product  string   // as reported; "" when none was
	Mac      *macView // nil when the catalog lacks the identifier
	Total    int
	Unseen   int
	Reviewed string
	Probes   []shareProbeView
	Modified []tally
	Releases []tally
}

// shareProbeView is one distinct set of IDs within a group.
type shareProbeView struct {
	Board      string
	BoardKnown bool
	BoardOf    []string // releases the board is tied to (PLAN.md §29)
	CPU        string
	CPUKnown   bool
	PCI        []idView // new IDs first, then the catalog's components
	Plumbing   []idView // chipset IDs the catalog leaves out on purpose (data/plumbing.yaml)
	Count      int
	First      string // shared_on of the oldest and newest share
	Last       string
	Best       *configView   // the configuration it matches today, when exact
	Tied       []*configView // when it matches several equally
}

type idView struct {
	ID     string
	Known  bool
	Name   string // plumbing only
	Lookup string // pci-ids.ucw.cz page, for new IDs
}

type tally struct {
	Label string
	N     int
}

type adminSharesData struct {
	Who    string
	CSRF   string
	All    bool
	Groups []shareGroupView
	Done   string
}

func (s *Server) adminShares(w http.ResponseWriter, r *http.Request, who string) {
	ctx := r.Context()
	all := r.URL.Query().Get("all") != ""
	groups, err := s.store.ShareGroups(ctx, all)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := adminSharesData{Who: who, CSRF: s.csrf(ctx, who), All: all, Done: r.URL.Query().Get("done")}
	for _, g := range groups {
		d.Groups = append(d.Groups, s.shareGroup(g))
	}
	// Identifiers the catalog lacks first: they're what the catalog misses.
	sort.SliceStable(d.Groups, func(i, j int) bool { return d.Groups[i].Mac == nil && d.Groups[j].Mac != nil })
	s.render(w, r, http.StatusOK, "admin-shares", page{Title: "Shared IDs", Data: d})
}

var modifiedLabels = map[string]string{"yes": "Modified", "no": "Not modified", "unsure": "Not sure", "": "No answer"}

func (s *Server) shareGroup(g store.ShareGroup) shareGroupView {
	view := s.data().view
	gv := shareGroupView{Product: g.Product, Total: len(g.Shares), Unseen: g.Unseen, Reviewed: g.Reviewed}
	if g.Product != "" {
		if mv := view.bySlug[strings.ToLower(strings.ReplaceAll(g.Product, ",", "-"))]; mv != nil && mv.Identifier == g.Product {
			gv.Mac = mv
		}
	}
	modified, releases := map[string]int{}, map[string]int{}
	probes := map[string]*shareProbeView{}
	var order []string
	for _, sh := range g.Shares {
		modified[sh.Modified]++
		if sh.Release != "" {
			releases[sh.Release]++
		}
		key := sh.BoardID + "\x00" + sh.CPU + "\x00" + strings.Join(sh.PCI, ",")
		pv := probes[key]
		if pv == nil {
			pv = s.shareProbe(sh)
			if gv.Mac != nil && sh.BoardID != "" {
				for _, rv := range gv.Mac.Releases {
					if slices.ContainsFunc(rv.BoardIDs, func(b string) bool { return strings.EqualFold(b, sh.BoardID) }) {
						pv.BoardOf = append(pv.BoardOf, rv.Name)
					}
				}
			}
			probes[key] = pv
			order = append(order, key)
		}
		pv.Count++
		if pv.First == "" || sh.SharedOn < pv.First {
			pv.First = sh.SharedOn
		}
		if sh.SharedOn > pv.Last {
			pv.Last = sh.SharedOn
		}
	}
	for _, k := range order {
		gv.Probes = append(gv.Probes, *probes[k])
	}
	sort.SliceStable(gv.Probes, func(i, j int) bool { return gv.Probes[i].Count > gv.Probes[j].Count })
	for _, k := range []string{"yes", "no", "unsure", ""} {
		if n := modified[k]; n > 0 {
			gv.Modified = append(gv.Modified, tally{modifiedLabels[k], n})
		}
	}
	for id, n := range releases {
		label := id
		if gv.Mac != nil {
			for _, rv := range gv.Mac.Releases {
				if rv.ID == id {
					label = rv.Name
				}
			}
		}
		gv.Releases = append(gv.Releases, tally{label, n})
	}
	sort.Slice(gv.Releases, func(i, j int) bool {
		a, b := gv.Releases[i], gv.Releases[j]
		return a.N > b.N || a.N == b.N && a.Label < b.Label
	})
	return gv
}

// shareProbe reads one share's IDs against today's catalog.
func (s *Server) shareProbe(sh store.Share) *shareProbeView {
	pv := &shareProbeView{Board: sh.BoardID, CPU: sh.CPU}
	pv.BoardKnown = sh.BoardID == "" || s.match.KnownBoard(sh.BoardID)
	pv.CPUKnown = sh.CPU == "" || s.match.KnownCPU(sh.CPU)
	var known []idView
	for _, id := range sh.PCI {
		switch p, plumbing := s.match.Plumbing(id); {
		case s.match.KnownDevice(id, "pci"):
			known = append(known, idView{ID: id, Known: true})
		case plumbing:
			pv.Plumbing = append(pv.Plumbing, idView{ID: id, Known: true, Name: p.Name})
		default:
			pv.PCI = append(pv.PCI, idView{ID: id, Lookup: pciLookup(id)})
		}
	}
	pv.PCI = append(pv.PCI, known...)
	res := s.match.Match(match.Probe{ProductName: sh.Product, BoardID: sh.BoardID, CPU: sh.CPU, PCI: sh.PCI})
	view := s.data().view
	if res.Exact {
		pv.Best = view.configs[res.Best()]
	} else {
		for _, c := range res.Candidates {
			if cv := view.configs[c.Config]; cv != nil && c.Score == res.Candidates[0].Score && len(pv.Tied) < 12 {
				pv.Tied = append(pv.Tied, cv)
			}
		}
	}
	return pv
}

// pciLookup is the pci-ids.ucw.cz page that names a PCI ID ("" if malformed).
func pciLookup(id string) string {
	ids := match.NormalizeIDs([]string{id}, "pci")
	if len(ids) != 1 || !strings.HasPrefix(ids[0], "pci:") {
		return ""
	}
	return "https://pci-ids.ucw.cz/read/PC/" + strings.ReplaceAll(strings.TrimPrefix(ids[0], "pci:"), ":", "/")
}

func (s *Server) adminSharesReview(w http.ResponseWriter, r *http.Request, who string) {
	product := r.FormValue("product")
	n, err := s.store.ReviewShares(r.Context(), product, who)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	label := product
	if label == "" {
		label = "no identifier"
	}
	q := url.Values{"done": {label + ": " + strconv.FormatInt(n, 10) + " marked reviewed"}}
	http.Redirect(w, r, "/admin/shares?"+q.Encode(), http.StatusSeeOther)
}
