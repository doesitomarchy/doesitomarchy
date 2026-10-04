package main

import (
	"context"
	"fmt"
	"time"

	"github.com/doesitomarchy/doesitomarchy/data"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/results"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

// seedDemo fills a throwaway database with made-up results for design
// review (serve -demo). Everything goes through the real path, Validate,
// InsertResult and SetResultState, so the demo exercises the real engine.
// The cases cover every verdict, a conflict, a stale result, a retraction
// and the maintainer's Unsupported flag.
func seedDemo(ctx context.Context, st *store.Store, c *catalog.Catalog) error {
	now := time.Now()
	add := func(f *results.File, accept bool) (int64, error) {
		r, err := results.Validate(f, c, now)
		if err != nil {
			return 0, fmt.Errorf("demo result for %s: %w", f.Config, err)
		}
		id, err := st.InsertResult(ctx, r, nil, results.SchemaV1, "demo")
		if err != nil {
			return 0, err
		}
		if accept {
			err = st.SetResultState(ctx, id, store.Accepted, "", "demo")
		}
		return id, err
	}
	// file builds a result for one config: every applicable capability gets
	// status(i, id), which returns "" to leave it out.
	file := func(configID, version, on, handle string, status func(i int, id string) string, evidence map[string]string) *results.File {
		f := &results.File{Schema: results.SchemaV1, Config: configID, TestedAt: on, Items: map[string]results.FileItem{},
			Omarchy: results.FileOmarchy{Version: version}, Tester: results.FileTester{Handle: handle},
			Source: results.FileSource{ID: "manual", Profile: "full"}}
		m, cfg := findDemoConfig(c, configID)
		for i, cp := range c.Applicable(m, cfg) {
			if s := status(i, cp.ID); s != "" {
				method := "automatic"
				if i%2 == 1 {
					method = "observed"
				}
				f.Items[cp.ID] = results.FileItem{Status: s, Method: method, Evidence: evidence[cp.ID]}
			}
		}
		return f
	}
	allPass := func(int, string) string { return "supported" }

	// Verified: every configuration of MacBookPro8,2, and one iMac12,2 config.
	for _, m := range c.Macs {
		if m.Identifier != "MacBookPro8,2" {
			continue
		}
		for _, r := range m.Releases {
			for _, cfg := range r.Configs {
				if _, err := add(file(cfg.ID, "4.0.4", "2026-09-28T15:30:00Z", "demo-tester", allPass, nil), true); err != nil {
					return err
				}
			}
		}
	}
	if _, err := add(file("imac12-2-27-mid-2011-a", "4.0.4", "2026-09-27T15:30:00Z", "demo-tester", allPass, nil), true); err != nil {
		return err
	}

	// Partial: a failure and a capability given up on (MacBookPro15,1, 2018).
	speakers := map[string]string{"audio.speakers": "snd_hda_intel 0000:00:1f.3: no codecs found (T2 audio needs the apple-bce aaudio driver)"}
	mbp151 := file("macbookpro15-1-15-2018-a", "4.0.4", "2026-09-30T15:30:00Z", "demo-tester", func(i int, id string) string {
		switch {
		case id == "audio.speakers":
			return "failed"
		case i%3 == 2:
			return ""
		}
		return "supported"
	}, speakers)
	// Its per-connector criteria are tested on the first port of each group
	// (PLAN §25.1a): with two Thunderbolt 3 controllers, a result that names
	// no port would cover neither group.
	m, cfg := findDemoConfig(c, mbp151.Config)
	for crit, groups := range c.CriterionGroups(m, cfg) {
		it, ok := mbp151.Items[crit]
		if !ok {
			continue
		}
		delete(mbp151.Items, crit)
		for _, g := range groups {
			mbp151.Items[crit+"@"+g.Connectors[0]] = it
		}
	}
	if _, err := add(mbp151, true); err != nil {
		return err
	}
	// A newer edge build fixes those speakers (PLAN §28.2): the stable verdict
	// stays Failed, with the edge result on its own line.
	if _, err := add(file("macbookpro15-1-15-2018-a", "4.0.0.r6713.ga85e29a", "2026-10-02T15:30:00Z", "demo-tester", func(i int, id string) string {
		if id == "audio.speakers" {
			return "supported"
		}
		return ""
	}, nil), true); err != nil {
		return err
	}
	// Tested only on an edge build (MacBookAir5,2): untested on stable.
	if _, err := add(file("macbookair5-2-mid-2012-a", "4.0.0.r6713.ga85e29a", "2026-10-03T16:49:05Z", "demo-tester", func(i int, id string) string {
		switch {
		case id == "camera.builtin":
			return "failed"
		case i%3 == 0:
			return "supported"
		}
		return ""
	}, map[string]string{"camera.builtin": "Captured 14.5 fps of 29.97 advertised, with visible flicker"}), true); err != nil {
		return err
	}
	if err := st.SetUnsupported(ctx, "bridge.touch-bar-camera", "macbookpro15-1-15-2018-a", "",
		"Given up after 3 fix attempts made no progress (demo).", "demo"); err != nil {
		return err
	}

	// Partial: the synthetic MacBookPro15,2 fixture (2018).
	f, err := results.Parse(results.SyntheticFixture)
	if err != nil {
		return err
	}
	if _, err := add(f, true); err != nil {
		return err
	}

	// Failed: a Boot capability failed (MacBookPro15,2, 2019).
	install := map[string]string{"boot.install": "nvme0n1 not found: the installer kernel has no apple-bce module, so the T2 SSD is invisible"}
	if _, err := add(file("macbookpro15-2-13-2019-4tb3-a", "4.0.4", "2026-10-01T15:30:00Z", "", func(i int, id string) string {
		switch {
		case id == "boot.install":
			return "failed"
		case id == "boot.installer-efi64":
			return "supported"
		}
		return ""
	}, install), true); err != nil {
		return err
	}

	// Unsupported: a Boot capability given up on (MacBookPro16,2).
	if _, err := add(file("macbookpro16-2-13-2020-4tb3-a", "4.0.4", "2026-09-29T15:30:00Z", "demo-tester", func(i int, id string) string {
		if id == "boot.installer-efi64" {
			return "failed"
		}
		return ""
	}, map[string]string{"boot.installer-efi64": "Firmware refuses the installer image"}), true); err != nil {
		return err
	}
	if err := st.SetUnsupported(ctx, "boot.installer-efi64", "macbookpro16-2-13-2020-4tb3-a", "",
		"Firmware refuses the installer image and Secure Boot policy cannot be relaxed on this unit; 4 attempts (demo).", "demo"); err != nil {
		return err
	}

	// Conflict: two results on the same Omarchy version disagree about Wi-Fi
	// (MacBookAir7,2, 2017), plus a retracted third.
	wifi := func(s string) func(int, string) string {
		return func(i int, id string) string {
			if id == "network.wifi" {
				return s
			}
			if i%4 == 0 {
				return "supported"
			}
			return ""
		}
	}
	if _, err := add(file("macbookair7-2-2017-a", "4.0.4", "2026-09-25T15:30:00Z", "demo-tester", wifi("supported"), nil), true); err != nil {
		return err
	}
	if _, err := add(file("macbookair7-2-2017-a", "4.0.4", "2026-09-26T15:30:00Z", "another-tester", wifi("failed"),
		map[string]string{"network.wifi": "brcmfmac: firmware load failed"}), true); err != nil {
		return err
	}
	id, err := add(file("macbookair7-2-2017-a", "4.0.3", "2026-09-20T15:30:00Z", "", wifi("supported"), nil), true)
	if err != nil {
		return err
	}
	if err := st.SetResultState(ctx, id, store.Retracted, "Tested with a third-party Wi-Fi card (demo)", "demo"); err != nil {
		return err
	}

	// Stale: verified, but only on an older Omarchy major (MacBookPro11,1).
	if _, err := add(file("macbookpro11-1-13-mid-2014-a", "3.2.0", "2025-11-10T15:30:00Z", "demo-tester", allPass, nil), true); err != nil {
		return err
	}

	// A pending result: stored, never shown.
	if _, err = add(file("macbookpro11-1-13-late-2013-a", "4.0.4", "2026-10-01T15:30:00Z", "", allPass, nil), false); err != nil {
		return err
	}

	// Per-connector port results (PLAN §25) on MacBookPro11,3: one USB port
	// passes and the other, on the same controller, fails (a suspect port,
	// flagged; USB-A stays Supported); HDMI and one Thunderbolt port pass and
	// cover their groups.
	ports := &results.File{Schema: results.SchemaV1, Config: "macbookpro11-3-15-late-2013-a", TestedAt: "2026-09-29T15:30:00Z",
		Omarchy: results.FileOmarchy{Version: "4.0.4"}, Tester: results.FileTester{Handle: "demo-tester"},
		Source: results.FileSource{ID: "manual", Profile: "ports"}, Items: map[string]results.FileItem{
			"boot.install":                      {Status: "supported", Method: "observed"},
			"ports.usb-a@left-4":                {Status: "supported", Method: "fixture"},
			"ports.usb-a@right-3":               {Status: "failed", Method: "fixture", Evidence: "usb 2-1: device descriptor read/64, error -71"},
			"graphics.external-display@right-2": {Status: "supported", Method: "observed"},
			"ports.thunderbolt@left-2":          {Status: "supported", Method: "fixture"},
		}}
	if _, err := add(ports, true); err != nil {
		return err
	}

	// An OmacDiag report imported by a maintainer, pending (PLAN §24).
	imp, err := results.Import(results.OmacDiagFixture, "omacdiag", c, data.FS, results.ImportOptions{Omarchy: "4.0.4", Tester: "demo-tester"}, now)
	if err != nil {
		return fmt.Errorf("demo OmacDiag report: %w", err)
	}
	if err := st.EnsureSource(ctx, imp.Source.ID, imp.Source.Name, imp.Source.Homepage); err != nil {
		return err
	}
	if _, err := st.InsertResult(ctx, imp.Result, imp.Raw, imp.Format, "demo"); err != nil {
		return err
	}

	// Fix issues (PLAN §26), one in each state. Made up, like the rest of the
	// demo: the numbers don't exist in the fix repo.
	day := func(d int) string { return now.UTC().AddDate(0, 0, -d).Format(time.RFC3339) }
	issue := func(n int) string {
		return fmt.Sprintf("https://github.com/doesitomarchy/wecanfixeverything/issues/%d", n)
	}
	for _, fx := range []store.Fix{
		{Issue: 101, Capability: "audio.speakers", Component: "audio/apple-t2-audio", Title: "Built-in speakers on Apple T2 audio (demo)",
			URL: issue(101), Open: true, Assignee: "t2-audio-dev", LastActivity: day(3), OpenedBy: "demo"},
		{Issue: 102, Capability: "input.touch-id", Component: "bridge/apple-t2", Title: "Touch ID on Apple T2 (demo)",
			URL: issue(102), Open: true, LastActivity: day(10), OpenedBy: "demo"},
		{Issue: 103, Capability: "power.sleep-wake", Config: "macbookpro15-2-13-2018-4tb3-a", Title: "Sleep and wake on MacBook Pro (13-inch, 2018) (demo)",
			URL: issue(103), Open: true, Assignee: "sleepy", Proposed: true, LastActivity: day(1), OpenedBy: "demo"},
		{Issue: 104, Capability: "network.wifi", Component: "wifi/broadcom-bcm4360", Title: "Wi-Fi on Broadcom BCM4360 (demo)",
			URL: issue(104), Open: false, StateReason: "completed", Assignee: "wl-fixer", LastActivity: "2026-09-30T12:00:00Z",
			ClosedAt: "2026-09-30T12:00:00Z", FixLink: "https://github.com/basecamp/omarchy/pull/1", OpenedBy: "demo"},
		{Issue: 105, Capability: "audio.microphone", Component: "audio/apple-t2-audio", Title: "Built-in microphone on Apple T2 audio (demo)",
			URL: issue(105), Open: true, Assignee: "gone-quiet", LastActivity: day(90), OpenedBy: "demo"},
	} {
		if _, err := st.UpsertFix(ctx, fx); err != nil {
			return err
		}
	}

	// Shared IDs (PF-3) for /admin/shares: a known Mac with an aftermarket
	// Wi-Fi card, the same set twice, an identifier the catalog lacks, and an
	// iMac10,1 whose chipset IDs fold away and whose board picks the 21.5-inch
	// (PLAN §29; model-level IDs only).
	for _, sh := range []store.Share{
		{Product: "iMac10,1", BoardID: "Mac-F2268CC8", CPU: "Intel(R) Core(TM)2 Duo CPU     E7600  @ 3.06GHz",
			PCI: []string{"1002:9488", "1002:aa38", "104c:823e", "104c:823f", "10de:0a84", "10de:0a88", "10de:0a89", "10de:0a98", "10de:0aa2",
				"10de:0aa3", "10de:0aa4", "10de:0aa5", "10de:0aa6", "10de:0aa7", "10de:0aa9", "10de:0aab", "10de:0aac", "10de:0ab0", "10de:0ab9",
				"10de:0ac0", "10de:0ac4", "10de:0ac6", "10de:0ac7", "168c:002a"}, Modified: "yes", Release: "27-late-2009"},
		{Product: "MacBookPro8,2", BoardID: "Mac-94245A3940C91C80", CPU: "Intel(R) Core(TM) i7-2720QM CPU @ 2.20GHz",
			PCI: []string{"8086:0126", "1002:6760", "14e4:43a0"}, Modified: "yes", Release: "15-early-2011"},
		{Product: "MacBookPro8,2", BoardID: "Mac-94245A3940C91C80", CPU: "Intel(R) Core(TM) i7-2720QM CPU @ 2.20GHz",
			PCI: []string{"8086:0126", "1002:6760", "14e4:4331"}, Modified: "no", Release: "15-early-2011"},
		{Product: "MacBookPro8,2", BoardID: "Mac-94245A3940C91C80", CPU: "Intel(R) Core(TM) i7-2720QM CPU @ 2.20GHz",
			PCI: []string{"8086:0126", "1002:6760", "14e4:4331"}},
		{Product: "MacBookPro18,1", BoardID: "Mac-0000000000000001"},
	} {
		if _, err := st.AddShare(ctx, sh); err != nil {
			return err
		}
	}
	return nil
}

func findDemoConfig(c *catalog.Catalog, id string) (*catalog.Mac, *catalog.Config) {
	for _, m := range c.Macs {
		for ri := range m.Releases {
			for ci := range m.Releases[ri].Configs {
				if cfg := &m.Releases[ri].Configs[ci]; cfg.ID == id {
					return m, cfg
				}
			}
		}
	}
	panic("demo: unknown config " + id)
}
