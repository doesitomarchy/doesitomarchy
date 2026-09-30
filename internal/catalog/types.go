package catalog

// Vocabulary is data/vocabulary.yaml: every controlled value the catalog may use.
type Vocabulary struct {
	Lines          map[string]Line        `yaml:"lines"`
	SecurityChips  map[string]Named       `yaml:"security_chips"`
	ComponentKinds map[string]Named       `yaml:"component_kinds"`
	CPUCodenames   map[string]CPUCodename `yaml:"cpu_codenames"`
	Ports          map[string]Port        `yaml:"ports"`
	Features       map[string]Named       `yaml:"features"`
}

type Line struct {
	Name             string `yaml:"name"`
	Form             string `yaml:"form"`
	IdentifierPrefix string `yaml:"identifier_prefix"`
}

type Named struct {
	Name string `yaml:"name"`
}

type CPUCodename struct {
	Name   string `yaml:"name"`
	Family string `yaml:"family"`
	Bits   int    `yaml:"bits"`
}

type Port struct {
	Name  string `yaml:"name"`
	Video bool   `yaml:"video"`
}

// CapabilityFile is data/capabilities.yaml.
type CapabilityFile struct {
	Categories   []Category   `yaml:"categories"`
	Capabilities []Capability `yaml:"capabilities"`
}

type Category struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	Icon     string `yaml:"icon"`
	Blocking bool   `yaml:"blocking"` // a failure here makes the config Unsupported
}

type Capability struct {
	ID          string     `yaml:"id"`
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	When        *Condition `yaml:"when"`
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
	All  []string `yaml:"all"`
	Any  []string `yaml:"any"`
	None []string `yaml:"none"`
}

// Uncertain flags a field whose value could not be confirmed from sources.
type Uncertain struct {
	Field string `yaml:"field"`
	Note  string `yaml:"note"`
}

// Component is one entry in data/components/<kind>.yaml.
type Component struct {
	ID        string      `yaml:"id"` // "<kind>/<slug>"
	Name      string      `yaml:"name"`
	Vendor    string      `yaml:"vendor"`
	Role      string      `yaml:"role"`   // gpu only: integrated | discrete
	IDs       []string    `yaml:"ids"`    // "pci:vvvv:dddd" or "usb:vvvv:pppp"
	Driver    string      `yaml:"driver"` // Linux driver seen on real hardware (research, not a test result)
	Notes     string      `yaml:"notes"`
	Sources   []string    `yaml:"sources"`
	Uncertain []Uncertain `yaml:"uncertain"`

	Kind string `yaml:"-"` // from the file name
	File string `yaml:"-"`
}

// Mac is one data/macs/<Identifier>.yaml file.
type Mac struct {
	Identifier    string      `yaml:"identifier"`
	Line          string      `yaml:"line"`
	EFI           int         `yaml:"efi"`
	SecurityChip  string      `yaml:"security_chip"`
	HardBlocker   string      `yaml:"hard_blocker"`
	BoardIDs      []string    `yaml:"board_ids"`
	ResearchNotes string      `yaml:"research_notes"`
	Sources       []string    `yaml:"sources"`
	Uncertain     []Uncertain `yaml:"uncertain"`
	Releases      []Release   `yaml:"releases"`

	File string `yaml:"-"`
}

type Release struct {
	ID           string   `yaml:"id"`
	Name         string   `yaml:"name"`
	Announced    string   `yaml:"announced"` // YYYY-MM-DD
	Discontinued string   `yaml:"discontinued"`
	ModelNumbers []string `yaml:"model_numbers"` // Apple "A" numbers
	EMC          []string `yaml:"emc"`
	Sources      []string `yaml:"sources"`
	Configs      []Config `yaml:"configs"`
}

type Config struct {
	ID            string         `yaml:"id"` // permanent; results reference it
	Label         string         `yaml:"label"`
	OrderNumbers  []string       `yaml:"order_numbers"`
	BTOOnly       bool           `yaml:"bto_only"` // only available as a build-to-order option
	CPU           CPU            `yaml:"cpu"`
	Memory        Memory         `yaml:"memory"`
	Storage       Storage        `yaml:"storage"`
	Display       *Display       `yaml:"display"`
	Components    []string       `yaml:"components"`
	BTOComponents []string       `yaml:"bto_components"` // optional add-ons that don't warrant their own config
	Ports         map[string]int `yaml:"ports"`
	Features      []string       `yaml:"features"`
	Notes         string         `yaml:"notes"`
	Aliases       []string       `yaml:"aliases"` // former IDs of this config
	Sources       []string       `yaml:"sources"`
	Uncertain     []Uncertain    `yaml:"uncertain"`
}

type CPU struct {
	Codename string      `yaml:"codename"`
	Standard []Processor `yaml:"standard"`
	BTO      []Processor `yaml:"bto"`
}

type Processor struct {
	Model string  `yaml:"model"` // e.g. "Core 2 Duo P7350"
	GHz   float64 `yaml:"ghz"`
	Cores int     `yaml:"cores"`
}

type Memory struct {
	Type       string    `yaml:"type"`
	StandardGB []float64 `yaml:"standard_gb"`
	MaxGB      float64   `yaml:"max_gb"` // Apple's official maximum
	Soldered   bool      `yaml:"soldered"`
	Notes      string    `yaml:"notes"`
}

type Storage struct {
	Interface string   `yaml:"interface"` // sata | pata | pcie-ahci | nvme
	Standard  []string `yaml:"standard"`
	BTO       []string `yaml:"bto"`
}

type Display struct {
	Inches     float64 `yaml:"inches"`
	Resolution string  `yaml:"resolution"`
	Panel      string  `yaml:"panel"`
}

// Catalog is the fully loaded and cross-referenced data directory.
type Catalog struct {
	Vocab         Vocabulary
	Categories    []Category
	Capabilities  []Capability
	Components    map[string]*Component
	Macs          []*Mac
	LockedIDs     []string       // data/config-ids.lock
	CoverageRules []CoverageRule // data/coverage.yaml (optional)
}
