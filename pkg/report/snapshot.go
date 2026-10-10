package report

import "github.com/doesitomarchy/doesitomarchy/pkg/match"

// CatalogSnapshot is GET /api/v1/snapshot: what a test tool needs from the
// catalog to build, check and match a report offline. A tool embeds a copy
// and fetches a newer one when it's online.
type CatalogSnapshot struct {
	Version      string              `json:"version" doc:"Changes whenever the content does; the response's ETag is this value, quoted"`
	Match        match.Snapshot      `json:"match" doc:"Every Mac's identifier, board IDs and configurations with their hardware IDs and CPUs, for matching a Mac offline"`
	Capabilities []SnapshotCriterion `json:"capabilities" doc:"The test criteria, with whether a live boot can decide each"`
	Fixes        []BootFix           `json:"fixes" doc:"The registered OmaBoot? fixes a report may list in fixes"`
}

// Live flags: whether a live boot can decide a criterion.
const (
	LiveYes = "yes" // a live boot decides it as an installed system would
	LiveNo  = "no"  // only an installed system can: send it as not_tested, reason live-limit
	LiveT2  = "t2"  // only on Macs with a T2 chip
)

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
	Criteria     []string    `json:"criteria" doc:"The criteria whose results it can change"`
	Upstream     []FixLink   `json:"upstream,omitempty" doc:"Where the fix comes from, or where it's heading"`
	UpstreamNote string      `json:"upstream_note,omitempty"`
	TestedOn     []string    `json:"tested_on,omitempty" doc:"Configurations it was tried on, on real hardware"`
	Since        string      `json:"since" doc:"The first OmaBoot? build that carries it"`
	URL          string      `json:"url" doc:"Its entry on the site's list of fixes"`
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
