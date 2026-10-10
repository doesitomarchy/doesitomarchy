package web

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// /admin/unsupported (PLAN.md §20.1): the maintainer's white flag. Failing
// criteria, each with "Mark Unsupported", and the flags in force, each with
// "Lift". A flag covers a criterion on one component (every configuration
// with it, as the criterion's fix_by names) or on one configuration.

// failure is a criterion failing on one or more configurations that one
// white flag would cover.
type failure struct {
	Capability, Criterion string
	Component, Config     string // the flag's scope
	Scope                 string // its name
	Configs               []*configView
	Unsupported           bool
}

type adminUnsupportedData struct {
	Who         string
	CSRF        string
	Failures    []failure
	Unsupported []unsupportedRow
	Done, Err   string
}

type unsupportedRow struct {
	U         store.Unsupported
	Criterion string
	Scope     string
}

func (s *Server) adminUnsupportedRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/unsupported", s.admin(s.adminUnsupported))
	mux.HandleFunc("POST /admin/unsupported", s.admin(s.adminUnsupportedSet))
	mux.HandleFunc("POST /admin/unsupported/{id}/clear", s.admin(s.adminUnsupportedClear))
}

// failures lists the criteria that fail, only partly work or were given up
// on, grouped by what one white flag would cover.
func (s *Server) failures() []failure {
	v := s.data().view
	if v.rollup == nil {
		return nil
	}
	caps := map[string]catalog.Capability{}
	for _, cp := range s.cat.Capabilities {
		caps[cp.ID] = cp
	}
	byKey := map[string]*failure{}
	var keys []string
	for _, mv := range v.macs {
		for _, cv := range mv.Configs {
			cfg := s.cfgs[cv.ID]
			if cfg == nil {
				continue
			}
			for capID, cs := range cv.Status.Caps {
				if cs.Verdict != "failed" && cs.Verdict != "partial" && cs.Verdict != "unsupported" {
					continue
				}
				cp := caps[capID]
				comp := s.cat.FixComponent(cp, cfg)
				key := capID + "|" + comp
				if comp == "" {
					key = capID + "|config:" + cv.ID
				}
				fl := byKey[key]
				if fl == nil {
					fl = &failure{Capability: capID, Criterion: cp.Name, Component: comp}
					if comp == "" {
						fl.Config = cv.ID
					}
					fl.Scope = scopeName(s.cat, fl.Component, fl.Config)
					byKey[key] = fl
					keys = append(keys, key)
				}
				fl.Configs = append(fl.Configs, cv)
				fl.Unsupported = fl.Unsupported || cs.Verdict == "unsupported"
			}
		}
	}
	out := make([]failure, 0, len(keys))
	for _, k := range keys {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Configs) != len(out[j].Configs) {
			return len(out[i].Configs) > len(out[j].Configs)
		}
		return out[i].Criterion < out[j].Criterion
	})
	return out
}

// scopeName names a white flag's scope: the component, or the configuration.
func scopeName(c *catalog.Catalog, component, config string) string {
	if component != "" {
		if comp := c.Components[component]; comp != nil {
			return comp.Name
		}
		return component
	}
	for _, m := range c.Macs {
		for _, r := range m.Releases {
			for _, cfg := range r.Configs {
				if cfg.ID == config {
					return r.Name + " · " + cfg.Label
				}
			}
		}
	}
	return config
}

func (s *Server) adminUnsupported(w http.ResponseWriter, r *http.Request, who string) {
	ctx := r.Context()
	d := adminUnsupportedData{Who: who, CSRF: s.csrf(ctx, who), Failures: s.failures(),
		Done: r.URL.Query().Get("done"), Err: r.URL.Query().Get("err")}
	us, err := s.store.UnsupportedList(ctx, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names := map[string]string{}
	for _, cp := range s.cat.Capabilities {
		names[cp.ID] = cp.Name
	}
	for _, u := range us {
		d.Unsupported = append(d.Unsupported, unsupportedRow{u, names[u.Capability], scopeName(s.cat, u.Component, u.Config)})
	}
	s.render(w, r, http.StatusOK, "admin-unsupported", page{Title: "Unsupported", Data: d})
}

func (s *Server) unsupportedBack(w http.ResponseWriter, r *http.Request, done string, err error) {
	q := url.Values{}
	if err != nil {
		q.Set("err", err.Error())
	} else {
		q.Set("done", done)
	}
	http.Redirect(w, r, "/admin/unsupported?"+q.Encode(), http.StatusSeeOther)
}

func (s *Server) adminUnsupportedSet(w http.ResponseWriter, r *http.Request, who string) {
	ctx := r.Context()
	capability, component, config := r.FormValue("capability"), r.FormValue("component"), r.FormValue("config")
	reason := strings.TrimSpace(r.FormValue("reason"))
	err := s.store.SetUnsupported(ctx, capability, config, component, reason, who)
	s.refreshNow(ctx)
	s.unsupportedBack(w, r, "marked Unsupported", err)
}

func (s *Server) adminUnsupportedClear(w http.ResponseWriter, r *http.Request, who string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	err = s.store.ClearUnsupported(r.Context(), id, who)
	s.refreshNow(r.Context())
	s.unsupportedBack(w, r, "Unsupported lifted", err)
}

// refreshNow rebuilds the site before an /admin action redirects, rather
// than at the watcher's next tick, so the page it lands on is current.
func (s *Server) refreshNow(ctx context.Context) {
	changed, err := s.Refresh(ctx)
	if err != nil {
		s.log.Warn("rebuild", "err", err)
	}
	if changed {
		s.purge.schedule()
	}
}
