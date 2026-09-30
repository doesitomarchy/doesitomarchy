package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Real catalog ────────────────────────────────────────────────────────────

func loadReal(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load(filepath.Join("..", "..", "data"))
	if err != nil {
		t.Fatalf("data/ must validate:\n%v", err)
	}
	return c
}

func findConfig(t *testing.T, c *Catalog, id string) (*Mac, *Config) {
	t.Helper()
	for _, m := range c.Macs {
		for ri := range m.Releases {
			for ci := range m.Releases[ri].Configs {
				if cfg := &m.Releases[ri].Configs[ci]; cfg.ID == id {
					return m, cfg
				}
			}
		}
	}
	t.Fatalf("config %s not found", id)
	return nil, nil
}

func capSet(c *Catalog, m *Mac, cfg *Config) map[string]bool {
	out := map[string]bool{}
	for _, cap := range c.Applicable(m, cfg) {
		out[cap.ID] = true
	}
	return out
}

func TestRealCatalogApplicability(t *testing.T) {
	c := loadReal(t)
	tests := []struct {
		config   string
		has, not []string
	}{
		{"macmini2-1-mid-2007-a",
			[]string{"boot.installer-efi32", "graphics.integrated", "ports.firewire", "storage.optical", "input.ir-receiver"},
			[]string{"boot.installer-efi64", "graphics.discrete", "ports.sd-card", "audio.display-out"}},
		{"macmini3-1-late-2009-a", []string{"storage.optical", "audio.optical-out"}, nil},
		{"macmini3-1-late-2009-server", nil, []string{"storage.optical"}},
		{"macmini5-2-mid-2011-a",
			[]string{"graphics.discrete", "ports.thunderbolt", "audio.display-out", "audio.headset-mic"},
			[]string{"graphics.integrated", "graphics.switching"}},
		{"macmini8-1-2018-a",
			[]string{"boot.installer-efi64", "ports.usb-c", "ports.thunderbolt", "network.ethernet", "network.wifi", "audio.headphone"},
			[]string{"ports.sd-card", "input.ir-receiver", "audio.line-in", "ports.firewire", "bridge.touch-bar-camera"}},
	}
	laptops := []struct {
		config   string
		has, not []string
	}{
		{"macbookair1-1-early-2008-a",
			[]string{"boot.installer-efi64", "graphics.external-display", "input.keyboard-backlight", "input.ambient-light-sensor", "power.lid", "camera.builtin"},
			[]string{"ports.thunderbolt", "network.ethernet", "ports.sd-card"}},
		{"macbookair3-1-late-2010-a", []string{"input.trackpad", "power.battery-status"}, []string{"input.keyboard-backlight", "ports.sd-card"}},
		{"macbookair7-2-2017-a", []string{"ports.sd-card", "ports.thunderbolt", "audio.microphone"}, []string{"input.touch-id", "ports.usb-c"}},
		{"imac5-1-17-late-2006-a", []string{"boot.installer-efi32", "input.ir-receiver", "storage.optical", "audio.line-in"}, []string{"boot.installer-efi64", "ports.thunderbolt"}},
		{"imac10-1-27-late-2009-a", []string{"audio.display-out", "ports.sd-card", "ports.firewire", "graphics.discrete"}, []string{"graphics.integrated", "power.lid"}},
		{"imac20-1-5k-2020-10gbe", []string{"network.ethernet", "ports.usb-c", "display.brightness"}, []string{"input.ir-receiver", "storage.optical", "audio.line-in", "input.keyboard"}},
		{"macbook5-1-late-2008-b", []string{"input.keyboard-backlight", "graphics.external-display", "storage.optical", "audio.optical-in"}, []string{"ports.firewire", "input.force-touch"}},
		{"macbook5-1-late-2008-a", []string{"input.trackpad"}, []string{"input.keyboard-backlight"}},
		{"macbook10-1-2017-a", []string{"input.force-touch", "ports.usb-c", "ports.usb-c-charging", "input.ambient-light-sensor", "thermal.sensors"}, []string{"thermal.fans", "ports.usb-a", "network.ethernet", "storage.optical"}},
		{"macpro1-1-2006-a", []string{"boot.installer-efi32", "ports.firewire", "storage.optical", "audio.optical-in"}, []string{"network.wifi", "display.brightness"}},
		{"macpro5-1-mid-2010-a", []string{"network.wifi", "network.bluetooth", "graphics.discrete", "ports.firewire"}, []string{"graphics.integrated", "ports.thunderbolt"}},
		{"macpro7-1-2019-w6900x", []string{"ports.thunderbolt", "network.ethernet", "graphics.discrete"}, []string{"storage.optical", "ports.sd-card", "display.native-resolution"}},
		{"macbookair9-1-2020-b",
			[]string{"input.touch-id", "input.force-touch", "ports.usb-c", "ports.usb-c-charging", "network.bluetooth"},
			[]string{"ports.usb-a", "ports.sd-card", "input.touch-bar", "bridge.touch-bar-camera", "graphics.discrete"}},
		{"macbookpro2-2-15-late-2006-a",
			[]string{"boot.installer-efi32", "ports.expresscard", "ports.firewire", "audio.optical-in", "input.ir-receiver", "input.keyboard-backlight"},
			[]string{"boot.installer-efi64", "graphics.switching", "ports.sd-card"}},
		{"macbookpro5-3-15-mid-2009-a",
			[]string{"graphics.switching", "graphics.power-down", "graphics.integrated", "graphics.discrete", "ports.sd-card", "audio.line-in"},
			[]string{"ports.expresscard", "audio.display-out"}},
		{"macbookpro5-4-15-2-53ghz-mid-2009-a", []string{"graphics.integrated", "ports.sd-card"}, []string{"graphics.discrete", "graphics.switching"}},
		{"macbookpro8-3-17-late-2011-a", []string{"ports.expresscard", "ports.thunderbolt", "graphics.switching", "storage.optical"}, []string{"ports.sd-card", "ports.usb-c"}},
		{"macbookpro10-1-15-mid-2012-a",
			[]string{"audio.display-out", "audio.optical-out", "ports.sd-card", "graphics.switching"},
			[]string{"network.ethernet", "ports.firewire", "storage.optical", "input.ir-receiver", "audio.line-in"}},
		{"macbookpro13-1-13-2016-2tb3-a",
			[]string{"ports.usb-c", "ports.usb-c-charging", "input.force-touch", "camera.builtin"},
			[]string{"input.touch-bar", "input.touch-id", "ports.usb-a", "ports.sd-card", "graphics.discrete"}},
		{"macbookpro14-3-15-2017-a",
			[]string{"input.touch-bar", "input.touch-id", "graphics.switching", "ports.thunderbolt"},
			[]string{"bridge.touch-bar-camera", "audio.optical-out", "ports.sd-card"}},
		{"macbookpro16-4-16-2019-5600m-a",
			[]string{"input.touch-bar", "bridge.touch-bar-camera", "graphics.discrete", "graphics.switching"},
			[]string{"ports.usb-a", "network.ethernet", "storage.optical"}},
	}
	for _, tt := range laptops {
		t.Run(tt.config, func(t *testing.T) {
			m, cfg := findConfig(t, c, tt.config)
			got := capSet(c, m, cfg)
			for _, id := range tt.has {
				if !got[id] {
					t.Errorf("expected %s to apply", id)
				}
			}
			for _, id := range tt.not {
				if got[id] {
					t.Errorf("expected %s NOT to apply", id)
				}
			}
		})
	}
	laptopOnly := []string{"power.battery-status", "power.lid", "input.keyboard", "input.trackpad", "display.brightness", "camera.builtin"}
	for _, tt := range tests {
		t.Run(tt.config, func(t *testing.T) {
			m, cfg := findConfig(t, c, tt.config)
			got := capSet(c, m, cfg)
			for _, id := range tt.has {
				if !got[id] {
					t.Errorf("expected %s to apply", id)
				}
			}
			for _, id := range append(tt.not, laptopOnly...) {
				if got[id] {
					t.Errorf("expected %s NOT to apply", id)
				}
			}
			if !got["boot.install"] || !got["power.sleep-wake"] {
				t.Error("universal capabilities must always apply")
			}
		})
	}
}

