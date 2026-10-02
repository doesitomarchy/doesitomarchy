package catalog

import (
	"fmt"
	"strings"
)

// Tags returns the applicability tags for one configuration (see the header
// of data/capabilities.yaml for the vocabulary).
func (c *Catalog) Tags(m *Mac, cfg *Config) map[string]bool {
	t := map[string]bool{
		fmt.Sprintf("efi:%d", m.EFI): true,
		"chip:" + m.SecurityChip:     true,
	}
	if line, ok := c.Vocab.Lines[m.Line]; ok {
		t["form:"+line.Form] = true
	}
	for _, f := range cfg.Features {
		t["feature:"+f] = true
	}
	for p := range cfg.Ports {
		t["port:"+p] = true
		if c.Vocab.Ports[p].Video {
			t["video-out"] = true
		}
	}
	for _, id := range append(append([]string{}, cfg.Components...), cfg.BTOComponents...) {
		comp, ok := c.Components[id]
		if !ok {
			continue
		}
		t["has:"+comp.Kind] = true
		if comp.Kind == "gpu" && comp.Role != "" {
			t["gpu:"+comp.Role] = true
		}
	}
	return t
}

// Matches reports whether tags satisfy the condition. A nil condition matches everything.
func (cond *Condition) Matches(tags map[string]bool) bool {
	if cond == nil {
		return true
	}
	for _, x := range cond.All {
		if !tags[x] {
			return false
		}
	}
	if len(cond.Any) > 0 {
		ok := false
		for _, x := range cond.Any {
			if tags[x] {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, x := range cond.None {
		if tags[x] {
			return false
		}
	}
	return true
}

// Applicable returns, in catalog order, the capabilities that apply to cfg.
func (c *Catalog) Applicable(m *Mac, cfg *Config) []Capability {
	tags := c.Tags(m, cfg)
	var out []Capability
	for _, cap := range c.Capabilities {
		if !cap.Retired && cap.When.Matches(tags) {
			out = append(out, cap)
		}
	}
	return out
}

// validTag reports whether a condition tag is one the vocabulary can produce.
func (v *Vocabulary) validTag(tag string) bool {
	if tag == "video-out" {
		return true
	}
	ns, val, ok := strings.Cut(tag, ":")
	if !ok || val == "" {
		return false
	}
	switch ns {
	case "efi":
		return val == "32" || val == "64"
	case "chip":
		_, ok := v.SecurityChips[val]
		return ok
	case "form":
		for _, l := range v.Lines {
			if l.Form == val {
				return true
			}
		}
		return false
	case "feature":
		_, ok := v.Features[val]
		return ok
	case "port":
		_, ok := v.Ports[val]
		return ok
	case "has":
		_, ok := v.ComponentKinds[val]
		return ok
	case "gpu":
		return val == "integrated" || val == "discrete"
	}
	return false
}
