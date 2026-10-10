package report

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/status"
)

// HandleRe is what a tester handle may look like.
var HandleRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,38}$`)

// Text limits, in bytes.
const (
	MaxNotes    = 10000
	MaxEvidence = 4000
	MaxNote     = 1000
	MaxShort    = 200 // one-line fields; longer ones are cut, not rejected
)

// Limits on the lists a report carries.
const (
	maxFixes    = 20
	maxParts    = 10
	maxPartIDs  = 10
	maxFixIDLen = 100
)

// Check runs every check that needs no catalog: the schema, the tester's
// handle, the test date (now bounds it: no future dates), the Omarchy
// version, text sizes, each item's status, method and reason, extras, and
// the context, fixes and replaced parts. It returns Errors listing every
// problem, or nil. The server runs it first, then checks the report against
// the catalog (the configuration, criteria, connectors, live limits and
// fix IDs).
func Check(f *File, now time.Time) error {
	var errs Errors
	bad := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if f.Schema != SchemaV1 {
		bad("schema: %q is not supported (want %q)", f.Schema, SchemaV1)
	}
	if h := strings.TrimSpace(f.Tester.Handle); h != "" && !HandleRe.MatchString(h) {
		bad("tester.handle: %q must be 1–39 letters, digits, '.', '_' or '-'", h)
	}

	// When the test ran: a full timestamp with a time zone.
	if d, err := time.Parse(time.RFC3339, strings.TrimSpace(f.TestedAt)); err != nil {
		bad("tested_at: %q must be a timestamp with a time zone, e.g. 2026-09-30T14:05:00Z or 2026-09-30T10:05:00-04:00", f.TestedAt)
	} else if d.After(now.Add(15 * time.Minute)) {
		bad("tested_at: %s is in the future", f.TestedAt)
	} else if d.UTC().Year() < 2025 {
		bad("tested_at: %s is before Omarchy existed", f.TestedAt)
	}

	if v, err := status.ParseVersion(f.Omarchy.Version); err != nil {
		bad("omarchy.version: %v", err)
	} else if _, err := status.ValidChannel(v, strings.ToLower(strings.TrimSpace(f.Omarchy.Channel))); err != nil {
		bad("omarchy.%v", err)
	}

	if len(f.Notes) > MaxNotes {
		bad("notes: %d bytes; the limit is %d", len(f.Notes), MaxNotes)
	}

	ctx := strings.TrimSpace(f.Context)
	if ctx != "" && !oneOf(ctx, Contexts) {
		bad("context: %q must be one of %s", f.Context, strings.Join(Contexts, ", "))
	}
	live := ctx == ContextLive

	if len(f.Items) == 0 && len(f.Extras) == 0 {
		bad("items: nothing to record (no items and no extras)")
	}
	keys := make([]string, 0, len(f.Items))
	for k := range f.Items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fi := f.Items[key]
		st, method, reason := strings.TrimSpace(fi.Status), strings.TrimSpace(fi.Method), strings.TrimSpace(fi.Reason)
		switch {
		case !oneOf(st, Statuses):
			bad("items.%s.status: %q must be one of %s", key, fi.Status, strings.Join(Statuses, ", "))
		case st == "not_tested":
			if method != "" && !oneOf(method, Methods) {
				bad("items.%s.method: %q must be one of %s", key, fi.Method, strings.Join(Methods, ", "))
			}
			switch {
			case reason != "" && !oneOf(reason, Reasons):
				bad("items.%s.reason: %q must be one of %s", key, fi.Reason, strings.Join(Reasons, ", "))
			case reason == ReasonLiveLimit && !live:
				bad("items.%s.reason: %s is only for live reports (context: live)", key, ReasonLiveLimit)
			}
		default:
			if !oneOf(method, Methods) {
				bad("items.%s.method: %q must be one of %s (required unless not_tested)", key, fi.Method, strings.Join(Methods, ", "))
			}
			if reason != "" {
				bad("items.%s.reason: only not_tested items take a reason", key)
			}
		}
		if len(fi.Note) > MaxNote {
			bad("items.%s.note: %d bytes; the limit is %d", key, len(fi.Note), MaxNote)
		}
		if len(fi.Evidence) > MaxEvidence {
			bad("items.%s.evidence: %d bytes; the limit is %d", key, len(fi.Evidence), MaxEvidence)
		}
	}

	seen := map[string]bool{}
	for i, x := range f.Extras {
		id := strings.TrimSpace(x.ID)
		switch {
		case id == "":
			bad("extras[%d].id: required", i)
		case seen[id]:
			bad("extras[%d].id: %q appears twice", i, id)
		case len(x.Detail) > MaxEvidence:
			bad("extras[%d].detail: %d bytes; the limit is %d", i, len(x.Detail), MaxEvidence)
		}
		seen[id] = true
	}

	if len(f.Fixes) > maxFixes {
		bad("fixes: %d fixes; the limit is %d", len(f.Fixes), maxFixes)
	}
	seen = map[string]bool{}
	for i, id := range f.Fixes {
		id = strings.TrimSpace(id)
		switch {
		case id == "":
			bad("fixes[%d]: empty", i)
		case len(id) > maxFixIDLen:
			bad("fixes[%d]: %d bytes; the limit is %d", i, len(id), maxFixIDLen)
		case seen[id]:
			bad("fixes[%d]: %q appears twice", i, id)
		}
		seen[id] = true
	}

	if len(f.ReplacedParts) > maxParts {
		bad("replaced_parts: %d parts; the limit is %d", len(f.ReplacedParts), maxParts)
	}
	for i, p := range f.ReplacedParts {
		if k := strings.TrimSpace(p.Kind); !oneOf(k, PartKinds) {
			bad("replaced_parts[%d].kind: %q must be one of %s", i, p.Kind, strings.Join(PartKinds, ", "))
		}
		if len(p.Detail) > MaxShort {
			bad("replaced_parts[%d].detail: %d bytes; the limit is %d", i, len(p.Detail), MaxShort)
		}
		if len(p.IDs) > maxPartIDs {
			bad("replaced_parts[%d].ids: %d IDs; the limit is %d", i, len(p.IDs), maxPartIDs)
		}
		for j, id := range p.IDs {
			if id = strings.TrimSpace(id); id == "" || len(id) > MaxShort {
				bad("replaced_parts[%d].ids[%d]: must be 1–%d bytes", i, j, MaxShort)
			}
		}
	}

	if len(f.ConsentNotice) > MaxNote {
		bad("consent_notice: %d bytes; the limit is %d", len(f.ConsentNotice), MaxNote)
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}
