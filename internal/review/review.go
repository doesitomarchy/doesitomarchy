// Package review holds the fixed rules a report must pass before the AI
// reviewer may accept it (PLAN.md §32), and the check, hold and accept steps
// that the admin CLI and the reviewer's SSH command share.
//
// Every gate reads only what the server stored and derived: states, flags,
// the source's standing, hardware IDs, scrubber marks, item methods. None
// reads free text for meaning, so nothing written in a report (an "approve
// me" in a note) can make it pass.
package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/match"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
	"github.com/doesitomarchy/doesitomarchy/pkg/report"
)

// OmaBootSource is OmaBoot? Live's source ID (data/sources/boot-live.yaml).
// Only its reports may be accepted by the reviewer (decision 1a): they are
// structured, made by our own tool, and sent with its own key.
const OmaBootSource = "boot-live"

// PerMacWindow is the window the per-Mac limit counts in.
const PerMacWindow = 24 * time.Hour

// Input is everything the gates read about one report.
type Input struct {
	R         *store.ResultDetail
	Raw       string        // the scrubbed raw report
	Source    *store.Source // nil when the source isn't registered
	Duplicate string        // the code of an earlier report this one duplicates, or ""
	SameMac   int           // reports from this source and Mac in the PerMacWindow up to this one, itself included
}

// Gate is one rule. Hold says why the report must wait for the owner, or ""
// when it passes.
type Gate struct {
	Name string
	Hold func(in *Input) string
}

// Gates are the rules, in the order they're reported. A report passes only
// if every one does.
var Gates = []Gate{
	{"pending", pending},
	{"omaboot", omabootOnly},
	{"source", sourceInGoodStanding},
	{"one-config", oneConfig},
	{"no-open-flags", noOpenFlags},
	{"no-console-changes", noConsoleChanges},
	{"not-vm", notVM},
	{"not-duplicate", notDuplicate},
	{"per-mac-limit", underPerMacLimit},
	{"nothing-scrubbed", nothingScrubbed},
	{"evidence", evidenceFitsMethod},
}

// pending: only a pending report can be accepted.
func pending(in *Input) string {
	if in.R.State != store.Pending {
		return "the report is " + in.R.State + ", not pending"
	}
	return ""
}

// omabootOnly: the reviewer may only accept OmaBoot? reports (decision 1a).
func omabootOnly(in *Input) string {
	if in.R.SourceID != OmaBootSource {
		return "not an OmaBoot? report: for the owner"
	}
	return ""
}

// sourceInGoodStanding: the source is registered, its key isn't revoked, and
// a maintainer has marked it trusted (doiomad sources trust ID trusted), the
// mark PLAN §20.1 set aside for auto-accepting.
func sourceInGoodStanding(in *Input) string {
	switch src := in.Source; {
	case src == nil:
		return fmt.Sprintf("source %q is not registered", in.R.SourceID)
	case src.RevokedAt != "":
		return fmt.Sprintf("source %q was revoked on %s", src.ID, src.RevokedAt)
	case src.Trust != "trusted":
		return fmt.Sprintf("source %q is %s, not trusted (doiomad sources trust %s trusted)", src.ID, src.Trust, src.ID)
	}
	return ""
}

// oneConfig: the hardware matched exactly one configuration. A report that
// fitted several holds even after a maintainer picked one: the pick is a
// person's judgement, so the owner accepts it.
func oneConfig(in *Input) string {
	if len(in.R.Candidates) > 1 {
		return fmt.Sprintf("the hardware fits %d configurations: %s", len(in.R.Candidates), strings.Join(in.R.Candidates, ", "))
	}
	for _, fl := range in.R.Flags {
		if fl.Kind == results.FlagAmbiguous {
			return fmt.Sprintf("the configuration was ambiguous (flag %d); a maintainer picked it", fl.ID)
		}
	}
	return ""
}

// noOpenFlags: every flag of any kind is resolved.
func noOpenFlags(in *Input) string {
	var open []string
	for _, fl := range in.R.Flags {
		if fl.ResolvedAt == "" {
			open = append(open, fmt.Sprintf("%s (flag %d)", fl.Kind, fl.ID))
		}
	}
	if len(open) > 0 {
		return "open flags: " + strings.Join(open, ", ")
	}
	return ""
}

