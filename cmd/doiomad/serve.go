package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/doesitomarchy/doesitomarchy/data"
	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/search"
	"github.com/doesitomarchy/doesitomarchy/internal/store"
	"github.com/doesitomarchy/doesitomarchy/internal/web"
)

// dbFlags registers the flags shared by commands that open the database.
func dbFlags(fs *flag.FlagSet) (db, dataDir *string) {
	def := os.Getenv("DOIOMAD_DB")
	if def == "" {
		def = "doesitomarchy.db"
	}
	db = fs.String("db", def, "SQLite database file")
	dataDir = fs.String("data", "", "catalog directory (default: the catalog built into this binary)")
	return db, dataDir
}

// openSynced loads the catalog (embedded unless dataDir is set), opens and
// migrates the database, and syncs the catalog into it.
func openSynced(ctx context.Context, dbPath, dataDir string) (*store.Store, *catalog.Catalog, bool, error) {
	var fsys fs.FS = data.FS
	if dataDir != "" {
		fsys = os.DirFS(dataDir)
	}
	c, err := catalog.LoadFS(fsys)
	if err != nil {
		return nil, nil, false, fmt.Errorf("catalog invalid:\n%w", err)
	}
	hash, err := catalog.HashFS(fsys)
	if err != nil {
		return nil, nil, false, err
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, false, err
	}
	changed, err := st.SyncCatalog(ctx, c, hash)
	if err != nil {
		st.Close()
		return nil, nil, false, fmt.Errorf("sync: %w", err)
	}
	return st, c, changed, nil
}

func cmdSync(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	db, dataDir := dbFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx := context.Background()
	st, _, changed, err := openSynced(ctx, *db, *dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "sync: %v\n", err)
		return 1
	}
	defer st.Close()
	n, err := st.CatalogCounts(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "sync: %v\n", err)
		return 1
	}
	what := "catalog unchanged"
	if changed {
		what = "catalog synced"
	}
	fmt.Fprintf(stdout, "%s: %d macs, %d releases, %d configs, %d components, %d capabilities (%s)\n",
		what, n.Macs, n.Releases, n.Configs, n.Components, n.Capabilities, *db)
	return 0
}

func cmdServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	db, dataDir := dbFlags(fs)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, c, changed, err := openSynced(ctx, *db, *dataDir)
	if err != nil {
		log.Error("startup", "err", err)
		return 1
	}
	defer st.Close()
	start := time.Now()
	ix := search.Build(c, nil)
	log.Info("search index built", "ms", time.Since(start).Milliseconds())
	srv, err := web.New(st, c, ix, log, version)
	if err != nil {
		log.Error("templates", "err", err)
		return 1
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Error("listen", "err", err)
		return 1
	}
	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	log.Info("serving", "addr", ln.Addr().String(), "db", *db, "catalog_changed", changed, "version", version)
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case err := <-errc:
		log.Error("serve", "err", err)
		return 1
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := hs.Shutdown(shutCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("shutdown", "err", err)
		return 1
	}
	return 0
}
