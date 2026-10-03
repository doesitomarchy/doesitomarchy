package fixes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// Syncer keeps the site's copy of fix issues current.
type Syncer struct {
	Client *Client
	Store  *store.Store
	Log    *slog.Logger
}

// SyncIssue refreshes one issue (and its timeline) from GitHub. Issues that
// aren't fixes are forgotten if the site had them.
func (s *Syncer) SyncIssue(ctx context.Context, number int) (changed bool, err error) {
	is, err := s.Client.Issue(ctx, number)
	if err != nil {
		return false, err
	}
	return s.apply(ctx, is)
}

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
		return false, s.Store.RemoveFix(ctx, is.Number)
	}
	return s.Store.UpsertFix(ctx, f)
}

// SyncAll refreshes every issue updated since the last full sync (all of
// them the first time), catching webhook deliveries that were missed.
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
	for _, is := range issues {
		changed, err := s.apply(ctx, is)
		if err != nil {
			return updated, err
		}
		if changed {
			updated++
		}
	}
	return updated, s.Store.SetSetting(ctx, "fixes_synced_at", started.Format(time.RFC3339))
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

// WebhookIssue returns the issue a webhook delivery is about: "issues" and
// "issue_comment" events from the fix repo. Other events, and pull requests,
// return ok=false.
func WebhookIssue(event string, body []byte, repo string) (Issue, bool) {
	if event != "issues" && event != "issue_comment" {
		return Issue{}, false
	}
	var p struct {
		Issue      Issue `json:"issue"`
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &p); err != nil || p.Issue.Number == 0 || p.Issue.PullRequest != nil {
		return Issue{}, false
	}
	if !strings.EqualFold(p.Repository.FullName, repo) {
		return Issue{}, false
	}
	return p.Issue, true
}
