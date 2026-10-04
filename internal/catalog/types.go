package catalog

import "strings"

// Vocabulary is data/vocabulary.yaml: every controlled value the catalog may use.
type Vocabulary struct {
	Lines          map[string]Line        `yaml:"lines" json:"lines"`
	SecurityChips  map[string]Named       `yaml:"security_chips" json:"security_chips"`
	ComponentKinds map[string]Named       `yaml:"component_kinds" json:"component_kinds"`
	CPUCodenames   map[string]CPUCodename `yaml:"cpu_codenames" json:"cpu_codenames"`
	Ports          map[string]Port        `yaml:"ports" json:"ports"`
	PowerConn      map[string]Named       `yaml:"power_connectors" json:"power_connectors"`
	Features       map[string]Named       `yaml:"features" json:"features"`
}

type Line struct {
	Name             string `yaml:"name" json:"name"`
	Form             string `yaml:"form" json:"form"`
	IdentifierPrefix string `yaml:"identifier_prefix" json:"identifier_prefix"`
}

type Named struct {
	Name string `yaml:"name" json:"name"`
}

type CPUCodename struct {
	Name   string `yaml:"name" json:"name"`
	Family string `yaml:"family" json:"family"`
	Bits   int    `yaml:"bits" json:"bits"`
}

type Port struct {
	Name  string   `yaml:"name" json:"name"`
	Video bool     `yaml:"video" json:"video"`
	Tests []string `yaml:"tests" json:"tests"` // criteria each connector of this class is tested for
}

// CapabilityFile is data/capabilities.yaml.
type CapabilityFile struct {
	Categories   []Category   `yaml:"categories" json:"categories"`
	Capabilities []Capability `yaml:"capabilities" json:"capabilities"`
}

type Category struct {
	ID       string `yaml:"id" json:"id"`
	Name     string `yaml:"name" json:"name"`
	Icon     string `yaml:"icon" json:"icon"`
	Blocking bool   `yaml:"blocking" json:"blocking"` // a failure here makes the config Unsupported
}

type Capability struct {
	ID          string     `yaml:"id" json:"id"`
	Name        string     `yaml:"name" json:"name"`
	Description string     `yaml:"description" json:"description"`
	When        *Condition `yaml:"when" json:"when"`
	// FixBy is the component kind (optionally "kind:role") whose driver
	// usually decides this criterion, for fix tracking (PLAN §26).
	FixBy string `yaml:"fix_by" json:"fix_by,omitempty"`
	// OneGroup: a per-connector criterion judged on all its connectors as one
	// port group, whatever controller each is on, because it doesn't depend
	// on that controller's driver (USB-C charging is negotiated by each
	// port's power-delivery chip and the SMC; PLAN §25.1a).
	OneGroup bool `yaml:"one_group" json:"one_group,omitempty"`
	// Retired criteria no longer apply to any configuration. They stay in the
	// catalog because results may reference them; IDs are never deleted.
	Retired bool `yaml:"retired" json:"retired,omitempty"`
}

// Category returns the category part of the capability ID ("boot" for "boot.install").
func (c Capability) Category() string {
	for i := range c.ID {
		if c.ID[i] == '.' {
			return c.ID[:i]
		}
	}
	return ""
}

// Condition selects configurations by their derived tags (see Tags).
type Condition struct {
	All  []string `yaml:"all" json:"all"`
	Any  []string `yaml:"any" json:"any"`
	None []string `yaml:"none" json:"none"`
}

// Uncertain flags a field whose value could not be confirmed from sources.
type Uncertain struct {
	Field string `yaml:"field" json:"field"`
	Note  string `yaml:"note" json:"note"`
}

// Component is one entry in data/components/<kind>.yaml.
type Component struct {
	ID        string      `yaml:"id" json:"id"` // "<kind>/<slug>"
	Name      string      `yaml:"name" json:"name"`
	Vendor    string      `yaml:"vendor" json:"vendor"`
	Role      string      `yaml:"role" json:"role"`     // gpu only: integrated | discrete
	GLES      string      `yaml:"gles" json:"gles"`     // gpu only: highest OpenGL ES version its Linux driver reaches (PLAN §30)
	IDs       []string    `yaml:"ids" json:"ids"`       // "pci:vvvv:dddd" or "usb:vvvv:pppp"
	Driver    string      `yaml:"driver" json:"driver"` // Linux driver seen on real hardware (research, not a test result)
	Notes     string      `yaml:"notes" json:"notes"`
	Sources   []string    `yaml:"sources" json:"sources"`
	Uncertain []Uncertain `yaml:"uncertain" json:"uncertain"`

	Kind string `yaml:"-" json:"-"` // from the file name
	File string `yaml:"-" json:"-"`
}

