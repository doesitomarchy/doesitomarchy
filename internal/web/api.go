package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/match"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// The open API (PLAN.md §22.3). Reads are public JSON that any site may
// fetch; submissions need a source key.

// ReportsPerHour is how many reports one source may submit in an hour
// (a variable so tests can lower it).
var ReportsPerHour = 60

func (s *Server) apiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1", s.apiIndex)
	mux.HandleFunc("GET /api/v1/{$}", s.apiIndex)
	mux.HandleFunc("GET /api/v1/capabilities", s.apiCapabilities)
	mux.HandleFunc("GET /api/v1/macs", s.apiMacs)
	mux.HandleFunc("GET /api/v1/macs/{identifier}", s.apiMac)
	mux.HandleFunc("GET /api/v1/configs/{id}", s.apiConfig)
	mux.HandleFunc("GET /api/v1/schema", s.apiSchema)
	mux.HandleFunc("POST /api/v1/match", s.apiMatch)
	mux.HandleFunc("POST /api/v1/reports", s.apiSubmit)
	mux.HandleFunc("GET /api/v1/reports/{code}", s.apiReport)
	mux.HandleFunc("/api/", apiNotFound)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	if h.Get("Cache-Control") == "" {
		h.Set("Cache-Control", "no-store")
	}
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

// readable marks a public read: any origin may fetch it, briefly cached.
func readable(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=60")
}

type apiError struct {
	Error    string   `json:"error"`
	Problems []string `json:"problems,omitempty"`
}

func apiFail(w http.ResponseWriter, code int, msg string, problems ...string) {
	writeJSON(w, code, apiError{Error: msg, Problems: problems})
}

func (s *Server) apiIndex(w http.ResponseWriter, r *http.Request) {
	readable(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"name":   "DoesItOmarchy API",
		"docs":   BaseURL + "/api",
		"schema": results.SchemaV1,
		"endpoints": map[string]string{
			"capabilities": "/api/v1/capabilities",
			"macs":         "/api/v1/macs",
			"mac":          "/api/v1/macs/{identifier}",
			"config":       "/api/v1/configs/{id}",
			"schema":       "/api/v1/schema",
			"match":        "POST /api/v1/match",
			"submit":       "POST /api/v1/reports",
			"report":       "/api/v1/reports/{code}",
		},
	})
}

type apiCapability struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Blocking    bool   `json:"blocking,omitempty"`
	Retired     bool   `json:"retired,omitempty"`
}

func (s *Server) apiCapabilities(w http.ResponseWriter, r *http.Request) {
	blocking := map[string]bool{}
	for _, k := range s.cat.Categories {
		blocking[k.ID] = k.Blocking
	}
	out := []apiCapability{}
	for _, cp := range s.cat.Capabilities {
		out = append(out, apiCapability{cp.ID, cp.Category(), cp.Name, cp.Description, blocking[cp.Category()], cp.Retired})
	}
	readable(w)
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": out})
}

type apiMacSummary struct {
	Identifier string           `json:"identifier"`
	Name       string           `json:"name"`
	Line       string           `json:"line"`
	Years      string           `json:"years"`
	Verdicts   []status.Verdict `json:"verdicts"`
	Configs    []string         `json:"configs"`
	URL        string           `json:"url"`
}

func macSummary(m *macView) apiMacSummary {
	x := apiMacSummary{Identifier: m.Identifier, Name: m.Title, Line: m.LineKey, Years: m.Years, Verdicts: m.Verdicts,
		URL: BaseURL + "/mac/" + url.PathEscape(m.Slug), Configs: []string{}}
	for _, cv := range m.Configs {
		x.Configs = append(x.Configs, cv.ID)
	}
	return x
}

func (s *Server) apiMacs(w http.ResponseWriter, r *http.Request) {
	out := []apiMacSummary{}
	for _, m := range s.data().view.macs {
		out = append(out, macSummary(m))
	}
	readable(w)
	writeJSON(w, http.StatusOK, map[string]any{"macs": out})
}

type apiMacDetail struct {
	apiMacSummary
	EFI          int         `json:"efi"`
	SecurityChip string      `json:"security_chip,omitempty"`
	HardBlocker  string      `json:"hard_blocker,omitempty"`
	BoardIDs     []string    `json:"board_ids"`
	ConfigDetail []apiConfig `json:"configurations"`
}

