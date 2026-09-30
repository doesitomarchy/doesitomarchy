package search

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

// Suggestion is one autocomplete entry. Query is the whole query with the
// token at the cursor replaced; Cursor is where the caret goes afterwards.
type Suggestion struct {
	Label  string
	Kind   string // "field", "value", "id" or "alias"
	Query  string
	Cursor int
}

const maxSuggestions = 8

// Suggest completes the token under cursor (a rune offset into q; negative
// or past the end means the end of q).
func (ix *Index) Suggest(q string, cursor int) []Suggestion {
	rs := []rune(q)
	if cursor < 0 || cursor > len(rs) {
		cursor = len(rs)
	}
	start, end := tokenBounds(rs, cursor)
	tok := string(rs[start:end])
	neg := ""
	if strings.HasPrefix(tok, "-") {
		neg, tok = "-", tok[1:]
	}
	replace := func(label, kind, text string) Suggestion {
		nq := string(rs[:start]) + neg + text + string(rs[end:])
		return Suggestion{Label: label, Kind: kind, Query: nq, Cursor: start + utf8.RuneCountInString(neg+text)}
	}

	var out []Suggestion
	if f, v, ok := strings.Cut(tok, ":"); ok {
		spec, known := fields[strings.ToLower(f)]
		if !known {
			return nil
		}
		v = strings.ToLower(strings.Trim(v, `"`))
		for _, s := range ix.fieldValues(spec) {
			if matchesTyped(s, v) {
				out = append(out, replace(s, "value", strings.ToLower(f)+":"+quoteIfNeeded(s)))
				if len(out) == maxSuggestions {
					break
				}
			}
		}
		return out
	}

	lt := strings.ToLower(tok)
	if lt == "" {
		return nil
	}
	for _, f := range FieldNames {
		if strings.HasPrefix(f, lt) {
			out = append(out, replace(f+":", "field", f+":"))
		}
	}
	if len(lt) >= 3 {
		nid := normID(lt)
		for _, m := range ix.macs {
			if strings.HasPrefix(normID(m.Identifier), nid) && len(out) < maxSuggestions {
				out = append(out, replace(m.Identifier, "id", m.Identifier))
			}
		}
	}
	if len(lt) >= 2 {
		var phrases []string
		for p := range ix.aliases {
			if strings.HasPrefix(p, lt) && p != lt {
				phrases = append(phrases, p)
			}
		}
		sort.Strings(phrases)
		for _, p := range phrases {
			if len(out) < maxSuggestions {
				out = append(out, replace(p, "alias", quoteIfNeeded(p)))
			}
		}
	}
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

// tokenBounds finds the whitespace-delimited token (quotes kept together)
// that ends at or contains cursor.
func tokenBounds(rs []rune, cursor int) (int, int) {
	start := 0
	inQuote := false
	for i := 0; i < cursor; i++ {
		switch {
		case rs[i] == '"':
			inQuote = !inQuote
		case unicode.IsSpace(rs[i]) && !inQuote:
			start = i + 1
		}
	}
	end := cursor
	for end < len(rs) && (inQuote || !unicode.IsSpace(rs[end])) {
		if rs[end] == '"' {
			inQuote = !inQuote
		}
		end++
	}
	return start, end
}

// fieldValues lists the values offered for a field, most common first.
func (ix *Index) fieldValues(spec fieldSpec) []string {
	var out []string
	switch spec.key {
	case "size":
		return []string{"11", "12", "13", "15", "16", "17", "20", "21.5", "24", "27"}
	case "cores":
		return []string{"2", "4", "6", "8", "10", "12", "18", "28"}
	case "year":
		for _, s := range ix.suggest["year"] {
			out = append(out, s.Value)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(out)))
		return out
	}
	for _, s := range ix.suggest[spec.key] {
		out = append(out, s.Value)
	}
	return out
}

// matchesTyped: empty input matches everything; otherwise any word of the
// candidate starts with the typed text, or the candidate does.
func matchesTyped(candidate, typed string) bool {
	if typed == "" {
		return true
	}
	lc := strings.ToLower(candidate)
	if strings.HasPrefix(lc, typed) {
		return true
	}
	for _, w := range words(lc) {
		if strings.HasPrefix(w, typed) {
			return true
		}
	}
	return false
}

func quoteIfNeeded(s string) string {
	if strings.ContainsAny(s, " ,") {
		return strconv.Quote(strings.ToLower(s))
	}
	return strings.ToLower(s)
}

// ValidateAliases checks every alias in the catalog parses cleanly and
// matches at least one configuration (run by `doioma validate`).
func ValidateAliases(c *catalog.Catalog) []string {
	ix := Build(c, nil)
	var problems []string
	for i, a := range c.Aliases {
		q := ix.parseNoAlias(a.Means)
		if len(q.Errors) > 0 {
			problems = append(problems, fmt.Sprintf("aliases.yaml: entry %d (%s): %s", i, strings.Join(a.Match, ", "), strings.Join(q.Errors, "; ")))
			continue
		}
		if q.Empty() {
			problems = append(problems, fmt.Sprintf("aliases.yaml: entry %d (%s): means is empty", i, strings.Join(a.Match, ", ")))
			continue
		}
		ix.expandFuzzy(q)
		hit := false
		for _, d := range ix.docs {
			if _, ok := ix.matchDoc(d, q); ok {
				hit = true
				break
			}
		}
		if !hit {
			problems = append(problems, fmt.Sprintf("aliases.yaml: entry %d (%s): %q matches no configuration", i, strings.Join(a.Match, ", "), a.Means))
		}
	}
	return problems
}
