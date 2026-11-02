-- Latest worldwide result per monitor and country (rotating Globalping
-- sweeps). One row per (monitor, country); a sweep replaces them.
CREATE TABLE region_results (
  monitor_id INTEGER NOT NULL,
  country TEXT NOT NULL,
  city TEXT NOT NULL DEFAULT '',
  ok INTEGER NOT NULL,
  code INTEGER,
  latency_ms INTEGER,
  detail TEXT NOT NULL DEFAULT '',
  checked_at INTEGER NOT NULL,
  PRIMARY KEY (monitor_id, country)
) WITHOUT ROWID;
