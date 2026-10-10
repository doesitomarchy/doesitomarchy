package match_test

import (
	"reflect"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	server "github.com/doesitomarchy/doesitomarchy/internal/match"
	"github.com/doesitomarchy/doesitomarchy/pkg/match"
)

// Real-shaped command output for each supported form.
const (
	linuxSys = `MacBookPro8,2
Mac-94245A3940C91C80
model name	: Intel(R) Core(TM) i7-2635QM CPU @ 2.00GHz
pci 0x8086:0x0116
pci 0x8086:0x1c03
pci 0x1002:0x6760
pci 0x14e4:0x4331
pci 0x8086:0x0116
`
	linuxLspci = `MacBookPro8,2
Mac-94245A3940C91C80
00:02.0 VGA compatible controller [0300]: Intel Corporation 2nd Generation Core Processor Family Integrated Graphics Controller [8086:0126] (rev 09)
01:00.0 VGA compatible controller [0300]: Advanced Micro Devices, Inc. [AMD/ATI] Whistler [Radeon HD 6730M/6770M/7690M XT] [1002:6741]
03:00.0 Network controller [0280]: Broadcom Inc. and subsidiaries BCM4331 802.11a/b/g/n [14e4:4331] (rev 02)
`
	macOS = `MacBookPro8,2
Intel(R) Core(TM) i7-2635QM CPU @ 2.00GHz
    | |   "board-id" = <"Mac-94245a3940c91c80">
Graphics/Displays:

    Intel HD Graphics 3000:

      Chipset Model: Intel HD Graphics 3000
      Type: GPU
      Vendor: Intel (0x8086)
      Device ID: 0x0126

    AMD Radeon HD 6490M:

      Chipset Model: AMD Radeon HD 6490M
      Vendor: AMD (0x1002)
      Device ID: 0x6760
      Displays:
        Color LCD:
          Serial Number: XY1234
`
)

func TestParseProbe(t *testing.T) {
	tests := []struct {
		name, text string
		want       match.Probe
	}{
		{"linux /sys", linuxSys, match.Probe{ProductName: "MacBookPro8,2", BoardID: "Mac-94245A3940C91C80",
			PCI: []string{"8086:0116", "8086:1c03", "1002:6760", "14e4:4331"}, CPU: "Intel(R) Core(TM) i7-2635QM CPU @ 2.00GHz"}},
		{"linux lspci -nn", linuxLspci, match.Probe{ProductName: "MacBookPro8,2", BoardID: "Mac-94245A3940C91C80",
			PCI: []string{"8086:0126", "1002:6741", "14e4:4331"}}},
		{"macOS", macOS, match.Probe{ProductName: "MacBookPro8,2", BoardID: "Mac-94245A3940C91C80",
			PCI: []string{"8086:0126", "1002:6760"}, CPU: "Intel(R) Core(TM) i7-2635QM CPU @ 2.00GHz"}},
		{"Core 2 padding", "MacBookPro5,5\nmodel name\t: Intel(R) Core(TM)2 Duo CPU     P8700  @ 2.53GHz\n",
			match.Probe{ProductName: "MacBookPro5,5", CPU: "Intel(R) Core(TM)2 Duo CPU P8700 @ 2.53GHz"}},
		{"8-digit board ID", "iMac9,1\nMac-f2218fc8\n", match.Probe{ProductName: "iMac9,1", BoardID: "Mac-F2218FC8"}},
		{"nothing useful", "hello world [0300]", match.Probe{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := match.ParseProbe(tt.text); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v\nwant %+v", got, tt.want)
			}
		})
	}
	// End to end: each paste finds the right configuration in the catalog.
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	m := server.New(c)
	if r := m.Match(match.ParseProbe(linuxSys)); !r.Exact || r.Best() != "macbookpro8-2-15-early-2011-a" {
		t.Errorf("/sys paste: %+v", r)
	}
	if r := m.Match(match.ParseProbe(macOS)); !r.Exact || r.Best() != "macbookpro8-2-15-early-2011-a" {
		t.Errorf("macOS paste: %+v", r)
	}
	if r := m.Match(match.ParseProbe("Mac-F2208EC8\n")); r.Identifier != "Macmini4,1" || r.By != "board_id" {
		t.Errorf("8-digit board ID alone: %+v", r)
	}
	if r := m.Match(match.ParseProbe(linuxLspci)); r.Exact {
		t.Errorf("the HD 6750M is shared by two configs: %+v", r)
	}
}
