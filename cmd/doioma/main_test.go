package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	empty := t.TempDir()
	bad := t.TempDir()
	if err := os.MkdirAll(filepath.Join(bad, "macs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "macs", "x.yaml"), []byte("a: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
	}{
		{"no args", nil, 2, ""},
		{"unknown", []string{"frobnicate"}, 2, ""},
		{"help", []string{"help"}, 0, "Usage:"},
		{"version", []string{"version"}, 0, "dev"},
		{"validate empty", []string{"validate", "-data", empty}, 0, "catalog ok: 0 mac files"},
		{"validate bad", []string{"validate", "-data", bad}, 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := run(tt.args, &out, &errOut); code != tt.wantCode {
				t.Fatalf("exit %d, want %d (stderr: %s)", code, tt.wantCode, errOut.String())
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Fatalf("stdout %q missing %q", out.String(), tt.wantOut)
			}
		})
	}
}
