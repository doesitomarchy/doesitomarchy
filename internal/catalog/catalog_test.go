package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEmptyCatalog(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("empty catalog should validate, got %v", err)
	}
	if s != (Summary{}) {
		t.Fatalf("expected zero summary, got %+v", s)
	}
}

func TestLoadCountsFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "capabilities.yaml"), "- id: boot.efi64\n")
	write(t, filepath.Join(dir, "components", "gpu.yaml"), "- id: gpu/x\n")
	write(t, filepath.Join(dir, "macs", "MacBookPro5-1.yaml"), "identifier: MacBookPro5,1\n")
	write(t, filepath.Join(dir, "macs", "README.md"), "not yaml, ignored")

	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Summary{Capabilities: 1, Components: 1, Macs: 1}
	if s != want {
		t.Fatalf("got %+v, want %+v", s, want)
	}
}

func TestLoadReportsAllInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "macs", "a.yaml"), "key: [unclosed\n")
	write(t, filepath.Join(dir, "components", "b.yaml"), "a: 1\n  b: 2\n")

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	for _, name := range []string{"a.yaml", "b.yaml"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error should mention %s, got:\n%v", name, err)
		}
	}
}

func TestLoadMissingDir(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing dir")
	}
}
