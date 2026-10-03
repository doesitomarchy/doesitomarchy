// Package fakegithub is just enough of GitHub's REST API, for one repo, to
// test fix tracking without the network. Only tests import it.
package fakegithub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Token is the only token the fake accepts.
const Token = "test-token"

type User struct {
	Login string `json:"login"`
}

type Label struct {
	Name string `json:"name"`
}

// Issue is an issue as the fake stores and returns it.
type Issue struct {
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	HTMLURL     string     `json:"html_url"`
	State       string     `json:"state"`
	StateReason string     `json:"state_reason,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	Assignee    *User      `json:"assignee,omitempty"`
	Labels      []Label    `json:"labels"`
	RepoURL     string     `json:"repository_url,omitempty"` // set to another repo's to fake a transfer
}

func (is *Issue) hasLabel(name string) bool {
	for _, l := range is.Labels {
		if l.Name == name {
			return true
		}
	}
	return false
}

// Server is a fake GitHub; Mu guards its maps for tests that change them.
// Deleting from Issues fakes a deleted issue.
type Server struct {
	Mu       sync.Mutex
	Repo     string
	Issues   map[int]*Issue
	Timeline map[int][]map[string]any
	Labels   map[string]bool
	Comments map[int][]string // the first entry is the issue's body
	Fail     map[int]int      // issue → HTTP status its reads answer with, to fake an outage
	URL      string
	last     int // the last issue number handed out
}

// New starts a fake for repo, stopped when the test ends.
func New(t testing.TB, repo string) *Server {
	f := &Server{Repo: repo, Issues: map[int]*Issue{}, Timeline: map[int][]map[string]any{}, Labels: map[string]bool{}, Comments: map[int][]string{},
		Fail: map[int]int{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	return f
}

func (f *Server) serve(w http.ResponseWriter, r *http.Request) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+Token {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]string{"message": "Bad credentials"})
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/repos/"+f.Repo)
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	write := func(code int, v any) {
		w.WriteHeader(code)
		if v != nil {
			json.NewEncoder(w).Encode(v)
		}
	}
	switch {
	case r.Method == "POST" && path == "/labels":
		name, _ := body["name"].(string)
		if f.Labels[name] {
			write(422, map[string]string{"message": "Validation Failed"})
			return
		}
		f.Labels[name] = true
		write(201, nil)
	case r.Method == "POST" && path == "/issues":
		for n := range f.Issues { // issues a test added directly
			f.last = max(f.last, n)
		}
		f.last++
		n := f.last
		is := &Issue{Number: n, Title: body["title"].(string), HTMLURL: fmt.Sprintf("https://github.com/%s/issues/%d", f.Repo, n),
			State: "open", UpdatedAt: time.Now().UTC(), Labels: []Label{}, RepoURL: f.URL + "/repos/" + f.Repo}
		for _, l := range body["labels"].([]any) {
			is.Labels = append(is.Labels, Label{l.(string)})
		}
		f.Issues[n] = is
		f.Comments[n] = []string{body["body"].(string)}
		write(201, is)
	case r.Method == "GET" && path == "/issues":
		q := r.URL.Query()
		var since time.Time
		if s := q.Get("since"); s != "" {
			since, _ = time.Parse(time.RFC3339, s)
		}
		state := q.Get("state")
		if state == "" {
			state = "open"
		}
		var labels []string
		if l := q.Get("labels"); l != "" {
			labels = strings.Split(l, ",")
		}
		out := []*Issue{}
	issues:
		for _, is := range f.Issues {
			if is.UpdatedAt.Before(since) || (state != "all" && is.State != state) {
				continue
			}
			for _, l := range labels {
				if !is.hasLabel(l) {
					continue issues
				}
			}
			out = append(out, is)
		}
		sort.Slice(out, func(i, j int) bool { // as asked: least recently updated first
			if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
				return out[i].UpdatedAt.Before(out[j].UpdatedAt)
			}
			return out[i].Number < out[j].Number
		})
		write(200, out)
	case len(parts) >= 2 && parts[0] == "issues":
		n, _ := strconv.Atoi(parts[1])
		is := f.Issues[n]
		if is == nil {
			write(404, map[string]string{"message": "Not Found"})
			return
		}
		if code := f.Fail[n]; code != 0 && r.Method == "GET" {
			write(code, map[string]string{"message": "Server Error"})
			return
		}
		switch {
		case r.Method == "GET" && len(parts) == 2:
			write(200, is)
		case r.Method == "GET" && parts[2] == "timeline":
			ev := f.Timeline[n]
			if ev == nil {
				ev = []map[string]any{}
			}
			write(200, ev)
		case r.Method == "POST" && parts[2] == "comments":
			f.Comments[n] = append(f.Comments[n], body["body"].(string))
			write(201, nil)
		case r.Method == "POST" && parts[2] == "labels":
			for _, l := range body["labels"].([]any) {
				is.Labels = append(is.Labels, Label{l.(string)})
			}
			write(200, is.Labels)
		case r.Method == "PATCH":
			is.State, _ = body["state"].(string)
			is.StateReason, _ = body["state_reason"].(string)
			now := time.Now().UTC()
			is.ClosedAt, is.UpdatedAt = &now, now
			write(200, is)
		default:
			write(404, map[string]string{"message": "Not Found"})
		}
	default:
		write(404, map[string]string{"message": "Not Found"})
	}
}
