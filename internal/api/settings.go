package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"opskest/internal/alert"
	"opskest/internal/auth"
	"opskest/internal/model"
	"opskest/internal/store"
)

func (s *server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.d.Store.ListUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	type userOut struct {
		ID          int64  `json:"id"`
		Email       string `json:"email"`
		Username    string `json:"username"`
		Name        string `json:"name"`
		AvatarURL   string `json:"avatarUrl"`
		Role        string `json:"role"`
		CreatedAt   int64  `json:"createdAt"`
		LastLoginAt *int64 `json:"lastLoginAt"`
		TwoFactor   bool   `json:"twoFactorEnabled"`
	}
	twoFactor, _ := s.d.Store.TwoFactorUsers(r.Context())
	out := make([]userOut, 0, len(users))
	for _, u := range users {
		uo := userOut{
			ID:        u.ID,
			Email:     u.Email,
			Name:      u.Name,
			AvatarURL: u.AvatarURL,
			Role:      u.Role,
			CreatedAt: u.CreatedAt.UnixMilli(),
			TwoFactor: twoFactor[u.ID],
		}
		if u.LastLoginAt != nil {
			ms := u.LastLoginAt.UnixMilli()
			uo.LastLoginAt = &ms
		}
		if c, err := s.d.Store.Credentials(r.Context(), u.ID); err == nil {
			uo.Username = c.Username
		}
		out = append(out, uo)
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if !auth.ValidAssignableRole(in.Role) {
		writeErr(w, http.StatusBadRequest, "invalid role", nil)
		return
	}
	// Roles are an administrator's decision about someone else: nobody
	// changes their own (an admin cannot demote or disable themselves).
	if actor, ok := auth.UserFrom(r.Context()); ok && actor.ID == id {
		writeErr(w, http.StatusConflict, "you cannot change your own role", nil)
		return
	}
	if err := s.d.Store.SetUserRole(r.Context(), id, in.Role); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "cannot remove last admin", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "update failed", nil)
		return
	}

	// If the user was disabled, delete their sessions.
	if in.Role == store.RoleDisabled {
		_ = s.d.Store.DeleteUserSessions(r.Context(), id)
	}

	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update_role", "user", &id, in.Role)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.d.Store.GetSettings(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	// A client that does not send requireTwoFactor (an older form) must not
	// switch the policy off by omission, so the body is read twice.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyDefault))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", nil)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var in store.Settings
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	var sent struct {
		RequireTwoFactor *bool `json:"requireTwoFactor"`
	}
	_ = json.Unmarshal(body, &sent)
	if sent.RequireTwoFactor == nil {
		in.RequireTwoFactor = s.requireTwoFactor.Load()
	}
	if err := s.d.Store.PutSettings(r.Context(), in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	s.requireTwoFactor.Store(in.RequireTwoFactor)
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update", "settings", nil, "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleAlertLog(w http.ResponseWriter, r *http.Request) {
	limit := clampLimit(queryInt(r, "limit", 50))
	before := queryInt64(r, "before", 0)
	entries, err := s.d.Store.ListAlertLogFiltered(r.Context(), limit, before, r.URL.Query().Get("failed") == "true")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	// Resolve channel and monitor names so the UI can show them.
	chanNames := map[int64]string{}
	if chans, err := s.d.Store.ListChannels(r.Context()); err == nil {
		for _, c := range chans {
			chanNames[c.ID] = c.Name
		}
	}
	type entryOut struct {
		ID          int64  `json:"id"`
		TS          int64  `json:"ts"`
		ChannelID   *int64 `json:"channelId"`
		ChannelName string `json:"channelName"`
		MonitorID   *int64 `json:"monitorId"`
		MonitorName string `json:"monitorName"`
		Event       string `json:"event"`
		OK          bool   `json:"ok"`
		Attempt     int    `json:"attempt"`
		Error       string `json:"error"`
	}
	out := make([]entryOut, 0, len(entries))
	for _, e := range entries {
		o := entryOut{ID: e.ID, TS: e.TS, ChannelID: e.ChannelID, MonitorID: e.MonitorID, Event: e.Event, OK: e.OK, Attempt: e.Attempt, Error: e.Error}
		if e.ChannelID != nil {
			o.ChannelName = chanNames[*e.ChannelID]
		}
		if e.MonitorID != nil {
			if m, ok := s.d.Engine.Monitor(*e.MonitorID); ok {
				o.MonitorName = m.Name
			}
		}
		out = append(out, o)
	}
	var next *int64
	if len(entries) == limit {
		v := entries[len(entries)-1].TS
		next = &v
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "nextBefore": next})
}

