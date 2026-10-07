package web

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// One status per connector, from its per-connector criteria (PLAN §27).
func TestConnStatuses(t *testing.T) {
	cp := func(ps ...capPort) capView { return capView{Ports: ps} }
	cv := &configView{Categories: []categoryView{{Caps: []capView{
		cp(capPort{ID: "left-1", Verdict: status.Supported}, capPort{ID: "left-2", Verdict: status.Supported},
			capPort{ID: "left-3", Verdict: status.Untested, CoveredBy: "left-2"}, capPort{ID: "left-4", Verdict: status.Supported},
			capPort{ID: "right-1", Verdict: status.Failed}, capPort{ID: "right-2", Verdict: status.Failed, Suspect: true},
			capPort{ID: "right-3", Verdict: status.Untested}),
		cp(capPort{ID: "left-1", Verdict: status.Supported}, capPort{ID: "left-2", Verdict: status.Untested, CoveredBy: "left-1"},
			capPort{ID: "left-3", Verdict: status.Untested, CoveredBy: "left-1"}, capPort{ID: "left-4", Verdict: status.Untested},
			capPort{ID: "right-1", Verdict: status.Untested}, capPort{ID: "back-1", Verdict: status.Partial}),
		// right-4 passed one criterion and failed another: partly works.
		{Name: "USB data", Ports: []capPort{{ID: "right-4", Verdict: status.Supported}}},
		{Name: "External display output", Ports: []capPort{{ID: "right-4", Verdict: status.Failed}}},
	}}}}
	want := map[string]string{"left-1": pmSupported, "left-2": pmCovered, "left-3": pmCovered, "left-4": pmPartly,
		"right-1": pmFailed, "right-2": pmSuspect, "back-1": pmPartial, "right-4": pmPartial}
	got, results := connStatuses(cv)
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %q, want %q", id, got[id], w)
		}
	}
	if _, ok := got["right-3"]; ok {
		t.Error("an untested connector gets no status")
	}
	if r := strings.Join(results["right-4"], "; "); r != "USB data passed; External display output failed" {
		t.Errorf("right-4 results: %q", r)
	}
}

