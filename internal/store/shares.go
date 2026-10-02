package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// PF-3 (PLAN.md §23.3): hardware IDs a visitor chose to share from Identify
// my Mac. Nothing here links a share to a person: no IP address, no time of
// day, no free text.

// Share is one shared set of IDs.
type Share struct {
	ID         int64
	SharedOn   string // YYYY-MM-DD, UTC
	Product    string // the model identifier as reported; may be one the catalog lacks
	BoardID    string
	CPU        string
	PCI        []string // "vvvv:dddd", lower-case, sorted
	Modified   string   // "", yes, no, unsure
	Release    string   // a release ID of the identifier, or ""
	ReviewedAt string
	ReviewedBy string
}

// Limits on what a share may hold.
const (
	MaxSharePCI = 64
	maxShareCPU = 200
)

// Answers to "Has this Mac been modified?".
var ShareModified = []string{"yes", "no", "unsure"}

var (
	shareProductRe = regexp.MustCompile(`^[A-Za-z]{2,24}\d{1,2},\d{1,2}$`)
	shareBoardRe   = regexp.MustCompile(`^Mac-(?:[0-9A-F]{16}|[0-9A-F]{8})$`)
	sharePCIRe     = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{4}$`)
	shareReleaseRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)
)

// Normalize tidies a share's fields into their stored form.
func (sh *Share) Normalize() {
	sh.Product = strings.TrimSpace(sh.Product)
	if b := strings.TrimSpace(sh.BoardID); len(b) > 4 && strings.EqualFold(b[:4], "Mac-") {
		sh.BoardID = "Mac-" + strings.ToUpper(b[4:])
	} else {
		sh.BoardID = b
	}
	sh.CPU = strings.Join(strings.Fields(sh.CPU), " ")
	seen := map[string]bool{}
	var pci []string
	for _, id := range sh.PCI {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" && !seen[id] {
			seen[id] = true
			pci = append(pci, id)
		}
	}
	sort.Strings(pci)
	sh.PCI = pci
	sh.Modified = strings.TrimSpace(sh.Modified)
	sh.Release = strings.TrimSpace(sh.Release)
}

// Validate checks a normalised share. It doesn't check that the release
// belongs to the identifier; the caller knows the catalog.
func (sh *Share) Validate() error {
	var errs []string
	if sh.Product == "" && sh.BoardID == "" && len(sh.PCI) == 0 {
		errs = append(errs, "nothing to share")
	}
	if sh.Product != "" && !shareProductRe.MatchString(sh.Product) {
		errs = append(errs, "model identifier")
	}
	if sh.BoardID != "" && !shareBoardRe.MatchString(sh.BoardID) {
		errs = append(errs, "board ID")
	}
	if len(sh.CPU) > maxShareCPU || strings.IndexFunc(sh.CPU, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		errs = append(errs, "CPU name")
	}
	if len(sh.PCI) > MaxSharePCI {
		errs = append(errs, fmt.Sprintf("more than %d device IDs", MaxSharePCI))
	}
	for _, id := range sh.PCI {
		if !sharePCIRe.MatchString(id) {
			errs = append(errs, "device ID "+id)
			break
		}
	}
	if sh.Modified != "" && !contains(ShareModified, sh.Modified) {
		errs = append(errs, "modified answer")
	}
	if sh.Release != "" && (!shareReleaseRe.MatchString(sh.Release) || sh.Product == "") {
		errs = append(errs, "release")
	}
	if len(errs) > 0 {
		return errors.New("invalid share: " + strings.Join(errs, ", "))
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// hash identifies a share's content, for spotting repeats.
func (sh *Share) hash() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{sh.Product, sh.BoardID, sh.CPU, strings.Join(sh.PCI, ","), sh.Modified, sh.Release}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// Today is the UTC date shares are stored under.
func Today() string { return time.Now().UTC().Format("2006-01-02") }

// AddShare stores a share under today's date. added is false when the same
// share was already stored today.
func (s *Store) AddShare(ctx context.Context, sh Share) (added bool, err error) {
	sh.Normalize()
	if err := sh.Validate(); err != nil {
		return false, err
	}
	pci, _ := json.Marshal(sh.PCI)
	if sh.PCI == nil {
		pci = []byte("[]")
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO id_shares (shared_on, probe_hash, product, board_id, cpu, pci, modified, release_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (probe_hash, shared_on) DO NOTHING`,
		Today(), sh.hash(), sh.Product, sh.BoardID, sh.CPU, string(pci), sh.Modified, sh.Release)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// SharesOn counts the shares stored on a date (YYYY-MM-DD).
func (s *Store) SharesOn(ctx context.Context, day string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM id_shares WHERE shared_on = ?", day).Scan(&n)
	return n, err
}

// ShareGroup is the shares for one reported model identifier ("" when none
// was reported).
type ShareGroup struct {
	Product  string
	Shares   []Share // newest first
	Unseen   int     // not yet reviewed
	Reviewed string  // when the group was last marked reviewed
}

// ShareGroups lists shares grouped by reported identifier. Without all, only
// groups with unreviewed shares are listed (with all their shares).
func (s *Store) ShareGroups(ctx context.Context, all bool) ([]ShareGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, shared_on, product, board_id, cpu, pci, modified, release_id, reviewed_at, reviewed_by
		FROM id_shares ORDER BY product, shared_on DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var groups []ShareGroup
	for rows.Next() {
		var sh Share
		var pci string
		if err := rows.Scan(&sh.ID, &sh.SharedOn, &sh.Product, &sh.BoardID, &sh.CPU, &pci, &sh.Modified, &sh.Release, &sh.ReviewedAt, &sh.ReviewedBy); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(pci), &sh.PCI); err != nil {
			return nil, fmt.Errorf("share %d: %w", sh.ID, err)
		}
		if len(groups) == 0 || groups[len(groups)-1].Product != sh.Product {
			groups = append(groups, ShareGroup{Product: sh.Product})
		}
		g := &groups[len(groups)-1]
		g.Shares = append(g.Shares, sh)
		if sh.ReviewedAt == "" {
			g.Unseen++
		} else if sh.ReviewedAt > g.Reviewed {
			g.Reviewed = sh.ReviewedAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !all {
		kept := groups[:0]
		for _, g := range groups {
			if g.Unseen > 0 {
				kept = append(kept, g)
			}
		}
		groups = kept
	}
	return groups, nil
}

// ReviewShares marks every unreviewed share of a reported identifier as
// reviewed ("" is the group with no identifier). It returns how many it marked.
func (s *Store) ReviewShares(ctx context.Context, product, who string) (int64, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE id_shares SET reviewed_at = ?, reviewed_by = ? WHERE product = ? AND reviewed_at = ''",
		now(), who, product)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
