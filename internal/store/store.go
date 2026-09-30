// Package store is the SQLite database: schema migrations, the catalog tables
// (rebuilt from YAML by SyncCatalog) and the queries the web server needs.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // pure-Go driver, registers "sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies pending
// migrations. Use ":memory:" only for single-connection tests.
func Open(ctx context.Context, path string) (*Store, error) {
	q := url.Values{}
	for _, p := range []string{"journal_mode(WAL)", "foreign_keys(1)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks the database is reachable (health checks).
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

type migration struct {
	version int
	name    string
	sql     string
}

func migrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		n, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(n)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("migration %s: name must start with a positive number and _", e.Name())
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{v, e.Name(), string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %s: expected version %d (no gaps or duplicates)", m.name, i+1)
		}
	}
	return out, nil
}

// SchemaVersion is the migration level the database is at.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v)
	return v, err
}

// migrate applies each pending migration in its own transaction, tracked by
// PRAGMA user_version.
func (s *Store) migrate(ctx context.Context) error {
	ms, err := migrations()
	if err != nil {
		return err
	}
	cur, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if cur > len(ms) {
		return fmt.Errorf("database schema version %d is newer than this binary supports (%d)", cur, len(ms))
	}
	for _, m := range ms[cur:] {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
	}
	return nil
}

// ErrNotFound is returned by lookups that match nothing.
var ErrNotFound = errors.New("not found")

// Setting returns a settings value, or ErrNotFound.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetSetting stores a settings value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value", key, value)
	return err
}
