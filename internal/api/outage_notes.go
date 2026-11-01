package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"opskest/internal/auth"
	"opskest/internal/store"
)

// Outage notes are a thread per outage. Any signed-in user (viewers
// included, spec §15) may read and add notes; only a note's author or an
// admin may edit or delete it. Notes written before the thread existed have
// no author id, so only an admin changes those. Note text is never logged.

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

func userLabel(u *store.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

func canChangeNote(u *store.User, n *store.OutageNote) bool {
	return u.Role == auth.RoleAdmin || (n.AuthorID != nil && *n.AuthorID == u.ID)
}

// handleNoteOutage serves the single-note endpoint kept for older clients:
// it maps onto the thread (see store.NoteOutage).
func (s *server) handleNoteOutage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	var in struct {
		Note string `json:"note"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if len([]rune(in.Note)) > store.MaxOutageNoteLen {
		writeErr(w, http.StatusBadRequest, "note too long (max 2000)", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	by := userLabel(u)
	at := s.d.Now().UnixMilli()
	if err := s.d.Store.NoteOutageAs(r.Context(), id, in.Note, &u.ID, by, at); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not found", nil)
		case errors.Is(err, store.ErrNoteLimit):
			writeErr(w, http.StatusBadRequest, "this outage has the maximum number of notes", nil)
		default:
			writeErr(w, http.StatusInternalServerError, "note failed", nil)
		}
		return
	}
	s.d.Store.Audit(r.Context(), &u.ID, "outage.note", "outage", &id, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "noteBy": by, "noteAt": at})
}

func (s *server) handleListOutageNotes(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	limit := clampLimit(queryInt(r, "limit", 50))
	notes, err := s.d.Store.ListOutageNotes(r.Context(), id, limit, queryInt64(r, "after", 0))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	for i := range notes {
		notes[i].CanEdit = canChangeNote(u, &notes[i])
	}
	var next *int64
	if len(notes) == limit {
		last := notes[len(notes)-1].ID
		next = &last
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": notes, "nextAfter": next})
}

func decodeNoteBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in struct {
		Body string `json:"body"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return "", false
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		writeErr(w, http.StatusBadRequest, "a note needs text", map[string]string{"body": "required"})
		return "", false
	}
	if len([]rune(body)) > store.MaxOutageNoteLen {
		writeErr(w, http.StatusBadRequest, "note too long (max 2000)", map[string]string{"body": "too long"})
		return "", false
	}
	return body, true
}

func (s *server) handleAddOutageNote(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	body, ok := decodeNoteBody(w, r)
	if !ok {
		return
	}
	u, _ := auth.UserFrom(r.Context())
	n, err := s.d.Store.AddOutageNote(r.Context(), id, body, &u.ID, userLabel(u), s.d.Now().UnixMilli())
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not found", nil)
		case errors.Is(err, store.ErrNoteLimit):
			writeErr(w, http.StatusBadRequest, "this outage has the maximum number of notes", nil)
		case errors.Is(err, store.ErrInvalid):
			writeErr(w, http.StatusBadRequest, "invalid note", nil)
		default:
			writeErr(w, http.StatusInternalServerError, "add note failed", nil)
		}
		return
	}
	n.CanEdit = true
	s.d.Store.Audit(r.Context(), &u.ID, "outage.note", "outage", &id, "note "+itoa64(n.ID))
	writeJSON(w, http.StatusCreated, n)
}

// loadChangeableNote resolves {id}/{noteId} and checks the caller may change
// the note: 404 for an unknown one, 403 for someone else's note.
func (s *server) loadChangeableNote(w http.ResponseWriter, r *http.Request) (outageID, noteID int64, u *store.User, ok bool) {
	id, err1 := pathID(r, "id")
	nid, err2 := pathID(r, "noteId")
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return 0, 0, nil, false
	}
	u, _ = auth.UserFrom(r.Context())
	cur, err := s.d.Store.GetOutageNote(r.Context(), id, nid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return 0, 0, nil, false
		}
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return 0, 0, nil, false
	}
	if !canChangeNote(u, cur) {
		writeErr(w, http.StatusForbidden, "only the author or an admin can change this note", nil)
		return 0, 0, nil, false
	}
	return id, nid, u, true
}

func (s *server) handleEditOutageNote(w http.ResponseWriter, r *http.Request) {
	id, noteID, u, ok := s.loadChangeableNote(w, r)
	if !ok {
		return
	}
	body, ok := decodeNoteBody(w, r)
	if !ok {
		return
	}
	n, err := s.d.Store.EditOutageNote(r.Context(), id, noteID, body, s.d.Now().UnixMilli())
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not found", nil)
		case errors.Is(err, store.ErrInvalid):
			writeErr(w, http.StatusBadRequest, "invalid note", nil)
		default:
			writeErr(w, http.StatusInternalServerError, "edit note failed", nil)
		}
		return
	}
	n.CanEdit = true
	s.d.Store.Audit(r.Context(), &u.ID, "outage.note.edit", "outage", &id, "note "+itoa64(noteID))
	writeJSON(w, http.StatusOK, n)
}

func (s *server) handleDeleteOutageNote(w http.ResponseWriter, r *http.Request) {
	id, noteID, u, ok := s.loadChangeableNote(w, r)
	if !ok {
		return
	}
	if err := s.d.Store.DeleteOutageNote(r.Context(), id, noteID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete note failed", nil)
		return
	}
	s.d.Store.Audit(r.Context(), &u.ID, "outage.note.delete", "outage", &id, "note "+itoa64(noteID))
	w.WriteHeader(http.StatusNoContent)
}
