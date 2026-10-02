package web

import (
	"context"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := catalog.HashFS(data.FS)
	if _, err := st.SyncCatalog(ctx, c, h); err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, c, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// eligible is N: configs whose Mac has no hard blocker and that are in coverage scope.
func eligible(t *testing.T) int {
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range c.Macs {
		for i := range m.Releases {
			if m.HardBlocker == "" && c.CoverageExclusion(m, &m.Releases[i]) == "" {
				n += len(m.Releases[i].Configs)
			}
		}
	}
	return n
}

func TestRoutes(t *testing.T) {
	ts := newTestServer(t)
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	tests := []struct {
		method, path string
		code         int
		location     string
		body         string
		ctype        string
	}{
		{"GET", "/healthz", 200, "", "ok test", "text/plain"},
		{"GET", "/", 200, "", "<span class=\"n\">0</span> / " + strconv.Itoa(eligible(t)), "text/html"},
		{"GET", "/mac/MacBookPro5-1", 200, "", "MacBook Pro (15-inch, Late 2008)", "text/html"},
		{"GET", "/mac/MacBookPro5-1", 200, "", "#v-untested\"></use></svg>Untested</span>", "text/html"},
		{"GET", "/mac/MacBookPro1-1", 200, "", "<strong>Not compatible:</strong>", "text/html"},
		{"GET", "/mac/MacBookPro5-1", 200, "", "Out of coverage scope · Released before 2009", "text/html"},
		{"GET", "/mac/Xserve3-1", 200, "", "Out of coverage scope · Xserve (rack server)", "text/html"},
		{"GET", "/", 200, "", "Released before 2009", "text/html"},
		{"GET", "/mac/MacBookPro5,1", 301, "/mac/MacBookPro5-1", "", ""},
		{"GET", "/mac/MacBookPro5%2C1", 301, "/mac/MacBookPro5-1", "", ""},
		{"GET", "/mac/macbookpro5-1?view=matrix", 301, "/mac/MacBookPro5-1?view=matrix", "", ""},
		{"GET", "/mac/Nope9,9", 404, "", "404", "text/html"},
		{"GET", "/nothing/here", 404, "", "404", "text/html"},
		{"GET", "/api/v1/macs", 200, "", `"identifier": "MacBookPro5,1"`, "application/json"},
		{"GET", "/api/v1/nothing", 404, "", `"error":"not found"`, "application/json"},
		{"POST", "/api/v1/reports", 401, "", `"error"`, "application/json"},
		{"POST", "/mac/MacBookPro5-1", 404, "", "", ""},
		{"GET", "/static/site.css", 200, "", "--accent", "text/css"},
		{"HEAD", "/", 200, "", "", "text/html"},
		{"GET", "/criteria", 200, "", `id="line-macbook-pro"`, "text/html"},
		{"GET", "/releases", 200, "", "MacBook Pro (16-inch, 2019)", "text/html"},
		{"GET", "/configs", 200, "", "macbookpro8-2-15-late-2011-b", "text/html"},
		{"GET", "/configs?scope=in", 200, "", "Configurations counted in coverage", "text/html"},
		{"GET", "/configs?status=not-compatible", 200, "", "macbookpro1-1-15-early-2006-a", "text/html"},
		{"GET", "/configs?excluded=Xserve+%28rack+server%29", 200, "", "xserve3-1", "text/html"},
		{"GET", "/configs?q=gpu%3A6770m", 200, "", "imac12-2", "text/html"},
		{"GET", "/components", 200, "", "pci:10de:0647", "text/html"},
		{"GET", "/attribution", 200, "", "Omacom Foundation", "text/html"},
		{"GET", "/changelog", 200, "", "MC118", "text/html"},
		{"GET", "/", 200, "", `href="/changelog" title="Catalog changelog"`, "text/html"},
		{"GET", "/", 200, "", "https://github.com/doesitomarchy/doesitomarchy/tree/main/data", "text/html"},
		{"GET", "/methodology", 200, "", `id="criteria"`, "text/html"},
		{"GET", "/mac/MacBookPro8-2", 200, "", "HD 6490M · Early 2011", "text/html"},
		{"GET", "/mac/MacBookPro8-2?view=matrix", 200, "", `class="count-head"`, "text/html"},
		{"GET", "/", 200, "", `Not officially affiliated with <a href="https://omarchy.org" rel="noopener">Omarchy</a> or the <a href="https://omarchy.org/foundation/" rel="noopener">Omacom Foundation</a>.`, "text/html"},
		{"GET", "/", 200, "", `#WeCanFixEverything!</span> <a class="help" href="/contribute">You can help!</a>`, "text/html"},
		{"GET", "/sitemap.xml", 200, "", "/mac/MacBookPro16-4", "application/xml"},
		{"GET", "/robots.txt", 200, "", "Sitemap:", "text/plain"},
		{"GET", "/search?q=mbp+2011", 200, "", `href="/mac/MacBookPro8-2"`, "text/html"},
		{"GET", "/search?q=mbp+2011", 200, "", "<html", "text/html"},
		{"GET", "/search?q=gpu%3A6770m", 200, "", `href="/mac/MacBookPro8-2#cfg-macbookpro8-2-15-late-2011-b"`, "text/html"},
		{"GET", "/search?q=macbookk+pro+20099", 200, "", "Did you mean", "text/html"},
		{"GET", "/search?q=chip%3At3", 200, "", "chip: expected none, t1 or t2", "text/html"},
		{"GET", "/search?q=%3Cscript%3E", 200, "", "&lt;script&gt;", "text/html"},
		{"GET", "/search", 200, "", `role="search"`, "text/html"},
		{"GET", "/search/suggest?q=gp", 200, "", `href="/search?q=gpu%3A"`, "text/html"},
		{"GET", "/search/suggest?q=gp+year%3A2012&cursor=2", 200, "", `data-query="gpu: year:2012"`, "text/html"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, ts.URL+tt.path, nil)
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != tt.code {
				t.Fatalf("status %d, want %d", res.StatusCode, tt.code)
			}
			if loc := res.Header.Get("Location"); loc != tt.location {
				t.Errorf("Location %q, want %q", loc, tt.location)
			}
			if !strings.Contains(string(body), tt.body) {
				t.Errorf("body missing %q", tt.body)
			}
			if !strings.HasPrefix(res.Header.Get("Content-Type"), tt.ctype) {
				t.Errorf("Content-Type %q, want %q…", res.Header.Get("Content-Type"), tt.ctype)
			}
			if tt.method == "HEAD" && len(body) != 0 {
				t.Error("HEAD response has a body")
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{"/", "/mac/MacBookPro5-1", "/nothing", "/api/v1/x", "/static/site.css"} {
		res, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		csp := res.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("%s: CSP %q", path, csp)
		}
		for h, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Strict-Transport-Security": "max-age=86400"} {
			if got := res.Header.Get(h); got != want {
				t.Errorf("%s: %s = %q", path, h, got)
			}
		}
	}
}

// HTMX requests get the results partial only.
func TestSearchPartial(t *testing.T) {
	ts := newTestServer(t)
	req, _ := http.NewRequest("GET", ts.URL+"/search?q=xserve", nil)
	req.Header.Set("HX-Request", "true")
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	s := string(body)
	if res.StatusCode != 200 || strings.Contains(s, "<html") || !strings.Contains(s, `id="results"`) || strings.Count(s, `href="/mac/Xserve`) != 3 {
		t.Fatalf("partial wrong (%d):\n%s", res.StatusCode, s)
	}
	if v := res.Header.Get("Vary"); v != "HX-Request" {
		t.Errorf("Vary = %q", v)
	}
	// Cloudflare ignores Vary: a cached fragment would be served as an unstyled page.
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("fragment Cache-Control = %q, want no-store", cc)
	}
	if full, err := ts.Client().Get(ts.URL + "/search?q=xserve"); err != nil || !strings.Contains(full.Header.Get("Cache-Control"), "s-maxage") {
		t.Errorf("full page should stay cacheable: %v %q", err, full.Header.Get("Cache-Control"))
	}
	long := strings.Repeat("a", 5000)
	if res, err := ts.Client().Get(ts.URL + "/search?q=" + long); err != nil || res.StatusCode != 200 {
		t.Fatalf("long query: %v %v", err, res.StatusCode)
	}
}

