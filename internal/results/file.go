// Package results reads test-result submissions (the DoesItOmarchy result
// schema, PLAN.md §21.2), checks them against the catalog, and scrubs
// personal data before anything is stored. It knows nothing about the
// database: the store persists what Validate returns.
package results

import (
	"bytes"
	"fmt"

	"github.com/goccy/go-yaml"
)

// SchemaV1 identifies the first version of the result schema.
const SchemaV1 = "doesitomarchy/report/v1"

// MaxSize is the largest submission accepted, in bytes.
const MaxSize = 1 << 20

// File is a result submission in schema v1. YAML and JSON share it.
type File struct {
	Schema     string              `yaml:"schema" json:"schema"`
	Config     string              `yaml:"config" json:"config"`         // optional when identifier/hardware let the server find it
	Identifier string              `yaml:"identifier" json:"identifier"` // the model identifier, e.g. MacBookPro15,2
	Source     FileSource          `yaml:"source" json:"source"`
	Tester     FileTester          `yaml:"tester" json:"tester"`
	TestedAt   string              `yaml:"tested_at" json:"tested_at"`
	Omarchy    FileOmarchy         `yaml:"omarchy" json:"omarchy"`
	Kernel     string              `yaml:"kernel" json:"kernel"`
	Notes      string              `yaml:"notes" json:"notes"`
	Hardware   map[string]any      `yaml:"hardware" json:"hardware"`
	Items      map[string]FileItem `yaml:"items" json:"items"`
	Extras     []FileExtra         `yaml:"extras" json:"extras"`
	// ConsentNotice is the notice the tool showed the tester before
	// submitting (PLAN §22.1); stored for the record.
	ConsentNotice string `yaml:"consent_notice" json:"consent_notice"`
}

// FileSource names the app that produced the result.
type FileSource struct {
	ID       string `yaml:"id" json:"id"`
	Version  string `yaml:"version" json:"version"`
	Profile  string `yaml:"profile" json:"profile"`
	Workflow string `yaml:"workflow" json:"workflow"`
}

// FileTester is who ran the test. Both fields are optional.
type FileTester struct {
	Handle  string `yaml:"handle" json:"handle"`
	Contact string `yaml:"contact" json:"contact"` // stored hashed, never shown
}

// FileOmarchy is the Omarchy build that was tested.
type FileOmarchy struct {
	Version string `yaml:"version" json:"version"` // /etc/os-release's VERSION_ID, in any form (PLAN §28.1)
	// Channel is only needed for dev, which reads like edge; the others
	// follow from the version.
	Channel  string `yaml:"channel" json:"channel"`
	Revision string `yaml:"revision" json:"revision"` // a dev build's commit (`omarchy-version` prints "dev (<hash>)")
	Image    string `yaml:"image" json:"image"`
}

// FileItem is the result for one capability.
type FileItem struct {
	Status   string `yaml:"status" json:"status"`
	Method   string `yaml:"method" json:"method"`
	Reason   string `yaml:"reason" json:"reason"`
	Note     string `yaml:"note" json:"note"`
	Evidence string `yaml:"evidence" json:"evidence"`
}

// FileExtra is a check that is not (yet) one of our criteria.
type FileExtra struct {
	ID     string `yaml:"id" json:"id"`
	Label  string `yaml:"label" json:"label"`
	Status string `yaml:"status" json:"status"`
	Detail string `yaml:"detail" json:"detail"`
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
