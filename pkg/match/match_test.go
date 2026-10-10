package match

import (
	"reflect"
	"testing"
)

// Matching against the catalog is tested in internal/match, which builds
// the snapshot; these are the catalog-free parts.

func TestNormalizeIDs(t *testing.T) {
	got := NormalizeIDs([]string{"10DE:0647", "pci:10de:0647", "[8086:1C02] rev 05", "usb:05ac:8509", "nonsense", "1002:6741 and 14e4:4331"}, "pci")
	want := []string{"pci:10de:0647", "pci:8086:1c02", "usb:05ac:8509", "pci:1002:6741", "pci:14e4:4331"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCPUTokens(t *testing.T) {
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
