package web

import (
	"io"
	"strings"
	"testing"
)

// Lists use the plain "size, season, year" name; Mac pages keep Apple's,
// which About This Mac shows.
func TestListNames(t *testing.T) {
	ts := newTestServer(t)
	get := func(path string) string {
		t.Helper()
		res, err := ts.Client().Get(ts.URL + path)
		if err != nil || res.StatusCode != 200 {
			t.Fatalf("%s: %v %v", path, err, res)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	for _, path := range []string{"/macs?q=mbp+2016", "/search?q=mbp+2016"} {
		page := get(path)
		if !strings.Contains(page, "MacBook Pro (13-inch, 2016)<") || strings.Contains(page, "Two Thunderbolt 3 ports") {
			t.Errorf("%s: want the short name only", path)
		}
	}
	if page := get("/mac/MacBookPro13-1"); !strings.Contains(page, "<b>MacBook Pro (13-inch, 2016, Two Thunderbolt 3 ports)</b>") {
		t.Error("/mac/MacBookPro13-1: Apple's name is gone")
	}
	if page := get("/mac/MacBookPro16-4"); strings.Contains(page, "5600M)<") || !strings.Contains(page, "<b>MacBook Pro (16-inch, 2019)</b>") {
		t.Error("/mac/MacBookPro16-4: want Apple's name, without our GPU suffix")
	}
}
