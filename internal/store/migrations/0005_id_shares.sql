-- PF-3 (PLAN.md §23.3): hardware IDs that visitors chose to share from
-- Identify my Mac, for maintainers to compare with the catalog. A share holds
-- only the extracted IDs, two optional answers and the UTC date: never the
-- pasted text, an IP address or a time of day.
CREATE TABLE id_shares (
  id          INTEGER PRIMARY KEY,
  shared_on   TEXT NOT NULL,                -- YYYY-MM-DD, UTC
  probe_hash  TEXT NOT NULL,                -- SHA-256 of the normalised IDs and answers
  product     TEXT NOT NULL DEFAULT '',     -- the model identifier as reported, known or not
  board_id    TEXT NOT NULL DEFAULT '',
  cpu         TEXT NOT NULL DEFAULT '',
  pci         TEXT NOT NULL DEFAULT '[]',   -- JSON array of "vvvv:dddd"
  modified    TEXT NOT NULL DEFAULT '' CHECK (modified IN ('', 'yes', 'no', 'unsure')),
  release_id  TEXT NOT NULL DEFAULT '',     -- "Which release does About This Mac show?"
  reviewed_at TEXT NOT NULL DEFAULT '',
  reviewed_by TEXT NOT NULL DEFAULT '',
  UNIQUE (probe_hash, shared_on)            -- the same share twice in a day is stored once
) STRICT;
CREATE INDEX id_shares_product ON id_shares (product);
CREATE INDEX id_shares_day ON id_shares (shared_on);
