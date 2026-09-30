package catalog

import "fmt"

// CoverageFile is data/coverage.yaml: which configurations are out of scope
// for the site's coverage metrics. Excluded configs are still listed and can
// still be tested; they are just not counted in N (PLAN.md §6.1). This is
// separate from hard_blocker, which makes a config Not compatible.
type CoverageFile struct {
	Exclude []CoverageRule `yaml:"exclude" json:"exclude"`
}

// CoverageRule excludes every config matching all of its conditions.
type CoverageRule struct {
	Reason string        `yaml:"reason" json:"reason"`
	When   CoverageMatch `yaml:"when" json:"when"`
}

// CoverageMatch conditions are ANDed; at least one must be set.
type CoverageMatch struct {
	AnnouncedBefore string   `yaml:"announced_before" json:"announced_before"` // YYYY-MM-DD, compared with the config's release date
	Line            string   `yaml:"line" json:"line"`                         // vocabulary line key, e.g. xserve
	Identifiers     []string `yaml:"identifiers" json:"identifiers"`           // e.g. ["iMac4,1"]
}

func (w CoverageMatch) empty() bool {
	return w.AnnouncedBefore == "" && w.Line == "" && len(w.Identifiers) == 0
}

func (w CoverageMatch) matches(m *Mac, r *Release) bool {
	if w.AnnouncedBefore != "" && !(r.Announced < w.AnnouncedBefore) { // ISO dates compare as strings
		return false
	}
	if w.Line != "" && m.Line != w.Line {
		return false
	}
	if len(w.Identifiers) > 0 {
		found := false
		for _, id := range w.Identifiers {
			found = found || id == m.Identifier
		}
		if !found {
			return false
		}
	}
	return true
}

// CoverageExclusion returns the reason of the first rule excluding configs of
// release r of m, or "" if they count toward coverage.
func (c *Catalog) CoverageExclusion(m *Mac, r *Release) string {
	for _, rule := range c.CoverageRules {
		if rule.When.matches(m, r) {
			return rule.Reason
		}
	}
	return ""
}

func (l *loader) validateCoverage() {
	const path = "coverage.yaml"
	idents := map[string]bool{}
	for _, m := range l.cat.Macs {
		idents[m.Identifier] = true
	}
	for i, rule := range l.cat.CoverageRules {
		where := fmt.Sprintf("exclude[%d]", i)
		if rule.Reason == "" {
			l.errf(path, "%s: reason is required", where)
		}
		if rule.When.empty() {
			l.errf(path, "%s: when needs at least one condition (announced_before, line, identifiers)", where)
		}
		if d := rule.When.AnnouncedBefore; d != "" && !validDate(d) {
			l.errf(path, "%s: announced_before %q must be YYYY-MM-DD", where, d)
		}
		if ln := rule.When.Line; ln != "" {
			if _, ok := l.cat.Vocab.Lines[ln]; !ok {
				l.errf(path, "%s: unknown line %q", where, ln)
			}
		}
		for _, id := range rule.When.Identifiers {
			if !idents[id] {
				l.errf(path, "%s: unknown identifier %q", where, id)
			}
		}
	}
}
