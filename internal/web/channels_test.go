package web

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// Omarchy channels on the site (PLAN §28.1–28.2): stable results decide the
// verdict; a newer build with a different result shows on its own line; every
// version reads "Omarchy <version> (<channel>)".
func TestChannels(t *testing.T) {
	ctx := context.Background()
	ts, _, st, c := adminServer(t, Options{AdminInsecure: true})
	get := func(path string) string {
		t.Helper()
		res, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	waitFor := func(path, want string) string {
		t.Helper()
		var body string
		for i := 0; i < 100; i++ {
			if body = get(path); strings.Contains(body, want) {
				return body
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatalf("%s never showed %q", path, want)
		return ""
	}
	accept := func(config, version, channel, builtAt, tested, speakers string) string {
		t.Helper()
		f, err := results.Parse([]byte(`schema: doesitomarchy/report/v1
config: ` + config + `
tested_at: ` + tested + `
omarchy: { version: "` + version + `", channel: "` + channel + `" }
items:
  audio.speakers: { status: ` + speakers + `, method: observed, evidence: "speakers ` + speakers + ` on ` + version + `" }
`))
		if err != nil {
			t.Fatal(err)
		}
		r, err := results.Validate(f, c, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		r.BuiltAt = builtAt // what the build lookup would have found
		id, err := st.InsertResult(ctx, r, nil, results.SchemaV1, "test")
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetResultState(ctx, id, store.Accepted, "", "test"); err != nil {
			t.Fatal(err)
		}
		d, _ := st.Result(ctx, id)
		return d.Code
	}

	// Stable 4.0.4 fails the speakers; a newer dev build fixes them.
	card := "/mac/MacBookPro11-3"
	stableCode := accept("macbookpro11-3-15-late-2013-a", "v4.0.4-1", "", "2026-09-15T05:34:12Z", "2026-09-20T12:00:00Z", "failed")
	devCode := accept("macbookpro11-3-15-late-2013-a", "4.0.0.r6800.g1a2b3c4", "dev", "2026-10-01T09:00:00Z", "2026-10-02T12:00:00Z", "supported")
	body := waitFor(card, "Newer build:")
	for _, want := range []string{
		"Omarchy 4.0.4 (stable)", // the canonical form of v4.0.4-1, with its channel
		`Newer build: <svg class="i v v-supported"`,
		"Supported on <a href=\"/report/" + devCode,
		"Omarchy 4.0.0.r6800.g1a2b3c4 (dev)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("card: missing %q", want)
		}
	}
	if !strings.Contains(body, `<span class="st st-failed">Failed</span>`) {
		t.Error("the stable verdict should stay Failed")
	}
	if rp := get("/report/" + stableCode); !strings.Contains(rp, "Omarchy <b>4.0.4</b> (stable)") {
		t.Error("report page: version and channel")
	}

	var api struct {
		Capabilities []struct {
			ID             string `json:"id"`
			Verdict        string `json:"verdict"`
			Omarchy        string `json:"omarchy"`
			OmarchyChannel string `json:"omarchy_channel"`
			NewestBuild    *struct {
				Verdict        string `json:"verdict"`
				Omarchy        string `json:"omarchy"`
				OmarchyChannel string `json:"omarchy_channel"`
				Report         string `json:"report"`
			} `json:"newest_build"`
		} `json:"capabilities"`
		Reports []struct {
			Code           string `json:"code"`
			Omarchy        string `json:"omarchy"`
			OmarchyChannel string `json:"omarchy_channel"`
		} `json:"reports"`
	}
	if err := json.Unmarshal([]byte(get("/api/v1/configs/macbookpro11-3-15-late-2013-a")), &api); err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, cp := range api.Capabilities {
		if cp.ID == "audio.speakers" {
			n := cp.NewestBuild
			ok = cp.Verdict == "failed" && cp.Omarchy == "4.0.4" && cp.OmarchyChannel == "stable" && n != nil &&
				n.Verdict == "supported" && n.OmarchyChannel == "dev" && strings.HasSuffix(n.Report, devCode)
		}
	}
	if !ok {
		t.Errorf("API speakers: %+v", api.Capabilities)
	}
	for _, r := range api.Reports {
		if r.Code == devCode && (r.Omarchy != "4.0.0.r6800.g1a2b3c4" || r.OmarchyChannel != "dev") {
			t.Errorf("API report: %+v", r)
		}
	}

	// A Mac tested only on an edge build: untested on stable, with the
	// newer build's result shown.
	accept("macbookair5-2-mid-2012-a", "4.0.0.r6713.ga85e29a", "", "2026-09-30T00:00:00Z", "2026-10-03T16:49:05Z", "supported")
	air := waitFor("/mac/MacBookAir5-2", "Untested on stable")
	if !strings.Contains(air, "Omarchy 4.0.0.r6713.ga85e29a (edge)") || !strings.Contains(air, "Newer build:") {
		t.Error("edge-only Mac: the newer build's result")
	}
}
