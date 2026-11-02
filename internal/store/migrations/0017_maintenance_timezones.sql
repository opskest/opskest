-- Timezone validation is performed by the maintenance API using Go's IANA
-- database. Preserve existing windows and target foreign keys while removing
-- the original India/UTC-only constraint.
CREATE TABLE maintenance_new (
  id INTEGER PRIMARY KEY,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL CHECK (kind IN ('once','weekly')),
  starts_at INTEGER,
  ends_at INTEGER,
  weekdays INTEGER NOT NULL DEFAULT 0,
  start_minute INTEGER NOT NULL DEFAULT 0,
  duration_min INTEGER NOT NULL DEFAULT 0,
  tz TEXT NOT NULL CHECK (length(tz) BETWEEN 1 AND 255),
  active INTEGER NOT NULL DEFAULT 1,
  created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
INSERT INTO maintenance_new SELECT * FROM maintenance;
DROP TABLE maintenance;
ALTER TABLE maintenance_new RENAME TO maintenance;
