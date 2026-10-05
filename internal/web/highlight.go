package web

import (
	"html/template"
	"strings"
	"unicode"
)

// A small syntax highlighter for the API docs' samples: JSON, shell (curl),
// JavaScript and Python. It marks keys, strings, numbers, keywords,
// functions, comments and punctuation; colours come from the theme's
// --syn-* tokens. The samples are ours, so it only needs to handle them.

var keywords = map[string]map[string]bool{
	"js": set("const", "let", "await", "async", "function", "return", "new", "import", "from", "for", "of", "if", "else", "true", "false", "null"),
	"py": set("import", "from", "for", "in", "if", "else", "return", "def", "with", "as", "True", "False", "None", "print"),
	"sh": set("export"),
}

func set(words ...string) map[string]bool {
	m := map[string]bool{}
	for _, w := range words {
		m[w] = true
	}
	return m
}

// highlight returns src as HTML with <span class="t-…"> tokens.
func highlight(lang, src string) template.HTML {
	var b strings.Builder
	r := []rune(src)
	span := func(class string, s []rune) {
		b.WriteString(`<span class="t-` + class + `">`)
		b.WriteString(template.HTMLEscapeString(string(s)))
		b.WriteString(`</span>`)
	}
	lineStart := true // at the first word of a shell command
	for i := 0; i < len(r); {
		c := r[i]
		j := i + 1
		switch {
		case c == '\n':
			b.WriteRune(c)
			// A line ending in "\" continues the command.
			lineStart = i == 0 || r[i-1] != '\\'
			i = j
			continue
		case c == '…' || (c == '#' && (lang == "sh" || lang == "py")) || (c == '/' && lang == "js" && j < len(r) && r[j] == '/'):
			// To the line's end; a "… 3 more" inside a list ends at its bracket.
			for j < len(r) && r[j] != '\n' && !(c == '…' && (r[j] == ']' || r[j] == '}')) {
				j++
			}
			span("com", r[i:j])
		case c == '"' || c == '\'' || (c == '`' && lang == "js"):
			// A shell's single quotes may span lines (a JSON body); others end at the line.
			for j < len(r) && r[j] != c && (r[j] != '\n' || (lang == "sh" && c == '\'')) {
				if r[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(r) && r[j] == c {
				j++
			}
			k := j
			for k < len(r) && r[k] == ' ' {
				k++
			}
			if k < len(r) && r[k] == ':' && lang != "sh" {
				span("key", r[i:j])
			} else {
				span("str", r[i:j])
			}
		case unicode.IsDigit(c) || (c == '-' && lang != "sh" && j < len(r) && unicode.IsDigit(r[j])):
			for j < len(r) && (unicode.IsDigit(r[j]) || r[j] == '.') {
				j++
			}
			span("num", r[i:j])
		case lang == "sh" && c == 'h' && strings.HasPrefix(string(r[i:min(len(r), i+8)]), "https://"):
			// A bare URL argument.
			for j < len(r) && !unicode.IsSpace(r[j]) {
				j++
			}
			span("str", r[i:j])
		case c == '$' && lang == "sh":
			for j < len(r) && (r[j] == '_' || unicode.IsLetter(r[j]) || unicode.IsDigit(r[j])) {
				j++
			}
			span("key", r[i:j])
		case c == '-' && lang == "sh":
			for j < len(r) && (r[j] == '-' || unicode.IsLetter(r[j])) {
				j++
			}
			span("kw", r[i:j])
		case unicode.IsLetter(c) || c == '_' || c == '$':
			for j < len(r) && (r[j] == '_' || r[j] == '$' || unicode.IsLetter(r[j]) || unicode.IsDigit(r[j])) {
				j++
			}
			w := string(r[i:j])
			switch {
			case lang == "json" && (w == "true" || w == "false" || w == "null"):
				span("num", r[i:j])
			case keywords[lang][w]:
				span("kw", r[i:j])
			case lang == "sh" && lineStart:
				span("fn", r[i:j])
			case lang != "sh" && j < len(r) && r[j] == '(':
				span("fn", r[i:j])
			default:
				b.WriteString(template.HTMLEscapeString(w))
			}
		case strings.ContainsRune("{}[](),:;=", c) && lang != "json":
			span("p", r[i:j]) // JSON's punctuation takes the block's muted colour: fewer spans
		default:
			b.WriteString(template.HTMLEscapeString(string(c)))
		}
		if c != ' ' {
			lineStart = false
		}
		i = j
	}
	return template.HTML(b.String())
}
