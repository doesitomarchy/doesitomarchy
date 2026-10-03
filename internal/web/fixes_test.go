package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/fixes"
	"github.com/doesitomarchy/doesitomarchy/internal/fixes/fakegithub"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

const testSecret = "webhook-secret"

// Fix tracking end to end (PLAN §26.4): a failure, an issue opened from
// /admin, webhook updates, the re-test prompt, and giving up.
func TestFixTracking(t *testing.T) {
	ctx := context.Background()
	gh := fakegithub.New(t, fixes.DefaultRepo)
	ts, srv, st, c := adminServer(t, Options{AdminInsecure: true, GitHubToken: fakegithub.Token, WebhookSecret: testSecret})
	srv.gh.Base = gh.URL
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	get := func(path string) string {
		t.Helper()
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	// waitFor polls a page until it shows want (the site rebuilds in the background).
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
	card := "/mac/MacBookPro11-3"

	// An accepted report: the speakers fail on the Late 2013 15-inch.
	f, err := results.Parse([]byte(`schema: doesitomarchy/report/v1
config: macbookpro11-3-15-late-2013-a
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4" }
items:
  boot.install: { status: supported, method: observed }
  audio.speakers: { status: failed, method: observed, evidence: "cs4208: no output on the speaker pins" }
`))
	if err != nil {
		t.Fatal(err)
	}
	r, err := results.Validate(f, c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := st.InsertResult(ctx, r, nil, results.SchemaV1, "test")
	if err := st.SetResultState(ctx, id, store.Accepted, "", "test"); err != nil {
		t.Fatal(err)
	}
	waitFor(card, "cs4208: no output")
	if body := get(card); !strings.Contains(body, `<a href="/contribute">Help fix it →</a>`) {
		t.Error("no fix yet: Help fix it leads to /contribute")
	}

	// /admin/fixes offers to open a fix for the speakers on the codec.
	page := get("/admin/fixes")
	if !strings.Contains(page, "Built-in speakers") || !strings.Contains(page, "Cirrus Logic CS4208") {
		t.Fatalf("/admin/fixes doesn't list the failure:\n%s", page)
	}
	csrf := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(page)[1]
	post := func(path string, form url.Values) string {
		t.Helper()
		form.Set("csrf", csrf)
		res, err := client.PostForm(ts.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.Header.Get("Location")
	}
	loc := post("/admin/fixes/open", url.Values{"capability": {"audio.speakers"}, "component": {"audio/cirrus-cs4208"}})
	if !strings.Contains(loc, "done=") || !strings.Contains(loc, "issue+%231") {
		t.Fatalf("open: %s", loc)
	}
	is := gh.Issues[1]
	if is == nil || is.Title != "Built-in speakers on Cirrus Logic CS4208" || !gh.Labels["component:audio/cirrus-cs4208"] ||
		!strings.Contains(gh.Comments[1][0], "cs4208: no output") || !strings.Contains(gh.Comments[1][0], "/report/") {
		t.Fatalf("issue: %+v\n%v", is, gh.Comments[1])
	}
	waitFor(card, "Nobody is working on this yet")
	if !strings.Contains(get(card), `href="https://github.com/doesitomarchy/wecanfixeverything/issues/1"`) {
		t.Error("the card links the issue")
	}

	// Webhook deliveries.
	deliver := func(event, delivery, secret string, payload any) int {
		t.Helper()
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest("POST", ts.URL+"/hooks/github", bytes.NewReader(b))
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-GitHub-Delivery", delivery)
		m := hmac.New(sha256.New, []byte(secret))
		m.Write(b)
		req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(m.Sum(nil)))
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	repo := map[string]string{"full_name": fixes.DefaultRepo}
	if code := deliver("ping", "d0", testSecret, map[string]any{"zen": "hi"}); code != 200 {
		t.Errorf("ping: %d", code)
	}
	if code := deliver("issues", "d1", "wrong", map[string]any{"issue": is, "repository": repo}); code != 401 {
		t.Errorf("bad signature: %d", code)
	}
	// Someone claims it.
	gh.Mu.Lock()
	is.Assignee, is.UpdatedAt = &fakegithub.User{Login: "helper"}, time.Now().UTC()
	gh.Mu.Unlock()
	if code := deliver("issues", "d2", testSecret, map[string]any{"action": "assigned", "issue": is, "repository": repo}); code != 202 {
		t.Errorf("assigned: %d", code)
	}
	waitFor(card, "Being worked on by <b>@helper</b>")
	if code := deliver("issues", "d2", testSecret, map[string]any{"action": "assigned", "issue": is, "repository": repo}); code != 200 {
		t.Errorf("a redelivery is a no-op: %d", code)
	}

	// It closes as completed by a commit: the card asks for a re-test.
	gh.Mu.Lock()
	now := time.Now().UTC()
	is.State, is.StateReason, is.ClosedAt, is.UpdatedAt = "closed", "completed", &now, now
	gh.Timeline[1] = []map[string]any{{"event": "closed", "created_at": now, "commit_id": "abc123",
		"commit_url": "https://api.github.com/repos/basecamp/omarchy/commits/abc123"}}
	gh.Mu.Unlock()
	deliver("issues", "d3", testSecret, map[string]any{"action": "closed", "issue": is, "repository": repo})
	body := waitFor(card, "Please re-test")
	if !strings.Contains(body, `href="https://github.com/basecamp/omarchy/commit/abc123"`) {
		t.Error("the re-test prompt links the change")
	}
	if fx := get("/fixes"); !strings.Contains(fx, "Fixed: please re-test") || !strings.Contains(fx, `href="https://github.com/doesitomarchy/wecanfixeverything/issues/1"`) {
		t.Error("/fixes lists the fix")
	}
	var api struct {
		Capabilities []struct {
			ID  string  `json:"id"`
			Fix *apiFix `json:"fix"`
		} `json:"capabilities"`
	}
	json.Unmarshal([]byte(get("/api/v1/configs/macbookpro11-3-15-late-2013-a")), &api)
	found := false
	for _, cp := range api.Capabilities {
		if cp.ID == "audio.speakers" {
			found = cp.Fix != nil && cp.Fix.Issue == 1 && cp.Fix.State == "fixed" && cp.Fix.Retest
		}
	}
	if !found {
		t.Error("the API shows the fix")
	}

	// A second failure, given up on: the issue opened for it closes as not planned.
	loc = post("/admin/fixes/open", url.Values{"capability": {"audio.speakers"}, "config": {"macbookpro11-3-15-late-2013-a"}})
	if !strings.Contains(loc, "%232") {
		t.Fatalf("second open: %s", loc)
	}
	loc = post("/admin/unsupported", url.Values{"capability": {"audio.speakers"}, "config": {"macbookpro11-3-15-late-2013-a"}, "reason": {"no driver for the amp"}})
	if !strings.Contains(loc, "closed+as+not+planned") || gh.Issues[2].State != "closed" || gh.Issues[2].StateReason != "not_planned" ||
		!strings.Contains(gh.Comments[2][1], "no driver for the amp") {
		t.Fatalf("unsupported: %s %+v", loc, gh.Issues[2])
	}
	page = waitFor("/admin/fixes", "no driver for the amp")
	lift := regexp.MustCompile(`/admin/unsupported/(\d+)/clear`).FindStringSubmatch(page)
	if lift == nil {
		t.Fatal("no Lift button")
	}
	if loc := post(lift[0], url.Values{}); !strings.Contains(loc, "lifted") {
		t.Errorf("lift: %s", loc)
	}
}

// Without the secret, the webhook refuses everything.
func TestWebhookUnconfigured(t *testing.T) {
	ts := newTestServer(t)
	res, err := ts.Client().Post(ts.URL+"/hooks/github", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("no secret: %d", res.StatusCode)
	}
}
