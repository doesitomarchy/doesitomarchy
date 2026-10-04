package search

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// clauseKind says how a clause is evaluated against a document.
type clauseKind int

const (
	kText     clauseKind = iota // free text: words against the doc's bag
	kField                      // text field (gpu:, cpu:, …): phrase against the field's phrases
	kKey                        // exact key field (chip:, status:, …); values may carry a trailing * for prefix
	kNumber                     // numeric field (year:, size:, cores:, gles:)
	kID                         // identifier, exact or prefix
	kNumberID                   // order / A-number / EMC / hw / board exact key
)

// clause is one ANDed condition. Alternatives inside a clause are ORed.
type clause struct {
	neg   bool
	field string // "" for free text
	kind  clauseKind
	alts  []alt
	raw   string // as typed, for messages
}

type alt struct {
	words  []string // text/field: normalized words
	key    string   // key / id / number-id
	prefix bool     // key/id prefix match
	cands  []string // text: typo candidates when the word itself is not in the dictionary
	lo, hi float64  // number range (inclusive)
}

// Query is a parsed search.
type Query struct {
	Raw     string
	clauses []clause
	Errors  []string
}

// Empty reports whether nothing searchable was entered.
func (q *Query) Empty() bool { return len(q.clauses) == 0 }

type fieldSpec struct {
	kind  clauseKind
	key   string // canonical field name
	short string // help text for errors
}

// fields maps every accepted field name (including synonyms) to its spec.
var fields = map[string]fieldSpec{
	"id": {kID, "id", ""}, "identifier": {kID, "id", ""},
	"line": {kKey, "line", ""}, "model": {kKey, "line", ""},
	"form":    {kKey, "form", "laptop, desktop, all-in-one or server"},
	"year":    {kNumber, "year", "a year or range"},
	"release": {kKey, "release", "early, mid or late"},
	"size":    {kNumber, "size", "a size in inches"},
	"display": {kField, "display", ""},
	"cpu":     {kField, "cpu", ""}, "arch": {kField, "arch", ""},
	"cores": {kNumber, "cores", "a number of cores"},
	"gles":  {kNumber, "gles", "an OpenGL ES version: 2.0, 3.0, 3.1 or 3.2"},
	"gpu":   {kField, "gpu", ""}, "wifi": {kField, "wifi", ""}, "bt": {kField, "bluetooth", ""}, "bluetooth": {kField, "bluetooth", ""},
	"audio": {kField, "audio", ""}, "camera": {kField, "camera", ""}, "storage": {kField, "storage", ""},
	"ethernet": {kField, "ethernet", ""}, "thunderbolt": {kField, "thunderbolt", ""}, "firewire": {kField, "firewire", ""},
	"reader": {kField, "card-reader", ""}, "input": {kField, "input", ""}, "bridge": {kField, "bridge", ""}, "ir": {kField, "ir", ""},
	"hw":    {kNumberID, "hw", ""},
	"board": {kNumberID, "board", ""},
	"chip":  {kKey, "chip", "none, t1 or t2"},
	"efi":   {kKey, "efi", "32 or 64"},
	"port":  {kKey, "port", ""}, "feature": {kKey, "feature", ""},
	"status": {kKey, "status", "untested, supported, partial, failed, unsupported or not-compatible"},
	"scope":  {kKey, "scope", "in or out"},
	"tested": {kKey, "tested", "yes or no"},
	"order":  {kNumberID, "order", ""}, "a": {kNumberID, "a", ""}, "emc": {kNumberID, "emc", ""},
}

// FieldNames lists the canonical field names, for suggestions and help.
var FieldNames = []string{"id", "line", "form", "year", "release", "size", "display", "cpu", "arch", "cores",
	"gpu", "gles", "wifi", "bt", "audio", "camera", "storage", "ethernet", "thunderbolt", "firewire", "reader", "input", "bridge",
	"hw", "board", "chip", "efi", "port", "feature", "status", "scope", "tested", "order", "a", "emc"}

// token is one lexed piece of the query.
type token struct {
	neg    bool
	field  string // lower-cased field name, "" if none
	value  string
	quoted bool
	raw    string
}

