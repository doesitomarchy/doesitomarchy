package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/doesitomarchy/doesitomarchy/internal/match"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
)

// The MCP server at /mcp (PLAN.md §30c): four read-only tools that let an
// AI assistant answer "does Omarchy run on my Mac?" from the catalog, in
// short Markdown written for the model. Stateless, JSON responses; the SDK
// speaks both the 2026-07-28 protocol and the older initialize-based ones.

// MCPCallsPerMinute is how many /mcp requests one IP may make in a minute.
// It's generous: claude.ai and ChatGPT call from shared provider IPs.
var MCPCallsPerMinute = 300

const mcpInstructions = `DoesItOmarchy says how well Omarchy (Arch Linux with Hyprland) runs on every Intel Mac from 2006 to 2020, judged per hardware configuration from reviewed test reports.
- Verdicts belong to configurations, not Macs. "Untested" means no one has reported yet, not that it fails.
- Find a Mac with search_macs, then read it with get_mac. If the user has the Mac at hand, identify_mac pins down its exact configuration from hardware IDs or a command's output.
- Use what_needs_testing when the user wants to help: it lists what's still untested and how to contribute.
- Link the user to the pages in the answers; name Macs as "Name [Identifier]".`

// mcpHandler serves /mcp.
func (s *Server) mcpHandler() http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: "doesitomarchy", Title: "DoesItOmarchy", Version: s.version, WebsiteURL: BaseURL},
		&mcp.ServerOptions{Instructions: mcpInstructions, Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(bool)}
	mcp.AddTool(srv, &mcp.Tool{Name: "search_macs", Title: "Search Intel Macs", Annotations: readOnly,
		Description: "Find Intel Macs by name, year, identifier or hardware, with each one's Omarchy verdicts. " +
			"Plain words work (\"macbook pro 2015\", \"imac 27\"), and so do fields: year:2011, gpu:nvidia, cpu:i7, size:13, " +
			"status:supported, tested:yes, gles:2.0, board:Mac-94245A3940C91C80, a:A1286."},
		mcpTool(s, "search_macs", s.mcpSearch))
	mcp.AddTool(srv, &mcp.Tool{Name: "get_mac", Title: "Get a Mac", Annotations: readOnly,
		Description: "One Mac's configurations and how Omarchy runs on each: the verdict, what failed or only partly works (with the reported evidence and any fix in progress), known limitations, and links. " +
			"Give the model identifier, e.g. MacBookPro8,2; optionally a configuration ID to see just that one."},
		mcpTool(s, "get_mac", s.mcpGetMac))
	mcp.AddTool(srv, &mcp.Tool{Name: "identify_mac", Title: "Identify a Mac from its hardware", Annotations: readOnly, InputSchema: portableSchema[mcpIdentifyIn](),
		Description: "Identify a Mac and its exact configuration from its hardware IDs, then give its Omarchy verdict. " +
			"Best: pass the output of the identify command as command_output. On Linux: " + identifyLinux + " . On macOS: " + identifyMacOS + " . " +
			"Or pass the IDs you know: product_name (e.g. MacBookPro8,2), board_id, cpu, pci (vendor:device IDs from lspci -nn)."},
		mcpTool(s, "identify_mac", s.mcpIdentify))
	mcp.AddTool(srv, &mcp.Tool{Name: "what_needs_testing", Title: "What needs testing", Annotations: readOnly,
		Description: "For a Mac (or one of its configurations): which test criteria are still untested or due a re-test, and how the user can help by testing and submitting a report."},
		mcpTool(s, "what_needs_testing", s.mcpNeedsTesting))
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 64 << 10,
		// The tunnel reaches us from 127.0.0.1 with Host doesitomarchy.com,
		// which the SDK's DNS-rebinding guard would refuse; the data is public.
		DisableLocalhostProtection: true,
	})
	limit := newRateLimiter(MCPCallsPerMinute, time.Minute)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Any origin may call, like the /api/v1 reads.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "POST")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Mcp-Protocol-Version, Mcp-Method, Mcp-Name, Mcp-Session-Id, Last-Event-ID")
			w.Header().Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !limit.allow(clientIP(r), time.Now()) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"jsonrpc":"2.0","error":{"code":-31029,"message":"over %d requests a minute from this address; try again in a minute"}}`+"\n", MCPCallsPerMinute)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// portableSchema infers T's input schema, typing lists as plain arrays: the
// inferred ["null", "array"] is valid JSON Schema, but some clients (Gemini's
// function declarations) reject a list of types.
func portableSchema[T any]() *jsonschema.Schema {
	sch, err := jsonschema.For[T](nil)
	if err != nil {
		panic(err)
	}
	for _, p := range sch.Properties {
		if len(p.Types) == 2 && p.Types[0] == "null" {
			p.Type, p.Types = p.Types[1], nil
		}
	}
	return sch
}

// mcpTool logs each call by tool name only (never its arguments) and wraps
// the text answer. An error becomes a tool error the model can act on.
func mcpTool[In any](s *Server, name string, f func(In) (string, error)) mcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, any, error) {
		s.log.Info("mcp tool", "tool", name)
		text, err := f(in)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
	}
}

// ── search_macs ─────────────────────────────────────────────────────────

type mcpSearchIn struct {
	Query string `json:"query" jsonschema:"what to look for: words (\"mbp 2015\") or fields (year:2011 gpu:nvidia)"`
	Limit int    `json:"limit,omitempty" jsonschema:"how many Macs to list, 1 to 25 (default 10)"`
}

func (s *Server) mcpSearch(in mcpSearchIn) (string, error) {
	if strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("give a query, e.g. \"macbook pro 2015\" or year:2011 gpu:nvidia")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	limit = min(limit, 25)
	d := s.runSearch(in.Query, "")
	var b strings.Builder
	for _, e := range d.Resp.Errors {
		fmt.Fprintf(&b, "Note: %s\n", e)
	}
	if len(d.Results) == 0 {
		b.WriteString("No Macs match " + strconv.Quote(in.Query) + ".")
		if d.Resp.DidYouMean != "" {
			b.WriteString(" Did you mean " + strconv.Quote(d.Resp.DidYouMean) + "?")
		}
		return b.String(), nil
	}
	fmt.Fprintf(&b, "%d %s %q", len(d.Results), plural(len(d.Results), "Mac matches", "Macs match"), in.Query)
	if len(d.Results) > limit {
		fmt.Fprintf(&b, " (showing the first %d; narrow it with fields such as year: or size:)", limit)
	}
	b.WriteString(":\n\n")
	for _, rv := range d.Results[:min(limit, len(d.Results))] {
		m := rv.Mac
		if m == nil {
			continue
		}
		fmt.Fprintf(&b, "- %s: %d %s (%s). %s\n", macName(m), len(m.Configs), plural(len(m.Configs), "configuration", "configurations"), verdictList(m), macURL(m))
		if !rv.AllConfigs {
			for _, cv := range rv.Scoped {
				if cv != nil {
					fmt.Fprintf(&b, "  - matching configuration %s (%s): %s\n", cv.ID, cv.Diff, cv.Status.Verdict.Label())
				}
			}
		}
	}
	b.WriteString("\nUse get_mac with an identifier for the details.")
	return b.String(), nil
}

// ── get_mac ─────────────────────────────────────────────────────────────

type mcpMacIn struct {
	Identifier string `json:"identifier" jsonschema:"the model identifier, e.g. MacBookPro8,2"`
	Config     string `json:"config,omitempty" jsonschema:"a configuration ID, to show just that one"`
}

// mcpMac finds the Mac (and the configurations to show) for an input.
func (s *Server) mcpMac(in mcpMacIn) (*macView, []*configView, error) {
	m := s.data().view.bySlug[strings.ToLower(strings.ReplaceAll(strings.TrimSpace(in.Identifier), ",", "-"))]
	if m == nil {
		if cv := s.data().view.configs[strings.TrimSpace(in.Identifier)]; cv != nil { // a config ID given as the identifier
			return cv.Mac, []*configView{cv}, nil
		}
		return nil, nil, fmt.Errorf("no Mac with identifier %q; find it with search_macs (identifiers look like MacBookPro8,2)", in.Identifier)
	}
	if in.Config == "" {
		return m, m.Configs, nil
	}
	for _, cv := range m.Configs {
		if cv.ID == in.Config {
			return m, []*configView{cv}, nil
		}
	}
	ids := []string{}
	for _, cv := range m.Configs {
		ids = append(ids, cv.ID)
	}
	return nil, nil, fmt.Errorf("%s has no configuration %q; its configurations are %s", m.Identifier, in.Config, strings.Join(ids, ", "))
}

func (s *Server) mcpGetMac(in mcpMacIn) (string, error) {
	m, cvs, err := s.mcpMac(in)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n%s. %d-bit EFI", macName(m), m.Years, m.EFI)
	if m.Chip != "" {
		b.WriteString(", " + m.Chip)
	}
	fmt.Fprintf(&b, ". %s\n", macURL(m))
	if m.HardBlocker != "" {
		fmt.Fprintf(&b, "\nIt can never run Omarchy: %s\n", m.HardBlocker)
	}
	for _, cv := range cvs {
		st := cv.Status
		fmt.Fprintf(&b, "\n## %s (%s)\n%s: %s\n", cv.ID, cv.Diff, cv.ReleaseName, cv.Label)
		fmt.Fprintf(&b, "Verdict: %s", st.Verdict.Label())
		if st.Applicable > 0 {
			fmt.Fprintf(&b, ", %d of %d criteria tested", st.Tested, st.Applicable)
		}
		if cv.Verified {
			b.WriteString(". Every applicable criterion passed")
		}
		b.WriteString(".\n")
		if cv.OutOfScope != "" {
			fmt.Fprintf(&b, "Left out of coverage counts: %s.\n", cv.OutOfScope)
		}
		for _, l := range cv.Limitations {
			fmt.Fprintf(&b, "Known limitation: %s\n", l)
		}
		for _, cat := range cv.Categories {
			for _, cp := range cat.Caps {
				switch cp.Verdict {
				case status.Failed, status.Partial, status.Unsupported:
					fmt.Fprintf(&b, "- %s: %s (%s)", cp.Verdict.Label(), cp.Name, cp.ID)
					if cp.Error != "" {
						fmt.Fprintf(&b, ". Reported: %q", oneLine(cp.Error, 200))
					}
					if cp.Reason != "" {
						fmt.Fprintf(&b, ". Why: %s", cp.Reason)
					}
					if f := cp.Fix; f != nil {
						fmt.Fprintf(&b, ". Fix: %s (%s)", f.URL, f.State)
					}
					b.WriteString("\n")
				}
			}
		}
		if n := st.Applicable - st.Tested; n > 0 && st.Applicable > 0 {
			fmt.Fprintf(&b, "%d %s untested (what_needs_testing lists them).\n", n, plural(n, "criterion is", "criteria are"))
		}
		fmt.Fprintf(&b, "%s#cfg-%s\n", macURL(m), cv.ID)
	}
	return b.String(), nil
}

// ── identify_mac ────────────────────────────────────────────────────────

type mcpIdentifyIn struct {
	CommandOutput string   `json:"command_output,omitempty" jsonschema:"the output of the identify command (see the tool description), pasted as is"`
	ProductName   string   `json:"product_name,omitempty" jsonschema:"the model identifier, e.g. MacBookPro8,2"`
	BoardID       string   `json:"board_id,omitempty" jsonschema:"e.g. Mac-94245A3940C91C80"`
	CPU           string   `json:"cpu,omitempty" jsonschema:"the processor's name, e.g. Intel(R) Core(TM) i7-2635QM CPU @ 2.00GHz"`
	PCI           []string `json:"pci,omitempty" jsonschema:"PCI vendor:device IDs, e.g. 1002:6760"`
}

func (s *Server) mcpIdentify(in mcpIdentifyIn) (string, error) {
	p := match.ParseProbe(in.CommandOutput)
	if in.ProductName != "" {
		p.ProductName = in.ProductName
	}
	if in.BoardID != "" {
		p.BoardID = in.BoardID
	}
	if in.CPU != "" {
		p.CPU = in.CPU
	}
	p.PCI = append(p.PCI, in.PCI...)
	if p.ProductName == "" && p.BoardID == "" && len(p.PCI) == 0 {
		return "", fmt.Errorf("nothing to identify: pass the identify command's output as command_output, or product_name, board_id or pci IDs")
	}
	res := s.match.Match(p)
	var b strings.Builder
	if len(res.Candidates) == 0 {
		b.WriteString("No Mac in the catalog fits this hardware.")
		if p.ProductName != "" {
			fmt.Fprintf(&b, " %q isn't an Intel Mac identifier we know.", p.ProductName)
		}
		b.WriteString(" If it is an Intel Mac, please open an issue with this output: https://github.com/doesitomarchy/doesitomarchy/issues")
		return b.String(), nil
	}
	m := s.data().view.bySlug[strings.ToLower(strings.ReplaceAll(res.Identifier, ",", "-"))]
	if m != nil {
		fmt.Fprintf(&b, "This is a %s (identified by %s).\n", macName(m), strings.ReplaceAll(res.By, "_", " "))
	}
	top := res.Candidates[0].Score
	var best []*configView
	for _, c := range res.Candidates {
		if c.Score == top {
			best = append(best, s.data().view.configs[c.Config])
		}
	}
	if res.Exact && len(best) == 1 && best[0] != nil {
		cv := best[0]
		fmt.Fprintf(&b, "Configuration: %s (%s, %s).\nVerdict: %s", cv.ID, cv.Diff, cv.ReleaseName, cv.Status.Verdict.Label())
		if cv.Status.Applicable > 0 {
			fmt.Fprintf(&b, ", %d of %d criteria tested", cv.Status.Tested, cv.Status.Applicable)
		}
		fmt.Fprintf(&b, ". %s#cfg-%s\n", macURL(cv.Mac), cv.ID)
	} else {
		b.WriteString("Several configurations fit equally; the hardware given can't tell them apart:\n")
		for _, cv := range best {
			if cv != nil {
				fmt.Fprintf(&b, "- %s (%s, %s): %s. %s#cfg-%s\n", cv.ID, cv.Diff, cv.ReleaseName, cv.Status.Verdict.Label(), macURL(cv.Mac), cv.ID)
			}
		}
		switch {
		case len(p.PCI) == 0:
			b.WriteString("To tell them apart, pass the PCI IDs (lspci -nn) and the CPU name, or the identify command's output.\n")
		case p.CPU == "":
			b.WriteString("The CPU name often tells them apart: pass cpu.\n")
		}
	}
	b.WriteString("Use get_mac for what works and what doesn't.")
	return b.String(), nil
}

// ── what_needs_testing ──────────────────────────────────────────────────

func (s *Server) mcpNeedsTesting(in mcpMacIn) (string, error) {
	m, cvs, err := s.mcpMac(in)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# What needs testing: %s\n", macName(m))
	if m.HardBlocker != "" {
		fmt.Fprintf(&b, "Nothing: it can never run Omarchy (%s).\n", m.HardBlocker)
		return b.String(), nil
	}
	for _, cv := range cvs {
		var untested, retest []string
		for _, cat := range cv.Categories {
			for _, cp := range cat.Caps {
				switch {
				case cp.Verdict == status.Untested:
					untested = append(untested, fmt.Sprintf("%s (%s)", cp.Name, cp.ID))
				case cp.Fix != nil && cp.Fix.Retest:
					retest = append(retest, fmt.Sprintf("%s (%s): a fix landed, %s", cp.Name, cp.ID, cp.Fix.URL))
				case cp.Stale:
					retest = append(retest, fmt.Sprintf("%s (%s): last tested on an older Omarchy", cp.Name, cp.ID))
				}
			}
		}
		fmt.Fprintf(&b, "\n## %s (%s)\n", cv.ID, cv.Diff)
		if len(untested) == 0 && len(retest) == 0 {
			b.WriteString("Everything that applies has a current result.\n")
		}
		if len(untested) > 0 {
			fmt.Fprintf(&b, "Untested (%d of %d): %s\n", len(untested), cv.Status.Applicable, strings.Join(untested, "; "))
		}
		if len(retest) > 0 {
			fmt.Fprintf(&b, "Due a re-test: %s\n", strings.Join(retest, "; "))
		}
	}
	b.WriteString(`
