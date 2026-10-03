package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"sort"
)

// HashFS fingerprints the catalog files the loader reads (vocabulary,
// capabilities, lock, coverage, aliases, changelog, components, macs, port
// layouts), so a sync can skip an unchanged catalog. Other files under data/
// (README, LICENSE, embed.go, the test tools' sources/ mappings) don't affect it.
func HashFS(fsys fs.FS) (string, error) {
	names := []string{"vocabulary.yaml", "capabilities.yaml", LockFile, "coverage.yaml", "aliases.yaml", "changelog.yaml"}
	for _, pat := range []string{"components/*.yaml", "macs/*.yaml", "layouts/*.yaml"} {
		m, err := fs.Glob(fsys, pat)
		if err != nil {
			return "", err
		}
		names = append(names, m...)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, err := fs.ReadFile(fsys, n)
		if errors.Is(err, fs.ErrNotExist) && (n == "coverage.yaml" || n == "aliases.yaml" || n == "changelog.yaml") { // optional files
			continue
		}
		if err != nil {
			return "", err
		}
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
