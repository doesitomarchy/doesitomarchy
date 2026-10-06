package web

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

// The /api docs page (PLAN §22.7): a single page, Stripe-style, with each
// section's code beside it. Request samples are generated per endpoint in
// curl, JavaScript and Python; responses come from the real handlers,
// shortened, so the samples can't go stale.

// codeTab is one highlighted sample.
type codeTab struct {
	ID, Lang, Label string
	HTML            template.HTML
}

// codeBlock is a code panel: a title (e.g. "GET /api/v1/macs") and one or
// more tabs, one per language.
type codeBlock struct {
	Method, Title string
	Tabs          []codeTab
}

var langLabels = map[string]string{"sh": "curl", "js": "JavaScript", "py": "Python", "json": "JSON", "text": "Text"}

func tab(id, lang, src string) codeTab {
	h := template.HTML(template.HTMLEscapeString(src))
	if lang != "text" {
		h = highlight(lang, src)
	}
	return codeTab{ID: "code-" + id + "-" + lang, Lang: lang, Label: langLabels[lang], HTML: h}
}

// resultVar names what each endpoint's reply is called in the samples.
var resultVar = map[string]string{"macs": "data", "capabilities": "data", "submit": "receipt", "openapi": "spec"}

// requestSample is how to call e, in three languages.
func requestSample(e apiEndpoint) codeBlock {
	url := BaseURL + e.Path
	if e.Example != "" {
		url = BaseURL + e.Example
	}
	v := resultVar[e.Name]
	if v == "" {
		v = e.Name
	}
	var sh, js, py string
	switch {
	case e.Key:
		sh = fmt.Sprintf("curl -X POST %s \\\n  -H \"Authorization: Bearer $DOI_KEY\" \\\n  -H \"Content-Type: application/json\" \\\n  --data-binary @report.json", url)
		js = fmt.Sprintf("import { readFile } from \"node:fs/promises\";\n\nconst res = await fetch(\"%s\", {\n  method: \"POST\",\n  headers: {\n    Authorization: `Bearer ${process.env.DOI_KEY}`,\n    \"Content-Type\": \"application/json\",\n  },\n  body: await readFile(\"report.json\"),\n});\nconst %s = await res.json();", url, v)
		py = fmt.Sprintf("import os\nimport requests\n\nwith open(\"report.json\", \"rb\") as f:\n    %s = requests.post(\n        \"%s\",\n        headers={\n            \"Authorization\": f\"Bearer {os.environ['DOI_KEY']}\",\n            \"Content-Type\": \"application/json\",\n        },\n        data=f,\n    ).json()", v, url)
	case e.Method == "POST":
		sh = fmt.Sprintf("curl -X POST %s \\\n  -H \"Content-Type: application/json\" \\\n  -d '%s'", url, e.Body)
		// No Content-Type header: a browser then sends it without a CORS preflight.
		js = fmt.Sprintf("const res = await fetch(\"%s\", {\n  method: \"POST\",\n  body: JSON.stringify(%s),\n});\nconst %s = await res.json();", url, e.Body, v)
		py = fmt.Sprintf("import requests\n\n%s = requests.post(\n    \"%s\",\n    json=%s,\n).json()", v, url, e.Body)
	default:
		sh = "curl " + url
		js = fmt.Sprintf("const res = await fetch(\"%s\");\nconst %s = await res.json();", url, v)
		py = fmt.Sprintf("import requests\n\n%s = requests.get(\"%s\").json()", v, url)
	}
	return codeBlock{Method: e.Method, Title: e.Path, Tabs: []codeTab{tab(e.Name, "sh", sh), tab(e.Name, "js", js), tab(e.Name, "py", py)}}
}

const quickstartSh = `# 1. Every Mac, with its configuration IDs
curl ` + BaseURL + `/api/v1/macs

# 2. One Mac, with all its configurations
curl ` + BaseURL + `/api/v1/macs/MacBookPro8,2

# 3. One configuration: verdict, criteria, ports, reports
curl ` + BaseURL + `/api/v1/configs/macbookpro8-2-15-early-2011-a`

const quickstartJS = `const api = "` + BaseURL + `/api/v1";
const get = (path) => fetch(api + path).then((res) => res.json());

const mac = await get("/macs/MacBookPro8,2");
for (const c of mac.configurations) {
  console.log(c.distinction, c.verdict, ` + "`${c.tested} of ${c.applicable} tested`" + `);
}`

