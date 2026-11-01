package api

import (
	"errors"
	"net/http"

	"opskest/internal/auth"
	"opskest/internal/store"
)

func (s *server) handleListOutages(w http.ResponseWriter, r *http.Request) {
	var mid *int64
	if id := queryInt64(r, "monitorId", 0); id > 0 {
		mid = &id
	}
	var gid *int64
	if id := queryInt64(r, "groupId", 0); id > 0 {
		gid = &id
	}
	limit := clampLimit(queryInt(r, "limit", 50))
	before := queryInt64(r, "before", 0)
	from := queryInt64(r, "from", 0)
	to := queryInt64(r, "to", 0)
	query := r.URL.Query().Get("q")
	if len([]rune(query)) > 100 {
		query = string([]rune(query)[:100])
	}
	note := r.URL.Query().Get("note")
	if note != "missing" && note != "present" {
		note = ""
	}
	var active *bool
	switch r.URL.Query().Get("active") {
	case "true":
		t := true
		active = &t
	case "false":
		f := false
		active = &f
	}

	outages, err := s.d.Store.ListOutages(r.Context(), store.OutageFilter{
		MonitorID: mid,
		GroupID:   gid,
		From:      from,
		To:        to,
		Before:    before,
		BeforeID:  queryInt64(r, "beforeId", 0),
		Limit:     limit,
		Active:    active,
		Query:     query,
		Note:      note,
		// minDuration is in minutes (the UI's "longer than" select).
		MinDurationMs: queryInt64(r, "minDuration", 0) * 60_000,
		Now:           s.d.Now().UnixMilli(),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	if outages == nil {
		outages = []store.Outage{}
	}
	nextBefore, nextBeforeID := outageCursor(outages, limit)
	writeJSON(w, http.StatusOK, map[string]any{"outages": outages, "nextBefore": nextBefore, "nextBeforeId": nextBeforeID})
}

func (s *server) handleOutageSummary(w http.ResponseWriter, r *http.Request) {
	now := s.d.Now().UnixMilli()
	summary, err := s.d.Store.OutageSummary(r.Context(), now)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *server) handleAckOutage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	by := u.Name
	if by == "" {
		by = u.Email
	}
	now := s.d.Now().UnixMilli()
	if err := s.d.Store.AckOutage(r.Context(), id, by, now); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "ack failed", nil)
		return
	}
	_ = s.d.Engine.Ack(id, by)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// outageCursor returns the (started_at, id) keyset cursor for the next
// page, or nils when this page wasn't full.
func outageCursor(outages []store.Outage, limit int) (*int64, *int64) {
	if len(outages) < limit || len(outages) == 0 {
		return nil, nil
	}
	last := outages[len(outages)-1]
	ts, id := last.StartedAt, last.ID
	return &ts, &id
}
