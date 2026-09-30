package web

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
)

// assets fingerprints the embedded static files: "site.css" is served as
// "/static/site.<hash>.css" with a one-year immutable cache, so a deploy
// never serves stale CSS or JS. Unhashed names still work (fonts referenced
// from CSS, old links) with a one-day cache.
type assets struct {
	fsys   fs.FS
	hashed map[string]string // "site.css" → "site.3f9a1c0b2d.css"
	orig   map[string]string // reverse
}

func newAssets(fsys fs.FS) (*assets, error) {
	a := &assets{fsys: fsys, hashed: map[string]string{}, orig: map[string]string{}}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		ext := path.Ext(p)
		h := strings.TrimSuffix(p, ext) + "." + hex.EncodeToString(sum[:5]) + ext
		a.hashed[p], a.orig[h] = h, p
		return nil
	})
	return a, err
}

// URL returns the fingerprinted URL of a static file (template func "asset").
func (a *assets) URL(name string) string {
	if h, ok := a.hashed[name]; ok {
		return "/static/" + h
	}
	return "/static/" + name
}

func (a *assets) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	cache := "public, max-age=86400"
	if o, ok := a.orig[name]; ok {
		name, cache = o, "public, max-age=31536000, immutable"
	}
	b, err := fs.ReadFile(a.fsys, name)
	if err != nil || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", cache)
	if r.Method != http.MethodHead {
		w.Write(b)
	}
}

// compressible content types get gzip when the client accepts it.
func compressible(ct string) bool {
	for _, p := range []string{"text/", "application/javascript", "text/javascript", "application/json", "image/svg+xml"} {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	return false
}

var gzPool = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestCompression); return w }}

type gzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
	on      bool
}

func (g *gzipWriter) WriteHeader(code int) {
	if !g.decided {
		g.decided = true
		h := g.Header()
		if code != http.StatusNoContent && code != http.StatusNotModified && h.Get("Content-Encoding") == "" && compressible(h.Get("Content-Type")) {
			g.on = true
			h.Set("Content-Encoding", "gzip")
			h.Del("Content-Length")
			g.gz = gzPool.Get().(*gzip.Writer)
			g.gz.Reset(g.ResponseWriter)
		}
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.decided {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.on {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipWriter) close() {
	if g.on {
		g.gz.Close()
		gzPool.Put(g.gz)
	}
}

// compress gzips compressible responses for clients that accept it
// (Cloudflare re-compresses at the edge; this keeps origin transfers and
// the page-weight budget honest).
func compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if r.Method == http.MethodHead || !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		g := &gzipWriter{ResponseWriter: w}
		defer g.close()
		next.ServeHTTP(g, r)
	})
}
