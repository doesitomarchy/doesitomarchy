package results

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

// The OmacDiag adapter (PLAN.md §22.10, §24): an OmacDiag JSON report becomes
// a report in our schema, mapped by the rules in data/sources/omacdiag.yaml.

// FormatOmacDiag names OmacDiag's native report format.
const FormatOmacDiag = "omacdiag/report/v1"

// Mapping is a source's data file: how its checks map to our criteria.
type Mapping struct {
	ID             string            `yaml:"id"`
	Name           string            `yaml:"name"`
	Homepage       string            `yaml:"homepage"`
	Format         string            `yaml:"format"`
	Rules          []MapRule         `yaml:"rules"`
	FailedReasons  []string          `yaml:"failed_reasons"`
	SkipReasons    map[string]string `yaml:"skip_reasons"`
	InstallWhenDis []string          `yaml:"install_when_distribution"`
}

// MapRule maps the checks it matches to one criterion.
type MapRule struct {
	Kind     string            `yaml:"kind"`
	Probe    string            `yaml:"probe"`
	Check    string            `yaml:"check"`
	Attrs    map[string]string `yaml:"attrs"`
	Contains map[string]string `yaml:"contains"`
	Excludes map[string]string `yaml:"excludes"`
	To       string            `yaml:"to"`
	Method   string            `yaml:"method"`
}

// toUSB is the rule target for USB connectors: USB-A or USB-C, decided from
// the configuration's ports.
const toUSB = "ports.usb"

// LoadMapping reads data/sources/<id>.yaml and checks its targets against
// the criteria.
func LoadMapping(fsys fs.FS, id string, c *catalog.Catalog) (*Mapping, error) {
	b, err := fs.ReadFile(fsys, "sources/"+id+".yaml")
	if err != nil {
		return nil, err
	}
	var m Mapping
	if err := yaml.UnmarshalWithOptions(b, &m, yaml.DisallowUnknownField(), yaml.Strict()); err != nil {
		return nil, fmt.Errorf("sources/%s.yaml: %w", id, err)
	}
	known := map[string]bool{toUSB: true, "extra": true}
	for _, cp := range c.Capabilities {
		known[cp.ID] = true
	}
	for i, r := range m.Rules {
		if !known[r.To] {
			return nil, fmt.Errorf("sources/%s.yaml: rule %d maps to unknown criterion %q", id, i+1, r.To)
		}
		if r.Method != "" && !oneOf(r.Method, Methods) {
			return nil, fmt.Errorf("sources/%s.yaml: rule %d: method %q", id, i+1, r.Method)
		}
	}
	for k, v := range m.SkipReasons {
		if !oneOf(v, Reasons) {
			return nil, fmt.Errorf("sources/%s.yaml: skip reason %s → %q", id, k, v)
		}
	}
	return &m, nil
}

// OmacDiag's report, as far as the adapter reads it (OmacDiag src/model.rs).
type odReport struct {
	SchemaVersion      int    `json:"schema_version"`
	ApplicationVersion string `json:"application_version"`
	SessionID          string `json:"session_id"`
	StartedAt          string `json:"started_at"`
	Overall            string `json:"overall"`
	Inventory          struct {
		Machine struct {
			Manufacturer string `json:"manufacturer"`
			Model        string `json:"model"`
			Distribution string `json:"distribution"`
			OSVersion    string `json:"os_version"` // not in OmacDiag 0.1.0; proposed upstream
			Kernel       string `json:"kernel"`
		} `json:"machine"`
		Devices []odDevice `json:"devices"`
	} `json:"inventory"`
	Plan struct {
		Profile string   `json:"profile"`
		Mode    string   `json:"mode"`
		Tests   []odTest `json:"tests"`
	} `json:"plan"`
	Results []odResult `json:"results"`
}

type odDevice struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Name        string            `json:"name"`
	Recognition string            `json:"recognition"`
	Attributes  map[string]string `json:"attributes"`
}

type odTest struct {
	ID       string `json:"id"`
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Probe    string `json:"probe"`
	Required bool   `json:"required"`
}

type odResult struct {
	TestID      string `json:"test_id"`
	DeviceID    string `json:"device_id"`
	Outcome     string `json:"outcome"`
	ReasonCode  string `json:"reason_code"`
	Summary     string `json:"summary"`
	Observation *struct {
		Verdict string `json:"verdict"`
		Note    string `json:"note"`
	} `json:"observation"`
}

// ImportOptions are what a maintainer supplies alongside a native report.
type ImportOptions struct {
	Omarchy string // the Omarchy version, when the report lacks it
	Tester  string // the tester's public handle, optional
}

// Converted is a native report turned into our schema.
type Converted struct {
	File  *File
	Flags []Flag // to add after validation
	Raw   []byte // the native report, scrubbed, for storage
}

// ErrNoOmarchyVersion: neither the report nor the maintainer gave one.
var ErrNoOmarchyVersion = errors.New("the report doesn't say which Omarchy version it ran on; give it (doiomad: -omarchy 4.0.4)")

