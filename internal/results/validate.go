package results

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/match"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/pkg/report"
)

// Statuses, methods and skip reasons a result item may carry (pkg/report).
var (
	Statuses = report.Statuses
	Methods  = report.Methods
	Reasons  = report.Reasons
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
	// FlagRegression: an item fails or only partly works on a newer build
	// than the one it currently passes on, in the same view (PLAN §28.2).
	FlagRegression = "regression"
	// FlagUnknownBuild: the build's commit date isn't known yet (a dev commit
	// never pushed, or GitHub unreachable), so the test date stands in.
	FlagUnknownBuild = "unknown_build"
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
	Context       string // installed | live
	Omarchy       status.Version
	OmarchyRaw    string // the canonical form (Omarchy.String()), stored as omarchy_version
	Channel       string // stable | rc | beta | edge | dev
	Commit        string // the full commit tested, once looked up (internal/builds)
	BuiltAt       string // that commit's date, RFC 3339 UTC; "" when unknown
	Revision      string
	Image         string
	Kernel        string
	Notes         string
	Hardware      string   // scrubbed JSON
	Candidates    []string // when the hardware fits several configs: all of them (ConfigID is the first)
	Fixes         []string // registered OmaBoot? fix IDs that took effect (data/fixes.yaml)
	ReplacedParts []ReplacedPart
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
type Errors = report.Errors

// Validate checks a parsed submission and returns the canonical, scrubbed
// result. It runs report.Check (everything that needs no catalog) first,
// then checks the report against the catalog: the configuration, the
// criteria and connectors, the live limits and the fix IDs. now bounds
// tested_at (no future dates).
func Validate(f *File, c *catalog.Catalog, now time.Time) (*Result, error) {
	var errs Errors
	if err := report.Check(f, now); err != nil {
		if !errors.As(err, &errs) {
			return nil, err
		}
	}
	bad := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	r := &Result{Schema: SchemaV1, SourceID: strings.ToLower(strings.TrimSpace(f.Source.ID))}
	if r.SourceID == "" {
		r.SourceID = "manual"
	}
	r.SourceVersion, r.Profile, r.Workflow = short(f.Source.Version), short(f.Source.Profile), short(f.Source.Workflow)
	r.Context = report.ContextInstalled
	live := f.IsLive()
	if live {
		r.Context = report.ContextLive
	}

	// The configuration: given by ID (an old ID listed in a config's aliases
	// also resolves), or found from the identifier and hardware probe.
	probe := probeOf(f)
	configID := strings.TrimSpace(f.Config)
	noMatch := false
	if configID == "" && (probe.ProductName != "" || probe.BoardID != "") {
		mr := matcherFor(c).Match(probe)
		switch {
		case len(mr.Candidates) == 0:
			bad("config: no configuration matches this hardware (identifier %q, board %q); send a config ID", probe.ProductName, probe.BoardID)
			noMatch = true
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
	case configID == "" && f.Config == "" && !noMatch:
		bad("config: required (a configuration ID such as macbookpro15-2-13-2018-4tb3-a, or an identifier and hardware probe)")
	case f.Config != "":
		bad("config: %q is not a known configuration ID", f.Config)
	}

	// report.Check has checked these; here they only take their stored form.
	if h := strings.TrimSpace(f.Tester.Handle); report.HandleRe.MatchString(h) {
		r.TesterHandle = h
	}
	r.Contact = strings.TrimSpace(f.Tester.Contact)
	if d, err := time.Parse(time.RFC3339, strings.TrimSpace(f.TestedAt)); err == nil {
		r.TestedAt = d.UTC().Format(time.RFC3339)
	}
	if v, err := status.ParseVersion(f.Omarchy.Version); err == nil {
		if ch, err := status.ValidChannel(v, strings.ToLower(strings.TrimSpace(f.Omarchy.Channel))); err == nil {
			r.Omarchy, r.OmarchyRaw, r.Channel = v, v.String(), ch
		}
	}
	r.Revision, r.Image, r.Kernel = short(f.Omarchy.Revision), short(f.Omarchy.Image), short(f.Kernel)
	r.Notes = Scrub(strings.TrimSpace(f.Notes))

	hw := map[string]any{}
	if f.Hardware != nil {
		hw = ScrubValue(f.Hardware).(map[string]any)
	}
	b, _ := json.Marshal(hw)
	r.Hardware = string(b)

	// Fixes must be registered (data/fixes.yaml); replaced parts are kept as
	// sent, scrubbed. A fix that took effect lifts the live limit on the
	// criteria it targets: that's what it's for (applesmc on pre-T2 Macs).
	lifted := map[string]bool{}
	for _, id := range f.Fixes {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(r.Fixes, id) {
			continue // report.Check has said so
		}
		fx := c.OmabootFix(id)
		if fx == nil {
			bad("fixes: %q is not a registered fix (see /api/v1/snapshot, fixes)", id)
			continue
		}
		r.Fixes = append(r.Fixes, id)
		for _, cp := range fx.Targets.Criteria {
			lifted[cp] = true
		}
	}
	for _, p := range f.ReplacedParts {
		rp := ReplacedPart{Kind: strings.TrimSpace(p.Kind), Detail: short(p.Detail)}
		for _, id := range p.IDs {
			rp.IDs = append(rp.IDs, short(id))
		}
		r.ReplacedParts = append(r.ReplacedParts, rp)
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
		if it.Status == "not_tested" {
			it.Method = ""
		}
		// A live boot decides only what it can (LIVE-PLAN §7): the rest comes
		// as not_tested, reason live-limit, so nothing pretends to be a result.
		if live && it.Status != "not_tested" && oneOf(it.Status, Statuses) && !lifted[id] {
			switch {
			case cp.Live == catalog.LiveNo:
				bad("items.%s: a live boot can't decide %s (%s); send it as not_tested with reason %s", key, id, cp.Name, report.ReasonLiveLimit)
			case cp.Live == catalog.LiveT2 && m != nil && m.SecurityChip != "t2":
				bad("items.%s: a live boot can decide %s (%s) only on a Mac with a T2 chip, and %s has none; send it as not_tested with reason %s",
					key, id, cp.Name, m.Identifier, report.ReasonLiveLimit)
			}
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

	for _, x := range f.Extras {
		x.ID, x.Label, x.Status = strings.TrimSpace(x.ID), short(x.Label), short(x.Status)
		x.Detail = Scrub(strings.TrimSpace(x.Detail))
		r.Extras = append(r.Extras, x)
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
	if len(s) > report.MaxShort {
		s = strings.ToValidUTF8(s[:report.MaxShort], "") // never split a character
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
		} else if tied, own := m.BoardReleases(p.BoardID), m.ReleaseOf(cfg.ID); len(tied) > 0 && own != nil && !slices.Contains(tied, own) {
			// The board belongs to another release of this Mac (PLAN.md §29).
			names := make([]string, len(tied))
			for i, r := range tied {
				names[i] = r.Name
			}
			out = append(out, Flag{FlagHardware, fmt.Sprintf("board ID %s belongs to the %s; the config is from the %s", p.BoardID, strings.Join(names, " or "), own.Name)})
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

// HardwareFingerprint identifies a Mac by its hardware, never by a serial
// number: the SHA-256 of its product name, board ID and sorted PCI IDs, as
// the report's probe gives them. It limits reports per Mac from sources
// whose key is public (OmaBoot? Live).
func HardwareFingerprint(f *File) string {
	p := probeOf(f)
	pci := match.NormalizeIDs(p.PCI, "pci")
	sort.Strings(pci)
	sum := sha256.Sum256([]byte(strings.ToLower(p.ProductName) + "\n" + strings.ToLower(p.BoardID) + "\n" + strings.Join(pci, ",")))
	return hex.EncodeToString(sum[:])
}
