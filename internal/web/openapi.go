package web

import (
	"bytes"
	_ "embed"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"text/template"

	"github.com/doesitomarchy/doesitomarchy/internal/status"
)

// The OpenAPI document and llms.txt, for AI agents and code generators.
// Both are built from apiEndpoints, and the schemas from the response
// types themselves (with their `doc` tags), so they can't drift from the
// code.

var openAPIDoc = sync.OnceValue(func() map[string]any {
	g := schemaGen{defs: map[string]any{}}
	g.of(reflect.TypeOf(apiError{})) // defines Error
	errResp := map[string]any{"description": "An error: `error` says what went wrong; `problems` lists every problem in a report.",
		"content": map[string]any{"application/json": map[string]any{"schema": ref("Error")}}}
	paths := map[string]any{}
	for _, e := range apiEndpoints {
		op := map[string]any{"operationId": e.Name, "summary": e.Summary,
			"externalDocs": map[string]any{"url": BaseURL + "/api#" + docAnchors[e.Name]}}
		var params []any
		for _, p := range pathParams(e.Path) {
			params = append(params, map[string]any{"name": p, "in": "path", "required": true,
				"description": paramDocs[p], "schema": map[string]any{"type": "string"}})
		}
		if params != nil {
			op["parameters"] = params
		}
		code, ok := "200", map[string]any{"description": "OK"}
		switch {
		case e.Name == "schema":
			ok["content"] = map[string]any{"application/schema+json": map[string]any{"schema": map[string]any{"type": "object"}}}
		case e.Name == "openapi":
			ok["content"] = map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}
		case e.res != nil:
			ok["content"] = map[string]any{"application/json": map[string]any{"schema": g.of(reflect.TypeOf(e.res))}}
		}
		if e.Key {
			code, ok["description"] = "201", "Created: the report is pending review"
			op["security"] = []any{map[string]any{"sourceKey": []any{}}}
			op["requestBody"] = map[string]any{"required": true, "description": "One report in the doesitomarchy/report/v1 format, as JSON or YAML.",
				"content": map[string]any{
					"application/json": map[string]any{"schema": map[string]any{"$ref": BaseURL + "/api/v1/schema"}},
					"application/yaml": map[string]any{"schema": map[string]any{"$ref": BaseURL + "/api/v1/schema"}}}}
		} else if e.req != nil {
			op["requestBody"] = map[string]any{"required": true,
				"content": map[string]any{"application/json": map[string]any{"schema": g.of(reflect.TypeOf(e.req))}}}
		}
		op["responses"] = map[string]any{code: ok, "default": errResp}
		path := strings.TrimPrefix(e.Path, "/api/v1")
		if path == "" {
			path = "/"
		}
		if paths[path] == nil {
			paths[path] = map[string]any{}
		}
		paths[path].(map[string]any)[strings.ToLower(e.Method)] = op
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{"title": "DoesItOmarchy API", "version": "1",
			"description": "How well Omarchy runs on every Intel Mac (2006–2020), per configuration. Reads are public JSON with CORS and need no key; " +
				"submitting diagnostic reports needs a source key. Catalog data is CC BY-SA 4.0. AI assistants can use the MCP server at " + BaseURL + "/mcp instead. Docs: " + BaseURL + "/api",
			"license": map[string]any{"name": "CC BY-SA 4.0 (data)", "url": "https://creativecommons.org/licenses/by-sa/4.0/"}},
		"externalDocs": map[string]any{"url": BaseURL + "/api"},
		"servers":      []any{map[string]any{"url": BaseURL + "/api/v1"}},
		"paths":        paths,
		"components": map[string]any{"schemas": g.defs,
			"securitySchemes": map[string]any{"sourceKey": map[string]any{"type": "http", "scheme": "bearer",
				"description": "A source key (doi_ plus 32 characters), issued to registered test tools."}}},
	}
})

// docAnchors are each endpoint's section on /api.
var docAnchors = map[string]string{"index": "intro", "macs": "macs", "mac": "mac", "config": "config", "capabilities": "capabilities",
	"match": "match", "submit": "submit", "report": "report", "schema": "format", "openapi": "ai"}

