package results

import (
	"strings"
	"testing"
)

// Per-connector items (PLAN §25) on MacBookPro11,3, whose layout is
// left: MagSafe 2, Thunderbolt 2 ×2, USB 3, headphone/optical;
// right: SD, HDMI, USB 3.
func TestPerConnectorItems(t *testing.T) {
	c := loadCatalog(t)
	base := `schema: doesitomarchy/report/v1
config: macbookpro11-3-15-late-2013-a
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4" }
items:
  ports.usb-a@left-4: { status: supported, method: fixture }
  ports.usb-a@right-3: { status: failed, method: fixture, evidence: "no device enumerates" }
  graphics.external-display@right-2: { status: supported, method: observed }
  graphics.external-display@left-2: { status: supported, method: observed }
  audio.optical-out@left-5: { status: not_tested, reason: no-equipment }
  ports.usb-a: { status: supported, method: observed }
`
	f, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Validate(f, c, now)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range r.Items {
		got = append(got, it.Capability+"@"+it.Connector+"="+it.Status)
	}
	want := "graphics.external-display@left-2=supported graphics.external-display@right-2=supported audio.optical-out@left-5=not_tested " +
		"ports.usb-a@=supported ports.usb-a@left-4=supported ports.usb-a@right-3=failed"
	if strings.Join(got, " ") != want {
		t.Errorf("items:\n%s\nwant\n%s", strings.Join(got, " "), want)
	}

	// USB-A left-4 passed and right-3 failed: same controller, so a suspect port.
	if len(r.Flags) != 1 || r.Flags[0].Kind != FlagPortSuspect || !strings.Contains(r.Flags[0].Detail, "right-3 failed while left-4") {
		t.Errorf("flags: %+v", r.Flags)
	}

	for name, tc := range map[string]struct{ item, want string }{
		"no such connector":     {"ports.usb-a@left-9", `no connector "left-9"`},
		"wrong kind of port":    {"ports.usb-a@right-1", "isn't tested for ports.usb-a"},
		"power inlet":           {"ports.usb-a@left-1", "isn't tested"},
		"criterion not applied": {"ports.firewire@left-2", "isn't tested"},
	} {
		y := base + "  " + tc.item + ": { status: supported, method: fixture }\n"
		f, _ := Parse([]byte(y))
		if _, err := Validate(f, c, now); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A ports entry's connectors must be in the configuration's layout.
func TestPortsConnectors(t *testing.T) {
	c := loadCatalog(t)
	base := `schema: doesitomarchy/report/v1
config: macbookpro11-3-15-late-2013-a
tested_at: 2026-10-01T12:00:00Z
omarchy: { version: "4.0.4" }
items:
  ports.usb-a@left-4: { status: supported, method: fixture }
`
	f, err := Parse([]byte(base + `ports: [{ test: ports.usb-a, connector: left-4, paths: ["usb2:0000:00:14.0/1", "usb3:0000:00:14.0/1"], how: corrected, wiring_said: right-3 }]` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Validate(f, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Ports) != 1 || r.Ports[0].Connector != "left-4" || r.Ports[0].WiringSaid != "right-3" || len(r.Ports[0].Paths) != 2 {
		t.Errorf("ports: %+v", r.Ports)
	}
	for name, tc := range map[string]struct{ entry, want string }{
		"no such connector":      {`{ test: ports.usb-a, connector: left-9, paths: ["usb2:0000:00:14.0/1"], how: asked }`, `no connector "left-9"`},
		"no such wiring_said":    {`{ test: ports.usb-a, connector: left-4, paths: ["usb2:0000:00:14.0/1"], how: corrected, wiring_said: back-1 }`, `no connector "back-1"`},
		"a bad path still fails": {`{ test: ports.usb-a, connector: left-4, paths: ["3-1"], how: asked }`, "ports[0].paths[0]"},
	} {
		f, err := Parse([]byte(base + "ports: [" + tc.entry + "]\n"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Validate(f, c, now); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
