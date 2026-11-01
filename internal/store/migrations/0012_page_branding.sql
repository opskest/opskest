-- Customer-facing status page branding (spec 2026-10-12 §A.1) and a
-- one-line description per component.
ALTER TABLE status_pages ADD COLUMN logo_url TEXT NOT NULL DEFAULT '';
ALTER TABLE status_pages ADD COLUMN brand_color TEXT NOT NULL DEFAULT '';
ALTER TABLE status_pages ADD COLUMN website_url TEXT NOT NULL DEFAULT '';
ALTER TABLE status_pages ADD COLUMN support_url TEXT NOT NULL DEFAULT '';
ALTER TABLE status_pages ADD COLUMN footer_text TEXT NOT NULL DEFAULT '';
ALTER TABLE status_pages ADD COLUMN show_response_time INTEGER NOT NULL DEFAULT 0;
ALTER TABLE status_pages ADD COLUMN history_days INTEGER NOT NULL DEFAULT 90;
ALTER TABLE status_page_items ADD COLUMN description TEXT NOT NULL DEFAULT '';
