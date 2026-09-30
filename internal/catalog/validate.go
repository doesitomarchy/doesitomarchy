package catalog

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

var (
	reSlug        = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	reCapID       = regexp.MustCompile(`^[a-z]+\.[a-z0-9]+(-[a-z0-9]+)*$`)
	reCompID      = regexp.MustCompile(`^[a-z-]+/[a-z0-9]+(-[a-z0-9]+)*$`)
	reHWID        = regexp.MustCompile(`^(pci|usb):[0-9a-f]{4}:[0-9a-f]{4}$`)
	reIdentifier  = regexp.MustCompile(`^([A-Za-z]+)[0-9]+,[0-9]+$`)
	reBoardID     = regexp.MustCompile(`^Mac-[0-9A-F]{8}([0-9A-F]{8})?$`)
	reOrderNumber = regexp.MustCompile(`^[A-Z0-9]{4,5}[A-Z]{1,2}/[A-Z]$`) // MB463LL/A, MGEM2LL/A
	reModelNumber = regexp.MustCompile(`^A[0-9]{4}$`)
	reEMC         = regexp.MustCompile(`^[0-9]{4}(-[0-9])?$`) // Apple revision suffix, e.g. "2353-1"
	reURL         = regexp.MustCompile(`^https?://\S+$`)
)

var storageInterfaces = map[string]bool{"pata": true, "sata": true, "pcie-ahci": true, "nvme": true}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func (l *loader) validate() {
	l.validateVocab()
	l.validateCapabilities()
	l.validateComponents()
	l.validateMacs()
	l.validateCoverage()
	l.validateAliases()
	l.validateLock()
}

func (l *loader) validateVocab() {
	v, path := &l.cat.Vocab, "vocabulary.yaml"
	for name, n := range map[string]int{
		"lines": len(v.Lines), "security_chips": len(v.SecurityChips), "component_kinds": len(v.ComponentKinds),
		"cpu_codenames": len(v.CPUCodenames), "ports": len(v.Ports), "features": len(v.Features),
	} {
		if n == 0 {
			l.errf(path, "%s: must not be empty", name)
		}
	}
	if _, ok := v.SecurityChips["none"]; !ok {
		l.errf(path, `security_chips: must include "none"`)
	}
	for k, line := range v.Lines {
		if line.Name == "" || line.Form == "" || line.IdentifierPrefix == "" {
			l.errf(path, "lines.%s: name, form and identifier_prefix are required", k)
		}
	}
	for k, cpu := range v.CPUCodenames {
		if cpu.Bits != 32 && cpu.Bits != 64 {
			l.errf(path, "cpu_codenames.%s: bits must be 32 or 64", k)
		}
	}
	for _, m := range []map[string]Named{v.SecurityChips, v.ComponentKinds, v.Features} {
		for k, n := range m {
			if !reSlug.MatchString(k) || n.Name == "" {
				l.errf(path, "%q: key must be a lower-case slug and name is required", k)
			}
		}
	}
	for k, p := range v.Ports {
		if !reSlug.MatchString(k) || p.Name == "" {
			l.errf(path, "ports.%s: key must be a lower-case slug and name is required", k)
		}
	}
}

func (l *loader) validateCapabilities() {
	c, path := l.cat, "capabilities.yaml"
	cats := map[string]bool{}
	for _, cat := range c.Categories {
		if !reSlug.MatchString(cat.ID) || cat.Name == "" {
			l.errf(path, "category %q: id must be a slug and name is required", cat.ID)
		}
		if cats[cat.ID] {
			l.errf(path, "category %q defined twice", cat.ID)
		}
		cats[cat.ID] = true
	}
	seen := map[string]bool{}
	for _, cap := range c.Capabilities {
		if !reCapID.MatchString(cap.ID) {
			l.errf(path, "capability %q: id must look like <category>.<name>", cap.ID)
			continue
		}
		if seen[cap.ID] {
			l.errf(path, "capability %q defined twice", cap.ID)
		}
		seen[cap.ID] = true
		if !cats[cap.Category()] {
			l.errf(path, "capability %q: unknown category %q", cap.ID, cap.Category())
		}
		if cap.Name == "" {
			l.errf(path, "capability %q: name is required", cap.ID)
		}
		if w := cap.When; w != nil {
			if len(w.All)+len(w.Any)+len(w.None) == 0 {
				l.errf(path, "capability %q: empty when (omit it to apply everywhere)", cap.ID)
			}
			for _, tag := range append(append(append([]string{}, w.All...), w.Any...), w.None...) {
				if !c.Vocab.validTag(tag) {
					l.errf(path, "capability %q: unknown tag %q", cap.ID, tag)
				}
			}
		}
	}
}

func (l *loader) checkSources(file, where string, sources []string, required bool) {
	if required && len(sources) == 0 {
		l.errf(file, "%s: at least one source URL is required", where)
	}
	for _, s := range sources {
		if !reURL.MatchString(s) {
			l.errf(file, "%s: source %q is not a URL", where, s)
		}
	}
}

