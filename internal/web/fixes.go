package web

import (
	"context"
	"net/http"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/builds"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
)

// The public /fixes page: the OmaBoot? fixes a report may list
// (data/fixes.yaml), each at /fixes#<id>, where results that ran with one
// link to it.

type fixesPageData struct {
	Fixes    []catalog.OmabootFix
	CapNames map[string]string
	Configs  map[string]*configView // the configurations fixes name
}

func (s *Server) fixesPage(w http.ResponseWriter, r *http.Request) {
	d := fixesPageData{Fixes: s.cat.OmabootFixes, CapNames: map[string]string{}, Configs: s.data().view.configs}
	for _, cp := range s.cat.Capabilities {
		d.CapNames[cp.ID] = cp.Name
	}
	s.render(w, r, http.StatusOK, "fixes", page{Title: "OmaBoot? fixes", Nav: "fixes", Data: d,
		Description: "The fixes OmaBoot? carries for Intel Macs, what each changes, where it comes from, and how to help fix things upstream. #WeCanFixEverything"})
}

// fixURL is a fix's entry on /fixes.
func fixURL(id string) string { return "/fixes#" + id }

// prepareResult finishes a new report before it's stored (PLAN §28.2): the
// Omarchy build it ran (or an unknown_build flag), and regression flags
// against the current results.
func (s *Server) prepareResult(ctx context.Context, res *results.Result) {
	if s.builds != nil {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		s.builds.Fill(ctx, res)
		cancel()
	}
	if ru := s.data().view.rollup; ru != nil {
		res.Flags = append(res.Flags, builds.Regressions(s.cat, ru, res)...)
	}
}

// ResolveBuilds looks up builds that weren't known when their report
// arrived, at start-up and then every interval; a no-op when lookups are off.
func (s *Server) ResolveBuilds(ctx context.Context, every time.Duration) {
	if s.builds == nil {
		return
	}
	s.builds.Loop(ctx, every)
}
