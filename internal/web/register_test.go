package web

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// regClient posts forms and reads pages without following redirects.
type regClient struct {
	t    *testing.T
	base string
	c    *http.Client
}

func (rc regClient) do(method, path string, form url.Values, origin string) (int, string, http.Header) {
	rc.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, rc.base+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := rc.c.Do(req)
	if err != nil {
		rc.t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res.StatusCode, string(b), res.Header
}

func registration() url.Values {
	return url.Values{"name": {"Mac Tester"}, "id": {"mac-tester"}, "repo": {"https://github.com/example/mac-tester"},
		"email": {"dev@example.com"}, "description": {"Checks Wi-Fi, sleep and audio."}, "rules": {"yes"}}
}

// The whole flow: apply, a maintainer approves, the key is shown once and
// submits a report; a new key link stops it.
func TestRegisterFlow(t *testing.T) {
	ts, _, _, _ := adminServer(t, Options{AdminInsecure: true})
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	rc := regClient{t, ts.URL, client}

	code, page, _ := rc.do("GET", "/api/register", nil, "")
	if code != 200 || !strings.Contains(page, `name="website"`) || !strings.Contains(page, "/api#sources") {
		t.Fatalf("form: %d", code)
	}
	// Mistakes come back with the form filled in.
	bad := registration()
	bad.Set("repo", "http://example.com")
	bad.Del("rules")
	if code, page, _ := rc.do("POST", "/api/register", bad, ""); code != 422 || !strings.Contains(page, "https://") || !strings.Contains(page, "confirm your tool shows the consent notice") || !strings.Contains(page, `value="Mac Tester"`) {
		t.Errorf("invalid: %d", code)
	}
	if code, _, _ := rc.do("POST", "/api/register", registration(), "https://evil.example"); code != 400 {
		t.Errorf("cross-site post: %d", code)
	}
	code, _, h := rc.do("POST", "/api/register", registration(), ts.URL)
	loc := h.Get("Location")
	if code != 303 || !regexp.MustCompile(`^/api/register/[0-9a-f]{64}\?new=1$`).MatchString(loc) {
		t.Fatalf("submit: %d %q", code, loc)
	}
	status := strings.TrimSuffix(loc, "?new=1")
	code, page, h = rc.do("GET", loc, nil, "")
	if code != 200 || !strings.Contains(page, "<h1>Mac Tester API key request</h1>") || !strings.Contains(page, "This link is only shown once") || !strings.Contains(page, "Pending review") ||
		h.Get("Referrer-Policy") != "same-origin" || h.Get("Cache-Control") != "no-store" || strings.Contains(page, `rel="canonical"`) {
		t.Fatalf("status page: %d %v", code, h)
	}
	if code, _, _ := rc.do("POST", status+"/key", url.Values{}, ""); code != 200 {
		t.Errorf("reveal before approval: %d", code)
	}

	// The maintainer sees it, with a count in the bar, and approves it.
	code, page, _ = rc.do("GET", "/admin/sources", nil, "")
	if code != 200 || !strings.Contains(page, `<span class="admin-count">(1)</span>`) || !strings.Contains(page, "dev@example.com") {
		t.Fatalf("admin: %d", code)
	}
	csrf := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(page)[1]
	reqID := regexp.MustCompile(`/admin/sources/request/(\d+)/approve`).FindStringSubmatch(page)[1]
	approve := url.Values{"csrf": {csrf}, "id": {"mactester"}, "name": {"Mac Tester"}, "repo": {"https://github.com/example/mac-tester"}}
	if code, _, h := rc.do("POST", "/admin/sources/request/"+reqID+"/approve", approve, ""); code != 303 || !strings.Contains(h.Get("Location"), "done=") {
		t.Fatalf("approve: %d %v", code, h)
	}

	// The applicant's link now offers the key, once.
	if _, page, _ := rc.do("GET", status, nil, ""); !strings.Contains(page, "Show my key") || !strings.Contains(page, "mactester") {
		t.Fatal("no reveal button after approval")
	}
	code, page, _ = rc.do("POST", status+"/key", url.Values{}, ts.URL)
	key := regexp.MustCompile(`doi_[0-9a-f]{32}`).FindString(page)
	if code != 200 || key == "" {
		t.Fatalf("reveal: %d", code)
	}
	if _, page, _ := rc.do("POST", status+"/key", url.Values{}, ts.URL); regexp.MustCompile(`doi_[0-9a-f]{32}`).MatchString(page) || !strings.Contains(page, "already shown") {
		t.Error("the key was shown twice")
	}
	sub, _, _ := apiCall(t, client, "POST", ts.URL+"/api/v1/reports", key, fixtureWithoutConfig())
	if sub != 201 {
		t.Errorf("submit with the new key: %d", sub)
	}

	// A new key link stops the key and is shown to the maintainer once.
	code, page, _ = rc.do("POST", "/admin/sources/mactester/keylink", url.Values{"csrf": {csrf}}, "")
	link := regexp.MustCompile(`/api/register/[0-9a-f]{64}`).FindString(page)
	if code != 200 || link == "" || !strings.Contains(page, "dev@example.com") {
		t.Fatalf("key link: %d", code)
	}
	if sub, _, _ := apiCall(t, client, "POST", ts.URL+"/api/v1/reports", key, fixtureWithoutConfig()); sub != 401 {
		t.Errorf("old key after a new link: %d", sub)
	}
	if _, page, _ := rc.do("GET", link, nil, ""); !strings.Contains(page, "A new API key for mactester") || !strings.Contains(page, "Show my key") {
		t.Error("key link page")
	}
	if code, _, _ := rc.do("GET", "/api/register/"+strings.Repeat("0", 64), nil, ""); code != 404 {
		t.Errorf("unknown link: %d", code)
	}
}

func TestRegisterDeclineHoneypotLimit(t *testing.T) {
	old := RequestsPerDayPerIP
	RequestsPerDayPerIP = 2
	t.Cleanup(func() { RequestsPerDayPerIP = old })
	ts, _, st, _ := adminServer(t, Options{AdminInsecure: true})
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	rc := regClient{t, ts.URL, client}

	// A bot that fills the honeypot is thanked, and nothing is kept.
	spam := registration()
	spam.Set("website", "https://spam.example")
	if code, page, _ := rc.do("POST", "/api/register", spam, ""); code != 200 || !strings.Contains(page, "Request received") {
		t.Errorf("honeypot: %d", code)
	}
	if n, _ := st.PendingRequests(context.Background()); n != 0 {
		t.Errorf("the honeypot stored %d requests", n)
	}
	_, _, h := rc.do("POST", "/api/register", registration(), "")
	status := strings.TrimSuffix(h.Get("Location"), "?new=1")
	other := registration()
	other.Set("id", "other-tool")
	rc.do("POST", "/api/register", other, "")
	other.Set("id", "third-tool")
	if code, _, _ := rc.do("POST", "/api/register", other, ""); code != 429 {
		t.Errorf("over the daily limit: %d", code)
	}

	_, page, _ := rc.do("GET", "/admin/sources", nil, "")
	csrf := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(page)[1]
	reqs, _ := st.SourceRequests(context.Background(), store.RequestPending)
	decline := "/admin/sources/request/" + strconv.FormatInt(reqs[0].ID, 10) + "/decline"
	if code, _, h := rc.do("POST", decline, url.Values{"csrf": {csrf}}, ""); code != 303 || !strings.Contains(h.Get("Location"), "err=") {
		t.Errorf("decline without a reason: %d", code)
	}
	rc.do("POST", decline, url.Values{"csrf": {csrf}, "reason": {"No source code at that link."}}, "")
	if _, page, _ := rc.do("GET", "/admin/sources", nil, ""); !strings.Contains(page, "Declined requests") || !strings.Contains(page, "No source code at that link.") {
		t.Error("/admin/sources doesn't list the declined request")
	}
	if _, page, _ := rc.do("GET", status, nil, ""); !strings.Contains(page, `st-rejected">Declined<`) || !strings.Contains(page, "No source code at that link.") {
		t.Error("the applicant doesn't see the reason")
	}
}
