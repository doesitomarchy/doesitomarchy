package theme

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func loadAll(t *testing.T) []*Theme {
	t.Helper()
	files, _ := filepath.Glob("../../themes/omarchy/*.toml")
	sort.Strings(files)
	if len(files) < 20 {
		t.Fatalf("expected the vendored Omarchy themes, found %d", len(files))
	}
	var out []*Theme
	for _, f := range files {
		r, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		th, err := FromOmarchy(strings.TrimSuffix(filepath.Base(f), ".toml"), r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, th)
	}
	return out
}

// Every theme, including our pair, meets WCAG AA for all text tokens.
func TestContrast(t *testing.T) {
	light, dark := Defaults()
	for _, th := range append(loadAll(t), light, dark) {
		for _, p := range th.Problems() {
			t.Error(p)
		}
		for _, k := range append(Tokens, SynTokens...) {
			if _, ok := th.Colors[k]; !ok {
				t.Errorf("%s: missing --%s", th.Key, k)
			}
		}
	}
}

// The committed stylesheet must match the generator's output.
func TestGeneratedCSSUpToDate(t *testing.T) {
	light, dark := Defaults()
	want := CSS(light, dark, loadAll(t))
	got, err := os.ReadFile("../web/static/themes.css")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatal("internal/web/static/themes.css is stale: run `go generate ./internal/web`")
	}
	got, err = os.ReadFile("../web/static/syntax.css")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != SyntaxCSS(light, dark, loadAll(t)) {
		t.Fatal("internal/web/static/syntax.css is stale: run `go generate ./internal/web`")
	}
}

func TestContrastMath(t *testing.T) {
	if r := Contrast(RGB{0, 0, 0}, RGB{255, 255, 255}); r < 20.99 || r > 21.01 {
		t.Errorf("black/white = %v, want 21", r)
	}
	if r := Contrast(must("#777777"), must("#ffffff")); r < 4.47 || r > 4.49 {
		t.Errorf("#777/#fff = %v, want ~4.48", r)
	}
	c := ensure(must("#777777"), false, MinText, must("#ffffff"))
	if Contrast(c, must("#ffffff")) < MinText {
		t.Errorf("ensure did not reach %v: %s", MinText, c)
	}
	if _, err := ParseHex("#12345"); err == nil {
		t.Error("bad hex accepted")
	}
}
