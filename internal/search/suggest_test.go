package search

import (
	"strings"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

func labels(ss []Suggestion) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Label)
	}
	return out
}

func TestSuggest(t *testing.T) {
	ix := index(t)
	tests := []struct {
		q      string
		cursor int
		want   string // a label that must be offered
		query  string // the rewritten query for that label, if checked
	}{
		{"gp", -1, "gpu:", "gpu:"},
		{"mbp gp", -1, "gpu:", "mbp gpu:"},
		{"gpu:", -1, "NVIDIA GeForce 9400M", `gpu:"nvidia geforce 9400m"`},
		{"gpu:nv", -1, "NVIDIA GeForce 9400M", ""},
		{"chip:", -1, "t2", "chip:t2"},
		{"year:", -1, "2020", ""},
		{"line:m", -1, "macbook-pro", ""},
		{"MacBookPro11", -1, "MacBookPro11,5", "MacBookPro11,5"},
		{"MacBookPro1", -1, "MacBookPro1,1", ""},
		{"tras", -1, "trash can", `"trash can"`},
		{"-fea", -1, "feature:", "-feature:"},
		{"gp year:2012", 2, "gpu:", "gpu: year:2012"},
	}
	for _, tt := range tests {
		t.Run(tt.q, func(t *testing.T) {
			got := ix.Suggest(tt.q, tt.cursor)
			if len(got) > maxSuggestions {
				t.Fatalf("%d suggestions, max %d", len(got), maxSuggestions)
			}
			for _, s := range got {
				if s.Label == tt.want {
					if tt.query != "" && s.Query != tt.query {
						t.Errorf("query %q, want %q", s.Query, tt.query)
					}
					if s.Cursor < 0 || s.Cursor > len([]rune(s.Query)) {
						t.Errorf("cursor %d out of range", s.Cursor)
					}
					return
				}
			}
			t.Errorf("%q not offered; got %v", tt.want, labels(got))
		})
	}
	for _, q := range []string{"", "   ", "nope:", "zzzz:"} {
		if got := ix.Suggest(q, -1); len(got) != 0 {
			t.Errorf("Suggest(%q) = %v, want nothing", q, labels(got))
		}
	}
}

// Every value suggested for a field must itself find something.
func TestSuggestedValuesMatch(t *testing.T) {
	ix := index(t)
	for _, f := range []string{"gpu", "wifi", "bt", "cpu", "arch", "line", "chip", "port", "feature", "year", "status", "scope"} {
		for _, s := range ix.Suggest(f+":", -1) {
			if r := ix.Search(s.Query); len(r.Results) == 0 || len(r.Errors) > 0 {
				t.Errorf("suggestion %q finds nothing (errors %v)", s.Query, r.Errors)
			}
		}
	}
}

func TestParseErrorsAndShapes(t *testing.T) {
	ix := index(t)
	for q, want := range map[string]string{
		"chip:t3":       "chip: expected none, t1 or t2",
		"size:big":      "size: expected",
		"hw:nope":       "hw: expected",
		"line:toaster":  `line: unknown value "toaster"`,
		"year:2012..20": "year: expected",
	} {
		if r := ix.Search(q); !strings.Contains(strings.Join(r.Errors, ";"), want) {
			t.Errorf("%s: errors %v, want %q", q, r.Errors, want)
		}
	}
	// Valid values of result-driven fields are never errors, even when no Mac has them yet.
	for f, vs := range closed {
		for _, v := range vs {
			if r := ix.Search(f + ":" + v); len(r.Errors) > 0 {
				t.Errorf("%s:%s: errors %v", f, v, r.Errors)
			}
		}
	}
	if r := ix.Search("status:great"); !strings.Contains(strings.Join(r.Errors, ";"), "status: expected") {
		t.Errorf("status:great: errors %v", r.Errors)
	}
	// A bad clause doesn't stop the rest of the query.
	if r := ix.Search("chip:t3 mbp 2011"); len(r.Results) != 3 || len(r.Errors) != 1 {
		t.Errorf("partial query: %d results, errors %v", len(r.Results), r.Errors)
	}
}

func TestOsaDistance(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		d    int
	}{
		{"imac", "imca", 1}, {"macbook", "macbok", 1}, {"macbook", "macbuk", 2}, {"retina", "retna", 1},
		{"mini", "mnii", 1}, {"abc", "abc", 0}, {"", "abc", 3}, {"zzzzzz", "macbook", 3},
	} {
		if got := osaDistance(tt.a, tt.b, 5); got != tt.d && !(got > 2 && tt.d > 2) {
			t.Errorf("osa(%q,%q) = %d, want %d", tt.a, tt.b, got, tt.d)
		}
	}
}

func TestAliasesValid(t *testing.T) {
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	if p := ValidateAliases(c); len(p) > 0 {
		t.Fatalf("alias problems:\n%s", strings.Join(p, "\n"))
	}
	bad := *c
	bad.Aliases = []catalog.Alias{{Match: []string{"x"}, Means: "chip:t9"}, {Match: []string{"y"}, Means: `id:"Nope1,1"`}}
	if p := ValidateAliases(&bad); len(p) != 2 {
		t.Fatalf("want 2 problems, got %v", p)
	}
}
