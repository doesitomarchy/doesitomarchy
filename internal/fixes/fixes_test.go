package fixes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/fixes/fakegithub"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "f.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestStateOf(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	recent, old := now.Add(-48*time.Hour).Format(time.RFC3339), now.Add(-61*24*time.Hour).Format(time.RFC3339)
	for want, f := range map[State]store.Fix{
		Open:       {Open: true, LastActivity: recent},
		Claimed:    {Open: true, Assignee: "carl", LastActivity: recent},
		Stale:      {Open: true, Assignee: "carl", LastActivity: old},
		Proposed:   {Open: true, Assignee: "carl", Proposed: true, LastActivity: old},
		Fixed:      {Open: false, StateReason: "completed"},
		NotPlanned: {Open: false, StateReason: "not_planned"},
		Duplicate:  {Open: false, StateReason: "duplicate"},
	} {
		if got := StateOf(f, now); got != want {
			t.Errorf("%+v: %s, want %s", f, got, want)
		}
	}
	// Closed before GitHub had reasons: completed.
	if got := StateOf(store.Fix{Open: false}, now); got != Fixed {
		t.Errorf("closed without a reason: %s", got)
	}
	// A duplicate is never the fix shown for a criterion.
	dup := store.Fix{Issue: 2, Capability: "audio.speakers", Component: "audio/x", StateReason: "duplicate"}
	if _, _, ok := Best([]store.Fix{dup}, "audio.speakers", "cfg", []string{"audio/x"}, now); ok {
		t.Error("Best picked a duplicate")
	}
}

func TestNextChange(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	all := []store.Fix{
		{Open: true, LastActivity: ago(time.Hour)},                                          // unclaimed: never changes by time
		{Open: true, Assignee: "a", LastActivity: ago(10 * 24 * time.Hour)},                 // stale in 50 days
		{Open: true, Assignee: "b", LastActivity: ago(59 * 24 * time.Hour)},                 // stale in 1 day: first
		{Open: true, Assignee: "c", LastActivity: ago(61 * 24 * time.Hour)},                 // already stale
		{Open: true, Assignee: "d", Proposed: true, LastActivity: ago(59 * 24 * time.Hour)}, // proposed: doesn't go stale
		{Open: false, Assignee: "e", LastActivity: ago(59 * 24 * time.Hour)},                // closed
	}
	if got, want := NextChange(all, now), now.Add(24*time.Hour); !got.Equal(want) {
		t.Errorf("NextChange %v, want %v", got, want)
	}
	if got := NextChange(all[:1], now); !got.IsZero() {
		t.Errorf("nothing changes by time: %v", got)
	}
}

func TestFromIssue(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	base := Issue{Number: 7, Title: "Speakers on Cirrus Logic CS8409", HTMLURL: "https://github.com/x/y/issues/7", State: "open",
		UpdatedAt: t0, Labels: []Label{{"criterion:audio.speakers"}, {"component:audio/cirrus-cs8409"}}}
	f, ok := FromIssue(base, nil, "x/y")
	if !ok || f.Capability != "audio.speakers" || f.Component != "audio/cirrus-cs8409" || f.LastActivity != "2026-10-01T09:00:00Z" {
		t.Fatalf("%+v %v", f, ok)
	}
	if _, ok := FromIssue(Issue{Number: 8, Labels: []Label{{"bug"}}}, nil, "x/y"); ok {
		t.Error("an issue without a criterion label isn't a fix")
	}
	if _, ok := FromIssue(Issue{Number: 9, Labels: []Label{{"criterion:audio.speakers"}}}, nil, "x/y"); ok {
		t.Error("a fix needs a component or a configuration")
	}

	// An open pull request in another repo that mentions the issue: proposed, and activity.
	pr := Event{Event: "cross-referenced", CreatedAt: t0.Add(time.Hour), Source: &Source{Issue: &SourceIssue{
		HTMLURL: "https://github.com/basecamp/omarchy/pull/99", State: "open", PullRequest: &PullRequest{}}}}
	f, _ = FromIssue(base, []Event{pr}, "x/y")
	if !f.Proposed || f.LastActivity != "2026-10-01T10:00:00Z" {
		t.Errorf("cross-referenced PR: %+v", f)
	}

	// Closed as completed by a commit: fixed, linked to the commit's page.
	closed := base
	closed.State, closed.StateReason = "closed", "completed"
	at := t0.Add(2 * time.Hour)
	closed.ClosedAt = &at
	f, _ = FromIssue(closed, []Event{{Event: "closed", CreatedAt: at, CommitID: "abc123",
		CommitURL: "https://api.github.com/repos/basecamp/omarchy/commits/abc123"}}, "x/y")
	if f.Open || f.FixLink != "https://github.com/basecamp/omarchy/commit/abc123" || f.Proposed {
		t.Errorf("closed by commit: %+v", f)
	}
	// Closed after a merged pull request, without a closing commit: the pull request.
	m := at
	merged := Event{Event: "cross-referenced", CreatedAt: t0.Add(time.Hour), Source: &Source{Issue: &SourceIssue{
		HTMLURL: "https://github.com/basecamp/omarchy/pull/99", State: "closed", PullRequest: &PullRequest{MergedAt: &m}}}}
	f, _ = FromIssue(closed, []Event{merged, {Event: "closed", CreatedAt: at}}, "x/y")
	if f.FixLink != "https://github.com/basecamp/omarchy/pull/99" {
		t.Errorf("closed after merged PR: %+v", f)
	}
	// Closed as a duplicate: nothing landed here, so no fix link.
	dup := closed
	dup.StateReason = "duplicate"
	if f, _ = FromIssue(dup, []Event{merged}, "x/y"); f.FixLink != "" {
		t.Errorf("duplicate: %+v", f)
	}
}

