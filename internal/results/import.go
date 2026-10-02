package results

import (
	"fmt"
	"io/fs"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

// Imported is a validated report ready to store, with the raw report as it
// should be kept and its input format.
type Imported struct {
	Result *Result
	Raw    []byte
	Format string
	Source *Mapping // the native format's source, to register before storing; nil for our schema
}

// Import validates a report in our schema (format "" or SchemaV1) or a
// registered source's native format ("omacdiag"), as a maintainer imports
// it. fsys holds the catalog's data files (the sources/ mappings).
func Import(raw []byte, format string, c *catalog.Catalog, fsys fs.FS, opt ImportOptions, now time.Time) (*Imported, error) {
	switch format {
	case "", SchemaV1:
		f, err := Parse(raw)
		if err != nil {
			return nil, err
		}
		r, err := Validate(f, c, now)
		if err != nil {
			return nil, err
		}
		return &Imported{r, raw, SchemaV1, nil}, nil
	case "omacdiag", FormatOmacDiag:
		mp, err := LoadMapping(fsys, "omacdiag", c)
		if err != nil {
			return nil, err
		}
		conv, err := FromOmacDiag(raw, c, mp, opt)
		if err != nil {
			return nil, err
		}
		r, err := Validate(conv.File, c, now)
		if err != nil {
			return nil, err
		}
		r.Flags = append(r.Flags, conv.Flags...)
		return &Imported{r, conv.Raw, FormatOmacDiag, mp}, nil
	}
	return nil, fmt.Errorf("unknown format %q: use %s or omacdiag", format, SchemaV1)
}
