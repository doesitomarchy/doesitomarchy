package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/status"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

const resultsUsage = `doiomad results — moderate test results (PLAN.md §21.5)

  results import [-accept] FILE      validate a result file (YAML or JSON; "-" reads stdin),
                                     store it as pending and preview its effect
  results list [-state S] [-config ID] [-limit N]
  results show ID                    a result in full, with its flags and history
  results accept ID                  make a pending result count
  results reject ID -reason TEXT     turn a pending result down
  results retract ID -reason TEXT    stop an accepted result counting (it stays on record)
  results flags [-all]               review flags (open ones by default)
  results resolve FLAG -note TEXT    record how a flag was dealt with

Every change is recorded with who made it ($SUDO_USER, else $USER). Nothing is
ever deleted. Running servers pick up accepts and retractions within seconds.
`

// actor names who is moderating: the admin behind the doiomad wrapper's
// sudo, or the local user.
func actor() string {
	for _, k := range []string{"SUDO_USER", "USER"} {
		if v := os.Getenv(k); v != "" && v != "root" && v != "doiomad" {
			return v
		}
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "unknown"
}

func cmdResults(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, resultsUsage)
		return 0
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("results "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	db, dataDir := dbFlags(fs)
	accept := fs.Bool("accept", false, "import: accept immediately (your own tests)")
	state := fs.String("state", "", "list: pending | accepted | rejected | retracted")
	config := fs.String("config", "", "list: only this config ID")
	limit := fs.Int("limit", 50, "list: at most this many")
	reason := fs.String("reason", "", "reject, retract: why")
	note := fs.String("note", "", "resolve: how the flag was dealt with")
	all := fs.Bool("all", false, "flags: include resolved flags")
	pos, ok := parseInterleaved(fs, rest)
	if !ok {
		return 2
	}
	ctx := context.Background()
	st, c, _, err := openSynced(ctx, *db, *dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "results: %v\n", err)
		return 1
	}
	defer st.Close()
	who := actor()

	id := func() (int64, bool) {
		if len(pos) != 1 {
			fmt.Fprintf(stderr, "results %s: give one ID\n", sub)
			return 0, false
		}
		n, err := strconv.ParseInt(strings.TrimPrefix(pos[0], "#"), 10, 64)
		if err != nil || n <= 0 {
			fmt.Fprintf(stderr, "results %s: %q is not an ID\n", sub, pos[0])
			return 0, false
		}
		return n, true
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "results %s: %v\n", sub, err)
		return 1
	}

	switch sub {
	case "import":
		if len(pos) != 1 {
			fmt.Fprintln(stderr, "results import: give one file (or - for stdin)")
			return 2
		}
		raw, err := readInput(pos[0])
		if err != nil {
			return fail(err)
		}
		f, err := results.Parse(raw)
		if err != nil {
			return fail(err)
		}
		r, err := results.Validate(f, c, time.Now())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		before, after, err := preview(ctx, st, c, r)
		if err != nil {
			return fail(err)
		}
		n, err := st.InsertResult(ctx, r, raw, results.SchemaV1, who)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "result %d · pending · %s · %s\n", n, r.Identifier, r.ConfigID)
		printEffect(stdout, before, after)
		for _, fl := range r.Flags {
			fmt.Fprintf(stdout, "  flag: %s: %s\n", fl.Kind, fl.Detail)
		}
		if *accept {
			if err := st.SetResultState(ctx, n, store.Accepted, "", who); err != nil {
				return fail(err)
			}
			fmt.Fprintf(stdout, "result %d accepted\n", n)
		} else {
			fmt.Fprintf(stdout, "accept with: doiomad results accept %d\n", n)
		}
		return 0

	case "list":
		list, err := st.ListResults(ctx, store.ResultFilter{State: *state, Config: *config, Limit: *limit})
		if err != nil {
			return fail(err)
		}
		if len(list) == 0 {
			fmt.Fprintln(stdout, "no results")
			return 0
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTATE\tMAC\tTESTED\tOMARCHY\t●\t◐\t✕\tFLAGS\tSOURCE\tTESTER")
		for _, x := range list {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%d\t%s\t%s\n", x.ID, x.State, x.Identifier, x.TestedOn, x.Omarchy,
				x.Supported, x.Partial, x.Failed, x.OpenFlags, x.SourceID, orAnon(x.TesterHandle))
		}
		tw.Flush()
		return 0

	case "show":
		n, ok := id()
		if !ok {
			return 2
		}
		d, err := st.Result(ctx, n)
		if err != nil {
			return fail(err)
		}
		printDetail(stdout, d)
		return 0

	case "accept", "reject", "retract":
		n, ok := id()
		if !ok {
			return 2
		}
		to := map[string]string{"accept": store.Accepted, "reject": store.Rejected, "retract": store.Retracted}[sub]
		if err := st.SetResultState(ctx, n, to, *reason, who); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "result %d %s\n", n, to)
		return 0

	case "flags":
		flags, err := st.Flags(ctx, !*all)
		if err != nil {
			return fail(err)
		}
		if len(flags) == 0 {
			fmt.Fprintln(stdout, "no flags")
			return 0
		}
		for _, fl := range flags {
			line := fmt.Sprintf("flag %d · result %d · %s: %s", fl.ID, fl.ResultID, fl.Kind, fl.Detail)
			if fl.ResolvedAt != "" {
				line += fmt.Sprintf(" · resolved by %s: %s", fl.ResolvedBy, fl.Resolution)
			}
			fmt.Fprintln(stdout, line)
		}
		return 0

	case "resolve":
		n, ok := id()
		if !ok {
			return 2
		}
		if err := st.ResolveFlag(ctx, n, *note, who); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "flag %d resolved\n", n)
		return 0
	}
	fmt.Fprintf(stderr, "results: unknown command %q\n\n%s", sub, resultsUsage)
	return 2
}

