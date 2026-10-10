-- Fix tracking through GitHub issues (0008, PLAN.md §26) is retired: no fix
-- issue was ever opened. Fixes now live upstream, and the site lists the
-- OmaBoot? fixes from the catalog (data/fixes.yaml). The maintainer's white
-- flag stays: it is the unsupported table (0003), not these.
DROP TABLE fix_deliveries;
DROP INDEX fixes_capability;
DROP TABLE fixes;
