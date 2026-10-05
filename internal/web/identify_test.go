package web

import (
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
)

// Pasted outputs from the /identify commands (PLAN §22.9 item 6).
const (
	pasteLinux = `MacBookPro8,2
Mac-94245A3940C91C80
pci 0x8086:0x0104
pci 0x8086:0x0126
pci 0x1002:0x6760
pci 0x14e4:0x4331
`
	pasteMacOS = `MacBookPro8,2
  | |   "board-id" = <"Mac-94245A3940C91C80">
Graphics/Displays:

    Intel HD Graphics 3000:

      Chipset Model: Intel HD Graphics 3000
      Type: GPU
      Vendor: Intel (0x8086)
      Device ID: 0x0126

    AMD Radeon HD 6490M:

      Chipset Model: AMD Radeon HD 6490M
      Vendor: AMD (0x1002)
      Device ID: 0x6760
`
)

func TestIdentify(t *testing.T) {
	ts := newTestServer(t)
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	get := func(path string) (int, string) {
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	// Without JavaScript: the paste is posted and turned into the result URL.
	for name, paste := range map[string]string{"linux": pasteLinux, "macos": pasteMacOS} {
		res, err := client.PostForm(ts.URL+"/identify", url.Values{"paste": {paste}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		loc := res.Header.Get("Location")
		if res.StatusCode != http.StatusSeeOther || !strings.Contains(loc, "product=MacBookPro8%2C2") || !strings.Contains(loc, "1002%3A6760") {
			t.Fatalf("%s: %d %s", name, res.StatusCode, loc)
		}
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: the POST reply may be cached", name)
		}
		code, body := get(loc)
		if code != 200 || !strings.Contains(body, "Your configuration:") || !strings.Contains(body, "#cfg-macbookpro8-2-15-early-2011-a") {
			t.Errorf("%s: no exact match on %s", name, loc)
		}
	}

	for _, tc := range []struct{ path, want string }{
		// Two configurations share the Radeon HD 6750M.
		{"/identify?product=MacBookPro8,2&pci=1002:6741", "These configurations fit equally"},
		{"/identify?board=Mac-94245A3940C91C80", "MacBookPro8,2"},
		{"/identify?pci=1002:6760", "Possible matches"},
		{"/identify?product=MacBookPro99,1", "Not an Intel Mac we know"},
		{"/identify?none=1", "Nothing we recognise"},
		{"/identify", `id="identify-form"`},
	} {
		if code, body := get(tc.path); code != 200 || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d, want %q", tc.path, code, tc.want)
		}
	}
	// An identifier sold under several names is titled by the release identified.
	for path, want := range map[string]string{
		"/identify?board=Mac-F2268CC8&pci=1002:9488":    "</a> · iMac (21.5-inch, Late 2009)</h2>",
		"/identify?product=iMac10,1&pci=1002:9488":      "</a> · iMac (27-inch, Late 2009)</h2>", // ties across both sizes
		"/identify?product=MacBookPro8,2&pci=1002:6741": "</a> · MacBook Pro (15-inch, Late 2011)</h2>",
	} {
		if _, body := get(path); !strings.Contains(body, want) {
			t.Errorf("%s: heading isn't %q", path, want)
		}
	}
	// Only the tied configurations, not those the pasted GPU rules out.
	if _, body := get("/identify?product=MacBookPro8,2&pci=1002:6741"); strings.Contains(body, "#cfg-macbookpro8-2-15-early-2011-a") {
		t.Errorf("a ruled-out configuration is offered")
	}

	// A paste with nothing in it.
	res, _ := client.PostForm(ts.URL+"/identify", url.Values{"paste": {"hello"}})
	res.Body.Close()
	if loc := res.Header.Get("Location"); loc != "/identify?none=1" {
		t.Errorf("empty paste: %s", loc)
	}
}

func TestInfoPages(t *testing.T) {
	ts := newTestServer(t)
	for path, wants := range map[string][]string{
		"/privacy":     {"TLDR;", "30 days", "one-way hash"},
		"/api":         {"Becoming a source", "/api/v1/reports", template.HTMLEscapeString(ConsentNotice)},
		"/robots.txt":  {"Disallow: /admin"},
		"/sitemap.xml": {"/identify</loc>", "/api</loc>", "/privacy</loc>"},
		"/":            {`href="/identify"`, `href="/privacy"`},
		"/contribute":  {"For test-tool authors"},
		"/identify":    {"| tee /dev/tty | wl-copy", "| tee /dev/tty | pbcopy", `data-plain="cat /sys/class/dmi/id/product_name`, `role="switch"`},
		"/mac/MacBookPro8-2": {`<span class="muted tested-count">0 of 4 configurations tested</span>`, `href="/identify">Is this your Mac?`,
			`<ol class="notes"><li>Quad-core Sandy Bridge`, `data-caps-toggle`},
	} {
		res, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		for _, w := range wants {
			if res.StatusCode != 200 || !strings.Contains(string(b), w) {
				t.Errorf("%s: %d, missing %q", path, res.StatusCode, w)
			}
		}
	}
}

// The example on /api must stay a valid report.
func TestAPIExample(t *testing.T) {
	f, err := results.Parse([]byte(apiExample))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := catalog.LoadFS(data.FS)
	r, err := results.Validate(f, c, time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if r.ConfigID != "macbookpro8-2-15-early-2011-a" || len(r.Flags) != 0 {
		t.Errorf("example resolves to %s with flags %v", r.ConfigID, r.Flags)
	}
}
