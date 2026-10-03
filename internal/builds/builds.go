// Package builds finds which Omarchy code a result ran on, and when it was
// committed (PLAN §28.2). Omarchy's channels aren't one line of history
// (stable releases carry backports; edge runs ahead on the main branch), so
// the newest build is decided by commit date, whatever the channel.
//
// Where the commit comes from:
//   - edge: the version names it (4.0.0.r6713.ga85e29a);
//   - dev: the report's revision (`omarchy-version` prints "dev (<hash>)");
//   - stable, rc, beta: Omarchy's packaging repo, whose PKGBUILD history
//     records each release's pkgver and _commit (rc versions aren't tagged).
//
// Each lookup is cached in the store, found or not.
package builds

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

const (
	OmarchyRepo  = "basecamp/omarchy"
	PackagesRepo = "omacom/omarchy-pkgs"
	pkgbuildPath = "pkgbuilds/omarchy/PKGBUILD"
	// RetryAfter is how long a failed lookup is trusted before GitHub is asked again.
	RetryAfter = time.Hour
)

// Resolver looks builds up on GitHub's REST API.
type Resolver struct {
	Store *store.Store
	Base  string // API root; tests point it at a fake
	Token string // optional: public repos read without one, at a lower rate limit
	HTTP  *http.Client
	Log   *slog.Logger // nil: don't log
}

// New returns a resolver for GitHub.
func New(st *store.Store, token string, log *slog.Logger) *Resolver {
	return &Resolver{Store: st, Base: "https://api.github.com", Token: token, HTTP: &http.Client{Timeout: 20 * time.Second}, Log: log}
}

func (r *Resolver) log() *slog.Logger {
	if r.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.Log
}

// errNotFound means GitHub doesn't know the commit or release.
var errNotFound = errors.New("not found on GitHub")

// get fetches a GitHub API path into out. A token that GitHub refuses for a
// public repo (a fine-grained token limited to other repos) is dropped.
func (r *Resolver) get(ctx context.Context, path string, out any) error {
	for _, token := range []string{r.Token, ""} {
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(r.Base, "/")+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "doiomad (+https://doesitomarchy.com)")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := r.HTTP.Do(req)
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		res.Body.Close()
		if err != nil {
			return err
		}
		switch {
		case (res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden) && token != "":
			continue // try again without the token
		case res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusUnprocessableEntity:
			return errNotFound
		case res.StatusCode >= 300:
			return fmt.Errorf("GitHub API %s: %d", path, res.StatusCode)
		}
		return json.Unmarshal(b, out)
	}
	return fmt.Errorf("GitHub API %s: refused", path)
}

// Key is how a build is cached: by commit for edge and dev, by version for
// releases.
func Key(v status.Version, channel, revision string) string {
	switch {
	case channel == status.Dev && revision != "":
		return "commit:" + strings.ToLower(revision)
	case v.Hash != "":
		return "commit:" + v.Hash
	}
	return "release:" + v.String()
}

// Resolve returns the commit a build ran and when it was committed.
func (r *Resolver) Resolve(ctx context.Context, v status.Version, channel, revision string) (commit, committedAt string, err error) {
	key := Key(v, channel, revision)
	b, ok, err := r.Store.Build(ctx, key)
	switch {
	case err != nil:
		return "", "", err
	case ok && b.CommittedAt != "":
		return b.Commit, b.CommittedAt, nil
	case ok && b.Error != "":
		if at, err := time.Parse(time.RFC3339, b.CheckedAt); err == nil && time.Since(at) < RetryAfter {
			return "", "", fmt.Errorf("%s: %s (checked %s)", key, b.Error, b.CheckedAt)
		}
	}
	ref, isCommit := strings.CutPrefix(key, "commit:")
	switch {
	case ok && b.Commit != "":
		ref = b.Commit // a release seen while looking for another: its commit is known
	case !isCommit:
		ref, err = r.releaseCommit(ctx, v)
	}
	if err == nil {
		commit, committedAt, err = r.commitDate(ctx, ref)
	}
	nb := store.Build{Key: key, Commit: commit, CommittedAt: committedAt}
	if err != nil {
		nb.Error = err.Error()
	}
	if perr := r.Store.PutBuild(ctx, nb); perr != nil && err == nil {
		err = perr
	}
	return commit, committedAt, err
}