var paramDocs = map[string]string{
	"identifier": "A model identifier, e.g. MacBookPro8,2 (MacBookPro8-2 works too)",
	"id":         "A configuration ID, from a Mac's configs",
	"code":       "A report's code, from the reply to a submission",
}

func pathParams(path string) []string {
	var out []string
	for _, part := range strings.Split(path, "/") {
		if strings.HasPrefix(part, "{") {
			out = append(out, strings.Trim(part, "{}"))
		}
	}
	return out
}

func ref(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }

// schemaGen turns Go types into JSON Schema, named structs into shared
// components.
type schemaGen struct{ defs map[string]any }

var verdictType = reflect.TypeOf(status.Verdict(""))

// schemaNames renames types whose Go names read badly in the document.
var schemaNames = map[string]string{"connInfo": "Connector", "apiCapStatus": "CriterionStatus", "apiCapability": "Criterion",
	"apiCapabilityList": "CriterionList", "apiConfig": "Configuration", "apiMacDetail": "Mac", "apiIndexDoc": "Index"}

func (g *schemaGen) of(t reflect.Type) map[string]any {
	switch {
	case t == verdictType:
		if g.defs["Verdict"] == nil {
			g.defs["Verdict"] = map[string]any{"type": "string",
				"enum": []string{"supported", "partial", "failed", "unsupported", "untested", "not-compatible"}}
		}
		return ref("Verdict")
	case t.Kind() == reflect.Pointer:
		return g.of(t.Elem())
	case t.Kind() == reflect.String:
		return map[string]any{"type": "string"}
	case t.Kind() == reflect.Bool:
		return map[string]any{"type": "boolean"}
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Uint64:
		return map[string]any{"type": "integer"}
	case t.Kind() == reflect.Slice:
		return map[string]any{"type": "array", "items": g.of(t.Elem())}
	case t.Kind() == reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.of(t.Elem())}
	case t.Kind() == reflect.Struct:
		name := schemaNames[t.Name()]
		if name == "" {
			name = strings.TrimPrefix(t.Name(), "api")
			name = strings.ToUpper(name[:1]) + name[1:]
		}
		if _, done := g.defs[name]; !done {
			g.defs[name] = nil // reserve the name before recursing
			obj := map[string]any{"type": "object", "properties": map[string]any{}}
			var required []string
			g.fields(t, obj["properties"].(map[string]any), &required)
			if required != nil {
				obj["required"] = required
			}
			g.defs[name] = obj
		}
		return ref(name)
	}
	return map[string]any{}
}

func (g *schemaGen) fields(t reflect.Type, props map[string]any, required *[]string) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		switch {
		case f.Anonymous && name == "":
			g.fields(f.Type, props, required) // embedded: its fields are inlined
			continue
		case !f.IsExported() || name == "-":
			continue
		case name == "":
			name = f.Name
		}
		sch := g.of(f.Type)
		omit := strings.Contains(opts, "omitempty")
		if f.Type.Kind() == reflect.Pointer && !omit {
			sch = map[string]any{"oneOf": []any{sch, map[string]any{"type": "null"}}}
		}
		if d := f.Tag.Get("doc"); d != "" {
			if _, isRef := sch["$ref"]; isRef {
				sch = map[string]any{"allOf": []any{sch}}
			}
			sch["description"] = d
		}
		props[name] = sch
		if !omit {
			*required = append(*required, name)
		}
	}
}

func (s *Server) apiOpenAPI(w http.ResponseWriter, r *http.Request) {
	readable(w)
	writeJSON(w, http.StatusOK, openAPIDoc())
}

//go:embed templates/llms.txt
var llmsSource string

var llmsTmpl = template.Must(template.New("llms").Parse(llmsSource))

// llmsTxt is the site and its API in plain Markdown, for language models
// (llmstxt.org).
func (s *Server) llmsTxt(w http.ResponseWriter, r *http.Request) {
	var b bytes.Buffer
	if err := llmsTmpl.Execute(&b, map[string]any{"Base": BaseURL, "Endpoints": apiEndpoints, "Notice": ConsentNotice, "PerHour": ReportsPerHour}); err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(b.Bytes())
}
