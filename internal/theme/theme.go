// Package theme turns colour palettes (our default pair and Omarchy's
// themes) into the site's CSS design tokens, adjusting any colour that
// would fail WCAG AA contrast (PLAN.md §17.2).
package theme

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Tokens lists the site's colour tokens, in CSS output order.
var Tokens = []string{
	"bg", "panel", "line", "line-strong", "text", "muted", "heading",
	"accent", "accent-contrast", "link", "ident", "release", "ok", "warn", "bad", "unk",
}

// textTokens must reach MinText contrast on both --bg and --panel.
var textTokens = []string{"text", "muted", "heading", "accent", "link", "ident", "release", "ok", "warn", "bad", "unk"}

// MinText is the WCAG AA ratio for normal text.
const MinText = 4.5

// Theme is one selectable palette.
type Theme struct {
	Key    string // data-theme value, e.g. "tokyo-night"
	Name   string // shown in the picker
	Dark   bool
	Colors map[string]RGB
	Source string // where the palette came from
}

// RGB is an sRGB colour.
type RGB struct{ R, G, B uint8 }

func (c RGB) String() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// ParseHex reads #rgb or #rrggbb.
func ParseHex(s string) (RGB, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return RGB{}, fmt.Errorf("bad colour %q", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return RGB{}, fmt.Errorf("bad colour %q", s)
	}
	return RGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
}

func must(s string) RGB {
	c, err := ParseHex(s)
	if err != nil {
		panic(err)
	}
	return c
}