// lex splits on whitespace, keeping quoted runs together: word, -word,
// field:value, field:"a b", "a b".
func lex(s string) []token {
	var out []token
	rs := []rune(s)
	for i := 0; i < len(rs); {
		for i < len(rs) && unicode.IsSpace(rs[i]) {
			i++
		}
		if i >= len(rs) {
			break
		}
		start := i
		var t token
		if rs[i] == '-' && i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) {
			t.neg = true
			i++
		}
		var b strings.Builder
		inQuote := false
		for i < len(rs) && (inQuote || !unicode.IsSpace(rs[i])) {
			switch {
			case rs[i] == '"':
				inQuote = !inQuote
				t.quoted = true
			case rs[i] == ':' && !inQuote && t.field == "" && !t.quoted:
				if _, ok := fields[strings.ToLower(b.String())]; ok {
					t.field = strings.ToLower(b.String())
					b.Reset()
				} else {
					b.WriteRune(rs[i])
				}
			default:
				b.WriteRune(rs[i])
			}
			i++
		}
		t.value = b.String()
		t.raw = string(rs[start:i])
		out = append(out, t)
	}
	return out
}

// words normalizes text into lower-case search words: letters, digits and
// the joiners . - / + inside a word are kept ("i7-4870hq", "21.5", "ll/a").
func words(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		w := strings.Trim(b.String(), ".-/+")
		if w != "" {
			out = append(out, w)
		}
		b.Reset()
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case (r == '.' || r == '-' || r == '/' || r == '+') && b.Len() > 0:
			b.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out
}

var (
	reYear     = regexp.MustCompile(`^(19|20)\d\d$`)
	reNum      = regexp.MustCompile(`^\d+(\.\d+)?$`)
	reSize     = regexp.MustCompile(`^(\d{2}(\.\d)?)("|in|inch|-inch)?$`)
	reHW       = regexp.MustCompile(`^(?:(pci|usb):)?([0-9a-f]{4}:[0-9a-f]{4})$`)
	reBoard    = regexp.MustCompile(`^mac-[0-9a-f]{8}([0-9a-f]{8})?$`)
	reANum     = regexp.MustCompile(`^a\d{4}$`)
	reEMC      = regexp.MustCompile(`^\d{4}(-\d)?$`)
	reIDish    = regexp.MustCompile(`^([a-z]+)(\d+)(?:[,-](\d+))?$`)
	reOrderish = regexp.MustCompile(`^[a-z0-9]{4,5}(?:[a-z]{1,2}/?[a-z])?$`)
)

// sizes are display sizes a bare number is read as.
var sizes = map[string]bool{"11": true, "12": true, "13": true, "15": true, "16": true, "17": true, "20": true, "21": true, "21.5": true, "24": true, "27": true}

// idPrefixes are short forms accepted in front of identifier numbers ("mbp11,5").
var idPrefixes = map[string]string{"mbp": "macbookpro", "mba": "macbookair", "mb": "macbook", "mm": "macmini", "mp": "macpro", "imp": "imacpro"}

// normID lower-cases an identifier and uses "," as separator, expanding short prefixes.
func normID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	m := reIDish.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	fam := m[1]
	if long, ok := idPrefixes[fam]; ok {
		fam = long
	}
	if m[3] == "" {
		return fam + m[2]
	}
	return fam + m[2] + "," + m[3]
}

// parse turns raw input into clauses, expanding aliases. ix supplies the
// alias table and the key sets used to recognise bare tokens by shape.
func (ix *Index) parse(raw string) *Query {
	q := &Query{Raw: raw}
	toks := lex(raw)
	for i := 0; i < len(toks); {
		t := toks[i]
		// Aliases: longest run of plain words (or one quoted phrase) that is an alias phrase.
		if t.field == "" {
			if n, means := ix.matchAlias(toks[i:]); n > 0 {
				sub := ix.parseNoAlias(means)
				for _, c := range sub.clauses {
					c.neg = c.neg != t.neg
					q.clauses = append(q.clauses, c)
				}
				q.Errors = append(q.Errors, sub.Errors...)
				i += n
				continue
			}
		}
		ix.addToken(q, t)
		i++
	}
	return q
}

func (ix *Index) parseNoAlias(raw string) *Query {
	q := &Query{Raw: raw}
	for _, t := range lex(raw) {
		ix.addToken(q, t)
	}
	return q
}

// matchAlias returns how many tokens the longest alias phrase at toks[0] spans.
func (ix *Index) matchAlias(toks []token) (int, string) {
	if toks[0].quoted {
		if means, ok := ix.aliases[normalizePhrase(toks[0].value)]; ok {
			return 1, means
		}
		return 0, ""
	}
	best, bestMeans := 0, ""
	var parts []string
	for n := 1; n <= len(toks) && n <= 4; n++ {
		t := toks[n-1]
		if t.field != "" || t.quoted || (n > 1 && t.neg) {
			break
		}
		parts = append(parts, strings.ToLower(t.value))
		if means, ok := ix.aliases[strings.Join(parts, " ")]; ok {
			best, bestMeans = n, means
		}
	}
	return best, bestMeans
}

