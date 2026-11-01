-- Channel types are validated by the alert package (alert.ValidateConfig), so
-- adding a service no longer needs a schema change. The original table
-- listed the first seven types in a CHECK, which rejected every newer one.
-- Rows, ids and the foreign keys that point at channels are preserved.
CREATE TABLE channels_new (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (length(type) BETWEEN 1 AND 40),
  config_enc BLOB NOT NULL,
  is_default INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
INSERT INTO channels_new (id, name, type, config_enc, is_default, created_at)
  SELECT id, name, type, config_enc, is_default, created_at FROM channels;
DROP TABLE channels;
ALTER TABLE channels_new RENAME TO channels;
