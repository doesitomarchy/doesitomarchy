package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/review"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// The AI reviewer's only way in (PLAN.md §32): an SSH forced command. sshd
// runs "doiomad reviewer" whatever the client asked for, and puts the
// client's command line in SSH_ORIGINAL_COMMAND, which is read here as data:
// a verb, a report code, and for accept and hold a note. Nothing else runs,
// and nothing is passed to a shell. The authorized_keys line and its wrapper
// are in deploy/RUNBOOK.md ("The AI reviewer's SSH key").

const reviewerUsage = `doiomad reviewer — the AI reviewer's SSH forced command (PLAN §32)

Reads the command from $SSH_ORIGINAL_COMMAND; prints JSON:
  list                      pending reports
  show CODE                 a report (scrubbed), its effect, and its configuration's accepted results
  check CODE                the review gates: pass, or hold with reasons
  accept CODE --note TEXT   accept as "reviewer" if every gate passes (shadow mode: record
                            "would accept: TEXT" and leave it pending)
  hold CODE --note TEXT     keep it pending with a note saying why

CODE is a report's 10-character code. DOIOMAD_REVIEWER_SHADOW=0 turns shadow mode off;
it is on otherwise. -read-only (for the bug hunt's key) allows list, show and check only.
`

// maxReviewerCommand bounds SSH_ORIGINAL_COMMAND: a verb, a code and a note.
const maxReviewerCommand = store.MaxReviewNote + 64

// reviewerRequest is one parsed reviewer command.
type reviewerRequest struct{ Verb, Code, Note string }

// parseReviewerCommand reads SSH_ORIGINAL_COMMAND. Only the five verbs are
// allowed, each with exactly its arguments; the note is the rest of the line
// after --note (one pair of surrounding quotes removed), so it needs no
// shell quoting and no escape is ever interpreted.
func parseReviewerCommand(line string) (reviewerRequest, error) {
	var req reviewerRequest
	if len(line) > maxReviewerCommand {
		return req, fmt.Errorf("the command is %d bytes; the limit is %d", len(line), maxReviewerCommand)
	}
	for _, r := range line {
		if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f {
			return req, errors.New("the command contains a control character")
		}
	}
	verb, rest := cutWord(line)
	req.Verb = verb
	switch verb {
	case "":
		return req, errors.New("no command given")
	case "list":
		if rest != "" {
			return req, errors.New("list takes no arguments")
		}
		return req, nil
	case "show", "check", "accept", "hold":
	default:
		return req, fmt.Errorf("%q is not allowed", verb)
	}
	code, rest := cutWord(rest)
	req.Code = strings.ToLower(code)
	if !store.IsCode(req.Code) {
		return req, fmt.Errorf("%s: give a report code (10 hex characters), not %q", verb, code)
	}
	if verb == "show" || verb == "check" {
		if rest != "" {
			return req, fmt.Errorf("%s takes one report code", verb)
		}
		return req, nil
	}
	opt, note := cutWord(rest)
	if opt != "--note" && opt != "-note" {
		return req, fmt.Errorf("%s: give --note TEXT after the code", verb)
	}
	if n := len(note); n >= 2 && (note[0] == '"' || note[0] == '\'') && note[n-1] == note[0] {
		note = strings.TrimSpace(note[1 : n-1])
	}
	if note == "" {
		return req, fmt.Errorf("%s: the note is empty", verb)
	}
	req.Note = note
	return req, nil
}

// cutWord splits off the first whitespace-separated word.
func cutWord(s string) (word, rest string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\n"); i >= 0 {
		return s[:i], strings.TrimSpace(s[i:])
	}
	return s, ""
}

// reviewerShadow says whether the reviewer only recommends (decision 6a):
// on unless DOIOMAD_REVIEWER_SHADOW=0. The forced command's wrapper sets the
// environment; an SSH client can't.
func reviewerShadow() bool { return os.Getenv("DOIOMAD_REVIEWER_SHADOW") != "0" }

func cmdReviewer(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reviewer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, reviewerUsage) }
	db, dataDir := dbFlags(fs)
	readOnly := fs.Bool("read-only", false, "allow list, show and check only (the bug hunt's key)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprint(stderr, "reviewer: the command comes from $SSH_ORIGINAL_COMMAND, not arguments\n\n"+reviewerUsage)
		return 2
	}
	req, err := parseReviewerCommand(os.Getenv("SSH_ORIGINAL_COMMAND"))
	if err != nil {
		fmt.Fprintf(stderr, "reviewer: %v\n\n%s", err, reviewerUsage)
		return 2
	}
	if *readOnly && (req.Verb == "accept" || req.Verb == "hold") {
		fmt.Fprintf(stderr, "reviewer: %s is not allowed with this key (read-only)\n", req.Verb)
		return 2
	}
	ctx := context.Background()
	st, c, _, err := openSynced(ctx, *db, *dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "reviewer: %v\n", err)
		return 1
	}
	defer st.Close()
	return runReviewer(ctx, st, c, req, reviewerShadow(), stdout, stderr)
}

