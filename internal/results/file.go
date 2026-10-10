// Package results reads test-result submissions (the DoesItOmarchy result
// schema, PLAN.md §21.2), checks them against the catalog, and scrubs
// personal data before anything is stored. It knows nothing about the
// database: the store persists what Validate returns.
//
// The format itself, and every check that needs no catalog, is the public
// package pkg/report, so test tools run the same checks offline.
package results

import "github.com/doesitomarchy/doesitomarchy/pkg/report"

// SchemaV1 identifies the first version of the result schema.
const SchemaV1 = report.SchemaV1

// MaxSize is the largest submission accepted, in bytes.
const MaxSize = report.MaxSize

// The submission and its parts (pkg/report).
type (
	File         = report.File
	FileSource   = report.FileSource
	FileTester   = report.FileTester
	FileOmarchy  = report.FileOmarchy
	FileItem     = report.FileItem
	FileExtra    = report.FileExtra
	ReplacedPart = report.ReplacedPart
)

// SchemaV1JSON is the report schema as a JSON Schema document (GET /api/v1/schema).
var SchemaV1JSON = report.SchemaV1JSON

// Parse reads a YAML or JSON submission. Unknown fields are errors, so a
// typo in a field name never silently drops data.
func Parse(b []byte) (*File, error) { return report.Parse(b) }
