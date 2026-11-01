package api

import (
	"errors"
	"fmt"
	"net/http"

	"opskest/internal/auth"
	"opskest/internal/logx"
	"opskest/internal/model"
	"opskest/internal/store"
)

// handleDataUsage reports database size and per-table rows (admin).
func (s *server) handleDataUsage(w http.ResponseWriter, r *http.Request) {
	u, err := s.d.Store.Usage(r.Context())
	if err != nil {
		logx.Err(s.d.Log, "api: data usage", err)
		writeErr(w, http.StatusInternalServerError, "usage failed", nil)
		return
	}
	settings, _ := s.d.Store.GetSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"usage":            u,
		"retentionRawDays": settings.RetentionRawDays,
	})
}

// handleClearData deletes one kind of history, keeping the last keepDays
// (0 = clear all), optionally for one monitor (admin, audited).
func (s *server) handleClearData(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind      string `json:"kind"`
		KeepDays  int    `json:"keepDays"`
		MonitorID *int64 `json:"monitorId"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	fe := model.FieldErrors{}
	if _, ok := store.ClearKinds[in.Kind]; !ok {
		fe["kind"] = "must be heartbeats, status_events, outages, alert_log or audit_log"
	}
	if in.KeepDays < 0 || in.KeepDays > 3650 {
		fe["keepDays"] = "must be 0-3650"
	}
	if in.MonitorID != nil {
		if _, ok := s.d.Engine.Monitor(*in.MonitorID); !ok {
			fe["monitorId"] = "unknown monitor"
		}
	}
	if len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid request", fe)
		return
	}
	// Queued heartbeats would otherwise land right after the delete.
	s.d.Writer.Flush()
	n, err := s.d.Store.ClearHistory(r.Context(), in.Kind, in.KeepDays, in.MonitorID, s.d.Now())
	if err != nil {
		if errors.Is(err, store.ErrInvalid) {
			writeErr(w, http.StatusBadRequest, "invalid kind", nil)
			return
		}
		logx.Err(s.d.Log, "api: clear data", err)
		writeErr(w, http.StatusInternalServerError, "clear failed", nil)
		return
	}
	actor, _ := auth.UserFrom(r.Context())
	detail := fmt.Sprintf("%s keep=%dd removed=%d", in.Kind, in.KeepDays, n)
	s.d.Store.Audit(r.Context(), &actor.ID, "clear", "data", in.MonitorID, detail)
	writeJSON(w, http.StatusOK, map[string]any{"removed": n})
}

// handleCompact runs VACUUM to return freed space to the disk (admin).
func (s *server) handleCompact(w http.ResponseWriter, r *http.Request) {
	s.d.Writer.Flush()
	before, _ := s.d.Store.Usage(r.Context())
	if err := s.d.Store.Compact(r.Context()); err != nil {
		logx.Err(s.d.Log, "api: compact", err)
		writeErr(w, http.StatusInternalServerError, "compact failed", nil)
		return
	}
	after, _ := s.d.Store.Usage(r.Context())
	actor, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &actor.ID, "compact", "data", nil, fmt.Sprintf("%d→%d bytes", before.FileBytes, after.FileBytes))
	writeJSON(w, http.StatusOK, map[string]any{"beforeBytes": before.FileBytes, "afterBytes": after.FileBytes})
}
