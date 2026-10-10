package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/match"
	"github.com/doesitomarchy/doesitomarchy/pkg/report"
)

// The catalog snapshot (GET /api/v1/snapshot): what a test tool needs to
// build, check and match a report offline. OmaBoot? Live embeds a copy at
// build time and fetches a newer one when it's online. The catalog is fixed
// for the life of the process, so the body and its ETag are built once.

// catalogSnapshot builds the snapshot from the catalog. Its version is a
// hash of the content, so it changes exactly when the content does.
func catalogSnapshot(c *catalog.Catalog) (report.CatalogSnapshot, []byte) {
	x := report.CatalogSnapshot{Match: match.Snapshot(c), Capabilities: []report.SnapshotCriterion{}, Fixes: []report.BootFix{}}
	for _, cp := range c.Capabilities {
		x.Capabilities = append(x.Capabilities, report.SnapshotCriterion{ID: cp.ID, Name: cp.Name, Category: cp.Category(), Live: cp.Live, Retired: cp.Retired})
	}
	for _, f := range c.OmabootFixes {
		x.Fixes = append(x.Fixes, bootFixJSON(f))
	}
	body, _ := json.Marshal(x)
	sum := sha256.Sum256(body)
	x.Version = hex.EncodeToString(sum[:8])
	body, _ = json.Marshal(x)
	return x, body
}

// bootFixJSON is a registry entry as the API shows it.
func bootFixJSON(f catalog.OmabootFix) report.BootFix {
	x := report.BootFix{ID: f.ID, Name: f.Name, Changes: f.Changes, Hardware: f.Targets.Hardware, Configs: f.Targets.Configs,
		Criteria: f.Targets.Criteria, UpstreamNote: f.UpstreamNote, TestedOn: f.TestedOn, Since: f.Since, URL: BaseURL + fixURL(f.ID)}
	for _, d := range f.Targets.Devices {
		x.Devices = append(x.Devices, report.FixDevice{ID: d.ID, Subsystem: d.Subsystem})
	}
	for _, u := range f.Upstream {
		x.Upstream = append(x.Upstream, report.FixLink{Title: u.Title, URL: u.URL})
	}
	return x
}

// snapshotSample is the snapshot shortened for /api: the first item of each
// list, one plumbing ID (a map, which abbreviate can't shorten), and a fix's
// texts cut to their start.
func (s *Server) snapshotSample() string {
	var x report.CatalogSnapshot
	if err := json.Unmarshal(s.snapJSON, &x); err != nil {
		return ""
	}
	cut := func(s string) string {
		if i := strings.IndexAny(s, ":."); i > 0 && i < len(s)-1 {
			return s[:i] + " …"
		}
		return s
	}
	for i := range x.Fixes {
		x.Fixes[i].Changes, x.Fixes[i].Hardware = cut(x.Fixes[i].Changes), cut(x.Fixes[i].Hardware)
	}
	ids := make([]string, 0, len(x.Match.Plumbing))
	for id := range x.Match.Plumbing {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids[min(1, len(ids)):] {
		delete(x.Match.Plumbing, id)
	}
	b, _ := json.Marshal(x)
	return abbreviate(b, 1, 9)
}

func (s *Server) apiSnapshot(w http.ResponseWriter, r *http.Request) {
	readable(w)
	w.Header().Set("ETag", s.snapETag)
	if etagMatches(r.Header.Get("If-None-Match"), s.snapETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodHead {
		w.Write(s.snapJSON)
	}
}

// etagMatches reports whether an If-None-Match header names etag. Weak
// validators count (a proxy that compresses the body may weaken the tag).
func etagMatches(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || t == etag {
			return true
		}
	}
	return false
}