func normalizePhrase(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

func (ix *Index) addToken(q *Query, t token) {
	v := strings.TrimSpace(t.value)
	if t.field != "" {
		if v == "" { // dangling "gpu:" while typing: ignore, suggestions handle it
			return
		}
		c, err := ix.fieldClause(t.field, v)
		if err != "" {
			q.Errors = append(q.Errors, err)
			return
		}
		c.neg, c.raw = t.neg, t.raw
		q.clauses = append(q.clauses, c)
		return
	}
	if v == "" {
		return
	}
	if t.quoted {
		if ws := words(v); len(ws) > 0 {
			q.clauses = append(q.clauses, clause{neg: t.neg, kind: kText, alts: []alt{{words: ws}}, raw: t.raw})
		}
		return
	}
	c := ix.bareClause(strings.ToLower(v))
	c.neg, c.raw = t.neg, t.raw
	q.clauses = append(q.clauses, c)
}

// bareClause recognises a fieldless token by its shape; see PLAN §16.3.
func (ix *Index) bareClause(v string) clause {
	if m := reHW.FindStringSubmatch(v); m != nil {
		return clause{field: "hw", kind: kNumberID, alts: []alt{{key: hwKey(m[1], m[2])}}}
	}
	if reBoard.MatchString(v) {
		return clause{field: "board", kind: kNumberID, alts: []alt{{key: v}}}
	}
	if id := normID(v); reIDish.MatchString(v) {
		if ix.ids[id] {
			return clause{field: "id", kind: kID, alts: []alt{{key: id}}}
		}
		if !strings.Contains(id, ",") && ix.idFamilies[id] {
			return clause{field: "id", kind: kID, alts: []alt{{key: id + ",", prefix: true}}}
		}
	}
	if reANum.MatchString(v) && ix.keys["a"][v] {
		return clause{field: "a", kind: kNumberID, alts: []alt{{key: v}}}
	}
	if reOrderish.MatchString(v) && ix.keys["order"][orderKey(v)] {
		return clause{field: "order", kind: kNumberID, alts: []alt{{key: orderKey(v)}}}
	}
	if reYear.MatchString(v) {
		y, _ := strconv.Atoi(v)
		if y >= 2005 && y <= 2021 {
			return clause{field: "year", kind: kNumber, alts: []alt{{lo: float64(y), hi: float64(y)}}}
		}
	}
	if reEMC.MatchString(v) && ix.keys["emc"][v] {
		return clause{field: "emc", kind: kNumberID, alts: []alt{{key: v}}}
	}
	if m := reSize.FindStringSubmatch(v); m != nil && sizes[m[1]] {
		f, _ := strconv.ParseFloat(m[1], 64)
		return clause{field: "size", kind: kNumber, alts: []alt{sizeAlt(f)}}
	}
	return clause{kind: kText, alts: []alt{{words: words(v)}}}
}

// sizeAlt follows Apple's naming, where the marketed size is the whole-inch
// part of the real one: 11 matches 11.6, 15 matches 15.4, 21 matches 21.5.
// A decimal size (21.5) matches exactly.
func sizeAlt(f float64) alt {
	if f != float64(int(f)) {
		return alt{lo: f - 0.05, hi: f + 0.05}
	}
	return alt{lo: f, hi: f + 0.999}
}

func hwKey(bus, id string) string {
	if bus == "" {
		return id
	}
	return bus + ":" + id
}

// orderKey is the part-number stem: "MC374LL/A" → "mc374".
func orderKey(s string) string {
	s = strings.ToLower(s)
	if i := strings.IndexByte(s, '/'); i > 0 && len(s) > 5 {
		s = s[:i]
	}
	if len(s) > 5 {
		// strip the region letters (LL, B, ZP…) after a 4–5 char stem ending in a digit
		for n := 5; n >= 4; n-- {
			if n <= len(s) && s[n-1] >= '0' && s[n-1] <= '9' {
				return s[:n]
			}
		}
	}
	return s
}

// fieldClause builds a clause for field:value. Values may be ORed with |.
func (ix *Index) fieldClause(name, v string) (clause, string) {
	spec := fields[name]
	c := clause{field: spec.key, kind: spec.kind}
	for _, part := range strings.Split(v, "|") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		a, err := ix.fieldAlt(spec, part)
		if err != "" {
			return c, err
		}
		c.alts = append(c.alts, a)
	}
	if len(c.alts) == 0 {
		return c, fmt.Sprintf("%s: empty value", name)
	}
	return c, ""
}

