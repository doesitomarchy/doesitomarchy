// Command doiomad is the DoesItOmarchy.com server and admin tool.
package main

import (
	"flag"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/report"
	"github.com/doesitomarchy/doesitomarchy/internal/search"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

const usage = `doiomad — DoesItOmarchy.com server and admin tool

Usage:
  doiomad <command> [flags]

Commands:
  serve      Run the web server (-addr :8080, -db FILE, -data DIR)
  sync       Migrate the database and load the catalog into it (-db FILE, -data DIR)
  validate   Check the catalog in data/ (-data DIR)
  lock       Record new config IDs in data/config-ids.lock (-data DIR)
  report     Write an HTML review page for a batch (-line mac-mini -o FILE [-intro FILE])
  reports    Moderate diagnostic reports: import, list, show, accept, reject, retract,
             flags (doiomad reports help)
  sources    List the apps registered to submit reports
  version    Print the version
  help       Show this help

-db defaults to $DOIOMAD_DB, else ./doesitomarchy.db. serve and sync use the catalog
built into the binary unless -data DIR is given.
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
	case "serve":
		return cmdServe(rest, stdout, stderr)
	case "sync":
		return cmdSync(rest, stdout, stderr)
	case "validate":
		return cmdValidate(rest, stdout, stderr)
	case "lock":
		return cmdLock(rest, stdout, stderr)
	case "report":
		return cmdReport(rest, stdout, stderr)
	case "reports", "results":
		return cmdResults(rest, stdout, stderr)
	case "sources":
		return cmdSources(rest, stdout, stderr)
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "doiomad: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func dataFlag(name string, args []string, stderr io.Writer) (string, bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("data", "data", "catalog directory")
	if err := fs.Parse(args); err != nil {
		return "", false
	}
	return *dir, true
}

func cmdValidate(args []string, stdout, stderr io.Writer) int {
	dir, ok := dataFlag("validate", args, stderr)
	if !ok {
		return 2
	}
	c, err := catalog.Load(dir)
	if err != nil {
		fmt.Fprintf(stderr, "catalog invalid:\n%v\n", err)
		return 1
	}
	if problems := search.ValidateAliases(c); len(problems) > 0 {
		fmt.Fprintf(stderr, "catalog invalid:\n%s\n", strings.Join(problems, "\n"))
		return 1
	}
	s := c.Stats()
	fmt.Fprintf(stdout, "catalog ok: %d macs, %d releases, %d configs, %d components, %d capabilities, %d uncertain fields\n",
		s.Macs, s.Releases, s.Configs, s.Components, s.Capabilities, s.Uncertain)
	return 0
}

// cmdLock appends new config IDs to the lock file. The catalog must otherwise
// be valid, so malformed IDs never get locked in.
func cmdLock(args []string, stdout, stderr io.Writer) int {
	dir, ok := dataFlag("lock", args, stderr)
	if !ok {
		return 2
	}
	c, err := catalog.LoadUnlocked(dir)
	if err != nil {
		fmt.Fprintf(stderr, "catalog invalid; fix these first:\n%v\n", err)
		return 1
	}
	ids, _ := c.ConfigIDs()
	added, err := catalog.WriteLock(filepath.Join(dir, catalog.LockFile), ids)
	if err != nil {
		fmt.Fprintf(stderr, "lock: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "locked %d new config IDs (%d total)\n", len(added), len(ids))
	for _, id := range added {
		fmt.Fprintf(stdout, "  + %s\n", id)
	}
	return 0
}

func cmdReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("data", "data", "catalog directory")
	line := fs.String("line", "", "product line key from vocabulary.yaml (default: all)")
	out := fs.String("o", "", "output HTML file (required)")
	intro := fs.String("intro", "", "optional HTML fragment with reviewer notes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" {
		fmt.Fprintln(stderr, "report: -o FILE is required")
		return 2
	}
	c, err := catalog.Load(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "catalog invalid:\n%v\n", err)
		return 1
	}
	opt := report.Options{Line: *line, Now: time.Now()}
	if *intro != "" {
		b, err := os.ReadFile(*intro)
		if err != nil {
			fmt.Fprintf(stderr, "report: %v\n", err)
			return 1
		}
		opt.Intro = template.HTML(b) // trusted: written by the maintainer running the command
	}
	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintf(stderr, "report: %v\n", err)
		return 1
	}
	defer f.Close()
	if err := report.Render(f, c, opt); err != nil {
		fmt.Fprintf(stderr, "report: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s\n", *out)
	return 0
}