func runReviewer(ctx context.Context, st *store.Store, c *catalog.Catalog, req reviewerRequest, shadow bool, stdout, stderr io.Writer) int {
	fail := func(err error) int {
		fmt.Fprintf(stderr, "reviewer %s: %v\n", req.Verb, err)
		return 1
	}
	out := func(v any) int {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return fail(err)
		}
		return 0
	}
	if req.Verb == "list" {
		list, err := st.ListResults(ctx, store.ResultFilter{State: store.Pending, Limit: 200})
		if err != nil {
			return fail(err)
		}
		rows := []reviewerSummary{}
		for _, x := range list {
			rows = append(rows, summaryJSON(x))
		}
		return out(rows)
	}
	id, err := st.ResultIDByCode(ctx, req.Code)
	if err != nil {
		return fail(fmt.Errorf("report %s: %w", req.Code, err))
	}
	switch req.Verb {
	case "show":
		x, err := reviewerShow(ctx, st, c, id)
		if err != nil {
			return fail(err)
		}
		return out(x)
	case "check":
		v, err := review.Check(ctx, st, id)
		if err != nil {
			return fail(err)
		}
		return out(v)
	case "accept":
		v, outcome, err := review.Accept(ctx, st, id, req.Note, store.Reviewer, shadow)
		res := reviewerDone{Code: req.Code, Verdict: &v, Note: req.Note, Shadow: shadow}
		switch {
		case errors.Is(err, review.ErrHold):
			res.Outcome = "not accepted: a gate holds"
			out(res)
			return 1
		case err != nil:
			return fail(err)
		case outcome == review.WouldAccept:
			res.Outcome = "would accept (shadow mode: recorded as a note, still pending)"
		default:
			res.Outcome = "accepted"
		}
		return out(res)
	case "hold":
		if err := review.Hold(ctx, st, id, req.Note, store.Reviewer); err != nil {
			return fail(err)
		}
		return out(reviewerDone{Code: req.Code, Outcome: "held", Note: req.Note, Shadow: shadow})
	}
	return fail(fmt.Errorf("%q is not allowed", req.Verb)) // parseReviewerCommand allows nothing else
}

type reviewerDone struct {
	Code    string          `json:"code"`
	Outcome string          `json:"outcome"`
	Note    string          `json:"note"`
	Shadow  bool            `json:"shadow"`
	Verdict *review.Verdict `json:"verdict,omitempty"`
}

type reviewerNote struct {
	Action string `json:"action"` // held | would-accept | accepted
	By     string `json:"by"`
	At     string `json:"at"`
	Note   string `json:"note"`
}

type reviewerSummary struct {
	Code          string        `json:"code"`
	State         string        `json:"state"`
	Mac           string        `json:"mac"`
	Config        string        `json:"config"`
	Source        string        `json:"source"`
	SourceVersion string        `json:"source_version,omitempty"`
	Context       string        `json:"context"`
	TestedAt      string        `json:"tested_at"`
	SubmittedAt   string        `json:"submitted_at"`
	Omarchy       string        `json:"omarchy"`
	Channel       string        `json:"channel"`
	Kernel        string        `json:"kernel,omitempty"`
	Supported     int           `json:"supported"`
	Partial       int           `json:"partial"`
	Failed        int           `json:"failed"`
	NotTested     int           `json:"not_tested"`
	OpenFlags     int           `json:"open_flags"`
	Review        *reviewerNote `json:"review,omitempty"` // the newest review note
}

func summaryJSON(x store.ResultSummary) reviewerSummary {
	s := reviewerSummary{Code: x.Code, State: x.State, Mac: x.Identifier, Config: x.ConfigID, Source: x.SourceID, SourceVersion: x.SourceVersion,
		Context: x.Context, TestedAt: x.TestedAt, SubmittedAt: x.SubmittedAt, Omarchy: x.Omarchy, Channel: x.Channel, Kernel: x.Kernel,
		Supported: x.Supported, Partial: x.Partial, Failed: x.Failed, NotTested: x.NotTested, OpenFlags: x.OpenFlags}
	if n := x.Review; n.Note != "" {
		s.Review = &reviewerNote{Action: n.Action, By: n.By, At: n.At, Note: n.Note}
	}
	return s
}

type reviewerItem struct {
	Capability string `json:"capability"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Connector  string `json:"connector,omitempty"`
	Status     string `json:"status"`
	Method     string `json:"method,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Note       string `json:"note,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
	Applicable bool   `json:"applicable"`
}

type reviewerFlag struct {
	Kind       string `json:"kind"`
	Detail     string `json:"detail"`
	Resolved   bool   `json:"resolved"`
	Resolution string `json:"resolution,omitempty"`
}

// reviewerAccepted is how one criterion (and connector) fared in the
// configuration's accepted reports.
type reviewerAccepted struct {
	Capability string `json:"capability"`
	Connector  string `json:"connector,omitempty"`
	Supported  int    `json:"supported"`
	Partial    int    `json:"partial"`
	Failed     int    `json:"failed"`
}

