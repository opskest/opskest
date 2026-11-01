-- Internal incident management: severity, an incident owner (commander),
-- a postmortem, and team-only timeline notes that never reach status pages.
ALTER TABLE incidents ADD COLUMN severity TEXT NOT NULL DEFAULT 'minor';
ALTER TABLE incidents ADD COLUMN commander_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE incidents ADD COLUMN postmortem TEXT NOT NULL DEFAULT '';
ALTER TABLE incident_updates ADD COLUMN internal INTEGER NOT NULL DEFAULT 0;
