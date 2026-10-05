package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"
)

// Registering a source on the site (PLAN.md §30d): a request, a maintainer's
// decision, and one-time key links. Tokens are stored only as hashes.

// Request states.
const (
	RequestPending  = "pending"
	RequestApproved = "approved"
	RequestDeclined = "declined"
)

// KeyLinkTTL is how long a key link waits to be opened.
const KeyLinkTTL = 7 * 24 * time.Hour

// declinedEmailTTL is how long a declined request keeps its contact email.
const declinedEmailTTL = 30 * 24 * time.Hour

// Errors a key link can give.
var (
	ErrLinkUsed    = errors.New("this link's key was already shown")
	ErrLinkExpired = errors.New("this link expired")
)

// InputError is a problem with what the applicant or maintainer typed:
// shown to them as is.
type InputError string

func (e InputError) Error() string { return string(e) }

// SourceRequest is a request to become a source.
type SourceRequest struct {
	ID                                       int64
	SourceID, Name, RepoURL, Homepage, Email string
	Description                              string
	State, Reason                            string
	CreatedAt, DecidedAt, DecidedBy          string
}

// KeyLink is a one-time link to a source's key.
type KeyLink struct{ SourceID, CreatedAt, ExpiresAt, UsedAt string }

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Check validates a request's fields, as given or as edited by a maintainer.
func (r *SourceRequest) Check() error {
	r.SourceID = NormalizeSourceID(r.SourceID)
	for _, f := range []*string{&r.Name, &r.RepoURL, &r.Homepage, &r.Email, &r.Description} {
		*f = strings.TrimSpace(*f)
	}
	var problems []string
	switch {
	case !sourceIDRe.MatchString(r.SourceID):
		problems = append(problems, "the source ID needs 2–32 lower-case letters, digits and '-', starting with a letter")
	case r.SourceID == "manual":
		problems = append(problems, `the source ID "manual" is reserved`)
	}
	if r.Name == "" || utf8.RuneCountInString(r.Name) > 80 {
		problems = append(problems, "the tool's name needs 1–80 characters")
	}
	if !httpsURL(r.RepoURL) {
		problems = append(problems, "the source code link must be an https:// address")
	}
	if r.Homepage != "" && !httpsURL(r.Homepage) {
		problems = append(problems, "the homepage must be an https:// address")
	}
	if a, err := mail.ParseAddress(r.Email); err != nil || a.Address != r.Email || len(r.Email) > 254 {
		problems = append(problems, "the contact email isn't a valid address")
	}
	if r.Description == "" || utf8.RuneCountInString(r.Description) > 1000 {
		problems = append(problems, "say what the tool tests, in up to 1,000 characters")
	}
	if problems != nil {
		return InputError(strings.Join(problems, "; "))
	}
	return nil
}

func httpsURL(u string) bool {
	return len(u) <= 300 && strings.HasPrefix(u, "https://") && len(u) > len("https://x.y") && !strings.ContainsAny(u, " \t\n<>\"")
}

// idTaken reports whether a source or another pending request has id.
func (s *Store) idTaken(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string, except int64) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM sources WHERE id = ?) + (SELECT count(*) FROM source_requests WHERE source_id = ? AND state = 'pending' AND id <> ?)",
		id, id, except).Scan(&n)
	return n > 0, err
}

