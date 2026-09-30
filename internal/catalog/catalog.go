// Package catalog loads and validates the YAML catalog in data/.
//
// Phase 0 stub: it discovers the catalog files and checks that each one is
// well-formed YAML. Schema validation (unique IDs, resolvable references,
// capability applicability) arrives with the Phase 1 schemas.
package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Summary counts what was found in a catalog directory.
type Summary struct {
	Capabilities int // 1 if data/capabilities.yaml exists, else 0
	Components   int // files in data/components/
	Macs         int // files in data/macs/
}

// Load walks dir, parses every catalog file, and returns counts. All parse
// problems are collected and returned together, sorted by path.
func Load(dir string) (Summary, error) {
	var s Summary
	info, err := os.Stat(dir)
	if err != nil {
		return s, fmt.Errorf("catalog dir: %w", err)
	}
	if !info.IsDir() {
		return s, fmt.Errorf("catalog dir: %s is not a directory", dir)
	}

	var problems []string
	check := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			return
		}
		var v any
		if err := yaml.Unmarshal(b, &v); err != nil {
			problems = append(problems, fmt.Sprintf("%s: invalid YAML: %v", path, err))
		}
	}

	capPath := filepath.Join(dir, "capabilities.yaml")
	if _, err := os.Stat(capPath); err == nil {
		s.Capabilities = 1
		check(capPath)
	}

	for _, sub := range []struct {
		name  string
		count *int
	}{
		{"components", &s.Components},
		{"macs", &s.Macs},
	} {
		root := filepath.Join(dir, sub.name)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !isYAML(path) {
				return nil
			}
			*sub.count++
			check(path)
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, fmt.Sprintf("%s: %v", root, err))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return s, errors.New(strings.Join(problems, "\n"))
	}
	return s, nil
}

func isYAML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}