// commitDate looks a commit (any ref GitHub accepts) up in Omarchy's repo.
func (r *Resolver) commitDate(ctx context.Context, ref string) (sha, date string, err error) {
	var c struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := r.get(ctx, "/repos/"+OmarchyRepo+"/commits/"+url.PathEscape(ref), &c); err != nil {
		if errors.Is(err, errNotFound) {
			return "", "", fmt.Errorf("commit %s %w", ref, err)
		}
		return "", "", err
	}
	return c.SHA, c.Commit.Committer.Date.UTC().Format(time.RFC3339), nil
}

var (
	pkgverRe = regexp.MustCompile(`(?m)^pkgver=['"]?([^'"\s]+)`)
	commitRe = regexp.MustCompile(`(?m)^_commit=['"]?([0-9a-f]{7,40})`)
)

// releaseCommit finds the commit a release was built from, walking the
// packaging repo's PKGBUILD history (newest first) and caching every release
// it passes on the way.
func (r *Resolver) releaseCommit(ctx context.Context, want status.Version) (string, error) {
	for page := 1; page <= 10; page++ {
		var history []struct {
			SHA string `json:"sha"`
		}
		q := url.Values{"path": {pkgbuildPath}, "per_page": {"100"}, "page": {fmt.Sprint(page)}}
		if err := r.get(ctx, "/repos/"+PackagesRepo+"/commits?"+q.Encode(), &history); err != nil {
			return "", err
		}
		for _, h := range history {
			var file struct {
				Content string `json:"content"`
			}
			if err := r.get(ctx, "/repos/"+PackagesRepo+"/contents/"+pkgbuildPath+"?ref="+h.SHA, &file); err != nil {
				return "", err
			}
			text, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
			if err != nil {
				continue
			}
			pv, cm := pkgverRe.FindSubmatch(text), commitRe.FindSubmatch(text)
			if pv == nil || cm == nil {
				continue
			}
			v, err := status.ParseVersion(string(pv[1]))
			if err != nil {
				continue
			}
			if v == want {
				return string(cm[1]), nil
			}
			// Remember releases passed on the way (newest first, so the latest
			// rebuild of a version wins); their dates come when asked for.
			key := "release:" + v.String()
			if _, ok, _ := r.Store.Build(ctx, key); !ok {
				r.Store.PutBuild(ctx, store.Build{Key: key, Commit: string(cm[1])})
			}
		}
		if len(history) < 100 {
			break
		}
	}
	return "", fmt.Errorf("release %s %w (not in %s's history)", want, errNotFound, PackagesRepo)
}

// Fill records a result's build before it's stored. When it can't be found,
// the result gets an unknown_build flag and the test date stands in until
// the background lookup finds it.
func (r *Resolver) Fill(ctx context.Context, res *results.Result) {
	commit, at, err := r.Resolve(ctx, res.Omarchy, res.Channel, res.Revision)
	if err != nil {
		r.log().Warn("omarchy build", "version", res.OmarchyRaw, "channel", res.Channel, "err", err)
		res.Flags = append(res.Flags, results.Flag{Kind: results.FlagUnknownBuild, Detail: fmt.Sprintf(
			"Omarchy %s (%s): the build's commit date isn't known (%v); the test date stands in until it's found", res.OmarchyRaw, res.Channel, err)})
		return
	}
	res.Commit, res.BuiltAt = commit, at
}

// Loop looks up builds that weren't known when their result arrived, now
// and then every interval until ctx ends.
func (r *Resolver) Loop(ctx context.Context, every time.Duration) {
	for {
		if n, err := r.Retry(ctx); err != nil && ctx.Err() == nil {
			r.log().Warn("omarchy builds", "err", err)
		} else if n > 0 {
			r.log().Info("omarchy builds", "found", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

// Retry looks up every result whose build date isn't known yet, and returns
// how many it found.
func (r *Resolver) Retry(ctx context.Context) (int, error) {
	todo, err := r.Store.UnbuiltResults(ctx)
	if err != nil {
		return 0, err
	}
	found := 0
	for _, u := range todo {
		v, err := status.ParseVersion(u.Version)
		if err != nil {
			continue
		}
		commit, at, err := r.Resolve(ctx, v, u.Channel, u.Revision)
		if err != nil {
			continue
		}
		if err := r.Store.SetResultBuild(ctx, u.ID, commit, at, "builds"); err != nil {
			return found, err
		}
		found++
	}
	return found, nil
}
