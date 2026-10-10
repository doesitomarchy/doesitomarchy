package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
)

// Result states (PLAN §20.1). Only accepted results count; nothing is deleted.
const (
	Pending   = "pending"
	Accepted  = "accepted"
	Rejected  = "rejected"
	Retracted = "retracted"
)

// transitions lists the moderation moves allowed from each state.
var transitions = map[string][]string{
	Pending:  {Accepted, Rejected},
	Accepted: {Retracted},
}

// ErrState is returned for a moderation move the result's state doesn't allow.
var ErrState = errors.New("not allowed in this state")

// now is the current time in UTC, RFC 3339: every timestamp the store writes.
func now() string { return time.Now().UTC().Format(time.RFC3339) }

// CodeLen is the length of a report's public code.
const CodeLen = 10

// newCode is a report's public identifier: 10 random hex characters. Random,
// not derived from the row ID, so pending reports can't be found by counting.
func newCode() string {
	b := make([]byte, CodeLen/2)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	return hex.EncodeToString(b)
}

// IsCode reports whether s looks like a report code.
func IsCode(s string) bool {
	if len(s) != CodeLen {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// ResultIDByCode finds a report's internal ID from its public code.
func (s *Store) ResultIDByCode(ctx context.Context, code string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, "SELECT id FROM results WHERE code = ?", code).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// InsertResult stores a validated result as pending, with its scrubbed raw
// report, items, extras and flags, and returns its ID. Pending results don't
// change the site, so the data version is not bumped.
func (s *Store) InsertResult(ctx context.Context, r *results.Result, raw []byte, format, actor string) (id int64, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	var revoked string
	if err = tx.QueryRowContext(ctx, "SELECT revoked_at FROM sources WHERE id = ?", r.SourceID).Scan(&revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = fmt.Errorf("source %q is not registered", r.SourceID)
		}
		return 0, err
	}
	if revoked != "" {
		return 0, fmt.Errorf("source %q was revoked on %s", r.SourceID, revoked)
	}
	contact := ""
	if r.Contact != "" {
		var salt string
		if err = tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'contact_salt'").Scan(&salt); err != nil {
			return 0, fmt.Errorf("contact salt: %w", err)
		}
		sum := sha256.Sum256([]byte(salt + "\x00" + strings.ToLower(r.Contact)))
		contact = hex.EncodeToString(sum[:])
	}
	at := now()
	var res sql.Result
	for attempt := 0; ; attempt++ {
		res, err = tx.ExecContext(ctx, `INSERT INTO results (code, config_id, source_id, source_version, profile, workflow, schema,
			format, tester_handle, contact_hash, tested_at, omarchy_version, omarchy_major, omarchy_minor, omarchy_patch,
			omarchy_revision, omarchy_image, kernel, notes, hardware, state, submitted_by, submitted_at, consent_notice, candidates,
			omarchy_channel, omarchy_commit, omarchy_built_at, context, fixes, replaced_parts)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			newCode(), r.ConfigID, r.SourceID, r.SourceVersion, r.Profile, r.Workflow, r.Schema, format, r.TesterHandle, contact,
			r.TestedAt, r.OmarchyRaw, r.Omarchy.Major, r.Omarchy.Minor, r.Omarchy.Patch, r.Revision, r.Image, r.Kernel, r.Notes,
			r.Hardware, Pending, actor, at, r.ConsentNotice, jsonList(r.Candidates), channelOr(r.Channel), r.Commit, r.BuiltAt,
			contextOr(r.Context), jsonList(r.Fixes), jsonParts(r.ReplacedParts))
		if err == nil || attempt == 4 || !strings.Contains(err.Error(), "results.code") {
			break // retry only on the (1 in 10^12) code collision
		}
	}
	if err != nil {
		return 0, err
	}
	if id, err = res.LastInsertId(); err != nil {
		return 0, err
	}
	body := results.Scrub(string(raw))
	if _, err = tx.ExecContext(ctx, "INSERT INTO result_reports (result_id, format, body, size) VALUES (?, ?, ?, ?)",
		id, format, body, len(body)); err != nil {
		return 0, err
	}
	for _, it := range r.Items {
		if _, err = tx.ExecContext(ctx, `INSERT INTO result_items (result_id, capability_id, connector, status, method, reason,
			note, evidence, applicable, ord) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, it.Capability, it.Connector, it.Status, it.Method, it.Reason, it.Note, it.Evidence, b2i(it.Applicable), it.Ord); err != nil {
			return 0, fmt.Errorf("item %s: %w", it.Capability, err)
		}
	}
	for i, x := range r.Extras {
		if _, err = tx.ExecContext(ctx, "INSERT INTO result_extras (result_id, ord, check_id, label, status, detail) VALUES (?, ?, ?, ?, ?, ?)",
			id, i, x.ID, x.Label, x.Status, x.Detail); err != nil {
			return 0, err
		}
	}
	flags := r.Flags
	// The same source, tester, configuration and test time as an earlier report.
	var dup string
	err = tx.QueryRowContext(ctx, `SELECT code FROM results WHERE id <> ? AND source_id = ? AND tester_handle = ? AND config_id = ?
		AND tested_at = ? AND state <> 'rejected' ORDER BY id LIMIT 1`, id, r.SourceID, r.TesterHandle, r.ConfigID, r.TestedAt).Scan(&dup)
	if err == nil {
		flags = append(flags, results.Flag{Kind: results.FlagDuplicate,
			Detail: fmt.Sprintf("same source, tester, configuration and test time as report %s", dup)})
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	for _, f := range flags {
		if _, err = tx.ExecContext(ctx, "INSERT INTO result_flags (result_id, kind, detail) VALUES (?, ?, ?)", id, f.Kind, f.Detail); err != nil {
			return 0, err
		}
	}
	if err = event(ctx, tx, id, actor, "submitted", "source "+r.SourceID); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func event(ctx context.Context, tx *sql.Tx, id int64, actor, action, detail string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO result_events (result_id, at, actor, action, detail) VALUES (?, ?, ?, ?, ?)",
		id, now(), actor, action, detail)
	return err
}

func bumpDataVersion(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "UPDATE settings SET value = CAST(value AS INTEGER) + 1 WHERE key = 'data_version'")
	return err
}