// Every Mac in the catalog has a working page.
func TestEveryMacPageRenders(t *testing.T) {
	ts := newTestServer(t)
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range c.Macs {
		res, err := ts.Client().Get(ts.URL + "/mac/" + catalog.FileSlug(m.Identifier))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !strings.Contains(string(body), m.Identifier+"</h1>") {
			t.Errorf("%s: status %d", m.Identifier, res.StatusCode)
		}
		icon := "#m-" + m.Line + `"`
		if m.Identifier == "MacPro6,1" {
			icon = "#m-mac-pro-2013" + `"`
		}
		if h1 := string(body)[strings.Index(string(body), "<h1>"):]; !strings.Contains(h1[:strings.Index(h1, "</h1>")], icon) {
			t.Errorf("%s: heading should use icon %s", m.Identifier, icon)
		}
	}
}

// Every row on the stats page links to a configuration list with exactly
// the number of rows the stats page shows.
func TestStatsLinksMatchCounts(t *testing.T) {
	ts := newTestServer(t)
	get := func(path string) string {
		t.Helper()
		res, err := ts.Client().Get(ts.URL + path)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, err, res.StatusCode)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return string(b)
	}
	stats := get("/stats")
	rows := regexp.MustCompile(`<a class="hbar" href="([^"]+)"><span>([^<]*)</span>.*?<span class="n">(\d+)</span></a>`).FindAllStringSubmatch(stats, -1)
	if len(rows) < 20 {
		t.Fatalf("only %d linked stats rows", len(rows))
	}
	count := regexp.MustCompile(`<p class="muted">(\d+) configurations\.`)
	for _, r := range rows {
		href := html.UnescapeString(r[1])
		m := count.FindStringSubmatch(get(href))
		if m == nil || m[1] != r[3] {
			t.Errorf("%s (%s): stats shows %s, list shows %v", r[2], href, r[3], m)
		}
	}
}

