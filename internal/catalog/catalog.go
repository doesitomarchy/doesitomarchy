// Package catalog loads and validates the YAML catalog in data/.
//
// Layout:
//
//	data/vocabulary.yaml      controlled values (lines, ports, features, …)
//	data/capabilities.yaml    test criteria and their applicability rules
//	data/components/<kind>.yaml
//	data/macs/<Identifier>.yaml   (comma replaced by "-", e.g. Macmini3-1.yaml)
//	data/config-ids.lock      every config ID ever issued; IDs are permanent
package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Load reads and validates the catalog in dir. On failure it returns every
// problem found, one per line, so a contributor can fix them in one pass.
func Load(dir string) (*Catalog, error) { return loadDir(dir, true) }

// LoadUnlocked is Load without the "every config ID is locked" check, for
// `doiomad lock`. Removed IDs are still rejected.
func LoadUnlocked(dir string) (*Catalog, error) { return loadDir(dir, false) }

// LoadFS reads and validates a catalog rooted at fsys (e.g. the embedded data/).
func LoadFS(fsys fs.FS) (*Catalog, error) { return load(fsys, true) }

func loadDir(dir string, requireLocked bool) (*Catalog, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("catalog dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("catalog dir: %s is not a directory", dir)
	}
	return load(os.DirFS(dir), requireLocked)
}

func load(fsys fs.FS, requireLocked bool) (*Catalog, error) {
	l := &loader{fsys: fsys, requireLocked: requireLocked, cat: &Catalog{Components: map[string]*Component{}}}
	l.load()
	if len(l.problems) == 0 {
		l.validate()
	}
	if len(l.problems) > 0 {
		sort.Strings(l.problems)
		return nil, errors.New(strings.Join(l.problems, "\n"))
	}
	l.cat.mergeBoards()
	return l.cat, nil
}

// mergeBoards makes each Mac's BoardIDs its full list: the untied boards from
// the file, then each release's, once each.
func (c *Catalog) mergeBoards() {
	for _, m := range c.Macs {
		seen := map[string]bool{}
		for _, b := range m.BoardIDs {
			seen[b] = true
		}
		for _, r := range m.Releases {
			for _, b := range r.BoardIDs {
				if !seen[b] {
					seen[b] = true
					m.BoardIDs = append(m.BoardIDs, b)
				}
			}
		}
	}
}

// loader works on slash-separated paths relative to the catalog root.
type loader struct {
	fsys          fs.FS
	requireLocked bool
	cat           *Catalog
	problems      []string
}

func (l *loader) errf(file, format string, args ...any) {
	l.problems = append(l.problems, file+": "+fmt.Sprintf(format, args...))
}

func (l *loader) exists(name string) bool {
	_, err := fs.Stat(l.fsys, name)
	return err == nil
}

// decode strictly unmarshals a YAML file: unknown fields and duplicate keys are errors.
func (l *loader) decode(name string, v any) bool {
	b, err := fs.ReadFile(l.fsys, name)
	if err != nil {
		l.errf(name, "%v", err)
		return false
	}
	if err := yaml.UnmarshalWithOptions(b, v, yaml.Strict()); err != nil {
		l.errf(name, "%v", err)
		return false
	}
	return true
}

