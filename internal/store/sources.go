package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// KeyPrefix starts every source API key, so a leaked key is recognisable.
const KeyPrefix = "doi_"

var sourceIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func newKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return KeyPrefix + hex.EncodeToString(b)
}

// NormalizeSourceID lower-cases a source ID ("crh-DevTool" → "crh-devtool");
// IDs are stored and compared in lower case.
func NormalizeSourceID(id string) string { return strings.ToLower(strings.TrimSpace(id)) }

// AddSource registers an app that may submit reports and returns its API
// key. The key is shown once; only its hash is stored.
func (s *Store) AddSource(ctx context.Context, id, name, homepage string) (key string, err error) {
	id = NormalizeSourceID(id)
	if !sourceIDRe.MatchString(id) {
		return "", fmt.Errorf("source ID %q: 2–32 lower-case letters, digits and '-', starting with a letter", id)
	}
	if id == "manual" {
		return "", errors.New(`"manual" is reserved for maintainer imports and never has a key`)
	}
	if strings.TrimSpace(name) == "" {
		return "", errors.New("a name is required")
	}
	key = newKey()
	_, err = s.db.ExecContext(ctx, "INSERT INTO sources (id, name, homepage, key_hash, created_at) VALUES (?, ?, ?, ?, ?)",
		id, strings.TrimSpace(name), strings.TrimSpace(homepage), hashKey(key), now())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return "", fmt.Errorf("source %q already exists (rotate its key instead)", id)
	}
	return key, err
}

// RotateSourceKey issues a new key; the old one stops working at once.
func (s *Store) RotateSourceKey(ctx context.Context, id string) (string, error) {
	id = NormalizeSourceID(id)
	if id == "manual" {
		return "", errors.New(`"manual" never has a key`)
	}
	key := newKey()
	res, err := s.db.ExecContext(ctx, "UPDATE sources SET key_hash = ?, revoked_at = '' WHERE id = ?", hashKey(key), id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", fmt.Errorf("source %s: %w", id, ErrNotFound)
	}
	return key, nil
}

// RevokeSource stops a source submitting; its reports stay.
func (s *Store) RevokeSource(ctx context.Context, id string) error {
	id = NormalizeSourceID(id)
	return s.updateSource(ctx, id, "UPDATE sources SET revoked_at = ? WHERE id = ?", now(), id)
}

// SetSourceTrust marks a source pending or trusted (PLAN §20.1; trusted
// sources may later auto-accept).
func (s *Store) SetSourceTrust(ctx context.Context, id, trust string) error {
	if trust != "pending" && trust != "trusted" {
		return fmt.Errorf("trust is pending or trusted, not %q", trust)
	}
	id = NormalizeSourceID(id)
	return s.updateSource(ctx, id, "UPDATE sources SET trust = ? WHERE id = ?", trust, id)
}

func (s *Store) updateSource(ctx context.Context, id, q string, args ...any) error {
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("source %s: %w", id, ErrNotFound)
	}
	return nil
}

// ErrRevoked is returned for a key whose source was revoked.
var ErrRevoked = errors.New("source revoked")

// SourceByKey finds the source a key belongs to: ErrNotFound for an unknown
// key, ErrRevoked for a revoked source.
func (s *Store) SourceByKey(ctx context.Context, key string) (*Source, error) {
	if !strings.HasPrefix(key, KeyPrefix) {
		return nil, ErrNotFound
	}
	var x Source
	err := s.db.QueryRowContext(ctx, "SELECT id, name, homepage, trust, created_at, revoked_at FROM sources WHERE key_hash = ? AND key_hash <> ''",
		hashKey(key)).Scan(&x.ID, &x.Name, &x.Homepage, &x.Trust, &x.CreatedAt, &x.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if x.RevokedAt != "" {
		return &x, ErrRevoked
	}
	return &x, nil
}

// RecentSubmissions counts a source's reports submitted since t (rate limit).
func (s *Store) RecentSubmissions(ctx context.Context, source string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM results WHERE source_id = ? AND submitted_at >= ?",
		source, since.UTC().Format(time.RFC3339)).Scan(&n)
	return n, err
}

// Maintainer may moderate in /admin.
type Maintainer struct{ Handle, Email, AddedAt string }

var handleRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,38}$`)

// AddMaintainer lets an e-mail address (proved by Cloudflare Access) moderate
// under a handle.
func (s *Store) AddMaintainer(ctx context.Context, handle, email string) error {
	handle, email = strings.ToLower(strings.TrimSpace(handle)), strings.TrimSpace(email)
	if !handleRe.MatchString(handle) {
		return fmt.Errorf("handle %q: lower-case letters, digits, '.', '_' or '-'", handle)
	}
	if !strings.Contains(email, "@") {
		return fmt.Errorf("%q is not an e-mail address", email)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO maintainers (handle, email, added_at) VALUES (?, ?, ?)
		ON CONFLICT (handle) DO UPDATE SET email = excluded.email, removed_at = ''`, handle, email, now())
	return err
}

// RemoveMaintainer ends a maintainer's access (their past actions stay recorded).
func (s *Store) RemoveMaintainer(ctx context.Context, handle string) error {
	res, err := s.db.ExecContext(ctx, "UPDATE maintainers SET removed_at = ? WHERE handle = ? AND removed_at = ''", now(), handle)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("maintainer %s: %w", handle, ErrNotFound)
	}
	return nil
}

// MaintainerByEmail finds a current maintainer.
func (s *Store) MaintainerByEmail(ctx context.Context, email string) (*Maintainer, error) {
	var m Maintainer
	err := s.db.QueryRowContext(ctx, "SELECT handle, email, added_at FROM maintainers WHERE email = ? AND removed_at = ''",
		strings.TrimSpace(email)).Scan(&m.Handle, &m.Email, &m.AddedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &m, err
}

// Maintainers lists current maintainers.
func (s *Store) Maintainers(ctx context.Context) ([]Maintainer, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT handle, email, added_at FROM maintainers WHERE removed_at = '' ORDER BY handle")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Maintainer
	for rows.Next() {
		var m Maintainer
		if err := rows.Scan(&m.Handle, &m.Email, &m.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// EnsureSource registers a source without a key, if it isn't already, so a
// maintainer can import its native reports (PLAN §24). It can't submit
// through the API until it's given a key (sources rotate).
func (s *Store) EnsureSource(ctx context.Context, id, name, homepage string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO sources (id, name, homepage, created_at) VALUES (?, ?, ?, ?) ON CONFLICT (id) DO NOTHING",
		NormalizeSourceID(id), name, homepage, now())
	return err
}
