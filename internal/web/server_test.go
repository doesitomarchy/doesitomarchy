package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	srv, err := New(st, slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
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
		{"GET", "/healthz", 200, "", "ok", "text/plain"},
		{"GET", "/", 200, "", "0 / " + strconv.Itoa(eligible(t)) + " configs", "text/html"},
		{"GET", "/mac/MacBookPro5-1", 200, "", "MacBook Pro (15-inch, Late 2008)", "text/html"},
		{"GET", "/mac/MacBookPro5-1", 200, "", "</span> Untested", "text/html"},
		{"GET", "/mac/MacBookPro1-1", 200, "", "⛔ Not compatible", "text/html"},
		{"GET", "/mac/MacBookPro5-1", 200, "", "Not counted in coverage: Released before 2009", "text/html"},
		{"GET", "/mac/Xserve3-1", 200, "", "Not counted in coverage: Xserve (rack server)", "text/html"},
		{"GET", "/", 200, "", "Released before 2009", "text/html"},
		{"GET", "/mac/MacBookPro5,1", 301, "/mac/MacBookPro5-1", "", ""},
		{"GET", "/mac/MacBookPro5%2C1", 301, "/mac/MacBookPro5-1", "", ""},
		{"GET", "/mac/macbookpro5-1?view=matrix", 301, "/mac/MacBookPro5-1?view=matrix", "", ""},
		{"GET", "/mac/Nope9,9", 404, "", "404", "text/html"},
		{"GET", "/nothing/here", 404, "", "404", "text/html"},
		{"GET", "/api/v1/macs", 404, "", `"error":"not found"`, "application/json"},
		{"POST", "/api/v1/results", 404, "", `"error"`, "application/json"},
		{"POST", "/mac/MacBookPro5-1", 404, "", "", ""},
		{"GET", "/static/site.css", 200, "", "--accent", "text/css"},
		{"HEAD", "/", 200, "", "", "text/html"},
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
		for h, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY"} {
			if got := res.Header.Get(h); got != want {
				t.Errorf("%s: %s = %q", path, h, got)
			}
		}
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
		if res.StatusCode != 200 || !strings.Contains(string(body), "<h1>"+m.Identifier+"</h1>") {
			t.Errorf("%s: status %d", m.Identifier, res.StatusCode)
		}
	}
}
