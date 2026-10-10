package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
)

const unsupportedUsage = `doiomad unsupported — the maintainer's white flag (PLAN.md §20.1)

  unsupported list [-all]                      current flags (-all: lifted ones too)
  unsupported set CRITERION (-component ID | -config ID) -reason TEXT
                                               give up on a criterion
  unsupported clear ID                         lift a flag (it stays on record)
`

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
	return 0
}
