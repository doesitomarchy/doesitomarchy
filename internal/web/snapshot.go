package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/doesitomarchy/doesitomarchy/internal/catalog"
	"github.com/doesitomarchy/doesitomarchy/internal/match"
	"github.com/doesitomarchy/doesitomarchy/pkg/report"
)

// The catalog snapshot (GET /api/v1/snapshot): what a test tool needs to
// build, check and match a report offline. OmaBoot? Live embeds a copy at
// build time and fetches a newer one when it's online. The catalog is fixed
// for the life of the process, but the wiring comes from accepted reports,
// so the body and its ETag are rebuilt with the views (Refresh).

// catalogSnapshot builds the snapshot from the catalog and each
// configuration's wiring (config ID → wiring). Its version is a
// hash of the content, so it changes exactly when the content does. Its date
// is the catalog's last change: date, the day of the last commit to the
// catalog data that the build stamps (Options.CatalogDate), or, in a build
// without that stamp, the newest entry in the catalog's changelog. Neither
// goes into the hash.
func catalogSnapshot(c *catalog.Catalog, date string, wiring map[string]configWiring) (report.CatalogSnapshot, []byte) {
	x := report.CatalogSnapshot{Match: match.Snapshot(c), Capabilities: []report.SnapshotCriterion{}, Fixes: []report.BootFix{}, FixLevels: report.FixLevels}
	for _, m := range x.Match.Macs {
		for _, r := range m.Releases {
			for i := range r.Configs {
				if w, ok := wiring[r.Configs[i].ID]; ok {
					r.Configs[i].Wiring = w.snapshot()
				}
			}
		}
	}
	for _, cp := range c.Capabilities {
		x.Capabilities = append(x.Capabilities, report.SnapshotCriterion{ID: cp.ID, Name: cp.Name, Category: cp.Category(), Live: cp.Live, Retired: cp.Retired})
	}
	for _, f := range c.OmabootFixes {
		x.Fixes = append(x.Fixes, bootFixJSON(c, f))
	}
	body, _ := json.Marshal(x)
	sum := sha256.Sum256(body)
	x.Version = hex.EncodeToString(sum[:8])
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		date = ""
		for _, e := range c.Changelog {
			if _, err := time.Parse(time.DateOnly, e.Date); err == nil && e.Date > date {
				date = e.Date
			}
		}
	}
	x.Date = date
	body, _ = json.Marshal(x)
	return x, body
}

// bootFixJSON is a registry entry as the API shows it.
func bootFixJSON(c *catalog.Catalog, f catalog.OmabootFix) report.BootFix {
	x := report.BootFix{ID: f.ID, Name: f.Name, Changes: f.Changes, Hardware: f.Targets.Hardware, Configs: f.Targets.Configs,
		AppliesTo: c.FixConfigs(&f), AppliesRule: c.FixRule(&f),
		Criteria: f.Targets.Criteria, UpstreamNote: f.UpstreamNote, TestedOn: f.TestedOn, Since: f.Since, URL: BaseURL + fixURL(f.ID)}
	for _, d := range f.Targets.Devices {
		x.Devices = append(x.Devices, report.FixDevice{ID: d.ID, Subsystem: d.Subsystem})
	}
	for _, u := range f.Upstream {
		x.Upstream = append(x.Upstream, report.FixLink{Title: u.Title, URL: u.URL})
	}
	for _, p := range f.Levels {
		x.Levels = append(x.Levels, report.FixPlace{Level: p.Level, What: p.What})
	}
	return x
}

// snapshotSample is the snapshot shortened for /api: the first item of each
// list, one plumbing ID (a map, which abbreviate can't shorten), and a fix's
// texts cut to their start.
func (s *Server) snapshotSample() string {
	var x report.CatalogSnapshot
	if err := json.Unmarshal(s.data().snapJSON, &x); err != nil {
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
	sn := s.data()
	readable(w)
	w.Header().Set("ETag", sn.snapETag)
	if etagMatches(r.Header.Get("If-None-Match"), sn.snapETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Method != http.MethodHead {
		w.Write(sn.snapJSON)
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
