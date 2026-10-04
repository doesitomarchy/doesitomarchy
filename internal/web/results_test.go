package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/testdb"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// liveServer is a server over a real database, with the result watcher running.
func liveServer(t *testing.T) (*httptest.Server, *store.Store, *catalog.Catalog) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, c := testdb.Open(t)
	srv, err := New(st, c, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Watch(ctx, 20*time.Millisecond)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st, c
}

func body(t *testing.T, ts *httptest.Server, path string) (int, string) {
	t.Helper()
	res, err := ts.Client().Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// eventually polls until the page satisfies ok (the watcher rebuilds asynchronously).
func eventually(t *testing.T, ts *httptest.Server, path string, ok func(string) bool, what string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, b := body(t, ts, path)
		if ok(b) {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: %s never happened", path, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The slice 7a done-when (PLAN §21.8): import (no change), accept (the site
// updates without a restart), retract (back to the previous state).
func TestResultLifecycleOnTheSite(t *testing.T) {
	ts, st, c := liveServer(t)
	ctx := context.Background()
	const page = "/mac/MacBookPro15-2"
	const card = `id="cfg-macbookpro15-2-13-2018-4tb3-a"`

	_, before := body(t, ts, page)
	_, home := body(t, ts, "/")
	if !strings.Contains(home, `<span class="n">0</span> / `) {
		t.Fatal("coverage should start at 0")
	}

	f, err := results.Parse(results.SyntheticFixture)
	if err != nil {
		t.Fatal(err)
	}
	r, err := results.Validate(f, c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.InsertResult(ctx, r, results.SyntheticFixture, results.SchemaV1, "test")
	if err != nil {
		t.Fatal(err)
	}
	d, err := st.Result(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	report := "/report/" + d.Code

	// Pending: nothing on the site changes; the result page doesn't exist.
	time.Sleep(100 * time.Millisecond)
	if _, now := body(t, ts, page); now != before {
		t.Fatal("a pending result changed the model page")
	}
	if code, _ := body(t, ts, report); code != http.StatusNotFound {
		t.Fatalf("pending report page: %d", code)
	}

	if err := st.SetResultState(ctx, id, store.Accepted, "", "test"); err != nil {
		t.Fatal(err)
	}
	b := eventually(t, ts, page, func(b string) bool { return strings.Contains(b, "Latest diagnostic report") }, "the accepted result showing")
	cardHTML := b[strings.Index(b, card):]
	cardHTML = cardHTML[:strings.Index(cardHTML, "</section>")]
	for _, want := range []string{
		`class="verdict partial"`, // the verdict
		"27/31 tested",            // counts
		"Blocked by: Graphics → External display output",    // the blocker
		"DisplayPort alt mode works on the left ports only", // evidence on the partial capability
		"Why it failed",               // failed capabilities explain themselves
		`href="` + report + `"`,       // and link to the report
		"tested 2026-09-30 18:05 UTC", // the test time, in UTC
	} {
		if !strings.Contains(cardHTML, want) {
			t.Errorf("model page card lacks %q", want)
		}
	}
	if _, other := body(t, ts, "/mac/MacBookPro15-2"); strings.Count(other, "Latest diagnostic report") != 1 {
		t.Error("only the tested config should show a result")
	}

	// Coverage: tested 1, verified 0 (it isn't fully passing), on the home page and the footer.
	_, home = body(t, ts, "/")
	if !strings.Contains(home, `<span class="n">1</span> / `) || !strings.Contains(home, `<span class="n">0</span> / `) {
		t.Error("home coverage should read 0 verified, 1 tested")
	}
	_, stats := body(t, ts, "/stats")
	if !strings.Contains(stats, "<b>1</b> accepted report") || !strings.Contains(stats, `href="`+report+`"`) {
		t.Error("/stats should list the accepted report")
	}
	_, search := body(t, ts, "/search?q=tested%3Ayes")
	if !strings.Contains(search, "MacBookPro15,2") {
		t.Error("tested:yes should find the tested Mac")
	}
	code, rp := body(t, ts, report)
	if code != http.StatusOK || !strings.Contains(rp, "Diagnostic Report") || !strings.Contains(rp, d.Code) {
		t.Errorf("report page: %d", code)
	}
	for _, leak := range []string{"C02XG0FDH7JY", "a4:83:e7", "tester@example.com"} {
		if strings.Contains(rp, leak) {
			t.Errorf("report page leaks %q", leak)
		}
	}
	if !strings.Contains(rp, "tested <b>2026-09-30 18:05 UTC</b>") || !strings.Contains(rp, "submitted <b>") {
		t.Error("the report shows when it was tested and when it was submitted, separately, in UTC")
	}
	if c, _ := body(t, ts, "/report/1"); c != http.StatusNotFound {
		t.Error("reports are found by code, never by number")
	}
	if _, sm := body(t, ts, "/sitemap.xml"); !strings.Contains(sm, report+"<") {
		t.Error("accepted reports belong in the sitemap")
	}

	// Retract: the site returns to its previous state; the result page says so.
	if err := st.SetResultState(ctx, id, store.Retracted, "synthetic fixture", "test"); err != nil {
		t.Fatal(err)
	}
	after := eventually(t, ts, page, func(b string) bool { return !strings.Contains(b, "Latest diagnostic report") }, "the retraction")
	if !strings.Contains(after, `class="verdict untested"`) || strings.Contains(after[strings.Index(after, card):], `class="verdict partial"`) {
		t.Error("after retraction the config should be untested again")
	}
	_, home = body(t, ts, "/")
	if strings.Contains(home, `<span class="n">1</span> / `) {
		t.Error("coverage should drop back to 0 after retraction")
	}
	if code, rp := body(t, ts, report); code != http.StatusOK || !strings.Contains(rp, "Retracted") {
		t.Errorf("a retracted report keeps its page, marked retracted: %d", code)
	}
}

func TestPurgeDebounced(t *testing.T) {
	var calls atomic.Int32
	cf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || !strings.Contains(r.URL.Path, "/zones/zone1/") {
			t.Errorf("bad purge request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		calls.Add(1)
		w.Write([]byte(`{"success":true}`))
	}))
	defer cf.Close()
	p := newPurger("zone1", "tok", slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.endpoint, p.delay = cf.URL+"/zones/%s/purge_cache", 50*time.Millisecond
	done := make(chan struct{})
	p.done = done
	for i := 0; i < 5; i++ { // a moderation session: five changes in a row
		p.schedule()
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("purge never ran")
	}
	time.Sleep(100 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("want one purge for a burst of changes, got %d", n)
	}
	// Without a token, nothing is scheduled.
	(&purger{}).schedule()
	newPurger("", "", nil).schedule()
}

// Enter on a query with exactly one match goes straight to the Mac.
func TestSearchSingleMatchRedirects(t *testing.T) {
	ts, _, _ := liveServer(t)
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	get := func(path string, htmx bool) *http.Response {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := get("/search?q=macbookpro15%2C1", false); res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/mac/MacBookPro15-1" {
		t.Errorf("one match: %d → %q", res.StatusCode, res.Header.Get("Location"))
	}
	// MacBookPro15,2 has two configurations; an order number names one of them.
	if res := get("/search?q=MV962LL%2FA", false); res.StatusCode != http.StatusFound ||
		res.Header.Get("Location") != "/mac/MacBookPro15-2#cfg-macbookpro15-2-13-2019-4tb3-a" {
		t.Errorf("an order number pinpoints a config: %d → %q", res.StatusCode, res.Header.Get("Location"))
	}
	// MacBookPro7,1 has one configuration: just the Mac's page.
	if res := get("/search?q=MC374LL%2FA", false); res.Header.Get("Location") != "/mac/MacBookPro7-1" {
		t.Errorf("single-config Mac: %q", res.Header.Get("Location"))
	}
	for _, path := range []string{"/search?q=mbp+2011", "/search?q=", "/search?q=macbookpro15%2C1&sort=recent"} {
		if res := get(path, false); res.StatusCode != http.StatusOK {
			t.Errorf("%s: several matches (or a sort) stay on the results page, got %d", path, res.StatusCode)
		}
	}
	if res := get("/search?q=macbookpro15%2C1", true); res.StatusCode != http.StatusOK {
		t.Errorf("live search (HTMX) never redirects, got %d", res.StatusCode)
	}
}
