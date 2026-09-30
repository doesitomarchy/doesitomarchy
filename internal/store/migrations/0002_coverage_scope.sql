-- Coverage scope (data/coverage.yaml): the reason a config is not counted in
-- the coverage metrics, or '' when it counts. Set by SyncCatalog.
ALTER TABLE configs ADD COLUMN coverage_excluded TEXT NOT NULL DEFAULT '';