// FromOmacDiag converts an OmacDiag JSON report.
func FromOmacDiag(raw []byte, c *catalog.Catalog, mp *Mapping, opt ImportOptions) (*Converted, error) {
	if len(raw) > MaxSize {
		return nil, fmt.Errorf("report is %d bytes; the limit is %d", len(raw), MaxSize)
	}
	var od odReport
	if err := json.Unmarshal(raw, &od); err != nil {
		return nil, fmt.Errorf("not an OmacDiag report: %w", err)
	}
	if od.SchemaVersion != 1 || od.Inventory.Machine.Model == "" {
		return nil, fmt.Errorf("not an OmacDiag report (schema_version %d, model %q); this adapter reads schema_version 1", od.SchemaVersion, od.Inventory.Machine.Model)
	}
	version := strings.TrimSpace(od.Inventory.Machine.OSVersion)
	if version == "" {
		version = strings.TrimSpace(opt.Omarchy)
	}
	if version == "" {
		return nil, ErrNoOmarchyVersion
	}

	// The scrubbed native report: personal keys replaced, then free text.
	var tree any
	json.Unmarshal(raw, &tree)
	scrubbed, _ := json.MarshalIndent(ScrubValue(tree), "", "  ")

	devices := map[string]odDevice{}
	for _, d := range od.Inventory.Devices {
		devices[d.ID] = d
	}
	tests := map[string]odTest{}
	for _, t := range od.Plan.Tests {
		tests[t.ID] = t
	}

	f := &File{Schema: SchemaV1, Identifier: od.Inventory.Machine.Model,
		Source:   FileSource{ID: mp.ID, Version: od.ApplicationVersion, Profile: od.Plan.Profile, Workflow: od.Plan.Mode},
		Tester:   FileTester{Handle: strings.TrimSpace(opt.Tester)},
		Omarchy:  FileOmarchy{Version: version},
		Kernel:   od.Inventory.Machine.Kernel,
		Hardware: odProbe(od),
		Items:    map[string]FileItem{},
	}
	if ms, err := strconv.ParseInt(od.StartedAt, 10, 64); err == nil {
		f.TestedAt = time.UnixMilli(ms).UTC().Format(time.RFC3339)
	} else {
		f.TestedAt = od.StartedAt // Validate explains what's wrong with it
	}
	f.Notes = fmt.Sprintf("Imported from %s %s (%s profile, %s mode); %d checks, OmacDiag's overall result: %s.",
		mp.Name, od.ApplicationVersion, od.Plan.Profile, od.Plan.Mode, len(od.Results), od.Overall)

	usbTo := usbCriterion(c, f)
	failed := map[string]bool{}
	for _, r := range mp.FailedReasons {
		failed[r] = true
	}
	type part struct {
		status, method, reason, evidence string
		missing                          bool
	}
	parts := map[string][]part{}
	var flags []Flag
	flagged := map[string]bool{}
	for _, res := range od.Results {
		t, ok := tests[res.TestID]
		if ok && !t.Required {
			continue // an earlier attempt, superseded by a retake
		}
		d := devices[res.DeviceID]
		check := strings.TrimPrefix(res.TestID, res.DeviceID+"/")
		if i := strings.Index(check, "/retake-"); i >= 0 {
			check = check[:i]
		}
		rule := mp.match(d, t.Probe, check)
		to := ""
		if rule != nil {
			to = rule.To
			if to == toUSB {
				to = usbTo
			}
		}
		pt := part{evidence: odEvidence(res)}
		switch {
		case res.Outcome == "cancelled":
			continue
		case res.Outcome == "passed":
			pt.status = "supported"
		case res.Outcome == "failed":
			pt.status = "failed"
		case failed[res.ReasonCode]:
			pt.status, pt.missing = "failed", true
		default:
			pt.status, pt.reason = "not_tested", mp.SkipReasons[res.ReasonCode]
			if pt.reason == "" {
				pt.reason = "uncertain"
			}
		}
		if to == "" || to == "extra" {
			f.Extras = append(f.Extras, FileExtra{ID: res.TestID, Label: t.Name, Status: res.Outcome, Detail: pt.evidence})
			continue
		}
		switch {
		case rule.Method != "":
			pt.method = rule.Method
		case res.Observation != nil && (res.Observation.Verdict == "passed" || res.Observation.Verdict == "failed"):
			pt.method = "observed"
		default:
			pt.method = "automatic"
		}
		parts[to] = append(parts[to], pt)
		if pt.missing && !flagged[to] {
			flagged[to] = true // one flag per criterion
			flags = append(flags, Flag{FlagDriverMissing, fmt.Sprintf("%s: OmacDiag reported %s on %s (%s); counted as failed, check it's a missing driver and not absent hardware",
				to, res.ReasonCode, res.TestID, d.Name)})
		}
	}

	for to, ps := range parts {
		var it FileItem
		pass, fail, ev := 0, 0, []string{}
		for _, p := range ps {
			switch p.status {
			case "supported":
				pass++
			case "failed":
				fail++
			}
			ev = append(ev, p.evidence)
		}
		switch {
		case pass > 0 && fail > 0:
			it.Status = "partial"
		case fail > 0:
			it.Status = "failed"
		case pass > 0:
			it.Status = "supported"
		default:
			// The most telling reason: "no equipment" says more than "uncertain".
			it.Status, it.Reason = "not_tested", "uncertain"
			for _, p := range ps {
				if p.reason != "uncertain" {
					it.Reason = p.reason
					break
				}
			}
		}
		if it.Status != "not_tested" {
			// The strongest evidence method among the conclusive checks.
			rank := map[string]int{"automatic": 1, "observed": 2, "fixture": 3}
			for _, p := range ps {
				if p.status != "not_tested" && rank[p.method] > rank[it.Method] {
					it.Method = p.method
				}
			}
		}
		it.Evidence = capText(strings.Join(ev, "\n"), maxEvidence)
		f.Items[to] = it
	}

	for _, want := range mp.InstallWhenDis {
		if strings.EqualFold(strings.TrimSpace(od.Inventory.Machine.Distribution), want) {
			if _, set := f.Items["boot.install"]; !set {
				f.Items["boot.install"] = FileItem{Status: "supported", Method: "automatic",
					Evidence: "OmacDiag ran on an installed system whose /etc/os-release names " + want + "."}
			}
		}
	}
	sort.Slice(f.Extras, func(i, j int) bool { return f.Extras[i].ID < f.Extras[j].ID })
	return &Converted{File: f, Flags: flags, Raw: scrubbed}, nil
}

