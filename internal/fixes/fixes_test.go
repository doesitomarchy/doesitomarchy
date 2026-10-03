package fixes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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
	} {
		if got := StateOf(f, now); got != want {
			t.Errorf("%+v: %s, want %s", f, got, want)
		}
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
	if is, ok := WebhookIssue("issues", body, DefaultRepo); !ok || is.Number != 4 {
		t.Errorf("issues event: %v %v", is, ok)
	}
	if _, ok := WebhookIssue("issue_comment", body, DefaultRepo); !ok {
		t.Error("issue_comment event")
	}
	if _, ok := WebhookIssue("issues", body, "someone/else"); ok {
		t.Error("another repo's delivery")
	}
	if _, ok := WebhookIssue("push", body, DefaultRepo); ok {
		t.Error("push event")
	}
	pr := []byte(`{"issue":{"number":5,"pull_request":{}},"repository":{"full_name":"doesitomarchy/wecanfixeverything"}}`)
	if _, ok := WebhookIssue("issue_comment", pr, DefaultRepo); ok {
		t.Error("a comment on a pull request")
	}
}
