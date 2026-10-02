package match

import (
	"reflect"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

func matcher(t *testing.T) *Matcher {
	t.Helper()
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	return New(c)
}

// Real IDs from the catalog: MacBookPro8,2 has four configurations told apart
// by the discrete GPU; early-2011-b and late-2011-a share the HD 6750M
// (1002:6741), so no probe can tell those two apart.
func TestMatch(t *testing.T) {
	m := matcher(t)
	igpu := "8086:0126"
	tests := []struct {
		name       string
		probe      Probe
		identifier string
		by         string
		exact      bool
		best       string
		candidates int
	}{
		{"product name and the GPU: exact", Probe{ProductName: "MacBookPro8,2", PCI: []string{igpu, "1002:6760", "14e4:4331"}},
			"MacBookPro8,2", "product_name", true, "macbookpro8-2-15-early-2011-a", 4},
		{"the GPU two configs share: ambiguous", Probe{ProductName: "macbookpro8,2", PCI: []string{igpu, "1002:6741"}},
			"MacBookPro8,2", "product_name", false, "", 4},
		{"board ID only, from lspci-style text", Probe{BoardID: "Mac-94245A3940C91C80", PCI: []string{"01:00.0 VGA compatible controller [0300]: AMD [1002:6740]"}},
			"MacBookPro8,2", "board_id", true, "macbookpro8-2-15-late-2011-b", 4},
		{"no devices, several configs: not exact", Probe{ProductName: "MacBookAir7,2"},
			"MacBookAir7,2", "product_name", false, "", 2},
		{"identical configs (2015 and 2017): never exact", Probe{ProductName: "MacBookAir7,2", PCI: []string{"8086:1626", "14e4:43a0"}},
			"MacBookAir7,2", "product_name", false, "", 2},
		{"two releases, no devices: not exact", Probe{ProductName: "MacBookPro15,1", PCI: nil},
			"MacBookPro15,1", "product_name", false, "", 2},
		{"a Mac with one config: exact without devices", Probe{ProductName: "MacBookPro7,1"},
			"MacBookPro7,1", "product_name", true, "macbookpro7-1-13-mid-2010-a", 1},
		{"devices only", Probe{PCI: []string{"1002:6760"}},
			"", "devices", true, "macbookpro8-2-15-early-2011-a", 1},
		{"unknown hardware", Probe{ProductName: "MacBookPro99,9"},
			"", "", false, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := m.Match(tt.probe)
			if r.Identifier != tt.identifier || r.By != tt.by || r.Exact != tt.exact || len(r.Candidates) != tt.candidates {
				t.Fatalf("got identifier=%q by=%q exact=%v candidates=%d (%+v)", r.Identifier, r.By, r.Exact, len(r.Candidates), r.Candidates)
			}
			if tt.best != "" && r.Best() != tt.best {
				t.Fatalf("best = %s, want %s", r.Best(), tt.best)
			}
		})
	}
}

func TestAmbiguousCandidatesTie(t *testing.T) {
	r := matcher(t).Match(Probe{ProductName: "MacBookPro8,2", PCI: []string{"8086:0126", "1002:6741"}})
	top := []string{r.Candidates[0].Config, r.Candidates[1].Config}
	want := []string{"macbookpro8-2-15-early-2011-b", "macbookpro8-2-15-late-2011-a"}
	if !reflect.DeepEqual(top, want) || r.Candidates[0].Score != r.Candidates[1].Score || r.Candidates[2].Score >= r.Candidates[1].Score {
		t.Fatalf("the two configs with the HD 6750M should tie at the top: %+v", r.Candidates)
	}
	if len(r.Candidates[2].NotReported) == 0 {
		t.Error("configs whose GPU is absent should list it as missing")
	}
}

func TestNormalizeIDs(t *testing.T) {
	got := NormalizeIDs([]string{"10DE:0647", "pci:10de:0647", "[8086:1C02] rev 05", "usb:05ac:8509", "nonsense", "1002:6741 and 14e4:4331"}, "pci")
	want := []string{"pci:10de:0647", "pci:8086:1c02", "usb:05ac:8509", "pci:1002:6741", "pci:14e4:4331"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// The CPU breaks ties between configurations that share every device.
func TestCPUBreaksTie(t *testing.T) {
	m := matcher(t)
	r := m.Match(Probe{ProductName: "MacBookPro8,2", PCI: []string{"8086:0126", "1002:6741"}, CPU: "Intel(R) Core(TM) i7-2675QM CPU @ 2.20GHz"})
	if !r.Exact || r.Best() != "macbookpro8-2-15-late-2011-a" {
		t.Fatalf("the i7-2675QM is only in the Late 2011: %+v", r.Candidates)
	}
	if !reflect.DeepEqual(r.Candidates[0].Matched, []string{"pci:8086:0126", "pci:1002:6741", "cpu:i7-2675qm"}) {
		t.Errorf("matched: %v", r.Candidates[0].Matched)
	}
	// A CPU no configuration lists leaves the tie as it was.
	r = m.Match(Probe{ProductName: "MacBookPro8,2", PCI: []string{"1002:6741"}, CPU: "Intel(R) Core(TM) i9-9980HK"})
	if r.Exact {
		t.Fatalf("an unknown CPU decided the match: %+v", r.Candidates)
	}
	// Core 2 names pad with spaces; the model number must be a whole word.
	if !cpuHas("intel(r) core(tm)2 duo cpu p8700 @ 2.53ghz", "p8700") || cpuHas("intel(r) core(tm) i7-27200qm", "i7-2720") {
		t.Error("cpuHas word boundaries")
	}
	for model, want := range map[string]string{"Core i7-2720QM": "i7-2720qm", "Dual Xeon 5150": "5150", "Xeon E5-1620 v2": "e5-1620 v2", "Core 2 Duo P8700": "p8700"} {
		if got := cpuToken(model); got != want {
			t.Errorf("cpuToken(%q) = %q, want %q", model, got, want)
		}
	}
}

// A part costs the same however many IDs it's known by (the HD 3000 has two).
func TestNotReportedPerPart(t *testing.T) {
	r := matcher(t).Match(Probe{ProductName: "MacBookPro8,2", PCI: []string{"1002:6760"}})
	best := r.Candidates[0]
	if !r.Exact || best.Config != "macbookpro8-2-15-early-2011-a" || best.Score != -10 || r.Candidates[1].Score != -30 {
		t.Fatalf("scores: %+v", r.Candidates)
	}
	want := []string{"pci:8086:0116/pci:8086:0126", "pci:14e4:4331"}
	if !reflect.DeepEqual(best.NotReported, want) {
		t.Errorf("not reported: %v, want %v", best.NotReported, want)
	}
}
