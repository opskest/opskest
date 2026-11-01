-- Widen monitors.type for the database/service check types (mysql,
-- postgres, redis, grpc, smtp). SQLite can't alter a CHECK constraint, so
-- the table is rebuilt; the migration runner turns foreign keys off around
-- it (SQLite's documented procedure) so child rows are kept, and verifies
-- them with foreign_key_check afterwards. Type validity is enforced in Go.
CREATE TABLE monitors_new (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  group_id INTEGER REFERENCES groups(id) ON DELETE SET NULL,
  interval_s INTEGER NOT NULL,
  retry_interval_s INTEGER NOT NULL,
  retries INTEGER NOT NULL,
  timeout_ms INTEGER NOT NULL,
  degraded_ms INTEGER NOT NULL DEFAULT 0,
  ssl_warn_days INTEGER NOT NULL DEFAULT 14,
  upside_down INTEGER NOT NULL DEFAULT 0,
  notify_degraded INTEGER NOT NULL DEFAULT 0,
  reminder_min INTEGER NOT NULL DEFAULT 0,
  paused INTEGER NOT NULL DEFAULT 0,
  description TEXT NOT NULL DEFAULT '',
  runbook TEXT NOT NULL DEFAULT '',
  settings_json TEXT NOT NULL DEFAULT '{}',
  secrets_enc BLOB,
  push_token TEXT UNIQUE,
  created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
INSERT INTO monitors_new SELECT id, name, type, target, group_id, interval_s, retry_interval_s, retries, timeout_ms,
  degraded_ms, ssl_warn_days, upside_down, notify_degraded, reminder_min, paused, description, runbook,
  settings_json, secrets_enc, push_token, created_by, created_at, updated_at FROM monitors;
DROP TABLE monitors;
ALTER TABLE monitors_new RENAME TO monitors;
CREATE INDEX monitors_group ON monitors(group_id);
