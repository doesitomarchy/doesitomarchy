package web

import (
	"bytes"
	_ "embed"
	"net/http"
	"reflect"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/search"
)

// The MCP cheat sheet (PLAN §30e): one printable page at /api/mcp-cheatsheet,
// filled from the code it describes, and its PDF, printed by
// `make cheatsheet`. TestCheatsheetPDF fails when the page changes without a
// new PDF, so the download can't drift from the API.

//go:embed mcp-cheatsheet.pdf
var cheatsheetPDF []byte

type csTool struct {
	Name, Does string
	Args       []csArg
	AnyOf      bool // every argument is optional: give any of them
}

type csArg struct {
	Name     string
	Optional bool
}

// csToolFor lists a tool's arguments from its input struct's JSON tags.
func csToolFor[In any](name, does string) csTool {
	t := csTool{Name: name, Does: does, AnyOf: true}
	rt := reflect.TypeFor[In]()
	for i := range rt.NumField() {
		f := rt.Field(i)
		tag, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		a := csArg{Name: tag, Optional: strings.Contains(opts, "omitempty")}
		if f.Type.Kind() == reflect.Slice {
			a.Name += "[]"
		}
		t.AnyOf = t.AnyOf && a.Optional
		t.Args = append(t.Args, a)
	}
	return t
}

// csField is one row of search fields: Names are canonical field names
// (TestCheatsheetFields checks every one in search.FieldNames is on a row);
// without examples, Values defaults to the search package's help text.
type csField struct {
	Label    string // shown instead of Names: syntax rows, and long lists (Names then go in the text)
	Names    []string
	Values   string
	Examples []string // whole queries; TestCheatsheetQueries runs each
	Note     string
}

// csAsk is an example question and what the assistant runs for it: a tool,
// or a search_macs query (Query), which TestCheatsheetQueries runs.
type csAsk struct {
	Ask, Runs string
	Query     bool
}

type csAskGroup struct {
	Title string
	Asks  []csAsk
}

type cheatsheetData struct {
	Base, Canonical  string
	CallsPerMinute   int
	Tools            []csTool
	Fields           []csField
	Linux, MacOS     string
	Asks             []csAskGroup
	ClaudeCode, Curl string
	ExampleConfig    string
	APIDate          string
}

var cheatsheetTools = []csTool{
	csToolFor[mcpSearchIn]("search_macs", "Find Macs by words or fields, with their verdicts."),
	csToolFor[mcpMacIn]("get_mac", "One Mac's verdict per configuration, with failures and their evidence."),
	csToolFor[mcpIdentifyIn]("identify_mac", "The Mac at hand and its exact configuration."),
	csToolFor[mcpMacIn]("what_needs_testing", "What's untested or due a re-test on a Mac, and how to help."),
}

var cheatsheetFields = []csField{
	{Names: []string{"year"}, Examples: []string{"year:2011", "year:>=2012", "year:2009..2012"}},
	{Names: []string{"status"}, Note: "blocked = not-compatible"},
	{Names: []string{"tested"}, Note: "yes = any accepted result"},
	{Names: []string{"scope"}, Note: "out = before 2009, and Xserve"},
	{Names: []string{"gles"}, Note: "2.0 = below Hyprland's floor"},
	{Names: []string{"gpu", "cpu"}, Values: "words in the name", Examples: []string{"gpu:nvidia", "gpu:6770m", "cpu:i7"}},
	{Names: []string{"size", "cores"}, Examples: []string{"size:13", "size:21.5", "cores:4"}},
	{Names: []string{"form"}},
	{Names: []string{"release"}},
	{Names: []string{"chip", "efi"}},
	{Names: []string{"board"}, Examples: []string{"board:Mac-F2268CC8"}},
	{Names: []string{"a", "order", "emc"}, Examples: []string{"a:A1286", "order:MC374LL/A", "emc:2353"}},
	{Names: []string{"hw"}, Values: "a PCI or USB ID", Examples: []string{"hw:10de:0647"}},
	{Names: []string{"id", "line"}, Examples: []string{"id:MacBookPro8", "line:imac"}, Note: "no comma: the family"},
	{Names: []string{"port", "feature"}, Values: "prefix match", Examples: []string{"port:usb-c", "feature:touch-bar"}},
	{Label: "parts", Names: []string{"display", "arch", "wifi", "bt", "audio", "camera", "storage", "ethernet", "thunderbolt", "firewire", "reader", "input", "bridge"},
		Values: "words in the part's name", Examples: []string{"wifi:bcm4360"}},
	{Label: "a|b", Values: "either value", Examples: []string{"status:supported|partial"}},
	{Label: "-", Values: "leave out", Examples: []string{"-gpu:nvidia", "-xserve"}},
}

var cheatsheetAsks = []csAskGroup{
	{"Look things up", []csAsk{
		{"Does Omarchy run on a MacBookAir7,2?", "get_mac", false},
		{"Which Macs are below the GLES 3.0 floor?", "gles:2.0", true},
		{"What did A1286 ship as?", "a:A1286", true},
		{"Which Macs share board Mac-F2268CC8?", "board:Mac-F2268CC8", true},
	}},
	{"Coverage", []csAsk{
		{"Which Macs are still untested?", "tested:no", true},
		{"What's partial or failed from 2015 to 2020?", "status:partial|failed year:2015..2020", true},
		{"Which 2006 Macs can't run Omarchy, and why?", "status:not-compatible year:2006", true},
	}},
	{"The Mac in front of you", []csAsk{
		{"What Mac is this? (paste the output)", "identify_mac", false},
		{"I have a MacBookPro11,3. How can I help?", "what_needs_testing", false},
	}},
}

// cheatsheetCurl lists the tools; TestCheatsheetCurl sends its body.
const cheatsheetCurlBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

const cheatsheetCurl = `curl -s ` + BaseURL + `/mcp \
 -H 'Content-Type: application/json' \
 -H 'Accept: application/json, text/event-stream' \
 -d '` + cheatsheetCurlBody + `'`

// cheatsheetExampleConfig is a configuration ID shown as an example; a test
// checks it's in the catalog.
const cheatsheetExampleConfig = "imac10-1-21-late-2009-b"

func cheatsheetFieldRows() []csField {
	rows := make([]csField, len(cheatsheetFields))
	for i, f := range cheatsheetFields {
		if f.Values == "" && len(f.Examples) == 0 {
			var help []string
			for _, n := range f.Names {
				if h := search.FieldHelp(n); h != "" {
					help = append(help, h)
				}
			}
			f.Values = strings.Join(help, " · ")
		}
		rows[i] = f
	}
	return rows
}

func (s *Server) cheatsheet(w http.ResponseWriter, r *http.Request) {
	d := cheatsheetData{Base: BaseURL, Canonical: BaseURL + "/api/mcp-cheatsheet", CallsPerMinute: MCPCallsPerMinute,
		Tools: cheatsheetTools, Fields: cheatsheetFieldRows(), Linux: identifyLinux, MacOS: identifyMacOS, Asks: cheatsheetAsks,
		ClaudeCode: mcpClaudeCodeAdd, Curl: cheatsheetCurl, ExampleConfig: cheatsheetExampleConfig, APIDate: apiChangelog[0].Date}
	var buf bytes.Buffer
	if err := s.pages["mcp-cheatsheet"].Execute(&buf, d); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeHTML(w, r, http.StatusOK, buf.Bytes())
}

func (s *Server) cheatsheetPDF(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="doesitomarchy-mcp-cheatsheet.pdf"`)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if r.Method != http.MethodHead {
		w.Write(cheatsheetPDF)
	}
}
