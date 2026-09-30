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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Load reads and validates the catalog in dir. On failure it returns every
// problem found, one per line, so a contributor can fix them in one pass.
func Load(dir string) (*Catalog, error) { return load(dir, true) }

// LoadUnlocked is Load without the "every config ID is locked" check, for
// `doioma lock`. Removed IDs are still rejected.
func LoadUnlocked(dir string) (*Catalog, error) { return load(dir, false) }

func load(dir string, requireLocked bool) (*Catalog, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("catalog dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("catalog dir: %s is not a directory", dir)
	}

	l := &loader{dir: dir, requireLocked: requireLocked, cat: &Catalog{Components: map[string]*Component{}}}
	l.load()
	if len(l.problems) == 0 {
		l.validate()
	}
	if len(l.problems) > 0 {
		sort.Strings(l.problems)
		return nil, errors.New(strings.Join(l.problems, "\n"))
	}
	return l.cat, nil
}

type loader struct {
	dir           string
	requireLocked bool
	cat           *Catalog
	problems      []string
}

func (l *loader) errf(file, format string, args ...any) {
	rel, err := filepath.Rel(l.dir, file)
	if err != nil {
		rel = file
	}
	l.problems = append(l.problems, rel+": "+fmt.Sprintf(format, args...))
}

// decode strictly unmarshals a YAML file: unknown fields and duplicate keys are errors.
func (l *loader) decode(path string, v any) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		l.errf(path, "%v", err)
		return false
	}
	if err := yaml.UnmarshalWithOptions(b, v, yaml.Strict()); err != nil {
		l.errf(path, "%v", err)
		return false
	}
	return true
}

func yamlFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !e.IsDir() && (ext == ".yaml" || ext == ".yml") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (l *loader) load() {
	c := l.cat
	vocabPath := filepath.Join(l.dir, "vocabulary.yaml")
	if _, err := os.Stat(vocabPath); err != nil {
		l.errf(vocabPath, "missing (required)")
		return
	}
	l.decode(vocabPath, &c.Vocab)

	capPath := filepath.Join(l.dir, "capabilities.yaml")
	if _, err := os.Stat(capPath); err != nil {
		l.errf(capPath, "missing (required)")
	} else {
		var cf CapabilityFile
		if l.decode(capPath, &cf) {
			c.Categories, c.Capabilities = cf.Categories, cf.Capabilities
		}
	}

	compFiles, err := yamlFiles(filepath.Join(l.dir, "components"))
	if err != nil {
		l.errf(filepath.Join(l.dir, "components"), "%v", err)
	}
	for _, path := range compFiles {
		kind := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		var comps []*Component
		if !l.decode(path, &comps) {
			continue
		}
		for _, comp := range comps {
			comp.Kind, comp.File = kind, path
			if prev, dup := c.Components[comp.ID]; dup {
				l.errf(path, "component %q already defined in %s", comp.ID, filepath.Base(prev.File))
				continue
			}
			c.Components[comp.ID] = comp
		}
	}

	macFiles, err := yamlFiles(filepath.Join(l.dir, "macs"))
	if err != nil {
		l.errf(filepath.Join(l.dir, "macs"), "%v", err)
	}
	for _, path := range macFiles {
		m := &Mac{File: path}
		if l.decode(path, m) {
			if m.SecurityChip == "" {
				m.SecurityChip = "none"
			}
			c.Macs = append(c.Macs, m)
		}
	}

	ids, err := ReadLock(filepath.Join(l.dir, LockFile))
	if err != nil {
		l.errf(filepath.Join(l.dir, LockFile), "%v", err)
	}
	c.LockedIDs = ids
}

// Stats counts catalog entities, for CLI output.
type Stats struct {
	Macs, Releases, Configs, Components, Capabilities, Uncertain int
}

func (c *Catalog) Stats() Stats {
	s := Stats{Macs: len(c.Macs), Components: len(c.Components), Capabilities: len(c.Capabilities)}
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
