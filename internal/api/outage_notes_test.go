package api

import (
	"net/http"
	"strings"
	"testing"
)

type noteJSON struct {
	ID         int64  `json:"id"`
	OutageID   int64  `json:"outageId"`
	Body       string `json:"body"`
	AuthorName string `json:"authorName"`
	EditedAt   *int64 `json:"editedAt"`
	CanEdit    bool   `json:"canEdit"`
}

func openOutage(t *testing.T, env *testEnv) int64 {
	t.Helper()
	out := downMonitor(t, env, "notes monitor", "https://example.com/health", "HTTP 503")
	sum, ok := env.engine.Summary(out.ID)
	if !ok || sum.OutageID == nil {
		t.Fatalf("no outage: %+v", sum)
	}
	return *sum.OutageID
}

func addNote(t *testing.T, env *testEnv, outage int64, body, as string) noteJSON {
	t.Helper()
	r := env.do(http.MethodPost, "/api/outages/"+itoa(outage)+"/notes", map[string]any{"body": body}, as)
	if r.status != http.StatusCreated {
		t.Fatalf("add note as %s: %d %s", as, r.status, r.body)
	}
	var n noteJSON
	r.json(t, &n)
	return n
}

func listNotes(t *testing.T, env *testEnv, outage int64, as string) []noteJSON {
	t.Helper()
	r := env.do(http.MethodGet, "/api/outages/"+itoa(outage)+"/notes", nil, as)
	if r.status != http.StatusOK {
		t.Fatalf("list notes: %d %s", r.status, r.body)
	}
	var body struct {
		Notes []noteJSON `json:"notes"`
	}
	r.json(t, &body)
	return body.Notes
}

