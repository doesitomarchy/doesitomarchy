// Package web is the HTTP server: routing, middleware, templates and static
// files. Every page is server-rendered and works without JavaScript; HTMX
// and site.js only enhance (PLAN.md §17).
package web

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/match"
	"github.com/doesitomarchy/doesitomarchy/internal/search"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// BaseURL is the public origin, for the sitemap and canonical links.
const BaseURL = "https://doesitomarchy.com"

// RepoURL is the public source repository.
const RepoURL = "https://github.com/doesitomarchy/doesitomarchy"

// Server serves the site.
type Server struct {
	store   *store.Store
	cat     *catalog.Catalog
	opt     Options
	snap    atomic.Pointer[snapshot]
	assets  *assets
	log     *slog.Logger
	version string
	pages   map[string]*template.Template
	purge   *purger
	match   *match.Matcher
	access  *accessVerifier
	// shareLimit is PF-3's per-IP limit, held in memory only.
	shareLimit *rateLimiter
}

// snapshot is everything built from the catalog and the accepted results.
// It is immutable; a rebuild makes a new one and swaps it in atomically, so
// a request always sees one consistent version.
type snapshot struct {
	view    *catalogView
	index   *search.Index
	version int64 // the store's data version it was built from
}

// data returns the current snapshot.
func (s *Server) data() *snapshot { return s.snap.Load() }

// New builds the page views and the search index from the catalog and the
// accepted results, and parses templates.
func New(st *store.Store, c *catalog.Catalog, log *slog.Logger, opt Options) (*Server, error) {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	a, err := newAssets(static)
	if err != nil {
		return nil, err
	}
	s := &Server{store: st, cat: c, opt: opt, assets: a, log: log, version: opt.Version, pages: map[string]*template.Template{},
		purge: newPurger(opt.PurgeZone, opt.PurgeToken, log), match: match.New(c), access: newAccessVerifier(opt.AccessTeam, opt.AccessAUD),
		shareLimit: newRateLimiter(SharesPerHourPerIP, time.Hour)}
	if _, err := s.Refresh(context.Background()); err != nil {
		return nil, fmt.Errorf("load results: %w", err)
	}
	funcs := template.FuncMap{
		"asset":       a.URL,
		"pct":         formatPct,
		"barw":        func(n, d int) string { return fmt.Sprintf("%.2f", pctOf(n, d)) },
		"query":       func(q string) string { return "/search?q=" + url.QueryEscape(q) },
		"plural":      func(n int, one, many string) string { return map[bool]string{true: one, false: many}[n == 1] },
		"join":        strings.Join,
		"lines":       nonEmptyLines,
		"osver":       osVersion,
		"dict":        dict,
		"joinlim":     joinLimit,
		"dots":        func(n int) string { return strings.Repeat("·", n) },
		"add":         func(a, b int) int { return a + b },
		"applies":     s.appliesText,
		"v":           func(s string) status.Verdict { return status.Verdict(s) },
		"releaseURL":  releaseURL,
		"itemVerdict": itemVerdict,
		"utc":         formatUTC,
		"itemLabel":   itemLabel,
		"reasonLabel": reasonLabel,
	}
	for _, p := range []string{"home", "mac", "report", "identify", "privacy", "api", "admin", "admin-report", "admin-sources", "admin-shares", "message", "macs", "search", "suggest", "stats", "methodology", "contribute", "notfound", "error",
		"criteria", "releases", "configs", "components", "attribution", "changelog"} {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/"+p+".html", "templates/partials.html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", p, err)
		}
		s.pages[p] = t
	}
	return s, nil
}

// Handler returns the routed handler wrapped in middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", s.assets)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.assets.URL("favicon.svg"), http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /robots.txt", s.robots)
	mux.HandleFunc("GET /sitemap.xml", s.sitemap)
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /macs", s.macs)
	mux.HandleFunc("GET /mac/{id}", s.mac)
	mux.HandleFunc("GET /report/{code}", s.report)
	mux.HandleFunc("GET /search", s.search)
	mux.HandleFunc("GET /search/suggest", s.suggest)
	mux.HandleFunc("GET /stats", s.stats)
	mux.HandleFunc("GET /methodology", s.methodology)
	mux.HandleFunc("GET /contribute", s.contribute)
	mux.HandleFunc("GET /criteria", s.criteria)
	mux.HandleFunc("GET /releases", s.releases)
	mux.HandleFunc("GET /configs", s.configList)
	mux.HandleFunc("GET /components", s.componentList)
	mux.HandleFunc("GET /attribution", s.attribution)
	mux.HandleFunc("GET /changelog", s.changelog)
	mux.HandleFunc("GET /identify", s.identify)
	mux.HandleFunc("POST /identify", s.identifyPost)
	mux.HandleFunc("POST /identify/share", s.identifyShare)
	mux.HandleFunc("GET /privacy", s.privacy)
	mux.HandleFunc("GET /api", s.apiDocs)
	s.apiRoutes(mux)
	s.adminRoutes(mux)
	mux.HandleFunc("/", s.notFound)
	return s.recoverer(logRequests(s.log, securityHeaders(compress(mux))))
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// The version lets a deploy confirm the new release is the one serving.
	w.Write([]byte("ok " + s.version + "\n"))
}

func apiNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"error":"not found","detail":"the API is not available yet"}` + "\n"))
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusNotFound, "notfound", page{Title: "Not found"})
}

// message renders a short notice with a way back, for refusals a visitor
// should understand (a full form, a rate limit).
func (s *Server) message(w http.ResponseWriter, r *http.Request, code int, title, text, back string) {
	s.render(w, r, code, "message", page{Title: title, Data: messageData{title, text, back}})
}

type messageData struct{ Title, Text, Back string }

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "path", r.URL.Path, "err", err)
	s.render(w, r, http.StatusInternalServerError, "error", page{Title: "Server error"})
}

// page is what every template receives.
type page struct {
	Title       string
	Description string
	Nav         string // current section: "macs", "stats", "methodology", "contribute"
	Site        *site
	Version     string
	Canonical   string // absolute URL for <link rel="canonical">; empty on error pages
	Data        any
}

// render executes into a buffer first so a template error never sends half a page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, code int, name string, p page) {
	p.Site, p.Version = &s.data().view.site, s.version
	// Canonical: the path without its query (filters and views are the same
	// page), unless the handler chose one. Error pages have none.
	if code != http.StatusOK {
		p.Canonical = ""
	} else if p.Canonical == "" {
		p.Canonical = BaseURL + r.URL.EscapedPath()
	}
	if p.Description == "" {
		p.Description = "Which Intel Macs (2006–2020) run Omarchy, per model identifier and hardware configuration."
	}
	var buf bytes.Buffer
	if err := s.pages[name].Execute(&buf, p); err != nil {
		s.log.Error("render", "page", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.writeHTML(w, r, code, buf.Bytes())
}

// renderPartial executes one named template from a page's set, without the layout.
func (s *Server) renderPartial(w http.ResponseWriter, r *http.Request, pageName, name string, data any) {
	var buf bytes.Buffer
	if err := s.pages[pageName].ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("render", "partial", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.writeHTML(w, r, http.StatusOK, buf.Bytes())
}

func (s *Server) writeHTML(w http.ResponseWriter, r *http.Request, code int, b []byte) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	if code == http.StatusOK {
		// Browsers revalidate; Cloudflare may cache for 5 minutes (purged on
		// deploy). A handler that set its own policy keeps it.
		if h.Get("Cache-Control") == "" {
			h.Set("Cache-Control", "public, max-age=0, s-maxage=300")
		}
	} else {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		w.Write(b)
	}
}

// releaseURL links a release version (v0.5.1) to its GitHub release; dev
// builds ("dev", "2522443-dirty") have none.
func releaseURL(version string) string {
	if !releaseTag.MatchString(version) {
		return ""
	}
	return RepoURL + "/releases/tag/" + version
}

var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)

func pctOf(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}

func joinLimit(xs []string, n int) string {
	if len(xs) <= n {
		return strings.Join(xs, ", ")
	}
	return strings.Join(xs[:n], ", ") + fmt.Sprintf(" +%d", len(xs)-n)
}

// Refresh rebuilds the views and search index from the store's accepted
// results if they changed since the current snapshot, and swaps them in.
// It reports whether anything changed.
func (s *Server) Refresh(ctx context.Context) (bool, error) {
	ru, err := s.store.RollupData(ctx)
	if err != nil {
		return false, err
	}
	if cur := s.snap.Load(); cur != nil && cur.version == ru.Version {
		return false, nil
	}
	start := time.Now()
	view := buildView(s.cat, s.opt, ru)
	s.snap.Store(&snapshot{view: view, index: search.Build(s.cat, view.states), version: ru.Version})
	s.log.Info("results loaded", "data_version", ru.Version, "verified", view.site.Coverage.Verified,
		"tested", view.site.Coverage.Tested, "ms", time.Since(start).Milliseconds())
	return true, nil
}

// Watch polls the store's data version and rebuilds when it moves (an
// accept, retract or Unsupported flag from the CLI or /admin), then asks
// Cloudflare to drop its cached pages. It returns when ctx is done.
func (s *Server) Watch(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		v, err := s.store.DataVersion(ctx)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("data version", "err", err)
			}
			continue
		}
		if cur := s.snap.Load(); cur != nil && cur.version == v {
			continue
		}
		changed, err := s.Refresh(ctx)
		if err != nil {
			s.log.Error("rebuild", "err", err)
			continue
		}
		if changed {
			s.purge.schedule()
		}
	}
}

// itemVerdict maps a stored item status to the verdict glyph it shows.
func itemVerdict(st string) status.Verdict {
	switch st {
	case "supported":
		return status.Supported
	case "partial":
		return status.Partial
	case "failed":
		return status.Failed
	default:
		return status.Untested
	}
}

// itemLabel is a stored item status as people read it.
func itemLabel(st string) string {
	if st == "not_tested" {
		return "Not tested"
	}
	return itemVerdict(st).Label()
}

// reasonLabel explains why a check was skipped.
func reasonLabel(r string) string {
	switch r {
	case "no-equipment":
		return "skipped: no equipment"
	case "not-in-profile":
		return "skipped: not in the test profile"
	case "uncertain":
		return "skipped: result uncertain"
	case "other":
		return "skipped"
	}
	return ""
}

// formatUTC shows an RFC 3339 timestamp as "2026-09-30 18:05 UTC". Every time
// on the site is UTC. Anything that isn't a timestamp is shown as is.
func formatUTC(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.UTC().Format("2006-01-02 15:04") + " UTC"
}

// nonEmptyLines splits text into its non-blank lines (research notes are one
// note per line).
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// osVersion names what a report ran: "Omarchy 4.0.4 · kernel 6.16.2-arch1-1".
// Omarchy ships its own kernel builds, so the kernel matters as much.
func osVersion(omarchy, kernel string) string {
	if kernel == "" {
		return "Omarchy " + omarchy
	}
	return "Omarchy " + omarchy + " · kernel " + kernel
}

// dict builds a map for passing several values to a template.
func dict(kv ...any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return m
}
