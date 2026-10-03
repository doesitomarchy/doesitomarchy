package fixes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// Syncer keeps the site's copy of fix issues current. It reads one issue at
// a time per issue number, so the webhook, the poll and /admin can't
// interleave reads and writes of the same issue.
type Syncer struct {
	Client *Client
	Store  *store.Store
	Log    *slog.Logger // nil: don't log

	mu    sync.Mutex
	locks map[int]*sync.Mutex
}

// lock holds an issue until the returned func is called.
func (s *Syncer) lock(number int) (unlock func()) {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[int]*sync.Mutex{}
	}
	l := s.locks[number]
	if l == nil {
		l = &sync.Mutex{}
		s.locks[number] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (s *Syncer) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

// SyncIssue refreshes one issue (and its timeline) from GitHub. Issues that
// aren't fixes, were deleted or moved to another repo are forgotten.
func (s *Syncer) SyncIssue(ctx context.Context, number int) (changed bool, err error) {
	defer s.lock(number)()
	is, err := s.Client.Issue(ctx, number)
	if gone(err) || (err == nil && !s.Client.inRepo(is)) {
		return s.Store.RemoveFix(ctx, number)
	}
	if err != nil {
		return false, err
	}
	return s.apply(ctx, is)
}

// Forget drops an issue the webhook says was deleted or transferred.
func (s *Syncer) Forget(ctx context.Context, number int) (bool, error) {
	defer s.lock(number)()
	return s.Store.RemoveFix(ctx, number)
}

// apply stores an issue's state; the caller holds its lock.
func (s *Syncer) apply(ctx context.Context, is Issue) (bool, error) {
	if is.PullRequest != nil {
		return false, nil
	}
	tl, err := s.Client.Timeline(ctx, is.Number)
	if err != nil {
		return false, err
	}
	f, ok := FromIssue(is, tl, s.Client.Repo)
	if !ok {
		return s.Store.RemoveFix(ctx, is.Number)
	}
	return s.Store.UpsertFix(ctx, f)
}

// ReconcileEvery is how often SyncAll checks for issues that disappeared
// without a webhook delivery (deleted or transferred while the site was down).
const ReconcileEvery = 24 * time.Hour

// SyncAll catches up with GitHub, for webhook deliveries that were missed:
//   - every issue updated since the last sync (all of them the first time);
//   - every other open fix, because a linked pull request opening, closing or
//     merging in another repo doesn't touch the issue itself;
//   - once a day, fixes whose issue no longer exists.
//
// An issue that fails is logged and skipped, and the next sync starts from
// the oldest failure, so one bad issue can't hold up the rest.
func (s *Syncer) SyncAll(ctx context.Context) (updated int, err error) {
	var since time.Time
	if v, err := s.Store.Setting(ctx, "fixes_synced_at"); err == nil {
		since, _ = time.Parse(time.RFC3339, v)
		since = since.Add(-time.Hour) // overlap, in case of clock skew
	}
	started := time.Now().UTC()
	issues, err := s.Client.Issues(ctx, since)
	if err != nil {
		return 0, err
	}
	resume := started // where the next sync starts
	var failed []string
	fail := func(number int, err error) {
		s.log().Warn("fix sync", "issue", number, "err", err)
		failed = append(failed, fmt.Sprintf("#%d: %v", number, err))
	}
	count := func(changed bool) {
		if changed {
			updated++
		}
	}
	seen := map[int]bool{}
	for _, is := range issues {
		seen[is.Number] = true
		unlock := s.lock(is.Number)
		changed, err := s.apply(ctx, is)
		unlock()
		if err != nil {
			if ctx.Err() != nil {
				return updated, ctx.Err()
			}
			fail(is.Number, err)
			if is.UpdatedAt.Before(resume) {
				resume = is.UpdatedAt
			}
			continue
		}
		count(changed)
	}
	known, err := s.Store.Fixes(ctx)
	if err != nil {
		return updated, err
	}
	for _, f := range known {
		if !f.Open || seen[f.Issue] {
			continue
		}
		changed, err := s.SyncIssue(ctx, f.Issue)
		if err != nil {
			if ctx.Err() != nil {
				return updated, ctx.Err()
			}
			fail(f.Issue, err) // open fixes are read again next time anyway
			continue
		}
		count(changed)
	}
	if n, err := s.reconcile(ctx, started); err != nil {
		if ctx.Err() != nil {
			return updated, ctx.Err()
		}
		s.log().Warn("fix sync: reconcile", "err", err)
		failed = append(failed, "checking for deleted issues: "+err.Error())
	} else {
		updated += n
	}
	if err := s.Store.SetSetting(ctx, "fixes_synced_at", resume.Format(time.RFC3339)); err != nil {
		return updated, err
	}
	if len(failed) > 0 {
		return updated, fmt.Errorf("%d failed: %s", len(failed), strings.Join(failed, "; "))
	}
	return updated, nil
}

// reconcile forgets fixes whose issue is no longer in the repo, at most once
// per ReconcileEvery. It returns how many it forgot.
func (s *Syncer) reconcile(ctx context.Context, now time.Time) (int, error) {
	if v, err := s.Store.Setting(ctx, "fixes_reconciled_at"); err == nil {
		if at, err := time.Parse(time.RFC3339, v); err == nil && now.Sub(at) < ReconcileEvery {
			return 0, nil
		}
	}
	all, err := s.Client.Issues(ctx, time.Time{})
	if err != nil {
		return 0, err
	}
	exists := make(map[int]bool, len(all))
	for _, is := range all {
		exists[is.Number] = true
	}
	known, err := s.Store.Fixes(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, f := range known {
		if exists[f.Issue] {
			continue
		}
		removed, err := s.Forget(ctx, f.Issue)
		if err != nil {
			return n, err
		}
		if removed {
			s.log().Info("fix sync: issue gone from the repo", "issue", f.Issue)
			n++
		}
	}
	return n, s.Store.SetSetting(ctx, "fixes_reconciled_at", now.Format(time.RFC3339))
}

// Loop syncs now and then every interval until ctx ends.
func (s *Syncer) Loop(ctx context.Context, every time.Duration) {
	for {
		if n, err := s.SyncAll(ctx); err != nil {
			if ctx.Err() == nil {
				s.Log.Warn("fix sync", "err", err)
			}
		} else {
			s.Log.Info("fix sync", "updated", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// ValidSignature checks GitHub's X-Hub-Signature-256 header for a body.
func ValidSignature(secret string, body []byte, header string) bool {
	if secret == "" {
		return false
	}
	got, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	want, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(m.Sum(nil), want)
}

// WebhookIssue returns the issue a webhook delivery is about, and the action
// ("deleted", "transferred", "closed", "created"…): "issues" and
// "issue_comment" events from the fix repo. Other events, and pull requests,
// return ok=false.
func WebhookIssue(event string, body []byte, repo string) (is Issue, action string, ok bool) {
	if event != "issues" && event != "issue_comment" {
		return Issue{}, "", false
	}
	var p struct {
		Action     string `json:"action"`
		Issue      Issue  `json:"issue"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &p); err != nil || p.Issue.Number == 0 || p.Issue.PullRequest != nil {
		return Issue{}, "", false
	}
	if !strings.EqualFold(p.Repository.FullName, repo) {
		return Issue{}, "", false
	}
	return p.Issue, p.Action, true
}

// Gone reports whether a webhook action means the issue left the repo.
func Gone(action string) bool { return action == "deleted" || action == "transferred" }
