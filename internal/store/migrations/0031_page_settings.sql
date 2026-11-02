-- Status page presentation and content options (theme, markdown copy, pinned
-- announcement, show/hide switches, refresh interval, canonical address).
-- One JSON document: new options never need another migration, and a page
-- whose document is '{}' simply gets the defaults in store.DefaultPageSettings.
ALTER TABLE status_pages ADD COLUMN settings TEXT NOT NULL DEFAULT '{}';
