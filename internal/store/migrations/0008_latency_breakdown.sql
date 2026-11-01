-- Latency breakdown: server wait after the request was sent, the app's own
-- processing time when it reports one (Server-Timing, X-Response-Time, …),
-- and who answered at the edge (CDN / load balancer / web server).
ALTER TABLE heartbeats ADD COLUMN wait_ms INTEGER;
ALTER TABLE heartbeats ADD COLUMN app_ms INTEGER;
ALTER TABLE heartbeats ADD COLUMN edge TEXT;
