-- Catalog tables. Rebuilt from the YAML catalog by SyncCatalog; never edit by hand.
-- List-like detail fields are JSON text; everything search, stats or results
-- join on is relational.

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT;

INSERT INTO settings (key, value) VALUES ('current_omarchy_major', '4');

-- Controlled vocabulary: kind is lines | ports | features | security_chips |
-- component_kinds | cpu_codenames. attrs holds the kind-specific fields.
CREATE TABLE vocab (
  kind  TEXT NOT NULL,
  key   TEXT NOT NULL,
  name  TEXT NOT NULL,
  attrs TEXT NOT NULL DEFAULT '{}',
  PRIMARY KEY (kind, key)
) STRICT;

CREATE TABLE categories (
  id       TEXT PRIMARY KEY,
  name     TEXT NOT NULL,
  icon     TEXT NOT NULL,
  blocking INTEGER NOT NULL,
  ord      INTEGER NOT NULL
) STRICT;

CREATE TABLE capabilities (
  id          TEXT PRIMARY KEY,
  category_id TEXT NOT NULL REFERENCES categories (id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  description TEXT NOT NULL,
  ord         INTEGER NOT NULL
) STRICT;

CREATE TABLE components (
  id        TEXT PRIMARY KEY,
  kind      TEXT NOT NULL,
  name      TEXT NOT NULL,
  vendor    TEXT NOT NULL,
  role      TEXT NOT NULL,
  driver    TEXT NOT NULL,
  notes     TEXT NOT NULL,
  sources   TEXT NOT NULL,
  uncertain TEXT NOT NULL
) STRICT;

-- Hardware IDs ("pci:10de:0647", "usb:05ac:8290"): the future /api/v1/match looks these up.
CREATE TABLE component_ids (
  component_id TEXT NOT NULL REFERENCES components (id) ON DELETE CASCADE,
  hw_id        TEXT NOT NULL,
  PRIMARY KEY (component_id, hw_id)
) STRICT;
CREATE INDEX component_ids_hw ON component_ids (hw_id);

CREATE TABLE macs (
  identifier     TEXT PRIMARY KEY,           -- "MacBookPro5,1"
  slug           TEXT NOT NULL UNIQUE,       -- URL form "MacBookPro5-1"
  slug_lc        TEXT NOT NULL UNIQUE,       -- case-insensitive lookup
  line           TEXT NOT NULL,
  efi            INTEGER NOT NULL,
  security_chip  TEXT NOT NULL,
  hard_blocker   TEXT NOT NULL,
  board_ids      TEXT NOT NULL,
  research_notes TEXT NOT NULL,
  sources        TEXT NOT NULL,
  uncertain      TEXT NOT NULL,
  ord            INTEGER NOT NULL
) STRICT;

CREATE TABLE releases (
  mac_identifier TEXT NOT NULL REFERENCES macs (identifier) ON DELETE CASCADE,
  id             TEXT NOT NULL,
  name           TEXT NOT NULL,
  announced      TEXT NOT NULL,
  discontinued   TEXT NOT NULL,
  model_numbers  TEXT NOT NULL,
  emc            TEXT NOT NULL,
  sources        TEXT NOT NULL,
  ord            INTEGER NOT NULL,
  PRIMARY KEY (mac_identifier, id)
) STRICT;

-- Config IDs are permanent (data/config-ids.lock). Results (Phase 7) reference them.
CREATE TABLE configs (
  id             TEXT PRIMARY KEY,
  mac_identifier TEXT NOT NULL,
  release_id     TEXT NOT NULL,
  label          TEXT NOT NULL,
  order_numbers  TEXT NOT NULL,
  bto_only       INTEGER NOT NULL,
  cpu            TEXT NOT NULL,
  memory         TEXT NOT NULL,
  storage        TEXT NOT NULL,
  display        TEXT,
  notes          TEXT NOT NULL,
  sources        TEXT NOT NULL,
  uncertain      TEXT NOT NULL,
  ord            INTEGER NOT NULL,
  FOREIGN KEY (mac_identifier, release_id) REFERENCES releases (mac_identifier, id) ON DELETE CASCADE
) STRICT;
CREATE INDEX configs_mac ON configs (mac_identifier);

CREATE TABLE config_aliases (
  alias     TEXT PRIMARY KEY,
  config_id TEXT NOT NULL REFERENCES configs (id) ON DELETE CASCADE
) STRICT;

CREATE TABLE config_components (
  config_id    TEXT NOT NULL REFERENCES configs (id) ON DELETE CASCADE,
  component_id TEXT NOT NULL REFERENCES components (id) ON DELETE CASCADE,
  bto          INTEGER NOT NULL,
  ord          INTEGER NOT NULL,
  PRIMARY KEY (config_id, component_id, bto)
) STRICT;
CREATE INDEX config_components_comp ON config_components (component_id);

CREATE TABLE config_ports (
  config_id TEXT NOT NULL REFERENCES configs (id) ON DELETE CASCADE,
  port      TEXT NOT NULL,
  count     INTEGER NOT NULL,
  PRIMARY KEY (config_id, port)
) STRICT;

CREATE TABLE config_features (
  config_id TEXT NOT NULL REFERENCES configs (id) ON DELETE CASCADE,
  feature   TEXT NOT NULL,
  PRIMARY KEY (config_id, feature)
) STRICT;

-- Materialized applicability (catalog.Applicable).
CREATE TABLE config_capabilities (
  config_id     TEXT NOT NULL REFERENCES configs (id) ON DELETE CASCADE,
  capability_id TEXT NOT NULL REFERENCES capabilities (id) ON DELETE CASCADE,
  PRIMARY KEY (config_id, capability_id)
) STRICT;