// RequestSource stores a request and returns its private token.
func (s *Store) RequestSource(ctx context.Context, r SourceRequest) (string, error) {
	if err := r.Check(); err != nil {
		return "", err
	}
	s.purgeDeclinedEmails(ctx)
	if taken, err := s.idTaken(ctx, s.db, r.SourceID, 0); err != nil {
		return "", err
	} else if taken {
		return "", InputError(fmt.Sprintf("the source ID %q is taken; choose another", r.SourceID))
	}
	token := newToken()
	_, err := s.db.ExecContext(ctx, `INSERT INTO source_requests (token_hash, source_id, name, repo_url, homepage, email, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, hashKey(token), r.SourceID, r.Name, r.RepoURL, r.Homepage, r.Email, r.Description, now())
	return token, err
}

const requestCols = "id, source_id, name, repo_url, homepage, email, description, state, reason, created_at, decided_at, decided_by"

func scanRequest(row interface{ Scan(...any) error }) (*SourceRequest, error) {
	var r SourceRequest
	err := row.Scan(&r.ID, &r.SourceID, &r.Name, &r.RepoURL, &r.Homepage, &r.Email, &r.Description, &r.State, &r.Reason, &r.CreatedAt, &r.DecidedAt, &r.DecidedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &r, err
}

// SourceRequests lists requests in a state, oldest first.
func (s *Store) SourceRequests(ctx context.Context, state string) ([]SourceRequest, error) {
	s.purgeDeclinedEmails(ctx)
	rows, err := s.db.QueryContext(ctx, "SELECT "+requestCols+" FROM source_requests WHERE state = ? ORDER BY id", state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceRequest
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// PendingRequests counts requests awaiting a maintainer.
func (s *Store) PendingRequests(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM source_requests WHERE state = 'pending'").Scan(&n)
	return n, err
}

// RequestByToken finds the request a status link belongs to.
func (s *Store) RequestByToken(ctx context.Context, token string) (*SourceRequest, error) {
	return scanRequest(s.db.QueryRowContext(ctx, "SELECT "+requestCols+" FROM source_requests WHERE token_hash = ?", hashKey(token)))
}

// KeyLinkByToken finds a key link, used or not.
func (s *Store) KeyLinkByToken(ctx context.Context, token string) (*KeyLink, error) {
	var l KeyLink
	err := s.db.QueryRowContext(ctx, "SELECT source_id, created_at, expires_at, used_at FROM key_links WHERE token_hash = ?", hashKey(token)).
		Scan(&l.SourceID, &l.CreatedAt, &l.ExpiresAt, &l.UsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &l, err
}

// ApproveRequest registers the source (without a key yet) and turns the
// request's status link into its key link. edit holds the maintainer's
// final ID, name and links.
func (s *Store) ApproveRequest(ctx context.Context, id int64, edit SourceRequest, who string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var token, email, desc, state string
	err = tx.QueryRowContext(ctx, "SELECT token_hash, email, description, state FROM source_requests WHERE id = ?", id).Scan(&token, &email, &desc, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != RequestPending {
		return InputError("this request was already " + state)
	}
	edit.Email, edit.Description = email, desc
	if err := edit.Check(); err != nil {
		return err
	}
	if taken, err := s.idTaken(ctx, tx, edit.SourceID, id); err != nil {
		return err
	} else if taken {
		return InputError(fmt.Sprintf("the source ID %q is taken; choose another", edit.SourceID))
	}
	at := now()
	if _, err := tx.ExecContext(ctx, `UPDATE source_requests SET state = 'approved', source_id = ?, name = ?, repo_url = ?, homepage = ?, decided_at = ?, decided_by = ? WHERE id = ?`,
		edit.SourceID, edit.Name, edit.RepoURL, edit.Homepage, at, who, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO sources (id, name, homepage, repo_url, contact_email, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		edit.SourceID, edit.Name, edit.Homepage, edit.RepoURL, email, at); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO key_links (token_hash, source_id, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		token, edit.SourceID, who, at, time.Now().UTC().Add(KeyLinkTTL).Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

// DeclineRequest declines a request, with a reason the applicant sees.
func (s *Store) DeclineRequest(ctx context.Context, id int64, reason, who string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return InputError("give a reason: the applicant sees it")
	}
	res, err := s.db.ExecContext(ctx, "UPDATE source_requests SET state = 'declined', reason = ?, decided_at = ?, decided_by = ? WHERE id = ? AND state = 'pending'",
		reason, now(), who, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("request %d: %w (or already decided)", id, ErrNotFound)
	}
	return nil
}

// RedeemKeyLink gives the source a new key and returns it, once.
func (s *Store) RedeemKeyLink(ctx context.Context, token string) (sourceID, key string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var l KeyLink
	h := hashKey(token)
	err = tx.QueryRowContext(ctx, "SELECT source_id, expires_at, used_at FROM key_links WHERE token_hash = ?", h).Scan(&l.SourceID, &l.ExpiresAt, &l.UsedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", ErrNotFound
	case err != nil:
		return "", "", err
	case l.UsedAt != "":
		return "", "", ErrLinkUsed
	case now() > l.ExpiresAt:
		return "", "", ErrLinkExpired
	}
	key = newKey()
	if _, err := tx.ExecContext(ctx, "UPDATE sources SET key_hash = ?, revoked_at = '' WHERE id = ?", hashKey(key), l.SourceID); err != nil {
		return "", "", err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE key_links SET used_at = ? WHERE token_hash = ?", now(), h); err != nil {
		return "", "", err
	}
	return l.SourceID, key, tx.Commit()
}

// NewKeyLink stops the source's key at once and returns a fresh link's
// token, for a maintainer to send by hand. Older unused links stop working.
func (s *Store) NewKeyLink(ctx context.Context, sourceID, who string) (string, error) {
	sourceID = NormalizeSourceID(sourceID)
	if sourceID == "manual" {
		return "", errors.New(`"manual" never has a key`)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE sources SET key_hash = '' WHERE id = ?", sourceID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", fmt.Errorf("source %s: %w", sourceID, ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE key_links SET used_at = 'superseded' WHERE source_id = ? AND used_at = ''", sourceID); err != nil {
		return "", err
	}
	token := newToken()
	at := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, "INSERT INTO key_links (token_hash, source_id, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?)",
		hashKey(token), sourceID, who, at.Format(time.RFC3339), at.Add(KeyLinkTTL).Format(time.RFC3339)); err != nil {
		return "", err
	}
	return token, tx.Commit()
}

// purgeDeclinedEmails empties the contact email of requests declined more
// than 30 days ago (the privacy page promises it). Errors are ignored: it
// runs again with the next request or listing.
func (s *Store) purgeDeclinedEmails(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-declinedEmailTTL).Format(time.RFC3339)
	s.db.ExecContext(ctx, "UPDATE source_requests SET email = '' WHERE state = 'declined' AND email <> '' AND decided_at < ?", cutoff)
}
