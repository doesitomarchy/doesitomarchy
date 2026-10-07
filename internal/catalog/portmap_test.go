package catalog_test

import (
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

// Every drawing in data/portmaps belongs to a laid-out release and matches
// the layout of every configuration of that release.
func TestRealCatalogPortmaps(t *testing.T) {
	c, err := catalog.Load("../../data")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Portmaps) < 154 {
		t.Fatalf("%d port map drawings, want at least 154 (one per release)", len(c.Portmaps))
	}
	used := map[string]int{}
	for _, m := range c.Macs {
		for _, r := range m.Releases {
			key := catalog.PortmapKey(m.Identifier, r.ID)
			for _, cfg := range r.Configs {
				if c.Portmaps[key] != nil && len(cfg.Connectors) > 0 && cfg.Portmap != key {
					t.Errorf("%s: drawing %s not attached", cfg.ID, key)
				}
				if cfg.Portmap != "" {
					used[cfg.Portmap]++
				}
			}
		}
	}
	if len(used) != len(c.Portmaps) {
		t.Errorf("%d drawings attached to configurations, %d in data/portmaps", len(used), len(c.Portmaps))
	}
	pm := c.Portmaps["MacBookPro13-2_13-2016-4tb3"]
	if pm == nil || strings.Join(pm.Conns, " ") != "left-1 left-2 right-1 right-2 right-3" {
		t.Fatalf("MacBookPro13,2 drawing conns: %+v", pm)
	}
	if !strings.Contains(string(pm.Download), "<style>") || strings.Contains(string(pm.Site), "<style") {
		t.Error("the download flavour carries its style, the site flavour must not")
	}
	// MacPro7,1: the drawing shows the standard Radeon Pro 580X's two HDMI ports;
	// modules with one HDMI port have their own list and hide the second.
	for _, m := range c.Macs {
		if m.Identifier != "MacPro7,1" {
			continue
		}
		for _, cfg := range m.Releases[0].Configs {
			want := ""
			if strings.Contains(cfg.ID, "w5700x") || strings.Contains(cfg.ID, "vega-ii") || strings.Contains(cfg.ID, "w6800x") || strings.Contains(cfg.ID, "w6900x") {
				want = "back-7"
			}
			if cfg.Portmap != "MacPro7-1_2019" || strings.Join(cfg.PortmapHidden, " ") != want {
				t.Errorf("%s: drawing %q, hidden %v, want %q", cfg.ID, cfg.Portmap, cfg.PortmapHidden, want)
			}
		}
	}
}

// memCatalog copies data/ into memory so a test can change one file.
func memCatalog(t *testing.T) fstest.MapFS {
	t.Helper()
	m := fstest.MapFS{}
	root := os.DirFS("../../data")
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(root, p)
		m[p] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestPortmapValidation(t *testing.T) {
	const key = "portmaps/MacBookAir7-1_early-2015.svg"
	cases := map[string]struct {
		edit func(m fstest.MapFS)
		want string
	}{
		"port missing": {func(m fstest.MapFS) {
			m[key].Data = []byte(strings.Replace(string(m[key].Data), `class="pm-port" data-conn="right-2"`, `class="pm-port" data-conn="right-9"`, 1))
		}, "don't match"},
		"orphan": {func(m fstest.MapFS) {
			m["portmaps/MacBookAir7-1_early-2099.svg"] = m[key]
			m["portmaps/download/MacBookAir7-1_early-2099.svg"] = m[key]
		}, "no release with a port layout"},
		"no download copy": {func(m fstest.MapFS) {
			delete(m, "portmaps/download/MacBookAir7-1_early-2015.svg")
		}, "no standalone copy"},
		"not a subset": {func(m fstest.MapFS) {
			const lay = "layouts/MacPro7-1.yaml"
			m[lay].Data = []byte(strings.Replace(string(m[lay].Data), "        - { id: back-10, type: ethernet-10gbe }\n", "        - { id: back-11, type: ethernet-10gbe }\n", 1))
		}, "don't match"},
		"inline style": {func(m fstest.MapFS) {
			m[key].Data = []byte(strings.Replace(string(m[key].Data), "<title", "<style>.x{}</style><title", 1))
		}, "must not carry styles"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			m := memCatalog(t)
			tc.edit(m)
			_, err := catalog.LoadFS(m)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