// Mac is one data/macs/<Identifier>.yaml file.
type Mac struct {
	Identifier    string      `yaml:"identifier" json:"identifier"`
	Line          string      `yaml:"line" json:"line"`
	EFI           int         `yaml:"efi" json:"efi"`
	SecurityChip  string      `yaml:"security_chip" json:"security_chip"`
	HardBlocker   string      `yaml:"hard_blocker" json:"hard_blocker"`
	BoardIDs      []string    `yaml:"board_ids" json:"board_ids"` // in the file: untied boards only; after loading: all, see mergeBoards
	ResearchNotes string      `yaml:"research_notes" json:"research_notes"`
	Sources       []string    `yaml:"sources" json:"sources"`
	Uncertain     []Uncertain `yaml:"uncertain" json:"uncertain"`
	Releases      []Release   `yaml:"releases" json:"releases"`

	File string `yaml:"-" json:"-"`
}

type Release struct {
	ID           string   `yaml:"id" json:"id"`
	Name         string   `yaml:"name" json:"name"`                       // Apple's name, as About This Mac shows it
	ShortName    string   `yaml:"short_name" json:"short_name,omitempty"` // for lists, when Name doesn't follow ListNamePattern
	Announced    string   `yaml:"announced" json:"announced"`             // YYYY-MM-DD
	Discontinued string   `yaml:"discontinued" json:"discontinued"`
	ModelNumbers []string `yaml:"model_numbers" json:"model_numbers"` // Apple "A" numbers
	EMC          []string `yaml:"emc" json:"emc"`
	BoardIDs     []string `yaml:"board_ids" json:"board_ids,omitempty"` // tied to this release by real machines; may be on several releases (PLAN.md §29)
	Sources      []string `yaml:"sources" json:"sources"`
	Configs      []Config `yaml:"configs" json:"configs"`
}

// ListName is the release's name for lists: its ShortName, else its Name.
func (r *Release) ListName() string {
	if r.ShortName != "" {
		return r.ShortName
	}
	return r.Name
}

// BoardReleases returns the releases a board ID is tied to (any case), or
// nil when it's tied to none.
func (m *Mac) BoardReleases(board string) []*Release {
	var out []*Release
	for i := range m.Releases {
		for _, b := range m.Releases[i].BoardIDs {
			if strings.EqualFold(b, strings.TrimSpace(board)) {
				out = append(out, &m.Releases[i])
				break
			}
		}
	}
	return out
}

// ReleaseOf returns the release a configuration belongs to, or nil.
func (m *Mac) ReleaseOf(configID string) *Release {
	for i := range m.Releases {
		for _, cfg := range m.Releases[i].Configs {
			if cfg.ID == configID {
				return &m.Releases[i]
			}
		}
	}
	return nil
}

type Config struct {
	ID            string         `yaml:"id" json:"id"` // permanent; results reference it
	Label         string         `yaml:"label" json:"label"`
	OrderNumbers  []string       `yaml:"order_numbers" json:"order_numbers"`
	BTOOnly       bool           `yaml:"bto_only" json:"bto_only"` // only available as a build-to-order option
	CPU           CPU            `yaml:"cpu" json:"cpu"`
	Memory        Memory         `yaml:"memory" json:"memory"`
	Storage       Storage        `yaml:"storage" json:"storage"`
	Display       *Display       `yaml:"display" json:"display"`
	Components    []string       `yaml:"components" json:"components"`
	BTOComponents []string       `yaml:"bto_components" json:"bto_components"` // optional add-ons that don't warrant their own config
	Ports         map[string]int `yaml:"ports" json:"ports"`
	Features      []string       `yaml:"features" json:"features"`
	Notes         string         `yaml:"notes" json:"notes"`
	Aliases       []string       `yaml:"aliases" json:"aliases"` // former IDs of this config
	Sources       []string       `yaml:"sources" json:"sources"`
	Uncertain     []Uncertain    `yaml:"uncertain" json:"uncertain"`

	// Connectors is the configuration's port layout, from data/layouts
	// (its release's, unless the configuration has its own); nil when not
	// researched yet.
	Connectors    []Connector `yaml:"-" json:"connectors,omitempty"`
	LayoutSources []string    `yaml:"-" json:"layout_sources,omitempty"`
	LayoutNote    string      `yaml:"-" json:"layout_note,omitempty"`
	// Portmap is the key of the release's port map drawing (data/portmaps,
	// PLAN §27) when one exists and matches this configuration's layout.
	Portmap string `yaml:"-" json:"-"`
}

