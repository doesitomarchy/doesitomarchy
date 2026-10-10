package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// The maintainers' review queue (PLAN.md §22.5). Cloudflare Access protects
// /admin; the server also verifies Access's signed token and maps its e-mail
// address to a maintainer handle, which every action records.

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", s.admin(s.adminQueue))
	mux.HandleFunc("GET /admin/{$}", s.admin(s.adminQueue))
	mux.HandleFunc("GET /admin/report/{code}", s.admin(s.adminReport))
	mux.HandleFunc("POST /admin/report/{code}/{action}", s.admin(s.adminAction))
	mux.HandleFunc("POST /admin/flag/{id}/resolve", s.admin(s.adminResolve))
	mux.HandleFunc("GET /admin/sources", s.admin(s.adminSources))
	mux.HandleFunc("POST /admin/sources/request/{id}/{action}", s.admin(s.adminRequestDecide))
	mux.HandleFunc("POST /admin/sources/{id}/{action}", s.admin(s.adminSourceAction))
	mux.HandleFunc("GET /admin/shares", s.admin(s.adminShares))
	s.adminUnsupportedRoutes(mux)
	mux.HandleFunc("POST /admin/import", limitBody(results.MaxSize+64<<10, s.admin(s.adminImport)))
	mux.HandleFunc("POST /admin/shares/review", s.admin(s.adminSharesReview))
}

type adminHandler func(w http.ResponseWriter, r *http.Request, who string)

// admin authenticates a maintainer, then runs h.
func (s *Server) admin(h adminHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		who, err := s.maintainer(r)
		if err != nil {
			s.log.Warn("admin denied", "path", r.URL.Path, "reason", err)
			code := http.StatusForbidden
			if errors.Is(err, errAdminOff) {
				code = http.StatusServiceUnavailable
			}
			http.Error(w, "Not available.", code)
			return
		}
		if r.Method == http.MethodPost && !s.validPost(r, who) {
			http.Error(w, "This form expired or came from elsewhere; reload the page and try again.", http.StatusForbidden)
			return
		}
		h(w, r, who)
	}
}

var errAdminOff = errors.New("admin is not configured (CF_ACCESS_TEAM, CF_ACCESS_AUD)")

func (s *Server) maintainer(r *http.Request) (string, error) {
	if s.opt.AdminInsecure {
		return "local", nil
	}
	if s.access == nil {
		return "", errAdminOff
	}
	token := r.Header.Get("Cf-Access-Jwt-Assertion")
	if token == "" {
		if c, err := r.Cookie("CF_Authorization"); err == nil {
			token = c.Value
		}
	}
	if token == "" {
		return "", errors.New("no Access token")
	}
	email, err := s.access.verify(r.Context(), token)
	if err != nil {
		return "", err
	}
	m, err := s.store.MaintainerByEmail(r.Context(), email)
	if err != nil {
		return "", errors.New(email + " is not a maintainer")
	}
	return m.Handle, nil
}

// csrf is a per-maintainer form token: an HMAC of the handle with a secret
// kept in the database.
func (s *Server) csrf(ctx context.Context, who string) string {
	secret, err := s.store.Setting(ctx, "admin_secret")
	if err != nil {
		b := make([]byte, 32)
		rand.Read(b)
		secret = hex.EncodeToString(b)
		s.store.SetSetting(ctx, "admin_secret", secret)
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("admin-form\x00" + who))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Server) validPost(r *http.Request, who string) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	got := r.FormValue("csrf")
	return got != "" && hmac.Equal([]byte(got), []byte(s.csrf(r.Context(), who)))
}

type adminQueueData struct {
	Who     string
	CSRF    string
	Err     string
	Pending []store.ResultSummary
	Flags   []store.ResultFlag
	Recent  []store.ResultSummary
}

