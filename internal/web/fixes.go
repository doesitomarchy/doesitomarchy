package web

import (
	"context"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/builds"
	"github.com/doesitomarchy/doesitomarchy/internal/fixes"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// Fix tracking (PLAN.md §26): GitHub's webhook for the fix repo, the
// background sync, and the public /fixes page.

// githubHook takes a webhook delivery from the fix repo. It checks the
// signature, answers at once, and refreshes the issue in the background
// (the data-version watcher then rebuilds the site and purges the cache).
// The delivery is recorded as handled only once that worked, so
// redelivering a failed one from GitHub retries it.
func (s *Server) githubHook(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.opt.WebhookSecret == "" {
		http.Error(w, "webhook not configured", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 5<<20))
	if err != nil {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	if !fixes.ValidSignature(s.opt.WebhookSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		s.log.Warn("webhook refused", "reason", "bad signature")
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	if event == "ping" {
		w.Write([]byte("pong\n"))
		return
	}
	delivery := r.Header.Get("X-GitHub-Delivery")
	if delivery != "" {
		if done, err := s.store.DeliveryHandled(r.Context(), delivery); err == nil && done {
			w.WriteHeader(http.StatusOK) // a redelivery: already handled
			return
		}
	}
	repo := s.fixRepo()
	is, action, ok := fixes.WebhookIssue(event, body, repo)
	if !ok {
		w.WriteHeader(http.StatusNoContent) // not about a fix issue
		return
	}
	w.WriteHeader(http.StatusAccepted)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var err error
		switch {
		case fixes.Gone(action) && s.syncer != nil:
			_, err = s.syncer.Forget(ctx, is.Number)
		case fixes.Gone(action):
			_, err = s.store.RemoveFix(ctx, is.Number)
		case s.syncer != nil:
			_, err = s.syncer.SyncIssue(ctx, is.Number) // with its timeline: linked pull requests, closing commit
		default: // no token: the payload is all there is
			if f, ok := fixes.FromIssue(is, nil, repo); ok {
				_, err = s.store.UpsertFix(ctx, f)
			} else {
				_, err = s.store.RemoveFix(ctx, is.Number)
			}
		}
		if err != nil {
			s.log.Warn("webhook sync", "issue", is.Number, "action", action, "err", err)
			return
		}
		if delivery != "" {
			if err := s.store.RecordDelivery(ctx, delivery); err != nil {
				s.log.Warn("webhook delivery", "id", delivery, "err", err)
			}
		}
	}()
}

// SyncFixes runs the slow catch-up poll until ctx ends (PLAN §26: every
// 6 hours and at start-up). It does nothing without a GitHub token.
func (s *Server) SyncFixes(ctx context.Context, every time.Duration) {
	if s.syncer == nil {
		return
	}
	s.syncer.Loop(ctx, every)
}

// fixRow is one fix on /fixes and /admin/fixes.
type fixRow struct {
	Fix       store.Fix
	State     fixes.State
	Criterion string
	Scope     string // component or configuration name
	ScopeURL  string
	Configs   int // configurations it covers
	Failing   int // of those, where the criterion currently fails or only partly works
	OpenedBy  string
}

type fixesPageData struct {
	Groups []fixGroup
	Repo   string
}

type fixGroup struct {
	State fixes.State
	Title string
	Rows  []fixRow
}

// fixRows reads every fix against the current snapshot.
func (s *Server) fixRows() []fixRow {
	snap := s.data()
	v := snap.view
	if v.rollup == nil {
		return nil
	}
	names := map[string]string{}
	for _, cp := range s.cat.Capabilities {
		names[cp.ID] = cp.Name
	}
	var out []fixRow
	for _, f := range v.rollup.Fixes {
		row := fixRow{Fix: f, State: fixes.StateOf(f, v.at), Criterion: names[f.Capability], OpenedBy: f.OpenedBy}
		if row.Criterion == "" {
			row.Criterion = f.Capability
		}
		if f.Component != "" {
			row.Scope = f.Component
			if comp := s.cat.Components[f.Component]; comp != nil {
				row.Scope = comp.Name
			}
			row.ScopeURL = "/search?q=" + f.Component
		} else if cv := v.configs[f.Config]; cv != nil {
			row.Scope = cv.Mac.Identifier + " · " + cv.Diff + " · " + cv.ReleaseName
			row.ScopeURL = "/mac/" + cv.Mac.Slug + "#cfg-" + cv.ID
		} else {
			row.Scope = f.Config
		}
		for _, mv := range v.macs {
			for _, cv := range mv.Configs {
				// Standard components only, as on the configuration's own card.
				if cfg := s.cfgs[cv.ID]; cfg == nil || !fixes.Covers(f, f.Capability, cv.ID, cfg.Components) {
					continue
				}
				cs, applies := cv.Status.Caps[f.Capability]
				if !applies {
					continue
				}
				row.Configs++
				if cs.Verdict == "failed" || cs.Verdict == "partial" {
					row.Failing++
				}
			}
		}
		out = append(out, row)
	}
	return out
}

// fixesPage is the public list of fixes: what's being worked on, what needs
// a hand, and what landed recently.
func (s *Server) fixesPage(w http.ResponseWriter, r *http.Request) {
	order := []fixGroup{
		{State: fixes.Open, Title: "Needs a hand"},
		{State: fixes.Proposed, Title: "Fix proposed: help test it"},
		{State: fixes.Claimed, Title: "Being worked on"},
		{State: fixes.Fixed, Title: "Fixed: please re-test"},
	}
	idx := map[fixes.State]int{fixes.Open: 0, fixes.Stale: 0, fixes.Proposed: 1, fixes.Claimed: 2, fixes.Fixed: 3}
	for _, row := range s.fixRows() {
		if i, ok := idx[row.State]; ok {
			order[i].Rows = append(order[i].Rows, row)
		}
	}
	for i := range order {
		rows := order[i].Rows
		sort.SliceStable(rows, func(a, b int) bool {
			if rows[a].Failing != rows[b].Failing {
				return rows[a].Failing > rows[b].Failing
			}
			return rows[a].Fix.Issue > rows[b].Fix.Issue
		})
	}
	s.render(w, r, http.StatusOK, "fixes", page{Title: "Fixes", Nav: "fixes", Data: fixesPageData{Groups: order, Repo: s.fixRepo()},
		Description: "Fixes in progress for what doesn't work yet on Intel Macs running Omarchy. #WeCanFixEverything"})
}

// prepareResult finishes a new report before it's stored (PLAN §28.2): the
// Omarchy build it ran (or an unknown_build flag), and regression flags
// against the current results.
func (s *Server) prepareResult(ctx context.Context, res *results.Result) {
	if s.builds != nil {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		s.builds.Fill(ctx, res)
		cancel()
	}
	if ru := s.data().view.rollup; ru != nil {
		res.Flags = append(res.Flags, builds.Regressions(s.cat, ru, res)...)
	}
}

// ResolveBuilds looks up builds that weren't known when their report
// arrived, at start-up and then every interval; a no-op when lookups are off.
func (s *Server) ResolveBuilds(ctx context.Context, every time.Duration) {
	if s.builds == nil {
		return
	}
	s.builds.Loop(ctx, every)
}