func channel(v uint8) float64 {
	c := float64(v) / 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// Luminance is the WCAG relative luminance.
func (c RGB) Luminance() float64 {
	return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
}

// Contrast is the WCAG contrast ratio between two colours (1–21).
func Contrast(a, b RGB) float64 {
	la, lb := a.Luminance(), b.Luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Mix blends a toward b by t (0 = a, 1 = b).
func Mix(a, b RGB, t float64) RGB {
	m := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return RGB{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B)}
}

var (
	black = RGB{0, 0, 0}
	white = RGB{255, 255, 255}
)

// ensure nudges c toward white (dark themes) or black (light themes) until
// it reaches min contrast against every background.
func ensure(c RGB, dark bool, min float64, bgs ...RGB) RGB {
	target := black
	if dark {
		target = white
	}
	ok := func(x RGB) bool {
		for _, bg := range bgs {
			if Contrast(x, bg) < min {
				return false
			}
		}
		return true
	}
	for step := 0; step <= 50; step++ {
		x := Mix(c, target, float64(step)/50)
		if ok(x) {
			return x
		}
	}
	return target
}

// finish derives the computed tokens and fixes contrast.
func finish(t *Theme) {
	c := t.Colors
	if _, ok := c["line"]; !ok {
		c["line"] = Mix(c["bg"], c["text"], 0.16)
	}
	if _, ok := c["line-strong"]; !ok {
		c["line-strong"] = Mix(c["bg"], c["text"], 0.32)
	}
	for _, k := range textTokens {
		c[k] = ensure(c[k], t.Dark, MinText, c["bg"], c["panel"])
	}
	if Contrast(c["accent"], black) >= Contrast(c["accent"], white) {
		c["accent-contrast"] = black
	} else {
		c["accent-contrast"] = white
	}
}

// Problems lists every token pair below the contrast minimum (empty when
// the theme passes). Used by tests on the generated output.
func (t *Theme) Problems() []string {
	var out []string
	for _, k := range textTokens {
		for _, bg := range []string{"bg", "panel"} {
			if r := Contrast(t.Colors[k], t.Colors[bg]); r < MinText {
				out = append(out, fmt.Sprintf("%s: --%s on --%s is %.2f:1", t.Key, k, bg, r))
			}
		}
	}
	if r := Contrast(t.Colors["accent-contrast"], t.Colors["accent"]); r < 3 {
		out = append(out, fmt.Sprintf("%s: --accent-contrast on --accent is %.2f:1", t.Key, r))
	}
	return out
}

// Defaults returns the site's own pair (the review-report palette).
func Defaults() (light, dark *Theme) {
	light = &Theme{Key: "light", Name: "doesitomarchy light", Source: "site default", Colors: map[string]RGB{
		"bg": must("#f8f8f6"), "panel": must("#f0f0ed"), "line": must("#d9d9d4"), "line-strong": must("#b5b5b0"),
		"text": must("#121214"), "muted": must("#5d5d64"), "heading": must("#000000"),
		"accent": must("#c6371c"), "link": must("#6a3fd1"), "ident": must("#a15c00"), "release": must("#1d6f82"),
		"ok": must("#1a7f37"), "warn": must("#8a5a00"), "bad": must("#a3170b"), "unk": must("#5d5d64"),
	}}
	dark = &Theme{Key: "dark", Name: "doesitomarchy dark", Dark: true, Source: "site default", Colors: map[string]RGB{
		"bg": must("#000000"), "panel": must("#0c0c0e"), "line": must("#26262a"), "line-strong": must("#3b3b40"),
		"text": must("#ececee"), "muted": must("#a0a0a8"), "heading": must("#ffffff"),
		"accent": must("#ff5a36"), "link": must("#b594ff"), "ident": must("#f5b53f"), "release": must("#5fc3d6"),
		"ok": must("#4ade80"), "warn": must("#ffb000"), "bad": must("#ff8a73"), "unk": must("#a0a0a8"),
	}}
	finish(light)
	finish(dark)
	return light, dark
}

// omarchyMap maps site tokens to Omarchy colors.toml keys.
var omarchyMap = map[string]string{
	"bg": "background", "panel": "dark_background", "text": "foreground", "muted": "dark_foreground",
	"heading": "bright_foreground", "accent": "accent", "link": "magenta", "ident": "yellow",
	"release": "cyan", "ok": "green", "warn": "yellow", "bad": "red", "unk": "muted",
}

// displayNames overrides the title-cased theme key.
var displayNames = map[string]string{"rose-pine": "Rosé Pine", "retro-82": "Retro 82", "flexoki-light": "Flexoki Light"}

// FromOmarchy parses one Omarchy colors.toml (flat `key = "value"` lines).
func FromOmarchy(key string, r io.Reader) (*Theme, error) {
	kv := map[string]string{}
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		kv[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	t := &Theme{Key: key, Name: displayName(key), Dark: kv["mode"] != "light", Colors: map[string]RGB{}, Source: "Omarchy " + key}
	for tok, src := range omarchyMap {
		c, err := ParseHex(kv[src])
		if err != nil {
			return nil, fmt.Errorf("%s: %s (%s): %w", key, src, tok, err)
		}
		t.Colors[tok] = c
	}
	finish(t)
	return t, nil
}

func displayName(key string) string {
	if n, ok := displayNames[key]; ok {
		return n
	}
	parts := strings.Split(key, "-")
	for i, p := range parts {
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func block(sel string, t *Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s {\n", sel)
	for _, k := range Tokens {
		fmt.Fprintf(&b, "  --%s: %s;\n", k, t.Colors[k])
	}
	scheme := "light"
	if t.Dark {
		scheme = "dark"
	}
	fmt.Fprintf(&b, "  color-scheme: %s;\n}\n", scheme)
	return b.String()
}

// CSS renders the token stylesheet. With no data-theme attribute the page
// follows the system setting between our light and dark pair.
func CSS(light, dark *Theme, themes []*Theme) string {
	var b strings.Builder
	b.WriteString("/* Generated by tools/themegen from themes/omarchy (MIT, see themes/omarchy/LICENSE). Do not edit. */\n")
	b.WriteString(block(`:root, :root[data-theme="light"]`, light))
	b.WriteString("@media (prefers-color-scheme: dark) {\n")
	b.WriteString(strings.ReplaceAll(block(`:root:not([data-theme])`, dark), "\n  ", "\n    "))
	b.WriteString("}\n")
	b.WriteString(block(`:root[data-theme="dark"]`, dark))
	sorted := append([]*Theme{}, themes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for _, t := range sorted {
		b.WriteString(block(fmt.Sprintf(`:root[data-theme="%s"]`, t.Key), t))
	}
	return b.String()
}
