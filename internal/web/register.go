package web

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// Registering a source on the site (PLAN.md §30d): /api/register asks, a
// maintainer decides in /admin/sources, and the applicant's private link
// shows the decision and, once approved, the key, once.

// RequestsPerDayPerIP limits registration requests from one IP, counted in
// memory only.
var RequestsPerDayPerIP = 3

var tokenRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

type registerData struct {
	Form    store.SourceRequest
	Err     string
	PerHour int
}

// registerStatus is what /api/register/<token> shows.
type registerStatus struct {
	Link     string // the page's own URL, to keep
	Token    string
	New      bool                 // just submitted: say to keep the link
	Request  *store.SourceRequest // nil for a maintainer's key link
	Key      *store.KeyLink       // nil until approved
	SourceID string
	Expired  bool
	ShownKey string // the key, on the one page that shows it
	Err      string
	Spam     bool // the honeypot was filled: thank, keep nothing
	// Status is the big tag at the top; Steps the progress below it.
	Status, StatusClass string
	Steps               []regStep
}

// title names the page after the tool: "Mac Tester API key request".
func (d *registerStatus) title() string {
	if d.Request != nil {
		return d.Request.Name + " API key request"
	}
	return "API key request"
}

// regStep is one step of a request's progress (Carbon's progress indicator).
type regStep struct{ Label, State string } // State: done, current, error, or "" (not started)

// status fills in the tag and the steps from where the request stands.
func (d *registerStatus) status() {
	sent, review, key := "done", "current", ""
	switch {
	case d.ShownKey != "":
		d.Status, d.StatusClass, review, key = "Key shown", "retracted", "done", "done"
	case d.Request != nil && d.Request.State == store.RequestDeclined:
		d.Status, d.StatusClass, review = "Declined", "rejected", "error"
	case d.Request != nil && d.Request.State == store.RequestPending:
		d.Status, d.StatusClass = "Pending review", "pending"
	case d.Key == nil:
		d.Status, d.StatusClass = "Approved", "accepted"
	case d.Key.UsedAt == "superseded":
		d.Status, d.StatusClass, review, key = "Replaced by a newer link", "retracted", "done", "error"
	case d.Key.UsedAt != "":
		d.Status, d.StatusClass, review, key = "Key collected", "retracted", "done", "done"
	case d.Expired:
		d.Status, d.StatusClass, review, key = "Link expired", "rejected", "done", "error"
	default:
		d.Status, d.StatusClass, review, key = "Approved: key ready", "accepted", "done", "current"
	}
	if d.Request == nil { // a maintainer's key link: no request, no review
		d.Steps = []regStep{{"Key link issued", "done"}, {"Collect your key", key}}
		return
	}
	d.Steps = []regStep{{"Request sent", sent}, {"Maintainer review", review}, {"Collect your key", key}}
}

func privatePage(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	// same-origin, not no-referrer: other sites never see the private link,
	// but browsers still send a real Origin with the reveal's POST (with
	// no-referrer they send "null", which the same-origin check refuses).
	w.Header().Set("Referrer-Policy", "same-origin")
}

func (s *Server) registerForm(w http.ResponseWriter, r *http.Request) {
	s.renderRegister(w, r, http.StatusOK, registerData{})
}

func (s *Server) renderRegister(w http.ResponseWriter, r *http.Request, code int, d registerData) {
	d.PerHour = ReportsPerHour
	s.render(w, r, code, "register", page{Title: "Register a report source", Nav: "api", Styles: []string{"register.css"}, Scripts: []string{"register.js"},
		Description: "Ask for a source key so your test tool can send diagnostic reports to DoesItOmarchy.", Data: d})
}

