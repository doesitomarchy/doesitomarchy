-- Reports from live boots (OmaBoot? Live): where the test ran, the OmaBoot?
-- fixes that took effect (data/fixes.yaml IDs) and any replaced parts, kept
-- with the result. Results so far all ran on an installed Omarchy, with no
-- fixes and no replaced parts.
ALTER TABLE results ADD COLUMN context TEXT NOT NULL DEFAULT 'installed'
  CHECK (context IN ('installed', 'live'));
ALTER TABLE results ADD COLUMN fixes TEXT NOT NULL DEFAULT '[]';          -- JSON list of fix IDs
ALTER TABLE results ADD COLUMN replaced_parts TEXT NOT NULL DEFAULT '[]'; -- JSON list of {kind, detail, ids}

-- A new item method, challenge (a code a person or a phone gave back), and a
-- new skip reason, live-limit (a live boot can't decide the criterion).
-- Rebuilds result_items, as 0009 rebuilt result_flags.
CREATE TABLE result_items_new (
  result_id     INTEGER NOT NULL REFERENCES results (id),
  capability_id TEXT NOT NULL REFERENCES capabilities (id),
  connector     TEXT NOT NULL DEFAULT '',           -- physical connector, for per-connector port items (7c)
  status        TEXT NOT NULL CHECK (status IN ('supported', 'partial', 'failed', 'not_tested')),
  method        TEXT NOT NULL DEFAULT '' CHECK (method IN ('', 'automatic', 'observed', 'fixture', 'challenge')),
  reason        TEXT NOT NULL DEFAULT '' CHECK (reason IN ('', 'no-equipment', 'not-in-profile', 'uncertain', 'live-limit', 'other')),
  note          TEXT NOT NULL DEFAULT '',           -- scrubbed
  evidence      TEXT NOT NULL DEFAULT '',           -- why it failed, as reported (scrubbed)
  applicable    INTEGER NOT NULL,                   -- 0: stored and flagged, never counted
  ord           INTEGER NOT NULL,
  PRIMARY KEY (result_id, capability_id, connector)
) STRICT;
INSERT INTO result_items_new SELECT result_id, capability_id, connector, status, method, reason, note, evidence, applicable, ord FROM result_items;
DROP TABLE result_items;
ALTER TABLE result_items_new RENAME TO result_items;
CREATE INDEX result_items_capability ON result_items (capability_id);
