package web

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/testdb"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// fakeAccess signs tokens the way Cloudflare Access does and serves its keys.
type fakeAccess struct {
	key   *rsa.PrivateKey
	certs *httptest.Server
}

func newFakeAccess(t *testing.T) *fakeAccess {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fa := &fakeAccess{key: k}
	fa.certs = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b64 := base64.RawURLEncoding.EncodeToString
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "k1", "kty": "RSA", "alg": "RS256", "n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes())}}})
	}))
	t.Cleanup(fa.certs.Close)
	return fa
}

func (fa *fakeAccess) token(t *testing.T, claims map[string]any) string {
	b64 := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	head := b64(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	body := b64(claims)
	sum := sha256.Sum256([]byte(head + "." + body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, fa.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return head + "." + body + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (fa *fakeAccess) claims(email string, aud string, exp time.Duration) map[string]any {
	return map[string]any{"aud": []string{aud}, "email": email, "iss": "https://team.cloudflareaccess.com",
		"exp": time.Now().Add(exp).Unix(), "nbf": time.Now().Add(-time.Minute).Unix()}
}

func adminServer(t *testing.T, opt Options) (*httptest.Server, *Server, *store.Store, *catalog.Catalog) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	st, c := testdb.Open(t)
	opt.Version = "test"
	srv, err := New(st, c, slog.New(slog.NewTextHandler(io.Discard, nil)), opt)
	if err != nil {
		t.Fatal(err)
	}
	go srv.Watch(ctx, 20*time.Millisecond)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, st, c
}

func TestAdminAccess(t *testing.T) {
	fa := newFakeAccess(t)
	ts, srv, st, _ := adminServer(t, Options{AccessTeam: "team", AccessAUD: "aud-admin"})
	srv.access.certsURL = fa.certs.URL
	st.AddMaintainer(context.Background(), "carl", "carl@example.com")
	get := func(token string) int {
		req, _ := http.NewRequest("GET", ts.URL+"/admin", nil)
		if token != "" {
			req.Header.Set("Cf-Access-Jwt-Assertion", token)
		}
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	wrongIssuer := fa.claims("carl@example.com", "aud-admin", time.Hour)
	wrongIssuer["iss"] = "https://evil.cloudflareaccess.com"
	tampered := fa.token(t, fa.claims("carl@example.com", "aud-admin", time.Hour))
	tampered = tampered[:len(tampered)-4] + "AAAA"
	for name, tc := range map[string]struct {
		token string
		code  int
	}{
		"no token":          {"", 403},
		"garbage":           {"a.b.c", 403},
		"bad signature":     {tampered, 403},
		"other application": {fa.token(t, fa.claims("carl@example.com", "aud-other", time.Hour)), 403},
		"wrong issuer":      {fa.token(t, wrongIssuer), 403},
		"expired":           {fa.token(t, fa.claims("carl@example.com", "aud-admin", -time.Minute)), 403},
		"not a maintainer":  {fa.token(t, fa.claims("someone@example.com", "aud-admin", time.Hour)), 403},
		"maintainer":        {fa.token(t, fa.claims("Carl@Example.com", "aud-admin", time.Hour)), 200},
	} {
		if got := get(tc.token); got != tc.code {
			t.Errorf("%s: %d, want %d", name, got, tc.code)
		}
	}
	// Without Access settings, /admin is closed.
	ts2, _, _, _ := adminServer(t, Options{})
	if res, _ := ts2.Client().Get(ts2.URL + "/admin"); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("unconfigured admin: %d", res.StatusCode)
	}
}

// The ambiguous-report flow in /admin: accept is refused until a
// configuration is picked; actions need the form token and record the handle.
func TestAdminReview(t *testing.T) {
	ts, _, st, c := adminServer(t, Options{AdminInsecure: true})
	ctx := context.Background()
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	f, _ := results.Parse([]byte(`schema: doesitomarchy/report/v1
identifier: MacBookPro8,2
hardware: { pci: ["1002:6741"] }
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4" }
items: { boot.install: { status: supported, method: observed } }
`))
	r, err := results.Validate(f, c, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := st.InsertResult(ctx, r, nil, results.SchemaV1, "test")
	d, _ := st.Result(ctx, id)

	res, _ := client.Get(ts.URL + "/admin")
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(b), d.Code) || res.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("queue: %d", res.StatusCode)
	}
	res, _ = client.Get(ts.URL + "/admin/report/" + d.Code)
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	page := string(b)
	m := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(page)
	if m == nil || !strings.Contains(page, "fits these configurations equally") || !strings.Contains(page, "macbookpro8-2-15-late-2011-a") {
		t.Fatalf("report page lacks the form token or the candidates")
	}
	csrf := m[1]
	// Enough to decide: the probe read against the catalog, how the
	// candidates differ, and what to ask the tester.
	for _, want := range []string{`state-tag st-pending`, "Which configuration?", "They differ in: <b>release, CPU", "AMD Radeon HD 6630M / 6750M (Whistler)",
		"machdep.cpu.brand_string", `data-reason-form`, "Reason (required)"} {
		if !strings.Contains(page, want) {
			t.Errorf("report page lacks %q", want)
		}
	}
	post := func(path string, form url.Values, origin string) (int, string) {
		req, _ := http.NewRequest("POST", ts.URL+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode, res.Header.Get("Location")
	}
	base := "/admin/report/" + d.Code
	if code, _ := post(base+"/accept", url.Values{}, ""); code != 403 {
		t.Errorf("a post without the form token: %d", code)
	}
	if code, _ := post(base+"/accept", url.Values{"csrf": {csrf}}, "https://evil.example"); code != 403 {
		t.Errorf("a post from another origin: %d", code)
	}
	code, loc := post(base+"/accept", url.Values{"csrf": {csrf}}, "")
	if code != http.StatusSeeOther || !strings.Contains(loc, "err=") || !strings.Contains(loc, "pick+one") {
		t.Fatalf("accepting an ambiguous report: %d %s", code, loc)
	}
	if code, loc := post(base+"/config", url.Values{"csrf": {csrf}, "config": {"macbookpro8-2-15-late-2011-a"}}, ts.URL); code != 303 || !strings.Contains(loc, "done=config") {
		t.Fatalf("set config: %d %s", code, loc)
	}
	if code, loc := post(base+"/accept", url.Values{"csrf": {csrf}}, ""); code != 303 || !strings.Contains(loc, "done=accept") {
		t.Fatalf("accept: %d %s", code, loc)
	}
	d, _ = st.Result(ctx, id)
	if d.State != store.Accepted || d.StateBy != "local" || d.ConfigID != "macbookpro8-2-15-late-2011-a" {
		t.Fatalf("after review: %s by %s, config %s", d.State, d.StateBy, d.ConfigID)
	}
	// The public site follows.
	eventually(t, ts, "/mac/MacBookPro8-2", func(b string) bool { return strings.Contains(b, "/report/"+d.Code) }, "the accepted report on the model page")
	if code, loc := post(base+"/retract", url.Values{"csrf": {csrf}}, ""); code != 303 || !strings.Contains(loc, "err=") {
		t.Errorf("retract without a reason: %d %s", code, loc)
	}
}