type apiComponent struct {
	ID   string   `json:"id"`
	Kind string   `json:"kind"`
	Name string   `json:"name"`
	IDs  []string `json:"hardware_ids"`
	BTO  bool     `json:"build_to_order,omitempty"`
}

type apiCapStatus struct {
	ID           string         `json:"id"`
	Verdict      status.Verdict `json:"verdict"`
	LatestReport string         `json:"latest_report,omitempty"`
	Omarchy      string         `json:"omarchy,omitempty"`
	Kernel       string         `json:"kernel,omitempty"`
	TestedAt     string         `json:"tested_at,omitempty"`
	Method       string         `json:"method,omitempty"`
	Stale        bool           `json:"stale,omitempty"`
	Conflict     bool           `json:"conflict,omitempty"`
	Reason       string         `json:"unsupported_reason,omitempty"`
	// Per-connector criteria: each connector's status.
	Ports []apiPortStatus `json:"ports,omitempty"`
}

type apiPortStatus struct {
	Connector string         `json:"connector"`
	Verdict   status.Verdict `json:"verdict"`
	CoveredBy string         `json:"covered_by,omitempty"` // untested, but a connector in its group passed
	Suspect   bool           `json:"possible_hardware_fault,omitempty"`
}

type apiReportRef struct {
	Code     string `json:"code"`
	State    string `json:"state"`
	TestedAt string `json:"tested_at"`
	Omarchy  string `json:"omarchy"`
	Kernel   string `json:"kernel,omitempty"`
	URL      string `json:"url"`
}

type apiConfig struct {
	ID           string         `json:"id"`
	Identifier   string         `json:"identifier"`
	Label        string         `json:"label"`
	Distinction  string         `json:"distinction"`
	Release      string         `json:"release"`
	OrderNumbers []string       `json:"order_numbers"`
	Components   []apiComponent `json:"components"`
	Ports        []string       `json:"ports"`
	Connectors   []connInfo     `json:"connectors"` // the port layout; empty until researched
	LayoutSource []string       `json:"layout_sources,omitempty"`
	Features     []string       `json:"features"`
	OutOfScope   string         `json:"out_of_scope,omitempty"`
	Verdict      status.Verdict `json:"verdict"`
	Blocker      string         `json:"blocker,omitempty"`
	Applicable   int            `json:"applicable"`
	Tested       int            `json:"tested"`
	Verified     bool           `json:"verified"`
	Counts       status.Counts  `json:"counts"`
	Capabilities []apiCapStatus `json:"capabilities"`
	Reports      []apiReportRef `json:"reports"`
	URL          string         `json:"url"`
}

func configJSON(cv *configView) apiConfig {
	x := apiConfig{ID: cv.ID, Identifier: cv.Mac.Identifier, Label: cv.Label, Distinction: cv.Diff, Release: cv.ReleaseName,
		OrderNumbers: cv.OrderNumbers, Ports: cv.Ports, Features: cv.Features, OutOfScope: cv.OutOfScope,
		Verdict: cv.Status.Verdict, Blocker: cv.Status.Blocker, Applicable: cv.Status.Applicable, Tested: cv.Status.Tested,
		Verified: cv.Verified, Counts: cv.Status.Counts, Components: []apiComponent{}, Capabilities: []apiCapStatus{},
		Reports: []apiReportRef{}, URL: BaseURL + "/mac/" + url.PathEscape(cv.Mac.Slug) + "#cfg-" + cv.ID,
		Connectors: cv.Conns, LayoutSource: cv.LayoutSrc}
	if x.Connectors == nil {
		x.Connectors = []connInfo{}
	}
	for _, c := range cv.Components {
		ids := c.IDs
		if ids == nil {
			ids = []string{}
		}
		x.Components = append(x.Components, apiComponent{c.ID, c.Kind, c.Name, ids, c.BTO})
	}
	for _, cat := range cv.Categories {
		for _, cp := range cat.Caps {
			cs := apiCapStatus{ID: cp.ID, Verdict: cp.Verdict, LatestReport: cp.Code,
				Omarchy: cp.Omarchy, Kernel: cp.Kernel, TestedAt: cp.Date, Method: cp.Method, Stale: cp.Stale, Conflict: cp.Conflict, Reason: cp.Reason}
			for _, p := range cp.Ports {
				cs.Ports = append(cs.Ports, apiPortStatus{p.ID, p.Verdict, p.CoveredBy, p.Suspect})
			}
			x.Capabilities = append(x.Capabilities, cs)
		}
	}
	for _, rs := range cv.Results {
		x.Reports = append(x.Reports, apiReportRef{rs.Code, rs.State, rs.TestedAt, rs.Omarchy, rs.Kernel, BaseURL + "/report/" + rs.Code})
	}
	return x
}

