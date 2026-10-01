package catalog

import "fmt"

// ChangeEntry is one item of data/changelog.yaml, the public catalog
// changelog shown at /changelog.
type ChangeEntry struct {
	Date    string   `yaml:"date" json:"date"`
	Title   string   `yaml:"title" json:"title"`
	Summary string   `yaml:"summary" json:"summary"`
	Notes   []string `yaml:"notes" json:"notes"`
}

func (l *loader) validateChangelog() {
	const path = "changelog.yaml"
	prev := ""
	for i, e := range l.cat.Changelog {
		where := fmt.Sprintf("entry %d", i)
		if !validDate(e.Date) {
			l.errf(path, "%s: date %q must be YYYY-MM-DD", where, e.Date)
		}
		if e.Title == "" || e.Summary == "" {
			l.errf(path, "%s: title and summary are required", where)
		}
		if prev != "" && e.Date > prev {
			l.errf(path, "%s: entries must be newest first", where)
		}
		prev = e.Date
	}
}
