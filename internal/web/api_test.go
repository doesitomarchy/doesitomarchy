package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
	"github.com/doesitomarchy/doesitomarchy/pkg/match"
	"github.com/doesitomarchy/doesitomarchy/pkg/report"
)

func apiCall(t *testing.T, client *http.Client, method, url, key string, body []byte) (int, map[string]any, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(method, url, bytes.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var out map[string]any
	json.Unmarshal(b, &out)
	return res.StatusCode, out, res.Header
}

// The synthetic fixture with no config ID: the server finds it from the probe.
func fixtureWithoutConfig() []byte {
	f := strings.Replace(string(results.SyntheticFixture), "config: macbookpro15-2-13-2018-4tb3-a\n", "identifier: MacBookPro15,2\n", 1)
	return []byte(strings.Replace(f, "source: { id: manual, ", "source: { ", 1)) // the key names the source
}

func TestAPIReads(t *testing.T) {
	ts, _, _ := liveServer(t)
	c := ts.Client()
	code, idx, h := apiCall(t, c, "GET", ts.URL+"/api/v1", "", nil)
	if code != 200 || idx["schema"] != results.SchemaV1 || h.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("index: %d %v %v", code, idx, h)
	}
	code, caps, _ := apiCall(t, c, "GET", ts.URL+"/api/v1/capabilities", "", nil)
	if list, _ := caps["capabilities"].([]any); code != 200 || len(list) != 46 {
		t.Fatalf("capabilities: %d %d", code, len(list))
	}
	code, macs, _ := apiCall(t, c, "GET", ts.URL+"/api/v1/macs", "", nil)
	if list, _ := macs["macs"].([]any); code != 200 || len(list) != 121 {
		t.Fatalf("macs: %d %d", code, len(list))
	}
	for _, id := range []string{"MacBookPro15,2", "macbookpro15-2"} {
		code, mac, _ := apiCall(t, c, "GET", ts.URL+"/api/v1/macs/"+id, "", nil)
		cfgs, _ := mac["configurations"].([]any)
		if code != 200 || mac["identifier"] != "MacBookPro15,2" || len(cfgs) != 2 {
			t.Fatalf("mac %s: %d %v", id, code, mac["identifier"])
		}
	}
	code, cfg, _ := apiCall(t, c, "GET", ts.URL+"/api/v1/configs/macbookpro15-2-13-2018-4tb3-a", "", nil)
	if code != 200 || cfg["verdict"] != "untested" || cfg["applicable"] != float64(31) {
		t.Fatalf("config: %d %v", code, cfg)
	}
	if code, _, _ := apiCall(t, c, "GET", ts.URL+"/api/v1/configs/nope", "", nil); code != 404 {
		t.Errorf("unknown config: %d", code)
	}
	res, _ := c.Get(ts.URL + "/api/v1/schema")
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !json.Valid(b) || !strings.Contains(string(b), results.SchemaV1) {
		t.Error("schema endpoint")
	}
	code, m, _ := apiCall(t, c, "POST", ts.URL+"/api/v1/match", "", []byte(`{"product_name":"MacBookPro8,2","pci":["1002:6760"]}`))
	cands, _ := m["candidates"].([]any)
	if code != 200 || m["exact"] != true || len(cands) != 4 || !strings.Contains(cands[0].(map[string]any)["url"].(string), "#cfg-macbookpro8-2-15-early-2011-a") {
		t.Fatalf("match: %d %v", code, m)
	}
	if m["config"] != "macbookpro8-2-15-early-2011-a" || fmt.Sprint(m["best"]) != "[macbookpro8-2-15-early-2011-a]" {
		t.Errorf("exact match: config %v, best %v", m["config"], m["best"])
	}
	// The decision comes first in the body, then the full ranking.
	mres, _ := c.Post(ts.URL+"/api/v1/match", "application/json", strings.NewReader(`{"product_name":"MacBookPro8,2","pci":["1002:6760"]}`))
	raw, _ := io.ReadAll(mres.Body)
	mres.Body.Close()
	last := -1
	for _, k := range []string{`"identifier"`, `"by"`, `"exact"`, `"config"`, `"best"`, `"candidates"`} {
		i := bytes.Index(raw, []byte(k))
		if i < last {
			t.Errorf("%s out of order in %s", k, raw)
		}
		last = i
	}
	// A tie: config is null, and present, so clients needn't infer it.
	_, m, _ = apiCall(t, c, "POST", ts.URL+"/api/v1/match", "", []byte(`{"product_name":"MacBookPro8,2","pci":["1002:6741"]}`))
	if v, ok := m["config"]; !ok || v != nil || m["exact"] != false {
		t.Errorf("tied match: config %v (present %v), exact %v", v, ok, m["exact"])
	}
	if fmt.Sprint(m["best"]) != "[macbookpro8-2-15-early-2011-b macbookpro8-2-15-late-2011-a]" {
		t.Errorf("tied match best: %v", m["best"])
	}
	// The CPU breaks that tie.
	_, m, _ = apiCall(t, c, "POST", ts.URL+"/api/v1/match", "", []byte(`{"product_name":"MacBookPro8,2","pci":["1002:6741"],"cpu":"Intel(R) Core(TM) i7-2675QM CPU @ 2.20GHz"}`))
	if m["config"] != "macbookpro8-2-15-late-2011-a" {
		t.Errorf("CPU tie-break: %v", m["config"])
	}
	if code, _, _ := apiCall(t, c, "POST", ts.URL+"/api/v1/match", "", []byte(`not json`)); code != 400 {
		t.Errorf("bad match body: %d", code)
	}
}

