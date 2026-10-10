package web

import (
	"io"
	"net/http"
	"regexp"
	"testing"
)

// pageBudget is PLAN §9.1's transfer budget per page, excluding fonts
// (which are cached across pages).
const pageBudget = 60 * 1024

// pageBudgets are exceptions the owner approved. /criteria deliberately puts
// every criterion and configuration on one page (PLAN §30); /api is the
// whole API reference, with samples in three languages (owner, 2026-10-05),
// and grew with live boots and the catalog snapshot (S1 step 3, 2026-10-09).
var pageBudgets = map[string]int{"/criteria": 64 * 1024, "/api": 67 * 1024}

var assetRef = regexp.MustCompile(`(?:href|src)="(/static/[^"#]+\.(?:css|js|svg))[^"]*"`)

// gzSize fetches path with gzip, as browsers do, and returns the bytes on the wire.
func gzSize(t *testing.T, base, path string) int {
	t.Helper()
	req, _ := http.NewRequest("GET", base+path, nil)
	req.Header.Set("Accept-Encoding", "gzip") // set explicitly, so the client doesn't decompress
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("%s: status %d", path, res.StatusCode)
	}
	return len(b)
}

func TestPageWeight(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{"/", "/search?q=mbp+2011", "/macs", "/mac/MacBookPro8-2", "/mac/MacBookPro8-2?view=matrix",
		"/mac/MacPro7-1", "/mac/MacPro5-1", "/mac/iMac18-3", "/criteria", "/stats", "/methodology", "/contribute", "/configs", "/components", "/releases", "/changelog", "/attribution", "/api"} {
		res, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		html, _ := io.ReadAll(res.Body)
		res.Body.Close()
		total := gzSize(t, ts.URL, path)
		seen := map[string]bool{}
		for _, m := range assetRef.FindAllStringSubmatch(string(html), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				total += gzSize(t, ts.URL, m[1])
			}
		}
		t.Logf("%-32s %6.1f KB (%d assets)", path, float64(total)/1024, len(seen))
		budget := pageBudget
		if b, ok := pageBudgets[path]; ok {
			budget = b
		}
		if total > budget {
			t.Errorf("%s: %d bytes, budget %d", path, total, budget)
		}
	}
}
