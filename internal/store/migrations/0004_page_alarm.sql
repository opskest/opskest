-- Status-page alarm settings live on the page, so every browser showing it
-- (a wallboard TV, not just the admin who edited it) plays the same sounds.
ALTER TABLE status_pages ADD COLUMN alarm_muted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE status_pages ADD COLUMN alarm_down TEXT NOT NULL DEFAULT '';
ALTER TABLE status_pages ADD COLUMN alarm_degraded TEXT NOT NULL DEFAULT '';