func TestAPISubmit(t *testing.T) {
	ts, st, _ := liveServer(t)
	c := ts.Client()
	ctx := context.Background()
	key, err := st.AddSource(ctx, "testtool", "Test Tool", "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	submit := func(key string, body []byte) (int, map[string]any) {
		code, out, _ := apiCall(t, c, "POST", ts.URL+"/api/v1/reports", key, body)
		return code, out
	}

	// The happy path: no config ID; the probe finds it; pending; status by code.
	code, out := submit(key, fixtureWithoutConfig())
	if code != http.StatusCreated || out["state"] != "pending" || out["config"] != "macbookpro15-2-13-2018-4tb3-a" {
		t.Fatalf("submit: %d %v", code, out)
	}
	rc := out["code"].(string)
	code, status, _ := apiCall(t, c, "GET", ts.URL+"/api/v1/reports/"+rc, "", nil)
	if code != 200 || status["state"] != "pending" || status["url"] != nil {
		t.Fatalf("status: %d %v", code, status)
	}
	d, _ := st.Result(ctx, mustID(t, st, rc))
	if d.SourceID != "testtool" || d.SubmittedBy != "source:testtool" {
		t.Errorf("source: %s by %s", d.SourceID, d.SubmittedBy)
	}
	st.SetResultState(ctx, d.ID, store.Rejected, "test", "carl")
	_, status, _ = apiCall(t, c, "GET", ts.URL+"/api/v1/reports/"+rc, "", nil)
	if status["state"] != "rejected" || status["reason"] != "test" {
		t.Errorf("rejected status: %v", status)
	}

	// An ambiguous report: stored, flagged, candidates returned.
	amb := []byte(`schema: doesitomarchy/report/v1
identifier: MacBookPro8,2
hardware: { pci: ["1002:6741"] }
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4" }
items: { boot.install: { status: supported, method: observed } }
`)
	code, out = submit(key, amb)
	if cands, _ := out["candidates"].([]any); code != 201 || len(cands) != 2 || !strings.Contains(toJSON(out["flags"]), "config_ambiguous") {
		t.Fatalf("ambiguous: %d %v", code, out)
	}

	tests := []struct {
		name string
		key  string
		body []byte
		code int
		want string
	}{
		{"no key", "", fixtureWithoutConfig(), 401, "Bearer"},
		{"wrong key", "doi_00000000000000000000000000000000", fixtureWithoutConfig(), 401, "source key"},
		{"another source's name", key, []byte(strings.Replace(string(fixtureWithoutConfig()), "source: { ", "source: { id: omacdiag, ", 1)), 403, "belongs to source"},
		{"unknown hardware", key, []byte(strings.Replace(string(amb), "identifier: MacBookPro8,2\nhardware: { pci: [\"1002:6741\"] }", "identifier: MacBookPro99,9", 1)), 422, "no configuration matches"},
		{"invalid", key, []byte("schema: nope\n"), 400, "invalid report"},
		{"too large", key, bytes.Repeat([]byte("#"), results.MaxSize+10), 413, "1 MiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out := submit(tt.key, tt.body)
			if code != tt.code || !strings.Contains(toJSON(out), tt.want) {
				t.Fatalf("%d %v", code, out)
			}
		})
	}
	// A body naming "manual" isn't this key's source either: the API can never submit as manual.
	if code, out := submit(key, results.SyntheticFixture); code != 403 {
		t.Errorf("a body naming manual: %d %v", code, out)
	}
	if code, out, _ := apiCall(t, c, "POST", ts.URL+"/api/v1/reports?format=omacdiag/v1", key, fixtureWithoutConfig()); code != 415 {
		t.Errorf("unknown format: %d %v", code, out)
	}

	// Rate limit.
	old := ReportsPerHour
	ReportsPerHour = 2
	defer func() { ReportsPerHour = old }()
	if code, out := submit(key, fixtureWithoutConfig()); code != 429 || !strings.Contains(toJSON(out), "last hour") {
		t.Errorf("rate limit: %d %v", code, out)
	}

	// Revoked.
	st.RevokeSource(ctx, "testtool")
	ReportsPerHour = old
	if code, _ := submit(key, fixtureWithoutConfig()); code != 403 {
		t.Errorf("revoked: %d", code)
	}
	if code, _, _ := apiCall(t, c, "GET", ts.URL+"/api/v1/reports/0123456789", "", nil); code != 404 {
		t.Errorf("unknown report: %d", code)
	}
}