func TestPortmapsOnTheSite(t *testing.T) {
	ts, st, c := liveServer(t)
	ctx := context.Background()
	const cfg = "macbookpro11-3-15-late-2013-a"
	f := &results.File{Schema: results.SchemaV1, Config: cfg, TestedAt: "2026-09-29T15:30:00Z",
		Omarchy: results.FileOmarchy{Version: "4.0.4"}, Source: results.FileSource{ID: "manual"}, Items: map[string]results.FileItem{
			"boot.install":             {Status: "supported", Method: "observed"},
			"ports.usb-a@left-4":       {Status: "supported", Method: "fixture"},
			"ports.usb-a@right-3":      {Status: "failed", Method: "fixture"},
			"ports.thunderbolt@left-2": {Status: "supported", Method: "fixture"},
		}}
	r, err := results.Validate(f, c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(f)
	id, err := st.InsertResult(ctx, r, raw, results.SchemaV1, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetResultState(ctx, id, store.Accepted, "", "test"); err != nil {
		t.Fatal(err)
	}
	d, err := st.Result(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	// The Mac page: the list numbered to match the drawing, which loads when
	// the Hardware details open (a fragment, coloured by the results).
	page := eventually(t, ts, "/mac/MacBookPro11-3", func(b string) bool { return strings.Contains(b, "Latest diagnostic report") }, "the accepted report")
	for _, want := range []string{
		`data-pmsrc="/portmap/MacBookPro11-3_15-late-2013/` + cfg + `"`,
		`<noscript><img class="pmimg" src="/portmap/MacBookPro11-3_15-late-2013.svg"`,
		`data-conn="right-3" tabindex="0"><span class="pmnum" data-st="suspect"`,
		`href="/portmap/MacBookPro11-3_15-late-2013.svg?download"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("Mac page lacks %s", want)
		}
	}
	if strings.Contains(page, `<svg xmlns="http://www.w3.org/2000/svg" class="pm`) {
		t.Error("drawings should load on demand, not inline in the Mac page")
	}
	code, frag := body(t, ts, "/portmap/MacBookPro11-3_15-late-2013/"+cfg)
	for _, want := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" class="pm pm-wide"`,
		`class="pm-port" data-conn="right-3" data-st="suspect"`, // failed while its group-mate passed
		`class="pm-port" data-conn="left-4" data-st="supported"`,
		`id="pm-macbookpro11-3-15-late-2013-` + cfg + `-t"`, // IDs suffixed per configuration
	} {
		if code != http.StatusOK || !strings.Contains(frag, want) {
			t.Errorf("fragment (%d) lacks %s", code, want)
		}
	}
	if strings.Contains(frag, "<style") {
		t.Error("an inline <style> in the fragment: the CSP blocks it")
	}
	for _, p := range []string{"/portmap/MacBookPro11-2_15-late-2013/" + cfg, "/portmap/MacBookPro11-3_15-late-2013/nope"} {
		if code, _ := body(t, ts, p); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, code)
		}
	}

	// A configuration with fewer ports than its release's drawing hides the rest.
	_, one := body(t, ts, "/portmap/MacPro7-1_2019/macpro7-1-2019-w5700x")
	_, two := body(t, ts, "/portmap/MacPro7-1_2019/macpro7-1-2019-580x")
	if !strings.Contains(one, `class="pm-port" data-conn="back-7" data-absent=""`) || strings.Contains(two, "data-absent") {
		t.Error("the W5700X fragment should hide back-7 (its module's missing second HDMI); the 580X shows both")
	}

	// Element IDs stay unique when every configuration's drawing is open at once.
	_, multi := body(t, ts, "/mac/MacBookPro8-2")
	srcs := regexp.MustCompile(`data-pmsrc="([^"]+)"`).FindAllStringSubmatch(multi, -1)
	if len(srcs) < 2 {
		t.Fatalf("each MacBookPro8,2 configuration should have its drawing, got %d", len(srcs))
	}
	all := multi
	for _, m := range srcs {
		_, fr := body(t, ts, m[1])
		all += fr
	}
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(` id="([^"]+)"`).FindAllStringSubmatch(all, -1) {
		if seen[m[1]] {
			t.Errorf("duplicate id %q on /mac/MacBookPro8-2 with its drawings loaded", m[1])
		}
		seen[m[1]] = true
	}

	// The report page: coloured by the report's own items.
	_, rep := body(t, ts, "/report/"+d.Code)
	for _, want := range []string{`class="pm-port" data-conn="right-3" data-st="failed"`, `class="pm-port" data-conn="left-2" data-st="supported"`, "Coloured by this report"} {
		if !strings.Contains(rep, want) {
			t.Errorf("report page lacks %s", want)
		}
	}

	// The download: standalone, styled, credited, locked down.
	res, err := ts.Client().Get(ts.URL + "/portmap/MacBookPro11-3_15-late-2013.svg?download")
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1<<16)
	n, _ := res.Body.Read(b)
	res.Body.Close()
	svg := string(b[:n])
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/svg+xml; charset=utf-8" ||
		!strings.HasPrefix(res.Header.Get("Content-Security-Policy"), "default-src 'none'") ||
		!strings.Contains(res.Header.Get("Content-Disposition"), "MacBookPro11-3_15-late-2013.svg") {
		t.Errorf("download: %d %v", res.StatusCode, res.Header)
	}
	if !strings.Contains(svg, "<style>") || !strings.Contains(svg, "CC BY-SA 4.0") {
		t.Error("the download should carry its style and the credit line")
	}
	for _, p := range []string{"/portmap/MacBookPro11-3_15-late-2013", "/portmap/Nope_1.svg"} {
		if code, _ := body(t, ts, p); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, code)
		}
	}

	// The API points at the drawing.
	_, api := body(t, ts, "/api/v1/configs/"+cfg)
	if !strings.Contains(api, `"portmap_url": "https://doesitomarchy.com/portmap/MacBookPro11-3_15-late-2013.svg"`) {
		t.Errorf("API config lacks portmap_url")
	}
}
