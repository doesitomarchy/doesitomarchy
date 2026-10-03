-- The OmacDiag adapter (PLAN.md §24): a new review-flag kind, driver_missing
-- (a native report said a device or its driver was missing; counted as
-- failed). SQLite can't change a CHECK constraint in place, so result_flags
-- is rebuilt as in 0004.
CREATE TABLE result_flags_new (
  id          INTEGER PRIMARY KEY,
  result_id   INTEGER NOT NULL REFERENCES results (id),
  kind        TEXT NOT NULL CHECK (kind IN ('inapplicable_item', 'hardware_mismatch', 'unmapped_check', 'conflict',
                                            'config_ambiguous', 'duplicate', 'driver_missing')),
  detail      TEXT NOT NULL,
  resolution  TEXT NOT NULL DEFAULT '',
  resolved_by TEXT NOT NULL DEFAULT '',
  resolved_at TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO result_flags_new SELECT id, result_id, kind, detail, resolution, resolved_by, resolved_at FROM result_flags;
DROP TABLE result_flags;
ALTER TABLE result_flags_new RENAME TO result_flags;
CREATE INDEX result_flags_result ON result_flags (result_id);
