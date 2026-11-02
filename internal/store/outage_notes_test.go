package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMigration0032MovesOldNote upgrades a database at 0031 that holds an
// outage with a single note (and one without): the note becomes the first row
// of the thread, with its author name and time, and nothing else gets a row.
func TestMigration0032MovesOldNote(t *testing.T) {
	db, err := sql.Open("sqlite", writerDSN(filepath.Join(t.TempDir(), "old.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(migrationsTableDDL); err != nil {
		t.Fatal(err)
	}
	names, err := migrationNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if n >= "0032" {
			break
		}
		b, err := migrationsFS.ReadFile("migrations/" + n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(b)); err != nil {
			t.Fatalf("%s: %v", n, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES (?, 1)`, n); err != nil {
			t.Fatal(err)
		}
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", strings.Fields(q)[0], err)
		}
	}
	mustExec(`INSERT INTO monitors(id, name, type, interval_s, retry_interval_s, retries, timeout_ms, created_at, updated_at) VALUES (1, 'm', 'tcp', 60, 20, 0, 5000, 1, 1)`)
	mustExec(`INSERT INTO outages(id, monitor_id, started_at, ended_at, note, note_by, note_at) VALUES (1, 1, 1000, 2000, 'root cause: failover', 'Alex', 3000)`)
	mustExec(`INSERT INTO outages(id, monitor_id, started_at, ended_at, note) VALUES (2, 1, 5000, 6000, '   ')`)
	mustExec(`INSERT INTO outages(id, monitor_id, started_at, ended_at) VALUES (3, 1, 7000, 8000)`)

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var n int
	db.QueryRow(`SELECT count(*) FROM outage_notes`).Scan(&n)
	if n != 1 {
		t.Fatalf("outage_notes rows = %d, want 1 (blank and missing notes get none)", n)
	}
	var outage, createdAt int64
	var body, author string
	var authorID, edited sql.NullInt64
	if err := db.QueryRow(`SELECT outage_id, body, author_id, author_name, created_at, edited_at FROM outage_notes`).
		Scan(&outage, &body, &authorID, &author, &createdAt, &edited); err != nil {
		t.Fatal(err)
	}
	if outage != 1 || body != "root cause: failover" || author != "Alex" || createdAt != 3000 || authorID.Valid || edited.Valid {
		t.Fatalf("migrated note = %d %q %q %d %v %v", outage, body, author, createdAt, authorID, edited)
	}

	// Deleting the outage removes its thread.
	mustExec(`PRAGMA foreign_keys=ON`)
	mustExec(`DELETE FROM outages WHERE id=1`)
	db.QueryRow(`SELECT count(*) FROM outage_notes`).Scan(&n)
	if n != 0 {
		t.Fatalf("notes after deleting the outage = %d, want 0", n)
	}
}

func TestOutageNoteThread(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()
	m := createTestMonitor(t, s, "Checkout")
	w.Do(OpOpenOutage(Outage{ID: 1, MonitorID: m.ID, StartedAt: 1000, FirstReason: "HTTP 503", ErrClass: "http_status"}))
	w.Do(OpOpenOutage(Outage{ID: 2, MonitorID: m.ID, StartedAt: 2000, FirstReason: "dns", ErrClass: "dns"}))
	w.Flush()

	author, err := s.CreateUser(ctx, "alex@example.com", "Alex", RoleViewer, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	uid := author.ID
	a, err := s.AddOutageNote(ctx, 1, "  first note ", &uid, "Alex", 100)
	if err != nil || a.Body != "first note" {
		t.Fatalf("add: %+v %v", a, err)
	}
	b, err := s.AddOutageNote(ctx, 1, "second note", nil, "Sam", 200)
	if err != nil {
		t.Fatal(err)
	}

	notes, err := s.ListOutageNotes(ctx, 1, 50, 0)
	if err != nil || len(notes) != 2 || notes[0].ID != a.ID || notes[1].ID != b.ID {
		t.Fatalf("list oldest first: %+v %v", notes, err)
	}
	if after, _ := s.ListOutageNotes(ctx, 1, 50, a.ID); len(after) != 1 || after[0].ID != b.ID {
		t.Fatalf("list after: %+v", after)
	}
	if _, err := s.ListOutageNotes(ctx, 99, 50, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("list unknown outage: %v", err)
	}

	o := listOne(t, s, 1)
	if o.NoteCount != 2 || o.Note == nil || *o.Note != "second note" || o.NoteBy == nil || *o.NoteBy != "Sam" {
		t.Fatalf("summary after adds = %+v", o)
	}

	// Edit the newest: the summary follows and edited_at is set.
	e, err := s.EditOutageNote(ctx, 1, b.ID, "second note, revised", 300)
	if err != nil || e.EditedAt == nil || *e.EditedAt != 300 || e.CreatedAt != 200 {
		t.Fatalf("edit: %+v %v", e, err)
	}
	if o := listOne(t, s, 1); o.Note == nil || *o.Note != "second note, revised" || o.NoteAt == nil || *o.NoteAt != 300 {
		t.Fatalf("summary after edit = %+v", o)
	}
	// A note is only reachable through its own outage.
	if _, err := s.EditOutageNote(ctx, 2, a.ID, "x", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("edit via wrong outage: %v", err)
	}
	if _, err := s.EditOutageNote(ctx, 1, a.ID, "   ", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("edit to blank: %v", err)
	}
	if _, err := s.AddOutageNote(ctx, 1, strings.Repeat("x", MaxOutageNoteLen+1), nil, "x", 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("add too long: %v", err)
	}
	if _, err := s.AddOutageNote(ctx, 42, "x", nil, "x", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("add to unknown outage: %v", err)
	}

	// Deleting the newest moves the summary back to the previous note.
	if err := s.DeleteOutageNote(ctx, 1, b.ID); err != nil {
		t.Fatal(err)
	}
	if o := listOne(t, s, 1); o.NoteCount != 1 || o.Note == nil || *o.Note != "first note" {
		t.Fatalf("summary after delete = %+v", o)
	}
	if err := s.DeleteOutageNote(ctx, 1, a.ID); err != nil {
		t.Fatal(err)
	}
	if o := listOne(t, s, 1); o.NoteCount != 0 || o.Note != nil || o.NoteBy != nil || o.NoteAt != nil {
		t.Fatalf("summary after clearing = %+v", o)
	}
	if err := s.DeleteOutageNote(ctx, 1, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestOutageNoteLimitAndSearch(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()
	m := createTestMonitor(t, s, "Checkout")
	w.Do(OpOpenOutage(Outage{ID: 1, MonitorID: m.ID, StartedAt: 1000, FirstReason: "x", ErrClass: "other"}))
	w.Flush()

	for i := 0; i < MaxOutageNotes; i++ {
		if _, err := s.AddOutageNote(ctx, 1, "note", nil, "ops", int64(i)); err != nil {
			t.Fatalf("note %d: %v", i, err)
		}
	}
	if _, err := s.AddOutageNote(ctx, 1, "one too many", nil, "ops", 1); !errors.Is(err, ErrNoteLimit) {
		t.Fatalf("over the limit: %v", err)
	}
	// Even asking for more than the cap returns at most 200.
	got, err := s.ListOutageNotes(ctx, 1, 1000, 0)
	if err != nil || len(got) != MaxOutageNotes {
		t.Fatalf("list = %d %v", len(got), err)
	}

	// The search finds an outage by an older note, not just the newest one.
	if _, err := s.EditOutageNote(ctx, 1, got[0].ID, "needle in the first note", 5); err != nil {
		t.Fatal(err)
	}
	found, err := s.ListOutages(ctx, OutageFilter{Query: "needle"})
	if err != nil || len(found) != 1 || found[0].NoteCount != MaxOutageNotes {
		t.Fatalf("search by older note = %+v %v", found, err)
	}
}

// The single-note interface older clients use keeps working on the thread.
func TestLegacyNoteOutageMapsToThread(t *testing.T) {
	s := newTestStore(t)
	w := testWriter(t, s)
	ctx := context.Background()
	m := createTestMonitor(t, s, "Checkout")
	w.Do(OpOpenOutage(Outage{ID: 1, MonitorID: m.ID, StartedAt: 1000, FirstReason: "x", ErrClass: "other"}))
	w.Flush()

	if err := s.NoteOutage(ctx, 1, "first", "ops", 10); err != nil {
		t.Fatal(err)
	}
	if err := s.NoteOutage(ctx, 1, "first, reworded", "ops", 20); err != nil { // same author: edits in place
		t.Fatal(err)
	}
	if o := listOne(t, s, 1); o.NoteCount != 1 || *o.Note != "first, reworded" {
		t.Fatalf("after rewording = %+v", o)
	}
	if err := s.NoteOutage(ctx, 1, "from someone else", "sam", 30); err != nil {
		t.Fatal(err)
	}
	if o := listOne(t, s, 1); o.NoteCount != 2 || *o.Note != "from someone else" {
		t.Fatalf("after another author = %+v", o)
	}
	if err := s.NoteOutage(ctx, 1, "", "sam", 40); err != nil { // clears the newest
		t.Fatal(err)
	}
	if o := listOne(t, s, 1); o.NoteCount != 1 || *o.Note != "first, reworded" {
		t.Fatalf("after clearing = %+v", o)
	}
	if err := s.NoteOutage(ctx, 99, "x", "ops", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown outage: %v", err)
	}
}

func TestEditIncidentUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	inc := &Incident{Title: "t", Status: "investigating", Impact: "minor", Severity: "minor"}
	if err := s.CreateIncident(ctx, inc, "first body"); err != nil {
		t.Fatal(err)
	}
	pub := &IncidentUpdate{IncidentID: inc.ID, Status: "identified", Body: "found it"}
	if err := s.AddIncidentUpdate(ctx, pub); err != nil {
		t.Fatal(err)
	}
	note := &IncidentUpdate{IncidentID: inc.ID, Status: "identified", Body: "team only", Internal: true}
	if err := s.AddIncidentUpdate(ctx, note); err != nil {
		t.Fatal(err)
	}

	got, err := s.EditIncidentUpdate(ctx, inc.ID, pub.ID, "found it: a bad deploy", nil)
	if err != nil || got.Body != "found it: a bad deploy" || got.EditedAt == nil || got.Status != "identified" || got.Internal {
		t.Fatalf("edit public: %+v %v", got, err)
	}
	// The incident's status is untouched by editing.
	if cur, _ := s.GetIncident(ctx, inc.ID); cur.Status != "identified" {
		t.Fatalf("status = %q", cur.Status)
	}
	yes, no := true, false
	if got, err = s.EditIncidentUpdate(ctx, inc.ID, pub.ID, "now internal", &yes); err != nil || !got.Internal {
		t.Fatalf("make internal: %+v %v", got, err)
	}
	if got, err = s.EditIncidentUpdate(ctx, inc.ID, note.ID, "team only", &no); err != nil || got.Internal {
		t.Fatalf("make public: %+v %v", got, err)
	}
	if _, err := s.EditIncidentUpdate(ctx, inc.ID, pub.ID, "  ", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty team note: %v", err)
	}
	if _, err := s.EditIncidentUpdate(ctx, inc.ID+1, pub.ID, "x", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong incident: %v", err)
	}
}

func listOne(t *testing.T, s *Store, id int64) Outage {
	t.Helper()
	list, err := s.ListOutages(context.Background(), OutageFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range list {
		if o.ID == id {
			return o
		}
	}
	t.Fatalf("outage %d not listed", id)
	return Outage{}
}