func TestOutageNotesThreadAndPermissions(t *testing.T) {
	env := newTestEnv(t)
	oid := openOutage(t, env)
	path := "/api/outages/" + itoa(oid) + "/notes"

	if r := env.do(http.MethodGet, path, nil, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d", r.status)
	}

	// Several notes, from both roles: viewers may annotate (spec 15).
	a := addNote(t, env, oid, "  paging the DB team  ", "admin")
	v := addNote(t, env, oid, "failover started", "viewer")
	addNote(t, env, oid, "failover done", "admin")
	if a.Body != "paging the DB team" {
		t.Fatalf("body not trimmed: %q", a.Body)
	}
	notes := listNotes(t, env, oid, "viewer")
	if len(notes) != 3 || notes[0].ID != a.ID || notes[1].ID != v.ID {
		t.Fatalf("thread = %+v, want 3 oldest first", notes)
	}
	// canEdit is per requester: the viewer sees only their own note as editable.
	if notes[0].CanEdit || !notes[1].CanEdit || notes[2].CanEdit {
		t.Fatalf("viewer canEdit = %v %v %v", notes[0].CanEdit, notes[1].CanEdit, notes[2].CanEdit)
	}
	for _, n := range listNotes(t, env, oid, "admin") {
		if !n.CanEdit {
			t.Fatalf("admin cannot edit note %d", n.ID)
		}
	}

	// The outage list carries the count and the newest note as its summary.
	var list struct {
		Outages []struct {
			ID        int64   `json:"id"`
			Note      *string `json:"note"`
			NoteCount int     `json:"noteCount"`
		} `json:"outages"`
	}
	env.do(http.MethodGet, "/api/outages", nil, "viewer").json(t, &list)
	if len(list.Outages) != 1 || list.Outages[0].NoteCount != 3 || list.Outages[0].Note == nil || *list.Outages[0].Note != "failover done" {
		t.Fatalf("outage list = %+v", list.Outages)
	}

	// Edit: the author may, an admin may, a viewer may not touch another's.
	vpath := path + "/" + itoa(v.ID)
	if r := env.do(http.MethodPut, vpath, map[string]any{"body": "hacked"}, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous edit: %d", r.status)
	}
	apath := path + "/" + itoa(a.ID)
	if r := env.do(http.MethodPut, apath, map[string]any{"body": "hacked"}, "viewer"); r.status != http.StatusForbidden {
		t.Fatalf("viewer edits admin's note: %d, want 403", r.status)
	}
	if r := env.do(http.MethodDelete, apath, nil, "viewer"); r.status != http.StatusForbidden {
		t.Fatalf("viewer deletes admin's note: %d, want 403", r.status)
	}
	r := env.do(http.MethodPut, vpath, map[string]any{"body": "failover started at 10:02"}, "viewer")
	if r.status != http.StatusOK {
		t.Fatalf("author edit: %d %s", r.status, r.body)
	}
	var edited noteJSON
	r.json(t, &edited)
	if edited.Body != "failover started at 10:02" || edited.EditedAt == nil {
		t.Fatalf("edited = %+v", edited)
	}
	if r := env.do(http.MethodPut, vpath, map[string]any{"body": "[admin] tidied"}, "admin"); r.status != http.StatusOK {
		t.Fatalf("admin edits viewer's note: %d %s", r.status, r.body)
	}
	for _, body := range []string{"", "   ", strings.Repeat("x", 2001)} {
		if r := env.do(http.MethodPut, vpath, map[string]any{"body": body}, "viewer"); r.status != http.StatusBadRequest {
			t.Fatalf("edit to %d chars: %d, want 400", len(body), r.status)
		}
		if r := env.do(http.MethodPost, path, map[string]any{"body": body}, "viewer"); r.status != http.StatusBadRequest {
			t.Fatalf("add %d chars: %d, want 400", len(body), r.status)
		}
	}
	if got := listNotes(t, env, oid, "admin"); got[1].Body != "[admin] tidied" {
		t.Fatalf("thread after edits = %+v", got)
	}

	// Delete: the author, then an admin.
	if r := env.do(http.MethodDelete, vpath, nil, "viewer"); r.status != http.StatusNoContent {
		t.Fatalf("author delete: %d %s", r.status, r.body)
	}
	if r := env.do(http.MethodDelete, apath, nil, "admin"); r.status != http.StatusNoContent {
		t.Fatalf("admin delete: %d", r.status)
	}
	if got := listNotes(t, env, oid, "admin"); len(got) != 1 || got[0].Body != "failover done" {
		t.Fatalf("thread after deletes = %+v", got)
	}

	// Unknown things are 404, whoever asks.
	for _, p := range []struct{ method, path string }{
		{http.MethodGet, "/api/outages/99999/notes"},
		{http.MethodPost, "/api/outages/99999/notes"},
		{http.MethodPut, path + "/99999"},
		{http.MethodDelete, path + "/99999"},
		{http.MethodPut, "/api/outages/99999/notes/" + itoa(v.ID)},
	} {
		if r := env.do(p.method, p.path, map[string]any{"body": "x"}, "admin"); r.status != http.StatusNotFound {
			t.Errorf("%s %s: %d, want 404", p.method, p.path, r.status)
		}
	}
}

func TestOutageNotesBounded(t *testing.T) {
	env := newTestEnv(t)
	oid := openOutage(t, env)
	for i := 0; i < 200; i++ {
		addNote(t, env, oid, "note", "admin")
	}
	if r := env.do(http.MethodPost, "/api/outages/"+itoa(oid)+"/notes", map[string]any{"body": "one more"}, "admin"); r.status != http.StatusBadRequest {
		t.Fatalf("201st note: %d, want 400", r.status)
	}
	if got := listNotes(t, env, oid, "admin"); len(got) != 50 {
		t.Fatalf("default page = %d, want 50", len(got))
	}
	r := env.do(http.MethodGet, "/api/outages/"+itoa(oid)+"/notes?limit=1000", nil, "admin")
	var body struct {
		Notes     []noteJSON `json:"notes"`
		NextAfter *int64     `json:"nextAfter"`
	}
	r.json(t, &body)
	if len(body.Notes) != 200 {
		t.Fatalf("limit=1000 gave %d notes, want 200", len(body.Notes))
	}
	r = env.do(http.MethodGet, "/api/outages/"+itoa(oid)+"/notes?limit=10&after="+itoa(body.Notes[0].ID), nil, "admin")
	r.json(t, &body)
	if len(body.Notes) != 10 || body.NextAfter == nil || body.Notes[0].ID <= 0 {
		t.Fatalf("paged = %d next=%v", len(body.Notes), body.NextAfter)
	}
}