func (s *server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	limit := clampLimit(queryInt(r, "limit", 50))
	before := queryInt64(r, "before", 0)
	entries, err := s.d.Store.ListAudit(r.Context(), limit, before)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	type entryOut struct {
		ID        int64  `json:"id"`
		TS        int64  `json:"ts"`
		UserID    *int64 `json:"userId"`
		UserEmail string `json:"userEmail"`
		Action    string `json:"action"`
		Entity    string `json:"entity"`
		EntityID  *int64 `json:"entityId"`
		Detail    string `json:"detail"`
	}
	out := make([]entryOut, 0, len(entries))
	for _, e := range entries {
		out = append(out, entryOut{ID: e.ID, TS: e.TS, UserID: e.UserID, UserEmail: e.UserEmail, Action: e.Action, Entity: e.Entity, EntityID: e.EntityID, Detail: e.Detail})
	}
	var next *int64
	if len(entries) == limit {
		v := entries[len(entries)-1].TS
		next = &v
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out, "nextBefore": next})
}

// handleCreateUser creates a password-protected local teammate account.
func (s *server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Name     string `json:"name"`
		Role     string `json:"role"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	username := auth.NormalizeUsername(in.Username)
	if in.Role == "" {
		in.Role = store.RoleViewer
	}
	fields := model.FieldErrors{}
	if err := auth.ValidateCredentials(username, in.Password); err != nil {
		fields["credentials"] = err.Error()
	}
	if !auth.ValidAssignableRole(in.Role) {
		fields["role"] = "role must be admin, user, viewer or disabled"
	}
	if len(in.Name) > 120 {
		fields["name"] = "at most 120 characters"
	}
	if len(fields) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid user", fields)
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create failed", nil)
		return
	}
	u, err := s.d.Store.CreateLocalUser(r.Context(), username, strings.TrimSpace(in.Name), hash, in.Role, false, s.d.Now())
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "a user with this username already exists", model.FieldErrors{"username": "already exists"})
			return
		}
		writeErr(w, http.StatusInternalServerError, "create failed", nil)
		return
	}
	actor, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &actor.ID, "create", "user", &u.ID, u.Role)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": u.ID, "email": u.Email, "username": username, "name": u.Name, "avatarUrl": u.AvatarURL,
		"role": u.Role, "createdAt": u.CreatedAt.UnixMilli(), "lastLoginAt": nil,
	})
}

// handleDeleteUser removes an account (admin only). You cannot remove
// yourself, and the last admin cannot be removed.
func (s *server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	actor, _ := auth.UserFrom(r.Context())
	if actor != nil && actor.ID == id {
		writeErr(w, http.StatusConflict, "you cannot remove your own account", nil)
		return
	}
	if err := s.d.Store.DeleteUser(r.Context(), id); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not found", nil)
		case errors.Is(err, store.ErrConflict):
			writeErr(w, http.StatusConflict, "cannot remove the last admin", nil)
		default:
			writeErr(w, http.StatusInternalServerError, "delete failed", nil)
		}
		return
	}
	s.d.Store.Audit(r.Context(), &actor.ID, "delete", "user", &id, "")
	w.WriteHeader(http.StatusNoContent)
}

// handleResendAlert serves POST /api/alert-log/{id}/resend: rebuild the
// logged event (monitor, kind, and the outage's reason for down/recovered)
// and send it once to the same channel.
func (s *server) handleResendAlert(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	e, err := s.d.Store.GetAlertLog(r.Context(), id)
	if err != nil || e.ChannelID == nil || e.MonitorID == nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	ch, err := s.d.Store.GetChannel(r.Context(), *e.ChannelID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "channel no longer exists", nil)
		return
	}
	kind := model.EventKind(e.Event)
	switch kind {
	case model.EvDown, model.EvRecovered, model.EvDegraded, model.EvReminder:
	default:
		writeErr(w, http.StatusBadRequest, "this kind of alert can't be resent", nil)
		return
	}
	ev := model.Event{Kind: kind, MonitorID: *e.MonitorID, At: time.UnixMilli(e.TS), Since: time.UnixMilli(e.TS)}
	switch kind {
	case model.EvDown, model.EvReminder:
		ev.Status = model.StatusDown
	case model.EvRecovered:
		ev.Status = model.StatusUp
	case model.EvDegraded:
		ev.Status = model.StatusDegraded
	}
	mid := *e.MonitorID
	if outs, err := s.d.Store.ListOutages(r.Context(), store.OutageFilter{MonitorID: &mid, To: e.TS + 1, Limit: 1}); err == nil && len(outs) > 0 {
		o := outs[0]
		ev.Reason, ev.Since, ev.OutageID = o.FirstReason, time.UnixMilli(o.StartedAt), o.ID
		if o.EndedAt != nil {
			ev.Duration = time.Duration(*o.EndedAt-o.StartedAt) * time.Millisecond
		}
	}
	if err := s.d.Alerts.Resend(r.Context(), *ch, ev); err != nil {
		writeErr(w, http.StatusBadGateway, "resend failed: "+alert.Redact(err.Error()), nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "resend", "alert", &id, ch.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
