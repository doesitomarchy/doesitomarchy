package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/fixes"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// /admin/fixes (PLAN.md §26): failures without a fix, with "Open a fix
// issue"; the fixes the site knows about; and the Unsupported white flags.

// failure is a criterion failing on one or more configurations that a single
// fix would cover.
type failure struct {
	Capability, Criterion string
	Component, Config     string // the fix's scope
	Scope                 string // its name
	Configs               []*configView
	Unsupported           bool
	Closed                *store.Fix // a fix closed without one (not planned)
}

type adminFixesData struct {
	Who         string
	CSRF        string
	Failures    []failure
	Fixes       []fixRow
	Unsupported []unsupportedRow
	CanOpen     bool // a GitHub token is set
	Repo        string
	Done, Err   string
}

type unsupportedRow struct {
	U         store.Unsupported
	Criterion string
	Scope     string
}

func (s *Server) adminFixRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/fixes", s.admin(s.adminFixes))
	mux.HandleFunc("POST /admin/fixes/open", s.admin(s.adminFixOpen))
	mux.HandleFunc("POST /admin/fixes/sync", s.admin(s.adminFixSync))
	mux.HandleFunc("POST /admin/unsupported", s.admin(s.adminUnsupportedSet))
	mux.HandleFunc("POST /admin/unsupported/{id}/clear", s.admin(s.adminUnsupportedClear))
}

// catalogConfigs maps config IDs to their catalog entries.
func (s *Server) catalogConfig(id string) (*catalog.Mac, *catalog.Config) {
	for _, m := range s.cat.Macs {
		for ri := range m.Releases {
			for ci := range m.Releases[ri].Configs {
				if cfg := &m.Releases[ri].Configs[ci]; cfg.ID == id {
					return m, cfg
				}
			}
		}
	}
	return nil, nil
}