// The single-note endpoint older clients use still works and shows in the thread.
func TestLegacyOutageNoteEndpoint(t *testing.T) {
	env := newTestEnv(t)
	oid := openOutage(t, env)
	r := env.do(http.MethodPost, "/api/outages/"+itoa(oid)+"/note", map[string]any{"note": "legacy note"}, "viewer")
	if r.status != http.StatusOK {
		t.Fatalf("legacy note: %d %s", r.status, r.body)
	}
	notes := listNotes(t, env, oid, "viewer")
	if len(notes) != 1 || notes[0].Body != "legacy note" || !notes[0].CanEdit {
		t.Fatalf("thread = %+v", notes)
	}
	if r := env.do(http.MethodPost, "/api/outages/"+itoa(oid)+"/note", map[string]any{"note": ""}, "viewer"); r.status != http.StatusOK {
		t.Fatalf("legacy clear: %d", r.status)
	}
	if notes := listNotes(t, env, oid, "viewer"); len(notes) != 0 {
		t.Fatalf("thread after clear = %+v", notes)
	}
	if r := env.do(http.MethodPost, "/api/outages/99999/note", map[string]any{"note": "x"}, "viewer"); r.status != http.StatusNotFound {
		t.Fatalf("legacy unknown outage: %d", r.status)
	}
}

func TestEditIncidentUpdateAPI(t *testing.T) {
	env := newTestEnv(t)
	cr := env.do(http.MethodPost, "/api/incidents", map[string]any{"title": "Orig", "impact": "minor", "body": "first"}, "admin")
	var inc struct {
		ID int64 `json:"id"`
	}
	cr.json(t, &inc)
	var full struct {
		Updates []struct {
			ID int64 `json:"id"`
		} `json:"updates"`
	}
	env.do(http.MethodGet, "/api/incidents/"+itoa(inc.ID), nil, "admin").json(t, &full)
	upd := full.Updates[0].ID
	path := "/api/incidents/" + itoa(inc.ID) + "/updates/" + itoa(upd)

	if r := env.do(http.MethodPut, path, map[string]any{"body": "x"}, "viewer"); r.status != http.StatusForbidden {
		t.Fatalf("viewer edit: %d, want 403", r.status)
	}
	r := env.do(http.MethodPut, path, map[string]any{"body": "first, corrected"}, "admin")
	if r.status != http.StatusOK {
		t.Fatalf("admin edit: %d %s", r.status, r.body)
	}
	var out struct {
		Body     string `json:"body"`
		EditedAt *int64 `json:"editedAt"`
		Internal bool   `json:"internal"`
	}
	r.json(t, &out)
	if out.Body != "first, corrected" || out.EditedAt == nil || out.Internal {
		t.Fatalf("edited = %+v", out)
	}
	if r := env.do(http.MethodPut, path, map[string]any{"body": "now private", "internal": true}, "admin"); r.status != http.StatusOK {
		t.Fatalf("make internal: %d", r.status)
	}
	if r := env.do(http.MethodPut, path, map[string]any{"body": " "}, "admin"); r.status != http.StatusBadRequest {
		t.Fatalf("empty team note: %d, want 400", r.status)
	}
	if r := env.do(http.MethodPut, "/api/incidents/"+itoa(inc.ID)+"/updates/99999", map[string]any{"body": "x"}, "admin"); r.status != http.StatusNotFound {
		t.Fatalf("unknown update: %d, want 404", r.status)
	}
	if r := env.do(http.MethodPut, "/api/incidents/"+itoa(inc.ID), map[string]any{"title": "  ", "impact": "minor"}, "admin"); r.status != http.StatusBadRequest {
		t.Fatalf("blank title: %d, want 400", r.status)
	}
	if r := env.do(http.MethodPut, "/api/incidents/"+itoa(inc.ID), map[string]any{"title": "Renamed", "impact": "major"}, "admin"); r.status != http.StatusOK {
		t.Fatalf("rename: %d", r.status)
	}
}
