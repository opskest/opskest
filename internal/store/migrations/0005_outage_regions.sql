-- Multi-region confirmation: when an outage opens, other regions are asked
-- (via Globalping) whether they can reach the target. The verdict (JSON)
-- is attached to the outage so an operator can tell "down for everyone"
-- from "only opskest's network can't reach it".
ALTER TABLE outages ADD COLUMN regions TEXT;
