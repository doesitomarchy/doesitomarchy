// Package fixes tracks fixes for failed criteria (PLAN.md §26). Each fix is
// an issue in a public GitHub repo (doesitomarchy/wecanfixeverything),
// linked to a criterion and a component or configuration by labels. The site
// keeps a copy of each issue's state, refreshed by GitHub's webhook and a
// slow poll.
package fixes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultRepo is the public fix repo.
const DefaultRepo = "doesitomarchy/wecanfixeverything"

// Client talks to the GitHub REST API for one repo.
type Client struct {
	Repo  string // owner/name
	Token string // fine-grained: Issues read/write on the repo only
	Base  string // API root; tests point it at a fake server
	HTTP  *http.Client
}

// NewClient returns a client for repo (DefaultRepo when "").
func NewClient(repo, token string) *Client {
	if repo == "" {
		repo = DefaultRepo
	}
	return &Client{Repo: repo, Token: token, Base: "https://api.github.com", HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Issue is the part of a GitHub issue the site uses.
type Issue struct {
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	HTMLURL     string     `json:"html_url"`
	State       string     `json:"state"`        // open | closed
	StateReason string     `json:"state_reason"` // completed | not_planned | reopened
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	Assignee    *User      `json:"assignee"`
	Labels      []Label    `json:"labels"`
	PullRequest *struct{}  `json:"pull_request"` // set when the "issue" is a pull request
}

type User struct {
	Login string `json:"login"`
}

type Label struct {
	Name string `json:"name"`
}

// Event is one entry of an issue's timeline.
type Event struct {
	Event     string    `json:"event"` // commented, assigned, cross-referenced, closed, referenced, labeled…
	CreatedAt time.Time `json:"created_at"`
	CommitID  string    `json:"commit_id"`
	CommitURL string    `json:"commit_url"` // API URL of the commit
	Source    *Source   `json:"source"`     // cross-referenced: where the issue was mentioned
}

// Source is the issue or pull request that mentioned a fix issue.
type Source struct {
	Issue *SourceIssue `json:"issue"`
}

type SourceIssue struct {
	HTMLURL     string       `json:"html_url"`
	State       string       `json:"state"`
	PullRequest *PullRequest `json:"pull_request"` // set when the mention is a pull request
}

type PullRequest struct {
	MergedAt *time.Time `json:"merged_at"`
}

// Errors from GitHub carry the status and message.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return fmt.Sprintf("GitHub API: %d %s", e.Status, e.Message) }

func (c *Client) do(ctx context.Context, method, path string, body, out any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.Base, "/")+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "doiomad (+https://doesitomarchy.com)")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return res, err
	}
	if res.StatusCode >= 300 {
		var m struct {
			Message string `json:"message"`
		}
		json.Unmarshal(b, &m)
		return res, &APIError{res.StatusCode, m.Message}
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return res, fmt.Errorf("GitHub API %s %s: %w", method, path, err)
		}
	}
	return res, nil
}

func (c *Client) repoPath(rest string) string { return "/repos/" + c.Repo + rest }

// Issue fetches one issue.
func (c *Client) Issue(ctx context.Context, number int) (Issue, error) {
	var is Issue
	_, err := c.do(ctx, "GET", c.repoPath(fmt.Sprintf("/issues/%d", number)), nil, &is)
	return is, err
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// Issues lists issues (not pull requests) updated since a time, all pages.
func (c *Client) Issues(ctx context.Context, since time.Time) ([]Issue, error) {
	q := url.Values{"state": {"all"}, "per_page": {"100"}, "sort": {"updated"}, "direction": {"asc"}}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	path := c.repoPath("/issues?" + q.Encode())
	var out []Issue
	for page := 0; path != "" && page < 50; page++ {
		var batch []Issue
		res, err := c.do(ctx, "GET", path, nil, &batch)
		if err != nil {
			return nil, err
		}
		for _, is := range batch {
			if is.PullRequest == nil {
				out = append(out, is)
			}
		}
		path = ""
		if m := nextLink.FindStringSubmatch(res.Header.Get("Link")); m != nil {
			path = strings.TrimPrefix(m[1], strings.TrimRight(c.Base, "/"))
		}
	}
	return out, nil
}

// Timeline lists an issue's timeline events (first 100, which covers a fix issue's life).
func (c *Client) Timeline(ctx context.Context, number int) ([]Event, error) {
	var ev []Event
	_, err := c.do(ctx, "GET", c.repoPath(fmt.Sprintf("/issues/%d/timeline?per_page=100", number)), nil, &ev)
	return ev, err
}

// CreateIssue opens an issue with labels.
func (c *Client) CreateIssue(ctx context.Context, title, body string, labels []string) (Issue, error) {
	var is Issue
	_, err := c.do(ctx, "POST", c.repoPath("/issues"), map[string]any{"title": title, "body": body, "labels": labels}, &is)
	return is, err
}

// EnsureLabel creates a label unless it exists.
func (c *Client) EnsureLabel(ctx context.Context, name, color, description string) error {
	_, err := c.do(ctx, "POST", c.repoPath("/labels"), map[string]string{"name": name, "color": color, "description": description}, nil)
	var api *APIError
	if err != nil && errorsAs(err, &api) && api.Status == http.StatusUnprocessableEntity {
		return nil // already there
	}
	return err
}

// Comment adds a comment to an issue.
func (c *Client) Comment(ctx context.Context, number int, body string) error {
	_, err := c.do(ctx, "POST", c.repoPath(fmt.Sprintf("/issues/%d/comments", number)), map[string]string{"body": body}, nil)
	return err
}

// AddLabels adds labels to an issue.
func (c *Client) AddLabels(ctx context.Context, number int, labels []string) error {
	_, err := c.do(ctx, "POST", c.repoPath(fmt.Sprintf("/issues/%d/labels", number)), map[string][]string{"labels": labels}, nil)
	return err
}

// Close closes an issue with a reason: completed or not_planned.
func (c *Client) Close(ctx context.Context, number int, reason string) error {
	_, err := c.do(ctx, "PATCH", c.repoPath(fmt.Sprintf("/issues/%d", number)), map[string]string{"state": "closed", "state_reason": reason}, nil)
	return err
}
