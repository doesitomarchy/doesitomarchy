package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Fix is the site's copy of an issue in the fix repo (PLAN.md §26).
type Fix struct {
	Issue        int
	Capability   string
	Component    string // the fix covers every configuration with this component…
	Config       string // …or just this configuration
	Title, URL   string
	Open         bool
	StateReason  string // completed | not_planned | reopened
	Assignee     string
	Proposed     bool
	LastActivity string // RFC 3339, UTC
	ClosedAt     string
	FixLink      string
	OpenedBy     string
	SyncedAt     string
}

const fixCols = `issue, capability_id, component_id, config_id, title, url, open, state_reason, assignee, proposed,
	last_activity, closed_at, fix_link, opened_by, synced_at`

func scanFix(row interface{ Scan(...any) error }) (Fix, error) {
	var f Fix
	var open, proposed int
	err := row.Scan(&f.Issue, &f.Capability, &f.Component, &f.Config, &f.Title, &f.URL, &open, &f.StateReason, &f.Assignee,
		&proposed, &f.LastActivity, &f.ClosedAt, &f.FixLink, &f.OpenedBy, &f.SyncedAt)
	f.Open, f.Proposed = open == 1, proposed == 1
	return f, err
}

// UpsertFix stores an issue's current state. It keeps opened_by from an
// earlier insert, and bumps the data version when what the site shows
// changes, so running servers rebuild. changed reports whether it did.
func (s *Store) UpsertFix(ctx context.Context, f Fix) (changed bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	prev, err := scanFix(tx.QueryRowContext(ctx, "SELECT "+fixCols+" FROM fixes WHERE issue = ?", f.Issue))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		err = nil
	case err != nil:
		return false, err
	default:
		if f.OpenedBy == "" {
			f.OpenedBy = prev.OpenedBy
		}
		if f.LastActivity < prev.LastActivity {
			f.LastActivity = prev.LastActivity
		}
		if f.FixLink == "" && !f.Open {
			f.FixLink = prev.FixLink
		}
	}
	f.SyncedAt = now()
	cmp := prev
	cmp.SyncedAt = f.SyncedAt
	changed = cmp != f
	if _, err = tx.ExecContext(ctx, `INSERT INTO fixes (`+fixCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (issue) DO UPDATE SET capability_id = excluded.capability_id, component_id = excluded.component_id,
		config_id = excluded.config_id, title = excluded.title, url = excluded.url, open = excluded.open,
		state_reason = excluded.state_reason, assignee = excluded.assignee, proposed = excluded.proposed,
		last_activity = excluded.last_activity, closed_at = excluded.closed_at, fix_link = excluded.fix_link,
		opened_by = excluded.opened_by, synced_at = excluded.synced_at`,
		f.Issue, f.Capability, f.Component, f.Config, f.Title, f.URL, b2i(f.Open), f.StateReason, f.Assignee, b2i(f.Proposed),
		f.LastActivity, f.ClosedAt, f.FixLink, f.OpenedBy, f.SyncedAt); err != nil {
		return false, err
	}
	if changed {
		if err = bumpDataVersion(ctx, tx); err != nil {
			return false, err
		}
	}
	return changed, tx.Commit()
}

// RemoveFix forgets an issue that no longer carries a criterion: label.
func (s *Store) RemoveFix(ctx context.Context, issue int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM fixes WHERE issue = ?", issue)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			err = bumpDataVersion(ctx, tx)
		}
	}
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Fixes lists every tracked issue, newest first.
func (s *Store) Fixes(ctx context.Context) ([]Fix, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+fixCols+" FROM fixes ORDER BY issue DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fix
	for rows.Next() {
		f, err := scanFix(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SeenDelivery records a webhook delivery ID and reports whether it was
// already handled. IDs older than a week are forgotten.
func (s *Store) SeenDelivery(ctx context.Context, id string) (bool, error) {
	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, "DELETE FROM fix_deliveries WHERE received_at < ?", cutoff); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, "INSERT INTO fix_deliveries (id, received_at) VALUES (?, ?) ON CONFLICT (id) DO NOTHING", id, now())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 0, nil
}

// Unsupported is a maintainer's white flag on a criterion (PLAN §20.1).
type Unsupported struct {
	ID         int64
	Capability string
	Config     string
	Component  string
	Reason     string
	SetBy      string
	SetAt      string
	ClearedBy  string
	ClearedAt  string
}

// UnsupportedList lists white flags, current ones first (all includes cleared ones).
func (s *Store) UnsupportedList(ctx context.Context, all bool) ([]Unsupported, error) {
	q := `SELECT id, capability_id, config_id, component_id, reason, set_by, set_at, cleared_by, cleared_at FROM unsupported`
	if !all {
		q += " WHERE cleared_at = ''"
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY cleared_at <> '', set_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Unsupported
	for rows.Next() {
		var u Unsupported
		if err := rows.Scan(&u.ID, &u.Capability, &u.Config, &u.Component, &u.Reason, &u.SetBy, &u.SetAt, &u.ClearedBy, &u.ClearedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ClearUnsupported lifts a white flag (it stays on record).
func (s *Store) ClearUnsupported(ctx context.Context, id int64, actor string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "UPDATE unsupported SET cleared_by = ?, cleared_at = ? WHERE id = ? AND cleared_at = ''", actor, now(), id)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			err = ErrNotFound
		} else {
			err = bumpDataVersion(ctx, tx)
		}
	}
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
