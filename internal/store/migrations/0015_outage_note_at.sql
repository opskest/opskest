-- When an outage's note was last written, so the Outages tab can show
-- "noted by X on <date>". NULL for outages noted before this column existed
-- and for outages with no note.
ALTER TABLE outages ADD COLUMN note_at INTEGER;
