ALTER TABLE authors ADD COLUMN monitored INTEGER NOT NULL DEFAULT 0;
ALTER TABLE authors ADD COLUMN discovery_checked_at TEXT;
ALTER TABLE series ADD COLUMN monitored INTEGER NOT NULL DEFAULT 0;
ALTER TABLE series ADD COLUMN discovery_checked_at TEXT;
ALTER TABLE books ADD COLUMN availability_checked_at TEXT;
CREATE TABLE catalog_actions (
 kind TEXT NOT NULL CHECK(kind IN ('authors','series')),
 entity_id TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN ('discover','downloads')),
 PRIMARY KEY(kind,entity_id,action)
);
