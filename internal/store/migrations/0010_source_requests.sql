-- Registering a source on the site (PLAN.md §30d). A test-tool author asks
-- at /api/register; a maintainer approves or declines in /admin; the
-- applicant collects the key once from a private link. Tokens, like keys,
-- are stored only as hashes.
ALTER TABLE sources ADD COLUMN repo_url TEXT NOT NULL DEFAULT '';
ALTER TABLE sources ADD COLUMN contact_email TEXT NOT NULL DEFAULT ''; -- shown only to maintainers

CREATE TABLE source_requests (
  id          INTEGER PRIMARY KEY,
  token_hash  TEXT NOT NULL UNIQUE,          -- the applicant's status link
  source_id   TEXT NOT NULL,                 -- proposed; the maintainer may change it
  name        TEXT NOT NULL,
  repo_url    TEXT NOT NULL,
  homepage    TEXT NOT NULL DEFAULT '',
  email       TEXT NOT NULL,                 -- emptied 30 days after a decline
  description TEXT NOT NULL,
  state       TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'approved', 'declined')),
  reason      TEXT NOT NULL DEFAULT '',      -- why it was declined; the applicant sees it
  created_at  TEXT NOT NULL,
  decided_at  TEXT NOT NULL DEFAULT '',
  decided_by  TEXT NOT NULL DEFAULT ''
) STRICT;

-- One-time links that reveal a source's key: made on approval (on the
-- request's own token) or by a maintainer ("new key link").
CREATE TABLE key_links (
  token_hash TEXT PRIMARY KEY,
  source_id  TEXT NOT NULL REFERENCES sources (id),
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at    TEXT NOT NULL DEFAULT ''       -- when the key was shown, or 'superseded'
) STRICT;
