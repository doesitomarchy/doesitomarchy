package builds

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
	"github.com/doesitomarchy/doesitomarchy/internal/testdb"
)

// fakeGitHub serves Omarchy's commits and the packaging repo's PKGBUILD
// history, newest first, and counts requests.
type fakeGitHub struct {
	mu       sync.Mutex
	commits  map[string][2]string // ref (short or full) → full SHA, date
	pkgbuild [][2]string          // history, newest first: pkgver, _commit
	refuse   bool                 // answer 401 to any token, as for a token limited to other repos
	requests int
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if f.refuse && r.Header.Get("Authorization") != "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	write := func(v any) { json.NewEncoder(w).Encode(v) }
	switch p := r.URL.Path; {
	case strings.HasPrefix(p, "/repos/"+OmarchyRepo+"/commits/"):
		c, ok := f.commits[strings.TrimPrefix(p, "/repos/"+OmarchyRepo+"/commits/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		write(map[string]any{"sha": c[0], "commit": map[string]any{"committer": map[string]any{"date": c[1]}}})
	case p == "/repos/"+PackagesRepo+"/commits":
		var out []map[string]string
		if r.URL.Query().Get("page") == "1" {
			for i := range f.pkgbuild {
				out = append(out, map[string]string{"sha": "pkgs" + string(rune('a'+i))})
			}
		}
		write(out)
	case p == "/repos/"+PackagesRepo+"/contents/"+pkgbuildPath:
		i := int(strings.TrimPrefix(r.URL.Query().Get("ref"), "pkgs")[0] - 'a')
		text := "pkgname='omarchy'\n_tag='v" + f.pkgbuild[i][0] + "'\n_commit='" + f.pkgbuild[i][1] + "'\npkgver=" + f.pkgbuild[i][0] + "\npkgrel=1\n"
		write(map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(text))})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeGitHub) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func newFake(t *testing.T) (*fakeGitHub, *Resolver, *store.Store) {
	t.Helper()
	f := &fakeGitHub{commits: map[string][2]string{
		"a85e29a": {"a85e29a" + strings.Repeat("0", 33), "2026-10-01T10:00:00Z"}, // edge r6713
		"1a2b3c4": {"1a2b3c4" + strings.Repeat("0", 33), "2026-10-04T09:00:00Z"}, // a dev checkout
		"c668141": {"c668141" + strings.Repeat("0", 33), "2026-09-15T05:34:12Z"}, // stable 4.0.4
		"bbbbbbb": {"bbbbbbb" + strings.Repeat("0", 33), "2026-09-10T00:00:00Z"}, // 4.0.4rc2
		"ccccccc": {"ccccccc" + strings.Repeat("0", 33), "2026-08-30T00:00:00Z"}, // 4.0.3
	}, pkgbuild: [][2]string{{"4.0.4", "c668141"}, {"4.0.4rc2", "bbbbbbb"}, {"4.0.3", "ccccccc"}}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	st, err := store.Open(context.Background(), testdb.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := New(st, "", nil)
	r.Base = srv.URL
	return f, r, st
}

func ver(t *testing.T, s string) status.Version {
	t.Helper()
	v, err := status.ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestResolve(t *testing.T) {
	ctx := context.Background()
	f, r, _ := newFake(t)

	// Edge: the version names the commit. Asked again, the cache answers.
	commit, at, err := r.Resolve(ctx, ver(t, "4.0.0.r6713.ga85e29a"), status.Edge, "")
	if err != nil || !strings.HasPrefix(commit, "a85e29a") || at != "2026-10-01T10:00:00Z" {
		t.Fatalf("edge: %s %s %v", commit, at, err)
	}
	n := f.count()
	if _, _, err := r.Resolve(ctx, ver(t, "4.0.0.r6713.ga85e29a"), status.Edge, ""); err != nil || f.count() != n {
		t.Errorf("edge again: %v, %d more requests", err, f.count()-n)
	}

	// Dev: the reported revision, not the version's hash.
	if commit, _, err := r.Resolve(ctx, ver(t, "4.0.0.r6713.ga85e29a"), status.Dev, "1A2B3C4"); err != nil || !strings.HasPrefix(commit, "1a2b3c4") {
		t.Errorf("dev: %s %v", commit, err)
	}

	// A release: found in the packaging history; the releases passed on the
	// way are remembered, so 4.0.4 then costs one request (its date).
	if commit, at, err := r.Resolve(ctx, ver(t, "4.0.3"), status.Stable, ""); err != nil || !strings.HasPrefix(commit, "ccccccc") || at != "2026-08-30T00:00:00Z" {
		t.Fatalf("4.0.3: %s %s %v", commit, at, err)
	}
	n = f.count()
	if commit, _, err := r.Resolve(ctx, ver(t, "v4.0.4-1"), status.Stable, ""); err != nil || !strings.HasPrefix(commit, "c668141") || f.count() != n+1 {
		t.Errorf("4.0.4 after 4.0.3: %s %v, %d requests", commit, err, f.count()-n)
	}
	if _, at, err := r.Resolve(ctx, ver(t, "4.0.4rc2"), status.RC, ""); err != nil || at != "2026-09-10T00:00:00Z" {
		t.Errorf("rc2: %s %v", at, err)
	}

	// Unknown: an error, remembered for an hour so GitHub isn't asked again.
	if _, _, err := r.Resolve(ctx, ver(t, "4.0.0.r9999.gdeadbee"), status.Edge, ""); err == nil {
		t.Error("an unknown commit resolved")
	}
	n = f.count()
	if _, _, err := r.Resolve(ctx, ver(t, "4.0.0.r9999.gdeadbee"), status.Edge, ""); err == nil || f.count() != n {
		t.Errorf("unknown again: %v, %d requests", err, f.count()-n)
	}
	if _, _, err := r.Resolve(ctx, ver(t, "9.9.9"), status.Stable, ""); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown release: %v", err)
	}
}

// A token GitHub refuses for public repos (one limited to other repos) is
// dropped rather than failing the lookup.
func TestResolveWithoutToken(t *testing.T) {
	f, r, _ := newFake(t)
	f.refuse, r.Token = true, "limited"
	if _, _, err := r.Resolve(context.Background(), ver(t, "4.0.0.r6713.ga85e29a"), status.Edge, ""); err != nil {
		t.Errorf("refused token: %v", err)
	}
}

// A report whose build isn't known gets an unknown_build flag; once the
// background lookup finds it, the result gets its date and the flag closes.
func TestFillAndRetry(t *testing.T) {
	ctx := context.Background()
	f, r, st := newFake(t)
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	file, err := results.Parse([]byte(`schema: doesitomarchy/report/v1
config: macbookair5-2-mid-2012-a
tested_at: 2026-10-05T12:00:00Z
omarchy: { version: "4.0.0.r6800.g1a2b3c4", channel: dev, revision: "7777777" }
items:
  audio.speakers: { status: supported, method: observed }
`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := results.Validate(file, c, time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	r.Fill(ctx, res) // the dev commit 7777777 isn't on GitHub (never pushed)
	if res.BuiltAt != "" || len(res.Flags) != 1 || res.Flags[0].Kind != results.FlagUnknownBuild {
		t.Fatalf("unknown build: %q %+v", res.BuiltAt, res.Flags)
	}
	id, err := st.InsertResult(ctx, res, nil, results.SchemaV1, "test")
	if err != nil {
		t.Fatal(err)
	}
	// It's pushed later; the next retry (after the hour's wait) finds it.
	f.mu.Lock()
	f.commits["7777777"] = [2]string{"7777777" + strings.Repeat("0", 33), "2026-10-05T08:00:00Z"}
	f.mu.Unlock()
	if n, _ := r.Retry(ctx); n != 0 {
		t.Errorf("retried within the hour: %d", n)
	}
	if err := st.PutBuild(ctx, store.Build{Key: "commit:7777777", Error: "not found", CheckedAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if n, err := r.Retry(ctx); err != nil || n != 1 {
		t.Fatalf("retry: %d %v", n, err)
	}
	d, err := st.Result(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.OmarchyBuiltAt != "2026-10-05T08:00:00Z" || d.Channel != status.Dev || d.Omarchy != "4.0.0.r6800.g1a2b3c4" {
		t.Errorf("after retry: built %q, %s %s", d.OmarchyBuiltAt, d.Omarchy, d.Channel)
	}
	for _, fl := range d.Flags {
		if fl.Kind == results.FlagUnknownBuild && fl.ResolvedAt == "" {
			t.Error("unknown_build still open")
		}
	}
}

// A failure on a newer build than the current pass, in the same view, is
// flagged; an older build's failure, or another view's, is not.
func TestRegressions(t *testing.T) {
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	const cfg = "macbookair5-2-mid-2012-a"
	pass := status.Item{Capability: "audio.speakers", Verdict: status.Supported, Omarchy: ver(t, "4.0.4"), Channel: status.Stable,
		BuiltAt: "2026-09-15T05:34:12Z", TestedAt: "2026-09-20T00:00:00Z", ResultID: 1}
	ru := &store.Rollup{Items: map[string][]status.Item{cfg: {pass}}, StableResults: map[string]int{cfg: 1}, Results: map[string]int{cfg: 1}}
	report := func(version, channel, built string) *results.Result {
		v := ver(t, version)
		return &results.Result{ConfigID: cfg, Omarchy: v, OmarchyRaw: v.String(), Channel: channel, BuiltAt: built, TestedAt: "2026-10-10T00:00:00Z",
			Items: []results.Item{{Capability: "audio.speakers", Status: "failed", Applicable: true}}}
	}
	if fl := Regressions(c, ru, report("4.0.5", status.Stable, "2026-10-01T00:00:00Z")); len(fl) != 1 || !strings.Contains(fl[0].Detail, "passed on Omarchy 4.0.4 (stable, built 2026-09-15)") {
		t.Errorf("newer stable failure: %+v", fl)
	}
	if fl := Regressions(c, ru, report("4.0.3", status.Stable, "2026-08-30T00:00:00Z")); len(fl) != 0 {
		t.Errorf("older build's failure: %+v", fl)
	}
	// An edge failure counts toward the newest-build view, where the stable
	// pass is also the current result: flagged too.
	if fl := Regressions(c, ru, report("4.0.0.r6800.g1a2b3c4", status.Edge, "2026-10-04T00:00:00Z")); len(fl) != 1 {
		t.Errorf("newer edge failure: %+v", fl)
	}
}