func mustID(t *testing.T, st *store.Store, code string) int64 {
	t.Helper()
	id, err := st.ResultIDByCode(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The catalog snapshot (with ETag) and OmaBoot? Live's per-Mac limit.
func TestAPILive(t *testing.T) {
	ts, st, _ := liveServer(t)
	c := ts.Client()
	res, err := c.Get(ts.URL + "/api/v1/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var snap report.CatalogSnapshot
	if err := json.Unmarshal(b, &snap); err != nil || res.StatusCode != 200 {
		t.Fatalf("snapshot: %d %v", res.StatusCode, err)
	}
	if etag := res.Header.Get("ETag"); etag != `"`+snap.Version+`"` || len(snap.Version) != 16 || len(snap.Capabilities) != 46 || len(snap.Fixes) < 2 {
		t.Fatalf("snapshot: ETag %s, version %s, %d criteria, %d fixes", etag, snap.Version, len(snap.Capabilities), len(snap.Fixes))
	}
	if r := match.New(snap.Match).Match(match.Probe{ProductName: "MacBookPro8,2", PCI: []string{"1002:6760"}}); r.Best() != "macbookpro8-2-15-early-2011-a" {
		t.Errorf("matching on the snapshot: %+v", r)
	}
	for _, inm := range []string{`"` + snap.Version + `"`, `W/"` + snap.Version + `", "other"`, "*"} {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1/snapshot", nil)
		req.Header.Set("If-None-Match", inm)
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotModified || res.Header.Get("ETag") == "" {
			t.Errorf("If-None-Match %s: %d", inm, res.StatusCode)
		}
	}
	if _, caps, _ := apiCall(t, c, "GET", ts.URL+"/api/v1/capabilities", "", nil); !strings.Contains(toJSON(caps), `"live":"t2"`) {
		t.Error("capabilities lack the live flag")
	}

	key, err := st.AddSource(context.Background(), "boot-live", "OmaBoot? Live", "https://doesitboot.com")
	if err != nil {
		t.Fatal(err)
	}
	live := func(board string) []byte {
		return []byte(`schema: doesitomarchy/report/v1
identifier: MacBookAir5,2
context: live
hardware: { board_id: ` + board + `, pci: ["8086:0166", "14e4:4353"] }
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4", image: "omaboot-live dev 0b8738f" }
items:
  boot.install: { status: not_tested, reason: live-limit }
  display.brightness: { status: supported, method: challenge }
`)
	}
	for i := 1; i <= ReportsPerDayPerMac; i++ {
		if code, out, _ := apiCall(t, c, "POST", ts.URL+"/api/v1/reports", key, live("Mac-2E6FAB96566FE58C")); code != 201 {
			t.Fatalf("live report %d: %d %v", i, code, out)
		}
	}
	code, out, h := apiCall(t, c, "POST", ts.URL+"/api/v1/reports", key, live("Mac-2E6FAB96566FE58C"))
	if code != 429 || !strings.Contains(toJSON(out), "this Mac has sent 5 reports") || h.Get("Retry-After") == "" {
		t.Fatalf("per-Mac limit: %d %v %v", code, out, h)
	}
	// Another Mac (here, another board) isn't held back.
	if code, out, _ := apiCall(t, c, "POST", ts.URL+"/api/v1/reports", key, live("Mac-66F35F19FE2A0D05")); code != 201 {
		t.Fatalf("another Mac: %d %v", code, out)
	}
	// boot-live has its own hourly limit, below the usual one: 6 stored so far.
	old := ReportsPerHourPublic
	ReportsPerHourPublic = 6
	defer func() { ReportsPerHourPublic = old }()
	code, out, h = apiCall(t, c, "POST", ts.URL+"/api/v1/reports", key, live("Mac-0000000000000001"))
	if code != 429 || !strings.Contains(toJSON(out), "submitted 6 reports in the last hour") || h.Get("Retry-After") == "" {
		t.Fatalf("boot-live hourly limit: %d %v", code, out)
	}
	if ReportsPerHourPublic >= ReportsPerHour || old >= ReportsPerHour {
		t.Error("the public key's hourly limit should be below the usual one")
	}
}
