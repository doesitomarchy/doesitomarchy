package catalog

import (
	"fmt"
	"strings"
)

// Alias is one entry of data/aliases.yaml: search phrases that stand for a
// query fragment, e.g. "trash can" → `id:"MacPro6,1"`. The search package
// checks that every Means parses and matches something (doiomad validate).
type Alias struct {
	Match []string `yaml:"match" json:"match"`
	Means string   `yaml:"means" json:"means"`
}

// NormalizePhrase lower-cases and collapses whitespace, the form alias
// phrases are compared in.
func NormalizePhrase(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

func (l *loader) validateAliases() {
	const path = "aliases.yaml"
	seen := map[string]int{}
	for i, a := range l.cat.Aliases {
		where := fmt.Sprintf("entry %d", i)
		if len(a.Match) == 0 || strings.TrimSpace(a.Means) == "" {
			l.errf(path, "%s: match and means are required", where)
		}
		for _, m := range a.Match {
			n := NormalizePhrase(m)
			if n == "" || strings.ContainsAny(n, `":`) {
				l.errf(path, "%s: phrase %q must be plain words (no quotes or colons)", where, m)
				continue
			}
			if j, dup := seen[n]; dup {
				l.errf(path, "%s: phrase %q already used by entry %d", where, n, j)
			}
			seen[n] = i
		}
	}
}
