package match

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	pm "github.com/doesitomarchy/doesitomarchy/pkg/match"
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
const igpu = "8086:0126"

var matchCases = []struct {
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

func TestMatch(t *testing.T) {
	m := matcher(t)
	for _, tt := range matchCases {
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

func TestKnown(t *testing.T) {
	m := matcher(t)
	if !m.KnownBoard("mac-94245a3940c91c80") || m.KnownBoard("Mac-0000000000000000") {
		t.Error("KnownBoard")
	}
	if !m.KnownDevice("1002:6760", "pci") || !m.KnownDevice("pci:8086:0126", "pci") || m.KnownDevice("ffff:0001", "pci") {
		t.Error("KnownDevice")
	}
	if !m.KnownCPU("Intel(R) Core(TM) i7-2720QM CPU @ 2.20GHz") || m.KnownCPU("Intel(R) Core(TM) i9-99999 CPU") {
		t.Error("KnownCPU")
	}
}

// The owner's iMac10,1 share (2026-10-03): a 21.5-inch with the HD 4670,
// which the 27-inch also has. Only the board ID tells them apart (PLAN.md §29).
var ownerIMac = Probe{ProductName: "iMac10,1", BoardID: "Mac-F2268CC8", CPU: "Intel(R) Core(TM)2 Duo CPU     E7600  @ 3.06GHz",
	PCI: []string{"1002:9488", "1002:aa38", "104c:823e", "104c:823f", "10de:0a84", "10de:0a88", "10de:0a89", "10de:0a98", "10de:0aa2",
		"10de:0aa3", "10de:0aa4", "10de:0aa5", "10de:0aa6", "10de:0aa7", "10de:0aa9", "10de:0aab", "10de:0aac", "10de:0ab0", "10de:0ab9",
		"10de:0ac0", "10de:0ac4", "10de:0ac6", "10de:0ac7", "168c:002a"}}

func TestBoardBreaksTie(t *testing.T) {
	m := matcher(t)
	r := m.Match(ownerIMac)
	if !r.Exact || r.Best() != "imac10-1-21-late-2009-b" {
		t.Fatalf("the 21.5-inch board should decide: %+v", r.Candidates)
	}
	if !slices.Contains(r.Candidates[0].Matched, "board:Mac-F2268CC8") || r.Candidates[0].Score-r.Candidates[1].Score != 10 {
		t.Errorf("the board should add one matched ID: %+v", r.Candidates)
	}
	// Without the board, the two HD 4670 configs tie, as before.
	p := ownerIMac
	p.BoardID = ""
	if r := m.Match(p); r.Exact {
		t.Fatalf("no board: %+v", r.Candidates)
	}
	// The 27-inch board, found by board ID alone.
	r = m.Match(Probe{BoardID: "mac-f2268dc8", PCI: []string{"1002:9488"}})
	if r.By != "board_id" || !r.Exact || r.Best() != "imac10-1-27-late-2009-a" {
		t.Fatalf("27-inch board: %+v", r)
	}
	// iMac9,1: the 9400M and the E8135 are in both the 20- and 24-inch Early 2009.
	nine := Probe{ProductName: "iMac9,1", PCI: []string{"10de:0869"}, CPU: "Intel(R) Core(TM)2 Duo CPU     E8135  @ 2.66GHz"}
	if r := m.Match(nine); r.Exact {
		t.Fatalf("iMac9,1 without a board should tie: %+v", r.Candidates)
	}
	for board, want := range map[string]string{"Mac-F2218FA9": "imac9-1-24-early-2009-a", "Mac-F2218EA9": "imac9-1-20-early-2009-a"} {
		nine.BoardID = board
		if r := m.Match(nine); !r.Exact || r.Best() != want {
			t.Errorf("%s: %+v", board, r.Candidates)
		}
	}
	// A board tied to no release (MacBook2,1's Late 2006 one) changes nothing.
	r = m.Match(Probe{ProductName: "MacBook2,1", BoardID: "Mac-F4208CA9"})
	for _, c := range r.Candidates {
		if c.Score != 0 {
			t.Errorf("untied board scored: %+v", r.Candidates)
		}
	}
}

func TestPlumbing(t *testing.T) {
	m := matcher(t)
	if p, ok := m.Plumbing("10DE:0A84"); !ok || p.Name != "MCP79 Host Bridge" {
		t.Errorf("MCP79 host bridge: %+v %v", p, ok)
	}
	n := 0
	for _, id := range ownerIMac.PCI {
		if _, ok := m.Plumbing(id); ok {
			n++
		} else if !m.KnownDevice(id, "pci") {
			t.Errorf("%s is neither plumbing nor a component", id)
		}
	}
	if n != 19 {
		t.Errorf("plumbing IDs in the owner's share: %d, want 19", n)
	}
	if _, ok := m.Plumbing("1002:9488"); ok {
		t.Error("a GPU is never plumbing")
	}
}

// A tool matches on the snapshot it fetched as JSON (pkg/match): the
// matches must be the server's, for every probe these tests use.
func TestSnapshotRoundTrip(t *testing.T) {
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(Snapshot(c))
	if err != nil {
		t.Fatal(err)
	}
	var snap pm.Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		t.Fatal(err)
	}
	server, tool := New(c), pm.New(snap)
	probes := []Probe{ownerIMac, {ProductName: "MacBookPro8,2", PCI: []string{"8086:0126", "1002:6741"}, CPU: "Intel(R) Core(TM) i7-2675QM CPU @ 2.20GHz"},
		{BoardID: "mac-f2268dc8", PCI: []string{"1002:9488"}}, {ProductName: "iMac9,1", BoardID: "Mac-F2218FA9", PCI: []string{"10de:0869"}},
		{ProductName: "MacBook2,1", BoardID: "Mac-F4208CA9"}, {ProductName: "MacBookPro15,2", CPU: "Intel(R) Core(TM) i5-8259U CPU @ 2.30GHz"}}
	for _, tt := range matchCases {
		probes = append(probes, tt.probe)
	}
	for _, p := range probes {
		if got, want := tool.Match(p), server.Match(p); !reflect.DeepEqual(got, want) {
			t.Errorf("%+v:\n tool   %+v\n server %+v", p, got, want)
		}
	}
	for _, id := range ownerIMac.PCI {
		gp, gok := tool.Plumbing(id)
		wp, wok := server.Plumbing(id)
		if gp != wp || gok != wok || tool.KnownDevice(id, "pci") != server.KnownDevice(id, "pci") {
			t.Errorf("%s: plumbing or known device differs", id)
		}
	}
	if !tool.KnownBoard("Mac-F2268CC8") || !tool.KnownCPU(ownerIMac.CPU) {
		t.Error("the snapshot lost board IDs or CPUs")
	}
}
