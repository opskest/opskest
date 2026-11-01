package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// MaxOutageNoteLen is the longest outage note body, in characters.
const MaxOutageNoteLen = 2000

// MaxOutageNotes is how many notes one outage's thread may hold, so the
// thread (and the endpoint that returns it) stays bounded.
const MaxOutageNotes = 200

// ErrNoteLimit is returned by AddOutageNote when the thread is full.
var ErrNoteLimit = errors.New("outage has the maximum number of notes")

// OutageNote is one entry in an outage's note thread.
type OutageNote struct {
	ID         int64  `json:"id"`
	OutageID   int64  `json:"outageId"`
	Body       string `json:"body"`
	AuthorID   *int64 `json:"authorId"`   // nil for notes from before the thread existed
	AuthorName string `json:"authorName"` // the name at the time of writing
	CreatedAt  int64  `json:"createdAt"`
	EditedAt   *int64 `json:"editedAt"`
	// CanEdit is filled in by the API for the requesting user.
	CanEdit bool `json:"canEdit"`
}

const outageNoteColumns = `id, outage_id, body, author_id, author_name, created_at, edited_at`

func scanOutageNote(sc scanner) (OutageNote, error) {
	var n OutageNote
	var author, edited sql.NullInt64
	if err := sc.Scan(&n.ID, &n.OutageID, &n.Body, &author, &n.AuthorName, &n.CreatedAt, &edited); err != nil {
		return n, err
	}
	n.AuthorID = scanNullInt64(author)
	n.EditedAt = scanNullInt64(edited)
	return n, nil
}

