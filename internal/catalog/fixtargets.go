package catalog

import (
	"strconv"
	"strings"
)

// FixConfigs returns the configurations a fix applies to: its configs when it
// lists them, otherwise, in catalog order, every configuration that has one of
// its devices among its parts (standard or build-to-order) and fits its when
// rule and years, leaving out Macs with a hard blocker (Omarchy can't run on
// them at all). A fix gives at least one of these (validateFixes).
func (c *Catalog) FixConfigs(f *OmabootFix) []string {
	t := f.Targets
	if len(t.Configs) > 0 {
		return t.Configs
	}
	devices := map[string]bool{}
	for _, d := range t.Devices {
		devices[d.ID] = true
	}
	var out []string
	for _, m := range c.Macs {
		if m.HardBlocker != "" {
			continue
		}
		for ri := range m.Releases {
			r := &m.Releases[ri]
			if y, _ := strconv.Atoi(r.Announced[:min(4, len(r.Announced))]); (t.From > 0 && y < t.From) || (t.To > 0 && y > t.To) {
				continue
			}
			for i := range r.Configs {
				cfg := &r.Configs[i]
				if len(devices) > 0 && !c.hasDevice(cfg, devices) {
					continue
				}
				if t.When != nil && !t.When.Matches(c.Tags(m, cfg)) {
					continue
				}
				out = append(out, cfg.ID)
			}
		}
	}
	return out
}

// hasDevice reports whether one of cfg's parts carries one of these hardware IDs.
func (c *Catalog) hasDevice(cfg *Config, ids map[string]bool) bool {
	for _, id := range append(append([]string{}, cfg.Components...), cfg.BTOComponents...) {
		if comp, ok := c.Components[id]; ok {
			for _, x := range comp.IDs {
				if ids[x] {
					return true
				}
			}
		}
	}
	return false
}

// FixRule says in words how FixConfigs picks a fix's configurations, to head
// a list of them: "Every Mac with a lid, from 2009". Empty when the fix lists
// them.
func (c *Catalog) FixRule(f *OmabootFix) string {
	t := f.Targets
	if len(t.Configs) > 0 {
		return ""
	}
	who, with, without := "Mac", []string{}, []string{}
	if len(t.Devices) > 0 {
		with = append(with, "one of the devices it acts on")
	}
	if w := t.When; w != nil {
		for _, tag := range w.All {
			if tag == "form:laptop" {
				who = "Mac laptop"
			} else {
				with = append(with, c.Vocab.tagWords(tag))
			}
		}
		if len(w.Any) > 0 {
			any := make([]string, len(w.Any))
			for i, tag := range w.Any {
				any[i] = c.Vocab.tagWords(tag)
			}
			with = append(with, strings.Join(any, " or "))
		}
		for _, tag := range w.None {
			without = append(without, c.Vocab.tagWords(tag))
		}
	}
	s := "Every " + who
	if len(with) > 0 {
		s += " with " + strings.Join(with, " and ")
	}
	if len(without) > 0 {
		if len(with) > 0 {
			s += ","
		}
		s += " without " + strings.Join(without, " or ")
	}
	switch {
	case t.From > 0 && t.To > 0:
		s += ", from " + strconv.Itoa(t.From) + " to " + strconv.Itoa(t.To)
	case t.From > 0:
		s += ", from " + strconv.Itoa(t.From)
	case t.To > 0:
		s += ", to " + strconv.Itoa(t.To)
	}
	return s
}

// tagWords names a condition tag for a sentence: "a lid", "a T2 chip".
func (v *Vocabulary) tagWords(tag string) string {
	ns, val, _ := strings.Cut(tag, ":")
	var name string
	switch ns {
	case "chip":
		if val == "none" {
			return "a security chip"
		}
		return "a " + strings.ToUpper(val) + " chip"
	case "feature":
		name = v.Features[val].Name
	case "port":
		name = v.Ports[val].Name
	case "has":
		name = v.ComponentKinds[val].Name
	}
	if name == "" {
		return tag
	}
	name, _, _ = strings.Cut(name, " (")
	if !strings.HasPrefix(name, "USB") && !strings.HasPrefix(name, "SD") {
		name = strings.ToLower(name[:1]) + name[1:]
	}
	if strings.ContainsRune("aeiou", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}