func TestOpenSyncAndGiveUp(t *testing.T) {
	ctx := context.Background()
	gh := fakegithub.New(t, DefaultRepo)
	st := openStore(t)
	c := NewClient(DefaultRepo, fakegithub.Token)
	c.Base = gh.URL
	affected := []Affected{
		{ConfigID: "macbookpro16-1-16-2019-a", Name: "MacBook Pro (16-inch, 2019)", URL: "https://doesitomarchy.com/mac/MacBookPro16-1#cfg-a",
			Failed: true, Evidence: "cs8409: no speaker amp", Report: "https://doesitomarchy.com/report/0123456789"},
		{ConfigID: "macbookpro15-1-15-2018-a", Name: "MacBook Pro (15-inch, 2018)", URL: "https://doesitomarchy.com/mac/MacBookPro15-1#cfg-a"},
	}
	f, err := OpenIssue(ctx, c, st, "audio.speakers", "Built-in speakers", Scope{Component: "audio/cirrus-cs8409", Name: "Cirrus Logic CS8409"}, affected, "crh")
	if err != nil {
		t.Fatal(err)
	}
	if f.Issue != 1 || f.OpenedBy != "crh" || !gh.Labels["criterion:audio.speakers"] || !gh.Labels["component:audio/cirrus-cs8409"] {
		t.Fatalf("opened: %+v, labels %v", f, gh.Labels)
	}
	body := gh.Comments[1][0]
	for _, want := range []string{"Where it failed", "MacBook Pro (16-inch, 2019)", "cs8409: no speaker amp", "Also covered", "MacBook Pro (15-inch, 2018)"} {
		if !strings.Contains(body, want) {
			t.Errorf("issue body: missing %q", want)
		}
	}
	if gh.Issues[1].Title != "Built-in speakers on Cirrus Logic CS8409" {
		t.Errorf("title %q", gh.Issues[1].Title)
	}
	// Opening a second fix reuses the labels (GitHub answers 422 for existing ones).
	if _, err := OpenIssue(ctx, c, st, "audio.speakers", "Built-in speakers", Scope{Config: "macbookpro16-1-16-2019-a", Name: "MacBook Pro (16-inch, 2019)"}, nil, "crh"); err != nil {
		t.Fatal(err)
	}
	// The same fix again (a double click, another maintainer, the CLI): refused, naming the open issue.
	_, err = OpenIssue(ctx, c, st, "audio.speakers", "Built-in speakers", Scope{Component: "audio/cirrus-cs8409", Name: "Cirrus Logic CS8409"}, affected, "crh")
	var already *AlreadyOpenError
	if !errors.As(err, &already) || already.Issue.Number != 1 || len(gh.Issues) != 2 {
		t.Fatalf("second open of the same fix: %v, %d issues", err, len(gh.Issues))
	}

	// Someone claims issue 1 on GitHub; an unrelated issue appears; the poll catches both.
	gh.Mu.Lock()
	gh.Issues[1].Assignee = &fakegithub.User{Login: "helper"}
	gh.Issues[1].UpdatedAt = time.Now().UTC()
	gh.Issues[3] = &fakegithub.Issue{Number: 3, Title: "Question", State: "open", UpdatedAt: time.Now().UTC()}
	gh.Mu.Unlock()
	sy := &Syncer{Client: c, Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, err := sy.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := st.Fixes(ctx)
	if len(all) != 2 || all[1].Assignee != "helper" || StateOf(all[1], time.Now()) != Claimed {
		t.Fatalf("after sync: %+v", all)
	}

	// Giving up: a comment with the reason, the unsupported label, closed as not planned.
	if err := GiveUp(ctx, c, 1, "needs a firmware Apple never released", "crh"); err != nil {
		t.Fatal(err)
	}
	if _, err := sy.SyncIssue(ctx, 1); err != nil {
		t.Fatal(err)
	}
	all, _ = st.Fixes(ctx)
	if StateOf(all[1], time.Now()) != NotPlanned || !strings.Contains(gh.Comments[1][1], "needs a firmware") {
		t.Errorf("after giving up: %+v %v", all[1], gh.Comments[1])
	}

	// Relabelled so it's no longer a fix: forgotten.
	gh.Mu.Lock()
	gh.Issues[2].Labels = []fakegithub.Label{{Name: "question"}}
	gh.Mu.Unlock()
	if _, err := sy.SyncIssue(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if all, _ = st.Fixes(ctx); len(all) != 1 {
		t.Errorf("relabelled issue kept: %+v", all)
	}

	// A bad token is reported, not ignored.
	bad := NewClient(DefaultRepo, "nope")
	bad.Base = gh.URL
	if _, err := bad.Issue(ctx, 1); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("bad token: %v", err)
	}
}

func TestWebhook(t *testing.T) {
	body := []byte(`{"action":"assigned","issue":{"number":4,"title":"x","state":"open","labels":[]},"repository":{"full_name":"doesitomarchy/wecanfixeverything"}}`)
	m := hmac.New(sha256.New, []byte("s3cret"))
	m.Write(body)
	sig := "sha256=" + hex.EncodeToString(m.Sum(nil))
	if !ValidSignature("s3cret", body, sig) || ValidSignature("other", body, sig) || ValidSignature("s3cret", body, "sha1=00") ||
		ValidSignature("", body, sig) || ValidSignature("s3cret", append(body, ' '), sig) {
		t.Error("signature checks")
	}
	if is, action, ok := WebhookIssue("issues", body, DefaultRepo); !ok || is.Number != 4 || action != "assigned" || Gone(action) {
		t.Errorf("issues event: %v %q %v", is, action, ok)
	}
	if _, _, ok := WebhookIssue("issue_comment", body, DefaultRepo); !ok {
		t.Error("issue_comment event")
	}
	if _, _, ok := WebhookIssue("issues", body, "someone/else"); ok {
		t.Error("another repo's delivery")
	}
	if _, _, ok := WebhookIssue("push", body, DefaultRepo); ok {
		t.Error("push event")
	}
	pr := []byte(`{"issue":{"number":5,"pull_request":{}},"repository":{"full_name":"doesitomarchy/wecanfixeverything"}}`)
	if _, _, ok := WebhookIssue("issue_comment", pr, DefaultRepo); ok {
		t.Error("a comment on a pull request")
	}
	for _, a := range []string{"deleted", "transferred"} {
		if !Gone(a) {
			t.Errorf("%s: the issue left the repo", a)
		}
	}
}

// A copy of an issue older than the stored one never overwrites it: two
// webhook syncs can finish in either order.
func TestUpsertIgnoresOlder(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	newer := store.Fix{Issue: 1, Capability: "audio.speakers", Component: "audio/x", Title: "t", URL: "u",
		Open: false, StateReason: "completed", LastActivity: "2026-10-03T12:00:05Z"}
	older := newer
	older.Open, older.StateReason, older.Assignee, older.LastActivity = true, "", "helper", "2026-10-03T12:00:01Z"
	if _, err := st.UpsertFix(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if changed, err := st.UpsertFix(ctx, older); err != nil || changed {
		t.Fatalf("older copy: changed %v, %v", changed, err)
	}
	all, _ := st.Fixes(ctx)
	if all[0].Open || all[0].Assignee != "" {
		t.Errorf("the older copy overwrote the newer: %+v", all[0])
	}
}

// The catch-up poll survives a failing issue, notices linked pull requests
// that don't touch the issue, and forgets issues that left the repo.
func TestSyncAllRobust(t *testing.T) {
	ctx := context.Background()
	gh := fakegithub.New(t, DefaultRepo)
	st := openStore(t)
	c := NewClient(DefaultRepo, fakegithub.Token)
	c.Base = gh.URL
	sy := &Syncer{Client: c, Store: st}
	labels := func(comp string) []fakegithub.Label {
		return []fakegithub.Label{{Name: "criterion:audio.speakers"}, {Name: "component:" + comp}}
	}
	twoHoursAgo := time.Now().UTC().Add(-2 * time.Hour)
	gh.Mu.Lock()
	for n := 1; n <= 3; n++ {
		gh.Issues[n] = &fakegithub.Issue{Number: n, Title: "fix", State: "open", UpdatedAt: twoHoursAgo.Add(time.Duration(n) * time.Minute),
			Labels: labels(fmt.Sprintf("audio/c%d", n)), RepoURL: gh.URL + "/repos/" + DefaultRepo}
	}
	gh.Fail[2] = 502 // GitHub has a bad moment for issue 2
	gh.Mu.Unlock()

	n, err := sy.SyncAll(ctx)
	if err == nil || !strings.Contains(err.Error(), "#2") || n != 2 {
		t.Fatalf("first sync: %d updated, %v; want 2 and an error naming #2", n, err)
	}
	if v, _ := st.Setting(ctx, "fixes_synced_at"); v != gh.Issues[2].UpdatedAt.Format(time.RFC3339) {
		t.Errorf("the next sync should start at issue 2's update, not %s", v)
	}
	gh.Mu.Lock()
	delete(gh.Fail, 2)
	gh.Mu.Unlock()
	if n, err := sy.SyncAll(ctx); err != nil || n != 1 {
		t.Fatalf("retry: %d updated, %v", n, err)
	}

	// A pull request in another repo now links issue 3. The issue itself
	// doesn't change, so only re-reading open fixes notices it.
	gh.Mu.Lock()
	gh.Timeline[3] = []map[string]any{{"event": "cross-referenced", "created_at": twoHoursAgo,
		"source": map[string]any{"issue": map[string]any{"html_url": "https://github.com/basecamp/omarchy/pull/7", "state": "open", "pull_request": map[string]any{}}}}}
	gh.Mu.Unlock()
	if _, err := sy.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := st.Fixes(ctx)
	if len(all) != 3 || all[0].Issue != 3 || !all[0].Proposed {
		t.Fatalf("linked pull request not noticed: %+v", all)
	}

	// Issue 3 moves to another repo; issue 1 is deleted while no webhook comes.
	gh.Mu.Lock()
	gh.Issues[3].RepoURL = gh.URL + "/repos/someone/else"
	delete(gh.Issues, 1)
	gh.Mu.Unlock()
	if removed, err := sy.SyncIssue(ctx, 3); err != nil || !removed {
		t.Errorf("transferred issue: removed %v, %v", removed, err)
	}
	st.SetSetting(ctx, "fixes_reconciled_at", time.Now().UTC().Add(-ReconcileEvery).Format(time.RFC3339))
	if _, err := sy.SyncAll(ctx); err != nil {
		t.Fatal(err)
	}
	if all, _ = st.Fixes(ctx); len(all) != 1 || all[0].Issue != 2 {
		t.Errorf("after the deletion: %+v", all)
	}
	// A webhook saying it was deleted is believed without asking GitHub.
	if removed, err := sy.Forget(ctx, 2); err != nil || !removed {
		t.Errorf("forget: %v %v", removed, err)
	}
}