func cmdSources(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprintln(stderr, "usage: doiomad sources list")
		return 2
	}
	fs := flag.NewFlagSet("sources list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	db, dataDir := dbFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	ctx := context.Background()
	st, _, _, err := openSynced(ctx, *db, *dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "sources: %v\n", err)
		return 1
	}
	defer st.Close()
	list, err := st.Sources(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "sources: %v\n", err)
		return 1
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTRUST\tCREATED\tREVOKED")
	for _, x := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", x.ID, x.Name, x.Trust, x.CreatedAt, x.RevokedAt)
	}
	tw.Flush()
	return 0
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(io.LimitReader(os.Stdin, results.MaxSize+1))
	}
	return os.ReadFile(path)
}

func orAnon(h string) string {
	if h == "" {
		return "anonymous"
	}
	return "@" + h
}

// preview computes a config's status now, and as it would be if this result
// were accepted, so a maintainer sees the effect before it counts.
func preview(ctx context.Context, st *store.Store, c *catalog.Catalog, r *results.Result) (before, after status.ConfigStatus, err error) {
	ru, err := st.RollupData(ctx)
	if err != nil {
		return before, after, err
	}
	var m *catalog.Mac
	var cfg *catalog.Config
	var excl string
	for _, mm := range c.Macs {
		for ri := range mm.Releases {
			for ci := range mm.Releases[ri].Configs {
				if x := &mm.Releases[ri].Configs[ci]; x.ID == r.ConfigID {
					m, cfg, excl = mm, x, c.CoverageExclusion(mm, &mm.Releases[ri])
				}
			}
		}
	}
	if cfg == nil {
		return before, after, fmt.Errorf("config %s vanished from the catalog", r.ConfigID)
	}
	cats := map[string]catalog.Category{}
	for _, k := range c.Categories {
		cats[k.ID] = k
	}
	var caps []status.Capability
	for _, cp := range c.Applicable(m, cfg) {
		k := cats[cp.Category()]
		caps = append(caps, status.Capability{ID: cp.ID, Label: k.Name + " → " + cp.Name, Blocking: k.Blocking})
	}
	in := status.ConfigInput{HardBlocker: m.HardBlocker, Excluded: excl, Caps: caps, Items: ru.Items[cfg.ID],
		Unsupported: ru.Unsupported[cfg.ID], Results: ru.Results[cfg.ID], LatestResult: ru.Latest[cfg.ID], CurrentMajor: ru.CurrentMajor}
	before = status.Config(in)
	verdicts := map[string]status.Verdict{"supported": status.Supported, "partial": status.Partial, "failed": status.Failed}
	items := append([]status.Item(nil), in.Items...)
	for _, it := range r.Items {
		if it.Applicable && it.Status != "not_tested" {
			items = append(items, status.Item{Capability: it.Capability, Verdict: verdicts[it.Status], Method: it.Method,
				Omarchy: r.Omarchy, TestedOn: r.TestedOn, ResultID: 1 << 62, Evidence: it.Evidence})
		}
	}
	in.Items, in.Results = items, in.Results+1
	if r.TestedOn > in.LatestResult {
		in.LatestResult = r.TestedOn
	}
	after = status.Config(in)
	return before, after, nil
}