func (ix *Index) fieldAlt(spec fieldSpec, v string) (alt, string) {
	lv := strings.ToLower(v)
	switch spec.kind {
	case kID:
		id := normID(lv)
		if ix.ids[id] {
			return alt{key: id}, ""
		}
		if ix.idFamilies[id] {
			return alt{key: id + ",", prefix: true}, ""
		}
		return alt{key: id}, "" // unknown identifier: simply matches nothing
	case kNumber:
		return numberAlt(spec, lv)
	case kNumberID:
		switch spec.key {
		case "hw":
			if m := reHW.FindStringSubmatch(lv); m != nil {
				return alt{key: hwKey(m[1], m[2])}, ""
			}
			return alt{}, fmt.Sprintf("hw: expected a PCI/USB ID like 10de:0647")
		case "order":
			return alt{key: orderKey(lv)}, ""
		case "a":
			if !strings.HasPrefix(lv, "a") {
				lv = "a" + lv
			}
		}
		return alt{key: lv}, ""
	case kKey:
		return ix.keyAlt(spec, lv)
	default: // kField
		ws := words(lv)
		if len(ws) == 0 {
			return alt{}, fmt.Sprintf("%s: empty value", spec.key)
		}
		return alt{words: ws}, ""
	}
}

func numberAlt(spec fieldSpec, v string) (alt, string) {
	bad := fmt.Sprintf("%s: expected %s", spec.key, spec.short)
	if spec.key == "year" {
		bad = "year: expected a year or range (2012, >=2012, 2009..2012)"
	}
	num := func(s string) (float64, bool) {
		s = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(s, "\""), "inch"), "in")
		if !reNum.MatchString(s) {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	const inf = 1e9
	switch {
	case strings.Contains(v, ".."):
		a, b, _ := strings.Cut(v, "..")
		lo, ok1 := num(a)
		hi, ok2 := num(b)
		if !ok1 || !ok2 || lo > hi {
			return alt{}, bad
		}
		if spec.key == "size" {
			return alt{lo: sizeAlt(lo).lo, hi: sizeAlt(hi).hi}, ""
		}
		return alt{lo: lo, hi: hi}, ""
	case strings.HasPrefix(v, ">="), strings.HasPrefix(v, "<="), strings.HasPrefix(v, ">"), strings.HasPrefix(v, "<"):
		op := strings.TrimRight(v[:2], "0123456789.")
		f, ok := num(v[len(op):])
		if !ok {
			return alt{}, bad
		}
		switch op {
		case ">=":
			return alt{lo: f, hi: inf}, ""
		case "<=":
			return alt{lo: -inf, hi: f}, ""
		case ">":
			return alt{lo: f + 0.001, hi: inf}, ""
		default:
			return alt{lo: -inf, hi: f - 0.001}, ""
		}
	}
	f, ok := num(v)
	if !ok {
		return alt{}, bad
	}
	if spec.key == "size" {
		return sizeAlt(f), ""
	}
	return alt{lo: f, hi: f}, ""
}

// keyAlt validates an exact-key value; port: and feature: also accept a prefix.
func (ix *Index) keyAlt(spec fieldSpec, v string) (alt, string) {
	v = strings.ReplaceAll(strings.Join(strings.Fields(v), "-"), "_", "-")
	switch spec.key {
	case "status":
		if v == "blocked" {
			v = "not-compatible"
		}
	case "efi":
		v = strings.TrimPrefix(v, "efi")
	case "line":
		if means, ok := ix.aliases[strings.ReplaceAll(v, "-", " ")]; ok && strings.HasPrefix(means, "line:") {
			v = strings.TrimPrefix(means, "line:")
		} else if means, ok := ix.aliases[v]; ok && strings.HasPrefix(means, "line:") {
			v = strings.TrimPrefix(means, "line:")
		}
	}
	known := ix.keys[spec.key]
	if known[v] {
		return alt{key: v}, ""
	}
	if spec.key == "port" || spec.key == "feature" {
		for k := range known {
			if strings.HasPrefix(k, v) {
				return alt{key: v, prefix: true}, ""
			}
		}
	}
	if spec.short != "" {
		return alt{}, fmt.Sprintf("%s: expected %s", spec.key, spec.short)
	}
	return alt{}, fmt.Sprintf("%s: unknown value %q", spec.key, v)
}