const quickstartPy = `import requests

API = "` + BaseURL + `/api/v1"

mac = requests.get(f"{API}/macs/MacBookPro8,2").json()
for c in mac["configurations"]:
    print(c["distinction"], c["verdict"], f"{c['tested']} of {c['applicable']} tested")`

const aiPrompt = `I'm building a tool on the DoesItOmarchy API, which says how well
Omarchy (Arch Linux + Hyprland) runs on each Intel Mac.

Read these first:
` + BaseURL + `/llms.txt
` + BaseURL + `/api/v1/openapi.json

Reads need no key. Use real identifiers such as MacBookPro8,2.
Fetch /api/v1/macs once and cache it; don't request every Mac.

What I want to build: `

// mcpClaudeCodeAdd connects Claude Code (on /api and the cheat sheet).
const mcpClaudeCodeAdd = "claude mcp add --transport http doesitomarchy " + BaseURL + "/mcp"

const mcpCurl = `curl -X POST ` + BaseURL + `/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "MCP-Protocol-Version: 2026-07-28" \
  -H "Mcp-Method: tools/call" \
  -H "Mcp-Name: search_macs" \
  -d '{
    "jsonrpc": "2.0", "id": 1, "method": "tools/call",
    "params": {
      "name": "search_macs",
      "arguments": {"query": "macbook pro 2015", "limit": 3},
      "_meta": {
        "io.modelcontextprotocol/protocolVersion": "2026-07-28",
        "io.modelcontextprotocol/clientCapabilities": {}
      }
    }
  }'`

const submitReply = `{
  "code": "3f9a1c07be",
  "state": "pending",
  "config": "macbookpro8-2-15-early-2011-a",
  "candidates": [],
  "flags": [],
  "status_url": "` + BaseURL + `/api/v1/reports/3f9a1c07be"
}`

const reportReply = `{
  "code": "3f9a1c07be",
  "state": "rejected",
  "config": "macbookpro8-2-15-early-2011-a",
  "identifier": "MacBookPro8,2",
  "tested_at": "2026-10-01T22:30:00Z",
  "submitted_at": "2026-10-01T22:41:07Z",
  "reason": "Not a real test run: every item has the same evidence."
}`

const errorReply = `{
  "error": "invalid report",
  "problems": [
    "tested_at: \"2026-10-01 18:30\" must be a timestamp with a time zone, e.g. 2026-09-30T14:05:00Z or 2026-09-30T10:05:00-04:00",
    "items.wifi: unknown capability (see /api/v1/capabilities, or send it as an extra)"
  ]
}`

// apiDocsData is everything the page shows.
type apiDocsData struct {
	Base, Notice      string
	PerHour           int
	Req               map[string]codeBlock // request samples, by endpoint name
	Res               map[string]codeBlock // response samples, by endpoint name
	Quickstart        codeBlock
	Prompt            codeBlock
	Error             codeBlock
	Example           codeBlock   // a full report
	NoticeCode        codeBlock   // the consent notice, to copy
	MCP               []codeBlock // connecting an assistant, and a call to try
	MCPCallsPerMinute int
	Changelog         []apiChange
}

