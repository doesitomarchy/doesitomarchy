// Command doioma is the DoesItOmarchy.com server and admin tool.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

const usage = `doioma — DoesItOmarchy.com server and admin tool

Usage:
  doioma <command> [flags]

Commands:
  validate   Check the catalog in data/ (-data DIR)
  version    Print the version
  help       Show this help

Planned: serve, sync, results, stats
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch cmd, rest := args[0], args[1:]; cmd {
	case "validate":
		return cmdValidate(rest, stdout, stderr)
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "doioma: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func cmdValidate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("data", "data", "catalog directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	s, err := catalog.Load(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "catalog invalid:\n%v\n", err)
		return 1
	}
	caps := "none yet"
	if s.Capabilities > 0 {
		caps = "present"
	}
	fmt.Fprintf(stdout, "catalog ok: %d mac files, %d component files, capabilities: %s\n",
		s.Macs, s.Components, caps)
	return 0
}
