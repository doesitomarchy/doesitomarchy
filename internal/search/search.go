package search

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// Score weights (PLAN §16.4): exact IDs and part numbers outrank fielded
// matches, which outrank plain words, prefixes and typo matches.
const (
	scoreID       = 1000
	scoreNumberID = 800
	scoreIDPrefix = 500
	scoreField    = 100
	scoreWord     = 20
	scorePrefix   = 10
	scoreFuzzy    = 5
)

// Result is one model identifier with the configurations that matched.
type Result struct {
	Identifier string
	Slug       string
	LineName   string
	Releases   []string // names of the matched configs' releases, catalog order
	Configs    []ConfigHit
	// AllConfigs is true when every config of the Mac matched, so listing
	// them adds nothing.
	AllConfigs bool
	Score      int
	Latest     string // newest accepted result among matched configs ("" if none)
	newest     string
}

// ConfigHit is a matched configuration.
type ConfigHit struct {
	ID          string
	Label       string
	ReleaseName string
}

// Response is the outcome of a search.
type Response struct {
	Query      string
	Results    []Result
	Errors     []string // parse problems, shown as chips; the rest of the query still runs
	DidYouMean string   // a corrected query, only when there are no results
	Took       time.Duration
}

// SortRecent orders results by their newest accepted test result, newest
// first (the "Latest with Test" preset); untested Macs keep relevance order
// after the tested ones.
func SortRecent(rs []Result) {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Latest > rs[j].Latest })
}

// Search runs a query. An empty query returns no results and no errors.
func (ix *Index) Search(raw string) Response {
	start := time.Now()
	q := ix.parse(raw)
	resp := Response{Query: raw, Errors: q.Errors}
	if q.Empty() {
		resp.Took = time.Since(start)
		return resp
	}
	ix.expandFuzzy(q)

	type group struct {
		res  *Result
		docs []*doc
	}
	groups := map[*macInfo]*group{}
	var order []*macInfo
	for _, d := range ix.docs {
		score, ok := ix.matchDoc(d, q)
		if !ok {
			continue
		}
		g := groups[d.mac]
		if g == nil {
			g = &group{res: &Result{Identifier: d.mac.Identifier, Slug: d.mac.Slug, LineName: d.mac.LineName}}
			groups[d.mac] = g
			order = append(order, d.mac)
		}
		g.docs = append(g.docs, d)
		if score > g.res.Score {
			g.res.Score = score
		}
		if d.Announced > g.res.newest {
			g.res.newest = d.Announced
		}
		if d.Latest > g.res.Latest {
			g.res.Latest = d.Latest
		}
	}
	for _, m := range order {
		g := groups[m]
		seen := map[string]bool{}
		for _, d := range g.docs {
			g.res.Configs = append(g.res.Configs, ConfigHit{d.ConfigID, d.Label, d.ReleaseName})
			if !seen[d.ReleaseName] {
				seen[d.ReleaseName] = true
				g.res.Releases = append(g.res.Releases, d.ReleaseName)
			}
		}
		g.res.AllConfigs = len(g.docs) == m.configs
		resp.Results = append(resp.Results, *g.res)
	}
	sort.SliceStable(resp.Results, func(i, j int) bool {
		a, b := resp.Results[i], resp.Results[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.newest != b.newest {
			return a.newest > b.newest
		}
		return a.Identifier < b.Identifier
	})
	if len(resp.Results) == 0 {
		resp.DidYouMean = ix.didYouMean(raw, q)
	}
	resp.Took = time.Since(start)
	return resp
}

// matchDoc checks every clause and returns the summed score.
func (ix *Index) matchDoc(d *doc, q *Query) (int, bool) {
	total := 0
	for i := range q.clauses {
		c := &q.clauses[i]
		s := ix.matchClause(d, c)
		if c.neg {
			if s > 0 {
				return 0, false
			}
			continue
		}
		if s == 0 {
			return 0, false
		}
		total += s
	}
	return total, true
}

func (ix *Index) matchClause(d *doc, c *clause) int {
	best := 0
	for i := range c.alts {
		if s := ix.matchAlt(d, c, &c.alts[i]); s > best {
			best = s
		}
	}
	return best
}

func (ix *Index) matchAlt(d *doc, c *clause, a *alt) int {
	switch c.kind {
	case kID:
		if a.prefix {
			if strings.HasPrefix(d.id, a.key) {
				return scoreIDPrefix
			}
		} else if d.id == a.key {
			return scoreID
		}
	case kNumberID:
		for _, v := range d.keys[c.field] {
			if v == a.key {
				return scoreNumberID
			}
		}
	case kKey:
		for _, v := range d.keys[c.field] {
			if v == a.key || (a.prefix && strings.HasPrefix(v, a.key)) {
				return scoreField
			}
		}
	case kNumber:
		var vals []float64
		switch c.field {
		case "year":
			vals = []float64{d.year}
		case "size":
			vals = d.sizes
		case "cores":
			vals = d.cores
		case "gles":
			vals = d.gles
		}
		for _, v := range vals {
			if v >= a.lo && v <= a.hi {
				return scoreField
			}
		}
	case kField:
		if phraseIn(d.text[c.field], a.words, 2) > 0 {
			return scoreField
		}
	case kText:
		if len(a.words) > 1 {
			return phraseIn(d.bag, a.words, 3)
		}
		if len(a.words) == 1 {
			if s := phraseIn(d.bag, a.words, 3); s > 0 {
				return s
			}
		}
		for _, w := range a.cands { // typo candidates
			if phraseIn(d.bag, []string{w}, 0) > 0 {
				return scoreFuzzy
			}
		}
	}
	return 0
}

// phraseIn finds ws as consecutive words in any phrase. Each word may match
// exactly or, when at least minPrefix long (0 disables), as a prefix. It
// returns scoreWord for an all-exact match, scorePrefix otherwise, 0 if none.
func phraseIn(phrases [][]string, ws []string, minPrefix int) int {
	best := 0
	for _, ph := range phrases {
		for i := 0; i+len(ws) <= len(ph); i++ {
			exact, ok := true, true
			for j, w := range ws {
				p := ph[i+j]
				if p == w {
					continue
				}
				if minPrefix > 0 && len(w) >= minPrefix && isPrefix(p, w) {
					exact = false
					continue
				}
				ok = false
				break
			}
			if ok {
				if exact {
					return scoreWord
				}
				best = scorePrefix
			}
		}
	}
	return best
}

// isPrefix is strings.HasPrefix, except that a prefix may not stop in the
// middle of a number: "bcm4360" must not match "bcm43602", but "6770"
// matches "6770m" and "nv" matches "nvidia".
func isPrefix(word, p string) bool {
	if !strings.HasPrefix(word, p) {
		return false
	}
	if len(word) == len(p) {
		return true
	}
	last, next := p[len(p)-1], word[len(p)]
	return !(last >= '0' && last <= '9' && next >= '0' && next <= '9')
}

// fuzzyBudget is the allowed edit distance for a word (PLAN §16.4).
func fuzzyBudget(w string) int {
	for _, r := range w {
		if unicode.IsDigit(r) {
			return 0 // numbers and IDs are never fuzzy
		}
	}
	switch n := len([]rune(w)); {
	case n >= 6:
		return 2
	case n >= 4:
		return 1
	}
	return 0
}

// expandFuzzy gives single free-text words with no exact or prefix match in
// the dictionary a list of close dictionary words to match instead.
func (ix *Index) expandFuzzy(q *Query) {
	for ci := range q.clauses {
		c := &q.clauses[ci]
		if c.kind != kText {
			continue
		}
		for ai := range c.alts {
			a := &c.alts[ai]
			if len(a.words) != 1 || ix.inDict(a.words[0]) {
				continue
			}
			a.cands = ix.closeWords(a.words[0], fuzzyBudget(a.words[0]))
		}
	}
}

// inDict reports whether w matches a dictionary word exactly or as a 3+ prefix.
func (ix *Index) inDict(w string) bool {
	if ix.dictSet[w] {
		return true
	}
	if len(w) < 3 {
		return false
	}
	for i := sort.SearchStrings(ix.dict, w); i < len(ix.dict) && strings.HasPrefix(ix.dict[i], w); i++ {
		if isPrefix(ix.dict[i], w) {
			return true
		}
	}
	return false
}

// closeWords returns dictionary words within max edits of w, closest first.
func (ix *Index) closeWords(w string, max int) []string {
	if max == 0 {
		return nil
	}
	type cand struct {
		w string
		d int
	}
	var cs []cand
	for _, dw := range ix.dict {
		if abs(len(dw)-len(w)) > max {
			continue
		}
		if d := osaDistance(w, dw, max); d <= max {
			cs = append(cs, cand{dw, d})
		}
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].d != cs[j].d {
			return cs[i].d < cs[j].d
		}
		return cs[i].w < cs[j].w
	})
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.w
	}
	return out
}