func (s *Server) apiMac(w http.ResponseWriter, r *http.Request) {
	m := s.data().view.bySlug[strings.ToLower(strings.ReplaceAll(r.PathValue("identifier"), ",", "-"))]
	if m == nil {
		apiFail(w, http.StatusNotFound, "no Mac with that identifier")
		return
	}
	x := apiMacDetail{apiMacSummary: macSummary(m), EFI: m.EFI, SecurityChip: m.Chip, HardBlocker: m.HardBlocker,
		BoardIDs: m.BoardIDs, ConfigDetail: []apiConfig{}}
	for _, cv := range m.Configs {
		x.ConfigDetail = append(x.ConfigDetail, configJSON(cv))
	}
	readable(w)
	writeJSON(w, http.StatusOK, x)
}

func (s *Server) apiConfig(w http.ResponseWriter, r *http.Request) {
	cv := s.data().view.configs[r.PathValue("id")]
	if cv == nil {
		apiFail(w, http.StatusNotFound, "no configuration with that ID")
		return
	}
	readable(w)
	writeJSON(w, http.StatusOK, configJSON(cv))
}

func (s *Server) apiSchema(w http.ResponseWriter, r *http.Request) {
	readable(w)
	w.Header().Set("Content-Type", "application/schema+json")
	w.Write(results.SchemaV1JSON)
}

type apiMatchCandidate struct {
	match.Candidate
	Label string `json:"label"`
	URL   string `json:"url"`
}

// apiMatchResult is POST /match's answer, the decision first: exact, then
// config (the single best configuration, or null), then best (every
// configuration tied at the top score, in catalog order), then the full
// ranking.
type apiMatchResult struct {
	Identifier string              `json:"identifier"`
	By         string              `json:"by"`
	Exact      bool                `json:"exact"`
	Config     *string             `json:"config"`
	Best       []string            `json:"best"`
	Candidates []apiMatchCandidate `json:"candidates"`
}

func (s *Server) matchJSON(p match.Probe) apiMatchResult {
	res := s.match.Match(p)
	out := apiMatchResult{Identifier: res.Identifier, By: res.By, Exact: res.Exact, Best: []string{}, Candidates: []apiMatchCandidate{}}
	if res.Exact {
		best := res.Best()
		out.Config = &best
	}
	for _, c := range res.Candidates {
		if c.Score == res.Candidates[0].Score {
			out.Best = append(out.Best, c.Config)
		}
		x := apiMatchCandidate{Candidate: c}
		if cv := s.data().view.configs[c.Config]; cv != nil {
			x.Label = cv.Mac.Identifier + " · " + cv.Diff + " · " + cv.ReleaseName
			x.URL = BaseURL + "/mac/" + url.PathEscape(cv.Mac.Slug) + "#cfg-" + cv.ID
		}
		out.Candidates = append(out.Candidates, x)
	}
	return out
}

func (s *Server) apiMatch(w http.ResponseWriter, r *http.Request) {
	var p match.Probe
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&p); err != nil {
		apiFail(w, http.StatusBadRequest, "send JSON: {product_name, board_id, cpu, pci: [...], usb: [...]}")
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, http.StatusOK, s.matchJSON(p))
}