func printEffect(w io.Writer, before, after status.ConfigStatus) {
	desc := func(s status.ConfigStatus) string {
		out := fmt.Sprintf("%s (%d/%d tested", s.Verdict.Label(), s.Tested, s.Applicable)
		if s.Verified() {
			out += ", verified"
		}
		out += ")"
		if s.Blocker != "" {
			out += ", blocked by " + s.Blocker
		}
		return out
	}
	if desc(before) == desc(after) {
		fmt.Fprintf(w, "  if accepted: no change: %s\n", desc(after))
		return
	}
	fmt.Fprintf(w, "  if accepted: %s\n             → %s\n", desc(before), desc(after))
	if after.Conflicts > before.Conflicts {
		fmt.Fprintf(w, "  note: this result would create %d conflicting report(s) on the same Omarchy version\n", after.Conflicts-before.Conflicts)
	}
}

func printDetail(w io.Writer, d *store.ResultDetail) {
	fmt.Fprintf(w, "result %d · %s", d.ID, d.State)
	if d.StateReason != "" {
		fmt.Fprintf(w, " (%s)", d.StateReason)
	}
	fmt.Fprintf(w, "\n  %s · %s\n", d.Identifier, d.ConfigID)
	fmt.Fprintf(w, "  tested %s · Omarchy %s", d.TestedOn, d.Omarchy)
	if d.Kernel != "" {
		fmt.Fprintf(w, " · kernel %s", d.Kernel)
	}
	fmt.Fprintf(w, "\n  by %s via %s", orAnon(d.TesterHandle), strings.TrimSpace(d.SourceName+" "+d.SourceVersion))
	if d.Profile != "" {
		fmt.Fprintf(w, " · %s profile", d.Profile)
	}
	fmt.Fprintf(w, "\n  ● %d  ◐ %d  ✕ %d  · %d not tested · raw report %d bytes (%s)\n", d.Supported, d.Partial, d.Failed,
		d.NotTested, d.ReportSize, d.ReportVisibility)
	if d.Notes != "" {
		fmt.Fprintf(w, "  notes: %s\n", strings.ReplaceAll(d.Notes, "\n", "\n         "))
	}
	cat := ""
	for _, it := range d.Items {
		if it.CategoryName != cat {
			cat = it.CategoryName
			fmt.Fprintf(w, "  %s\n", cat)
		}
		line := fmt.Sprintf("    %-11s %s", it.Status, it.CapabilityName)
		if it.Method != "" {
			line += " · " + it.Method
		}
		if it.Reason != "" {
			line += " · " + it.Reason
		}
		if !it.Applicable {
			line += " · NOT COUNTED (doesn't apply)"
		}
		fmt.Fprintln(w, line)
		for _, s := range []string{it.Evidence, it.Note} {
			if s != "" {
				fmt.Fprintf(w, "                %s\n", s)
			}
		}
	}
	for _, x := range d.Extras {
		fmt.Fprintf(w, "  extra %s · %s · %s %s\n", x.ID, x.Label, x.Status, x.Detail)
	}
	for _, fl := range d.Flags {
		state := "open"
		if fl.ResolvedAt != "" {
			state = "resolved: " + fl.Resolution
		}
		fmt.Fprintf(w, "  flag %d · %s: %s · %s\n", fl.ID, fl.Kind, fl.Detail, state)
	}
	for _, e := range d.Events {
		fmt.Fprintf(w, "  %s  %-14s %s %s\n", e.At, e.Action, e.Actor, e.Detail)
	}
}

// parseInterleaved parses flags wherever they appear, so both
// "retract 7 -reason x" and "retract -reason x 7" work. It returns the
// positional arguments in order.
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, bool) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, false
		}
		if fs.NArg() == 0 {
			return pos, true
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
