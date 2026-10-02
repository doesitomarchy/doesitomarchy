package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/match"
)

const sharesUsage = `doiomad shares — hardware IDs shared from Identify my Mac (PLAN.md §23.3)

  shares list [-all]           groups with new shares (-all: reviewed ones too)
  shares review IDENTIFIER     mark a group's new shares reviewed ("none": the
                               group with no identifier)

IDs the catalog doesn't have are marked "*". A review is recorded with who
made it ($SUDO_USER, else $USER). /admin/shares shows the same.
`

func cmdShares(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, sharesUsage)
		return 0
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("shares "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	db, dataDir := dbFlags(fs)
	all := fs.Bool("all", false, "list: include reviewed groups")
	pos, ok := parseInterleaved(fs, rest)
	if !ok {
		return 2
	}
	want := map[string]int{"list": 0, "review": 1}
	if n, known := want[sub]; !known || len(pos) != n {
		fmt.Fprintf(stderr, "shares %s: wrong arguments\n\n%s", sub, sharesUsage)
		return 2
	}
	ctx := context.Background()
	st, c, _, err := openSynced(ctx, *db, *dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "shares: %v\n", err)
		return 1
	}
	defer st.Close()

	if sub == "review" {
		product := pos[0]
		if product == "none" {
			product = ""
		}
		n, err := st.ReviewShares(ctx, product, actor())
		if err != nil {
			fmt.Fprintf(stderr, "shares review: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%s: %d marked reviewed\n", pos[0], n)
		return 0
	}

	groups, err := st.ShareGroups(ctx, *all)
	if err != nil {
		fmt.Fprintf(stderr, "shares list: %v\n", err)
		return 1
	}
	if len(groups) == 0 {
		fmt.Fprintln(stdout, "No new shares.")
		return 0
	}
	m := match.New(c)
	mark := func(known bool) string {
		if known {
			return ""
		}
		return "*"
	}
	for _, g := range groups {
		name := g.Product
		if name == "" {
			name = "(no identifier)"
		} else if id, _ := m.Identify(match.Probe{ProductName: g.Product}); id == "" {
			name += "*"
		}
		fmt.Fprintf(stdout, "%s: %d shares, %d new\n", name, len(g.Shares), g.Unseen)
		for _, sh := range g.Shares {
			var pci []string
			for _, id := range sh.PCI {
				pci = append(pci, id+mark(m.KnownDevice(id, "pci")))
			}
			line := []string{sh.SharedOn}
			if sh.BoardID != "" {
				line = append(line, sh.BoardID+mark(m.KnownBoard(sh.BoardID)))
			}
			if sh.CPU != "" {
				line = append(line, strings.ReplaceAll(sh.CPU, " ", "_")+mark(m.KnownCPU(sh.CPU)))
			}
			if len(pci) > 0 {
				line = append(line, "pci="+strings.Join(pci, ","))
			}
			if sh.Modified != "" {
				line = append(line, "modified="+sh.Modified)
			}
			if sh.Release != "" {
				line = append(line, "release="+sh.Release)
			}
			if sh.ReviewedAt != "" {
				line = append(line, "reviewed")
			}
			fmt.Fprintf(stdout, "  %s\n", strings.Join(line, "  "))
		}
	}
	return 0
}