// apiSubmit takes a diagnostic report from a registered source.
func (s *Server) apiSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	key := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	src, err := s.store.SourceByKey(ctx, key)
	switch {
	case errors.Is(err, store.ErrRevoked):
		apiFail(w, http.StatusForbidden, "this source's key was revoked")
		return
	case errors.Is(err, store.ErrNotFound):
		w.Header().Set("WWW-Authenticate", `Bearer realm="doesitomarchy"`)
		apiFail(w, http.StatusUnauthorized, "send your source key: Authorization: Bearer doi_…  (see "+BaseURL+"/api)")
		return
	case err != nil:
		s.log.Error("api key", "err", err)
		apiFail(w, http.StatusInternalServerError, "internal error")
		return
	}
	if f := r.URL.Query().Get("format"); f != "" && f != results.SchemaV1 {
		apiFail(w, http.StatusUnsupportedMediaType, "unknown format "+strconv.Quote(f)+"; send "+results.SchemaV1)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, results.MaxSize))
	if err != nil {
		apiFail(w, http.StatusRequestEntityTooLarge, "reports are limited to 1 MiB")
		return
	}
	if n, err := s.store.RecentSubmissions(ctx, src.ID, time.Now().Add(-time.Hour)); err == nil && n >= ReportsPerHour {
		w.Header().Set("Retry-After", "600")
		apiFail(w, http.StatusTooManyRequests, "this source has submitted "+strconv.Itoa(ReportsPerHour)+" reports in the last hour; try again later")
		return
	}
	f, err := results.Parse(raw)
	if err != nil {
		apiFail(w, http.StatusBadRequest, "invalid report", err.Error())
		return
	}
	if f.Source.ID != "" && store.NormalizeSourceID(f.Source.ID) != src.ID {
		apiFail(w, http.StatusForbidden, "this key belongs to source "+strconv.Quote(src.ID)+", not "+strconv.Quote(f.Source.ID))
		return
	}
	f.Source.ID = src.ID
	res, err := results.Validate(f, s.cat, time.Now())
	if err != nil {
		var problems results.Errors
		if errors.As(err, &problems) {
			code := http.StatusBadRequest
			for _, p := range problems {
				if strings.HasPrefix(p, "config: no configuration matches") {
					code = http.StatusUnprocessableEntity
				}
			}
			apiFail(w, code, "invalid report", problems...)
			return
		}
		apiFail(w, http.StatusBadRequest, "invalid report", err.Error())
		return
	}
	id, err := s.store.InsertResult(ctx, res, raw, results.SchemaV1, "source:"+src.ID)
	if err != nil {
		s.log.Error("store report", "source", src.ID, "err", err)
		apiFail(w, http.StatusInternalServerError, "internal error")
		return
	}
	d, err := s.store.Result(ctx, id)
	if err != nil {
		s.log.Error("load report", "err", err)
		apiFail(w, http.StatusInternalServerError, "internal error")
		return
	}
	flags := []map[string]string{}
	for _, fl := range d.Flags {
		flags = append(flags, map[string]string{"kind": fl.Kind, "detail": fl.Detail})
	}
	s.log.Info("report submitted", "source", src.ID, "code", d.Code, "config", d.ConfigID, "flags", len(flags))
	w.Header().Set("Location", "/api/v1/reports/"+d.Code)
	writeJSON(w, http.StatusCreated, map[string]any{
		"code": d.Code, "state": d.State, "config": d.ConfigID, "candidates": d.Candidates, "flags": flags,
		"status_url": BaseURL + "/api/v1/reports/" + d.Code,
	})
}

// apiReport tells a submitter where their report stands.
func (s *Server) apiReport(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !store.IsCode(code) {
		apiFail(w, http.StatusNotFound, "no report with that code")
		return
	}
	id, err := s.store.ResultIDByCode(r.Context(), code)
	if err == nil {
		var d *store.ResultDetail
		if d, err = s.store.Result(r.Context(), id); err == nil {
			out := map[string]any{"code": d.Code, "state": d.State, "config": d.ConfigID, "identifier": d.Identifier,
				"tested_at": d.TestedAt, "submitted_at": d.SubmittedAt}
			if d.State == store.Rejected || d.State == store.Retracted {
				out["reason"] = d.StateReason
			}
			if d.State == store.Accepted || d.State == store.Retracted {
				out["url"] = BaseURL + "/report/" + d.Code
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		apiFail(w, http.StatusNotFound, "no report with that code")
		return
	}
	s.log.Error("report status", "err", err)
	apiFail(w, http.StatusInternalServerError, "internal error")
}