func TestRealCatalogHardBlocker(t *testing.T) {
	c := loadReal(t)
	for _, m := range c.Macs {
		switch m.Identifier {
		case "Macmini1,1", "MacBook1,1", "MacBookPro1,1", "MacBookPro1,2":
			if m.HardBlocker == "" {
				t.Errorf("%s (Yonah) must have a hard_blocker", m.Identifier)
			}
		}
	}
}

// ── Validation rules, against a minimal fixture ─────────────────────────────

var fixture = map[string]string{
	"vocabulary.yaml": `
lines:
  mac-mini: { name: Mac mini, form: desktop, identifier_prefix: Macmini }
security_chips:
  none: { name: None }
component_kinds:
  gpu: { name: Graphics }
  wifi: { name: Wi-Fi }
cpu_codenames:
  yonah: { name: Yonah, family: yonah, bits: 32 }
  penryn: { name: Penryn, family: core, bits: 64 }
ports:
  usb-a-2: { name: USB 2.0 }
  hdmi: { name: HDMI, video: true }
features:
  fan: { name: Fan }
  builtin-display: { name: Built-in display }
`,
	"capabilities.yaml": `
categories:
  - { id: boot, name: Boot, blocking: true }
  - { id: graphics, name: Graphics }
capabilities:
  - { id: boot.install, name: Install completes }
  - id: graphics.external-display
    name: External display
    when: { all: ["video-out"] }
`,
	"components/gpu.yaml": `
- id: gpu/test-gpu
  name: Test GPU
  vendor: Intel
  role: integrated
  ids: [pci:8086:0001]
  sources: [https://example.com/gpu]
`,
	"macs/Macmini9-9.yaml": `
identifier: Macmini9,9
line: mac-mini
efi: 64
sources: [https://example.com/mini]
releases:
  - id: mid-2099
    name: Mac mini (Mid 2099)
    announced: 2099-06-01
    model_numbers: [A1234]
    emc: ["1234"]
    configs:
      - id: macmini9-9-mid-2099-a
        label: Test config
        order_numbers: [MB463LL/A]
        cpu: { codename: penryn, standard: [{ model: Core 2 Duo P7350, ghz: 2.0, cores: 2 }] }
        memory: { type: DDR3, standard_gb: [2], max_gb: 8 }
        storage: { interface: sata, standard: [120 GB HDD] }
        components: [gpu/test-gpu]
        ports: { usb-a-2: 2, hdmi: 1 }
        features: [fan]
`,
	"config-ids.lock": "macmini9-9-mid-2099-a\n",
}