func (s *Server) registerSubmit(w http.ResponseWriter, r *http.Request) {
	privatePage(w)
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		s.message(w, r, http.StatusBadRequest, "Couldn't send the request", "The form expired or came from elsewhere. Reload it and try again.", "/api/register")
		return
	}
	if r.PostFormValue("website") != "" { // the honeypot: only bots fill it in
		s.render(w, r, http.StatusOK, "register-status", page{Title: "Request received", Styles: []string{"register.css"}, Canonical: noCanonical, Data: registerStatus{Spam: true}})
		return
	}
	f := store.SourceRequest{SourceID: r.PostFormValue("id"), Name: r.PostFormValue("name"), RepoURL: r.PostFormValue("repo"),
		Homepage: r.PostFormValue("homepage"), Email: r.PostFormValue("email"), Description: r.PostFormValue("description")}
	// Typing mistakes don't count towards the daily limit.
	problem := ""
	if err := f.Check(); err != nil {
		problem = err.Error()
	}
	if r.PostFormValue("rules") != "yes" {
		problem = strings.TrimPrefix(problem+"; tick the box to confirm your tool shows the consent notice", "; ")
	}
	if problem != "" {
		s.renderRegister(w, r, http.StatusUnprocessableEntity, registerData{Form: f, Err: problem})
		return
	}
	if !s.registerLimit.allow(clientIP(r), time.Now()) {
		s.message(w, r, http.StatusTooManyRequests, "Too many requests", "This address has sent several requests today. Try again tomorrow.", "/api")
		return
	}
	token, err := s.store.RequestSource(r.Context(), f)
	var input store.InputError
	switch {
	case errors.As(err, &input):
		s.renderRegister(w, r, http.StatusUnprocessableEntity, registerData{Form: f, Err: input.Error()})
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	s.log.Info("source requested") // no personal data
	http.Redirect(w, r, "/api/register/"+token+"?new=1", http.StatusSeeOther)
}

// registerLookup resolves a private link: a request's status link (which
// becomes its key link on approval) or a maintainer's key link.
func (s *Server) registerLookup(w http.ResponseWriter, r *http.Request) (registerStatus, bool) {
	privatePage(w)
	token := r.PathValue("token")
	d := registerStatus{Link: BaseURL + "/api/register/" + token, Token: token}
	if !tokenRe.MatchString(token) {
		s.notFound(w, r)
		return d, false
	}
	ctx := r.Context()
	req, err := s.store.RequestByToken(ctx, token)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return d, false
	}
	key, err := s.store.KeyLinkByToken(ctx, token)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.fail(w, r, err)
		return d, false
	}
	if req == nil && key == nil {
		s.notFound(w, r)
		return d, false
	}
	d.Request, d.Key = req, key
	if req != nil {
		d.SourceID = req.SourceID
	}
	if key != nil {
		d.SourceID = key.SourceID
		d.Expired = key.UsedAt == "" && time.Now().UTC().Format(time.RFC3339) > key.ExpiresAt
	}
	return d, true
}

func (s *Server) registerStatusPage(w http.ResponseWriter, r *http.Request) {
	d, ok := s.registerLookup(w, r)
	if !ok {
		return
	}
	d.New = r.URL.Query().Get("new") == "1" && d.Request != nil && d.Request.State == store.RequestPending
	d.status()
	s.render(w, r, http.StatusOK, "register-status", page{Title: d.title(), Styles: []string{"register.css"}, Canonical: noCanonical, Data: d})
}

// registerReveal shows the key, once. It's a POST, so link previews in chat
// and email can't use the link up.
func (s *Server) registerReveal(w http.ResponseWriter, r *http.Request) {
	d, ok := s.registerLookup(w, r)
	if !ok {
		return
	}
	if !sameOrigin(r) {
		d.Err = "The page expired. Reload it and try again."
		d.status()
		s.render(w, r, http.StatusForbidden, "register-status", page{Title: d.title(), Styles: []string{"register.css"}, Canonical: noCanonical, Data: d})
		return
	}
	id, key, err := s.store.RedeemKeyLink(r.Context(), r.PathValue("token"))
	switch {
	case err == nil:
		s.log.Info("source key collected", "source", id)
		d.ShownKey = key
	case errors.Is(err, store.ErrNotFound):
		d.Err = "No key yet: the request is still waiting for a maintainer."
	case errors.Is(err, store.ErrLinkUsed), errors.Is(err, store.ErrLinkExpired):
		d.Err = "No key to show: " + err.Error() + "."
	default:
		s.fail(w, r, err)
		return
	}
	if d.Key, err = s.store.KeyLinkByToken(r.Context(), r.PathValue("token")); err != nil {
		d.Key = nil
	}
	d.status()
	s.render(w, r, http.StatusOK, "register-status", page{Title: "Your test source API key", Styles: []string{"register.css"}, Canonical: noCanonical, Data: d})
}
