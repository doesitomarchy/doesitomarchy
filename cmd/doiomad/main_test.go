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
	dbDir := t.TempDir()
	dbPath, dbPath2, dbPath3 := filepath.Join(dbDir, "a.db"), filepath.Join(dbDir, "b.db"), filepath.Join(dbDir, "c.db")
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
		{"validate real data", []string{"validate", "-data", filepath.Join("..", "..", "data")}, 0, "catalog ok:"},
		{"validate empty", []string{"validate", "-data", empty}, 1, ""},
		{"validate bad", []string{"validate", "-data", bad}, 1, ""},
		{"lock refuses invalid catalog", []string{"lock", "-data", bad}, 1, ""},
		{"sync embedded catalog", []string{"sync", "-db", dbPath}, 0, "catalog synced: 121 macs"},
		{"sync again is a no-op", []string{"sync", "-db", dbPath}, 0, "catalog unchanged"},
		{"sync from a directory", []string{"sync", "-db", dbPath2, "-data", filepath.Join("..", "..", "data")}, 0, "catalog synced"},
		{"sync refuses invalid catalog", []string{"sync", "-db", dbPath3, "-data", bad}, 1, ""},
		{"serve bad flag", []string{"serve", "-nope"}, 2, ""},
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
