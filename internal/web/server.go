// Package web is the HTTP server: routing, middleware, templates and static files.
//
// Phase 2 is a skeleton: health check, a placeholder home page, a minimal
// model page read from the database, 404s, and the reserved /api/v1/ prefix.
// The full UI arrives in Phase 4.
package web

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/doesitomarchy/doesitomarchy/internal/search"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Server serves the site from a synced store.
type Server struct {
	store   *store.Store
	index   *search.Index
	log     *slog.Logger
	version string
	pages   map[string]*template.Template
}

// New parses templates and returns a server. version is shown in the footer.
func New(st *store.Store, ix *search.Index, log *slog.Logger, version string) (*Server, error) {
	s := &Server{store: st, index: ix, log: log, version: version, pages: map[string]*template.Template{}}
	funcs := template.FuncMap{"pct": func(f float64) string { return formatPct(f) }}
	for _, p := range []string{"home", "mac", "notfound", "error", "search", "suggest"} {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/"+p+".html", "templates/partials.html")
		if err != nil {
			return nil, err
		}
		s.pages[p] = t
	}
	return s, nil
}

// Handler returns the routed handler wrapped in middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /mac/{id}", s.mac)
	mux.HandleFunc("GET /search", s.search)
	mux.HandleFunc("GET /search/suggest", s.suggest)
	mux.HandleFunc("/api/v1/", apiNotFound) // reserved until the API ships (Phase 7)
	mux.HandleFunc("/", s.notFound)
	return s.recoverer(logRequests(s.log, securityHeaders(mux)))
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte("ok\n"))
}

type homeData struct {
	Counts     store.Counts
	Coverage   status.Coverage
	Exclusions []store.Exclusion
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	counts, err := s.store.CatalogCounts(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	sums, err := s.store.ConfigSummaries(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	all := make([]status.ConfigStatus, len(sums))
	for i, cs := range sums {
		all[i] = status.Config(status.ConfigInput{HardBlocker: cs.HardBlocker, Excluded: cs.Excluded, Applicable: cs.Applicable})
	}
	excl, err := s.store.CoverageExclusions(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "home", "Does it Omarchy?", homeData{counts, status.Summarize(all), excl})
}

type macData struct {
	Mac      *store.Mac
	Statuses map[string]status.ConfigStatus
}

func (s *Server) mac(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.store.MacBySlug(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
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
	d := macData{Mac: m, Statuses: map[string]status.ConfigStatus{}}
	for _, rel := range m.Releases {
		for _, c := range rel.Configs {
			d.Statuses[c.ID] = status.Config(status.ConfigInput{HardBlocker: c.Summary.HardBlocker, Excluded: c.Summary.Excluded, Applicable: c.Summary.Applicable})
		}
	}
	s.render(w, r, http.StatusOK, "mac", m.Identifier, d)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusNotFound, "notfound", "Not found", nil)
}

func apiNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"error":"not found","detail":"the API is not available yet"}` + "\n"))
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "path", r.URL.Path, "err", err)
	s.render(w, r, http.StatusInternalServerError, "error", "Server error", nil)
}

type page struct {
	Title   string
	Version string
	Data    any
}

// render executes into a buffer first so a template error never sends half a page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, code int, name, title string, data any) {
	var buf bytes.Buffer
	if err := s.pages[name].Execute(&buf, page{title, s.version, data}); err != nil {
		s.log.Error("render", "page", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		buf.WriteTo(w)
	}
}
