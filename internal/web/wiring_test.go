package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
	"github.com/doesitomarchy/doesitomarchy/pkg/report"
)

// The agreement rule: two accepted reports, or one checked; a path on two
// connectors counts on neither.
func TestBuildWiring(t *testing.T) {
	layout := map[string]bool{"left-2": true, "left-10": true, "right-1": true}
	row := func(id int64, checked bool, conn string, paths ...string) store.PortRow {
		return store.PortRow{ResultID: id, Checked: checked, PortAnswer: report.PortAnswer{Connector: conn, Paths: paths}}
	}
	for name, tc := range map[string]struct {
		rows          []store.PortRow
		want, dropped string
	}{
		"one report":                    {[]store.PortRow{row(1, false, "left-2", "a")}, "", ""},
		"one report, two tests":         {[]store.PortRow{row(1, false, "left-2", "a"), row(1, false, "left-2", "a")}, "", ""},
		"two reports agree":             {[]store.PortRow{row(1, false, "left-2", "b", "a"), row(2, false, "left-2", "a")}, "left-2=a", ""},
		"one checked report":            {[]store.PortRow{row(1, true, "left-2", "b", "a")}, "left-2=a,b", ""},
		"sorted by connector":           {[]store.PortRow{row(1, true, "left-10", "x"), row(1, true, "left-2", "y"), row(1, true, "right-1", "z")}, "left-2=y left-10=x right-1=z", ""},
		"not in the layout":             {[]store.PortRow{row(1, true, "back-1", "a")}, "", ""},
		"a counted path on two":         {[]store.PortRow{row(1, true, "left-2", "a", "b"), row(2, true, "right-1", "a")}, "left-2=b", "a@left-2/right-1"},
		"an uncounted one doesn't drop": {[]store.PortRow{row(1, true, "left-2", "a"), row(2, false, "right-1", "a")}, "left-2=a", ""},
	} {
		w := buildWiring(tc.rows, layout)
		var got, dropped []string
		for _, x := range w.snapshot() {
			got = append(got, x.Connector+"="+strings.Join(x.Paths, ","))
		}
		for _, d := range w.Dropped {
			dropped = append(dropped, d.Path+"@"+strings.Join(d.Connectors, "/"))
		}
		if strings.Join(got, " ") != tc.want || strings.Join(dropped, " ") != tc.dropped {
			t.Errorf("%s: wiring %q dropped %q; want %q, %q", name, got, dropped, tc.want, tc.dropped)
		}
	}
}

// Ports answers end to end: stored, shown in /admin, marked checked, and in
// the snapshot's wiring, whose version follows.
func TestPortWiring(t *testing.T) {
	ts, _, st, c := adminServer(t, Options{AdminInsecure: true})
	ctx := context.Background()
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	const cfg = "macbookpro11-3-15-late-2013-a" // left-4 and right-3 are USB 3
	submit := func(at, ports string) (int64, string) {
		f, err := results.Parse([]byte(fmt.Sprintf(`schema: doesitomarchy/report/v1
config: %s
tested_at: %s
omarchy: { version: "4.0.4" }
items: { boot.install: { status: supported, method: observed } }
ports: [%s]
`, cfg, at, ports)))
		if err != nil {
			t.Fatal(err)
		}
		r, err := results.Validate(f, c, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		id, err := st.InsertResult(ctx, r, nil, results.SchemaV1, "test")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetResultState(ctx, id, store.Accepted, "", "test"); err != nil {
			t.Fatal(err)
		}
		d, _ := st.Result(ctx, id)
		return id, d.Code
	}
	wiring := func() (string, string) {
		res, err := client.Get(ts.URL + "/api/v1/snapshot")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var snap report.CatalogSnapshot
		json.NewDecoder(res.Body).Decode(&snap)
		var out []string
		for _, m := range snap.Match.Macs {
			for _, r := range m.Releases {
				for _, cf := range r.Configs {
					for _, w := range cf.Wiring {
						out = append(out, cf.ID+" "+w.Connector+"="+strings.Join(w.Paths, ","))
					}
				}
			}
		}
		return strings.Join(out, "; "), res.Header.Get("ETag")
	}
	page := func(code string) string {
		res, err := client.Get(ts.URL + "/admin/report/" + code)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	post := func(code, action string) string {
		csrf := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(page(code))[1]
		res, err := client.PostForm(ts.URL+"/admin/report/"+code+"/"+action, url.Values{"csrf": {csrf}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.Header.Get("Location")
	}
	settle := func(want string) string {
		deadline := time.Now().Add(5 * time.Second)
		for {
			got, etag := wiring()
			if got == want {
				return etag
			}
			if time.Now().After(deadline) {
				t.Fatalf("wiring %q, want %q", got, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	_, etag0 := wiring()
	_, a := submit("2026-10-01T12:00:00Z", `{ test: ports.usb-a, connector: left-4, paths: ["usb2:0000:00:14.0/1", "usb3:0000:00:14.0/1"], how: asked }`)
	settle("") // one report, unchecked: nothing yet
	if p := page(a); !strings.Contains(p, "<h2>Ports</h2>") || !strings.Contains(p, "usb3:0000:00:14.0/1") || !strings.Contains(p, "Mark: wiring checked") {
		t.Fatal("the report page lacks its ports or the mark")
	}
	if loc := post(a, "wiring-checked"); !strings.Contains(loc, "done=wiring-checked") {
		t.Fatalf("mark: %s", loc)
	}
	etag1 := settle(cfg + " left-4=usb2:0000:00:14.0/1,usb3:0000:00:14.0/1")
	if etag1 == etag0 {
		t.Error("the snapshot's ETag must change with its wiring")
	}
	if p := page(a); !strings.Contains(p, "Wiring checked by local") || !strings.Contains(p, "Unmark: wiring checked") {
		t.Error("the report page doesn't show the mark")
	}

	// A second report puts a path elsewhere: shown as a disagreement, and,
	// once checked too, dropped from both connectors.
	_, b := submit("2026-10-02T12:00:00Z", `{ test: ports.usb-a, connector: right-3, paths: ["usb2:0000:00:14.0/1"], how: corrected, wiring_said: left-4 }`)
	if p := page(b); !strings.Contains(p, "the wiring puts usb2:0000:00:14.0/1 on left-4") || !strings.Contains(p, "the tester corrected the wiring, which said left-4") {
		t.Error("the second report's disagreement isn't flagged")
	}
	post(b, "wiring-checked")
	settle(cfg + " left-4=usb3:0000:00:14.0/1")
	if p := page(a); !strings.Contains(p, "Left out of this configuration's wiring") {
		t.Error("the dropped path isn't shown")
	}
	post(a, "wiring-unchecked")
	settle(cfg + " right-3=usb2:0000:00:14.0/1")
	if p := page(a); !strings.Contains(p, "Mark: wiring checked") {
		t.Error("unmarking didn't show")
	}
}