func (l *loader) yamlFiles(dir string) ([]string, error) {
	entries, err := fs.ReadDir(l.fsys, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		ext := strings.ToLower(path.Ext(e.Name()))
		if !e.IsDir() && (ext == ".yaml" || ext == ".yml") {
			out = append(out, path.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (l *loader) load() {
	c := l.cat
	const vocabPath = "vocabulary.yaml"
	if !l.exists(vocabPath) {
		l.errf(vocabPath, "missing (required)")
		return
	}
	l.decode(vocabPath, &c.Vocab)

	const capPath = "capabilities.yaml"
	if !l.exists(capPath) {
		l.errf(capPath, "missing (required)")
	} else {
		var cf CapabilityFile
		if l.decode(capPath, &cf) {
			c.Categories, c.Capabilities = cf.Categories, cf.Capabilities
		}
	}

	compFiles, err := l.yamlFiles("components")
	if err != nil {
		l.errf("components", "%v", err)
	}
	for _, p := range compFiles {
		kind := strings.TrimSuffix(path.Base(p), path.Ext(p))
		var comps []*Component
		if !l.decode(p, &comps) {
			continue
		}
		for _, comp := range comps {
			comp.Kind, comp.File = kind, p
			if prev, dup := c.Components[comp.ID]; dup {
				l.errf(p, "component %q already defined in %s", comp.ID, path.Base(prev.File))
				continue
			}
			c.Components[comp.ID] = comp
		}
	}

	const covPath = "coverage.yaml" // optional: no file means every config counts
	if l.exists(covPath) {
		var cf CoverageFile
		if l.decode(covPath, &cf) {
			c.CoverageRules = cf.Exclude
		}
	}

	const aliasPath = "aliases.yaml" // optional: search nicknames
	if l.exists(aliasPath) {
		l.decode(aliasPath, &c.Aliases)
	}

	const changelogPath = "changelog.yaml" // optional: public changelog
	if l.exists(changelogPath) {
		l.decode(changelogPath, &c.Changelog)
	}

	const plumbingPath = "plumbing.yaml" // optional: chipset IDs for /admin/shares
	if l.exists(plumbingPath) {
		var vendors []PlumbingVendor
		if l.decode(plumbingPath, &vendors) {
			c.Plumbing = map[string]Plumbing{}
			for _, v := range vendors {
				for id, p := range v.IDs {
					if _, dup := c.Plumbing[id]; dup {
						l.errf(plumbingPath, "%s is listed twice", id)
					}
					c.Plumbing[id] = p
				}
			}
		}
	}

	macFiles, err := l.yamlFiles("macs")
	if err != nil {
		l.errf("macs", "%v", err)
	}
	for _, p := range macFiles {
		m := &Mac{File: p}
		if l.decode(p, m) {
			if m.SecurityChip == "" {
				m.SecurityChip = "none"
			}
			c.Macs = append(c.Macs, m)
		}
	}

	layoutFiles, err := l.yamlFiles("layouts")
	if err != nil {
		l.errf("layouts", "%v", err)
	}
	for _, p := range layoutFiles {
		lf := &LayoutFile{File: p}
		if l.decode(p, lf) {
			c.Layouts = append(c.Layouts, lf)
		}
	}

	l.loadPortmaps()

	ids, err := readLockFS(l.fsys, LockFile)
	if err != nil {
		l.errf(LockFile, "%v", err)
	}
	c.LockedIDs = ids
}

// Stats counts catalog entities, for CLI output.
type Stats struct {
	Macs, Releases, Configs, Components, Capabilities, Uncertain int
}

func (c *Catalog) Stats() Stats {
	s := Stats{Macs: len(c.Macs), Components: len(c.Components)}
	for _, cp := range c.Capabilities {
		if !cp.Retired {
			s.Capabilities++
		}
	}
	for _, comp := range c.Components {
		s.Uncertain += len(comp.Uncertain)
	}
	for _, m := range c.Macs {
		s.Releases += len(m.Releases)
		s.Uncertain += len(m.Uncertain)
		for _, r := range m.Releases {
			s.Configs += len(r.Configs)
			for _, cfg := range r.Configs {
				s.Uncertain += len(cfg.Uncertain)
			}
		}
	}
	return s
}

// ConfigIDs returns every current config ID plus aliases, sorted.
func (c *Catalog) ConfigIDs() (ids, aliases []string) {
	for _, m := range c.Macs {
		for _, r := range m.Releases {
			for _, cfg := range r.Configs {
				ids = append(ids, cfg.ID)
				aliases = append(aliases, cfg.Aliases...)
			}
		}
	}
	sort.Strings(ids)
	sort.Strings(aliases)
	return ids, aliases
}

// FileSlug is the data/macs file name stem for an identifier ("Macmini3,1" → "Macmini3-1").
func FileSlug(identifier string) string { return strings.ReplaceAll(identifier, ",", "-") }

// IDSlug is the lower-case prefix every config ID of an identifier starts with.
func IDSlug(identifier string) string { return strings.ToLower(FileSlug(identifier)) }
