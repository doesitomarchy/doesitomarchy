package report

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/doesitomarchy/doesitomarchy/internal/status"
)

// HandleRe is what a tester handle may look like.
var HandleRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,38}$`)

// PortPathRe is a ports entry's path: usb1:, usb2: or usb3: (a root hub of
// 12 Mb/s or slower, 480 Mb/s, or 5 Gb/s or faster: an xHCI controller has
// a USB 2 and a SuperSpeed root hub with the same port numbers), the host
// controller's PCI address and the root hub's port chain, stable across
// boots unlike bus numbers; pci: an SD reader on PCI; tb: a Thunderbolt route.
var PortPathRe = regexp.MustCompile(`^(usb[123]:[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]/[0-9]+(\.[0-9]+)*|pci:[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]|tb:[0-9]+-[0-9a-f-]+)$`)

// ConnectorRe is a connector ID in a port layout: <side>-<n>.
var ConnectorRe = regexp.MustCompile(`^(left|right|back|front|top)-([1-9][0-9]?)$`)

// Text limits, in bytes.
const (
	MaxNotes    = 10000
	MaxEvidence = 4000
	MaxNote     = 1000
	MaxShort    = 200 // one-line fields; longer ones are cut, not rejected
)

// Limits on the lists a report carries.
const (
	maxFixes    = 20
	maxParts    = 10
	maxPartIDs  = 10
	maxFixIDLen = 100
	maxPorts    = 32
	maxPaths    = 4
	maxPathLen  = 80
	maxConsole  = 64
	maxCommand  = 200 // characters
)

// Check runs every check that needs no catalog: the schema, the tester's
// handle, the test date (now bounds it: no future dates), the Omarchy
// version, text sizes, each item's status, method and reason, extras, and
// the context, fixes, replaced parts, ports and console. It returns Errors listing every
// problem, or nil. The server runs it first, then checks the report against
// the catalog (the configuration, criteria, connectors, live limits and
// fix IDs; a ports entry's connector).
func Check(f *File, now time.Time) error {
	var errs Errors
	bad := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if f.Schema != SchemaV1 {
		bad("schema: %q is not supported (want %q)", f.Schema, SchemaV1)
	}
	if h := strings.TrimSpace(f.Tester.Handle); h != "" && !HandleRe.MatchString(h) {
		bad("tester.handle: %q must be 1–39 letters, digits, '.', '_' or '-'", h)
	}

	// When the test ran: a full timestamp with a time zone.
	if d, err := time.Parse(time.RFC3339, strings.TrimSpace(f.TestedAt)); err != nil {
		bad("tested_at: %q must be a timestamp with a time zone, e.g. 2026-09-30T14:05:00Z or 2026-09-30T10:05:00-04:00", f.TestedAt)
	} else if d.After(now.Add(15 * time.Minute)) {
		bad("tested_at: %s is in the future", f.TestedAt)
	} else if d.UTC().Year() < 2025 {
		bad("tested_at: %s is before Omarchy existed", f.TestedAt)
	}

	if v, err := status.ParseVersion(f.Omarchy.Version); err != nil {
		bad("omarchy.version: %v", err)
	} else if _, err := status.ValidChannel(v, strings.ToLower(strings.TrimSpace(f.Omarchy.Channel))); err != nil {
		bad("omarchy.%v", err)
	}

	if len(f.Notes) > MaxNotes {
		bad("notes: %d bytes; the limit is %d", len(f.Notes), MaxNotes)
	}

	ctx := strings.TrimSpace(f.Context)
	if ctx != "" && !oneOf(ctx, Contexts) {
		bad("context: %q must be one of %s", f.Context, strings.Join(Contexts, ", "))
	}
	live := ctx == ContextLive

	if len(f.Items) == 0 && len(f.Extras) == 0 {
		bad("items: nothing to record (no items and no extras)")
	}
	keys := make([]string, 0, len(f.Items))
	for k := range f.Items {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fi := f.Items[key]
		st, method, reason := strings.TrimSpace(fi.Status), strings.TrimSpace(fi.Method), strings.TrimSpace(fi.Reason)
		switch {
		case !oneOf(st, Statuses):
			bad("items.%s.status: %q must be one of %s", key, fi.Status, strings.Join(Statuses, ", "))
		case st == "not_tested":
			if method != "" && !oneOf(method, Methods) {
				bad("items.%s.method: %q must be one of %s", key, fi.Method, strings.Join(Methods, ", "))
			}
			switch {
			case reason != "" && !oneOf(reason, Reasons):
				bad("items.%s.reason: %q must be one of %s", key, fi.Reason, strings.Join(Reasons, ", "))
			case reason == ReasonLiveLimit && !live:
				bad("items.%s.reason: %s is only for live reports (context: live)", key, ReasonLiveLimit)
			}
		default:
			if !oneOf(method, Methods) {
				bad("items.%s.method: %q must be one of %s (required unless not_tested)", key, fi.Method, strings.Join(Methods, ", "))
			}
			if reason != "" {
				bad("items.%s.reason: only not_tested items take a reason", key)
			}
		}
		if len(fi.Note) > MaxNote {
			bad("items.%s.note: %d bytes; the limit is %d", key, len(fi.Note), MaxNote)
		}
		if len(fi.Evidence) > MaxEvidence {
			bad("items.%s.evidence: %d bytes; the limit is %d", key, len(fi.Evidence), MaxEvidence)
		}
	}

	seen := map[string]bool{}
	for i, x := range f.Extras {
		id := strings.TrimSpace(x.ID)
		switch {
		case id == "":
			bad("extras[%d].id: required", i)
		case seen[id]:
			bad("extras[%d].id: %q appears twice", i, id)
		case len(x.Detail) > MaxEvidence:
			bad("extras[%d].detail: %d bytes; the limit is %d", i, len(x.Detail), MaxEvidence)
		}
		seen[id] = true
	}

	if len(f.Fixes) > maxFixes {
		bad("fixes: %d fixes; the limit is %d", len(f.Fixes), maxFixes)
	}
	seen = map[string]bool{}
	for i, id := range f.Fixes {
		id = strings.TrimSpace(id)
		switch {
		case id == "":
			bad("fixes[%d]: empty", i)
		case len(id) > maxFixIDLen:
			bad("fixes[%d]: %d bytes; the limit is %d", i, len(id), maxFixIDLen)
		case seen[id]:
			bad("fixes[%d]: %q appears twice", i, id)
		}
		seen[id] = true
	}

	if len(f.ReplacedParts) > maxParts {
		bad("replaced_parts: %d parts; the limit is %d", len(f.ReplacedParts), maxParts)
	}
	for i, p := range f.ReplacedParts {
		if k := strings.TrimSpace(p.Kind); !oneOf(k, PartKinds) {
			bad("replaced_parts[%d].kind: %q must be one of %s", i, p.Kind, strings.Join(PartKinds, ", "))
		}
		if len(p.Detail) > MaxShort {
			bad("replaced_parts[%d].detail: %d bytes; the limit is %d", i, len(p.Detail), MaxShort)
		}
		if len(p.IDs) > maxPartIDs {
			bad("replaced_parts[%d].ids: %d IDs; the limit is %d", i, len(p.IDs), maxPartIDs)
		}
		for j, id := range p.IDs {
			if id = strings.TrimSpace(id); id == "" || len(id) > MaxShort {
				bad("replaced_parts[%d].ids[%d]: must be 1–%d bytes", i, j, MaxShort)
			}
		}
	}

	checkPorts(f.Ports, bad)
	checkConsole(f.Console, bad)

	if len(f.ConsentNotice) > MaxNote {
		bad("consent_notice: %d bytes; the limit is %d", len(f.ConsentNotice), MaxNote)
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// checkPorts checks the ports entries' shape; the server then checks each
// connector against the configuration's layout.
func checkPorts(ports []PortAnswer, bad func(string, ...any)) {
	if len(ports) > maxPorts {
		bad("ports: %d entries; the limit is %d", len(ports), maxPorts)
	}
	seen := map[string]bool{}
	for i, p := range ports {
		test, conn, how, said := strings.TrimSpace(p.Test), strings.TrimSpace(p.Connector), strings.TrimSpace(p.How), strings.TrimSpace(p.WiringSaid)
		if !oneOf(test, PortTests) {
			bad("ports[%d].test: %q must be one of %s", i, p.Test, strings.Join(PortTests, ", "))
		}
		if !ConnectorRe.MatchString(conn) {
			bad("ports[%d].connector: %q must be a connector ID such as left-2", i, p.Connector)
		} else if key := test + "@" + conn; seen[key] {
			bad("ports[%d]: %s on %s appears twice", i, test, conn)
		} else {
			seen[key] = true
		}
		if n := len(p.Paths); n == 0 || n > maxPaths {
			bad("ports[%d].paths: %d paths; send 1–%d", i, n, maxPaths)
		}
		paths := map[string]bool{}
		for j, path := range p.Paths {
			switch {
			case len(path) > maxPathLen:
				bad("ports[%d].paths[%d]: %d bytes; the limit is %d", i, j, len(path), maxPathLen)
			case !PortPathRe.MatchString(path):
				bad("ports[%d].paths[%d]: %q must be usb1:, usb2: or usb3: with the host controller's PCI address and a port chain (usb3:0000:00:14.0/1), pci: and an address (pci:0000:03:00.0), or tb: and a route (tb:0-1)", i, j, path)
			case paths[path]:
				bad("ports[%d].paths[%d]: %q appears twice", i, j, path)
			}
			paths[path] = true
		}
		switch {
		case !oneOf(how, PortHows):
			bad("ports[%d].how: %q must be one of %s", i, p.How, strings.Join(PortHows, ", "))
		case how == PortCorrected && !ConnectorRe.MatchString(said):
			bad("ports[%d].wiring_said: %q must be the connector the wiring gave (how: corrected), such as left-2", i, p.WiringSaid)
		case how == PortCorrected && said == conn:
			bad("ports[%d].wiring_said: %s is the connector itself; a corrected entry names another", i, said)
		case how != PortCorrected && said != "":
			bad("ports[%d].wiring_said: only corrected entries take it", i)
		}
	}
}

// checkConsole checks the console entries: a command as typed, one line of
// printable text, and its kind.
func checkConsole(cmds []ConsoleEntry, bad func(string, ...any)) {
	if len(cmds) > maxConsole {
		bad("console: %d entries; the limit is %d", len(cmds), maxConsole)
	}
	for i, c := range cmds {
		switch cmd := strings.TrimSpace(c.Command); {
		case cmd == "":
			bad("console[%d].command: empty", i)
		case utf8.RuneCountInString(c.Command) > maxCommand:
			bad("console[%d].command: %d characters; the limit is %d", i, utf8.RuneCountInString(c.Command), maxCommand)
		case !printable(c.Command):
			bad("console[%d].command: %q must be one line of printable text", i, c.Command)
		}
		if k := strings.TrimSpace(c.Kind); !oneOf(k, ConsoleKinds) {
			bad("console[%d].kind: %q must be one of %s (read-only commands aren't recorded)", i, c.Kind, strings.Join(ConsoleKinds, ", "))
		}
	}
}

func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}
