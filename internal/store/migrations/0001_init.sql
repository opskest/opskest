CREATE TABLE users (
  id INTEGER PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL DEFAULT '',
  avatar_url TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin','viewer','disabled')),
  created_at INTEGER NOT NULL,
  last_login_at INTEGER
);
CREATE TABLE sessions (
  id_hash BLOB PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  csrf_token TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) WITHOUT ROWID;
CREATE INDEX sessions_user ON sessions(user_id);
CREATE INDEX sessions_expires ON sessions(expires_at);

CREATE TABLE groups (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE,
  sort INTEGER NOT NULL DEFAULT 0,
  description TEXT NOT NULL DEFAULT ''
);
CREATE TABLE tags (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE COLLATE NOCASE,
  color TEXT NOT NULL DEFAULT '#6b7385'
);
CREATE TABLE monitors (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('http','tcp','ping','dns','push','websocket')),
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
CREATE INDEX monitors_group ON monitors(group_id);
CREATE TABLE monitor_tags (
  monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (monitor_id, tag_id)
) WITHOUT ROWID;
CREATE INDEX monitor_tags_tag ON monitor_tags(tag_id);
CREATE TABLE monitor_runtime (
  monitor_id INTEGER PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
  cert_not_after INTEGER,
  cert_issuer TEXT,
  cert_subject TEXT,
  cert_alerted_days INTEGER,
  domain_expires_at INTEGER,
  domain_alerted_days INTEGER,
  domain_checked_at INTEGER,
  last_push_at INTEGER
);
CREATE TABLE channels (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('slack','email','teams','gchat','webhook','pagerduty','opsgenie')),
  config_enc BLOB NOT NULL,
  is_default INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL
);
CREATE TABLE monitor_channels (
  monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  PRIMARY KEY (monitor_id, channel_id)
) WITHOUT ROWID;
CREATE INDEX monitor_channels_channel ON monitor_channels(channel_id);

-- No FK on heartbeats/rollups: they are the hot write path, and DeleteMonitor
-- purges them explicitly in the same transaction.
CREATE TABLE heartbeats (
  monitor_id INTEGER NOT NULL,
  ts INTEGER NOT NULL,
  status INTEGER NOT NULL,
  latency_ms INTEGER,
  code INTEGER,
  dns_ms INTEGER,
  tcp_ms INTEGER,
  tls_ms INTEGER,
  ttfb_ms INTEGER,
  err_class TEXT,
  msg TEXT,
  maint INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (monitor_id, ts)
) WITHOUT ROWID;
CREATE TABLE rollup_hourly (
  monitor_id INTEGER NOT NULL,
  bucket INTEGER NOT NULL,
  n_up INTEGER NOT NULL DEFAULT 0,
  n_degraded INTEGER NOT NULL DEFAULT 0,
  n_pending INTEGER NOT NULL DEFAULT 0,
  n_down INTEGER NOT NULL DEFAULT 0,
  n_unknown INTEGER NOT NULL DEFAULT 0,
  n_maint INTEGER NOT NULL DEFAULT 0,
  lat_sum INTEGER NOT NULL DEFAULT 0,
  lat_count INTEGER NOT NULL DEFAULT 0,
  lat_min INTEGER,
  lat_max INTEGER,
  PRIMARY KEY (monitor_id, bucket)
) WITHOUT ROWID;
CREATE TABLE rollup_daily (
  monitor_id INTEGER NOT NULL,
  bucket INTEGER NOT NULL,
  n_up INTEGER NOT NULL DEFAULT 0,
  n_degraded INTEGER NOT NULL DEFAULT 0,
  n_pending INTEGER NOT NULL DEFAULT 0,
  n_down INTEGER NOT NULL DEFAULT 0,
  n_unknown INTEGER NOT NULL DEFAULT 0,
  n_maint INTEGER NOT NULL DEFAULT 0,
  lat_sum INTEGER NOT NULL DEFAULT 0,
  lat_count INTEGER NOT NULL DEFAULT 0,
  lat_min INTEGER,
  lat_max INTEGER,
  PRIMARY KEY (monitor_id, bucket)
) WITHOUT ROWID;

