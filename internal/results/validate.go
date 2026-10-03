package results

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/match"
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
	FlagAmbiguous    = "config_ambiguous"
	FlagDuplicate    = "duplicate"
	// FlagDriverMissing: a native report said a device or its driver was
	// missing, which counts as failed (PLAN §22.10); confirm it isn't absent hardware.
	FlagDriverMissing = "driver_missing"
	// FlagPortSuspect: a connector failed while another in its port group
	// passed, which points to a damaged port rather than Omarchy (PLAN §25.1a).
	FlagPortSuspect = "port_suspect"
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
	TestedAt      string // RFC 3339, UTC
	Omarchy       status.Version
	OmarchyRaw    string
	Revision      string
	Image         string
	Kernel        string
	Notes         string
	Hardware      string   // scrubbed JSON
	Candidates    []string // when the hardware fits several configs: all of them (ConfigID is the first)
	ConsentNotice string
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
// canonical, scrubbed result. now bounds tested_at (no future dates).
func Validate(f *File, c *catalog.Catalog, now time.Time) (*Result, error) {
	var errs Errors
	bad := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if f.Schema != SchemaV1 {
		bad("schema: %q is not supported (want %q)", f.Schema, SchemaV1)
	}
	r := &Result{Schema: SchemaV1, SourceID: strings.ToLower(strings.TrimSpace(f.Source.ID))}
	if r.SourceID == "" {
		r.SourceID = "manual"
	}
	r.SourceVersion, r.Profile, r.Workflow = short(f.Source.Version), short(f.Source.Profile), short(f.Source.Workflow)

	// The configuration: given by ID (an old ID listed in a config's aliases
	// also resolves), or found from the identifier and hardware probe.
	probe := probeOf(f)
	configID := strings.TrimSpace(f.Config)
	if configID == "" && (probe.ProductName != "" || probe.BoardID != "") {
		mr := matcherFor(c).Match(probe)
		switch {
		case len(mr.Candidates) == 0:
			bad("config: no configuration matches this hardware (identifier %q, board %q); send a config ID", probe.ProductName, probe.BoardID)
		case mr.Exact:
			configID = mr.Best()
		default:
			top := mr.Candidates[0].Score
			for _, cd := range mr.Candidates {
				if cd.Score == top {
					r.Candidates = append(r.Candidates, cd.Config)
				}
			}
			configID = r.Candidates[0]
			r.Flags = append(r.Flags, Flag{FlagAmbiguous, fmt.Sprintf("the hardware fits %d configurations equally: %s; a maintainer picks one before accepting",
				len(r.Candidates), strings.Join(r.Candidates, ", "))})
		}
	}
	m, cfg := findConfig(c, configID)
	switch {
	case cfg != nil:
		r.ConfigID, r.Identifier = cfg.ID, m.Identifier
		if id := strings.TrimSpace(f.Identifier); id != "" && !strings.EqualFold(id, m.Identifier) {
			bad("identifier: %q, but config %s is a %s", id, cfg.ID, m.Identifier)
		}
		if f.Config != "" {
			r.Flags = append(r.Flags, mismatches(c, m, cfg, probe)...)
		}
	case configID == "" && f.Config == "" && len(errs) == 0:
		bad("config: required (a configuration ID such as macbookpro15-2-13-2018-4tb3-a, or an identifier and hardware probe)")
	case f.Config != "":
		bad("config: %q is not a known configuration ID", f.Config)
	}

	if h := strings.TrimSpace(f.Tester.Handle); h != "" {
		if !handleRe.MatchString(h) {
			bad("tester.handle: %q must be 1–39 letters, digits, '.', '_' or '-'", h)
		}
		r.TesterHandle = h
	}
	r.Contact = strings.TrimSpace(f.Tester.Contact)

	// When the test ran: a full timestamp with a time zone, stored in UTC.
	if d, err := time.Parse(time.RFC3339, strings.TrimSpace(f.TestedAt)); err != nil {
		bad("tested_at: %q must be a timestamp with a time zone, e.g. 2026-09-30T14:05:00Z or 2026-09-30T10:05:00-04:00", f.TestedAt)
	} else if d.After(now.Add(15 * time.Minute)) {
		bad("tested_at: %s is in the future", f.TestedAt)
	} else if d.UTC().Year() < 2025 {
		bad("tested_at: %s is before Omarchy existed", f.TestedAt)
	} else {
		r.TestedAt = d.UTC().Format(time.RFC3339)
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
	capOf := func(k string) string { c, _, _ := strings.Cut(k, "@"); return c }
	sort.Slice(keys, func(i, j int) bool {
		oi, iok := order[capOf(keys[i])]
		oj, jok := order[capOf(keys[j])]
		if iok != jok {
			return iok
		}
		if oi != oj {
			return oi < oj
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		fi := f.Items[key]
		id, conn, perConnector := strings.Cut(key, "@")
		cp, known := byID[id]
		if !known {
			bad("items.%s: unknown capability (see /api/v1/capabilities, or send it as an extra)", key)
			continue
		}
		if perConnector {
			// capability@connector (PLAN §25): the connector must be in the
			// configuration's port layout and take this criterion.
			switch {
			case cfg == nil:
				continue // the config error is already reported
			case len(cfg.Connectors) == 0:
				bad("items.%s: %s has no port layout yet, so per-connector items can't be placed; send %s without a connector", key, cfg.ID, id)
				continue
			}
			var found *catalog.Connector
			for i := range cfg.Connectors {
				if cfg.Connectors[i].ID == conn {
					found = &cfg.Connectors[i]
				}
			}
			if found == nil {
				var ids []string
				for _, cn := range cfg.Connectors {
					ids = append(ids, cn.ID)
				}
				bad("items.%s: %s has no connector %q (it has %s)", key, cfg.ID, conn, strings.Join(ids, ", "))
				continue
			}
			if !oneOf(id, c.ConnectorCriteria(m, cfg, *found)) {
				bad("items.%s: connector %s (%s) isn't tested for %s", key, conn, c.ConnectorName(*found), id)
				continue
			}
		}
		it := Item{Capability: id, Connector: conn, Status: strings.TrimSpace(fi.Status), Method: strings.TrimSpace(fi.Method),
			Reason: strings.TrimSpace(fi.Reason), Ord: order[id]}
		switch {
		case !oneOf(it.Status, Statuses):
			bad("items.%s.status: %q must be one of %s", key, fi.Status, strings.Join(Statuses, ", "))
		case it.Status == "not_tested":
			if it.Method != "" && !oneOf(it.Method, Methods) {
				bad("items.%s.method: %q must be one of %s", key, fi.Method, strings.Join(Methods, ", "))
			}
			if it.Reason != "" && !oneOf(it.Reason, Reasons) {
				bad("items.%s.reason: %q must be one of %s", key, fi.Reason, strings.Join(Reasons, ", "))
			}
			it.Method = ""
		default:
			if !oneOf(it.Method, Methods) {
				bad("items.%s.method: %q must be one of %s (required unless not_tested)", key, fi.Method, strings.Join(Methods, ", "))
			}
			if it.Reason != "" {
				bad("items.%s.reason: only not_tested items take a reason", key)
			}
		}
		if len(fi.Note) > maxNote {
			bad("items.%s.note: %d bytes; the limit is %d", key, len(fi.Note), maxNote)
		}
		if len(fi.Evidence) > maxEvidence {
			bad("items.%s.evidence: %d bytes; the limit is %d", key, len(fi.Evidence), maxEvidence)
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

	if cfg != nil {
		r.Flags = append(r.Flags, portSuspects(c, m, cfg, r.Items)...)
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

	if len(f.ConsentNotice) > maxNote {
		bad("consent_notice: %d bytes; the limit is %d", len(f.ConsentNotice), maxNote)
	}
	r.ConsentNotice = strings.TrimSpace(f.ConsentNotice)

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

// matchers caches one matcher per loaded catalog.
var matchers sync.Map // *catalog.Catalog → *match.Matcher

func matcherFor(c *catalog.Catalog) *match.Matcher {
	if m, ok := matchers.Load(c); ok {
		return m.(*match.Matcher)
	}
	m, _ := matchers.LoadOrStore(c, match.New(c))
	return m.(*match.Matcher)
}

// probeOf reads the standard probe keys from a submission's hardware block
// (PLAN §22.2): product_name, board_id, pci and usb. The top-level
// identifier stands in for a missing product_name.
func probeOf(f *File) match.Probe { return ProbeFromHardware(f.Hardware, f.Identifier) }

// ProbeFromHardware reads the standard probe keys from a report's hardware
// map; identifier stands in for a missing product_name.
func ProbeFromHardware(hw map[string]any, identifier string) match.Probe {
	str := func(k string) string {
		s, _ := hw[k].(string)
		return strings.TrimSpace(s)
	}
	list := func(k string) []string {
		var out []string
		switch v := hw[k].(type) {
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok {
					out = append(out, s)
				}
			}
		case string:
			out = append(out, v)
		}
		return out
	}
	p := match.Probe{ProductName: str("product_name"), BoardID: str("board_id"), PCI: list("pci"), USB: list("usb"), CPU: str("cpu")}
	if p.ProductName == "" {
		p.ProductName = strings.TrimSpace(identifier)
	}
	return p
}

// mismatches flags a probe that disagrees with the config a report names
// (PLAN §15): another identifier or board, or a GPU or Wi-Fi chip the config
// doesn't have. The report still counts once accepted.
func mismatches(c *catalog.Catalog, m *catalog.Mac, cfg *catalog.Config, p match.Probe) []Flag {
	var out []Flag
	if p.ProductName != "" && !strings.EqualFold(p.ProductName, m.Identifier) {
		out = append(out, Flag{FlagHardware, fmt.Sprintf("the probe says %s; the config is a %s", p.ProductName, m.Identifier)})
	}
	if p.BoardID != "" {
		known := false
		for _, b := range m.BoardIDs {
			known = known || strings.EqualFold(b, p.BoardID)
		}
		if !known {
			out = append(out, Flag{FlagHardware, fmt.Sprintf("board ID %s isn't one of %s's (%s)", p.BoardID, m.Identifier, strings.Join(m.BoardIDs, ", "))})
		}
	}
	pci := match.NormalizeIDs(p.PCI, "pci")
	if len(pci) == 0 {
		return out
	}
	have := map[string]bool{}
	for _, id := range pci {
		have[id] = true
	}
	for _, ref := range cfg.Components {
		comp := c.Components[ref]
		if comp == nil || (comp.Kind != "gpu" && comp.Kind != "wifi") || len(comp.IDs) == 0 {
			continue
		}
		found := false
		for _, id := range comp.IDs {
			found = found || have[id]
		}
		if !found {
			out = append(out, Flag{FlagHardware, fmt.Sprintf("the probe lists no %s (%s) of this config", comp.Name, strings.Join(comp.IDs, ", "))})
		}
	}
	return out
}

// FindConfig looks a configuration up by ID or alias.
func FindConfig(c *catalog.Catalog, id string) (*catalog.Mac, *catalog.Config) {
	return findConfig(c, id)
}

// ApplicableSet lists the capabilities that apply to a configuration (for
// re-marking a report's items when its configuration changes).
func ApplicableSet(c *catalog.Catalog, configID string) (map[string]bool, error) {
	m, cfg := findConfig(c, configID)
	if cfg == nil {
		return nil, fmt.Errorf("config %q is not a known configuration ID", configID)
	}
	set := map[string]bool{}
	for _, cp := range c.Applicable(m, cfg) {
		set[cp.ID] = true
	}
	return set, nil
}

// portSuspects flags connectors that failed while another connector in the
// same port group passed in this report: probably a damaged port.
func portSuspects(c *catalog.Catalog, m *catalog.Mac, cfg *catalog.Config, items []Item) []Flag {
	status := map[string]string{}
	for _, it := range items {
		if it.Connector != "" {
			status[it.Capability+"@"+it.Connector] = it.Status
		}
	}
	if len(status) == 0 {
		return nil
	}
	var flags []Flag
	groups := c.CriterionGroups(m, cfg)
	caps := make([]string, 0, len(groups))
	for cp := range groups {
		caps = append(caps, cp)
	}
	sort.Strings(caps)
	for _, cp := range caps {
		for _, g := range groups[cp] {
			passed := ""
			for _, cn := range g.Connectors {
				if status[cp+"@"+cn] == "supported" && passed == "" {
					passed = cn
				}
			}
			if passed == "" {
				continue
			}
			for _, cn := range g.Connectors {
				if st := status[cp+"@"+cn]; st == "failed" || st == "partial" {
					flags = append(flags, Flag{FlagPortSuspect, fmt.Sprintf("%s: %s %s while %s, on the same controller, passed; possibly a damaged port, so it doesn't count against Omarchy",
						cp, cn, st, passed)})
				}
			}
		}
	}
	return flags
}
