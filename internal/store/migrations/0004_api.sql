-- Slice 7b (PLAN.md §22): new review-flag kinds and the maintainers list.

-- SQLite can't change a CHECK constraint in place, so result_flags is
-- rebuilt with the new kinds: config_ambiguous (the hardware matches several
-- configurations; a maintainer picks one) and duplicate (same source, tester,
-- configuration and test time as an earlier report).
CREATE TABLE result_flags_new (
  id          INTEGER PRIMARY KEY,
  result_id   INTEGER NOT NULL REFERENCES results (id),
  kind        TEXT NOT NULL CHECK (kind IN ('inapplicable_item', 'hardware_mismatch', 'unmapped_check', 'conflict',
                                            'config_ambiguous', 'duplicate')),
  detail      TEXT NOT NULL,
  resolution  TEXT NOT NULL DEFAULT '',
  resolved_by TEXT NOT NULL DEFAULT '',
  resolved_at TEXT NOT NULL DEFAULT ''
) STRICT;
INSERT INTO result_flags_new SELECT id, result_id, kind, detail, resolution, resolved_by, resolved_at FROM result_flags;
DROP TABLE result_flags;
ALTER TABLE result_flags_new RENAME TO result_flags;
CREATE INDEX result_flags_result ON result_flags (result_id);

-- People who may moderate in /admin. Cloudflare Access proves the e-mail
-- address; this table maps it to the handle recorded on every action.
CREATE TABLE maintainers (
  handle     TEXT PRIMARY KEY,
  email      TEXT NOT NULL UNIQUE COLLATE NOCASE,
  added_at   TEXT NOT NULL,
  removed_at TEXT NOT NULL DEFAULT ''
) STRICT;

-- The notice a submitting tool showed the tester (PLAN §22.1: consent by notice).
ALTER TABLE results ADD COLUMN consent_notice TEXT NOT NULL DEFAULT '';

-- When the hardware fits several configurations: all of them, as a JSON
-- array, so /admin can offer them (config_id holds the first until a
-- maintainer picks).
ALTER TABLE results ADD COLUMN candidates TEXT NOT NULL DEFAULT '[]';
