// Package report renders a catalog batch as a single HTML page for human
// review: every config as a complete computer, its components and hardware
// IDs, which test criteria apply, and every field flagged as uncertain.
package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

//go:embed report.html.tmpl
var pageTmpl string

// Options selects what to render.
type Options struct {
	Line  string        // vocabulary line key; empty = every line
	Intro template.HTML // optional reviewer notes rendered above the data
	Now   time.Time
}

// kindOrder is the display order of component kinds on a config card.
var kindOrder = []string{"gpu", "wifi", "bluetooth", "audio", "camera", "ethernet", "storage",
	"thunderbolt", "card-reader", "firewire", "ir", "input", "bridge"}

type page struct {
	Title, LineName, Generated string
	Intro                      template.HTML
	Stats                      catalog.Stats
	Macs                       []macView
	Uncertain                  []uncertainView
	Matrix                     matrix
}

type macView struct {
	*catalog.Mac
	Anchor, EFI, Chip string
	Releases          []releaseView
}

type releaseView struct {
	*catalog.Release
	Configs []configView
}

type configView struct {
	*catalog.Config
	Components  []compView
	Ports       []string
	Features    []string
	Applies     int
	UncertainBy map[string]string // field → note
}

type compView struct {
	Kind string
	BTO  bool
	*catalog.Component
}

type uncertainView struct{ Where, Anchor, Field, Note string }

type matrix struct {
	Configs []matrixCol
	Groups  []matrixGroup
}

type matrixCol struct{ ID, Short, Anchor string }

type matrixGroup struct {
	Category catalog.Category
	Rows     []matrixRow
}

type matrixRow struct {
	Cap   catalog.Capability
	Cells []bool
	Count int
}

// Render writes the review page for the selected line.
func Render(w io.Writer, c *catalog.Catalog, opt Options) error {
	lineName := "All lines"
	if opt.Line != "" {
		l, ok := c.Vocab.Lines[opt.Line]
		if !ok {
			return fmt.Errorf("unknown line %q", opt.Line)
		}
		lineName = l.Name
	}
	p := page{
		Title:     lineName + " Catalog Review",
		LineName:  lineName,
		Generated: opt.Now.UTC().Format("2006-01-02 15:04 UTC"),
		Intro:     opt.Intro,
	}

	var macs []*catalog.Mac
	for _, m := range c.Macs {
		if opt.Line == "" || m.Line == opt.Line {
			macs = append(macs, m)
		}
	}
	sort.Slice(macs, func(i, j int) bool { return identLess(macs[i].Identifier, macs[j].Identifier) })

	seenComp := map[string]bool{}
	for _, m := range macs {
		mv := macView{Mac: m, Anchor: catalog.FileSlug(m.Identifier), EFI: strconv.Itoa(m.EFI) + "-bit EFI",
			Chip: c.Vocab.SecurityChips[m.SecurityChip].Name}
		for _, u := range m.Uncertain {
			p.Uncertain = append(p.Uncertain, uncertainView{m.Identifier, mv.Anchor, u.Field, u.Note})
		}
		p.Stats.Macs++
		for ri := range m.Releases {
			r := &m.Releases[ri]
			rv := releaseView{Release: r}
			p.Stats.Releases++
			for ci := range r.Configs {
				cfg := &r.Configs[ci]
				cv := configView{Config: cfg, Applies: len(c.Applicable(m, cfg)), UncertainBy: map[string]string{}}
				for _, u := range cfg.Uncertain {
					cv.UncertainBy[u.Field] = u.Note
					p.Uncertain = append(p.Uncertain, uncertainView{cfg.ID, "cfg-" + cfg.ID, u.Field, u.Note})
				}
				cv.Components = components(c, cfg)
				for _, comp := range cv.Components {
					if !seenComp[comp.ID] {
						seenComp[comp.ID] = true
						for _, u := range comp.Uncertain {
							p.Uncertain = append(p.Uncertain, uncertainView{comp.ID, "", u.Field, u.Note})
						}
					}
				}
				for port, n := range cfg.Ports {
					name := c.Vocab.Ports[port].Name
					if n > 1 {
						name = fmt.Sprintf("%s ×%d", name, n)
					}
					cv.Ports = append(cv.Ports, name)
				}
				sort.Strings(cv.Ports)
				for _, f := range cfg.Features {
					cv.Features = append(cv.Features, c.Vocab.Features[f].Name)
				}
				rv.Configs = append(rv.Configs, cv)
				p.Stats.Configs++
				p.Stats.Uncertain += len(cfg.Uncertain)
				p.Matrix.Configs = append(p.Matrix.Configs, matrixCol{cfg.ID, shortID(m.Identifier, cfg.ID), "cfg-" + cfg.ID})
			}
			mv.Releases = append(mv.Releases, rv)
		}
		p.Stats.Uncertain += len(m.Uncertain)
		p.Macs = append(p.Macs, mv)
	}
	p.Stats.Components = len(seenComp)
	for id := range seenComp {
		p.Stats.Uncertain += len(c.Components[id].Uncertain)
	}
	p.Stats.Capabilities = len(c.Capabilities)
	p.Matrix.Groups = buildMatrix(c, macs)

	t, err := template.New("page").Funcs(template.FuncMap{
		"join":  strings.Join,
		"procs": procs,
		"gb":    gbList,
		"num":   func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) },
		"host":  host,
	}).Parse(pageTmpl)
	if err != nil {
		return err
	}
	return t.Execute(w, p)
}