// Connector is one physical connector in a port layout (PLAN.md §25).
type Connector struct {
	ID   string   `yaml:"id" json:"id"`               // <side>-<n>, numbered left to right facing that side
	Type string   `yaml:"type" json:"type"`           // a port class, a power connector, or "ethernet"
	Also []string `yaml:"also" json:"also,omitempty"` // more port classes the same connector carries
	Note string   `yaml:"note" json:"note,omitempty"` // e.g. "nearest the hinge"
	// Group names the connectors that share a controller and driver path,
	// when that differs from the default (one group per connector type).
	Group string `yaml:"group" json:"group,omitempty"`
}

// Side is the connector's side: left, right, back, front or top.
func (c Connector) Side() string {
	side, _, _ := strings.Cut(c.ID, "-")
	return side
}

// LayoutFile is one data/layouts/<Identifier>.yaml file: port layouts per
// release, researched from Apple's manuals.
type LayoutFile struct {
	Identifier string                   `yaml:"identifier"`
	Releases   map[string]ReleaseLayout `yaml:"releases"`

	File string `yaml:"-"`
}

type ReleaseLayout struct {
	Sources    []string               `yaml:"sources"`
	Note       string                 `yaml:"note"`
	Connectors []Connector            `yaml:"connectors"`
	Configs    map[string][]Connector `yaml:"configs"` // a configuration whose layout differs
}

type CPU struct {
	Codename string      `yaml:"codename" json:"codename"`
	Standard []Processor `yaml:"standard" json:"standard"`
	BTO      []Processor `yaml:"bto" json:"bto"`
}

type Processor struct {
	Model string  `yaml:"model" json:"model"` // e.g. "Core 2 Duo P7350"
	GHz   float64 `yaml:"ghz" json:"ghz"`
	Cores int     `yaml:"cores" json:"cores"`
}

type Memory struct {
	Type       string    `yaml:"type" json:"type"`
	StandardGB []float64 `yaml:"standard_gb" json:"standard_gb"`
	MaxGB      float64   `yaml:"max_gb" json:"max_gb"` // Apple's official maximum
	Soldered   bool      `yaml:"soldered" json:"soldered"`
	Notes      string    `yaml:"notes" json:"notes"`
}

type Storage struct {
	Interface string   `yaml:"interface" json:"interface"` // sata | pata | pcie-ahci | nvme
	Standard  []string `yaml:"standard" json:"standard"`
	BTO       []string `yaml:"bto" json:"bto"`
}

type Display struct {
	Inches     float64 `yaml:"inches" json:"inches"`
	Resolution string  `yaml:"resolution" json:"resolution"`
	Panel      string  `yaml:"panel" json:"panel"`
}

// Catalog is the fully loaded and cross-referenced data directory.
type Catalog struct {
	Vocab         Vocabulary
	Categories    []Category
	Capabilities  []Capability
	Components    map[string]*Component
	Macs          []*Mac
	LockedIDs     []string            // data/config-ids.lock
	CoverageRules []CoverageRule      // data/coverage.yaml (optional)
	Aliases       []Alias             // data/aliases.yaml (optional)
	Changelog     []ChangeEntry       // data/changelog.yaml (optional)
	Layouts       []*LayoutFile       // data/layouts (optional, PLAN §25)
	Portmaps      map[string]*Portmap // data/portmaps, by key (optional, PLAN §27)
	Plumbing      map[string]Plumbing // data/plumbing.yaml, by "pci:vvvv:dddd" (optional, PLAN §29)
}

// PlumbingVendor is one vendor's entry in data/plumbing.yaml: PCI IDs of
// chipset plumbing whose generic driver never decides a test criterion
// (bridges, SMBus, USB host and HD Audio controllers, …). Generated by
// omarchaeology gen/plumbing.py; used only to fold these IDs away on
// /admin/shares, never for matching.
type PlumbingVendor struct {
	Vendor string              `yaml:"vendor"`
	IDs    map[string]Plumbing `yaml:"ids"`
}

type Plumbing struct {
	Name  string `yaml:"name"`  // from pci.ids
	Class string `yaml:"class"` // the lspci class
}

// Portmap is one release's port map drawing: an original, to-scale line
// drawing generated by omarchaeology gen/portmaps.py (PLAN §27).
type Portmap struct {
	Key      string   // <FileSlug(identifier)>_<release ID>
	Site     []byte   // the site flavour, inlined in pages (no <style>, data-conn on ports)
	Download []byte   // the standalone flavour, served at /portmap/<Key>.svg
	Conns    []string // the connector IDs its ports carry, in drawing order
}
