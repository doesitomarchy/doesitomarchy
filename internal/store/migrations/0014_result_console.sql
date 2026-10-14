-- Commands typed in OmaBoot? Live's console that redid or changed a result
-- (LIVE-PLAN §24): kept with the result for review. Production refuses a
-- report with a change entry; staging takes it (DOIOMAD_ACCEPT_CONSOLE_CHANGES).
ALTER TABLE results ADD COLUMN console TEXT NOT NULL DEFAULT '[]'; -- JSON list of {command, kind}