func (s *Server) apiDocs(w http.ResponseWriter, r *http.Request) {
	d := apiDocsData{Base: BaseURL, Notice: ConsentNotice, PerHour: ReportsPerHour, Req: map[string]codeBlock{}, Res: map[string]codeBlock{}, Changelog: apiChangelog,
		Quickstart: codeBlock{Title: "Quickstart", Tabs: []codeTab{tab("quick", "sh", quickstartSh), tab("quick", "js", quickstartJS), tab("quick", "py", quickstartPy)}},
		Prompt:     codeBlock{Title: "Prompt for your AI assistant", Tabs: []codeTab{tab("prompt", "text", aiPrompt)}},
		Error:      codeBlock{Title: "Response: 400 Bad Request", Tabs: []codeTab{tab("error", "json", errorReply)}},
		Example:    codeBlock{Title: "report.json", Tabs: []codeTab{tab("example", "json", apiExample)}},
		NoticeCode: codeBlock{Title: "Consent notice", Tabs: []codeTab{tab("notice", "text", ConsentNotice)}},
	}
	// How much of each live reply to show: items per array, and how deep
	// objects are shown before they fold to {…}.
	shorten := map[string][2]int{"index": {99, 9}, "macs": {2, 9}, "mac": {1, 1}, "config": {2, 9}, "capabilities": {2, 9}, "match": {2, 9}, "schema": {3, 2}}
	for _, e := range apiEndpoints {
		d.Req[e.Name] = requestSample(e)
		if sh, ok := shorten[e.Name]; ok {
			status, body := s.liveReply(e)
			d.Res[e.Name] = codeBlock{Title: "Response: " + status, Tabs: []codeTab{tab(e.Name+"-res", "json", abbreviate(body, sh[0], sh[1]))}}
		}
	}
	try, _ := s.mcpSearch(mcpSearchIn{Query: "macbook pro 2015", Limit: 3})
	d.MCPCallsPerMinute = MCPCallsPerMinute
	d.MCP = []codeBlock{
		{Title: "Claude Code", Tabs: []codeTab{tab("mcp-claude", "sh", mcpClaudeCodeAdd)}},
		{Title: "VS Code: .vscode/mcp.json", Tabs: []codeTab{tab("mcp-vscode", "json", "{\n  \"servers\": {\n    \"doesitomarchy\": {\n      \"type\": \"http\",\n      \"url\": \""+BaseURL+"/mcp\"\n    }\n  }\n}")}},
		{Title: "Cursor: ~/.cursor/mcp.json", Tabs: []codeTab{tab("mcp-cursor", "json", "{\n  \"mcpServers\": {\n    \"doesitomarchy\": {\n      \"url\": \""+BaseURL+"/mcp\"\n    }\n  }\n}")}},
		{Method: "POST", Title: "/mcp: try a tool", Tabs: []codeTab{tab("mcp-curl", "sh", mcpCurl)}},
		{Title: "The answer's text", Tabs: []codeTab{tab("mcp-answer", "text", try)}},
	}
	d.Res["submit"] = codeBlock{Title: "Response: 201 Created", Tabs: []codeTab{tab("submit-res", "json", submitReply)}}
	d.Res["report"] = codeBlock{Title: "Response: 200 OK", Tabs: []codeTab{tab("report-res", "json", reportReply)}}
	s.render(w, r, http.StatusOK, "api", page{Title: "API Reference", Nav: "api", Styles: []string{"syntax.css", "api.css"}, Scripts: []string{"api.js"},
		Description: "Read DoesItOmarchy's catalog and test results as JSON, or submit diagnostic reports from a test tool. With an OpenAPI document and llms.txt for AI agents.",
		Data:        d})
}

// The API changelog: the catalog changelog's shape, where `backticks`
// mark code.

//go:embed api-changelog.yaml
var apiChangelogYAML []byte

type apiChange struct {
	Date, Title string
	Summary     template.HTML
	Notes       []template.HTML
}

var apiChangelog = mustAPIChangelog()

func mustAPIChangelog() []apiChange {
	var entries []catalog.ChangeEntry
	if err := yaml.Unmarshal(apiChangelogYAML, &entries); err != nil {
		panic("api-changelog.yaml: " + err.Error())
	}
	var out []apiChange
	for _, e := range entries {
		c := apiChange{Date: e.Date, Title: e.Title, Summary: codeMarks(e.Summary)}
		for _, n := range e.Notes {
			c.Notes = append(c.Notes, codeMarks(n))
		}
		out = append(out, c)
	}
	return out
}

// codeMarks escapes s and turns `x` into <code>x</code>.
func codeMarks(s string) template.HTML {
	parts := strings.Split(s, "`")
	for i, p := range parts {
		parts[i] = template.HTMLEscapeString(p)
		if i%2 == 1 {
			parts[i] = "<code>" + parts[i] + "</code>"
		}
	}
	return template.HTML(strings.Join(parts, ""))
}

// liveReply calls e's handler with its example and returns the status line
// and the body.
func (s *Server) liveReply(e apiEndpoint) (string, []byte) {
	path := e.Path
	if e.Example != "" {
		path = e.Example
	}
	req := httptest.NewRequest(e.Method, path, strings.NewReader(e.Body))
	want := strings.Split(e.Path, "/")
	for i, part := range strings.Split(path, "/") {
		if i < len(want) && strings.HasPrefix(want[i], "{") {
			req.SetPathValue(strings.Trim(want[i], "{}"), part)
		}
	}
	rec := httptest.NewRecorder()
	e.handle(s, rec, req)
	return fmt.Sprintf("%d %s", rec.Code, http.StatusText(rec.Code)), rec.Body.Bytes()
}

