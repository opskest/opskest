-- Outage notes become a thread: any number of notes per outage, each with its
-- author, a created time and, once changed, an edited time.
--
-- outages.note / note_by / note_at stay as a summary of the newest note (the
-- Outages tab searches and filters on them, the API keeps the old single-note
-- field), kept in step by the store whenever a note is added, edited or removed.
CREATE TABLE outage_notes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    outage_id   INTEGER NOT NULL REFERENCES outages(id) ON DELETE CASCADE,
    body        TEXT    NOT NULL,
    -- author_id is NULL for notes written before this table existed (the old
    -- column only kept a name) and when the author's account is deleted.
    author_id   INTEGER REFERENCES users(id) ON DELETE SET NULL,
    author_name TEXT    NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    edited_at   INTEGER
);
CREATE INDEX idx_outage_notes_outage ON outage_notes(outage_id, id);

-- Each existing single note becomes the first row of its outage's thread.
INSERT INTO outage_notes(outage_id, body, author_id, author_name, created_at)
SELECT id, note, NULL, COALESCE(note_by, ''), COALESCE(note_at, ended_at, started_at)
FROM outages
WHERE note IS NOT NULL AND trim(note) <> '';

-- Incident timeline entries (status updates and team notes) can be edited.
ALTER TABLE incident_updates ADD COLUMN edited_at INTEGER;
