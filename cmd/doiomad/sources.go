package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

const sourcesUsage = `doiomad sources — the test tools that submit reports (PLAN.md §22.1)

  sources list
  sources add ID -name NAME [-homepage URL]   register a tool and print its key
  sources rotate ID                           replace a tool's key (the old one stops working)
  sources revoke ID                           stop a tool submitting
  sources trust ID pending|trusted            how far its reports are trusted

ID is 2–32 letters, digits and "-", starting with a letter; it's stored in
lower case. A key is printed once and only
its hash is kept: send it to the tool's author privately. "manual" (reports
imported with the CLI) never has a key.
`

func cmdSources(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, sourcesUsage)
		return 0
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("sources "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	db, dataDir := dbFlags(fs)
	name := fs.String("name", "", "add: the tool's name")
	homepage := fs.String("homepage", "", "add: the tool's homepage")
	pos, ok := parseInterleaved(fs, rest)
	if !ok {
		return 2
	}
	want := map[string]int{"list": 0, "add": 1, "rotate": 1, "revoke": 1, "trust": 2}
	n, known := want[sub]
	if !known {
		fmt.Fprintf(stderr, "sources: unknown command %q\n\n%s", sub, sourcesUsage)
		return 2
	}
	if len(pos) != n {
		fmt.Fprintf(stderr, "sources %s: wrong arguments\n\n%s", sub, sourcesUsage)
		return 2
	}
	if n > 0 {
		// IDs are lower case; say so when the one given wasn't.
		if id := store.NormalizeSourceID(pos[0]); id != pos[0] {
			fmt.Fprintf(stdout, "source ID %q → %q (IDs are lower case)\n", pos[0], id)
			pos[0] = id
		}
	}
	ctx := context.Background()
	st, _, _, err := openSynced(ctx, *db, *dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "sources: %v\n", err)
		return 1
	}
	defer st.Close()
	fail := func(err error) int {
		fmt.Fprintf(stderr, "sources %s: %v\n", sub, err)
		return 1
	}
	printKey := func(id, key string) {
		fmt.Fprintf(stdout, "source %s key (shown once; send it privately):\n%s\n", id, key)
	}

	switch sub {
	case "list":
		list, err := st.Sources(ctx)
		if err != nil {
			return fail(err)
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tTRUST\tCREATED\tREVOKED\tHOMEPAGE")
		for _, x := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", x.ID, x.Name, x.Trust, utcShort(x.CreatedAt), utcShort(x.RevokedAt), x.Homepage)
		}
		tw.Flush()
	case "add":
		if *name == "" {
			fmt.Fprintln(stderr, "sources add: give -name")
			return 2
		}
		key, err := st.AddSource(ctx, pos[0], *name, *homepage)
		if err != nil {
			return fail(err)
		}
		printKey(pos[0], key)
	case "rotate":
		key, err := st.RotateSourceKey(ctx, pos[0])
		if err != nil {
			return fail(err)
		}
		printKey(pos[0], key)
	case "revoke":
		if err := st.RevokeSource(ctx, pos[0]); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "source %s revoked\n", pos[0])
	case "trust":
		if err := st.SetSourceTrust(ctx, pos[0], pos[1]); err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "source %s is %s\n", pos[0], pos[1])
	}
	return 0
}

const maintainersUsage = `doiomad maintainers — who may review reports in /admin (PLAN.md §22.5)

  maintainers list
  maintainers add HANDLE EMAIL    the address they sign in to Cloudflare Access with
  maintainers remove HANDLE

Their handle is recorded with every decision they make. The address must also be
allowed by the Access application (ADMIN_EMAILS in deploy/cloudflare.sh).
`

func cmdMaintainers(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, maintainersUsage)
		return 0
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("maintainers "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	db, dataDir := dbFlags(fs)
	pos, ok := parseInterleaved(fs, rest)
	if !ok {
		return 2
	}
	n, known := map[string]int{"list": 0, "add": 2, "remove": 1}[sub]
	if !known || len(pos) != n {
		fmt.Fprintf(stderr, "maintainers: wrong command or arguments\n\n%s", maintainersUsage)
		return 2
	}
	ctx := context.Background()
	st, _, _, err := openSynced(ctx, *db, *dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "maintainers: %v\n", err)
		return 1
	}
	defer st.Close()
	switch sub {
	case "list":
		list, err := st.Maintainers(ctx)
		if err != nil {
			break
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "HANDLE\tEMAIL\tADDED")
		for _, m := range list {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", m.Handle, m.Email, utcShort(m.AddedAt))
		}
		tw.Flush()
	case "add":
		if err = st.AddMaintainer(ctx, pos[0], pos[1]); err == nil {
			fmt.Fprintf(stdout, "maintainer %s added (%s)\n", pos[0], pos[1])
		}
	case "remove":
		if err = st.RemoveMaintainer(ctx, pos[0]); err == nil {
			fmt.Fprintf(stdout, "maintainer %s removed\n", pos[0])
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "maintainers %s: %v\n", sub, err)
		return 1
	}
	return 0
}
