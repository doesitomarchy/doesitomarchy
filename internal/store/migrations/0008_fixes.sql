-- Fix tracking (PLAN.md §26): one row per issue in the fix repo
-- (doesitomarchy/wecanfixeverything) that carries a criterion: label. The
-- issue is the record; this is the site's copy, refreshed by the webhook and
-- a slow poll. A fix covers a criterion on a component (every configuration
-- with it) or on one configuration.
CREATE TABLE fixes (
  issue          INTEGER PRIMARY KEY,           -- issue number in the fix repo
  capability_id  TEXT NOT NULL,                 -- from its criterion: label
  component_id   TEXT NOT NULL DEFAULT '',      -- from its component: label
  config_id      TEXT NOT NULL DEFAULT '',      -- from its config: label
  title          TEXT NOT NULL,
  url            TEXT NOT NULL,
  open           INTEGER NOT NULL,              -- 1 while the issue is open
  state_reason   TEXT NOT NULL DEFAULT '',      -- GitHub's: completed | not_planned | reopened
  assignee       TEXT NOT NULL DEFAULT '',      -- the claim
  proposed       INTEGER NOT NULL DEFAULT 0,    -- fix-proposed label, or an open linked pull request
  last_activity  TEXT NOT NULL,                 -- RFC 3339, UTC
  closed_at      TEXT NOT NULL DEFAULT '',
  fix_link       TEXT NOT NULL DEFAULT '',      -- the pull request or commit that closed it
  opened_by      TEXT NOT NULL DEFAULT '',      -- maintainer handle, when opened from the site
  synced_at      TEXT NOT NULL
) STRICT;
CREATE INDEX fixes_capability ON fixes (capability_id);

-- Webhook deliveries already handled, so a redelivery is a no-op.
CREATE TABLE fix_deliveries (
  id          TEXT PRIMARY KEY,                 -- X-GitHub-Delivery
  received_at TEXT NOT NULL
) STRICT;