type reviewerShowJSON struct {
	Report struct {
		reviewerSummary
		Profile       string                 `json:"profile,omitempty"`
		Image         string                 `json:"image,omitempty"`
		Tester        string                 `json:"tester,omitempty"`
		Notes         string                 `json:"notes,omitempty"`
		Hardware      json.RawMessage        `json:"hardware"`
		Fixes         []string               `json:"fixes"`
		ReplacedParts []results.ReplacedPart `json:"replaced_parts"`
		Console       []results.ConsoleEntry `json:"console"`
		Candidates    []string               `json:"candidates"`
		Items         []reviewerItem         `json:"items"`
		Extras        []results.FileExtra    `json:"extras"`
		Flags         []reviewerFlag         `json:"flags"`
	} `json:"report"`
	Raw struct {
		Format string `json:"format"`
		Body   string `json:"body"`
	} `json:"raw"` // as stored: scrubbed
	// Effect is the configuration's status now and if this report were accepted.
	Effect *struct {
		Before string `json:"before"`
		After  string `json:"after"`
	} `json:"effect,omitempty"`
	ConfigAccepted struct {
		Reports []reviewerSummary  `json:"reports"` // newest first
		Items   []reviewerAccepted `json:"items"`
	} `json:"config_accepted"`
}

// reviewerShow is one report as the reviewer reads it: the stored, scrubbed
// report (no contact hash), and its configuration's accepted results.
func reviewerShow(ctx context.Context, st *store.Store, c *catalog.Catalog, id int64) (*reviewerShowJSON, error) {
	d, err := st.Result(ctx, id)
	if err != nil {
		return nil, err
	}
	x := &reviewerShowJSON{}
	r := &x.Report
	r.reviewerSummary = summaryJSON(d.ResultSummary)
	r.Profile, r.Image, r.Tester, r.Notes = d.Profile, d.OmarchyImage, d.TesterHandle, d.Notes
	r.Hardware = json.RawMessage(d.Hardware)
	if !json.Valid(r.Hardware) {
		r.Hardware = json.RawMessage("{}")
	}
	r.Fixes, r.ReplacedParts, r.Console, r.Candidates, r.Extras = orEmpty(d.Fixes), orEmpty(d.ReplacedParts), orEmpty(d.Console),
		orEmpty(d.Candidates), orEmpty(d.Extras)
	r.Items, r.Flags = []reviewerItem{}, []reviewerFlag{}
	for _, it := range d.Items {
		r.Items = append(r.Items, reviewerItem{Capability: it.Capability, Name: it.CapabilityName, Category: it.CategoryName, Connector: it.Connector,
			Status: it.Status, Method: it.Method, Reason: it.Reason, Note: it.Note, Evidence: it.Evidence, Applicable: it.Applicable})
	}
	for _, fl := range d.Flags {
		r.Flags = append(r.Flags, reviewerFlag{Kind: fl.Kind, Detail: fl.Detail, Resolved: fl.ResolvedAt != "", Resolution: fl.Resolution})
	}
	if x.Raw.Format, x.Raw.Body, err = st.ResultReport(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}

	// The effect, as the import preview shows it.
	if v, err := status.ParseVersion(d.Omarchy); err == nil {
		pr := &results.Result{ConfigID: d.ConfigID, Channel: d.Channel, BuiltAt: d.OmarchyBuiltAt, TestedAt: d.TestedAt, Omarchy: v}
		for _, it := range d.Items {
			pr.Items = append(pr.Items, results.Item{Capability: it.Capability, Connector: it.Connector, Status: it.Status, Method: it.Method,
				Evidence: it.Evidence, Applicable: it.Applicable})
		}
		if before, after, err := preview(ctx, st, c, pr); err == nil {
			x.Effect = &struct {
				Before string `json:"before"`
				After  string `json:"after"`
			}{effectDesc(before), effectDesc(after)}
		}
	}

	accepted, err := st.ListResults(ctx, store.ResultFilter{State: store.Accepted, Config: d.ConfigID, Limit: 50})
	if err != nil {
		return nil, err
	}
	x.ConfigAccepted.Reports, x.ConfigAccepted.Items = []reviewerSummary{}, []reviewerAccepted{}
	for _, a := range accepted {
		x.ConfigAccepted.Reports = append(x.ConfigAccepted.Reports, summaryJSON(a))
	}
	ru, err := st.RollupData(ctx)
	if err != nil {
		return nil, err
	}
	at := map[string]int{}
	for _, it := range ru.Items[d.ConfigID] {
		k := it.Capability + "@" + it.Connector
		i, ok := at[k]
		if !ok {
			i = len(x.ConfigAccepted.Items)
			at[k] = i
			x.ConfigAccepted.Items = append(x.ConfigAccepted.Items, reviewerAccepted{Capability: it.Capability, Connector: it.Connector})
		}
		switch a := &x.ConfigAccepted.Items[i]; it.Verdict {
		case status.Supported:
			a.Supported++
		case status.Partial:
			a.Partial++
		case status.Failed:
			a.Failed++
		}
	}
	return x, nil
}

// orEmpty makes a nil list encode as [] rather than null.
func orEmpty[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}
