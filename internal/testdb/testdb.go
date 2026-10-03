// Package testdb gives tests a database with the embedded catalog already
// synced. Syncing the catalog takes seconds under the race detector, so it
// happens once per test binary into a template file, and each test gets a
// copy of that file. Only tests import this package.
package testdb

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
)

var (
	once     sync.Once
	dir      string
	template string
	cat      *catalog.Catalog
	hash     string
	setupErr error
)

func setup() {
	if cat, setupErr = catalog.LoadFS(data.FS); setupErr != nil {
		return
	}
	if hash, setupErr = catalog.HashFS(data.FS); setupErr != nil {
		return
	}
	if dir, setupErr = os.MkdirTemp("", "doiomad-testdb-"); setupErr != nil {
		return
	}
	template = filepath.Join(dir, "template.db")
	st, err := store.Open(context.Background(), template)
	if err != nil {
		setupErr = err
		return
	}
	if _, err := st.SyncCatalog(context.Background(), cat, hash); err != nil {
		st.Close()
		setupErr = err
		return
	}
	setupErr = st.Close() // closing checkpoints the WAL into the one file
}

// Catalog returns the embedded catalog, loaded once, and its hash. Treat it
// as read-only: every test in the binary shares it.
func Catalog(tb testing.TB) (*catalog.Catalog, string) {
	tb.Helper()
	once.Do(setup)
	if setupErr != nil {
		tb.Fatal(setupErr)
	}
	return cat, hash
}

// Path copies the synced template to a new file in the test's temporary
// directory and returns its path, for code that opens the database itself.
func Path(tb testing.TB) string {
	tb.Helper()
	Catalog(tb)
	dst := filepath.Join(tb.TempDir(), "t.db")
	if err := copyFile(template, dst); err != nil {
		tb.Fatal(err)
	}
	return dst
}

// Open returns an open store on a fresh copy of the synced template, closed
// when the test ends.
func Open(tb testing.TB) (*store.Store, *catalog.Catalog) {
	tb.Helper()
	st, err := store.Open(context.Background(), Path(tb))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { st.Close() })
	return st, cat
}

// Cleanup removes the template; call it from TestMain after m.Run.
func Cleanup() {
	if dir != "" {
		os.RemoveAll(dir)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
