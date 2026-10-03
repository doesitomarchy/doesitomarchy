package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/fixes"
	"github.com/doesitomarchy/doesitomarchy/internal/web"
)

const fixesUsage = `doiomad fixes — fix issues in the fix repo (PLAN.md §26)

  fixes list                                   every fix issue the site knows, with its state
  fixes open CRITERION (-component ID | -config ID)
                                               open a fix issue for a failing criterion
  fixes sync                                   refresh every fix issue from GitHub now

open and sync need GITHUB_TOKEN (fine-grained, Issues read/write on the fix
repo); FIX_REPO overrides the repo (default ` + fixes.DefaultRepo + `).
`

const unsupportedUsage = `doiomad unsupported — the maintainer's white flag (PLAN.md §20.1, §26)

  unsupported list [-all]                      current flags (-all: lifted ones too)
  unsupported set CRITERION (-component ID | -config ID) -reason TEXT
                                               give up on a criterion; with GITHUB_TOKEN,
                                               its open fix issue closes as not planned
  unsupported clear ID                         lift a flag (it stays on record)
`

func ghClient() *fixes.Client {
	if os.Getenv("GITHUB_TOKEN") == "" {
		return nil
	}
	c := fixes.NewClient(os.Getenv("FIX_REPO"), os.Getenv("GITHUB_TOKEN"))
	if api := os.Getenv("GITHUB_API_URL"); api != "" { // tests
		c.Base = api
	}
	return c
}

func cmdFixes(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, fixesUsage)
		return 0
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("fixes "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	db, dataDir := dbFlags(fs)
	component := fs.String("component", "", "open: the component the fix covers")
	config := fs.String("config", "", "open: the one configuration the fix covers")
	pos, ok := parseInterleaved(fs, rest)
	if !ok {
		return 2
	}
	if want := map[string]int{"list": 0, "open": 1, "sync": 0}; want[sub] != len(pos) || (sub != "list" && sub != "open" && sub != "sync") {
		fmt.Fprintf(stderr, "fixes %s: wrong arguments\n\n%s", sub, fixesUsage)
		return 2
	}
	ctx := context.Background()
	st, c, _, err := openSynced(ctx, *db, *dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "fixes: %v\n", err)
		return 1
	}
	defer st.Close()
	fail := func(err error) int {
		fmt.Fprintf(stderr, "fixes %s: %v\n", sub, err)
		return 1
	}
	switch sub {
	case "list":
		all, err := st.Fixes(ctx)
		if err != nil {
			return fail(err)
		}
		if len(all) == 0 {
			fmt.Fprintln(stdout, "no fix issues")
			return 0
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ISSUE\tSTATE\tCRITERION\tON\tASSIGNEE\tLAST ACTIVITY")
		for _, f := range all {
			on := f.Component
			if on == "" {
				on = f.Config
			}
			fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\t%s\t%s\n", f.Issue, fixes.StateOf(f, time.Now()), f.Capability, on, orDash(f.Assignee), utcShort(f.LastActivity))
		}
		tw.Flush()
		return 0
	case "sync":
		gh := ghClient()
		if gh == nil {
			return fail(fmt.Errorf("GITHUB_TOKEN isn't set"))
		}
		n, err := (&fixes.Syncer{Client: gh, Store: st}).SyncAll(ctx)
		fmt.Fprintf(stdout, "%d fix issues updated from GitHub\n", n)
		if err != nil {
			return fail(err)
		}
		return 0
	}
	// open
	gh := ghClient()
	if gh == nil {
		return fail(fmt.Errorf("GITHUB_TOKEN isn't set"))
	}
	var cp catalog.Capability
	for _, x := range c.Capabilities {
		if x.ID == pos[0] {
			cp = x
		}
	}
	if cp.ID == "" {
		return fail(fmt.Errorf("unknown criterion %q", pos[0]))
	}
	if (*component == "") == (*config == "") {
		return fail(fmt.Errorf("give -component ID or -config ID"))
	}
	ru, err := st.RollupData(ctx)
	if err != nil {
		return fail(err)
	}
	name := fixes.ScopeName(c, *component, *config)
	f, err := fixes.OpenIssue(ctx, gh, st, cp.ID, cp.Name, fixes.Scope{Component: *component, Config: *config, Name: name},
		fixes.AffectedConfigs(c, ru, cp.ID, *component, *config, web.BaseURL), actor())
	var open *fixes.AlreadyOpenError
	if errors.As(err, &open) {
		if _, serr := (&fixes.Syncer{Client: gh, Store: st}).SyncIssue(ctx, open.Issue.Number); serr != nil {
			fmt.Fprintf(stderr, "fixes open: reading issue #%d: %v\n", open.Issue.Number, serr)
		}
	}
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "opened issue #%d: %s\n%s\n", f.Issue, f.Title, f.URL)
	return 0
}

func cmdUnsupported(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, unsupportedUsage)
		return 0
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("unsupported "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	db, dataDir := dbFlags(fs)
	component := fs.String("component", "", "set: give up on every configuration with this component")
	config := fs.String("config", "", "set: give up on this configuration")
	reason := fs.String("reason", "", "set: why (shown on the site)")
	all := fs.Bool("all", false, "list: include lifted flags")
	pos, ok := parseInterleaved(fs, rest)
	if !ok {
		return 2
	}
	if want, known := map[string]int{"list": 0, "set": 1, "clear": 1}[sub]; !known || want != len(pos) {
		fmt.Fprintf(stderr, "unsupported %s: wrong arguments\n\n%s", sub, unsupportedUsage)
		return 2
	}
	ctx := context.Background()
	st, _, _, err := openSynced(ctx, *db, *dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "unsupported: %v\n", err)
		return 1
	}
	defer st.Close()
	fail := func(err error) int {
		fmt.Fprintf(stderr, "unsupported %s: %v\n", sub, err)
		return 1
	}
	who := actor()
	switch sub {
	case "list":
		us, err := st.UnsupportedList(ctx, *all)
		if err != nil {
			return fail(err)
		}
		if len(us) == 0 {
			fmt.Fprintln(stdout, "no Unsupported flags")
			return 0
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tCRITERION\tON\tREASON\tSET BY\tLIFTED")
		for _, u := range us {
			on := u.Component
			if on == "" {
				on = u.Config
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s %s\t%s\n", u.ID, u.Capability, on, u.Reason, u.SetBy, utcShort(u.SetAt), orDash(u.ClearedAt))
		}
		tw.Flush()
		return 0
	case "clear":
		id, err := strconv.ParseInt(pos[0], 10, 64)
		if err != nil {
			return fail(fmt.Errorf("%q is not a flag ID (see unsupported list)", pos[0]))
		}
		if err := st.ClearUnsupported(ctx, id, who); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Unsupported flag %d lifted\n", id)
		return 0
	}
	if err := st.SetUnsupported(ctx, pos[0], *config, *component, *reason, who); err != nil {
		return fail(err)
	}
	fmt.Fprintf(stdout, "%s marked Unsupported\n", pos[0])
	if gh := ghClient(); gh != nil {
		closed, err := fixes.GiveUpAll(ctx, &fixes.Syncer{Client: gh, Store: st}, pos[0], *component, *config, *reason, who)
		for _, n := range closed {
			fmt.Fprintf(stdout, "issue #%d closed as not planned, with the reason\n", n)
		}
		if err != nil {
			return fail(err)
		}
	}
	return 0
}