// writeFixture writes the fixture with optional edits: file → (old → new).
func writeFixture(t *testing.T, edits map[string][2]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range fixture {
		if e, ok := edits[name]; ok {
			if !strings.Contains(body, e[0]) {
				t.Fatalf("fixture %s does not contain %q", name, e[0])
			}
			body = strings.Replace(body, e[0], e[1], 1)
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestEMCRevisionSuffix(t *testing.T) {
	if _, err := Load(writeFixture(t, map[string][2]string{"macs/Macmini9-9.yaml": {`emc: ["1234"]`, `emc: ["1234-1"]`}})); err != nil {
		t.Fatalf("an EMC number with a -N revision should validate:\n%v", err)
	}
}

func TestFixtureIsValid(t *testing.T) {
	c, err := Load(writeFixture(t, nil))
	if err != nil {
		t.Fatalf("fixture should validate:\n%v", err)
	}
	m, cfg := c.Macs[0], &c.Macs[0].Releases[0].Configs[0]
	if got := capSet(c, m, cfg); !got["graphics.external-display"] || !got["boot.install"] {
		t.Errorf("unexpected applicability: %v", got)
	}
}

func TestValidationRules(t *testing.T) {
	const mac = "macs/Macmini9-9.yaml"
	tests := []struct {
		name  string
		file  string
		old   string
		new   string
		wants string
	}{
		{"unknown field", mac, "efi: 64", "efi: 64\nfirmware: 64", "unknown field"},
		{"unknown component", mac, "components: [gpu/test-gpu]", "components: [gpu/test-gpu, wifi/nope]", `unknown component "wifi/nope"`},
		{"no gpu", mac, "components: [gpu/test-gpu]", "components: []", "at least one gpu"},
		{"bad order number", mac, "MB463LL/A", "MB463", "must look like MB463LL/A"},
		{"missing mac source", mac, "sources: [https://example.com/mini]", "sources: []", "at least one source"},
		{"bad date", mac, "announced: 2099-06-01", "announced: June 2099", "YYYY-MM-DD"},
		{"32-bit cpu without blocker", mac, "codename: penryn", "codename: yonah", "requires hard_blocker"},
		{"identifier/line mismatch", mac, "identifier: Macmini9,9", "identifier: iMac9,9", "does not match"},
		{"config id prefix", mac, "id: macmini9-9-mid-2099-a", "id: imac-mid-2099-a", "starting with"},
		{"unknown port", mac, "usb-a-2: 2", "usb-z: 2", `unknown port class "usb-z"`},
		{"unknown feature", mac, "features: [fan]", "features: [fan, jetpack]", `unknown feature "jetpack"`},
		{"display without feature", mac, "features: [fan]", "features: [fan]\n        display: { inches: 13, resolution: 1280x800 }", "display is required exactly"},
		{"unlocked config", "config-ids.lock", "macmini9-9-mid-2099-a\n", "", "not locked yet"},
		{"removed config", "config-ids.lock", "macmini9-9-mid-2099-a\n", "macmini9-9-mid-2099-a\nmacmini9-9-old\n", "was removed"},
		{"unknown tag", "capabilities.yaml", `["video-out"]`, `["feature:jetpack"]`, `unknown tag "feature:jetpack"`},
		{"gpu without role", "components/gpu.yaml", "role: integrated", "role: ''", "gpu role"},
		{"bad hardware id", "components/gpu.yaml", "pci:8086:0001", "8086:0001", "must look like pci:"},
		{"duplicate key", mac, "efi: 64", "efi: 64\nefi: 32", "already defined"},
		{"bad emc", mac, `emc: ["1234"]`, `emc: ["1234-12"]`, "must be four digits"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeFixture(t, map[string][2]string{tt.file: {tt.old, tt.new}}))
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wants)
			}
			if !strings.Contains(err.Error(), tt.wants) {
				t.Fatalf("error should contain %q, got:\n%v", tt.wants, err)
			}
		})
	}
}

