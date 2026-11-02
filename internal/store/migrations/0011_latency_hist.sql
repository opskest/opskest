-- Sparse latency histogram per rollup bucket (internal/latency encoding),
-- merged in the writer's upsert by opskest_hist_merge. NULL on rows written
-- before this migration: their percentiles read as "no data".
ALTER TABLE rollup_hourly ADD COLUMN lat_hist BLOB;
ALTER TABLE rollup_daily ADD COLUMN lat_hist BLOB;
