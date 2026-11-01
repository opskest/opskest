-- User-defined starting points for new monitors. config is the monitor form's
-- settings as JSON, without anything identifying (name, target, group) and
-- without secrets; the API strips those before it gets here.
CREATE TABLE monitor_templates (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL COLLATE NOCASE UNIQUE CHECK (length(name) BETWEEN 1 AND 80),
  description TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL,
  config TEXT NOT NULL,
  created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
