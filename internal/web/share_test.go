package web

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

// PF-2: every part of each command is explained, in the variant shown.
func TestIdentifyExplained(t *testing.T) {
	for _, c := range identifyCommands {
		var parts []string
		for _, row := range c.Explain {
			parts = append(parts, row.Part)
			if row.ClipOnly {
				if !strings.Contains(c.Clip, strings.TrimSuffix(row.Part, "'…'")) || strings.Contains(c.Plain, strings.TrimSuffix(row.Part, "'…'")) {
					t.Errorf("%s: clipboard-only part %q", c.OS, row.Part)
				}
			} else if !strings.Contains(c.Plain, row.Part) {
				t.Errorf("%s: %q isn't in the command", c.OS, row.Part)
			}
			for _, l := range row.Links {
				if !strings.HasPrefix(l.URL, "https://") {
					t.Errorf("%s: link %q", c.OS, l.URL)
				}
			}
		}
		all := strings.Join(parts, " ")
		for _, tok := range strings.Fields(c.Clip) {
			if tok = strings.Trim(tok, "';"); tok != "" && !strings.Contains(all, tok) {
				t.Errorf("%s: %q isn't explained", c.OS, tok)
			}
		}
	}
}

func TestIdentifyShare(t *testing.T) {
	ts, srv, st, _ := adminServer(t, Options{AdminInsecure: true})
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
	share := func(form url.Values, header map[string]string) (int, string) {
		req, _ := http.NewRequest("POST", ts.URL+"/identify/share", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range header {
			req.Header.Set(k, v)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.Header.Get("Cache-Control") != "no-store" {
			t.Error("share replies may be cached")
		}
		return res.StatusCode, res.Header.Get("Location")
	}
	known := url.Values{"product": {"MacBookPro8,2"}, "board": {"Mac-94245A3940C91C80"}, "cpu": {"Intel(R) Core(TM) i7-2720QM CPU @ 2.20GHz"},
		"pci": {"8086:0126,1002:6760,ffff:0001"}, "modified": {"no"}, "release": {"15-early-2011"}, "consent": {"yes"}}

	// The form sits on the result, with the IDs and this Mac's releases; never pre-ticked.
	_, body := get("/identify?product=MacBookPro8,2&pci=8086:0126,1002:6760")
	for _, want := range []string{`action="/identify/share"`, `name="pci" value="8086:0126,1002:6760"`, `value="15-early-2011"`, `name="consent" value="yes" required>`} {
		if !strings.Contains(body, want) {
			t.Errorf("share form: missing %q", want)
		}
	}
	if regexp.MustCompile(`name="consent"[^>]*checked`).MatchString(body) {
		t.Error("the opt-in is pre-ticked")
	}
	if _, body := get("/identify?product=MacBookPro99,1"); !strings.Contains(body, `action="/identify/share"`) || strings.Contains(body, `name="release"`) {
		t.Error("unknown identifiers can be shared, without the release question")
	}
	if _, body := get("/identify"); strings.Contains(body, `action="/identify/share"`) {
		t.Error("nothing to share before identifying")
	}

	// Without the box ticked: back to the form, nothing stored.
	noConsent := url.Values{"product": {"MacBookPro8,2"}}
	if code, loc := share(noConsent, nil); code != http.StatusSeeOther || !strings.Contains(loc, "share=consent") {
		t.Errorf("no consent: %d %s", code, loc)
	}
	// Shared, and a repeat today gets the same thanks.
	for i := 0; i < 2; i++ {
		code, loc := share(known, map[string]string{"Origin": ts.URL})
		if code != http.StatusSeeOther || !strings.Contains(loc, "shared=1") || !strings.Contains(loc, "product=MacBookPro8%2C2") {
			t.Fatalf("share %d: %d %s", i, code, loc)
		}
		if _, body := get(loc); !strings.Contains(body, "Thanks, shared.") || strings.Contains(body, `action="/identify/share"`) {
			t.Errorf("share %d: no thanks", i)
		}
	}
	unknown := url.Values{"product": {"MacBookPro18,1"}, "consent": {"yes"}}
	if code, _ := share(unknown, nil); code != http.StatusSeeOther {
		t.Errorf("unknown identifier: %d", code)
	}
	if n, _ := st.SharesOn(context.Background(), time.Now().UTC().Format("2006-01-02")); n != 2 {
		t.Errorf("stored %d shares, want 2", n)
	}

	// Refusals.
	bad := func(name string, form url.Values, header map[string]string, want int) {
		t.Helper()
		if code, _ := share(form, header); code != want {
			t.Errorf("%s: %d, want %d", name, code, want)
		}
	}
	bad("other site", known, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden)
	bad("bad ID", url.Values{"product": {"MacBookPro8,2"}, "pci": {"<script>"}, "consent": {"yes"}}, nil, http.StatusBadRequest)
	bad("release of another Mac", url.Values{"product": {"MacBookPro8,2"}, "release": {"21-late-2009"}, "consent": {"yes"}}, nil, http.StatusBadRequest)
	bad("too big", url.Values{"product": {"MacBookPro8,2"}, "cpu": {strings.Repeat("x", 20<<10)}, "consent": {"yes"}}, nil, http.StatusRequestEntityTooLarge)

	// Ten an hour per IP; the 11th is refused, another IP is not.
	srv.shareLimit = newRateLimiter(SharesPerHourPerIP, time.Hour)
	for i := 0; i < SharesPerHourPerIP; i++ {
		f := url.Values{"product": {"MacBookPro8,2"}, "cpu": {"Intel(R) Core(TM) i7-2720QM CPU " + strings.Repeat("x", i)}, "consent": {"yes"}}
		if code, _ := share(f, map[string]string{"Cf-Connecting-IP": "192.0.2.1"}); code != http.StatusSeeOther {
			t.Fatalf("share %d: %d", i, code)
		}
	}
	bad("11th in an hour", unknown, map[string]string{"Cf-Connecting-IP": "192.0.2.1"}, http.StatusTooManyRequests)
	if code, _ := share(unknown, map[string]string{"Cf-Connecting-IP": "192.0.2.2"}); code != http.StatusSeeOther {
		t.Errorf("another IP: %d", code)
	}

	// /admin/shares: unknown identifiers first, unknown IDs marked, review hides a group.
	code, page := get("/admin/shares")
	if code != 200 || strings.Index(page, "MacBookPro18,1") > strings.Index(page, "MacBookPro8,2") {
		t.Fatalf("/admin/shares: %d, unknown identifier not first", code)
	}
	for _, want := range []string{`<code>ffff:0001</code> <span class="unk-tag">new</span>`, "Not modified: 1", "MacBook Pro (15-inch, Early 2011) (1)", "Mark reviewed"} {
		if !strings.Contains(page, want) {
			t.Errorf("/admin/shares: missing %q", want)
		}
	}
	csrf := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindStringSubmatch(page)
	if csrf == nil {
		t.Fatal("no form token")
	}
	res, err := client.PostForm(ts.URL+"/admin/shares/review", url.Values{"csrf": {csrf[1]}, "product": {"MacBookPro18,1"}})
	if err != nil || res.StatusCode != http.StatusSeeOther {
		t.Fatalf("review: %v %d", err, res.StatusCode)
	}
	res.Body.Close()
	if _, page := get("/admin/shares"); strings.Contains(page, "MacBookPro18,1") {
		t.Error("a reviewed group is still listed")
	}
	if _, page := get("/admin/shares?all=1"); !strings.Contains(page, "MacBookPro18,1") {
		t.Error("Show reviewed lists it")
	}
	if res, _ := client.PostForm(ts.URL+"/admin/shares/review", url.Values{"product": {"MacBookPro8,2"}}); res.StatusCode != http.StatusForbidden {
		t.Errorf("review without the form token: %d", res.StatusCode)
	}
}

func TestRateLimiter(t *testing.T) {
	l := newRateLimiter(2, time.Hour)
	t0 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if !l.allow("a", t0) || !l.allow("a", t0.Add(time.Minute)) || l.allow("a", t0.Add(2*time.Minute)) {
		t.Error("two an hour")
	}
	if !l.allow("b", t0) {
		t.Error("keys are separate")
	}
	if !l.allow("a", t0.Add(61*time.Minute)) {
		t.Error("the window slides")
	}
}