func (s *Server) adminQueue(w http.ResponseWriter, r *http.Request, who string) {
	ctx := r.Context()
	d := adminQueueData{Who: who, CSRF: s.csrf(ctx, who), Err: r.URL.Query().Get("err")}
	var err error
	if d.Pending, err = s.store.ListResults(ctx, store.ResultFilter{State: store.Pending, Limit: 200}); err == nil {
		if d.Flags, err = s.store.Flags(ctx, true); err == nil {
			var all []store.ResultSummary
			all, err = s.store.ListResults(ctx, store.ResultFilter{Limit: 40})
			for _, x := range all {
				if x.State != store.Pending && len(d.Recent) < 15 {
					d.Recent = append(d.Recent, x)
				}
			}
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin", page{Title: "Review queue", Data: d})
}

type adminCandidate struct{ ID, Label string }

type adminReportData struct {
	Who        string
	CSRF       string
	R          *store.ResultDetail
	Raw        string
	Config     *configView
	Candidates []adminCandidate
	Ambiguous  bool
	Review     review
	// Challenges are the items a person (or a phone) answered with a code
	// the tool showed or played.
	Challenges []store.ResultItem
	Done, Err  string
}

func (s *Server) adminReport(w http.ResponseWriter, r *http.Request, who string) {
	ctx := r.Context()
	id, err := s.store.ResultIDByCode(ctx, r.PathValue("code"))
	if errors.Is(err, store.ErrNotFound) || !store.IsCode(r.PathValue("code")) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rd, err := s.store.Result(ctx, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	d := adminReportData{Who: who, CSRF: s.csrf(ctx, who), R: rd, Config: s.data().view.configs[rd.ConfigID],
		Done: r.URL.Query().Get("done"), Err: r.URL.Query().Get("err")}
	_, d.Raw, _ = s.store.ResultReport(ctx, id)
	for _, fl := range rd.Flags {
		d.Ambiguous = d.Ambiguous || (fl.Kind == results.FlagAmbiguous && fl.ResolvedAt == "")
	}
	// Choices: the tied candidates, else every config of the same Mac.
	choices := rd.Candidates
	if cv := d.Config; cv != nil && (len(choices) == 0 || !d.Ambiguous) {
		choices = nil
		for _, x := range cv.Mac.Configs {
			choices = append(choices, x.ID)
		}
	}
	for _, id := range choices {
		label := id
		if cv := s.data().view.configs[id]; cv != nil {
			label = cv.Diff + " · " + cv.ReleaseName + " · " + id
		}
		d.Candidates = append(d.Candidates, adminCandidate{id, label})
	}
	d.Review = s.buildReview(rd, d.Ambiguous)
	for _, it := range rd.Items {
		if it.Method == "challenge" {
			d.Challenges = append(d.Challenges, it)
		}
	}
	s.render(w, r, http.StatusOK, "admin-report", page{Title: "Review " + rd.Code, Data: d})
}

func (s *Server) adminAction(w http.ResponseWriter, r *http.Request, who string) {
	ctx := r.Context()
	code := r.PathValue("code")
	id, err := s.store.ResultIDByCode(ctx, code)
	if err != nil {
		s.notFound(w, r)
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	switch action := r.PathValue("action"); action {
	case "accept":
		err = s.store.SetResultState(ctx, id, store.Accepted, "", who)
	case "reject":
		err = s.store.SetResultState(ctx, id, store.Rejected, reason, who)
	case "retract":
		err = s.store.SetResultState(ctx, id, store.Retracted, reason, who)
	case "config":
		var applicable map[string]bool
		if applicable, err = results.ApplicableSet(s.cat, r.FormValue("config")); err == nil {
			err = s.store.SetResultConfig(ctx, id, r.FormValue("config"), applicable, who)
		}
	default:
		s.notFound(w, r)
		return
	}
	s.afterAction(w, r, code, r.PathValue("action"), err)
}

func (s *Server) adminResolve(w http.ResponseWriter, r *http.Request, who string) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	err = s.store.ResolveFlag(ctx, id, r.FormValue("note"), who)
	s.afterAction(w, r, r.FormValue("report"), "resolve", err)
}

// afterAction returns to the report with the outcome; a successful change
// to what counts reaches the public site through the usual rebuild.
func (s *Server) afterAction(w http.ResponseWriter, r *http.Request, code, action string, err error) {
	q := url.Values{}
	if err != nil {
		q.Set("err", err.Error())
	} else {
		q.Set("done", action)
		// Don't wait for the next poll. The watcher then finds the snapshot
		// current and skips its purge, so purge here.
		go func() {
			if changed, err := s.Refresh(context.Background()); err == nil && changed {
				s.purge.schedule()
			}
		}()
	}
	if !store.IsCode(code) {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/report/"+code+"?"+q.Encode(), http.StatusSeeOther)
}

type adminSourcesData struct {
	Who, CSRF   string
	Done, Err   string
	Requests    []store.SourceRequest
	Declined    []store.SourceRequest // newest first
	Sources     []store.Source
	Maintainers []store.Maintainer
	// NewLink is a key link just issued, shown once for a maintainer to
	// email to the source's contact address (PLAN §30d).
	NewLink, NewLinkFor, NewLinkEmail string
}

func (s *Server) adminSources(w http.ResponseWriter, r *http.Request, who string) {
	q := r.URL.Query()
	s.renderAdminSources(w, r, who, adminSourcesData{Done: q.Get("done"), Err: q.Get("err")})
}

func (s *Server) renderAdminSources(w http.ResponseWriter, r *http.Request, who string, d adminSourcesData) {
	ctx := r.Context()
	d.Who, d.CSRF = who, s.csrf(ctx, who)
	var err error
	if d.Requests, err = s.store.SourceRequests(ctx, store.RequestPending); err == nil {
		d.Declined, err = s.store.SourceRequests(ctx, store.RequestDeclined)
		slices.Reverse(d.Declined)
	}
	if err == nil {
		if d.Sources, err = s.store.Sources(ctx); err == nil {
			d.Maintainers, err = s.store.Maintainers(ctx)
		}
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "admin-sources", page{Title: "Sources", Data: d})
}

// adminSourcesDone returns to /admin/sources with a message.
func adminSourcesDone(w http.ResponseWriter, r *http.Request, key, msg string) {
	http.Redirect(w, r, "/admin/sources?"+url.Values{key: {msg}}.Encode(), http.StatusSeeOther)
}

func (s *Server) adminRequestDecide(w http.ResponseWriter, r *http.Request, who string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	switch r.PathValue("action") {
	case "approve":
		edit := store.SourceRequest{SourceID: r.FormValue("id"), Name: r.FormValue("name"), RepoURL: r.FormValue("repo"), Homepage: r.FormValue("homepage")}
		err = s.store.ApproveRequest(ctx, id, edit, who)
		if err == nil {
			s.log.Info("source approved", "source", store.NormalizeSourceID(edit.SourceID), "by", who)
			adminSourcesDone(w, r, "done", "approved "+store.NormalizeSourceID(edit.SourceID)+"; the applicant collects the key from their link")
			return
		}
	case "decline":
		err = s.store.DeclineRequest(ctx, id, r.FormValue("reason"), who)
		if err == nil {
			s.log.Info("source request declined", "request", id, "by", who)
			adminSourcesDone(w, r, "done", "declined; the applicant sees the reason")
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	var input store.InputError
	if errors.As(err, &input) || errors.Is(err, store.ErrNotFound) {
		adminSourcesDone(w, r, "err", err.Error())
		return
	}
	s.fail(w, r, err)
}

func (s *Server) adminSourceAction(w http.ResponseWriter, r *http.Request, who string) {
	ctx := r.Context()
	id := store.NormalizeSourceID(r.PathValue("id"))
	switch r.PathValue("action") {
	case "revoke":
		if err := s.store.RevokeSource(ctx, id); err != nil {
			adminSourcesDone(w, r, "err", err.Error())
			return
		}
		s.log.Info("source revoked", "source", id, "by", who)
		adminSourcesDone(w, r, "done", "revoked "+id+"; its reports stay")
	case "keylink":
		token, err := s.store.NewKeyLink(ctx, id, who)
		if err != nil {
			adminSourcesDone(w, r, "err", err.Error())
			return
		}
		s.log.Info("source key link issued", "source", id, "by", who)
		d := adminSourcesData{NewLink: BaseURL + "/api/register/" + token, NewLinkFor: id}
		if srcs, err := s.store.Sources(ctx); err == nil {
			for _, x := range srcs {
				if x.ID == id {
					d.NewLinkEmail = x.ContactEmail
				}
			}
		}
		// Shown on this response only, never in a URL.
		s.renderAdminSources(w, r, who, d)
	default:
		http.NotFound(w, r)
	}
}

// limitBody caps a request body before anything reads it (the admin
// wrapper reads the form token first).
func limitBody(n int64, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, n)
		h(w, r)
	}
}

// adminImport stores an uploaded report (our schema or a source's native
// format, PLAN §24) as pending, then opens it.
func (s *Server) adminImport(w http.ResponseWriter, r *http.Request, who string) {
	back := func(msg string) {
		http.Redirect(w, r, "/admin?"+url.Values{"err": {msg}}.Encode()+"#import", http.StatusSeeOther)
	}
	file, _, err := r.FormFile("report")
	if err != nil {
		back("Choose a report file to import.")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, results.MaxSize+1))
	if err != nil || len(raw) > results.MaxSize {
		back("Reports are limited to 1 MiB.")
		return
	}
	imp, err := results.Import(raw, r.FormValue("format"), s.cat, s.catalogFS(),
		results.ImportOptions{Omarchy: r.FormValue("omarchy"), Channel: r.FormValue("channel"), Revision: r.FormValue("revision"),
			Tester: r.FormValue("tester")}, time.Now())
	if err != nil {
		back(err.Error())
		return
	}
	if src := imp.Source; src != nil {
		if err := s.store.EnsureSource(r.Context(), src.ID, src.Name, src.Homepage); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	s.prepareResult(r.Context(), imp.Result)
	id, err := s.store.InsertResult(r.Context(), imp.Result, imp.Raw, imp.Format, who)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rd, err := s.store.Result(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/admin/report/"+rd.Code+"?done=import", http.StatusSeeOther)
}
