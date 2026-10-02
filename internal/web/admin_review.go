package web

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/match"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// What a maintainer needs to pick a configuration: the hardware the report
// sent, read against the catalog, and the candidate configurations side by
// side with what tells them apart.

// probeRow is one fact from the report's hardware probe.
type probeRow struct {
	Label, Value string
	Known        string // what the catalog calls it
	Note         string // e.g. "not in the catalog"
	Fits         string // which candidate configurations have it
}

// candidateRow is one configuration a maintainer may pick.
type candidateRow struct {
	Config               *configView
	Current, Tied        bool
	Score                int
	Matched, NotReported []string
	CPU, GPU, WiFi       string
}

type review struct {
	Probe      []probeRow
	Extra      []probeRow // other keys the tool sent
	Rows       []candidateRow
	Differs    []string // fields in which the candidates differ
	Settle     []string // what would tell them apart
	Ambiguous  bool
	NoHardware bool
}

// deviceName is the catalog's name for a hardware ID.
func (s *Server) deviceName(id string) string {
	for _, c := range s.cat.Components {
		for _, x := range c.IDs {
			if x == id {
				return c.Name
			}
		}
	}
	return ""
}

func (s *Server) buildReview(rd *store.ResultDetail, ambiguous bool) review {
	rv := review{Ambiguous: ambiguous}
	hw := map[string]any{}
	if strings.TrimSpace(rd.Hardware) != "" {
		json.Unmarshal([]byte(rd.Hardware), &hw)
	}
	probe := results.ProbeFromHardware(hw, rd.Identifier)
	res := s.match.Match(probe)
	view := s.data().view

	// The candidates: the tied ones when ambiguous, else every configuration of the Mac.
	var ids []string
	if ambiguous && len(rd.Candidates) > 0 {
		ids = rd.Candidates
	} else if cv := view.configs[rd.ConfigID]; cv != nil && cv.Mac != nil {
		for _, x := range cv.Mac.Configs {
			ids = append(ids, x.ID)
		}
	}
	scored := map[string]match.Candidate{}
	for _, c := range res.Candidates {
		scored[c.Config] = c
	}
	top := 0
	for i, id := range ids {
		if sc := scored[id].Score; i == 0 || sc > top {
			top = sc
		}
	}
	for _, id := range ids {
		cv := view.configs[id]
		if cv == nil {
			continue
		}
		c := scored[id]
		row := candidateRow{Config: cv, Current: id == rd.ConfigID, Score: c.Score, Tied: c.Score == top,
			Matched: c.Matched, NotReported: c.NotReported, CPU: strings.Join(cv.CPU, "; ")}
		if len(cv.CPUBTO) > 0 {
			row.CPU += " (BTO: " + strings.Join(cv.CPUBTO, "; ") + ")"
		}
		var gpu, wifi []string
		for _, comp := range cv.Components {
			switch comp.Kind {
			case "gpu":
				gpu = append(gpu, comp.Name+idList(comp.IDs))
			case "wifi":
				wifi = append(wifi, comp.Name+idList(comp.IDs))
			}
		}
		row.GPU, row.WiFi = strings.Join(gpu, " + "), strings.Join(wifi, ", ")
		rv.Rows = append(rv.Rows, row)
	}

	// Which configurations have each probe fact.
	fits := func(has func(*configView) bool) string {
		if len(rv.Rows) < 2 {
			return ""
		}
		var in []string
		for _, r := range rv.Rows {
			if has(r.Config) {
				in = append(in, r.Config.Diff)
			}
		}
		switch len(in) {
		case 0:
			return "none of these configurations"
		case len(rv.Rows):
			return "all of these configurations"
		}
		return strings.Join(in, ", ")
	}
	if probe.ProductName != "" {
		note := ""
		if res.Identifier == "" {
			note = "not a Mac in the catalog"
		}
		rv.Probe = append(rv.Probe, probeRow{Label: "Model identifier", Value: probe.ProductName, Note: note})
	}
	if probe.BoardID != "" {
		r := probeRow{Label: "Board ID", Value: probe.BoardID}
		if id, by := s.match.Identify(match.Probe{BoardID: probe.BoardID}); by == "" {
			r.Note = "not in the catalog"
		} else if id != rd.Identifier {
			r.Note = "belongs to " + id
		} else {
			r.Known = id
		}
		rv.Probe = append(rv.Probe, r)
	}
	if probe.CPU != "" {
		cpu := strings.ToLower(probe.CPU)
		rv.Probe = append(rv.Probe, probeRow{Label: "CPU", Value: probe.CPU, Fits: fits(func(cv *configView) bool {
			for _, m := range append(append([]string{}, cv.CPU...), cv.CPUBTO...) {
				if t := cpuModel(m); t != "" && strings.Contains(cpu, t) {
					return true
				}
			}
			return false
		})})
	}
	for _, id := range append(match.NormalizeIDs(probe.PCI, "pci"), match.NormalizeIDs(probe.USB, "usb")...) {
		r := probeRow{Label: strings.ToUpper(id[:3]), Value: id[4:], Known: s.deviceName(id)}
		if r.Known == "" {
			r.Note = "not in the catalog"
		}
		r.Fits = fits(func(cv *configView) bool {
			for _, comp := range cv.Components {
				for _, x := range comp.IDs {
					if x == id {
						return true
					}
				}
			}
			return false
		})
		rv.Probe = append(rv.Probe, r)
	}
	rv.NoHardware = len(rv.Probe) == 0
	standard := map[string]bool{"product_name": true, "board_id": true, "cpu": true, "pci": true, "usb": true}
	var keys []string
	for k := range hw {
		if !standard[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		b, _ := json.Marshal(hw[k])
		v := string(b)
		if sv, ok := hw[k].(string); ok {
			v = sv
		}
		rv.Extra = append(rv.Extra, probeRow{Label: k, Value: v})
	}

	// What differs between the candidates, and what would settle it.
	if len(rv.Rows) > 1 {
		differs := func(name string, f func(candidateRow) string) {
			seen := map[string]bool{}
			for _, r := range rv.Rows {
				seen[f(r)] = true
			}
			if len(seen) > 1 {
				rv.Differs = append(rv.Differs, name)
			}
		}
		differs("release", func(r candidateRow) string { return r.Config.ReleaseName })
		differs("CPU", func(r candidateRow) string { return r.CPU })
		differs("GPU", func(r candidateRow) string { return r.GPU })
		differs("Wi-Fi", func(r candidateRow) string { return r.WiFi })
		differs("memory", func(r candidateRow) string { return r.Config.Memory })
		differs("storage", func(r candidateRow) string { return r.Config.Storage })
		differs("display", func(r candidateRow) string { return r.Config.Display })
		has := func(f string) bool {
			for _, d := range rv.Differs {
				if d == f {
					return true
				}
			}
			return false
		}
		if has("CPU") && probe.CPU == "" {
			rv.Settle = append(rv.Settle, `the CPU name: on Linux, grep -m1 "model name" /proc/cpuinfo; on macOS, sysctl -n machdep.cpu.brand_string`)
		}
		if has("release") {
			rv.Settle = append(rv.Settle, "the release (e.g. \"Early 2011\"), shown in About This Mac or on Apple's check-coverage page for the serial number")
		}
		if (has("GPU") || has("Wi-Fi")) && len(probe.PCI) == 0 {
			rv.Settle = append(rv.Settle, "the PCI device IDs (lspci -nn)")
		}
		if has("storage") || has("memory") {
			rv.Settle = append(rv.Settle, "the original storage or memory size, if it hasn't been upgraded")
		}
	}
	return rv
}

func idList(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	short := make([]string, len(ids))
	for i, id := range ids {
		short[i] = strings.TrimPrefix(strings.TrimPrefix(id, "pci:"), "usb:")
	}
	return fmt.Sprintf(" (%s)", strings.Join(short, ", "))
}

// cpuModel is the model number in a catalog CPU description
// ("Core i7-2720QM 2.2 GHz" → "i7-2720qm").
func cpuModel(desc string) string {
	for _, w := range strings.Fields(strings.ToLower(desc)) {
		if len(w) >= 3 && strings.ContainsAny(w, "0123456789") && !strings.Contains(w, "ghz") {
			return w
		}
	}
	return ""
}
