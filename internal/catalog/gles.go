package catalog

import "strings"

// The OpenGL ES floor (PLAN.md §30): Hyprland 0.50 and later renders with
// OpenGL ES 3.0 or later and won't start on a GPU that reaches only 2.0.

// GLESLevels are the values a GPU component's gles may take.
var GLESLevels = map[string]bool{"2.0": true, "3.0": true, "3.1": true, "3.2": true}

// HyprlandGLES is the lowest OpenGL ES version Hyprland 0.50 and later runs on.
const HyprlandGLES = "3.0"

// BelowGLESFloor reports whether none of a configuration's GPUs reaches
// Hyprland's OpenGL ES floor, so stock Omarchy's desktop can't start on it.
func (c *Catalog) BelowGLESFloor(cfg *Config) bool {
	gpus := 0
	for _, ref := range cfg.Components {
		if comp := c.Components[ref]; comp != nil && comp.Kind == "gpu" {
			gpus++
			if comp.GLES >= HyprlandGLES {
				return false
			}
		}
	}
	return gpus > 0
}

// Limitations returns research notes on what a configuration's hardware
// rules out before anyone tests it: for now, GPUs below Hyprland's OpenGL ES
// floor. They inform; they never change a verdict.
func (c *Catalog) Limitations(cfg *Config) []string {
	var low, ok []*Component
	for _, ref := range cfg.Components {
		comp := c.Components[ref]
		if comp == nil || comp.Kind != "gpu" {
			continue
		}
		if comp.GLES < HyprlandGLES { // "2.0" < "3.0": the levels compare as strings
			low = append(low, comp)
		} else {
			ok = append(ok, comp)
		}
	}
	if len(low) == 0 {
		return nil
	}
	reach := "reaches"
	if len(low) > 1 {
		reach = "reach"
	}
	subject := "The " + names(low) + " " + reach + " OpenGL ES " + low[0].GLES + " only"
	if len(ok) > 0 {
		return []string{subject + ", so Hyprland can run only on the " + names(ok) + "."}
	}
	return []string{subject + ". Hyprland 0.50 and later needs " + HyprlandGLES + ", so stock Omarchy's desktop won't start. " +
		"Software rendering or a GLES 2.0 fork of Hyprland may work; results will tell."}
}

func names(comps []*Component) string {
	n := make([]string, len(comps))
	for i, comp := range comps {
		n[i] = comp.Name
	}
	return strings.Join(n, " and ")
}
