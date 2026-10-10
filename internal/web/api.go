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
	"github.com/doesitomarchy/doesitomarchy/pkg/report"
)

// The open API (PLAN.md §22.3). Reads are public JSON that any site may
// fetch; submissions need a source key.

// ReportsPerHour is how many reports one source may submit in an hour
// (a variable so tests can lower it). Each source counts on its own.
var ReportsPerHour = 60

// ReportsPerHourPublic is the hourly limit for a source whose key is public
// (OmaBoot? Live), lower than the usual one.
var ReportsPerHourPublic = 30

// ReportsPerDayPerMac is how many reports one Mac may send a day through a
// source whose key is public, because it ships inside a public image
// (OmaBoot? Live). A Mac is its hardware fingerprint: no serial numbers.
var ReportsPerDayPerMac = 5

// publicKeySources are the sources limited per Mac.
var publicKeySources = map[string]bool{"boot-live": true}

// apiEndpoint is one route. This table drives the routes, the index, the
// OpenAPI document and llms.txt, so they can't drift apart.
type apiEndpoint struct {
	Name    string // the index key and the docs' anchor
	Method  string
	Path    string
	Summary string
	Example string // a concrete path to try
	Body    string // an example request body
	Key     bool   // needs a source key
	handle  func(*Server, http.ResponseWriter, *http.Request)
	req     any // the request body type, for OpenAPI (nil: none, or the report schema)
	res     any // the response type, for OpenAPI
}

var apiEndpoints []apiEndpoint

// Set in init, since the index handler reads the table (a package-level
// initializer would be a cycle).
func init() {
	apiEndpoints = []apiEndpoint{
		{Name: "index", Method: "GET", Path: "/api/v1", Summary: "List the endpoints", handle: (*Server).apiIndex, res: apiIndexDoc{}},
		{Name: "submit", Method: "POST", Path: "/api/v1/reports", Summary: "Submit a diagnostic report (needs a source key)", Key: true, handle: (*Server).apiSubmit, res: apiSubmitted{}},
		{Name: "report", Method: "GET", Path: "/api/v1/reports/{code}", Example: "/api/v1/reports/3f9a1c07be", Summary: "Check where a submitted report stands", handle: (*Server).apiReport, res: apiReportStatus{}},
		{Name: "schema", Method: "GET", Path: "/api/v1/schema", Summary: "The diagnostic report format, as JSON Schema", handle: (*Server).apiSchema},
		{Name: "snapshot", Method: "GET", Path: "/api/v1/snapshot", Summary: "What a test tool needs from the catalog to build, check and match a report offline", handle: (*Server).apiSnapshot, res: report.CatalogSnapshot{}},
		{Name: "macs", Method: "GET", Path: "/api/v1/macs", Summary: "List every Intel Mac", handle: (*Server).apiMacs, res: apiMacList{}},
		{Name: "mac", Method: "GET", Path: "/api/v1/macs/{identifier}", Example: "/api/v1/macs/MacBookPro8,2", Summary: "Get a Mac and all its configurations", handle: (*Server).apiMac, res: apiMacDetail{}},
		{Name: "config", Method: "GET", Path: "/api/v1/configs/{id}", Example: "/api/v1/configs/macbookpro8-2-15-early-2011-a", Summary: "Get a configuration: hardware, ports, criteria status and reports", handle: (*Server).apiConfig, res: apiConfig{}},
		{Name: "capabilities", Method: "GET", Path: "/api/v1/capabilities", Summary: "List the test criteria", handle: (*Server).apiCapabilities, res: apiCapabilityList{}},
		{Name: "match", Method: "POST", Path: "/api/v1/match", Summary: "Identify a Mac and its configuration from hardware IDs", Body: `{"product_name": "MacBookPro8,2", "pci": ["1002:6760"]}`, handle: (*Server).apiMatch, req: match.Probe{}, res: apiMatchResult{}},
		{Name: "openapi", Method: "GET", Path: "/api/v1/openapi.json", Summary: "This API as an OpenAPI 3.1 document", handle: (*Server).apiOpenAPI},
	}
}

