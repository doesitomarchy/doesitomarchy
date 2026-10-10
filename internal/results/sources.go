package results

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

// LoadSources reads every data/sources/<id>.yaml: each must load, and its id
// must match its file name.
func LoadSources(fsys fs.FS, c *catalog.Catalog) ([]*Mapping, error) {
	names, err := fs.Glob(fsys, "sources/*.yaml")
	if err != nil {
		return nil, err
	}
	var out []*Mapping
	for _, n := range names {
		id := strings.TrimSuffix(path.Base(n), ".yaml")
		m, err := LoadMapping(fsys, id, c)
		if err != nil {
			return nil, err
		}
		if m.ID != id {
			return nil, fmt.Errorf("%s: id %q must match the file name", n, m.ID)
		}
		out = append(out, m)
	}
	return out, nil
}
