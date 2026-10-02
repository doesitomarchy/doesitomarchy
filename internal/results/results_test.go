package results

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

var now = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

func loadCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSyntheticFixture(t *testing.T) {
	c := loadCatalog(t)
	b, err := os.ReadFile("fixtures/mbp152-synthetic.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Validate(f, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if r.ConfigID != "macbookpro15-2-13-2018-4tb3-a" || r.Identifier != "MacBookPro15,2" || r.SourceID != "manual" ||
		r.Omarchy.String() != "4.0.4" || r.TestedAt != "2026-09-30T18:05:00Z" || r.TesterHandle != "synthetic-fixture" {
		t.Fatalf("header: %+v", r)
	}
	counts := map[string]int{}
	for _, it := range r.Items {
		counts[it.Status]++
		if !it.Applicable {
			t.Errorf("%s should apply", it.Capability)
		}
	}
	if counts["supported"] != 19 || counts["partial"] != 3 || counts["failed"] != 5 || counts["not_tested"] != 3 {
		t.Errorf("status counts: %v", counts)
	}
	if r.Items[0].Capability != "boot.installer-efi64" || r.Items[len(r.Items)-1].Capability != "bridge.touch-bar-camera" {
		t.Error("items should be in catalog order")
	}
	if len(r.Flags) != 0 || len(r.Extras) != 1 {
		t.Errorf("flags %v extras %v", r.Flags, r.Extras)
	}
	// Personal data never survives validation.
	all := r.Notes + r.Hardware
	for _, leak := range []string{"C02XG0FDH7JY", "a4:83:e7:12:34:56", "work-mbp", "/home/carl", "192.168.1.42", "carl@"} {
		if strings.Contains(all, leak) {
			t.Errorf("%q leaked into %q", leak, all)
		}
	}
	if !strings.Contains(r.Hardware, "Mac-827FB448E656EC26") || !strings.Contains(r.Hardware, "8086:3e9b") {
		t.Errorf("useful hardware facts were scrubbed: %s", r.Hardware)
	}
}

func valid() string {
	return `schema: doesitomarchy/report/v1
config: macbookpro15-2-13-2018-4tb3-a
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4" }
items:
  boot.install: { status: supported, method: observed }
`
}

func TestValidateErrors(t *testing.T) {
	c := loadCatalog(t)
	tests := []struct {
		name, yaml string
		want       []string // substrings of the error
	}{
		{"wrong schema", strings.Replace(valid(), "v1", "v9", 1), []string{"schema:"}},
		{"unknown config", strings.Replace(valid(), "macbookpro15-2-13-2018-4tb3-a", "macbookpro99-1-x", 1), []string{"config:", "not a known"}},
		{"missing config", strings.Replace(valid(), "config: macbookpro15-2-13-2018-4tb3-a\n", "", 1), []string{"config: required"}},
		{"bad date", strings.Replace(valid(), "2026-10-01T12:00:00Z", "01/10/2026", 1), []string{"tested_at"}},
		{"date without a time", strings.Replace(valid(), "2026-10-01T12:00:00Z", "2026-10-01", 1), []string{"tested_at", "time zone"}},
		{"time without a zone", strings.Replace(valid(), "2026-10-01T12:00:00Z", "2026-10-01T12:00:00", 1), []string{"tested_at", "time zone"}},
		{"future date", strings.Replace(valid(), "2026-10-01T12:00:00Z", "2026-10-03T13:00:00Z", 1), []string{"in the future"}},
		{"pre-Omarchy date", strings.Replace(valid(), "2026-10-01T12:00:00Z", "2019-05-01T00:00:00Z", 1), []string{"before Omarchy"}},
		{"bad version", strings.Replace(valid(), `"4.0.4"`, `"four"`, 1), []string{"omarchy.version"}},
		{"unknown capability", valid() + "  audio.kazoo: { status: supported, method: observed }\n", []string{"items.audio.kazoo", "unknown capability"}},
		{"per-connector item", valid() + "  ports.usb-c@left-1: { status: supported, method: fixture }\n", []string{"per-connector"}},
		{"bad status", valid() + "  audio.speakers: { status: works, method: observed }\n", []string{"items.audio.speakers.status"}},
		{"missing method", valid() + "  audio.speakers: { status: failed }\n", []string{"items.audio.speakers.method", "required"}},
		{"reason on a tested item", valid() + "  audio.speakers: { status: failed, method: observed, reason: uncertain }\n", []string{"only not_tested"}},
		{"bad skip reason", valid() + "  audio.speakers: { status: not_tested, reason: lazy }\n", []string{"items.audio.speakers.reason"}},
		{"bad handle", valid() + "tester: { handle: \"<script>\" }\n", []string{"tester.handle"}},
		{"nothing to record", strings.Split(valid(), "items:")[0], []string{"nothing to record"}},
		{"duplicate extra", valid() + "extras:\n  - { id: a }\n  - { id: a }\n", []string{"appears twice"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := Parse([]byte(tt.yaml))
			if err != nil {
				t.Fatal(err)
			}
			_, err = Validate(f, c, now)
			if err == nil || !IsValidation(err) {
				t.Fatalf("want a validation error, got %v", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

func TestValidateCollectsEveryError(t *testing.T) {
	f, err := Parse([]byte("schema: nope\nconfig: nope\ntested_at: x\nomarchy: { version: y }\nitems: { boot.install: { status: z } }\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Validate(f, loadCatalog(t), now)
	if n := len(err.(Errors)); n < 5 {
		t.Fatalf("want every problem reported at once, got %d: %v", n, err)
	}
}

func TestInapplicableAndRetired(t *testing.T) {
	c := loadCatalog(t)
	// graphics.discrete does not apply to the 13-inch MacBookPro15,2 (integrated GPU only).
	f, _ := Parse([]byte(valid() + "  graphics.discrete: { status: supported, method: automatic }\n"))
	r, err := Validate(f, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Flags) != 1 || r.Flags[0].Kind != FlagInapplicable || !strings.Contains(r.Flags[0].Detail, "does not apply") {
		t.Fatalf("flags: %+v", r.Flags)
	}
	for _, it := range r.Items {
		if it.Capability == "graphics.discrete" && it.Applicable {
			t.Error("an inapplicable item is stored but must not count")
		}
	}
	// A retired criterion is stored and flagged the same way.
	for i := range c.Capabilities {
		if c.Capabilities[i].ID == "boot.install" {
			c.Capabilities[i].Retired = true
		}
	}
	f, _ = Parse([]byte(valid()))
	r, err = Validate(f, c, now)
	if err != nil || len(r.Flags) != 1 || !strings.Contains(r.Flags[0].Detail, "retired") {
		t.Fatalf("retired: %v %+v", err, r)
	}
}

func TestAliasResolves(t *testing.T) {
	c := loadCatalog(t)
	var alias, canonical string
	for _, m := range c.Macs {
		for _, rel := range m.Releases {
			for _, cfg := range rel.Configs {
				if len(cfg.Aliases) > 0 && alias == "" {
					alias, canonical = cfg.Aliases[0], cfg.ID
				}
			}
		}
	}
	if alias == "" {
		t.Skip("no config has an alias")
	}
	if _, cfg := findConfig(c, alias); cfg == nil || cfg.ID != canonical {
		t.Fatalf("alias %s should resolve to %s", alias, canonical)
	}
}

func TestParse(t *testing.T) {
	if _, err := Parse([]byte(valid() + "colour: red\n")); err == nil {
		t.Error("unknown fields must be rejected")
	}
	if _, err := Parse([]byte("  \n")); err == nil {
		t.Error("empty input must be rejected")
	}
	if _, err := Parse(make([]byte, MaxSize+1)); err == nil {
		t.Error("oversized input must be rejected")
	}
	js := `{"schema":"doesitomarchy/report/v1","config":"macbookpro15-2-13-2018-4tb3-a","tested_at":"2026-10-01T08:00:00-04:00",
	        "omarchy":{"version":"4.0.4"},"items":{"boot.install":{"status":"supported","method":"observed"}}}`
	f, err := Parse([]byte(js))
	if err != nil {
		t.Fatalf("JSON should parse: %v", err)
	}
	if _, err := Validate(f, loadCatalog(t), now); err != nil {
		t.Fatalf("JSON should validate: %v", err)
	}
}

func TestScrub(t *testing.T) {
	tests := []struct{ in, gone, kept string }{
		{"Serial Number (system): C02XG0FDH7JY", "C02XG0FDH7JY", "Serial Number (system): "},
		{"product_serial=C02XG0FDH7JY", "C02XG0FDH7JY", "product_serial="},
		{`"board_serial": "C0290240EGSGHCFA"`, "C0290240EGSGHCFA", "board_serial"},
		{"product_uuid: 1c9d4a2e-6a3b-4f6c-9a77-0123456789ab", "1c9d4a2e", "product_uuid"},
		{"link/ether a4:83:e7:12:34:56 brd ff:ff:ff:ff:ff:ff", "a4:83:e7:12:34:56", "link/ether"},
		{"wlan0: 00-1B-63-84-45-E6 up", "00-1B-63-84-45-E6", "wlan0"},
		{"inet 192.168.1.42/24", "192.168.1.42", "inet "},
		{"inet6 fe80::a6b1:c2ff:fe3d:1234/64", "fe80::a6b1", "inet6 "},
		{"inet6 2001:db8:85a3:0:0:8a2e:370:7334", "2001:db8", "inet6"},
		{"Hostname: work-mbp", "work-mbp", "Hostname: "},
		{"Static hostname: work-mbp", "work-mbp", "Static hostname"},
		{"cp /home/carl/logs/x.log", "carl", "/home/"},
		{"/Users/carl/Desktop", "carl", "/Users/"},
		{"carl@work-mbp:~$ lspci", "work-mbp", "lspci"},
		{"[carl@work-mbp ~]$ ls", "work-mbp", "ls"},
		{"contact me at carl@example.com", "carl@example.com", "contact me at"},
	}
	for _, tt := range tests {
		got := Scrub(tt.in)
		if strings.Contains(got, tt.gone) || !strings.Contains(got, tt.kept) {
			t.Errorf("Scrub(%q) = %q: want %q gone and %q kept", tt.in, got, tt.gone, tt.kept)
		}
	}
	// Things that look technical but aren't personal must survive.
	for _, keep := range []string{
		"snd_hda_intel 0000:00:1f.3: no codecs found",
		"kernel 6.16.2-arch1-1, Omarchy 4.0.4",
		"pci:10de:0647 and usb:05ac:8290",
		"resumed at 12:30:45",
		"getty@tty1.service started",
		"brcmfmac: brcmf_c_preinit_dcmds: Firmware: BCM4364/3 wl0: Mar 28 2021 22:55:10",
		"std::vector in Foo::bar()",
	} {
		if got := Scrub(keep); got != keep {
			t.Errorf("Scrub(%q) = %q: should be unchanged", keep, got)
		}
	}
}

func TestScrubValue(t *testing.T) {
	in := map[string]any{"serial_number": "C02X", "MAC": "a4:83:e7:12:34:56", "ip": "10.0.0.2", "username": "carl",
		"board_id": "Mac-827FB448E656EC26", "nested": []any{map[string]any{"hostname": "work-mbp", "kernel": "6.16"}, "Hostname: x"}}
	out := ScrubValue(in).(map[string]any)
	for _, k := range []string{"serial_number", "MAC", "ip", "username"} {
		if out[k] != Redacted {
			t.Errorf("%s = %v", k, out[k])
		}
	}
	if out["board_id"] != "Mac-827FB448E656EC26" {
		t.Error("board_id is not personal")
	}
	nested := out["nested"].([]any)
	if nested[0].(map[string]any)["hostname"] != Redacted || nested[0].(map[string]any)["kernel"] != "6.16" || nested[1] != "Hostname: "+Redacted {
		t.Errorf("nested: %v", nested)
	}
}

func TestTestedAtIsUTC(t *testing.T) {
	f, _ := Parse([]byte(strings.Replace(valid(), "2026-10-01T12:00:00Z", "2026-10-01T22:30:00-04:00", 1)))
	r, err := Validate(f, loadCatalog(t), now)
	if err != nil || r.TestedAt != "2026-10-02T02:30:00Z" {
		t.Fatalf("tested_at should be stored in UTC: %q %v", r.TestedAt, err)
	}
}
