package api

import (
	"errors"
	"net/http"
	"time"

	"opskest/internal/auth"
	"opskest/internal/maint"
	"opskest/internal/store"
)

func (s *server) handleListMaintenance(w http.ResponseWriter, r *http.Request) {
	windows, err := s.d.Store.ListMaintenance(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	now := s.d.Now()

	type maintOut struct {
		store.Maintenance
		State      string `json:"state"`
		NextStart  *int64 `json:"nextStart"`
		CurrentEnd *int64 `json:"currentEnd"`
	}
	out := make([]maintOut, 0, len(windows))
	for _, m := range windows {
		mo := maintOut{Maintenance: m}
		mo.State, mo.NextStart, mo.CurrentEnd = computeMaintenanceState(m, now)
		out = append(out, mo)
	}
	writeJSON(w, http.StatusOK, map[string]any{"maintenance": out})
}

func computeMaintenanceState(m store.Maintenance, now time.Time) (state string, nextStart, currentEnd *int64) {
	if !m.Active {
		return "inactive", nil, nil
	}

	if maint.IsActive(m, now) {
		end, _ := maint.CurrentEnd(m, now)
		if !end.IsZero() {
			ms := end.UnixMilli()
			return "active", nil, &ms
		}
		return "active", nil, nil
	}

	if m.Kind == "once" {
		if m.StartsAt != nil && now.UnixMilli() < *m.StartsAt {
			return "upcoming", m.StartsAt, nil
		}
		return "past", nil, nil
	}

	// Weekly: next occurrence.
	if start, ok := maint.NextStart(m, now); ok {
		ms := start.UnixMilli()
		return "upcoming", &ms, nil
	}
	return "upcoming", nil, nil
}

func (s *server) handleCreateMaintenance(w http.ResponseWriter, r *http.Request) {
	var in store.Maintenance
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if fe := maint.Validate(&in); len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid maintenance window", fe)
		return
	}
	if len(s.d.Engine.VendorIDs(in.MonitorIDs)) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid maintenance window", map[string]string{"monitorIds": "3rd-party feeds are alert-only and cannot be put in maintenance"})
		return
	}
	u, _ := auth.UserFrom(r.Context())
	in.CreatedBy = &u.ID
	now := s.d.Now().UnixMilli()
	in.CreatedAt = now
	in.UpdatedAt = now

	if err := s.d.Store.CreateMaintenance(r.Context(), &in); err != nil {
		if errors.Is(err, store.ErrInvalid) {
			writeErr(w, http.StatusBadRequest, err.Error(), nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "create failed", nil)
		return
	}
	_ = s.d.Engine.ReloadMaintenance(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "create", "maintenance", &in.ID, in.Title)
	writeJSON(w, http.StatusCreated, in)
}

func (s *server) handleUpdateMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	var in store.Maintenance
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	in.ID = id
	in.UpdatedAt = s.d.Now().UnixMilli()
	if fe := maint.Validate(&in); len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid maintenance window", fe)
		return
	}
	if len(s.d.Engine.VendorIDs(in.MonitorIDs)) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid maintenance window", map[string]string{"monitorIds": "3rd-party feeds are alert-only and cannot be put in maintenance"})
		return
	}

	if err := s.d.Store.UpdateMaintenance(r.Context(), &in); err != nil {
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
	_ = s.d.Engine.ReloadMaintenance(r.Context())
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update", "maintenance", &id, in.Title)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleDeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if err := s.d.Store.DeleteMaintenance(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete failed", nil)
		return
	}
	_ = s.d.Engine.ReloadMaintenance(r.Context())
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "delete", "maintenance", &id, "")
	w.WriteHeader(http.StatusNoContent)
}
