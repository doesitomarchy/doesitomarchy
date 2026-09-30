package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"sort"
)

// HashFS fingerprints the catalog files the loader reads (vocabulary,
// capabilities, lock, components, macs), so a sync can skip an unchanged catalog.
// Other files under data/ (README, LICENSE, embed.go) don't affect it.
func HashFS(fsys fs.FS) (string, error) {
	names := []string{"vocabulary.yaml", "capabilities.yaml", LockFile}
	for _, pat := range []string{"components/*.yaml", "macs/*.yaml"} {
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
