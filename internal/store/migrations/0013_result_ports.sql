-- Port wiring learned from reports (LIVE-PLAN §23.1): a report's ports
-- entries place bus paths on its configuration's connectors. The snapshot's
-- wiring counts a connector's path once two accepted reports agree on it, or
-- one a maintainer marked "wiring checked".
CREATE TABLE result_ports (
  result_id   INTEGER NOT NULL REFERENCES results (id),
  ord         INTEGER NOT NULL,
  test        TEXT NOT NULL,                     -- ports.usb-a | ports.usb-c | ports.sd-card | ports.thunderbolt
  connector   TEXT NOT NULL,                     -- from the configuration's layout, e.g. left-2
  paths       TEXT NOT NULL,                     -- JSON list: usb2:0000:00:14.0/1, pci:…, tb:…
  how         TEXT NOT NULL CHECK (how IN ('ring', 'asked', 'confirmed', 'corrected')),
  wiring_said TEXT NOT NULL DEFAULT '',          -- corrected only: the connector the wiring gave
  PRIMARY KEY (result_id, ord),
  UNIQUE (result_id, test, connector)
) STRICT;

-- A maintainer's mark that a report's port answers were checked: one such
-- accepted report is enough for its wiring to count.
ALTER TABLE results ADD COLUMN wiring_checked_at TEXT NOT NULL DEFAULT '';
ALTER TABLE results ADD COLUMN wiring_checked_by TEXT NOT NULL DEFAULT '';
