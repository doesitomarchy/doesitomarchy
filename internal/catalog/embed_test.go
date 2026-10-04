package catalog_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
)

// The embedded catalog must load identically to the on-disk one.
func TestLoadFSMatchesLoadDir(t *testing.T) {
	disk, err := catalog.Load("../../data")
	if err != nil {
		t.Fatal(err)
	}
	emb, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	if disk.Stats() != emb.Stats() {
		t.Fatalf("stats differ: disk %+v, embedded %+v", disk.Stats(), emb.Stats())
	}
	if !reflect.DeepEqual(disk.Macs, emb.Macs) || !reflect.DeepEqual(disk.Components, emb.Components) {
		t.Fatal("embedded catalog content differs from data/")
	}
}

// The configurations below Hyprland's OpenGL ES 3.0 floor (PLAN §30,
// GLES3-RESEARCH.md): 30 whose every GPU reaches 2.0 only, and the two
// Mid 2010 MacBook Pros whose Intel GPU does.
func TestGLESLimitations(t *testing.T) {
	c, err := catalog.LoadFS(data.FS)
	if err != nil {
		t.Fatal(err)
	}
	full := map[string]bool{
		"macbook1-1-mid-2006-a": true, "macbook2-1-late-2006-a": true, "macbook2-1-mid-2007-a": true,
		"macbook3-1-late-2007-a": true, "macbook4-1-early-2008-a": true, "macbookair1-1-early-2008-a": true,
		"macbookpro1-1-15-early-2006-a": true, "macbookpro1-2-17-early-2006-a": true,
		"macbookpro2-1-17-late-2006-a": true, "macbookpro2-2-15-late-2006-a": true,
		"macpro1-1-2006-a": true, "macpro1-1-2006-x1900xt": true, "macpro1-1-2006-fx4500": true,
		"macpro2-1-2007-a": true, "macpro2-1-2007-x1900xt": true, "macpro2-1-2007-fx4500": true,
		"macmini1-1-early-2006-a": true, "macmini1-1-late-2006-a": true, "macmini2-1-mid-2007-a": true,
		"xserve1-1-late-2006-a": true, "xserve2-1-early-2008-a": true,
		"imac4-1-17-early-2006-a": true, "imac4-1-20-early-2006-a": true, "imac4-2-17-mid-2006-a": true,
		"imac5-1-17-late-2006-a": true, "imac5-1-20-late-2006-a": true, "imac5-2-17-late-2006-a": true,
		"imac6-1-24-late-2006-a": true, "imac6-1-24-late-2006-7600gt": true,
	}
	partial := map[string]bool{"macbookpro6-1-17-mid-2010-a": true, "macbookpro6-2-15-mid-2010-a": true}
	if len(full) != 29 {
		t.Fatalf("test list: %d", len(full))
	}
	seen := 0
	for _, m := range c.Macs {
		for _, r := range m.Releases {
			for i := range r.Configs {
				cfg := &r.Configs[i]
				lim := c.Limitations(cfg)
				switch {
				case full[cfg.ID]:
					seen++
					if len(lim) != 1 || !strings.Contains(lim[0], "OpenGL ES 2.0 only. Hyprland 0.50 and later needs 3.0") {
						t.Errorf("%s: %q", cfg.ID, lim)
					}
				case partial[cfg.ID]:
					seen++
					if len(lim) != 1 || lim[0] != "The Intel HD Graphics (Arrandale) reaches OpenGL ES 2.0 only, so Hyprland can run only on the NVIDIA GeForce GT 330M." {
						t.Errorf("%s: %q", cfg.ID, lim)
					}
				case lim != nil:
					t.Errorf("%s: unexpected limitation %q", cfg.ID, lim)
				}
			}
		}
	}
	if seen != len(full)+len(partial) {
		t.Errorf("found %d of %d configurations", seen, len(full)+len(partial))
	}
}