// ListOutageNotes returns outage outageID's notes, oldest first, at most
// limit (clamped to 1..200, default 50) with id greater than afterID.
// ErrNotFound if the outage doesn't exist.
func (s *Store) ListOutageNotes(ctx context.Context, outageID int64, limit int, afterID int64) ([]OutageNote, error) {
	var one int
	if err := s.R.QueryRowContext(ctx, `SELECT 1 FROM outages WHERE id=?`, outageID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: outage exists: %w", err)
	}
	rows, err := s.R.QueryContext(ctx, `SELECT `+outageNoteColumns+` FROM outage_notes
		WHERE outage_id=? AND id>? ORDER BY id ASC LIMIT ?`, outageID, afterID, clampLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("store: list outage notes: %w", err)
	}
	defer rows.Close()
	out := []OutageNote{}
	for rows.Next() {
		n, err := scanOutageNote(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan outage note: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// GetOutageNote returns one note of outage outageID, or ErrNotFound.
func (s *Store) GetOutageNote(ctx context.Context, outageID, noteID int64) (*OutageNote, error) {
	n, err := scanOutageNote(s.R.QueryRowContext(ctx, `SELECT `+outageNoteColumns+` FROM outage_notes WHERE id=? AND outage_id=?`, noteID, outageID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get outage note: %w", err)
	}
	return &n, nil
}

// refreshOutageNoteSummary points outages.note / note_by / note_at at the
// newest note of the thread (or clears them when it is empty), so the
// search, the "needs a note" filter and old clients keep working.
func refreshOutageNoteSummary(ctx context.Context, tx *sql.Tx, outageID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE outages SET
		note    = (SELECT body FROM outage_notes WHERE outage_id=outages.id ORDER BY id DESC LIMIT 1),
		note_by = (SELECT NULLIF(author_name, '') FROM outage_notes WHERE outage_id=outages.id ORDER BY id DESC LIMIT 1),
		note_at = (SELECT COALESCE(edited_at, created_at) FROM outage_notes WHERE outage_id=outages.id ORDER BY id DESC LIMIT 1)
		WHERE id=?`, outageID)
	if err != nil {
		return fmt.Errorf("store: refresh outage note summary: %w", err)
	}
	return nil
}

// AddOutageNote appends a note to outage outageID's thread. ErrNotFound if
// the outage doesn't exist, ErrInvalid for an empty or over-long body,
// ErrNoteLimit when the thread already holds MaxOutageNotes. It runs on the
// writer connection (not queued), like the other user-triggered note edits.
func (s *Store) AddOutageNote(ctx context.Context, outageID int64, body string, authorID *int64, authorName string, at int64) (*OutageNote, error) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > MaxOutageNoteLen {
		return nil, ErrInvalid
	}
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM outage_notes WHERE outage_id=o.id) FROM outages o WHERE o.id=?`, outageID).Scan(&count); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: count outage notes: %w", err)
	}
	if count >= MaxOutageNotes {
		return nil, ErrNoteLimit
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO outage_notes(outage_id, body, author_id, author_name, created_at) VALUES (?,?,?,?,?)`,
		outageID, body, int64OrNil(authorID), authorName, at)
	if err != nil {
		return nil, fmt.Errorf("store: add outage note: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("store: last insert id: %w", err)
	}
	if err := refreshOutageNoteSummary(ctx, tx, outageID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return &OutageNote{ID: id, OutageID: outageID, Body: body, AuthorID: authorID, AuthorName: authorName, CreatedAt: at}, nil
}

// EditOutageNote replaces a note's body and stamps edited_at. ErrNotFound
// if the note isn't in that outage's thread, ErrInvalid for a bad body.
func (s *Store) EditOutageNote(ctx context.Context, outageID, noteID int64, body string, at int64) (*OutageNote, error) {
	body = strings.TrimSpace(body)
	if body == "" || len([]rune(body)) > MaxOutageNoteLen {
		return nil, ErrInvalid
	}
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `UPDATE outage_notes SET body=?, edited_at=? WHERE id=? AND outage_id=?`, body, at, noteID, outageID)
	if err != nil {
		return nil, fmt.Errorf("store: edit outage note: %w", err)
	}
	if err := checkAffected(res); err != nil {
		return nil, err
	}
	if err := refreshOutageNoteSummary(ctx, tx, outageID); err != nil {
		return nil, err
	}
	n, err := scanOutageNote(tx.QueryRowContext(ctx, `SELECT `+outageNoteColumns+` FROM outage_notes WHERE id=?`, noteID))
	if err != nil {
		return nil, fmt.Errorf("store: reread outage note: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit: %w", err)
	}
	return &n, nil
}

// DeleteOutageNote removes a note from its thread. ErrNotFound if it isn't
// there.
func (s *Store) DeleteOutageNote(ctx context.Context, outageID, noteID int64) error {
	tx, err := s.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `DELETE FROM outage_notes WHERE id=? AND outage_id=?`, noteID, outageID)
	if err != nil {
		return fmt.Errorf("store: delete outage note: %w", err)
	}
	if err := checkAffected(res); err != nil {
		return err
	}
	if err := refreshOutageNoteSummary(ctx, tx, outageID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// NoteOutage is the single-note interface kept for older clients: it sets
// "the" note of an outage. Non-blank text edits the newest note when the same
// person wrote it, otherwise adds a note; blank text removes the newest note.
// ErrNotFound if the outage doesn't exist.
func (s *Store) NoteOutage(ctx context.Context, id int64, note, by string, at int64) error {
	return s.NoteOutageAs(ctx, id, note, nil, by, at)
}

// NoteOutageAs is NoteOutage with the author's user id recorded on a new note.
func (s *Store) NoteOutageAs(ctx context.Context, id int64, note string, authorID *int64, by string, at int64) error {
	note = strings.TrimSpace(note)
	var latest sql.NullInt64
	var latestBy sql.NullString
	err := s.W.QueryRowContext(ctx, `SELECT id, author_name FROM outage_notes WHERE outage_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&latest, &latestBy)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: latest outage note: %w", err)
	}
	switch {
	case note == "" && latest.Valid:
		return s.DeleteOutageNote(ctx, id, latest.Int64)
	case note == "":
		var one int
		if err := s.W.QueryRowContext(ctx, `SELECT 1 FROM outages WHERE id=?`, id).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("store: outage exists: %w", err)
		}
		return nil
	case latest.Valid && latestBy.String == by:
		_, err := s.EditOutageNote(ctx, id, latest.Int64, note, at)
		return err
	default:
		_, err := s.AddOutageNote(ctx, id, note, authorID, by, at)
		return err
	}
}
