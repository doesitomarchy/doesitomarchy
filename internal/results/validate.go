package results

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
)

// Statuses, methods and skip reasons a result item may carry.
var (
	Statuses = []string{"supported", "partial", "failed", "not_tested"}
	Methods  = []string{"automatic", "observed", "fixture"}
	Reasons  = []string{"no-equipment", "not-in-profile", "uncertain", "other"}
)

// Flag kinds raised for maintainers to review.
const (
	FlagInapplicable = "inapplicable_item"
	FlagHardware     = "hardware_mismatch"
	FlagUnmapped     = "unmapped_check"
	FlagConflict     = "conflict"
)

// Result is a validated, scrubbed submission, ready to store.
type Result struct {
	Schema        string
	ConfigID      string // canonical (an alias in the file is resolved)
	Identifier    string // the config's model identifier
	SourceID      string
	SourceVersion string
	Profile       string
	Workflow      string
	TesterHandle  string
	Contact       string // raw; the store hashes it and never keeps it
	TestedOn      string
	Omarchy       status.Version
	OmarchyRaw    string
	Revision      string
	Image         string
	Kernel        string
	Notes         string
	Hardware      string // scrubbed JSON
	Items         []Item
	Extras        []FileExtra
	Flags         []Flag
}

// Item is one validated result item.
type Item struct {
	Capability string
	Connector  string
	Status     string
	Method     string
	Reason     string
	Note       string
	Evidence   string
	Applicable bool
	Ord        int
}

// Flag is something a maintainer should look at before accepting.
type Flag struct{ Kind, Detail string }

// Errors collects every problem in a submission, so a tool author sees them
// all at once instead of one per attempt.
type Errors []string

func (e Errors) Error() string {
	return "invalid result:\n  - " + strings.Join(e, "\n  - ")
}

var handleRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,38}$`)

// Text limits, in bytes.
const (
	maxNotes    = 10000
	maxEvidence = 4000
	maxNote     = 1000
	maxShort    = 200
)

// Validate checks a parsed submission against the catalog and returns the
// canonical, scrubbed result. now bounds tested_on (no future dates).
func Validate(f *File, c *catalog.Catalog, now time.Time) (*Result, error) {
	var errs Errors
	bad := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if f.Schema != SchemaV1 {
		bad("schema: %q is not supported (want %q)", f.Schema, SchemaV1)
	}
	r := &Result{Schema: SchemaV1, SourceID: strings.TrimSpace(f.Source.ID)}
	if r.SourceID == "" {
		r.SourceID = "manual"
	}
	r.SourceVersion, r.Profile, r.Workflow = short(f.Source.Version), short(f.Source.Profile), short(f.Source.Workflow)

	// The configuration (an old ID listed in a config's aliases also resolves).
	m, cfg := findConfig(c, strings.TrimSpace(f.Config))
	if cfg == nil {
		if f.Config == "" {
			bad("config: required (a configuration ID, e.g. macbookpro15-2-13-2018-4tb3-a)")
		} else {
			bad("config: %q is not a known configuration ID", f.Config)
		}
	} else {
		r.ConfigID, r.Identifier = cfg.ID, m.Identifier
	}

	if h := strings.TrimSpace(f.Tester.Handle); h != "" {
		if !handleRe.MatchString(h) {
			bad("tester.handle: %q must be 1–39 letters, digits, '.', '_' or '-'", h)
		}
		r.TesterHandle = h
	}
	r.Contact = strings.TrimSpace(f.Tester.Contact)

	if d, err := time.Parse("2006-01-02", strings.TrimSpace(f.TestedOn)); err != nil {
		bad("tested_on: %q must be a date (YYYY-MM-DD)", f.TestedOn)
	} else if d.After(now.Add(36 * time.Hour)) {
		bad("tested_on: %s is in the future", f.TestedOn)
	} else if d.Year() < 2025 {
		bad("tested_on: %s is before Omarchy existed", f.TestedOn)
	} else {
		r.TestedOn = d.Format("2006-01-02")
	}

	if v, err := status.ParseVersion(f.Omarchy.Version); err != nil {
		bad("omarchy.version: %v", err)
	} else {
		r.Omarchy, r.OmarchyRaw = v, strings.TrimSpace(f.Omarchy.Version)
	}
	r.Revision, r.Image, r.Kernel = short(f.Omarchy.Revision), short(f.Omarchy.Image), short(f.Kernel)

	if len(f.Notes) > maxNotes {
		bad("notes: %d bytes; the limit is %d", len(f.Notes), maxNotes)
	}
	r.Notes = Scrub(strings.TrimSpace(f.Notes))

	hw := map[string]any{}
	if f.Hardware != nil {
		hw = ScrubValue(f.Hardware).(map[string]any)
	}
	b, _ := json.Marshal(hw)
	r.Hardware = string(b)

	if len(f.Items) == 0 && len(f.Extras) == 0 {
		bad("items: nothing to record (no items and no extras)")
	}

	// Items, in catalog order so results read like the criteria list.
	order, byID := map[string]int{}, map[string]catalog.Capability{}
	for i, cp := range c.Capabilities {
		order[cp.ID], byID[cp.ID] = i, cp
	}
	applies := map[string]bool{}
	if cfg != nil {
		for _, cp := range c.Applicable(m, cfg) {
			applies[cp.ID] = true
		}
	}
	keys := make([]string, 0, len(f.Items))
	for k := range f.Items {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		oi, iok := order[keys[i]]
		oj, jok := order[keys[j]]
		if iok != jok {
			return iok
		}
		if oi != oj {
			return oi < oj
		}
		return keys[i] < keys[j]
	})
	for _, id := range keys {
		fi := f.Items[id]
		cp, known := byID[id]
		if !known {
			if strings.Contains(id, "@") {
				bad("items.%s: per-connector items are not accepted yet (they arrive with structured port layouts)", id)
			} else {
				bad("items.%s: unknown capability (see /api/v1/capabilities, or send it as an extra)", id)
			}
			continue
		}
		it := Item{Capability: id, Status: strings.TrimSpace(fi.Status), Method: strings.TrimSpace(fi.Method),
			Reason: strings.TrimSpace(fi.Reason), Ord: order[id]}
		switch {
		case !oneOf(it.Status, Statuses):
			bad("items.%s.status: %q must be one of %s", id, fi.Status, strings.Join(Statuses, ", "))
		case it.Status == "not_tested":
			if it.Method != "" && !oneOf(it.Method, Methods) {
				bad("items.%s.method: %q must be one of %s", id, fi.Method, strings.Join(Methods, ", "))
			}
			if it.Reason != "" && !oneOf(it.Reason, Reasons) {
				bad("items.%s.reason: %q must be one of %s", id, fi.Reason, strings.Join(Reasons, ", "))
			}
			it.Method = ""
		default:
			if !oneOf(it.Method, Methods) {
				bad("items.%s.method: %q must be one of %s (required unless not_tested)", id, fi.Method, strings.Join(Methods, ", "))
			}
			if it.Reason != "" {
				bad("items.%s.reason: only not_tested items take a reason", id)
			}
		}
		if len(fi.Note) > maxNote {
			bad("items.%s.note: %d bytes; the limit is %d", id, len(fi.Note), maxNote)
		}
		if len(fi.Evidence) > maxEvidence {
			bad("items.%s.evidence: %d bytes; the limit is %d", id, len(fi.Evidence), maxEvidence)
		}
		it.Note, it.Evidence = Scrub(strings.TrimSpace(fi.Note)), Scrub(strings.TrimSpace(fi.Evidence))
		it.Applicable = applies[id]
		if cfg != nil && !it.Applicable {
			why := "does not apply to this configuration"
			if cp.Retired {
				why = "is retired"
			}
			r.Flags = append(r.Flags, Flag{FlagInapplicable, fmt.Sprintf("%s (%s) %s; stored, never counted", id, cp.Name, why)})
		}
		r.Items = append(r.Items, it)
	}

	seen := map[string]bool{}
	for i, x := range f.Extras {
		x.ID, x.Label, x.Status = strings.TrimSpace(x.ID), short(x.Label), short(x.Status)
		switch {
		case x.ID == "":
			bad("extras[%d].id: required", i)
		case seen[x.ID]:
			bad("extras[%d].id: %q appears twice", i, x.ID)
		case len(x.Detail) > maxEvidence:
			bad("extras[%d].detail: %d bytes; the limit is %d", i, len(x.Detail), maxEvidence)
		}
		seen[x.ID] = true
		x.Detail = Scrub(strings.TrimSpace(x.Detail))
		r.Extras = append(r.Extras, x)
	}

	if len(errs) > 0 {
		return nil, errs
	}
	return r, nil
}

// IsValidation reports whether err lists problems with the submission itself
// (as opposed to a storage failure).
func IsValidation(err error) bool {
	var e Errors
	return errors.As(err, &e)
}

func findConfig(c *catalog.Catalog, id string) (*catalog.Mac, *catalog.Config) {
	if id == "" {
		return nil, nil
	}
	for _, m := range c.Macs {
		for ri := range m.Releases {
			for ci := range m.Releases[ri].Configs {
				cfg := &m.Releases[ri].Configs[ci]
				if cfg.ID == id || oneOf(id, cfg.Aliases) {
					return m, cfg
				}
			}
		}
	}
	return nil, nil
}

func oneOf(s string, set []string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}

// short trims a one-line field, scrubs it and caps its length.
func short(s string) string {
	s = Scrub(strings.TrimSpace(s))
	if len(s) > maxShort {
		s = strings.ToValidUTF8(s[:maxShort], "") // never split a character
	}
	return s
}
