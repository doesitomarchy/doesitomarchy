package report

import "strings"

import "github.com/doesitomarchy/doesitomarchy/pkg/match"

// CatalogSnapshot is GET /api/v1/snapshot: what a test tool needs from the
// catalog to build, check and match a report offline. A tool embeds a copy
// and fetches a newer one when it's online.
type CatalogSnapshot struct {
	Version      string              `json:"version" doc:"Changes whenever the content does; the response's ETag is this value, quoted"`
	Date         string              `json:"date,omitempty" doc:"The day the catalog's content last changed, YYYY-MM-DD"`
	Match        match.Snapshot      `json:"match" doc:"Every Mac's identifier, board IDs and configurations with their hardware IDs and CPUs, for matching a Mac offline"`
	Capabilities []SnapshotCriterion `json:"capabilities" doc:"The test criteria, with whether a live boot can decide each"`
	Fixes        []BootFix           `json:"fixes" doc:"The registered OmaBoot? fixes a report may list in fixes"`
	FixLevels    []FixLevel          `json:"fix_levels" doc:"Where in the system a fix can live, from the desktop down to the firmware; a fix's levels name these"`
}

// Live flags: whether a live boot can decide a criterion.
const (
	LiveYes = "yes" // a live boot decides it as an installed system would
	LiveNo  = "no"  // only an installed system can: send it as not_tested, reason live-limit
	LiveT2  = "t2"  // only on Macs with a T2 chip
	// LiveKernel: only when the live system runs the kernel an install gives the Mac
	// (InstallKernel), e.g. what needs that kernel's SMC driver.
	LiveKernel = "kernel"
)

// InstallKernel reports whether kernel (uname -r, a report's kernel) is the kernel an Omarchy
// install gives a Mac: linux-t2 on Macs with a T2 chip, linux-omarchy on the others (omarchy-iso
// since 2026-09-14). OmaBoot? Live boots that kernel by default.
func InstallKernel(t2 bool, kernel string) bool {
	kernel = strings.TrimSpace(kernel)
	if t2 {
		return strings.HasSuffix(kernel, "-t2")
	}
	return strings.HasSuffix(kernel, "-omarchy")
}

// SnapshotCriterion is one test criterion.
type SnapshotCriterion struct {
	ID       string `json:"id" doc:"The criterion ID reports use, e.g. network.wifi"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Live     string `json:"live" doc:"Whether a live boot can decide it: yes, no, or t2 (only on Macs with a T2 chip)"`
	Retired  bool   `json:"retired,omitempty" doc:"No longer tested; kept so old reports still resolve"`
}

// BootFix is a fix OmaBoot? applies in its live system, so a live boot
// matches what an installed Omarchy gets, or gets ahead of it.
type BootFix struct {
	ID           string      `json:"id" doc:"What a report lists in fixes, e.g. omaboot.radeon-imac10-1-panel-clock"`
	Name         string      `json:"name"`
	Changes      string      `json:"changes" doc:"What it changes"`
	Hardware     string      `json:"hardware" doc:"Which hardware it applies to, in words"`
	Devices      []FixDevice `json:"devices,omitempty" doc:"The devices it acts on"`
	Configs      []string    `json:"configs,omitempty" doc:"The configurations it applies to, when it's that narrow"`
	AppliesTo    []string    `json:"applies_to" doc:"Every configuration it applies to: its configs, or those the registry's rule picks (its devices, tags or years)"`
	AppliesRule  string      `json:"applies_rule,omitempty" doc:"The rule in words, e.g. Every Mac with a lid, from 2009; absent when it lists its configs"`
	Criteria     []string    `json:"criteria" doc:"The criteria whose results it can change"`
	Upstream     []FixLink   `json:"upstream,omitempty" doc:"Where the fix comes from, or where it's heading"`
	UpstreamNote string      `json:"upstream_note,omitempty"`
	TestedOn     []string    `json:"tested_on,omitempty" doc:"Configurations it was tried on, on real hardware"`
	Since        string      `json:"since" doc:"The first OmaBoot? build that carries it"`
	Levels       []FixPlace  `json:"levels" doc:"Where in the system it lives (see fix_levels), its main level first"`
	URL          string      `json:"url" doc:"Its entry on the site's list of fixes"`
}

// FixPlace is one level of the system a fix lives at, and what it changes there.
type FixPlace struct {
	Level string `json:"level" doc:"A fix level's id, e.g. driver-patch"`
	What  string `json:"what" doc:"What it changes at that level, e.g. radeon, rebuilt for both kernels"`
}

// FixLevel is a level of the system a fix can live at.
type FixLevel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Means string `json:"means"`
}

// FixLevels are the levels a fix can live at, from what the user sees down to
// the firmware. No fix lives at firmware: OmaBoot? never changes it, and the
// level is listed so a tool can say so.
var FixLevels = []FixLevel{
	{"desktop", "Desktop", "Omarchy's own settings: Hyprland, the apps, the look."},
	{"system", "System service", "Services, hooks and rules that run outside the kernel: systemd, udev."},
	{"driver-settings", "Driver settings", "Which drivers load, and their options (modprobe.d)."},
	{"new-driver", "New driver", "A driver the kernel Omarchy ships doesn't have."},
	{"driver-patch", "Driver patch", "A change to a driver Omarchy ships, rebuilt for its exact kernel."},
	{"kernel", "Kernel", "A change to the kernel itself."},
	{"boot-options", "Boot options", "Options the boot menu passes to the kernel."},
	{"firmware", "Firmware", "The Mac's own firmware and its settings. OmaBoot? never changes them."},
}

// FixLevelByID finds a fix level by its id.
func FixLevelByID(id string) (FixLevel, bool) {
	for _, l := range FixLevels {
		if l.ID == id {
			return l, true
		}
	}
	return FixLevel{}, false
}

// FixDevice is a PCI or USB device a fix acts on.
type FixDevice struct {
	ID        string `json:"id" doc:"e.g. pci:1002:9488"`
	Subsystem string `json:"subsystem,omitempty" doc:"The subsystem vendor:device, when the fix needs that too, e.g. 106b:00b6"`
}

// FixLink is a link to a fix's source.
type FixLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}
