package web

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/store"
	pm "github.com/doesitomarchy/doesitomarchy/pkg/match"
)

// Port wiring learned from reports (LIVE-PLAN §23.1): which bus paths sit
// behind which connector of a configuration. Every Mac of a configuration is
// wired the same, so the snapshot gives it to test tools, whose port check
// then starts on the port the wiring names (the tester confirms or corrects).

// configWiring is one configuration's wiring, as its accepted reports agree
// on it.
type configWiring struct {
	Conns   map[string][]string // connector → its paths, sorted
	Dropped []wiringConflict    // agreed paths that sat on more than one connector
}

// wiringConflict is a path the reports put on several connectors: it
// counts on none of them until a maintainer sorts it out.
type wiringConflict struct {
	Path       string
	Connectors []string
}

// buildWiring applies the agreement rule to a configuration's accepted
// ports entries: a (connector, path) pair counts when two distinct accepted
// reports have it, or one whose wiring a maintainer checked. A counted path
// on two connectors counts on neither. Entries for a connector the layout
// doesn't have (a report whose configuration changed) are left out.
func buildWiring(rows []store.PortRow, layout map[string]bool) configWiring {
	type pair struct{ conn, path string }
	reports, checked := map[pair]map[int64]bool{}, map[pair]bool{}
	for _, r := range rows {
		if !layout[r.Connector] {
			continue
		}
		for _, p := range r.Paths {
			k := pair{r.Connector, p}
			if reports[k] == nil {
				reports[k] = map[int64]bool{}
			}
			reports[k][r.ResultID] = true
			checked[k] = checked[k] || r.Checked
		}
	}
	on := map[string][]string{} // path → connectors it counts on
	for k, ids := range reports {
		if len(ids) >= 2 || checked[k] {
			on[k.path] = append(on[k.path], k.conn)
		}
	}
	w := configWiring{Conns: map[string][]string{}}
	for path, conns := range on {
		if len(conns) > 1 {
			sort.Slice(conns, func(i, j int) bool { return connLess(conns[i], conns[j]) })
			w.Dropped = append(w.Dropped, wiringConflict{path, conns})
			continue
		}
		w.Conns[conns[0]] = append(w.Conns[conns[0]], path)
	}
	for _, paths := range w.Conns {
		slices.Sort(paths)
	}
	sort.Slice(w.Dropped, func(i, j int) bool { return w.Dropped[i].Path < w.Dropped[j].Path })
	return w
}

// snapshot is the wiring as GET /api/v1/snapshot gives it, sorted by connector.
func (w configWiring) snapshot() []pm.SnapshotWiring {
	var out []pm.SnapshotWiring
	for conn, paths := range w.Conns {
		out = append(out, pm.SnapshotWiring{Connector: conn, Paths: paths})
	}
	sort.Slice(out, func(i, j int) bool { return connLess(out[i].Connector, out[j].Connector) })
	return out
}

// connectorOf says which connector the wiring puts a path on, or "".
func (w configWiring) connectorOf(path string) string {
	for conn, paths := range w.Conns {
		if slices.Contains(paths, path) {
			return conn
		}
	}
	return ""
}

// connLess orders connector IDs by side, then by number: left-2 before left-10.
func connLess(a, b string) bool {
	sa, na, _ := strings.Cut(a, "-")
	sb, nb, _ := strings.Cut(b, "-")
	if sa != sb {
		return sa < sb
	}
	ia, _ := strconv.Atoi(na)
	ib, _ := strconv.Atoi(nb)
	if ia != ib {
		return ia < ib
	}
	return a < b
}

// wiringOf builds every configuration's wiring from the accepted reports'
// ports entries, checked against each configuration's layout.
func (s *Server) wiringOf(ports map[string][]store.PortRow) map[string]configWiring {
	out := map[string]configWiring{}
	for cfgID, rows := range ports {
		cfg := s.cfgs[cfgID]
		if cfg == nil {
			continue
		}
		layout := map[string]bool{}
		for _, cn := range cfg.Connectors {
			layout[cn.ID] = true
		}
		if w := buildWiring(rows, layout); len(w.Conns) > 0 || len(w.Dropped) > 0 {
			out[cfgID] = w
		}
	}
	return out
}
