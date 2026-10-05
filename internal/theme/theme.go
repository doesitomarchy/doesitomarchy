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

// SynTokens colour code samples (keys, strings, numbers, keywords,
// functions). They come from the theme's own terminal palette, never its
// green, which the site keeps for passed tests. They must reach MinText on
// the code background too. Only pages with code load them (SyntaxCSS).
var SynTokens = []string{"syn-key", "syn-str", "syn-num", "syn-kw", "syn-fn"}

// codeBG is the code-sample background: the site CSS mixes 8% of --text
// into --bg.
func codeBG(c map[string]RGB) RGB { return Mix(c["bg"], c["text"], 0.08) }

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

// HSL returns hue (0–360), saturation and lightness (0–1).
func (c RGB) HSL() (h, s, l float64) {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l = (mx + mn) / 2
	if mx == mn {
		return 0, 0, l
	}
	d := mx - mn
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	switch mx {
	case r:
		h = math.Mod((g-b)/d+6, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h * 60, s, l
}

// statusHues: a status colour must keep its meaning in every theme. Some
// Omarchy themes are monochrome or use non-red "red"; those fall back to a
// true red, amber or green before the contrast fix.
var statusHues = map[string]struct {
	lo, hi          float64 // acceptable hue range (lo > hi wraps through 0)
	darkFB, lightFB string  // fallbacks for dark and light themes
}{
	"bad":  {340, 20, "#ff6b5a", "#b3261e"},
	"warn": {28, 62, "#ffb000", "#8a5a00"},
	"ok":   {80, 165, "#4ade80", "#1a7f37"},
}

// hueOK checks hue and a minimum saturation (stricter when choosing a
// fallback than when re-checking a colour already darkened for contrast).
func hueOK(c RGB, lo, hi, minSat float64) bool {
	h, s, _ := c.HSL()
	if s < minSat {
		return false
	}
	if lo > hi {
		return h >= lo || h <= hi
	}
	return h >= lo && h <= hi
}

// finish derives the computed tokens and fixes contrast.
func finish(t *Theme) {
	c := t.Colors
	for k, sh := range statusHues {
		if !hueOK(c[k], sh.lo, sh.hi, 0.3) {
			if t.Dark {
				c[k] = must(sh.darkFB)
			} else {
				c[k] = must(sh.lightFB)
			}
		}
	}
	if _, ok := c["line"]; !ok {
		c["line"] = Mix(c["bg"], c["text"], 0.16)
	}
	if _, ok := c["line-strong"]; !ok {
		c["line-strong"] = Mix(c["bg"], c["text"], 0.32)
	}
	for _, k := range textTokens {
		c[k] = ensure(c[k], t.Dark, MinText, c["bg"], c["panel"])
	}
	for _, k := range SynTokens {
		c[k] = ensure(c[k], t.Dark, MinText, c["bg"], c["panel"], codeBG(c))
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
	for _, k := range SynTokens {
		for bg, c := range map[string]RGB{"bg": t.Colors["bg"], "panel": t.Colors["panel"], "code": codeBG(t.Colors)} {
			if r := Contrast(t.Colors[k], c); r < MinText {
				out = append(out, fmt.Sprintf("%s: --%s on %s is %.2f:1", t.Key, k, bg, r))
			}
		}
	}
	for k, sh := range statusHues {
		if !hueOK(t.Colors[k], sh.lo, sh.hi, 0.2) {
			out = append(out, fmt.Sprintf("%s: --%s %s does not read as its status colour", t.Key, k, t.Colors[k]))
		}
	}
	if r := Contrast(t.Colors["accent-contrast"], t.Colors["accent"]); r < 3 {
		out = append(out, fmt.Sprintf("%s: --accent-contrast on --accent is %.2f:1", t.Key, r))
	}
	return out
}

// Defaults returns the site's own pair (the review-report palette).
func Defaults() (light, dark *Theme) {
	light = &Theme{Key: "light", Name: "DoesItOmarchy light", Source: "site default", Colors: map[string]RGB{
		"bg": must("#f8f8f6"), "panel": must("#f0f0ed"), "line": must("#d9d9d4"), "line-strong": must("#b5b5b0"),
		"text": must("#121214"), "muted": must("#5d5d64"), "heading": must("#000000"),
		"accent": must("#c6371c"), "link": must("#6a3fd1"), "ident": must("#a15c00"), "release": must("#1d6f82"),
		"ok": must("#1a7f37"), "warn": must("#8a5a00"), "bad": must("#a3170b"), "unk": must("#5d5d64"),
		"syn-key": must("#1f5fbf"), "syn-str": must("#a15c00"), "syn-num": must("#b4361b"), "syn-kw": must("#6a3fd1"), "syn-fn": must("#1d6f82"),
	}}
	dark = &Theme{Key: "dark", Name: "DoesItOmarchy dark", Dark: true, Source: "site default", Colors: map[string]RGB{
		"bg": must("#000000"), "panel": must("#0c0c0e"), "line": must("#26262a"), "line-strong": must("#3b3b40"),
		"text": must("#ececee"), "muted": must("#a0a0a8"), "heading": must("#ffffff"),
		"accent": must("#ff5a36"), "link": must("#b594ff"), "ident": must("#f5b53f"), "release": must("#5fc3d6"),
		"ok": must("#4ade80"), "warn": must("#ffb000"), "bad": must("#ff8a73"), "unk": must("#a0a0a8"),
		"syn-key": must("#7cb4ff"), "syn-str": must("#f5b53f"), "syn-num": must("#ff8a73"), "syn-kw": must("#b594ff"), "syn-fn": must("#5fc3d6"),
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
	"syn-key": "blue", "syn-str": "yellow", "syn-num": "orange", "syn-kw": "magenta", "syn-fn": "cyan",
}

// omarchyFallback stands in for a colors.toml key some themes leave out.
var omarchyFallback = map[string]string{"orange": "red"}

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
		if kv[src] == "" && omarchyFallback[src] != "" {
			src = omarchyFallback[src]
		}
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

func block(sel string, t *Theme, tokens []string, scheme bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s {\n", sel)
	for _, k := range tokens {
		fmt.Fprintf(&b, "  --%s: %s;\n", k, t.Colors[k])
	}
	if scheme {
		s := "light"
		if t.Dark {
			s = "dark"
		}
		fmt.Fprintf(&b, "  color-scheme: %s;\n", s)
	}
	b.WriteString("}\n")
	return b.String()
}

// CSS renders the token stylesheet. With no data-theme attribute the page
// follows the system setting between our light and dark pair.
func CSS(light, dark *Theme, themes []*Theme) string { return css(light, dark, themes, Tokens, true) }

// SyntaxCSS renders the code-sample colours, in the same shape.
func SyntaxCSS(light, dark *Theme, themes []*Theme) string {
	return css(light, dark, themes, SynTokens, false)
}

func css(light, dark *Theme, themes []*Theme, tokens []string, scheme bool) string {
	var b strings.Builder
	b.WriteString("/* Generated by tools/themegen from themes/omarchy (MIT, see themes/omarchy/LICENSE). Do not edit. */\n")
	b.WriteString(block(`:root, :root[data-theme="light"]`, light, tokens, scheme))
	b.WriteString("@media (prefers-color-scheme: dark) {\n")
	b.WriteString(strings.ReplaceAll(block(`:root:not([data-theme])`, dark, tokens, scheme), "\n  ", "\n    "))
	b.WriteString("}\n")
	b.WriteString(block(`:root[data-theme="dark"]`, dark, tokens, scheme))
	sorted := append([]*Theme{}, themes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for _, t := range sorted {
		b.WriteString(block(fmt.Sprintf(`:root[data-theme="%s"]`, t.Key), t, tokens, scheme))
	}
	return b.String()
}