// didYouMean replaces free-text words that matched nothing with the closest
// dictionary word (up to 2 edits); "" when there is nothing useful to offer.
func (ix *Index) didYouMean(raw string, q *Query) string {
	changed := false
	out := raw
	for _, c := range q.clauses {
		if c.kind != kText || c.neg || len(c.alts) != 1 || len(c.alts[0].words) != 1 {
			continue
		}
		w := c.alts[0].words[0]
		if ix.inDict(w) || len(w) < 4 || fuzzyBudget(w) == 0 && len(w) < 4 {
			continue
		}
		if cs := ix.closeWords(w, 2); len(cs) > 0 && cs[0] != w {
			out = replaceWordFold(out, w, cs[0])
			changed = true
		}
	}
	if !changed {
		return ""
	}
	return out
}

func replaceWordFold(s, old, repl string) string {
	i := strings.Index(strings.ToLower(s), old)
	if i < 0 {
		return s
	}
	return s[:i] + repl + s[i+len(old):]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// osaDistance is the optimal-string-alignment Damerau-Levenshtein distance
// (adjacent transpositions count as one edit). It stops early and returns
// max+1 once the distance must exceed max.
func osaDistance(a, b string, max int) int {
	ra, rb := []rune(a), []rune(b)
	n, m := len(ra), len(rb)
	prev2 := make([]int, m+1)
	prev := make([]int, m+1)
	cur := make([]int, m+1)
	for j := 0; j <= m; j++ {
		prev[j] = j
	}
	for i := 1; i <= n; i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= m; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			v := min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				v = min(v, prev2[j-2]+1)
			}
			cur[j] = v
			rowMin = min(rowMin, v)
		}
		if rowMin > max {
			return max + 1
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[m]
}
