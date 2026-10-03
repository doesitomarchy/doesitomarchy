-- Omarchy channels and builds (PLAN.md §28). A result records its channel
-- (stable, rc, beta, edge or dev) and the Omarchy commit it ran, with that
-- commit's date: the newest build wins, whatever the channel, and stable
-- results alone decide verdicts. Results so far are all stable releases.
ALTER TABLE results ADD COLUMN omarchy_channel TEXT NOT NULL DEFAULT 'stable'
  CHECK (omarchy_channel IN ('stable', 'rc', 'beta', 'edge', 'dev'));
ALTER TABLE results ADD COLUMN omarchy_commit TEXT NOT NULL DEFAULT '';   -- full SHA in basecamp/omarchy, once known
ALTER TABLE results ADD COLUMN omarchy_built_at TEXT NOT NULL DEFAULT ''; -- the commit's date (RFC 3339, UTC); '' until known

-- Builds looked up on GitHub, so each is looked up once.
CREATE TABLE omarchy_builds (
  key          TEXT PRIMARY KEY,               -- "commit:<abbreviated or full SHA>" or "release:<canonical version>"
  commit_sha   TEXT NOT NULL DEFAULT '',       -- '' when not found
  committed_at TEXT NOT NULL DEFAULT '',
  checked_at   TEXT NOT NULL,
  error        TEXT NOT NULL DEFAULT ''        -- why the last lookup failed
) STRICT;

-- Two new review-flag kinds: regression (fails on a newer build than the
-- current pass) and unknown_build (its build's date isn't known yet).
-- Rebuilds result_flags as in 0004, 0006 and 0007.
CREATE TABLE result_flags_new (
  id          INTEGER PRIMARY KEY,
  result_id   INTEGER NOT NULL REFERENCES results (id),
  kind        TEXT NOT NULL CHECK (kind IN ('inapplicable_item', 'hardware_mismatch', 'unmapped_check', 'conflict',
                                            'config_ambiguous', 'duplicate', 'driver_missing', 'port_suspect',
                                            'regression', 'unknown_build')),
  detail      TEXT NOT NULL,
  resolution  TEXT NOT NULL DEFAULT '',
  resolved_by TEXT NOT NULL DEFAULT '',
  resolved_at TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO result_flags_new SELECT id, result_id, kind, detail, resolution, resolved_by, resolved_at FROM result_flags;
DROP TABLE result_flags;
ALTER TABLE result_flags_new RENAME TO result_flags;
CREATE INDEX result_flags_result ON result_flags (result_id);