// failures lists failing criteria with no fix in progress or landed,
// grouped by what one fix would cover.
func (s *Server) failures() []failure {
	v := s.data().view
	if v.rollup == nil {
		return nil
	}
	caps := map[string]catalog.Capability{}
	for _, cp := range s.cat.Capabilities {
		caps[cp.ID] = cp
	}
	now := time.Now()
	byKey := map[string]*failure{}
	var keys []string
	for _, mv := range v.macs {
		for _, cv := range mv.Configs {
			_, cfg := s.catalogConfig(cv.ID)
			if cfg == nil {
				continue
			}
			for capID, cs := range cv.Status.Caps {
				if cs.Verdict != "failed" && cs.Verdict != "partial" && cs.Verdict != "unsupported" {
					continue
				}
				if _, st, ok := fixes.Best(v.rollup.Fixes, capID, cv.ID, cfg.Components, now); ok && st != fixes.NotPlanned {
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
					fl.Scope = s.fixScopeName(fl.Component, fl.Config)
					for i := range v.rollup.Fixes {
						if f := v.rollup.Fixes[i]; f.Capability == capID && f.Component == comp && f.Config == fl.Config && fixes.StateOf(f, now) == fixes.NotPlanned {
							fl.Closed = &f
							break
						}
					}
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

func (s *Server) adminFixes(w http.ResponseWriter, r *http.Request, who string) {
	ctx := r.Context()
	d := adminFixesData{Who: who, CSRF: s.csrf(ctx, who), Failures: s.failures(), Fixes: s.fixRows(), CanOpen: s.gh != nil,
		Repo: s.fixRepo(), Done: r.URL.Query().Get("done"), Err: r.URL.Query().Get("err")}
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
		d.Unsupported = append(d.Unsupported, unsupportedRow{u, names[u.Capability], s.fixScopeName(u.Component, u.Config)})
	}
	s.render(w, r, http.StatusOK, "admin-fixes", page{Title: "Fixes", Data: d})
}

func (s *Server) fixRepo() string {
	if s.opt.FixRepo != "" {
		return s.opt.FixRepo
	}
	return fixes.DefaultRepo
}

func (s *Server) fixesBack(w http.ResponseWriter, r *http.Request, done string, err error) {
	q := url.Values{}
	if err != nil {
		q.Set("err", err.Error())
	} else {
		q.Set("done", done)
	}
	http.Redirect(w, r, "/admin/fixes?"+q.Encode(), http.StatusSeeOther)
}

// affected lists the configurations a fix would cover, from the current
// snapshot: those failing first, with their latest report.
func (s *Server) affected(capability, component, config string) []fixes.Affected {
	v := s.data().view
	var out []fixes.Affected
	for _, mv := range v.macs {
		for _, cv := range mv.Configs {
			_, cfg := s.catalogConfig(cv.ID)
			if cfg == nil || !fixes.Covers(store.Fix{Capability: capability, Component: component, Config: config}, capability, cv.ID, cfg.Components) {
				continue
			}
			cs, applies := cv.Status.Caps[capability]
			if !applies {
				continue
			}
			a := fixes.Affected{ConfigID: cv.ID, Name: mv.Identifier + " · " + cv.ReleaseName + " · " + cv.Label,
				URL:    BaseURL + "/mac/" + url.PathEscape(mv.Slug) + "#cfg-" + cv.ID,
				Failed: cs.Verdict == "failed" || cs.Verdict == "partial"}
			if a.Failed && cs.Latest != nil {
				a.Evidence = cs.Latest.Evidence
				for _, rs := range cv.Results {
					if rs.ID == cs.Latest.ResultID {
						a.Report = BaseURL + "/report/" + rs.Code
					}
				}
			}
			out = append(out, a)
		}
	}
	return out
}

func (s *Server) adminFixOpen(w http.ResponseWriter, r *http.Request, who string) {
	if s.gh == nil {
		s.fixesBack(w, r, "", errors.New("GITHUB_TOKEN isn't set on the server, so issues can't be opened from here"))
		return
	}
	capability, component, config := r.FormValue("capability"), r.FormValue("component"), r.FormValue("config")
	name := ""
	for _, cp := range s.cat.Capabilities {
		if cp.ID == capability {
			name = cp.Name
		}
	}
	if name == "" || (component == "") == (config == "") {
		s.fixesBack(w, r, "", errors.New("pick a criterion and either a component or a configuration"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	f, err := fixes.OpenIssue(ctx, s.gh, s.store, capability, name, fixes.Scope{Component: component, Config: config, Name: s.fixScopeName(component, config)},
		s.affected(capability, component, config), who)
	if err != nil {
		s.fixesBack(w, r, "", err)
		return
	}
	s.refreshSoon()
	s.fixesBack(w, r, "opened issue #"+strconv.Itoa(f.Issue), nil)
}

func (s *Server) adminFixSync(w http.ResponseWriter, r *http.Request, who string) {
	if s.syncer == nil {
		s.fixesBack(w, r, "", errors.New("GITHUB_TOKEN isn't set on the server"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	n, err := s.syncer.SyncAll(ctx)
	s.refreshSoon()
	s.fixesBack(w, r, strconv.Itoa(n)+" fixes updated from GitHub", err)
}

func (s *Server) adminUnsupportedSet(w http.ResponseWriter, r *http.Request, who string) {
	ctx := r.Context()
	capability, component, config := r.FormValue("capability"), r.FormValue("component"), r.FormValue("config")
	reason := strings.TrimSpace(r.FormValue("reason"))
	if err := s.store.SetUnsupported(ctx, capability, config, component, reason, who); err != nil {
		s.fixesBack(w, r, "", err)
		return
	}
	// Close any fix issue for it, with the reason (PLAN §26).
	msg := "marked Unsupported"
	if s.gh != nil {
		all, err := s.store.Fixes(ctx) // not the snapshot: an issue opened a moment ago counts
		if err != nil {
			s.fixesBack(w, r, "", err)
			return
		}
		for _, f := range all {
			if f.Capability == capability && f.Component == component && f.Config == config && f.Open {
				if err := fixes.GiveUp(ctx, s.gh, f.Issue, reason, who); err != nil {
					s.fixesBack(w, r, "", errors.New("marked Unsupported, but closing issue #"+strconv.Itoa(f.Issue)+" failed: "+err.Error()))
					return
				}
				if s.syncer != nil {
					s.syncer.SyncIssue(ctx, f.Issue)
				}
				msg += "; issue #" + strconv.Itoa(f.Issue) + " closed as not planned"
			}
		}
	}
	s.refreshSoon()
	s.fixesBack(w, r, msg, nil)
}

func (s *Server) adminUnsupportedClear(w http.ResponseWriter, r *http.Request, who string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	err = s.store.ClearUnsupported(r.Context(), id, who)
	s.refreshSoon()
	s.fixesBack(w, r, "Unsupported lifted", err)
}

// refreshSoon rebuilds the site now rather than at the watcher's next tick.
func (s *Server) refreshSoon() {
	go func() {
		if changed, err := s.Refresh(context.Background()); err == nil && changed {
			s.purge.schedule()
		}
	}()
}