// SetResultState moderates a result: accept or reject a pending one, or
// retract an accepted one. Moves that change what counts bump the data
// version, which tells running servers to rebuild.
func (s *Store) SetResultState(ctx context.Context, id int64, to, reason, actor string) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	var from string
	if err = tx.QueryRowContext(ctx, "SELECT state FROM results WHERE id = ?", id).Scan(&from); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = fmt.Errorf("result %d: %w", id, ErrNotFound)
		}
		return err
	}
	allowed := false
	for _, t := range transitions[from] {
		allowed = allowed || t == to
	}
	if !allowed {
		return fmt.Errorf("result %d is %s; it can't become %s: %w", id, from, to, ErrState)
	}
	if (to == Rejected || to == Retracted) && strings.TrimSpace(reason) == "" {
		return fmt.Errorf("a reason is required to %s a result", strings.TrimSuffix(to, "ed"))
	}
	if to == Accepted {
		var open int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM result_flags WHERE result_id = ? AND kind = 'config_ambiguous' AND resolved_at = ''",
			id).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return fmt.Errorf("result %d fits several configurations; pick one first (-config): %w", id, ErrState)
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE results SET state = ?, state_reason = ?, state_by = ?, state_at = ? WHERE id = ?",
		to, strings.TrimSpace(reason), actor, now(), id); err != nil {
		return err
	}
	if err = event(ctx, tx, id, actor, to, strings.TrimSpace(reason)); err != nil {
		return err
	}
	if to == Accepted || from == Accepted {
		if err = bumpDataVersion(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DataVersion is bumped by every change to what counts (moderation, the
// Unsupported flag). Servers poll it and rebuild when it changes.
func (s *Store) DataVersion(ctx context.Context) (int64, error) {
	v, err := s.Setting(ctx, "data_version")
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

// ResultSummary is one row of a results list.
type ResultSummary struct {
	ID                                    int64
	Code                                  string
	ConfigID, Identifier, Slug            string
	SourceID, SourceName, SourceVersion   string
	Profile, Workflow                     string
	TesterHandle, TestedAt                string
	Omarchy                               string // the canonical version (PLAN §28.1)
	Channel                               string // stable | rc | beta | edge | dev
	Kernel                                string
	Context                               string   // installed | live
	Fixes                                 []string // OmaBoot? fixes that took effect (data/fixes.yaml IDs)
	State, StateReason, StateBy, StateAt  string
	SubmittedBy, SubmittedAt              string
	Supported, Partial, Failed, NotTested int
	OpenFlags                             int
}

// ResultFilter narrows ListResults.
type ResultFilter struct {
	State, Config string
	Limit         int
}

const summaryCols = `r.id, r.code, r.config_id, c.mac_identifier, m.slug, r.source_id, s.name, r.source_version, r.profile, r.workflow,
	r.tester_handle, r.tested_at, r.omarchy_version, r.omarchy_channel, r.kernel, r.state, r.state_reason, r.state_by, r.state_at,
	r.submitted_by, r.submitted_at, r.context, r.fixes,
	(SELECT count(*) FROM result_items i WHERE i.result_id = r.id AND i.status = 'supported'),
	(SELECT count(*) FROM result_items i WHERE i.result_id = r.id AND i.status = 'partial'),
	(SELECT count(*) FROM result_items i WHERE i.result_id = r.id AND i.status = 'failed'),
	(SELECT count(*) FROM result_items i WHERE i.result_id = r.id AND i.status = 'not_tested'),
	(SELECT count(*) FROM result_flags f WHERE f.result_id = r.id AND f.resolved_at = '')
	FROM results r JOIN configs c ON c.id = r.config_id JOIN macs m ON m.identifier = c.mac_identifier
	JOIN sources s ON s.id = r.source_id`

func scanSummary(sc interface{ Scan(...any) error }) (ResultSummary, error) {
	var x ResultSummary
	var fixes string
	err := sc.Scan(&x.ID, &x.Code, &x.ConfigID, &x.Identifier, &x.Slug, &x.SourceID, &x.SourceName, &x.SourceVersion, &x.Profile,
		&x.Workflow, &x.TesterHandle, &x.TestedAt, &x.Omarchy, &x.Channel, &x.Kernel, &x.State, &x.StateReason, &x.StateBy, &x.StateAt,
		&x.SubmittedBy, &x.SubmittedAt, &x.Context, &fixes, &x.Supported, &x.Partial, &x.Failed, &x.NotTested, &x.OpenFlags)
	x.Omarchy = CanonicalVersion(x.Omarchy)
	json.Unmarshal([]byte(fixes), &x.Fixes)
	return x, err
}

// CanonicalVersion is a stored version in its canonical form (PLAN §28.1);
// results stored before 0009 kept the version as reported.
func CanonicalVersion(stored string) string {
	if v, err := status.ParseVersion(stored); err == nil {
		return v.String()
	}
	return stored
}

// IsLive reports whether the result came from a live boot.
func (x ResultSummary) IsLive() bool { return x.Context == "live" }

func contextOr(ctx string) string {
	if ctx == "" {
		return "installed"
	}
	return ctx
}

func jsonParts(ps []results.ReplacedPart) string {
	if len(ps) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ps)
	return string(b)
}

func channelOr(ch string) string {
	if ch == "" {
		return status.Stable
	}
	return ch
}

// ListResults lists results, newest first.
func (s *Store) ListResults(ctx context.Context, f ResultFilter) ([]ResultSummary, error) {
	q, args := "SELECT "+summaryCols+" WHERE 1 = 1", []any{}
	if f.State != "" {
		q, args = q+" AND r.state = ?", append(args, f.State)
	}
	if f.Config != "" {
		q, args = q+" AND r.config_id = ?", append(args, f.Config)
	}
	q += " ORDER BY r.id DESC"
	if f.Limit > 0 {
		q += " LIMIT " + strconv.Itoa(f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResultSummary
	for rows.Next() {
		x, err := scanSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func jsonList(xs []string) string {
	if len(xs) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(xs)
	return string(b)
}

// SetResultConfig settles which configuration a pending report is for
// (after config_ambiguous, or to correct a tool's guess). applicable lists
// the new config's applicable capabilities: items are re-marked, flags for
// the old config are resolved, and new ones raised.
func (s *Store) SetResultConfig(ctx context.Context, id int64, configID string, applicable map[string]bool, actor string) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	var state, oldConfig, oldMac, newMac string
	if err = tx.QueryRowContext(ctx, `SELECT r.state, r.config_id, c.mac_identifier FROM results r JOIN configs c ON c.id = r.config_id
		WHERE r.id = ?`, id).Scan(&state, &oldConfig, &oldMac); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = fmt.Errorf("result %d: %w", id, ErrNotFound)
		}
		return err
	}
	if state != Pending {
		return fmt.Errorf("result %d is %s; only a pending report's configuration can change: %w", id, state, ErrState)
	}
	if err = tx.QueryRowContext(ctx, "SELECT mac_identifier FROM configs WHERE id = ?", configID).Scan(&newMac); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = fmt.Errorf("config %s: %w", configID, ErrNotFound)
		}
		return err
	}
	if newMac != oldMac {
		return fmt.Errorf("config %s is a %s; this report is for a %s", configID, newMac, oldMac)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE results SET config_id = ? WHERE id = ?", configID, id); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.capability_id, cp.name FROM result_items i JOIN capabilities cp ON cp.id = i.capability_id
		WHERE i.result_id = ?`, id)
	if err != nil {
		return err
	}
	type item struct{ id, name string }
	var items []item
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.id, &x.name); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	rows.Close()
	at := now()
	if _, err = tx.ExecContext(ctx, `UPDATE result_flags SET resolution = ?, resolved_by = ?, resolved_at = ?
		WHERE result_id = ? AND resolved_at = '' AND kind IN ('config_ambiguous', 'inapplicable_item')`,
		"configuration set to "+configID, actor, at, id); err != nil {
		return err
	}
	for _, x := range items {
		if _, err = tx.ExecContext(ctx, "UPDATE result_items SET applicable = ? WHERE result_id = ? AND capability_id = ?",
			b2i(applicable[x.id]), id, x.id); err != nil {
			return err
		}
		if !applicable[x.id] {
			if _, err = tx.ExecContext(ctx, "INSERT INTO result_flags (result_id, kind, detail) VALUES (?, 'inapplicable_item', ?)",
				id, fmt.Sprintf("%s (%s) does not apply to this configuration; stored, never counted", x.id, x.name)); err != nil {
				return err
			}
		}
	}
	if err = event(ctx, tx, id, actor, "config-set", oldConfig+" → "+configID); err != nil {
		return err
	}
	return tx.Commit()
}

// Candidates lists the configurations a report's hardware fitted equally.
func (s *Store) Candidates(ctx context.Context, id int64) ([]string, error) {
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT candidates FROM results WHERE id = ?", id).Scan(&raw); err != nil {
		return nil, err
	}
	var out []string
	err := json.Unmarshal([]byte(raw), &out)
	return out, err
}

// ResultItem is a stored result item, with its capability's name and category.
type ResultItem struct {
	Capability, CapabilityName, CategoryID, CategoryName string
	Connector, Status, Method, Reason, Note, Evidence    string
	Applicable                                           bool
}

// ResultFlag is something for a maintainer to review.
type ResultFlag struct {
	ID                                 int64
	ResultID                           int64
	ResultCode                         string
	Kind, Detail                       string
	Resolution, ResolvedBy, ResolvedAt string
}

// ResultEvent is one moderation action.
type ResultEvent struct{ At, Actor, Action, Detail string }

// ResultDetail is everything about one result.
type ResultDetail struct {
	ResultSummary
	OmarchyRevision, OmarchyImage, Notes, Hardware string
	OmarchyCommit, OmarchyBuiltAt                  string // the build tested (PLAN §28.2); "" until looked up
	ConsentNotice                                  string
	Candidates                                     []string               // configs the hardware fitted equally
	ReplacedParts                                  []results.ReplacedPart // parts that aren't the Mac's own
	Items                                          []ResultItem
	Extras                                         []results.FileExtra
	Flags                                          []ResultFlag
	Events                                         []ResultEvent
	ReportSize                                     int
	ReportVisibility                               string
}

// Result loads one result, or ErrNotFound.
func (s *Store) Result(ctx context.Context, id int64) (*ResultDetail, error) {
	sum, err := scanSummary(s.db.QueryRowContext(ctx, "SELECT "+summaryCols+" WHERE r.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d := &ResultDetail{ResultSummary: sum}
	var cands, parts string
	if err := s.db.QueryRowContext(ctx, `SELECT r.omarchy_revision, r.omarchy_image, r.omarchy_commit, r.omarchy_built_at, r.notes,
		r.hardware, r.consent_notice, r.candidates, r.replaced_parts, coalesce(p.size, 0), coalesce(p.visibility, 'private')
		FROM results r LEFT JOIN result_reports p ON p.result_id = r.id WHERE r.id = ?`, id).Scan(
		&d.OmarchyRevision, &d.OmarchyImage, &d.OmarchyCommit, &d.OmarchyBuiltAt, &d.Notes, &d.Hardware, &d.ConsentNotice, &cands, &parts,
		&d.ReportSize, &d.ReportVisibility); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(cands), &d.Candidates)
	json.Unmarshal([]byte(parts), &d.ReplacedParts)
	rows, err := s.db.QueryContext(ctx, `SELECT i.capability_id, cp.name, cp.category_id, k.name, i.connector, i.status, i.method,
		i.reason, i.note, i.evidence, i.applicable
		FROM result_items i JOIN capabilities cp ON cp.id = i.capability_id JOIN categories k ON k.id = cp.category_id
		WHERE i.result_id = ? ORDER BY k.ord, cp.ord, i.connector`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var it ResultItem
		var app int
		if err := rows.Scan(&it.Capability, &it.CapabilityName, &it.CategoryID, &it.CategoryName, &it.Connector, &it.Status,
			&it.Method, &it.Reason, &it.Note, &it.Evidence, &app); err != nil {
			rows.Close()
			return nil, err
		}
		it.Applicable = app == 1
		d.Items = append(d.Items, it)
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, "SELECT check_id, label, status, detail FROM result_extras WHERE result_id = ? ORDER BY ord", id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var x results.FileExtra
		if err := rows.Scan(&x.ID, &x.Label, &x.Status, &x.Detail); err != nil {
			rows.Close()
			return nil, err
		}
		d.Extras = append(d.Extras, x)
	}
	rows.Close()
	if d.Flags, err = s.flags(ctx, "WHERE f.result_id = ?", id); err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, "SELECT at, actor, action, detail FROM result_events WHERE result_id = ? ORDER BY id", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e ResultEvent
		if err := rows.Scan(&e.At, &e.Actor, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		d.Events = append(d.Events, e)
	}
	return d, rows.Err()
}

// ResultReport returns a result's scrubbed raw submission.
func (s *Store) ResultReport(ctx context.Context, id int64) (format, body string, err error) {
	err = s.db.QueryRowContext(ctx, "SELECT format, body FROM result_reports WHERE result_id = ?", id).Scan(&format, &body)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return format, body, err
}

func (s *Store) flags(ctx context.Context, where string, args ...any) ([]ResultFlag, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, f.result_id, r.code, f.kind, f.detail, f.resolution, f.resolved_by, f.resolved_at
		FROM result_flags f JOIN results r ON r.id = f.result_id `+where+" ORDER BY f.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResultFlag
	for rows.Next() {
		var f ResultFlag
		if err := rows.Scan(&f.ID, &f.ResultID, &f.ResultCode, &f.Kind, &f.Detail, &f.Resolution, &f.ResolvedBy, &f.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Flags lists review flags; openOnly hides resolved ones.
func (s *Store) Flags(ctx context.Context, openOnly bool) ([]ResultFlag, error) {
	if openOnly {
		return s.flags(ctx, "WHERE f.resolved_at = ''")
	}
	return s.flags(ctx, "")
}

// ResolveFlag records how a maintainer dealt with a flag.
func (s *Store) ResolveFlag(ctx context.Context, id int64, note, actor string) (err error) {
	if strings.TrimSpace(note) == "" {
		return errors.New("a note is required to resolve a flag")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	var resultID int64
	var resolved string
	if err = tx.QueryRowContext(ctx, "SELECT result_id, resolved_at FROM result_flags WHERE id = ?", id).Scan(&resultID, &resolved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = fmt.Errorf("flag %d: %w", id, ErrNotFound)
		}
		return err
	}
	if resolved != "" {
		return fmt.Errorf("flag %d was already resolved on %s: %w", id, resolved, ErrState)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE result_flags SET resolution = ?, resolved_by = ?, resolved_at = ? WHERE id = ?",
		strings.TrimSpace(note), actor, now(), id); err != nil {
		return err
	}
	if err = event(ctx, tx, resultID, actor, "flag-resolved", fmt.Sprintf("flag %d: %s", id, strings.TrimSpace(note))); err != nil {
		return err
	}
	return tx.Commit()
}

// Source is an app registered to submit results.
type Source struct {
	ID, Name, Homepage, Trust, CreatedAt, RevokedAt string
	RepoURL, ContactEmail                           string // from registration (PLAN §30d)
	HasKey                                          bool
}

// Sources lists registered sources.
func (s *Store) Sources(ctx context.Context) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, homepage, trust, created_at, revoked_at, repo_url, contact_email, key_hash <> '' FROM sources ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		var x Source
		if err := rows.Scan(&x.ID, &x.Name, &x.Homepage, &x.Trust, &x.CreatedAt, &x.RevokedAt, &x.RepoURL, &x.ContactEmail, &x.HasKey); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SetUnsupported raises the maintainer's white flag for a capability on one
// config or on every config with a component, with a reason (PLAN §20.1).
func (s *Store) SetUnsupported(ctx context.Context, capability, configID, componentID, reason, actor string) (err error) {
	if (configID == "") == (componentID == "") {
		return errors.New("give exactly one of a config or a component")
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("a reason is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `INSERT INTO unsupported (capability_id, config_id, component_id, reason, set_by, set_at)
		VALUES (?, ?, ?, ?, ?, ?)`, capability, configID, componentID, strings.TrimSpace(reason), actor, now()); err != nil {
		return err
	}
	if err = bumpDataVersion(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Rollup is everything the status engine needs from the results tables.
type Rollup struct {
	Items   map[string][]status.Item // config → accepted, applicable, tested items, from every channel
	Results map[string]int           // config → accepted results
	Latest  map[string]string        // config → newest accepted test date
	// The same, counting stable results only: the verdict's view (PLAN §28.2).
	StableResults map[string]int
	StableLatest  map[string]string
	Accepted      map[string][]ResultSummary   // config → accepted (and retracted) results, newest first
	Unsupported   map[string]map[string]string // config → capability → reason
	CurrentMajor  int
	Version       int64 // the data version this was read at
}

var verdictOf = map[string]status.Verdict{"supported": status.Supported, "partial": status.Partial, "failed": status.Failed}

// RollupData reads accepted results for the status engine, in one snapshot.
func (s *Store) RollupData(ctx context.Context) (*Rollup, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	r := &Rollup{Items: map[string][]status.Item{}, Results: map[string]int{}, Latest: map[string]string{},
		StableResults: map[string]int{}, StableLatest: map[string]string{},
		Accepted: map[string][]ResultSummary{}, Unsupported: map[string]map[string]string{}}
	var major, version string
	if err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'current_omarchy_major'").Scan(&major); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'data_version'").Scan(&version); err != nil {
		return nil, err
	}
	r.CurrentMajor, _ = strconv.Atoi(major)
	r.Version, _ = strconv.ParseInt(version, 10, 64)

	rows, err := tx.QueryContext(ctx, `SELECT r.id, r.config_id, i.capability_id, i.connector, i.status, i.method, i.evidence,
		r.omarchy_version, r.omarchy_major, r.omarchy_minor, r.omarchy_patch, r.omarchy_channel, r.omarchy_built_at, r.tested_at
		FROM result_items i JOIN results r ON r.id = i.result_id
		WHERE r.state = 'accepted' AND i.applicable = 1 AND i.status <> 'not_tested'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var it status.Item
		var cfg, st, raw string
		if err := rows.Scan(&it.ResultID, &cfg, &it.Capability, &it.Connector, &st, &it.Method, &it.Evidence,
			&raw, &it.Omarchy.Major, &it.Omarchy.Minor, &it.Omarchy.Patch, &it.Channel, &it.BuiltAt, &it.TestedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if v, err := status.ParseVersion(raw); err == nil {
			it.Omarchy = v
		}
		if it.BuiltAt == "" {
			it.BuiltAt = it.TestedAt // the build can't be newer than the test (PLAN §28.2)
		}
		it.Verdict = verdictOf[st]
		r.Items[cfg] = append(r.Items[cfg], it)
	}
	rows.Close()

	rows, err = tx.QueryContext(ctx, "SELECT "+summaryCols+" WHERE r.state IN ('accepted', 'retracted') ORDER BY r.tested_at DESC, r.id DESC")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		x, err := scanSummary(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		r.Accepted[x.ConfigID] = append(r.Accepted[x.ConfigID], x)
		if x.State == Accepted {
			r.Results[x.ConfigID]++
			if x.TestedAt > r.Latest[x.ConfigID] {
				r.Latest[x.ConfigID] = x.TestedAt
			}
			if x.Channel == status.Stable {
				r.StableResults[x.ConfigID]++
				if x.TestedAt > r.StableLatest[x.ConfigID] {
					r.StableLatest[x.ConfigID] = x.TestedAt
				}
			}
		}
	}
	rows.Close()

	// The white flag, per config: set directly, or through a component the config has.
	rows, err = tx.QueryContext(ctx, `SELECT u.config_id, u.capability_id, u.reason FROM unsupported u
		WHERE u.cleared_at = '' AND u.config_id <> ''
		UNION ALL
		SELECT cc.config_id, u.capability_id, u.reason FROM unsupported u
		JOIN config_components cc ON cc.component_id = u.component_id
		WHERE u.cleared_at = '' AND u.component_id <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cfg, cp, reason string
		if err := rows.Scan(&cfg, &cp, &reason); err != nil {
			return nil, err
		}
		if r.Unsupported[cfg] == nil {
			r.Unsupported[cfg] = map[string]string{}
		}
		r.Unsupported[cfg][cp] = reason
	}
	return r, rows.Err()
}
