-- The personal API token feature was removed before release. Databases that
-- already ran the old 0020 migration still have its table; drop it. On a fresh
-- database there is nothing to drop.
DROP TABLE IF EXISTS api_tokens;
