package web

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolText calls a tool through the SDK's own client and returns its text.
func toolText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("%s: %d content items", name, len(res.Content))
	}
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

// A client using the SDK (newest protocol) sees the four read-only tools and
// gets short, linked answers.
func TestMCPTools(t *testing.T) {
	ts, _, _ := liveServer(t)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
		if tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
			t.Errorf("%s: not marked read-only", tl.Name)
		}
	}
	if got := strings.Join(names, " "); got != "get_mac identify_mac search_macs what_needs_testing" {
		t.Errorf("tools: %s", got)
	}
	for _, c := range []struct {
		tool  string
		args  map[string]any
		want  []string
		isErr bool
	}{
		{"search_macs", map[string]any{"query": "macbook pro 2011", "limit": 2}, []string{"[MacBookPro8,1]", "showing the first 2", "/mac/MacBookPro8-1"}, false},
		{"search_macs", map[string]any{"query": ""}, []string{"give a query"}, true},
		{"get_mac", map[string]any{"identifier": "MacBookPro15,2"}, []string{"# MacBook Pro (13-inch, 2019) [MacBookPro15,2]", "## macbookpro15-2-13-2018-4tb3-a", "Verdict:"}, false},
		{"get_mac", map[string]any{"identifier": "macbookpro15-2", "config": "nope"}, []string{"has no configuration", "macbookpro15-2-13-2018-4tb3-a"}, true},
		{"get_mac", map[string]any{"identifier": "MacBookPro99,1"}, []string{"search_macs"}, true},
		{"identify_mac", map[string]any{"command_output": "MacBookPro8,2\nMac-94245A3940C91C80\npci 0x8086:0x0126\npci 0x1002:0x6760"},
			[]string{"[MacBookPro8,2]", "Configuration: macbookpro8-2-15-early-2011-a"}, false},
		{"identify_mac", map[string]any{"product_name": "MacBookPro8,2", "pci": []string{"1002:6741"}}, []string{"Several configurations fit equally", "pass cpu"}, false},
		{"identify_mac", map[string]any{}, []string{"nothing to identify"}, true},
		{"what_needs_testing", map[string]any{"identifier": "MacBookPro15,2"}, []string{"Untested (", "## How to help", "/contribute"}, false},
		{"what_needs_testing", map[string]any{"identifier": "MacBook1,1"}, []string{"can never run Omarchy"}, false},
		// An identifier sold under several names is named by the release the answer is about.
		{"get_mac", map[string]any{"identifier": "iMac10,1"}, []string{"# iMac (27-inch, Late 2009) [iMac10,1]", "Also sold as: iMac (21.5-inch, Late 2009).\n"}, false},
		{"get_mac", map[string]any{"identifier": "iMac10,1", "config": "imac10-1-21-late-2009-a"}, []string{"# iMac (21.5-inch, Late 2009) [iMac10,1]"}, false},
		{"what_needs_testing", map[string]any{"identifier": "iMac10,1", "config": "imac10-1-21-late-2009-b"}, []string{"# What needs testing: iMac (21.5-inch, Late 2009) [iMac10,1]"}, false},
		{"identify_mac", map[string]any{"board_id": "Mac-F2268CC8", "pci": []string{"1002:9488"}},
			[]string{"Identified by board id: iMac (21.5-inch, Late 2009) [iMac10,1].", "Configuration: imac10-1-21-late-2009-b"}, false},
	} {
		text, isErr := toolText(t, cs, c.tool, c.args)
		if isErr != c.isErr {
			t.Errorf("%s %v: isError %v\n%s", c.tool, c.args, isErr, text)
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s %v: missing %q in\n%s", c.tool, c.args, w, text)
			}
		}
	}
	// "Also sold as" only on the whole Mac, and only when it has other names.
	for _, args := range []map[string]any{{"identifier": "iMac10,1", "config": "imac10-1-21-late-2009-a"}, {"identifier": "MacBookAir5,2"}} {
		if text, _ := toolText(t, cs, "get_mac", args); strings.Contains(text, "Also sold as") {
			t.Errorf("get_mac %v: unexpected \"Also sold as\" in\n%s", args, text)
		}
	}
}

func mcpPost(t *testing.T, url, host, version, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if version != "" {
		req.Header.Set("MCP-Protocol-Version", version)
	}
	if host != "" {
		req.Host = host
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, string(b)
}

// The transport details that matter in production.
func TestMCPTransport(t *testing.T) {
	ts := newTestServer(t)
	u := ts.URL + "/mcp"
	// An older client's initialize, arriving the way the Cloudflare tunnel
	// delivers it: from 127.0.0.1 with Host doesitomarchy.com.
	res, body := mcpPost(t, u, "doesitomarchy.com", "",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	if res.StatusCode != 200 || !strings.Contains(body, `"protocolVersion":"2025-06-18"`) || !strings.Contains(body, `"tools":{}`) {
		t.Fatalf("legacy initialize: %d %s", res.StatusCode, body)
	}
	if res.Header.Get("Mcp-Session-Id") != "" || res.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("headers: %v", res.Header)
	}
	// A legacy tools/list needs no session.
	if res, body := mcpPost(t, u, "", "2025-06-18", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); res.StatusCode != 200 || !strings.Contains(body, "what_needs_testing") {
		t.Errorf("legacy tools/list: %d %s", res.StatusCode, body)
	}
	// GET (an old client's notification stream) is refused, as the spec allows.
	if res, _ := http.Get(u); res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", res.StatusCode)
	}
	// A browser's CORS preflight.
	req, _ := http.NewRequest("OPTIONS", u, nil)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 204 || !strings.Contains(res.Header.Get("Access-Control-Allow-Headers"), "Mcp-Protocol-Version") {
		t.Errorf("preflight: %d %v", res.StatusCode, res.Header)
	}
}

func TestMCPRateLimit(t *testing.T) {
	old := MCPCallsPerMinute
	MCPCallsPerMinute = 2
	t.Cleanup(func() { MCPCallsPerMinute = old })
	ts := newTestServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for i := range 3 {
		res, b := mcpPost(t, ts.URL+"/mcp", "", "2025-06-18", body)
		if want := map[bool]int{true: 200, false: 429}[i < 2]; res.StatusCode != want {
			t.Fatalf("call %d: %d, want %d: %s", i+1, res.StatusCode, want, b)
		}
		if i == 2 && (res.Header.Get("Retry-After") == "" || !strings.Contains(b, `"error"`)) {
			t.Errorf("429: %v %s", res.Header, b)
		}
	}
}