CREATE TABLE outages (
  id INTEGER PRIMARY KEY,
  monitor_id INTEGER NOT NULL,
  started_at INTEGER NOT NULL,
  ended_at INTEGER,
  first_reason TEXT NOT NULL DEFAULT '',
  err_class TEXT NOT NULL DEFAULT '',
  acked_by TEXT,
  acked_at INTEGER,
  note TEXT,
  note_by TEXT
);
CREATE INDEX outages_monitor ON outages(monitor_id, started_at);
CREATE INDEX outages_started ON outages(started_at);
CREATE INDEX outages_active ON outages(monitor_id) WHERE ended_at IS NULL;

CREATE TABLE incidents (
  id INTEGER PRIMARY KEY,
  title TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('investigating','identified','monitoring','resolved')),
  impact TEXT NOT NULL CHECK (impact IN ('minor','major','maintenance')),
  created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  resolved_at INTEGER
);
CREATE INDEX incidents_created ON incidents(created_at);
CREATE TABLE incident_updates (
  id INTEGER PRIMARY KEY,
  incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  status TEXT NOT NULL CHECK (status IN ('investigating','identified','monitoring','resolved')),
  body TEXT NOT NULL,
  created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX incident_updates_incident ON incident_updates(incident_id, created_at);
CREATE TABLE incident_monitors (
  incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
  PRIMARY KEY (incident_id, monitor_id)
) WITHOUT ROWID;
CREATE TABLE incident_pages (
  incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
  page_id INTEGER NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
  PRIMARY KEY (incident_id, page_id)
) WITHOUT ROWID;

CREATE TABLE maintenance (
  id INTEGER PRIMARY KEY,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL CHECK (kind IN ('once','weekly')),
  starts_at INTEGER,
  ends_at INTEGER,
  weekdays INTEGER NOT NULL DEFAULT 0,
  start_minute INTEGER NOT NULL DEFAULT 0,
  duration_min INTEGER NOT NULL DEFAULT 0,
  tz TEXT NOT NULL CHECK (tz IN ('Asia/Kolkata','UTC')),
  active INTEGER NOT NULL DEFAULT 1,
  created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE maintenance_targets (
  maintenance_id INTEGER NOT NULL REFERENCES maintenance(id) ON DELETE CASCADE,
  monitor_id INTEGER REFERENCES monitors(id) ON DELETE CASCADE,
  group_id INTEGER REFERENCES groups(id) ON DELETE CASCADE,
  CHECK ((monitor_id IS NULL) <> (group_id IS NULL))
);
CREATE INDEX maintenance_targets_m ON maintenance_targets(maintenance_id);

CREATE TABLE status_pages (
  id INTEGER PRIMARY KEY,
  slug TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  visibility TEXT NOT NULL CHECK (visibility IN ('public','private')),
  layout TEXT NOT NULL DEFAULT 'list' CHECK (layout IN ('list','board')),
  show_latency INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE status_page_items (
  id INTEGER PRIMARY KEY,
  page_id INTEGER NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
  sort INTEGER NOT NULL,
  group_id INTEGER REFERENCES groups(id) ON DELETE CASCADE,
  monitor_id INTEGER REFERENCES monitors(id) ON DELETE CASCADE,
  display_name TEXT NOT NULL DEFAULT '',
  CHECK ((monitor_id IS NULL) <> (group_id IS NULL))
);
CREATE INDEX status_page_items_page ON status_page_items(page_id, sort);

CREATE TABLE alert_log (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  channel_id INTEGER,
  monitor_id INTEGER,
  event TEXT NOT NULL,
  ok INTEGER NOT NULL,
  attempt INTEGER NOT NULL,
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX alert_log_ts ON alert_log(ts);
CREATE TABLE audit_log (
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  user_id INTEGER,
  action TEXT NOT NULL,
  entity TEXT NOT NULL,
  entity_id INTEGER,
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_ts ON audit_log(ts);
CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
) WITHOUT ROWID;
