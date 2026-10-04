package web

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// The OpenGL ES floor (PLAN §30): affected configuration cards carry a
// known-limitation note with the warning icon; the API and /components show
// each GPU's level; unaffected cards show nothing.
func TestGLESFloor(t *testing.T) {
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

	page := get("/mac/MacBook3-1")
	for _, want := range []string{`<p class="limit-line"><span class="warn-i"><svg class="i" aria-hidden="true"><use href="`, `#i-alert"></use></svg></span> Known limitation: The Intel GMA X3100 reaches OpenGL ES 2.0 only.`,
		`Intel GMA X3100 <span class="alt">· OpenGL ES 2.0</span>`} {
		if !strings.Contains(page, want) {
			t.Errorf("/mac/MacBook3-1: missing %q", want)
		}
	}
	if page := get("/mac/MacBookPro6-2"); !strings.Contains(page, "so Hyprland can run only on the NVIDIA GeForce GT 330M.") {
		t.Error("/mac/MacBookPro6-2: no partial note")
	}
	if page := get("/mac/MacBookPro8-2"); strings.Contains(page, "limit-line") {
		t.Error("/mac/MacBookPro8-2: a note on GPUs that meet the floor")
	}
	if page := get("/methodology"); !strings.Contains(page, "<h2>Known limitations</h2>") || !strings.Contains(page, "OpenGL ES 3.0 or later, not with the llvmpipe software renderer") {
		t.Error("/methodology: no known-limitations section or criterion description")
	}
	if page := get("/components"); !strings.Contains(page, `<span class="muted">· GLES 2.0</span>`) {
		t.Error("/components: no GLES levels")
	}

	// The GLES2 tag in the status column: the Macs list and search results.
	tag := `<span class="tag gles2" title="Its GPU reaches OpenGL ES 2.0 only.`
	for _, path := range []string{"/macs?q=gles:2.0", "/search?q=gles:2.0"} {
		page := get(path)
		if n := strings.Count(page, tag); n != 20 {
			t.Errorf("%s: %d GLES2 tags, want 20 (22 Macs, less MacBookPro6,1 and 6,2)", path, n)
		}
	}
	if page := get("/macs?q=MacBookPro6,2"); strings.Contains(page, tag) {
		t.Error("MacBookPro6,2 can run Hyprland on its GeForce: no GLES2 tag")
	}

	var mac struct {
		Configs []struct {
			Limitations []string `json:"known_limitations"`
			Components  []struct {
				Kind, GLES string
			} `json:"components"`
		} `json:"configurations"`
	}
	if err := json.Unmarshal([]byte(get("/api/v1/macs/MacBookAir1,1")), &mac); err != nil || len(mac.Configs) != 1 {
		t.Fatalf("API: %v %+v", err, mac)
	}
	cfg := mac.Configs[0]
	if len(cfg.Limitations) != 1 || !strings.HasPrefix(cfg.Limitations[0], "The Intel GMA X3100 reaches OpenGL ES 2.0 only.") {
		t.Errorf("API known_limitations: %q", cfg.Limitations)
	}
	for _, c := range cfg.Components {
		if c.Kind == "gpu" && c.GLES != "2.0" {
			t.Errorf("API gpu gles: %q", c.GLES)
		}
	}
}