// noConsoleChanges: no result was changed by hand in OmaBoot?'s console
// (force, fake). Redone checks are fine: they were measured.
func noConsoleChanges(in *Input) string {
	var ch []string
	for _, c := range in.R.Console {
		if c.Kind == report.ConsoleChange {
			ch = append(ch, c.Command)
		}
	}
	if len(ch) > 0 {
		return "results changed by hand in the console: " + strings.Join(ch, "; ")
	}
	return ""
}

// notVM: the hardware isn't a virtual machine's (the PCI vendors staging's
// smoke tests show; no Mac has them).
func notVM(in *Input) string {
	if results.FromVM(probe(in.R)) {
		return "the hardware is a virtual machine's (QEMU or virtio PCI IDs)"
	}
	return ""
}

// notDuplicate: no earlier report (not rejected) has the same source,
// tester, configuration and test time, read again from what's stored. It
// holds even when the duplicate flag was resolved.
func notDuplicate(in *Input) string {
	if in.Duplicate != "" {
		return "duplicates report " + in.Duplicate
	}
	return ""
}

// underPerMacLimit: the source sent no more than the API's per-Mac limit
// from this Mac in the 24 hours up to this report. The API refuses more on
// arrival, so this catches only reports that got past it (an import, or a
// VM exempted on staging).
func underPerMacLimit(in *Input) string {
	if in.SameMac > results.ReportsPerDayPerMac {
		return fmt.Sprintf("%d reports from this Mac through %s in 24 hours; the limit is %d", in.SameMac, in.R.SourceID, results.ReportsPerDayPerMac)
	}
	return ""
}

// nothingScrubbed: the scrubber removed nothing. The store doesn't record
// scrubber hits, but every redaction leaves results.Redacted in the stored
// text, so its absence everywhere means nothing private was found. A tool
// that sent the marker itself holds too, which errs the safe way.
func nothingScrubbed(in *Input) string {
	r := in.R
	var where []string
	has := func(label string, texts ...string) {
		for _, t := range texts {
			if strings.Contains(t, results.Redacted) {
				where = append(where, label)
				return
			}
		}
	}
	has("raw report", in.Raw)
	has("hardware", r.Hardware)
	has("notes", r.Notes)
	for _, it := range r.Items {
		has(it.Capability, it.Note, it.Evidence)
	}
	for _, x := range r.Extras {
		has("extra "+x.ID, x.Label, x.Detail)
	}
	for _, c := range r.Console {
		has("console", c.Command)
	}
	for _, p := range r.ReplacedParts {
		has("replaced parts", append([]string{p.Detail}, p.IDs...)...)
	}
	if len(where) > 0 {
		return "the scrubber redacted personal data in: " + strings.Join(dedupe(where), ", ")
	}
	return ""
}

// evidenceFitsMethod: each item carries what its method promises. The rules
// are deliberately conservative; anything else holds for the owner:
//   - a tested item (supported, partial, failed) measured by the tool
//     (automatic) or confirmed with a code given back (challenge) must say
//     what was measured: non-empty evidence;
//   - an observed item (a person looked) must say what they saw, in its
//     evidence or note;
//   - fixture results are test data and never pass, nor does a tested item
//     without a method;
//   - a not_tested item must give a reason, and the catch-all reason other
//     must be explained in its note or evidence.
func evidenceFitsMethod(in *Input) string {
	var bad []string
	for _, it := range in.R.Items {
		name := it.Capability
		if it.Connector != "" {
			name += "@" + it.Connector
		}
		ev, note := strings.TrimSpace(it.Evidence), strings.TrimSpace(it.Note)
		switch {
		case it.Status == "not_tested" && it.Reason == "":
			bad = append(bad, name+": not tested, without a reason")
		case it.Status == "not_tested" && it.Reason == "other" && ev == "" && note == "":
			bad = append(bad, name+": not tested for an unexplained other reason")
		case it.Status == "not_tested":
		case it.Method == "automatic" || it.Method == "challenge":
			if ev == "" {
				bad = append(bad, name+": "+it.Method+" without evidence")
			}
		case it.Method == "observed":
			if ev == "" && note == "" {
				bad = append(bad, name+": observed without evidence or a note")
			}
		case it.Method == "fixture":
			bad = append(bad, name+": a fixture result (test data)")
		default:
			bad = append(bad, name+": "+it.Status+" without a method")
		}
	}
	if len(bad) == 0 {
		return ""
	}
	const show = 5
	more := ""
	if len(bad) > show {
		more = fmt.Sprintf(", and %d more", len(bad)-show)
		bad = bad[:show]
	}
	return "evidence doesn't fit the method: " + strings.Join(bad, "; ") + more
}

