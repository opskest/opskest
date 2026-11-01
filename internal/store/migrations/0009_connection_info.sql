-- Connection facts per check: server IP, HTTP version, TLS version and
-- whether the TLS session was resumed — they explain handshake cost.
ALTER TABLE heartbeats ADD COLUMN conn TEXT;
