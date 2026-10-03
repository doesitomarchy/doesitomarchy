package store

import (
	"context"
	"database/sql"
	"errors"
)

// Build is a looked-up Omarchy build (PLAN §28.2): the commit it ran and
// when that was committed. Commit is "" when GitHub doesn't know it; Error
// says why the last lookup failed.
type Build struct {
	Key         string // "commit:<sha>" or "release:<canonical version>"
	Commit      string
	CommittedAt string
	CheckedAt   string
	Error       string
}

// Build returns a cached build, ok=false when it was never looked up.
func (s *Store) Build(ctx context.Context, key string) (b Build, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT key, commit_sha, committed_at, checked_at, error FROM omarchy_builds WHERE key = ?", key).
		Scan(&b.Key, &b.Commit, &b.CommittedAt, &b.CheckedAt, &b.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return Build{}, false, nil
	}
	return b, err == nil, err
}

// PutBuild caches a lookup, found or not.
func (s *Store) PutBuild(ctx context.Context, b Build) error {
	if b.CheckedAt == "" {
		b.CheckedAt = now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO omarchy_builds (key, commit_sha, committed_at, checked_at, error) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET commit_sha = excluded.commit_sha, committed_at = excluded.committed_at,
		checked_at = excluded.checked_at, error = excluded.error`, b.Key, b.Commit, b.CommittedAt, b.CheckedAt, b.Error)
	return err
}

// Unbuilt is a result whose build date isn't known yet.
type Unbuilt struct {
	ID                         int64
	Version, Channel, Revision string
}

// UnbuiltResults lists pending and accepted results whose build date isn't
// known, for the background lookup.
func (s *Store) UnbuiltResults(ctx context.Context) ([]Unbuilt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, omarchy_version, omarchy_channel, omarchy_revision FROM results
		WHERE omarchy_built_at = '' AND state IN ('pending', 'accepted') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unbuilt
	for rows.Next() {
		var u Unbuilt
		if err := rows.Scan(&u.ID, &u.Version, &u.Channel, &u.Revision); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetResultBuild records a result's build once it's known, resolves its open
// unknown_build flag with a note, and bumps the data version so running
// servers re-rank results (the build date decides which is latest).
func (s *Store) SetResultBuild(ctx context.Context, id int64, commit, builtAt, actor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE results SET omarchy_commit = ?, omarchy_built_at = ? WHERE id = ? AND omarchy_built_at = ''",
		commit, builtAt, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // already known
	}
	if _, err := tx.ExecContext(ctx, `UPDATE result_flags SET resolution = ?, resolved_by = ?, resolved_at = ?
		WHERE result_id = ? AND kind = 'unknown_build' AND resolved_at = ''`,
		"build found: commit "+short(commit, 12)+", committed "+builtAt, actor, now(), id); err != nil {
		return err
	}
	if err := bumpDataVersion(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func short(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
