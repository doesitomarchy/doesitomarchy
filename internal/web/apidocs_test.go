package web

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	ts := newTestServer(t)
	res, err := ts.Client().Get(ts.URL + url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestHighlight(t *testing.T) {
	for _, c := range []struct{ lang, src, want string }{
		{"json", `{"a": "b", "n": -1, "ok": true}`, `<span class="t-key">&#34;a&#34;</span>: <span class="t-str">&#34;b&#34;</span>`},
		{"json", `{"ok": true}`, `<span class="t-num">true</span>`},
		{"json", `[1, … 3 more]`, `<span class="t-com">… 3 more</span>]`},
		{"sh", "curl -X POST https://x.test/a?b=1 \\\n  -H \"A: b\"", `<span class="t-fn">curl</span> <span class="t-kw">-X</span> POST <span class="t-str">https://x.test/a?b=1</span>`},
		{"sh", "# note\ncurl $KEY", `<span class="t-com"># note</span>` + "\n" + `<span class="t-fn">curl</span> <span class="t-key">$KEY</span>`},
		{"js", `const r = await fetch("u"); // go`, `<span class="t-kw">const</span> r <span class="t-p">=</span> <span class="t-kw">await</span> <span class="t-fn">fetch</span>`},
		{"py", `x = requests.get(f"{A}/b")  # c`, `requests.<span class="t-fn">get</span><span class="t-p">(</span>f<span class="t-str">&#34;{A}/b&#34;</span><span class="t-p">)</span>  <span class="t-com"># c</span>`},
		{"json", `"<b>"`, `&lt;b&gt;`},
	} {
		if got := string(highlight(c.lang, c.src)); !strings.Contains(got, c.want) {
			t.Errorf("%s %q:\n got %s\nwant %s", c.lang, c.src, got, c.want)
		}
	}
}

func TestAbbreviate(t *testing.T) {
	got := abbreviate([]byte(`{"a": [1, 2, 3, 4], "o": [{"x": 1}, {"x": 2}, {"x": 3}], "deep": {"in": {"x": 1}}}`), 2, 1)
	want := "{\n  \"a\": [1, 2, … 2 more],\n  \"o\": [{…}, {…}, … 1 more],\n  \"deep\": {…}\n}"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// Every endpoint is in the OpenAPI document and llms.txt, and every
// example the docs show really answers.
func TestAPIDescriptions(t *testing.T) {
	code, body := get(t, "/api/v1/openapi.json")
	var doc struct {
		OpenAPI    string                    `json:"openapi"`
		Paths      map[string]map[string]any `json:"paths"`
		Components struct {
			Schemas map[string]any `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(body), &doc); code != 200 || err != nil || doc.OpenAPI != "3.1.0" {
		t.Fatalf("openapi: %d %v", code, err)
	}
	// Every component is a real schema, and every $ref resolves.
	for name, sch := range doc.Components.Schemas {
		if m, _ := sch.(map[string]any); m["type"] == nil {
			t.Errorf("openapi: component %s has no type: %v", name, sch)
		}
	}
	for _, ref := range strings.Split(body, `"$ref": "#/components/schemas/`)[1:] {
		name := ref[:strings.IndexByte(ref, '"')]
		if doc.Components.Schemas[name] == nil {
			t.Errorf("openapi: dangling $ref %s", name)
		}
	}
	_, llms := get(t, "/llms.txt")
	ts, _, _ := liveServer(t)
	for _, e := range apiEndpoints {
		p := strings.TrimPrefix(e.Path, "/api/v1")
		if p == "" {
			p = "/"
		}
		if doc.Paths[p][strings.ToLower(e.Method)] == nil {
			t.Errorf("openapi: no %s %s", e.Method, p)
		}
		if !strings.Contains(llms, "`"+e.Method+" "+e.Path+"`") {
			t.Errorf("llms.txt: no %s %s", e.Method, e.Path)
		}
		if docAnchors[e.Name] == "" {
			t.Errorf("%s: no docs anchor", e.Name)
		}
		if e.Example == "" || e.Name == "report" {
			continue
		}
		res, err := ts.Client().Get(ts.URL + e.Example)
		if err != nil || res.StatusCode != 200 {
			t.Errorf("%s: example %s: %v %v", e.Name, e.Example, err, res.StatusCode)
		}
		res.Body.Close()
	}
}

func TestAPIDocsPage(t *testing.T) {
	code, body := get(t, "/api")
	if code != 200 {
		t.Fatal(code)
	}
	for _, want := range []string{
		`id="` + docAnchors["config"] + `"`, `id="ai"`, `id="changelog"`, "The API opens", `href="/llms.txt"`, `href="/api/v1/openapi.json"`,
		"Response: 200 OK", `<span class="t-key">&#34;identifier&#34;</span>`, // a live response, highlighted
		`data-copy="code-match-sh"`, `id="code-match-py"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/api: missing %q", want)
		}
	}
	// Every in-page link has its target.
	for _, part := range strings.Split(body, `href="#`)[1:] {
		id := part[:strings.IndexByte(part, '"')]
		if id != "main" && !strings.Contains(body, `id="`+id+`"`) {
			t.Errorf("/api: link to missing #%s", id)
		}
	}
	if code, body := get(t, "/api/v1/nope"); code != 404 || !strings.Contains(body, `"error"`) {
		t.Errorf("unknown endpoint: %d %s", code, body)
	}
}

// The API changelog is newest first, dated, and complete.
func TestAPIChangelog(t *testing.T) {
	if len(apiChangelog) == 0 {
		t.Fatal("no entries")
	}
	prev := ""
	for i, e := range apiChangelog {
		if _, err := time.Parse("2006-01-02", e.Date); err != nil {
			t.Errorf("entry %d: date %q must be YYYY-MM-DD", i, e.Date)
		}
		if e.Title == "" || e.Summary == "" {
			t.Errorf("entry %d: title and summary are required", i)
		}
		if prev != "" && e.Date > prev {
			t.Errorf("entry %d: entries must be newest first", i)
		}
		prev = e.Date
	}
	if got := codeMarks("a `b<c>` d"); got != "a <code>b&lt;c&gt;</code> d" {
		t.Errorf("codeMarks: %s", got)
	}
}
