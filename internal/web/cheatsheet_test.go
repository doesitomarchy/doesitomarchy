package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/doesitomarchy/doesitomarchy/internal/search"
	"github.com/doesitomarchy/doesitomarchy/internal/testdb"
)

func cheatsheetServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	st, c := testdb.Open(t)
	srv, err := New(st, c, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

// The committed PDF was printed from the page as it renders now, and is one
// page; /api links it, and it's served as is.
func TestCheatsheetPDF(t *testing.T) {
	_, ts := cheatsheetServer(t)
	code, page := body(t, ts, "/api/mcp-cheatsheet")
	if code != http.StatusOK {
		t.Fatalf("/api/mcp-cheatsheet: %d", code)
	}
	sum := sha256.Sum256([]byte(page))
	want, err := os.ReadFile("mcp-cheatsheet.sum")
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(sum[:]); got != strings.TrimSpace(string(want)) {
		t.Errorf("the MCP cheat sheet changed, but its PDF wasn't printed again: run make cheatsheet (PLAN §30e)")
	}
	if n := len(regexp.MustCompile(`/Type\s*/Page[^s]`).FindAll(cheatsheetPDF, -1)); n != 1 {
		t.Errorf("mcp-cheatsheet.pdf has %d pages, want 1", n)
	}
	if _, api := body(t, ts, "/api"); !strings.Contains(api, `href="/api/mcp-cheatsheet.pdf"`) {
		t.Error("/api doesn't link the cheat sheet")
	}
	res, err := http.Get(ts.URL + "/api/mcp-cheatsheet.pdf")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/pdf" || !bytes.Equal(b, cheatsheetPDF) {
		t.Errorf("/api/mcp-cheatsheet.pdf: %d %q, %d bytes", res.StatusCode, res.Header.Get("Content-Type"), len(b))
	}
}

// The sheet lists exactly the server's tools, with their arguments.
func TestCheatsheetTools(t *testing.T) {
	_, ts := cheatsheetServer(t)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	sheet := map[string][]string{}
	for _, tl := range cheatsheetTools {
		for _, a := range tl.Args {
			sheet[tl.Name] = append(sheet[tl.Name], strings.TrimSuffix(a.Name, "[]"))
		}
	}
	for _, tl := range list.Tools {
		args, ok := sheet[tl.Name]
		if !ok {
			t.Errorf("tool %s isn't on the cheat sheet (cheatsheetTools)", tl.Name)
			continue
		}
		delete(sheet, tl.Name)
		schema, _ := tl.InputSchema.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		var want []string
		for p := range props {
			want = append(want, p)
		}
		slices.Sort(want)
		slices.Sort(args)
		if !slices.Equal(args, want) {
			t.Errorf("%s: the sheet lists %v, the tool takes %v", tl.Name, args, want)
		}
	}
	for name := range sheet {
		t.Errorf("the cheat sheet lists %s, which the server doesn't have", name)
	}
}

// Every search field is on a row, and every row names real fields.
func TestCheatsheetFields(t *testing.T) {
	on := map[string]bool{}
	for _, f := range cheatsheetFields {
		for _, n := range f.Names {
			if !slices.Contains(search.FieldNames, n) {
				t.Errorf("the cheat sheet lists %s:, which isn't a search field", n)
			}
			on[n] = true
		}
	}
	for _, n := range search.FieldNames {
		if !on[n] {
			t.Errorf("search field %s: isn't on the cheat sheet (cheatsheetFields)", n)
		}
	}
}

// Every query on the sheet is valid, and finds Macs unless it depends on
// test results (the test database has none).
func TestCheatsheetQueries(t *testing.T) {
	srv, _ := cheatsheetServer(t)
	var queries []string
	for _, f := range cheatsheetFields {
		queries = append(queries, f.Examples...)
	}
	for _, g := range cheatsheetAsks {
		for _, a := range g.Asks {
			if a.Query {
				queries = append(queries, a.Runs)
			}
		}
	}
	for _, q := range queries {
		r := srv.data().index.Search(q)
		if len(r.Errors) > 0 {
			t.Errorf("%q: %v", q, r.Errors)
		}
		if len(r.Results) == 0 && !strings.Contains(q, "status:") && !strings.Contains(q, "tested:") {
			t.Errorf("%q finds no Macs", q)
		}
	}
	if srv.cfgs[cheatsheetExampleConfig] == nil {
		t.Errorf("example configuration %s isn't in the catalog", cheatsheetExampleConfig)
	}
}

// The sheet's curl lists the tools, with the headers it shows.
func TestCheatsheetCurl(t *testing.T) {
	_, ts := cheatsheetServer(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(cheatsheetCurlBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(b), `"what_needs_testing"`) {
		t.Errorf("tools/list: %d %s", res.StatusCode, b)
	}
	if !strings.Contains(cheatsheetCurl, cheatsheetCurlBody) {
		t.Error("the curl doesn't send cheatsheetCurlBody")
	}
}