func TestMisnamedMacFile(t *testing.T) {
	dir := writeFixture(t, nil)
	if err := os.Rename(filepath.Join(dir, "macs", "Macmini9-9.yaml"), filepath.Join(dir, "macs", "mini.yaml")); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "file must be named Macmini9-9.yaml") {
		t.Fatalf("expected file-name error, got %v", err)
	}
}

func TestLoadUnlockedStillRejectsRemovedIDs(t *testing.T) {
	dir := writeFixture(t, map[string][2]string{"config-ids.lock": {"macmini9-9-mid-2099-a\n", "gone-id\n"}})
	_, err := LoadUnlocked(dir)
	if err == nil || !strings.Contains(err.Error(), "was removed") || strings.Contains(err.Error(), "not locked yet") {
		t.Fatalf("LoadUnlocked should only report the removed ID, got %v", err)
	}
}

func TestLoadMissingDir(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing dir")
	}
}

// ── Lock file ───────────────────────────────────────────────────────────────

func TestWriteLockIsAppendOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFile)
	added, err := WriteLock(path, []string{"b", "a"})
	if err != nil || strings.Join(added, ",") != "a,b" {
		t.Fatalf("first write: added=%v err=%v", added, err)
	}
	added, err = WriteLock(path, []string{"c"}) // "a" and "b" no longer passed in
	if err != nil || strings.Join(added, ",") != "c" {
		t.Fatalf("second write: added=%v err=%v", added, err)
	}
	ids, err := ReadLock(path)
	if err != nil || strings.Join(ids, ",") != "a,b,c" {
		t.Fatalf("lock must keep every ID ever written, got %v (err %v)", ids, err)
	}
}

// ── Conditions ──────────────────────────────────────────────────────────────

func TestConditionMatches(t *testing.T) {
	tags := map[string]bool{"efi:64": true, "port:hdmi": true}
	tests := []struct {
		cond *Condition
		want bool
	}{
		{nil, true},
		{&Condition{All: []string{"efi:64"}}, true},
		{&Condition{All: []string{"efi:64", "port:sd-card"}}, false},
		{&Condition{Any: []string{"port:sd-card", "port:hdmi"}}, true},
		{&Condition{Any: []string{"port:sd-card"}}, false},
		{&Condition{None: []string{"port:hdmi"}}, false},
		{&Condition{All: []string{"efi:64"}, None: []string{"chip:t2"}}, true},
	}
	for i, tt := range tests {
		if got := tt.cond.Matches(tags); got != tt.want {
			t.Errorf("case %d: got %v, want %v", i, got, tt.want)
		}
	}
}