func TestFooterReleaseLink(t *testing.T) {
	for v, want := range map[string]string{
		"v0.5.1": RepoURL + "/releases/tag/v0.5.1", "v1.0.0-rc.1": RepoURL + "/releases/tag/v1.0.0-rc.1",
		"dev": "", "2522443-dirty": "", "v0.5": "",
	} {
		if got := releaseURL(v); got != want {
			t.Errorf("releaseURL(%q) = %q, want %q", v, got, want)
		}
	}
}

// Every product line (and every per-identifier override) has an icon in the sprite.
func TestMachineIcons(t *testing.T) {
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	sprite, err := fs.ReadFile(staticFS, "static/icons.svg")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range c.Macs {
		if id := `id="m-` + machineIcon(m) + `"`; !strings.Contains(string(sprite), id) {
			t.Errorf("%s: icons.svg has no %s", m.Identifier, id)
		}
	}
}

// Each page names one canonical URL: no query (filters and views are the same
// page), the hyphenated slug for model pages, the query for search, none on errors.
func TestCanonical(t *testing.T) {
	ts := newTestServer(t)
	canon := regexp.MustCompile(`<link rel="canonical" href="([^"]*)">`)
	for path, want := range map[string]string{
		"/":                              BaseURL + "/",
		"/macs?q=chip%3At2&sort=year":    BaseURL + "/macs",
		"/configs?year=2018":             BaseURL + "/configs",
		"/mac/MacBookPro8-2?view=matrix": BaseURL + "/mac/MacBookPro8-2",
		"/mac/MacBookPro8,2":             BaseURL + "/mac/MacBookPro8-2",
		"/search?q=mbp+2011":             BaseURL + "/search?q=mbp+2011",
		"/search":                        BaseURL + "/search",
		"/methodology":                   BaseURL + "/methodology",
		"/nothing":                       "",
	} {
		res, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		got := ""
		if m := canon.FindStringSubmatch(string(b)); m != nil {
			got = html.UnescapeString(m[1])
		}
		if got != want {
			t.Errorf("%s: canonical %q, want %q", path, got, want)
		}
	}
}
