// Package match identifies a Mac and its configuration from what a machine
// reports about itself: the DMI product name (the model identifier), the
// board ID, PCI/USB device IDs and the CPU model (PLAN.md §22.4). The API's /match, report
// submission and the /identify page all use it.
package match

import (
	"regexp"
	"sort"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

// Probe is what a machine reports. IDs may be given in any common form:
// "10de:0647", "pci:10de:0647", "[10DE:0647]".
type Probe struct {
	ProductName string   `json:"product_name"`
	BoardID     string   `json:"board_id"`
	PCI         []string `json:"pci"`
	USB         []string `json:"usb"`
	// CPU is the processor's name as the system reports it, e.g. "Intel(R)
	// Core(TM) i7-2720QM CPU @ 2.20GHz" (/proc/cpuinfo, machdep.cpu.brand_string).
	CPU string `json:"cpu,omitempty"`
}

// Candidate is one configuration that fits the probe.
type Candidate struct {
	Config  string   `json:"config"`
	Score   int      `json:"score"`
	Matched []string `json:"matched"` // probe IDs this config's components have ("cpu:…" for the CPU)
	// NotReported lists this config's distinguishing parts (GPU, Wi-Fi, CPU)
	// that the probe doesn't show, one entry per part; a part known by several
	// IDs lists them with "/": "pci:8086:0116/pci:8086:0126".
	NotReported []string `json:"not_reported"`
}

// Result is the outcome of a match.
type Result struct {
	Identifier string      `json:"identifier,omitempty"`
	By         string      `json:"by,omitempty"` // product_name | board_id | devices
	Candidates []Candidate `json:"candidates"`
	// Exact: exactly one configuration fits best.
	Exact bool `json:"exact"`
}

// Best returns the top candidate's config ID, or "".
func (r Result) Best() string {
	if len(r.Candidates) == 0 {
		return ""
	}
	return r.Candidates[0].Config
}

// distinguishing component kinds: when the probe lists PCI devices, a
// config whose part of this kind is absent is a worse fit.
var distinguishing = map[string]bool{"gpu": true, "wifi": true}

type config struct {
	id    string
	ids   map[string]string // hardware ID → component kind (standard parts)
	bto   map[string]bool   // hardware IDs of build-to-order parts
	parts []part            // distinguishing standard parts with known IDs
	cpus  []string          // CPU model numbers, standard and BTO ("i7-2720qm", "e5-1620 v2")
}

type part struct {
	kind string
	ids  []string
}

// Matcher holds the catalog's identifiers, board IDs and device IDs.
type Matcher struct {
	byIdentifier map[string]string // lower-case identifier → identifier
	byBoard      map[string]string // lower-case board ID → identifier
	configs      map[string][]*config
	byDevice     map[string][]string // hardware ID → config IDs
	known        map[string]bool     // every hardware ID, standard and build-to-order
	order        []string            // identifiers in catalog order
}

// New indexes a catalog.
func New(c *catalog.Catalog) *Matcher {
	m := &Matcher{byIdentifier: map[string]string{}, byBoard: map[string]string{}, configs: map[string][]*config{},
		byDevice: map[string][]string{}, known: map[string]bool{}}
	for _, mac := range c.Macs {
		m.order = append(m.order, mac.Identifier)
		m.byIdentifier[strings.ToLower(mac.Identifier)] = mac.Identifier
		for _, b := range mac.BoardIDs {
			m.byBoard[strings.ToLower(b)] = mac.Identifier
		}
		for _, r := range mac.Releases {
			for _, cfg := range r.Configs {
				x := &config{id: cfg.ID, ids: map[string]string{}, bto: map[string]bool{}}
				for _, ref := range cfg.Components {
					comp := c.Components[ref]
					if comp == nil {
						continue
					}
					p := part{kind: comp.Kind}
					for _, id := range comp.IDs {
						x.ids[id] = comp.Kind
						m.known[id] = true
						p.ids = append(p.ids, id)
						m.byDevice[id] = append(m.byDevice[id], cfg.ID)
					}
					if distinguishing[comp.Kind] && len(p.ids) > 0 {
						x.parts = append(x.parts, p)
					}
				}
				for _, ref := range cfg.BTOComponents {
					if comp := c.Components[ref]; comp != nil {
						for _, id := range comp.IDs {
							x.bto[id] = true
							m.known[id] = true
						}
					}
				}
				for _, p := range append(append([]catalog.Processor{}, cfg.CPU.Standard...), cfg.CPU.BTO...) {
					if t := cpuToken(p.Model); t != "" {
						x.cpus = append(x.cpus, t)
					}
				}
				m.configs[mac.Identifier] = append(m.configs[mac.Identifier], x)
			}
		}
	}
	return m
}

// cpuToken is a catalog CPU's model number, the part a system's CPU name
// also contains: "Core i7-2720QM" → "i7-2720qm", "Dual Xeon 5150" → "5150",
// "Xeon E5-1620 v2" → "e5-1620 v2".
func cpuToken(model string) string {
	words := strings.Fields(strings.ToLower(model))
	for i, w := range words {
		if len(w) >= 3 && strings.ContainsAny(w, "0123456789") { // not the "2" of "Core 2 Duo"
			if i+1 < len(words) && len(words[i+1]) == 2 && words[i+1][0] == 'v' {
				return w + " " + words[i+1]
			}
			return w
		}
	}
	return ""
}

// cpuHas reports whether a system's CPU name contains a model number as a
// whole word.
func cpuHas(name, token string) bool {
	i := strings.Index(name, token)
	for i >= 0 {
		before, after := i == 0 || !isWordByte(name[i-1]), i+len(token) == len(name) || !isWordByte(name[i+len(token)])
		if before && after {
			return true
		}
		j := strings.Index(name[i+1:], token)
		if j < 0 {
			break
		}
		i += 1 + j
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '-' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z'
}

var idRe = regexp.MustCompile(`(?i)\b(?:(pci|usb):)?([0-9a-f]{4}):([0-9a-f]{4})\b`)

// NormalizeIDs turns IDs in any common form into "pci:vvvv:dddd" (or the
// given kind), lower-case, without duplicates.
func NormalizeIDs(ids []string, kind string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range ids {
		for _, m := range idRe.FindAllStringSubmatch(raw, -1) {
			k := strings.ToLower(m[1])
			if k == "" {
				k = kind
			}
			id := k + ":" + strings.ToLower(m[2]) + ":" + strings.ToLower(m[3])
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// Identify returns the model identifier a probe names, and how it was found.
func (m *Matcher) Identify(p Probe) (identifier, by string) {
	if id := m.byIdentifier[strings.ToLower(strings.TrimSpace(p.ProductName))]; id != "" {
		return id, "product_name"
	}
	if id := m.byBoard[strings.ToLower(strings.TrimSpace(p.BoardID))]; id != "" {
		return id, "board_id"
	}
	return "", ""
}

// KnownBoard reports whether the catalog lists a board ID.
func (m *Matcher) KnownBoard(board string) bool {
	return m.byBoard[strings.ToLower(strings.TrimSpace(board))] != ""
}

// KnownDevice reports whether any catalog component has a hardware ID, in
// any common form ("10de:0647", "pci:10de:0647").
func (m *Matcher) KnownDevice(id, kind string) bool {
	ids := NormalizeIDs([]string{id}, kind)
	return len(ids) == 1 && m.known[ids[0]]
}

// KnownCPU reports whether a system's CPU name contains the model number of
// any CPU in the catalog, standard or build-to-order.
func (m *Matcher) KnownCPU(name string) bool {
	name = strings.Join(strings.Fields(strings.ToLower(name)), " ")
	for _, cfgs := range m.configs {
		for _, cfg := range cfgs {
			for _, t := range cfg.cpus {
				if cpuHas(name, t) {
					return true
				}
			}
		}
	}
	return false
}

// Match ranks the configurations that fit a probe.
func (m *Matcher) Match(p Probe) Result {
	devices := append(NormalizeIDs(p.PCI, "pci"), NormalizeIDs(p.USB, "usb")...)
	have := map[string]bool{}
	for _, d := range devices {
		have[d] = true
	}
	hasPCI := len(NormalizeIDs(p.PCI, "pci")) > 0
	cpu := strings.Join(strings.Fields(strings.ToLower(p.CPU)), " ")
	var res Result
	res.Identifier, res.By = m.Identify(p)
	var pool []*config
	if res.Identifier != "" {
		pool = m.configs[res.Identifier]
	} else if len(devices) > 0 {
		// No identifier: every config that has one of the devices.
		seen := map[string]bool{}
		for _, d := range devices {
			for _, id := range m.byDevice[d] {
				seen[id] = true
			}
		}
		for _, ident := range m.order {
			for _, cfg := range m.configs[ident] {
				if seen[cfg.id] {
					pool = append(pool, cfg)
				}
			}
		}
		if len(pool) > 0 {
			res.By = "devices"
		}
	}
	for _, cfg := range pool {
		cand := Candidate{Config: cfg.id, Matched: []string{}, NotReported: []string{}}
		for _, d := range devices {
			if cfg.ids[d] != "" || cfg.bto[d] {
				cand.Matched = append(cand.Matched, d)
			}
		}
		if hasPCI {
			for _, pt := range cfg.parts {
				found := false
				for _, id := range pt.ids {
					found = found || have[id]
				}
				if !found {
					cand.NotReported = append(cand.NotReported, strings.Join(pt.ids, "/"))
				}
			}
		}
		// The CPU separates configurations that share every device (often
		// releases a year apart). A CPU no candidate lists costs them alike.
		if cpu != "" && len(cfg.cpus) > 0 {
			found := ""
			for _, t := range cfg.cpus {
				if cpuHas(cpu, t) {
					found = t
					break
				}
			}
			if found != "" {
				cand.Matched = append(cand.Matched, "cpu:"+found)
			} else {
				cand.NotReported = append(cand.NotReported, "cpu:"+strings.Join(cfg.cpus, "/"))
			}
		}
		// +10 per reported ID the config has, -10 per distinguishing part it
		// has that wasn't reported (per part, however many IDs it's known by).
		cand.Score = 10*len(cand.Matched) - 10*len(cand.NotReported)
		res.Candidates = append(res.Candidates, cand)
	}
	sort.SliceStable(res.Candidates, func(i, j int) bool { return res.Candidates[i].Score > res.Candidates[j].Score })
	switch {
	case len(res.Candidates) == 1:
		res.Exact = true
	case len(res.Candidates) > 1:
		// Parts a probe doesn't list cost every candidate alike, so only the
		// margin between the top two decides.
		res.Exact = res.Candidates[0].Score > res.Candidates[1].Score
	}
	if res.Candidates == nil {
		res.Candidates = []Candidate{}
	}
	return res
}
