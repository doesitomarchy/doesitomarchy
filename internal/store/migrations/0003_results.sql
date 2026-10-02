-- Test results (PLAN.md §20.2, §21.3). Unlike the catalog tables, these are
-- never rebuilt and never deleted from: SyncCatalog does not touch them.
--
-- They reference catalog IDs with plain foreign keys (no cascade). Catalog
-- sync rewrites the catalog inside one transaction with deferred foreign
-- keys, so a sync that would drop a config or capability that results
-- reference fails at commit instead of orphaning or deleting results.
-- Unused criteria are retired, never removed.

ALTER TABLE capabilities ADD COLUMN retired INTEGER NOT NULL DEFAULT 0;

INSERT INTO settings (key, value) VALUES
  ('data_version', '0'),                          -- bumped by every moderation change; the server rebuilds on change
  ('contact_salt', lower(hex(randomblob(32))));   -- salt for hashing tester contact details

-- Apps allowed to submit results. Keys are issued by maintainers and stored hashed (7b).
CREATE TABLE sources (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  homepage   TEXT NOT NULL DEFAULT '',
  trust      TEXT NOT NULL DEFAULT 'pending' CHECK (trust IN ('pending', 'trusted')),
  key_hash   TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  revoked_at TEXT NOT NULL DEFAULT ''
) STRICT;

INSERT INTO sources (id, name, created_at) VALUES ('manual', 'Manual entry', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));

CREATE TABLE results (
  id               INTEGER PRIMARY KEY,
  config_id        TEXT NOT NULL REFERENCES configs (id),
  source_id        TEXT NOT NULL REFERENCES sources (id),
  source_version   TEXT NOT NULL DEFAULT '',
  profile          TEXT NOT NULL DEFAULT '',
  workflow         TEXT NOT NULL DEFAULT '',
  schema           TEXT NOT NULL,                   -- "doesitomarchy/result/v1"
  format           TEXT NOT NULL,                   -- input format: "doesitomarchy/result/v1" or a source's native format
  tester_handle    TEXT NOT NULL DEFAULT '',
  contact_hash     TEXT NOT NULL DEFAULT '',
  tested_on        TEXT NOT NULL,                   -- YYYY-MM-DD
  omarchy_version  TEXT NOT NULL,                   -- as reported
  omarchy_major    INTEGER NOT NULL,
  omarchy_minor    INTEGER NOT NULL,
  omarchy_patch    INTEGER NOT NULL,
  omarchy_revision TEXT NOT NULL DEFAULT '',
  omarchy_image    TEXT NOT NULL DEFAULT '',
  kernel           TEXT NOT NULL DEFAULT '',
  notes            TEXT NOT NULL DEFAULT '',        -- scrubbed
  hardware         TEXT NOT NULL DEFAULT '{}',      -- scrubbed probe (JSON)
  hw_fingerprint   TEXT NOT NULL DEFAULT '',        -- salted hash of board-id + component IDs, for de-duplication
  state            TEXT NOT NULL CHECK (state IN ('pending', 'accepted', 'rejected', 'retracted')),
  state_reason     TEXT NOT NULL DEFAULT '',
  state_by         TEXT NOT NULL DEFAULT '',
  state_at         TEXT NOT NULL DEFAULT '',
  submitted_by     TEXT NOT NULL,                   -- maintainer (import) or source (API)
  submitted_at     TEXT NOT NULL
) STRICT;
CREATE INDEX results_config_state ON results (config_id, state);
CREATE INDEX results_state ON results (state, id);

-- The raw submission, scrubbed. Private at first; public later (PLAN §20.1).
CREATE TABLE result_reports (
  result_id  INTEGER PRIMARY KEY REFERENCES results (id),
  format     TEXT NOT NULL,
  body       TEXT NOT NULL,
  size       INTEGER NOT NULL,
  visibility TEXT NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'public'))
) STRICT;

CREATE TABLE result_items (
  result_id     INTEGER NOT NULL REFERENCES results (id),
  capability_id TEXT NOT NULL REFERENCES capabilities (id),
  connector     TEXT NOT NULL DEFAULT '',           -- physical connector, for per-connector port items (7c)
  status        TEXT NOT NULL CHECK (status IN ('supported', 'partial', 'failed', 'not_tested')),
  method        TEXT NOT NULL DEFAULT '' CHECK (method IN ('', 'automatic', 'observed', 'fixture')),
  reason        TEXT NOT NULL DEFAULT '' CHECK (reason IN ('', 'no-equipment', 'not-in-profile', 'uncertain', 'other')),
  note          TEXT NOT NULL DEFAULT '',           -- scrubbed
  evidence      TEXT NOT NULL DEFAULT '',           -- why it failed, as reported (scrubbed)
  applicable    INTEGER NOT NULL,                   -- 0: stored and flagged, never counted
  ord           INTEGER NOT NULL,
  PRIMARY KEY (result_id, capability_id, connector)
) STRICT;
CREATE INDEX result_items_capability ON result_items (capability_id);

-- Checks that are not (yet) our criteria: kept, shown as unverified extras.
CREATE TABLE result_extras (
  result_id INTEGER NOT NULL REFERENCES results (id),
  ord       INTEGER NOT NULL,
  check_id  TEXT NOT NULL,
  label     TEXT NOT NULL DEFAULT '',
  status    TEXT NOT NULL DEFAULT '',
  detail    TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (result_id, ord)
) STRICT;

CREATE TABLE result_flags (
  id          INTEGER PRIMARY KEY,
  result_id   INTEGER NOT NULL REFERENCES results (id),
  kind        TEXT NOT NULL CHECK (kind IN ('inapplicable_item', 'hardware_mismatch', 'unmapped_check', 'conflict')),
  detail      TEXT NOT NULL,
  resolution  TEXT NOT NULL DEFAULT '',
  resolved_by TEXT NOT NULL DEFAULT '',
  resolved_at TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX result_flags_result ON result_flags (result_id);

-- Every moderation action, in order: who did what to which result, and why.
CREATE TABLE result_events (
  id        INTEGER PRIMARY KEY,
  result_id INTEGER NOT NULL REFERENCES results (id),
  at        TEXT NOT NULL,
  actor     TEXT NOT NULL,
  action    TEXT NOT NULL,                          -- submitted | accepted | rejected | retracted | flag-resolved
  detail    TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX result_events_result ON result_events (result_id);

-- The maintainer's white flag (PLAN §20.1): a capability given up on, on one
-- config or on every config with a component. Cleared, never deleted.
CREATE TABLE unsupported (
  id            INTEGER PRIMARY KEY,
  capability_id TEXT NOT NULL REFERENCES capabilities (id),
  config_id     TEXT NOT NULL DEFAULT '',
  component_id  TEXT NOT NULL DEFAULT '',
  reason        TEXT NOT NULL,
  set_by        TEXT NOT NULL,
  set_at        TEXT NOT NULL,
  cleared_by    TEXT NOT NULL DEFAULT '',
  cleared_at    TEXT NOT NULL DEFAULT '',
  CHECK ((config_id = '') <> (component_id = ''))
) STRICT;
