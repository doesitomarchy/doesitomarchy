// Package report is the DoesItOmarchy diagnostic report format, schema v1
// (PLAN.md §21.2): the submission type, its JSON Schema, Parse, and Check,
// every check that needs no catalog. Test tools use it to build and check a
// report offline with the same code the server runs; the server then checks
// the report against the catalog (internal/results) and scrubs personal data
// before storing it.
package report

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

// SchemaV1 identifies the first version of the result schema.
const SchemaV1 = "doesitomarchy/report/v1"

// MaxSize is the largest submission accepted, in bytes.
const MaxSize = 1 << 20

// SchemaV1JSON is the report schema as a JSON Schema document (GET /api/v1/schema).
//
//go:embed schema_v1.json
var SchemaV1JSON []byte

// Values a report may use.
var (
	Statuses = []string{"supported", "partial", "failed", "not_tested"}
	// Methods: challenge means the tool showed or played a random code and a
	// person, a phone or a capture card gave it back; it counts like observed.
	Methods = []string{"automatic", "observed", "fixture", "challenge"}
	// Reasons a not_tested item gives. live-limit: a live boot can't decide
	// the criterion (live reports only).
	Reasons = []string{"no-equipment", "not-in-profile", "uncertain", "live-limit", "other"}
	// Contexts: where the test ran. installed is the default.
	Contexts = []string{ContextInstalled, ContextLive}
	// PartKinds are the kinds of part a replaced_parts entry names.
	PartKinds = []string{"wifi", "bluetooth", "gpu", "storage", "display", "battery", "other"}
)

// Contexts.
const (
	ContextInstalled = "installed" // Omarchy installed on the Mac
	ContextLive      = "live"      // a live boot, such as OmaBoot? Live
)

// ReasonLiveLimit is the skip reason for a criterion a live boot can't decide.
const ReasonLiveLimit = "live-limit"

// File is a report in schema v1. YAML and JSON share it.
type File struct {
	Schema     string     `yaml:"schema" json:"schema"`
	Config     string     `yaml:"config" json:"config,omitempty"`         // optional when identifier/hardware let the server find it
	Identifier string     `yaml:"identifier" json:"identifier,omitempty"` // the model identifier, e.g. MacBookPro15,2
	Source     FileSource `yaml:"source" json:"source"`
	Tester     FileTester `yaml:"tester" json:"tester"`
	TestedAt   string     `yaml:"tested_at" json:"tested_at"`
	// Context is where the test ran: installed (the default) or live.
	Context string      `yaml:"context" json:"context,omitempty"`
	Omarchy FileOmarchy `yaml:"omarchy" json:"omarchy"`
	Kernel  string      `yaml:"kernel" json:"kernel,omitempty"`
	Notes   string      `yaml:"notes" json:"notes,omitempty"`
	// Fixes lists the registered fixes (GET /api/v1/snapshot, fixes) that
	// took effect on this Mac during the test.
	Fixes []string `yaml:"fixes" json:"fixes,omitempty"`
	// ReplacedParts lists parts that aren't the Mac's original ones.
	ReplacedParts []ReplacedPart      `yaml:"replaced_parts" json:"replaced_parts,omitempty"`
	Hardware      map[string]any      `yaml:"hardware" json:"hardware,omitempty"`
	Items         map[string]FileItem `yaml:"items" json:"items"`
	Extras        []FileExtra         `yaml:"extras" json:"extras,omitempty"`
	// ConsentNotice is the notice the tool showed the tester before
	// submitting (PLAN §22.1); stored for the record.
	ConsentNotice string `yaml:"consent_notice" json:"consent_notice,omitempty"`
}

// FileSource names the app that produced the result.
type FileSource struct {
	ID       string `yaml:"id" json:"id,omitempty"`
	Version  string `yaml:"version" json:"version,omitempty"`
	Profile  string `yaml:"profile" json:"profile,omitempty"`
	Workflow string `yaml:"workflow" json:"workflow,omitempty"`
}

// FileTester is who ran the test. Both fields are optional.
type FileTester struct {
	Handle  string `yaml:"handle" json:"handle,omitempty"`
	Contact string `yaml:"contact" json:"contact,omitempty"` // stored hashed, never shown
}

// FileOmarchy is the Omarchy build that was tested.
type FileOmarchy struct {
	Version string `yaml:"version" json:"version"` // /etc/os-release's VERSION_ID, in any form (PLAN §28.1)
	// Channel is only needed for dev, which reads like edge; the others
	// follow from the version.
	Channel  string `yaml:"channel" json:"channel,omitempty"`
	Revision string `yaml:"revision" json:"revision,omitempty"` // a dev build's commit (`omarchy-version` prints "dev (<hash>)")
	// Image is the image the test ran from, e.g. "omaboot-live 2026.10".
	Image string `yaml:"image" json:"image,omitempty"`
}

// FileItem is the result for one capability.
type FileItem struct {
	Status   string `yaml:"status" json:"status"`
	Method   string `yaml:"method" json:"method,omitempty"`
	Reason   string `yaml:"reason" json:"reason,omitempty"`
	Note     string `yaml:"note" json:"note,omitempty"`
	Evidence string `yaml:"evidence" json:"evidence,omitempty"`
}

// FileExtra is a check that is not (yet) one of our criteria.
type FileExtra struct {
	ID     string `yaml:"id" json:"id"`
	Label  string `yaml:"label" json:"label,omitempty"`
	Status string `yaml:"status" json:"status,omitempty"`
	Detail string `yaml:"detail" json:"detail,omitempty"`
}

// ReplacedPart is a part that isn't the Mac's original (PLAN §8.3 of the
// OmaBoot? Live plan): a Wi-Fi card from another Mac, a third-party SSD, …
type ReplacedPart struct {
	Kind   string   `yaml:"kind" json:"kind"`               // one of PartKinds
	Detail string   `yaml:"detail" json:"detail,omitempty"` // e.g. "Broadcom BCM94360CD from a 2013 iMac"
	IDs    []string `yaml:"ids" json:"ids,omitempty"`       // what identifies it: PCI/USB IDs, a drive model, an EDID panel ID
}

// Parse reads a YAML or JSON submission. Unknown fields are errors, so a
// typo in a field name never silently drops data.
func Parse(b []byte) (*File, error) {
	if len(b) > MaxSize {
		return nil, fmt.Errorf("submission is %d bytes; the limit is %d", len(b), MaxSize)
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, fmt.Errorf("submission is empty")
	}
	var f File
	if err := yaml.UnmarshalWithOptions(b, &f, yaml.DisallowUnknownField(), yaml.Strict()); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return &f, nil
}

// Errors collects every problem in a submission, so a tool author sees them
// all at once instead of one per attempt.
type Errors []string

func (e Errors) Error() string {
	return "invalid result:\n  - " + strings.Join(e, "\n  - ")
}

// IsLive reports whether the report comes from a live boot.
func (f *File) IsLive() bool { return strings.TrimSpace(f.Context) == ContextLive }

func oneOf(s string, set []string) bool {
	for _, x := range set {
		if s == x {
			return true
		}
	}
	return false
}