// match returns the first rule a check satisfies, or nil.
func (mp *Mapping) match(d odDevice, probe, check string) *MapRule {
	for i := range mp.Rules {
		r := &mp.Rules[i]
		if (r.Kind != "" && r.Kind != d.Kind) || (r.Probe != "" && r.Probe != probe) || (r.Check != "" && r.Check != check) {
			continue
		}
		ok := true
		for k, v := range r.Attrs {
			ok = ok && d.Attributes[k] == v
		}
		for k, v := range r.Contains {
			ok = ok && strings.Contains(d.Attributes[k], v)
		}
		for k, v := range r.Excludes {
			ok = ok && !strings.Contains(d.Attributes[k], v)
		}
		if ok {
			return r
		}
	}
	return nil
}

// odProbe builds our hardware probe from OmacDiag's inventory: the model
// identifier, the CPU name, and PCI and USB device IDs.
func odProbe(od odReport) map[string]any {
	hw := map[string]any{"product_name": od.Inventory.Machine.Model}
	var pci, usb []any
	seen := map[string]bool{}
	hex := func(s string) string { return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "0x")) }
	for _, d := range od.Inventory.Devices {
		a := d.Attributes
		switch {
		case d.Kind == "cpu" && d.Recognition == "detected":
			hw["cpu"] = d.Name
		case a["vendor"] != "" && a["device"] != "":
			if id := hex(a["vendor"]) + ":" + hex(a["device"]); !seen["pci"+id] {
				seen["pci"+id] = true
				pci = append(pci, id)
			}
		case a["idVendor"] != "" && a["idProduct"] != "":
			if id := hex(a["idVendor"]) + ":" + hex(a["idProduct"]); !seen["usb"+id] {
				seen["usb"+id] = true
				usb = append(usb, id)
			}
		}
	}
	if pci != nil {
		hw["pci"] = pci
	}
	if usb != nil {
		hw["usb"] = usb
	}
	return hw
}

// usbCriterion decides what a USB connector check proves: ports.usb-a or
// ports.usb-c when every configuration the hardware could be has only that
// kind of USB port, else "extra" (until per-connector layouts, PLAN §20).
func usbCriterion(c *catalog.Catalog, f *File) string {
	probe := ProbeFromHardware(f.Hardware, f.Identifier)
	kinds := map[string]bool{}
	mr := matcherFor(c).Match(probe)
	for _, cd := range mr.Candidates {
		if cd.Score < mr.Candidates[0].Score {
			break
		}
		_, cfg := findConfig(c, cd.Config)
		if cfg == nil {
			continue
		}
		for port := range cfg.Ports {
			switch {
			case strings.HasPrefix(port, "usb-a"):
				kinds["ports.usb-a"] = true
			case port == "usb-c" || port == "thunderbolt-3":
				kinds["ports.usb-c"] = true
			}
		}
	}
	if len(kinds) == 1 {
		for k := range kinds {
			return k
		}
	}
	return "extra"
}

// odEvidence is one check's result in a line: what OmacDiag found and what
// the technician saw.
func odEvidence(r odResult) string {
	s := r.TestID + ": " + r.Outcome
	if r.ReasonCode != "" && r.ReasonCode != r.Outcome {
		s += " (" + r.ReasonCode + ")"
	}
	if sum := strings.TrimSpace(r.Summary); sum != "" {
		s += ". " + sum
	}
	if o := r.Observation; o != nil {
		s += " Technician: " + o.Verdict
		if n := strings.TrimSpace(o.Note); n != "" {
			s += ", " + n
		}
		s += "."
	}
	return capText(s, maxNote)
}

func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && (s[cut]&0xC0) == 0x80 { // don't split a UTF-8 character
		cut--
	}
	return s[:cut] + "…"
}
