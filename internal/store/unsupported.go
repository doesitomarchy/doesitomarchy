package store

import "context"

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
