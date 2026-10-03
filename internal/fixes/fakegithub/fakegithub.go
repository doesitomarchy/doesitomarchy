// Package fakegithub is just enough of GitHub's REST API, for one repo, to
// test fix tracking without the network. Only tests import it.
package fakegithub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
}

// Server is a fake GitHub; Mu guards its maps for tests that change them.
type Server struct {
	Mu       sync.Mutex
	Repo     string
	Issues   map[int]*Issue
	Timeline map[int][]map[string]any
	Labels   map[string]bool
	Comments map[int][]string // the first entry is the issue's body
	URL      string
}

// New starts a fake for repo, stopped when the test ends.
func New(t testing.TB, repo string) *Server {
	f := &Server{Repo: repo, Issues: map[int]*Issue{}, Timeline: map[int][]map[string]any{}, Labels: map[string]bool{}, Comments: map[int][]string{}}
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
		n := len(f.Issues) + 1
		is := &Issue{Number: n, Title: body["title"].(string), HTMLURL: fmt.Sprintf("https://github.com/%s/issues/%d", f.Repo, n),
			State: "open", UpdatedAt: time.Now().UTC(), Labels: []Label{}}
		for _, l := range body["labels"].([]any) {
			is.Labels = append(is.Labels, Label{l.(string)})
		}
		f.Issues[n] = is
		f.Comments[n] = []string{body["body"].(string)}
		write(201, is)
	case r.Method == "GET" && path == "/issues":
		var since time.Time
		if s := r.URL.Query().Get("since"); s != "" {
			since, _ = time.Parse(time.RFC3339, s)
		}
		out := []*Issue{}
		for i := 1; i <= len(f.Issues); i++ {
			if is := f.Issues[i]; is != nil && !is.UpdatedAt.Before(since) {
				out = append(out, is)
			}
		}
		write(200, out)
	case len(parts) >= 2 && parts[0] == "issues":
		n, _ := strconv.Atoi(parts[1])
		is := f.Issues[n]
		if is == nil {
			write(404, map[string]string{"message": "Not Found"})
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