func apiEndpointNamed(name string) apiEndpoint {
	for _, e := range apiEndpoints {
		if e.Name == name {
			return e
		}
	}
	panic("no API endpoint " + name)
}

func (s *Server) apiRoutes(mux *http.ServeMux) {
	for _, e := range apiEndpoints {
		h := func(w http.ResponseWriter, r *http.Request) { e.handle(s, w, r) }
		mux.HandleFunc(e.Method+" "+e.Path, h)
		if e.Name == "index" {
			mux.HandleFunc(e.Method+" "+e.Path+"/{$}", h)
		}
	}
	mux.HandleFunc("/api/", apiNotFound)
}

func apiNotFound(w http.ResponseWriter, r *http.Request) {
	apiFail(w, http.StatusNotFound, "no such endpoint; see "+BaseURL+"/api")
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

type apiIndexDoc struct {
	Name      string            `json:"name"`
	Docs      string            `json:"docs"`
	Schema    string            `json:"schema" doc:"The report format version submissions use"`
	Endpoints map[string]string `json:"endpoints" doc:"Each endpoint's path, with its method when that isn't GET"`
	MCP       string            `json:"mcp" doc:"The MCP server, for AI assistants"`
}

func (s *Server) apiIndex(w http.ResponseWriter, r *http.Request) {
	x := apiIndexDoc{Name: "DoesItOmarchy API", Docs: BaseURL + "/api", Schema: results.SchemaV1, Endpoints: map[string]string{}, MCP: BaseURL + "/mcp"}
	for _, e := range apiEndpoints {
		switch {
		case e.Name == "index":
		case e.Method == "GET":
			x.Endpoints[e.Name] = e.Path
		default:
			x.Endpoints[e.Name] = e.Method + " " + e.Path
		}
	}
	readable(w)
	writeJSON(w, http.StatusOK, x)
}

type apiCapability struct {
	ID          string `json:"id" doc:"The criterion ID reports use, e.g. network.wifi"`
	Category    string `json:"category"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Blocking    bool   `json:"blocking,omitempty" doc:"A failure in this category makes the whole configuration fail"`
	Retired     bool   `json:"retired,omitempty" doc:"No longer tested; kept so old reports still resolve"`
	Live        string `json:"live" doc:"Whether a live boot can decide it: yes, no, or t2 (only on Macs with a T2 chip). Live reports send the rest as not_tested, reason live-limit"`
}

type apiCapabilityList struct {
	Capabilities []apiCapability `json:"capabilities"`
}

func (s *Server) apiCapabilities(w http.ResponseWriter, r *http.Request) {
	blocking := map[string]bool{}
	for _, k := range s.cat.Categories {
		blocking[k.ID] = k.Blocking
	}
	out := []apiCapability{}
	for _, cp := range s.cat.Capabilities {
		out = append(out, apiCapability{cp.ID, cp.Category(), cp.Name, cp.Description, blocking[cp.Category()], cp.Retired, cp.Live})
	}
	readable(w)
	writeJSON(w, http.StatusOK, apiCapabilityList{out})
}

type apiMacSummary struct {
	Identifier string           `json:"identifier" doc:"The model identifier, e.g. MacBookPro8,2"`
	Name       string           `json:"name"`
	Line       string           `json:"line" doc:"The product line, e.g. macbook-pro"`
	Years      string           `json:"years"`
	Verdicts   []status.Verdict `json:"verdicts" doc:"The distinct verdicts of its configurations"`
	Configs    []string         `json:"configs" doc:"Its configuration IDs"`
	URL        string           `json:"url" doc:"Its page on the site"`
}

type apiMacList struct {
	Macs []apiMacSummary `json:"macs"`
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
	writeJSON(w, http.StatusOK, apiMacList{out})
}

type apiMacDetail struct {
	apiMacSummary
	EFI          int         `json:"efi" doc:"The firmware's EFI width: 32 or 64"`
	SecurityChip string      `json:"security_chip,omitempty" doc:"t1 or t2, when it has one"`
	HardBlocker  string      `json:"hard_blocker,omitempty" doc:"Why no configuration can run Omarchy (e.g. a 32-bit CPU)"`
	BoardIDs     []string    `json:"board_ids" doc:"Apple board IDs, e.g. Mac-94245A3940C91C80"`
	ConfigDetail []apiConfig `json:"configurations"`
}

type apiComponent struct {
	ID   string   `json:"id"`
	Kind string   `json:"kind"`
	Name string   `json:"name"`
	IDs  []string `json:"hardware_ids" doc:"IDs a probe can match, e.g. pci:1002:6760"`
	GLES string   `json:"gles,omitempty"` // gpu only: highest OpenGL ES version its Linux driver reaches (PLAN §30)
	BTO  bool     `json:"build_to_order,omitempty" doc:"A build-to-order option"`
}

type apiCapStatus struct {
	ID           string         `json:"id"`
	Verdict      status.Verdict `json:"verdict"`
	LatestReport string         `json:"latest_report,omitempty" doc:"The code of the report that set this verdict"`
	// The verdict counts stable releases only (PLAN §28.2).
	Omarchy        string `json:"omarchy,omitempty"`         // canonical: 4.0.4, 4.0.0rc2, 4.0.0.r6713.ga85e29a
	OmarchyChannel string `json:"omarchy_channel,omitempty"` // stable (rc, beta, edge, dev only in newest_build)
	Kernel         string `json:"kernel,omitempty"`
	TestedAt       string `json:"tested_at,omitempty"`
	Method         string `json:"method,omitempty" doc:"automatic, observed, fixture or challenge"`
	Stale          bool   `json:"stale,omitempty" doc:"Tested on an Omarchy release older than the current major"`
	Conflict       bool   `json:"conflict,omitempty" doc:"Accepted reports on the same Omarchy build disagree"`
	Reason         string `json:"unsupported_reason,omitempty"`
	// Per-connector criteria: each connector's status.
	Ports []apiPortStatus `json:"ports,omitempty" doc:"Per-connector results, for port criteria"`
	// NewestBuild is the result on the newest build of any channel, when it
	// differs from the stable verdict.
	NewestBuild *apiNewestBuild `json:"newest_build,omitempty" doc:"The result on the newest pre-release or edge build, when it differs"`
}

type apiNewestBuild struct {
	Verdict        status.Verdict `json:"verdict"`
	Omarchy        string         `json:"omarchy"`
	OmarchyChannel string         `json:"omarchy_channel"` // rc | beta | edge | dev
	Report         string         `json:"report"`
}

type apiPortStatus struct {
	Connector string         `json:"connector"`
	Verdict   status.Verdict `json:"verdict"`
	CoveredBy string         `json:"covered_by,omitempty"` // untested, but a connector in its group passed
	Suspect   bool           `json:"possible_hardware_fault,omitempty"`
}

type apiReportRef struct {
	Code           string   `json:"code"`
	State          string   `json:"state"`
	TestedAt       string   `json:"tested_at"`
	Omarchy        string   `json:"omarchy"`
	OmarchyChannel string   `json:"omarchy_channel"`
	Kernel         string   `json:"kernel,omitempty"`
	Context        string   `json:"context" doc:"Where the test ran: installed, or live (a live boot)"`
	Fixes          []string `json:"fixes,omitempty" doc:"OmaBoot? fixes that took effect, by ID (see /api/v1/snapshot)"`
	URL            string   `json:"url"`
}

type apiConfig struct {
	ID           string         `json:"id"`
	Identifier   string         `json:"identifier"`
	Label        string         `json:"label"`
	Distinction  string         `json:"distinction" doc:"What sets it apart from the Mac's other configurations"`
	Release      string         `json:"release" doc:"Apple's name for the release"`
	BoardIDs     []string       `json:"board_ids,omitempty"`         // tied to the release by real machines (PLAN §29)
	Limitations  []string       `json:"known_limitations,omitempty"` // research, before testing (PLAN §30)
	OrderNumbers []string       `json:"order_numbers" doc:"Apple order numbers, e.g. MC721LL/A"`
	Components   []apiComponent `json:"components"`
	Ports        []string       `json:"ports" doc:"Ports and media that aren't tested per connector"`
	Connectors   []connInfo     `json:"connectors"` // the port layout; empty until researched
	LayoutSource []string       `json:"layout_sources,omitempty"`
	PortmapURL   string         `json:"portmap_url,omitempty"` // the port map drawing (SVG), when there is one
	Features     []string       `json:"features" doc:"Other tested features, e.g. Battery"`
	OutOfScope   string         `json:"out_of_scope,omitempty" doc:"Why it's left out of coverage counts"`
	Verdict      status.Verdict `json:"verdict"`
	Blocker      string         `json:"blocker,omitempty" doc:"The criterion that decided a failed verdict"`
	Applicable   int            `json:"applicable" doc:"How many criteria apply to it"`
	Tested       int            `json:"tested" doc:"How many of those have a result"`
	Verified     bool           `json:"verified" doc:"Every applicable criterion passed"`
	Counts       status.Counts  `json:"counts" doc:"Criteria per verdict"`
	Capabilities []apiCapStatus `json:"capabilities"`
	Reports      []apiReportRef `json:"reports"`
	URL          string         `json:"url"`
}

func configJSON(cv *configView) apiConfig {
	x := apiConfig{ID: cv.ID, Identifier: cv.Mac.Identifier, Label: cv.Label, Distinction: cv.Diff, Release: cv.ReleaseName, BoardIDs: cv.BoardIDs, Limitations: cv.Limitations,
		OrderNumbers: cv.OrderNumbers, Ports: cv.Ports, Features: cv.Features, OutOfScope: cv.OutOfScope,
		Verdict: cv.Status.Verdict, Blocker: cv.Status.Blocker, Applicable: cv.Status.Applicable, Tested: cv.Status.Tested,
		Verified: cv.Verified, Counts: cv.Status.Counts, Components: []apiComponent{}, Capabilities: []apiCapStatus{},
		Reports: []apiReportRef{}, URL: BaseURL + "/mac/" + url.PathEscape(cv.Mac.Slug) + "#cfg-" + cv.ID,
		Connectors: cv.Conns, LayoutSource: cv.LayoutSrc}
	if cv.PortmapKey != "" {
		x.PortmapURL = BaseURL + "/portmap/" + cv.PortmapKey + ".svg"
	}
	if x.Connectors == nil {
		x.Connectors = []connInfo{}
	}
	for _, c := range cv.Components {
		ids := c.IDs
		if ids == nil {
			ids = []string{}
		}
		x.Components = append(x.Components, apiComponent{c.ID, c.Kind, c.Name, ids, c.GLES, c.BTO})
	}
	for _, cat := range cv.Categories {
		for _, cp := range cat.Caps {
			cs := apiCapStatus{ID: cp.ID, Verdict: cp.Verdict, LatestReport: cp.Code,
				Omarchy: cp.Omarchy, OmarchyChannel: cp.Channel, Kernel: cp.Kernel, TestedAt: cp.Date, Method: cp.Method, Stale: cp.Stale, Conflict: cp.Conflict, Reason: cp.Reason}
			if n := cp.Newer; n != nil {
				cs.NewestBuild = &apiNewestBuild{n.Verdict, n.Omarchy, n.Channel, BaseURL + "/report/" + n.Code}
			}
			for _, p := range cp.Ports {
				cs.Ports = append(cs.Ports, apiPortStatus{p.ID, p.Verdict, p.CoveredBy, p.Suspect})
			}
			x.Capabilities = append(x.Capabilities, cs)
		}
	}
	for _, rs := range cv.Results {
		x.Reports = append(x.Reports, apiReportRef{rs.Code, rs.State, rs.TestedAt, rs.Omarchy, rs.Channel, rs.Kernel, rs.Context, rs.Fixes, BaseURL + "/report/" + rs.Code})
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
	By         string              `json:"by" doc:"What identified the Mac: product_name, board_id or devices"`
	Exact      bool                `json:"exact" doc:"Exactly one configuration fits"`
	Config     *string             `json:"config" doc:"The single best configuration, or null when several fit equally"`
	Best       []string            `json:"best" doc:"Every configuration tied at the top score; their order means nothing"`
	Candidates []apiMatchCandidate `json:"candidates" doc:"Every configuration, best first"`
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
	perHour := ReportsPerHour
	if publicKeySources[src.ID] {
		perHour = ReportsPerHourPublic
	}
	if n, err := s.store.RecentSubmissions(ctx, src.ID, time.Now().Add(-time.Hour)); err == nil && n >= perHour {
		w.Header().Set("Retry-After", "600")
		apiFail(w, http.StatusTooManyRequests, "this source has submitted "+strconv.Itoa(perHour)+" reports in the last hour; try again later")
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
	if publicKeySources[src.ID] {
		// The key is public, so the limit is per Mac, counting only reports that would be stored.
		now := time.Now()
		if key := src.ID + " " + results.HardwareFingerprint(f); !s.macLimit.allow(key, now) {
			w.Header().Set("Retry-After", strconv.Itoa(int(s.macLimit.wait(key, now).Seconds())+1))
			apiFail(w, http.StatusTooManyRequests, "this Mac has sent "+strconv.Itoa(ReportsPerDayPerMac)+" reports through "+src.ID+
				" in the last 24 hours; try again after Retry-After seconds")
			return
		}
	}
	s.prepareResult(ctx, res)
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
	x := apiSubmitted{Code: d.Code, State: d.State, Config: d.ConfigID, Candidates: d.Candidates, Flags: []apiFlag{},
		StatusURL: BaseURL + "/api/v1/reports/" + d.Code}
	for _, fl := range d.Flags {
		x.Flags = append(x.Flags, apiFlag{fl.Kind, fl.Detail})
	}
	s.log.Info("report submitted", "source", src.ID, "code", d.Code, "config", d.ConfigID, "flags", len(x.Flags))
	w.Header().Set("Location", "/api/v1/reports/"+d.Code)
	writeJSON(w, http.StatusCreated, x)
}

type apiSubmitted struct {
	Code       string    `json:"code" doc:"The report's code; keep it to check its state"`
	State      string    `json:"state" doc:"pending until a maintainer reviews it"`
	Config     string    `json:"config" doc:"The configuration it was matched to (empty when several fit)"`
	Candidates []string  `json:"candidates" doc:"The configurations that fit equally, when the hardware couldn't tell them apart"`
	Flags      []apiFlag `json:"flags" doc:"Things a maintainer will check, e.g. duplicate or hardware_mismatch"`
	StatusURL  string    `json:"status_url"`
}

type apiFlag struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

type apiReportStatus struct {
	Code        string `json:"code"`
	State       string `json:"state" doc:"pending, accepted, rejected or retracted"`
	Config      string `json:"config"`
	Identifier  string `json:"identifier"`
	TestedAt    string `json:"tested_at"`
	SubmittedAt string `json:"submitted_at"`
	Reason      string `json:"reason,omitempty" doc:"Why it was rejected or retracted"`
	URL         string `json:"url,omitempty" doc:"Its public page, once accepted"`
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
			out := apiReportStatus{Code: d.Code, State: d.State, Config: d.ConfigID, Identifier: d.Identifier,
				TestedAt: d.TestedAt, SubmittedAt: d.SubmittedAt}
			if d.State == store.Rejected || d.State == store.Retracted {
				out.Reason = d.StateReason
			}
			if d.State == store.Accepted || d.State == store.Retracted {
				out.URL = BaseURL + "/report/" + d.Code
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
