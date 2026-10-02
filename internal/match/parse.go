package match

import (
	"regexp"
	"strings"
)

// Pasted command output (PF-1 /identify): the model identifier, board ID,
// CPU name and device IDs, from Linux (/sys, /proc/cpuinfo or lspci -nn) or
// macOS (sysctl, ioreg, system_profiler). static/site.js applies the same rules in the browser.
var (
	identifierRe = regexp.MustCompile(`\b(MacBook(?:Air|Pro)?|iMac(?:Pro)?|Macmini|MacPro|Xserve)\d{1,2},\d{1,2}\b`)
	boardRe      = regexp.MustCompile(`\bMac-[0-9A-Fa-f]{16}\b`)
	sysfsRe      = regexp.MustCompile(`(?i)\b0x([0-9a-f]{4}):0x([0-9a-f]{4})\b`) // /sys vendor:device
	lspciRe      = regexp.MustCompile(`(?i)\[([0-9a-f]{4}):([0-9a-f]{4})\]`)     // lspci -nn
	macVendorRe  = regexp.MustCompile(`(?i)Vendor:.*\(0x([0-9a-f]{4})\)`)        // system_profiler
	macDeviceRe  = regexp.MustCompile(`(?i)Device ID:\s*0x([0-9a-f]{4})`)        // system_profiler
	cpuRe        = regexp.MustCompile(`(?im)^[ \t]*(?:model name[ \t]*:[ \t]*)?((?:Genuine )?Intel\(R\)[^\n]*?)[ \t]*$`)
)

// ParseProbe extracts what /identify needs from pasted command output. Nothing
// else in the text is kept.
func ParseProbe(text string) Probe {
	var p Probe
	if m := identifierRe.FindString(text); m != "" {
		p.ProductName = m
	}
	if m := boardRe.FindString(text); m != "" {
		p.BoardID = "Mac-" + strings.ToUpper(m[4:])
	}
	if m := cpuRe.FindStringSubmatch(text); m != nil {
		p.CPU = strings.Join(strings.Fields(m[1]), " ")
	}
	seen := map[string]bool{}
	add := func(v, d string) {
		id := strings.ToLower(v) + ":" + strings.ToLower(d)
		if !seen[id] {
			seen[id] = true
			p.PCI = append(p.PCI, id)
		}
	}
	for _, m := range sysfsRe.FindAllStringSubmatch(text, -1) {
		add(m[1], m[2])
	}
	for _, m := range lspciRe.FindAllStringSubmatch(text, -1) {
		add(m[1], m[2])
	}
	// system_profiler: a "Vendor: … (0x8086)" line, then its "Device ID: 0x…".
	vendor := ""
	for _, line := range strings.Split(text, "\n") {
		if m := macVendorRe.FindStringSubmatch(line); m != nil {
			vendor = m[1]
		} else if m := macDeviceRe.FindStringSubmatch(line); m != nil && vendor != "" {
			add(vendor, m[1])
			vendor = ""
		}
	}
	return p
}