func probe(r *store.ResultDetail) match.Probe {
	hw := map[string]any{}
	json.Unmarshal([]byte(r.Hardware), &hw)
	return results.ProbeFromHardware(hw, r.Identifier)
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// GateResult is one gate's outcome.
type GateResult struct {
	Gate   string `json:"gate"`
	Pass   bool   `json:"pass"`
	Reason string `json:"reason,omitempty"`
}

// Verdict is the gates' answer for one report: pass only if every gate
// passes; Reasons has one line per gate that held.
type Verdict struct {
	Code    string       `json:"code"`
	Verdict string       `json:"verdict"` // pass | hold
	Reasons []string     `json:"reasons"`
	Gates   []GateResult `json:"gates"`
}

// Pass reports whether every gate passed.
func (v Verdict) Pass() bool { return v.Verdict == "pass" }

// Evaluate runs every gate.
func Evaluate(in *Input) Verdict {
	v := Verdict{Code: in.R.Code, Verdict: "pass", Reasons: []string{}}
	for _, g := range Gates {
		why := g.Hold(in)
		v.Gates = append(v.Gates, GateResult{Gate: g.Name, Pass: why == "", Reason: why})
		if why != "" {
			v.Verdict = "hold"
			v.Reasons = append(v.Reasons, g.Name+": "+why)
		}
	}
	return v
}

// Load reads what the gates need about result id.
func Load(ctx context.Context, st *store.Store, id int64) (*Input, error) {
	r, err := st.Result(ctx, id)
	if err != nil {
		return nil, err
	}
	in := &Input{R: r}
	if _, in.Raw, err = st.ResultReport(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	srcs, err := st.Sources(ctx)
	if err != nil {
		return nil, err
	}
	for i := range srcs {
		if srcs[i].ID == r.SourceID {
			in.Source = &srcs[i]
		}
	}
	if in.Duplicate, err = st.DuplicateOf(ctx, id); err != nil {
		return nil, err
	}
	if in.SameMac, err = st.SameMacReports(ctx, id, PerMacWindow); err != nil {
		return nil, err
	}
	return in, nil
}

// Check loads result id and runs every gate.
func Check(ctx context.Context, st *store.Store, id int64) (Verdict, error) {
	in, err := Load(ctx, st, id)
	if err != nil {
		return Verdict{}, err
	}
	return Evaluate(in), nil
}

// ErrHold is returned by Accept when a gate holds.
var ErrHold = errors.New("the report doesn't pass every gate")

// Outcome says what Accept did.
type Outcome int

const (
	Accepted    Outcome = iota // the report counts now
	WouldAccept                // shadow mode: recorded as a note, still pending
	NotAccepted                // a gate held (with ErrHold), or an error
)

// Accept re-runs every gate and accepts result id as by, with note, only if
// all pass; otherwise it returns the verdict and ErrHold. In shadow mode it
// records the acceptance it would have made as a review note ("would
// accept: …") and leaves the report pending (decision 6a).
func Accept(ctx context.Context, st *store.Store, id int64, note, by string, shadow bool) (Verdict, Outcome, error) {
	if strings.TrimSpace(note) == "" {
		return Verdict{}, NotAccepted, errors.New("a note saying why is required")
	}
	v, err := Check(ctx, st, id)
	if err != nil {
		return v, NotAccepted, err
	}
	if !v.Pass() {
		return v, NotAccepted, ErrHold
	}
	if shadow {
		return v, WouldAccept, st.AddReviewNote(ctx, id, store.ActionWouldAccept, "would accept: "+strings.TrimSpace(note), by)
	}
	if err := st.AcceptResult(ctx, id, note, by); err != nil {
		return v, NotAccepted, err
	}
	return v, Accepted, nil
}

// Hold keeps result id pending with a note saying why: not a state change,
// only the newest note on it.
func Hold(ctx context.Context, st *store.Store, id int64, note, by string) error {
	return st.AddReviewNote(ctx, id, store.ActionHeld, note, by)
}