// jnode is parsed JSON that keeps its key order.
type jnode struct {
	kind   byte   // 'o' object, 'a' array, 's' scalar
	scalar string // a scalar's JSON text
	keys   []string
	vals   []*jnode
}

func parseJSON(dec *json.Decoder) (*jnode, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		n := &jnode{kind: 'o'}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := parseJSON(dec)
			if err != nil {
				return nil, err
			}
			n.keys, n.vals = append(n.keys, k.(string)), append(n.vals, v)
		}
		_, err = dec.Token()
		return n, err
	case json.Delim('['):
		n := &jnode{kind: 'a'}
		for dec.More() {
			v, err := parseJSON(dec)
			if err != nil {
				return nil, err
			}
			n.vals = append(n.vals, v)
		}
		_, err = dec.Token()
		return n, err
	}
	return &jnode{kind: 's', scalar: jsonText(t)}, nil
}

func jsonText(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return strings.TrimSpace(b.String())
}

// abbreviate re-indents JSON, keeping the first keep items of each array
// and folding objects deeper than deep to {…}, and says what it left out.
func abbreviate(raw []byte, keep, deep int) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	n, err := parseJSON(dec)
	if err != nil && err != io.EOF {
		return string(raw)
	}
	var b strings.Builder
	n.write(&b, "", 0, keep, deep)
	return b.String()
}

func (n *jnode) inline(keep, depth, deep int) (string, bool) {
	switch n.kind {
	case 's':
		return n.scalar, true
	case 'o':
		if len(n.vals) == 0 {
			return "{}", true
		}
		return "{…}", depth >= deep
	}
	parts := []string{}
	for i, v := range n.vals {
		if i == keep {
			parts = append(parts, fmt.Sprintf("… %d more", len(n.vals)-keep))
			break
		}
		s, ok := v.inline(keep, depth+1, deep)
		if !ok {
			return "", false
		}
		parts = append(parts, s)
	}
	s := "[" + strings.Join(parts, ", ") + "]"
	return s, len(s) <= 60
}

func (n *jnode) write(b *strings.Builder, ind string, depth, keep, deep int) {
	if s, ok := n.inline(keep, depth, deep); ok {
		b.WriteString(s)
		return
	}
	in := ind + "  "
	if n.kind == 'o' {
		b.WriteString("{\n")
		for i, k := range n.keys {
			b.WriteString(in + jsonText(k) + ": ")
			n.vals[i].write(b, in, depth+1, keep, deep)
			if i < len(n.keys)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(ind + "}")
		return
	}
	b.WriteString("[\n")
	for i, v := range n.vals {
		if i == keep {
			fmt.Fprintf(b, "%s… %d more\n", in, len(n.vals)-keep)
			break
		}
		b.WriteString(in)
		v.write(b, in, depth+1, keep, deep)
		if i < len(n.vals)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(ind + "]")
}

const apiExample = `{
  "schema": "doesitomarchy/report/v1",
  "identifier": "MacBookPro8,2",
  "source": { "version": "1.2.0", "profile": "full" },
  "tester": { "handle": "vintage-fan" },
  "tested_at": "2026-10-01T18:30:00-04:00",
  "omarchy": { "version": "4.0.4" },
  "kernel": "6.16.2-arch1-1",
  "hardware": {
    "product_name": "MacBookPro8,2",
    "board_id": "Mac-94245A3940C91C80",
    "cpu": "Intel(R) Core(TM) i7-2635QM CPU @ 2.00GHz",
    "pci": ["8086:0126", "1002:6760", "14e4:4331"]
  },
  "items": {
    "boot.install":     { "status": "supported", "method": "observed" },
    "network.wifi":     { "status": "partial", "method": "automatic",
                          "evidence": "b43: firmware loaded; 5 GHz networks not listed" },
    "power.sleep-wake": { "status": "failed", "method": "observed",
                          "evidence": "resume hangs on a black screen" },
    "audio.headphone":  { "status": "not_tested", "reason": "no-equipment" }
  },
  "consent_notice": "This sends your test results to DoesItOmarchy.com. …"
}`
