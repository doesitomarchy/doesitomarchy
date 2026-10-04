package results

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/data"
)

// The OmacDiag adapter end to end (PLAN.md §24.2): a MacBookPro11,3 report
// with a PCIe camera that has no driver, a retaken check, and personal data.
func TestOmacDiag(t *testing.T) {
	c := loadCatalog(t)
	mp, err := LoadMapping(data.FS, "omacdiag", c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("fixtures/omacdiag-mbp113.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := FromOmacDiag(raw, c, mp, ImportOptions{}); !errors.Is(err, ErrNoOmarchyVersion) {
		t.Fatalf("without an Omarchy version: %v", err)
	}
	conv, err := FromOmacDiag(raw, c, mp, ImportOptions{Omarchy: "4.0.4", Tester: "carl"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Validate(conv.File, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.ConfigID != "macbookpro11-3-15-late-2013-a" || r.SourceID != "omacdiag" || r.Workflow != "guided" || r.TestedAt != "2026-10-02T16:53:20Z" || r.Kernel != "6.17.1-arch1-1" {
		t.Errorf("result: config %s source %s workflow %s tested %s kernel %s", r.ConfigID, r.SourceID, r.Workflow, r.TestedAt, r.Kernel)
	}
	want := map[string]string{ // criterion → status/method or not_tested/reason
		"boot.install":              "supported/automatic",
		"graphics.integrated":       "supported/automatic",
		"graphics.discrete":         "supported/automatic",
		"camera.builtin":            "failed/automatic",
		"ports.usb-a":               "supported/fixture",
		"ports.sd-card":             "supported/fixture",
		"audio.speakers":            "supported/observed",
		"audio.display-out":         "not_tested/no-equipment",
		"audio.microphone":          "supported/observed",
		"audio.headphone":           "not_tested/no-equipment",
		"display.native-resolution": "supported/observed",
		"graphics.external-display": "not_tested/no-equipment",
		"power.battery-status":      "not_tested/uncertain",
		"power.charging":            "supported/observed",
		"network.wifi":              "supported/observed",
		"network.bluetooth":         "supported/observed",
		"input.keyboard":            "supported/observed",
		"input.trackpad":            "supported/observed", // the retake, not the failed first attempt
		"input.keyboard-backlight":  "supported/observed",
		"thermal.sensors":           "not_tested/uncertain",
		"thermal.fans":              "not_tested/uncertain",
		"ports.thunderbolt":         "not_tested/no-equipment",
	}
	got := map[string]string{}
	for _, it := range r.Items {
		v := it.Status + "/" + it.Method
		if it.Status == "not_tested" {
			v = it.Status + "/" + it.Reason
		}
		got[it.Capability] = v
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%d items, want %d: %v", len(got), len(want), got)
	}
	var extras []string
	for _, x := range r.Extras {
		extras = append(extras, x.ID)
	}
	if strings.Join(extras, " ") != "cpu:system/compute memory:system/patterns network:enx001122334455/link power:ADP1/supply storage:sda/health storage:sda/read" {
		t.Errorf("extras: %v", extras)
	}
	if len(conv.Flags) != 1 || conv.Flags[0].Kind != FlagDriverMissing || !strings.Contains(conv.Flags[0].Detail, "camera.builtin") {
		t.Errorf("flags: %+v", conv.Flags)
	}
	// Nothing personal survives in the stored report or the evidence.
	stored := string(conv.Raw)
	for _, it := range r.Items {
		stored += it.Evidence
	}
	for _, x := range r.Extras {
		stored += x.Detail
	}
	for _, leak := range []string{"C02LK1ABFD57", "S1K5NYAF123456", "000000000820", "a4:5e:60:12:34:56", "a4:5e:60:ab:cd:ef",
		"AA:BB:CC:DD:EE:FF", "192.168.1.20", "/media/carl", "00:11:22:33:44:55"} {
		if strings.Contains(stored, leak) {
			t.Errorf("%q survives", leak)
		}
	}
	if !strings.Contains(stored, "MacBookPro11,3") || !strings.Contains(stored, "0x0d26") {
		t.Error("the hardware facts are kept")
	}
}

func TestOmacDiagRefusals(t *testing.T) {
	c := loadCatalog(t)
	mp, _ := LoadMapping(data.FS, "omacdiag", c)
	for name, raw := range map[string]string{
		"not JSON":       "schema: doesitomarchy/report/v1",
		"our own schema": `{"schema": "doesitomarchy/report/v1"}`,
		"newer schema":   `{"schema_version": 2, "inventory": {"machine": {"model": "MacBookPro11,3"}}}`,
	} {
		if _, err := FromOmacDiag([]byte(raw), c, mp, ImportOptions{Omarchy: "4.0.4"}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The version can come from the report itself.
	raw, _ := os.ReadFile("fixtures/omacdiag-mbp113.json")
	withVersion := strings.Replace(string(raw), `"distribution": "Omarchy",`, `"distribution": "Omarchy", "os_version": "4.0.5",`, 1)
	conv, err := FromOmacDiag([]byte(withVersion), c, mp, ImportOptions{})
	if err != nil || conv.File.Omarchy.Version != "4.0.5" {
		t.Errorf("os_version from the report: %v %+v", err, conv)
	}
}

// The rules Carl's real runs prompted (PLAN §28.3–28.5), on the fixture.
func TestOmacDiagRunRules(t *testing.T) {
	c := loadCatalog(t)
	mp, err := LoadMapping(data.FS, "omacdiag", c)
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.ReadFile("fixtures/omacdiag-mbp113.json")
	if err != nil {
		t.Fatal(err)
	}
	// convert edits the fixture: backlight results replace its passed one;
	// devices and results are added.
	convert := func(backlight []map[string]any, devices, extra []map[string]any) (*File, []Flag) {
		t.Helper()
		var od map[string]any
		if err := json.Unmarshal(base, &od); err != nil {
			t.Fatal(err)
		}
		var res []any
		for _, r := range od["results"].([]any) {
			if !strings.HasPrefix(r.(map[string]any)["test_id"].(string), "keyboard_backlight:") {
				res = append(res, r)
			}
		}
		for _, r := range append(backlight, extra...) {
			res = append(res, r)
		}
		od["results"] = res
		inv := od["inventory"].(map[string]any)
		for _, d := range devices {
			inv["devices"] = append(inv["devices"].([]any), d)
		}
		raw, _ := json.Marshal(od)
		conv, err := FromOmacDiag(raw, c, mp, ImportOptions{Omarchy: "4.0.4"})
		if err != nil {
			t.Fatal(err)
		}
		return conv.File, conv.Flags
	}
	bl := "keyboard_backlight:smc::kbd_backlight"
	result := func(id, device, outcome, reason string) map[string]any {
		return map[string]any{"test_id": id, "device_id": device, "outcome": outcome, "reason_code": reason, "summary": "x"}
	}

	// A pass, then a retake OmacDiag couldn't run: the pass counts (§28.4).
	f, _ := convert([]map[string]any{result(bl+"/visual", bl, "passed", "operator_assessment"),
		result(bl+"/visual/retake-1", bl, "blocked", "backlight_control_unavailable")}, nil, nil)
	if it := f.Items["input.keyboard-backlight"]; it.Status != "supported" {
		t.Errorf("pass then blocked retake: %+v", it)
	}

	// A "missing driver" reason on a backlight whose LED device is there:
	// OmacDiag's check failed, not the driver (§28.3). Not tested, no flag.
	f, flags := convert([]map[string]any{result(bl+"/visual", bl, "blocked", "backlight_control_unavailable")}, nil, nil)
	if it := f.Items["input.keyboard-backlight"]; it.Status != "not_tested" || it.Reason != "uncertain" {
		t.Errorf("blocked backlight with its device present: %+v", it)
	}
	for _, fl := range flags {
		if fl.Kind == FlagDriverMissing && strings.Contains(fl.Detail, "keyboard-backlight") {
			t.Errorf("driver_missing for a present backlight: %s", fl.Detail)
		}
	}

	// An Ethernet adapter on the Thunderbolt port (§28.5): its passing link
	// proves Thunderbolt works, and isn't built-in Ethernet.
	tb := "/sys/devices/pci0000:00/0000:00:1c.4/0000:06:00.0"
	devices := []map[string]any{
		{"id": "thunderbolt:domain0", "kind": "thunderbolt", "name": "domain0", "recognition": "detected",
			"attributes": map[string]string{"sysfs_target": tb + "/0000:07:00.0/0000:08:00.0/domain0"}},
		{"id": "network:ens9", "kind": "network", "name": "ens9", "recognition": "detected",
			"attributes": map[string]string{"wireless": "false", "carrier": "1", "sysfs_target": tb + "/0000:07:03.0/0000:0a:00.0/net/ens9"}},
	}
	f, _ = convert(nil, devices, []map[string]any{result("network:ens9/link", "network:ens9", "passed", "link_present")})
	if it := f.Items["ports.thunderbolt"]; it.Status != "supported" || it.Method != "automatic" || !strings.Contains(it.Evidence, "network:ens9 is attached through Thunderbolt") {
		t.Errorf("Thunderbolt-attached Ethernet: %+v", it)
	}
	if it, ok := f.Items["network.ethernet"]; ok && strings.Contains(it.Evidence, "ens9") {
		t.Errorf("Thunderbolt adapter counted as built-in Ethernet: %+v", it)
	}
}