## How to help
- Testing: run Omarchy on this Mac and check each criterion. Reports arrive through doioma, the DoesItOmarchy test tool, which is being built now: ` + BaseURL + `/contribute
- Until then: check the Mac's page matches the hardware (` + macURL(m) + `). If it doesn't, open an issue with the output of lspci -nn and lsusb: https://github.com/doesitomarchy/doesitomarchy/issues
- Fixing: failed criteria have fix issues at ` + BaseURL + `/fixes
- Building a test tool: ` + BaseURL + `/api#sources`)
	return b.String(), nil
}

// ── helpers ─────────────────────────────────────────────────────────────

// macName is how the site names a Mac in prose: "Name [Identifier]".
func macName(m *macView) string { return m.ListTitle + " [" + m.Identifier + "]" }

func macURL(m *macView) string { return BaseURL + "/mac/" + url.PathEscape(m.Slug) }

// verdictList counts a Mac's configurations by verdict: "1 Supported, 2 Untested".
func verdictList(m *macView) string {
	n := map[status.Verdict]int{}
	for _, cv := range m.Configs {
		n[cv.Status.Verdict]++
	}
	var out []string
	for _, v := range m.Verdicts { // distinct, in rank order
		out = append(out, fmt.Sprintf("%d %s", n[v], v.Label()))
	}
	return strings.Join(out, ", ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// oneLine flattens evidence to one line of at most n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
