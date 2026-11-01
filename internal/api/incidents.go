package api

import (
	"errors"
	"net/http"
	"strings"

	"opskest/internal/auth"
	"opskest/internal/store"
)

func (s *server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	activeOnly := r.URL.Query().Get("active") == "true"
	limit := clampLimit(queryInt(r, "limit", 50))
	before := queryInt64(r, "before", 0)

	incidents, err := s.d.Store.ListIncidents(r.Context(), activeOnly, limit, before)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"incidents": incidents})
}

func (s *server) handleCreateIncident(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title       string  `json:"title"`
		Impact      string  `json:"impact"`
		Body        string  `json:"body"`
		Severity    string  `json:"severity"`
		CommanderID *int64  `json:"commanderId"`
		MonitorIDs  []int64 `json:"monitorIds"`
		PageIDs     []int64 `json:"pageIds"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if in.Title == "" {
		writeErr(w, http.StatusBadRequest, "title required", nil)
		return
	}
	if in.Severity == "" {
		in.Severity = "minor"
	}
	if !store.ValidSeverity(in.Severity) {
		writeErr(w, http.StatusBadRequest, "severity must be critical, major or minor", map[string]string{"severity": "invalid"})
		return
	}
	if in.CommanderID != nil && *in.CommanderID <= 0 {
		in.CommanderID = nil
	}
	if len(s.d.Engine.VendorIDs(in.MonitorIDs)) > 0 {
		writeErr(w, http.StatusBadRequest, "3rd-party feeds are alert-only and cannot be linked to an incident", map[string]string{"monitorIds": "3rd-party feeds cannot be affected monitors"})
		return
	}

	u, _ := auth.UserFrom(r.Context())
	now := s.d.Now().UnixMilli()

	incident := &store.Incident{
		Title:       in.Title,
		Status:      "investigating",
		Impact:      in.Impact,
		CreatedBy:   &u.ID,
		CreatedAt:   now,
		UpdatedAt:   now,
		Severity:    in.Severity,
		CommanderID: in.CommanderID,
		MonitorIDs:  in.MonitorIDs,
		PageIDs:     in.PageIDs,
	}
	if err := s.d.Store.CreateIncident(r.Context(), incident, in.Body); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			writeErr(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "create failed", nil)
		return
	}
	s.d.Store.Audit(r.Context(), &u.ID, "create", "incident", &incident.ID, incident.Title)
	s.notifyIncident(r.Context(), incident.ID, in.Body, s.d.Now())
	writeJSON(w, http.StatusCreated, incident)
}

func (s *server) handleGetIncident(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	incident, err := s.d.Store.GetIncident(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, incident)
}

func (s *server) handleUpdateIncident(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	var in struct {
		Title       string  `json:"title"`
		Impact      string  `json:"impact"`
		Severity    string  `json:"severity"`
		CommanderID *int64  `json:"commanderId"`
		MonitorIDs  []int64 `json:"monitorIds"`
		PageIDs     []int64 `json:"pageIds"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		writeErr(w, http.StatusBadRequest, "title required", nil)
		return
	}
	if len([]rune(in.Title)) > 200 {
		writeErr(w, http.StatusBadRequest, "title too long (max 200)", nil)
		return
	}
	if in.Severity == "" {
		if cur, err := s.d.Store.GetIncident(r.Context(), id); err == nil {
			in.Severity = cur.Severity
		} else {
			in.Severity = "minor"
		}
	}
	if !store.ValidSeverity(in.Severity) {
		writeErr(w, http.StatusBadRequest, "severity must be critical, major or minor", map[string]string{"severity": "invalid"})
		return
	}
	if in.CommanderID != nil && *in.CommanderID <= 0 {
		in.CommanderID = nil
	}
	if len(s.d.Engine.VendorIDs(in.MonitorIDs)) > 0 {
		writeErr(w, http.StatusBadRequest, "3rd-party feeds are alert-only and cannot be linked to an incident", map[string]string{"monitorIds": "3rd-party feeds cannot be affected monitors"})
		return
	}

	now := s.d.Now().UnixMilli()
	incident := &store.Incident{
		ID:          id,
		Title:       in.Title,
		Impact:      in.Impact,
		Severity:    in.Severity,
		CommanderID: in.CommanderID,
		UpdatedAt:   now,
		MonitorIDs:  in.MonitorIDs,
		PageIDs:     in.PageIDs,
	}
	if err := s.d.Store.UpdateIncident(r.Context(), incident); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		if errors.Is(err, store.ErrInvalid) {
			writeErr(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "update failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update", "incident", &id, in.Title)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleDeleteIncident(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if err := s.d.Store.DeleteIncident(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "delete", "incident", &id, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleAddIncidentUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	var in struct {
		Status   string `json:"status"`
		Body     string `json:"body"`
		Internal bool   `json:"internal"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if len([]rune(in.Body)) > 5000 {
		writeErr(w, http.StatusBadRequest, "body too long (max 5000)", nil)
		return
	}

	u, _ := auth.UserFrom(r.Context())
	now := s.d.Now().UnixMilli()
	upd := &store.IncidentUpdate{
		IncidentID: id,
		Status:     in.Status,
		Body:       in.Body,
		Internal:   in.Internal,
		CreatedBy:  &u.ID,
		CreatedAt:  now,
	}
	if in.Internal && upd.Body == "" {
		writeErr(w, http.StatusBadRequest, "a note needs text", map[string]string{"body": "required"})
		return
	}
	if err := s.d.Store.AddIncidentUpdate(r.Context(), upd); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		if errors.Is(err, store.ErrInvalid) {
			writeErr(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "add update failed", nil)
		return
	}
	if !upd.Internal {
		s.notifyIncident(r.Context(), id, upd.Body, s.d.Now())
	}
	writeJSON(w, http.StatusCreated, upd)
}

// handleEditIncidentUpdate serves PUT /api/incidents/{id}/updates/{updateId}:
// admins change an entry's text and whether it is a team-only note. The
// entry's status never changes and subscribers are not re-notified.
func (s *server) handleEditIncidentUpdate(w http.ResponseWriter, r *http.Request) {
	id, err1 := pathID(r, "id")
	updateID, err2 := pathID(r, "updateId")
	if err1 != nil || err2 != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	var in struct {
		Body     string `json:"body"`
		Internal *bool  `json:"internal"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if len([]rune(in.Body)) > 5000 {
		writeErr(w, http.StatusBadRequest, "body too long (max 5000)", nil)
		return
	}
	upd, err := s.d.Store.EditIncidentUpdate(r.Context(), id, updateID, in.Body, in.Internal)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not found", nil)
		case errors.Is(err, store.ErrInvalid):
			writeErr(w, http.StatusBadRequest, "a note needs text", map[string]string{"body": "required"})
		default:
			writeErr(w, http.StatusInternalServerError, "edit failed", nil)
		}
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update.edit", "incident", &id, "update "+itoa64(updateID))
	writeJSON(w, http.StatusOK, upd)
}

// handleSetPostmortem serves PUT /api/incidents/{id}/postmortem.
func (s *server) handleSetPostmortem(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	var in struct {
		Postmortem string `json:"postmortem"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if len([]rune(in.Postmortem)) > 50000 {
		writeErr(w, http.StatusBadRequest, "postmortem too long (max 50000)", nil)
		return
	}
	if err := s.d.Store.SetPostmortem(r.Context(), id, in.Postmortem); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "save failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "postmortem", "incident", &id, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// publicIncident is what a status page may show of an incident: no
// severity, owner, postmortem, authors, linked monitors or team notes.
type publicIncident struct {
	ID         int64                  `json:"id"`
	Title      string                 `json:"title"`
	Status     string                 `json:"status"`
	Impact     string                 `json:"impact"`
	CreatedAt  int64                  `json:"createdAt"`
	UpdatedAt  int64                  `json:"updatedAt"`
	ResolvedAt *int64                 `json:"resolvedAt"`
	Updates    []publicIncidentUpdate `json:"updates"`
}

type publicIncidentUpdate struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"createdAt"`
}

func toPublicIncidents(list []store.Incident) []publicIncident {
	out := make([]publicIncident, 0, len(list))
	for _, inc := range list {
		p := publicIncident{ID: inc.ID, Title: inc.Title, Status: inc.Status, Impact: inc.Impact,
			CreatedAt: inc.CreatedAt, UpdatedAt: inc.UpdatedAt, ResolvedAt: inc.ResolvedAt, Updates: []publicIncidentUpdate{}}
		for _, u := range inc.Updates {
			if !u.Internal {
				p.Updates = append(p.Updates, publicIncidentUpdate{ID: u.ID, Status: u.Status, Body: u.Body, CreatedAt: u.CreatedAt})
			}
		}
		out = append(out, p)
	}
	return out
}
