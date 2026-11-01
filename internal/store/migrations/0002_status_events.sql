CREATE TABLE status_events (
  id INTEGER PRIMARY KEY,
  monitor_id INTEGER NOT NULL,
  ts INTEGER NOT NULL,
  from_status INTEGER NOT NULL,
  to_status INTEGER NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  err_class TEXT NOT NULL DEFAULT ''
);
CREATE INDEX status_events_ts ON status_events(ts);
CREATE INDEX status_events_monitor ON status_events(monitor_id, ts);
