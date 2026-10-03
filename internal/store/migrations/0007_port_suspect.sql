-- Port groups (PLAN.md §25.1a): a new review-flag kind, port_suspect (a
-- connector failed while another on the same controller passed: probably a
-- damaged port). Rebuilds result_flags as in 0004 and 0006.
CREATE TABLE result_flags_new (
  id          INTEGER PRIMARY KEY,
  result_id   INTEGER NOT NULL REFERENCES results (id),
  kind        TEXT NOT NULL CHECK (kind IN ('inapplicable_item', 'hardware_mismatch', 'unmapped_check', 'conflict',
                                            'config_ambiguous', 'duplicate', 'driver_missing', 'port_suspect')),
  detail      TEXT NOT NULL,
  resolution  TEXT NOT NULL DEFAULT '',
  resolved_by TEXT NOT NULL DEFAULT '',
  resolved_at TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO result_flags_new SELECT id, result_id, kind, detail, resolution, resolved_by, resolved_at FROM result_flags;
DROP TABLE result_flags;
ALTER TABLE result_flags_new RENAME TO result_flags;
CREATE INDEX result_flags_result ON result_flags (result_id);