func (l *loader) checkUncertain(file, where string, us []Uncertain) {
	for _, u := range us {
		if u.Field == "" || u.Note == "" {
			l.errf(file, "%s: uncertain entries need both field and note", where)
		}
	}
}

func (l *loader) validateComponents() {
	c := l.cat
	for id, comp := range c.Components {
		where := fmt.Sprintf("component %q", id)
		if _, ok := c.Vocab.ComponentKinds[comp.Kind]; !ok {
			l.errf(comp.File, "unknown component kind %q (file name must be a component_kinds key)", comp.Kind)
		}
		if !reCompID.MatchString(id) || !strings.HasPrefix(id, comp.Kind+"/") {
			l.errf(comp.File, "%s: id must be %q followed by a slug", where, comp.Kind+"/")
		}
		if comp.Name == "" || comp.Vendor == "" {
			l.errf(comp.File, "%s: name and vendor are required", where)
		}
		switch {
		case comp.Kind == "gpu" && comp.Role != "integrated" && comp.Role != "discrete":
			l.errf(comp.File, "%s: gpu role must be integrated or discrete", where)
		case comp.Kind != "gpu" && comp.Role != "":
			l.errf(comp.File, "%s: role is only valid for gpu components", where)
		}
		for _, hw := range comp.IDs {
			if !reHWID.MatchString(hw) {
				l.errf(comp.File, "%s: hardware id %q must look like pci:vvvv:dddd or usb:vvvv:pppp", where, hw)
			}
		}
		l.checkSources(comp.File, where, comp.Sources, true)
		l.checkUncertain(comp.File, where, comp.Uncertain)
	}
}

func (l *loader) validateMacs() {
	c := l.cat
	seenIdent := map[string]string{}
	seenConfig := map[string]string{} // id or alias → where
	for _, m := range c.Macs {
		f := m.File
		mm := reIdentifier.FindStringSubmatch(m.Identifier)
		if mm == nil {
			l.errf(f, "identifier %q is not a valid Mac model identifier", m.Identifier)
			continue
		}
		if want := FileSlug(m.Identifier) + ".yaml"; path.Base(f) != want {
			l.errf(f, "file must be named %s", want)
		}
		if prev, dup := seenIdent[m.Identifier]; dup {
			l.errf(f, "identifier %s already defined in %s", m.Identifier, prev)
		}
		seenIdent[m.Identifier] = path.Base(f)

		line, ok := c.Vocab.Lines[m.Line]
		switch {
		case !ok:
			l.errf(f, "unknown line %q", m.Line)
		case line.IdentifierPrefix != mm[1]:
			l.errf(f, "identifier %s does not match line %q (expected prefix %s)", m.Identifier, m.Line, line.IdentifierPrefix)
		}
		if m.EFI != 32 && m.EFI != 64 {
			l.errf(f, "efi must be 32 or 64")
		}
		if _, ok := c.Vocab.SecurityChips[m.SecurityChip]; !ok {
			l.errf(f, "unknown security_chip %q", m.SecurityChip)
		}
		for _, b := range m.BoardIDs {
			if !reBoardID.MatchString(b) {
				l.errf(f, "board id %q must look like Mac-XXXXXXXX", b)
			}
		}
		l.checkSources(f, m.Identifier, m.Sources, true)
		l.checkUncertain(f, m.Identifier, m.Uncertain)
		if len(m.Releases) == 0 {
			l.errf(f, "at least one release is required")
		}

		releaseIDs := map[string]bool{}
		for ri := range m.Releases {
			r := &m.Releases[ri]
			where := "release " + r.ID
			if !reSlug.MatchString(r.ID) {
				l.errf(f, "%s: id must be a lower-case slug", where)
			}
			if releaseIDs[r.ID] {
				l.errf(f, "%s: defined twice", where)
			}
			releaseIDs[r.ID] = true
			if r.Name == "" {
				l.errf(f, "%s: name is required", where)
			}
			if !validDate(r.Announced) {
				l.errf(f, "%s: announced must be YYYY-MM-DD", where)
			}
			if r.Discontinued != "" && !validDate(r.Discontinued) {
				l.errf(f, "%s: discontinued must be YYYY-MM-DD", where)
			}
			for _, n := range r.ModelNumbers {
				if !reModelNumber.MatchString(n) {
					l.errf(f, "%s: model number %q must look like A1234", where, n)
				}
			}
			for _, n := range r.EMC {
				if !reEMC.MatchString(n) {
					l.errf(f, "%s: EMC %q must be four digits, optionally with a -N revision", where, n)
				}
			}
			l.checkSources(f, where, r.Sources, false)
			if len(r.Configs) == 0 {
				l.errf(f, "%s: at least one config is required", where)
			}
			for ci := range r.Configs {
				l.validateConfig(m, r, &r.Configs[ci], seenConfig)
			}
		}
	}
}