func components(c *catalog.Catalog, cfg *catalog.Config) []compView {
	var out []compView
	for i, list := range [][]string{cfg.Components, cfg.BTOComponents} {
		for _, id := range list {
			comp := c.Components[id]
			out = append(out, compView{Kind: c.Vocab.ComponentKinds[comp.Kind].Name, BTO: i == 1, Component: comp})
		}
	}
	rank := map[string]int{}
	for i, k := range kindOrder {
		rank[k] = i
	}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Component.Kind] < rank[out[j].Component.Kind] })
	return out
}

func buildMatrix(c *catalog.Catalog, macs []*catalog.Mac) []matrixGroup {
	type pair struct {
		m   *catalog.Mac
		cfg *catalog.Config
	}
	var cols []pair
	for _, m := range macs {
		for ri := range m.Releases {
			for ci := range m.Releases[ri].Configs {
				cols = append(cols, pair{m, &m.Releases[ri].Configs[ci]})
			}
		}
	}
	tags := make([]map[string]bool, len(cols))
	for i, p := range cols {
		tags[i] = c.Tags(p.m, p.cfg)
	}
	var groups []matrixGroup
	for _, cat := range c.Categories {
		g := matrixGroup{Category: cat}
		for _, cap := range c.Capabilities {
			if cap.Category() != cat.ID {
				continue
			}
			row := matrixRow{Cap: cap, Cells: make([]bool, len(cols))}
			for i := range cols {
				if cap.When.Matches(tags[i]) {
					row.Cells[i] = true
					row.Count++
				}
			}
			g.Rows = append(g.Rows, row)
		}
		groups = append(groups, g)
	}
	return groups
}

// shortID turns "macmini3-1-late-2009-server" into "3,1 late-2009 server" for matrix headers.
func shortID(ident, id string) string {
	num := ident[strings.IndexAny(ident, "0123456789"):]
	rest := strings.TrimPrefix(id, catalog.IDSlug(ident)+"-")
	if i := strings.LastIndex(rest, "-"); i > 0 {
		return num + " " + rest[:i] + " " + rest[i+1:]
	}
	return num + " " + rest
}

func procs(ps []catalog.Processor) string {
	var out []string
	for _, p := range ps {
		out = append(out, fmt.Sprintf("%s %s GHz (%d-core)", p.Model, strconv.FormatFloat(p.GHz, 'f', -1, 64), p.Cores))
	}
	return strings.Join(out, " · ")
}

func gbList(gbs []float64) string {
	var out []string
	for _, g := range gbs {
		if g < 1 {
			out = append(out, fmt.Sprintf("%d MB", int(g*1024)))
		} else {
			out = append(out, strconv.FormatFloat(g, 'f', -1, 64)+" GB")
		}
	}
	return strings.Join(out, " or ")
}

func host(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.Index(u, "/"); i > 0 {
		return u[:i]
	}
	return u
}

// identLess orders identifiers naturally: Macmini2,1 < Macmini10,1.
func identLess(a, b string) bool {
	pa, pb := splitIdent(a), splitIdent(b)
	if pa.prefix != pb.prefix {
		return pa.prefix < pb.prefix
	}
	if pa.major != pb.major {
		return pa.major < pb.major
	}
	return pa.minor < pb.minor
}

type identParts struct {
	prefix       string
	major, minor int
}

func splitIdent(s string) identParts {
	i := strings.IndexAny(s, "0123456789")
	if i < 0 {
		return identParts{prefix: s}
	}
	maj, min, _ := strings.Cut(s[i:], ",")
	a, _ := strconv.Atoi(maj)
	b, _ := strconv.Atoi(min)
	return identParts{s[:i], a, b}
}