func (l *loader) validateConfig(m *Mac, r *Release, cfg *Config, seen map[string]string) {
	c, f := l.cat, m.File
	where := "config " + cfg.ID
	if !reSlug.MatchString(cfg.ID) || !strings.HasPrefix(cfg.ID, IDSlug(m.Identifier)+"-") {
		l.errf(f, "%s: id must be a lower-case slug starting with %q", where, IDSlug(m.Identifier)+"-")
	}
	for _, id := range append([]string{cfg.ID}, cfg.Aliases...) {
		if prev, dup := seen[id]; dup {
			l.errf(f, "%s: id %q already used by %s", where, id, prev)
		}
		seen[id] = m.Identifier + " " + cfg.ID
	}
	if cfg.Label == "" {
		l.errf(f, "%s: label is required", where)
	}
	switch {
	case cfg.BTOOnly && len(cfg.OrderNumbers) > 0:
		l.errf(f, "%s: bto_only configs have no standard order numbers", where)
	case !cfg.BTOOnly && len(cfg.OrderNumbers) == 0:
		l.errf(f, "%s: order_numbers required (or set bto_only: true)", where)
	}
	for _, o := range cfg.OrderNumbers {
		if !reOrderNumber.MatchString(o) {
			l.errf(f, "%s: order number %q must look like MB463LL/A", where, o)
		}
	}

	codename, ok := c.Vocab.CPUCodenames[cfg.CPU.Codename]
	if !ok {
		l.errf(f, "%s: unknown cpu codename %q", where, cfg.CPU.Codename)
	} else if codename.Bits == 32 && m.HardBlocker == "" {
		l.errf(f, "%s: 32-bit CPU (%s) requires hard_blocker on the Mac", where, codename.Name)
	}
	if len(cfg.CPU.Standard) == 0 {
		l.errf(f, "%s: at least one standard processor is required", where)
	}
	for _, p := range append(append([]Processor{}, cfg.CPU.Standard...), cfg.CPU.BTO...) {
		if p.Model == "" || p.GHz <= 0 || p.Cores <= 0 {
			l.errf(f, "%s: processors need model, ghz and cores", where)
		}
	}
	if cfg.Memory.Type == "" || len(cfg.Memory.StandardGB) == 0 || cfg.Memory.MaxGB <= 0 {
		l.errf(f, "%s: memory needs type, standard_gb and max_gb", where)
	}
	if !storageInterfaces[cfg.Storage.Interface] {
		l.errf(f, "%s: storage interface must be one of pata, sata, pcie-ahci, nvme", where)
	}
	if len(cfg.Storage.Standard) == 0 && !cfg.BTOOnly {
		l.errf(f, "%s: standard storage is required", where)
	}

	gpus, compSeen := 0, map[string]bool{}
	for _, list := range [][]string{cfg.Components, cfg.BTOComponents} {
		for _, id := range list {
			comp, ok := c.Components[id]
			if !ok {
				l.errf(f, "%s: unknown component %q", where, id)
				continue
			}
			if compSeen[id] {
				l.errf(f, "%s: component %q listed twice", where, id)
			}
			compSeen[id] = true
			if comp.Kind == "gpu" {
				gpus++
			}
		}
	}
	if gpus == 0 {
		l.errf(f, "%s: at least one gpu component is required", where)
	}
	if len(cfg.Ports) == 0 {
		l.errf(f, "%s: ports are required", where)
	}
	for p, n := range cfg.Ports {
		if _, ok := c.Vocab.Ports[p]; !ok {
			l.errf(f, "%s: unknown port class %q", where, p)
		}
		if n < 1 {
			l.errf(f, "%s: port %q count must be at least 1", where, p)
		}
	}
	featSeen := map[string]bool{}
	for _, feat := range cfg.Features {
		if _, ok := c.Vocab.Features[feat]; !ok {
			l.errf(f, "%s: unknown feature %q", where, feat)
		}
		if featSeen[feat] {
			l.errf(f, "%s: feature %q listed twice", where, feat)
		}
		featSeen[feat] = true
	}
	if featSeen["builtin-display"] != (cfg.Display != nil) {
		l.errf(f, "%s: display is required exactly when the builtin-display feature is set", where)
	}
	if featSeen["gpu-switching"] && gpus < 2 {
		l.errf(f, "%s: gpu-switching needs two gpu components", where)
	}
	l.checkSources(f, where, cfg.Sources, false)
	l.checkUncertain(f, where, cfg.Uncertain)
}

func (l *loader) validateLock() {
	c, path := l.cat, LockFile
	ids, aliases := c.ConfigIDs()
	known := map[string]bool{}
	for _, id := range append(append([]string{}, ids...), aliases...) {
		known[id] = true
	}
	locked := map[string]bool{}
	for _, id := range c.LockedIDs {
		locked[id] = true
		if !known[id] {
			l.errf(path, "config ID %q was removed; config IDs are permanent (rename by moving the old ID to aliases)", id)
		}
	}
	for _, id := range ids {
		if l.requireLocked && !locked[id] {
			l.errf(path, "config ID %q is not locked yet; run: doioma lock", id)
		}
	}
}
