package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"unicode/utf8"

	"opskest/internal/auth"
	"opskest/internal/logx"
	"opskest/internal/model"
	"opskest/internal/store"
)

// ── DTOs ──────────────────────────────────────────────────────────

type HeaderIn struct {
	Name   string  `json:"name"`
	Value  *string `json:"value"`
	Secret bool    `json:"secret"`
}

type MonitorIn struct {
	Name    string            `json:"name"`
	Type    model.MonitorType `json:"type"`
	Target  string            `json:"target"`
	GroupID *int64            `json:"groupId"`

	TagIDs     []int64 `json:"tagIds"`
	ChannelIDs []int64 `json:"channelIds"`

	IntervalS      int `json:"intervalS"`
	RetryIntervalS int `json:"retryIntervalS"`
	Retries        int `json:"retries"`
	TimeoutMs      int `json:"timeoutMs"`
	DegradedMs     int `json:"degradedMs"`
	SSLWarnDays    int `json:"sslWarnDays"`
	ReminderMin    int `json:"reminderMin"`

	UpsideDown     bool   `json:"upsideDown"`
	NotifyDegraded bool   `json:"notifyDegraded"`
	Paused         bool   `json:"paused"`
	Description    string `json:"description"`
	Runbook        string `json:"runbook"`

	Settings model.Settings `json:"settings"`
	Headers  []HeaderIn     `json:"headers"`

	Secrets struct {
		BasicPass   *string `json:"basicPass"`
		BearerToken *string `json:"bearerToken"`
		ProxyPass   *string `json:"proxyPass"`
		ClientCert  *string `json:"clientCert"`
		ClientKey   *string `json:"clientKey"`
		OAuthSecret *string `json:"oauthSecret"`
	} `json:"secrets"`
}

type SecretState struct {
	IsSet bool `json:"isSet"`
}

type HeaderOut struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret bool   `json:"secret"`
	IsSet  bool   `json:"isSet,omitempty"`
}

type MonitorOut struct {
	ID      int64             `json:"id"`
	Name    string            `json:"name"`
	Type    model.MonitorType `json:"type"`
	Target  string            `json:"target"`
	GroupID *int64            `json:"groupId"`

	TagIDs     []int64 `json:"tagIds"`
	ChannelIDs []int64 `json:"channelIds"`

	IntervalS      int `json:"intervalS"`
	RetryIntervalS int `json:"retryIntervalS"`
	Retries        int `json:"retries"`
	TimeoutMs      int `json:"timeoutMs"`
	DegradedMs     int `json:"degradedMs"`
	SSLWarnDays    int `json:"sslWarnDays"`
	ReminderMin    int `json:"reminderMin"`

	UpsideDown     bool   `json:"upsideDown"`
	NotifyDegraded bool   `json:"notifyDegraded"`
	Paused         bool   `json:"paused"`
	Description    string `json:"description"`
	Runbook        string `json:"runbook"`

	Settings model.Settings `json:"settings"`
	Headers  []HeaderOut    `json:"headers"`

	PushToken string `json:"pushToken,omitempty"`
	PushURL   string `json:"pushUrl,omitempty"`

	Secrets struct {
		BasicPass   SecretState `json:"basicPass"`
		BearerToken SecretState `json:"bearerToken"`
		ProxyPass   SecretState `json:"proxyPass"`
		ClientCert  SecretState `json:"clientCert"`
		ClientKey   SecretState `json:"clientKey"`
		OAuthSecret SecretState `json:"oauthSecret"`
	} `json:"secrets"`

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
}

func monitorToOut(m *model.Monitor, appURL string) MonitorOut {
	out := MonitorOut{
		ID:             m.ID,
		Name:           m.Name,
		Type:           m.Type,
		Target:         m.Target,
		GroupID:        m.GroupID,
		TagIDs:         m.TagIDs,
		ChannelIDs:     m.ChannelIDs,
		IntervalS:      m.IntervalS,
		RetryIntervalS: m.RetryIntervalS,
		Retries:        m.Retries,
		TimeoutMs:      m.TimeoutMs,
		DegradedMs:     m.DegradedMs,
		SSLWarnDays:    m.SSLWarnDays,
		ReminderMin:    m.ReminderMin,
		UpsideDown:     m.UpsideDown,
		NotifyDegraded: m.NotifyDegraded,
		Paused:         m.Paused,
		Description:    m.Description,
		Runbook:        m.Runbook,
		Settings:       m.Settings,
		CreatedAt:      m.CreatedAt.UnixMilli(),
		UpdatedAt:      m.UpdatedAt.UnixMilli(),
	}
	if out.TagIDs == nil {
		out.TagIDs = []int64{}
	}
	if out.ChannelIDs == nil {
		out.ChannelIDs = []int64{}
	}

	// Strip secret header values; show isSet.
	out.Settings.Headers = nil // clear from settings, use the explicit Headers
	for _, h := range m.Settings.Headers {
		ho := HeaderOut{Name: h.Name, Secret: h.Secret}
		if h.Secret {
			ho.IsSet = m.Secrets.Headers[h.Name] != ""
		} else {
			ho.Value = h.Value
		}
		out.Headers = append(out.Headers, ho)
	}
	if out.Headers == nil {
		out.Headers = []HeaderOut{}
	}

	out.Secrets.BasicPass = SecretState{IsSet: m.Secrets.BasicPass != ""}
	out.Secrets.BearerToken = SecretState{IsSet: m.Secrets.BearerToken != ""}
	out.Secrets.ProxyPass = SecretState{IsSet: m.Secrets.ProxyPass != ""}
	out.Secrets.ClientCert = SecretState{IsSet: m.Secrets.ClientCert != ""}
	out.Secrets.ClientKey = SecretState{IsSet: m.Secrets.ClientKey != ""}
	out.Secrets.OAuthSecret = SecretState{IsSet: m.Secrets.OAuthSecret != ""}

	if m.Type == model.TypePush && m.PushToken != "" {
		out.PushToken = m.PushToken
		out.PushURL = appURL + "/push/" + m.PushToken
	}

	return out
}

func applyMonitorIn(in MonitorIn, m *model.Monitor, stored *model.Monitor) {
	m.Name = in.Name
	m.Type = in.Type
	m.Target = in.Target
	m.GroupID = in.GroupID
	m.TagIDs = in.TagIDs
	m.ChannelIDs = in.ChannelIDs
	m.IntervalS = in.IntervalS
	m.RetryIntervalS = in.RetryIntervalS
	m.Retries = in.Retries
	m.TimeoutMs = in.TimeoutMs
	m.DegradedMs = in.DegradedMs
	m.SSLWarnDays = in.SSLWarnDays
	m.ReminderMin = in.ReminderMin
	m.UpsideDown = in.UpsideDown
	m.NotifyDegraded = in.NotifyDegraded
	m.Paused = in.Paused
	m.Description = in.Description
	m.Runbook = in.Runbook
	m.Settings = in.Settings

	// Non-secret headers.
	var headers []model.Header
	secrets := m.Secrets
	if secrets.Headers == nil {
		secrets.Headers = make(map[string]string)
	}
	for _, h := range in.Headers {
		headers = append(headers, model.Header{Name: h.Name, Secret: h.Secret})
		if h.Secret {
			if h.Value != nil {
				// New or changed value.
				secrets.Headers[h.Name] = *h.Value
			} else if stored != nil {
				// Keep stored.
				secrets.Headers[h.Name] = stored.Secrets.Headers[h.Name]
			}
		} else if h.Value != nil {
			headers[len(headers)-1].Value = *h.Value
		}
	}
	m.Settings.Headers = headers

	// Merge secrets.
	if in.Secrets.BasicPass != nil {
		secrets.BasicPass = *in.Secrets.BasicPass
	} else if stored != nil {
		secrets.BasicPass = stored.Secrets.BasicPass
	}
	if in.Secrets.BearerToken != nil {
		secrets.BearerToken = *in.Secrets.BearerToken
	} else if stored != nil {
		secrets.BearerToken = stored.Secrets.BearerToken
	}
	// The other write-only secrets follow the same rule: absent keeps the
	// stored value, a string (an empty one clears it) replaces it.
	secrets.ProxyPass = mergeSecret(in.Secrets.ProxyPass, stored, func(s model.Secrets) string { return s.ProxyPass })
	secrets.ClientCert = mergeSecret(in.Secrets.ClientCert, stored, func(s model.Secrets) string { return s.ClientCert })
	secrets.ClientKey = mergeSecret(in.Secrets.ClientKey, stored, func(s model.Secrets) string { return s.ClientKey })
	secrets.OAuthSecret = mergeSecret(in.Secrets.OAuthSecret, stored, func(s model.Secrets) string { return s.OAuthSecret })
	if m.Type != model.TypeHTTP {
		secrets.ProxyPass, secrets.ClientCert, secrets.ClientKey, secrets.OAuthSecret = "", "", "", ""
	}
	if m.Settings.ProxyURL == "" { // no proxy: its login goes with it
		m.Settings.ProxyUser, secrets.ProxyPass = "", ""
	}
	m.Secrets = secrets

	m.ApplyDefaults()
}

// mergeSecret picks a write-only secret's new value: the submitted one, or
// the stored one when the field was left out.
func mergeSecret(in *string, stored *model.Monitor, get func(model.Secrets) string) string {
	switch {
	case in != nil:
		return *in
	case stored != nil:
		return get(stored.Secrets)
	}
	return ""
}

// ── Handlers ──────────────────────────────────────────────────────

func (s *server) handleListMonitors(w http.ResponseWriter, r *http.Request) {
	sums := s.d.Engine.Summaries()
	groups := s.d.Engine.Groups()
	writeJSON(w, http.StatusOK, map[string]any{
		"monitors": sums,
		"groups":   groups,
	})
}

func (s *server) handleCreateMonitor(w http.ResponseWriter, r *http.Request) {
	var in MonitorIn
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	m := model.NewMonitor(in.Type)
	applyMonitorIn(in, m, nil)

	if fe := s.validateMonitor(m); len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "validation error", fe)
		return
	}

	u, _ := auth.UserFrom(r.Context())
	m.CreatedBy = &u.ID
	now := s.d.Now()
	m.CreatedAt = now
	m.UpdatedAt = now

	if err := s.d.Store.CreateMonitor(r.Context(), m); err != nil {
		writeErr(w, http.StatusInternalServerError, "create failed", nil)
		return
	}

	s.d.Engine.UpsertMonitor(m)
	s.d.Store.Audit(r.Context(), &u.ID, "create", "monitor", &m.ID, m.Name)

	writeJSON(w, http.StatusCreated, monitorToOut(m, s.d.Cfg.AppURL))
}

func (s *server) handleGetMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	m, err := s.d.Store.GetMonitor(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, monitorToOut(m, s.d.Cfg.AppURL))
}

func (s *server) handleUpdateMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	stored, err := s.d.Store.GetMonitor(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	var in MonitorIn
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	m := stored.Clone()
	applyMonitorIn(in, m, stored)

	if fe := s.validateMonitor(m); len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "validation error", fe)
		return
	}

	m.UpdatedAt = s.d.Now()
	if err := s.d.Store.UpdateMonitor(r.Context(), m); err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed", nil)
		return
	}

	s.d.Engine.UpsertMonitor(m)
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update", "monitor", &m.ID, m.Name)

	writeJSON(w, http.StatusOK, monitorToOut(m, s.d.Cfg.AppURL))
}

func (s *server) handleDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	m, err := s.d.Store.GetMonitor(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	// Remove from the engine first, then the store.
	s.d.Engine.RemoveMonitor(id)
	if err := s.d.Store.DeleteMonitor(r.Context(), id); err != nil {
		// A failed delete must not silently stop monitoring a stored service.
		s.d.Engine.UpsertMonitor(m)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete failed", nil)
		return
	}
	if s.d.Regions != nil {
		s.d.Regions.Forget(id)
		s.d.Writer.Do(store.OpReplaceRegionResults(id, nil))
	}
	s.detachDependents(r.Context(), id)
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "delete", "monitor", &id, "")
	w.WriteHeader(http.StatusNoContent)
}

// detachDependents removes a deleted monitor from every other monitor's
// dependsOn and from every group's children. SQLite reuses the highest
// deleted id, so a stale parent id would otherwise start holding the
// children's alerts whenever an unrelated new monitor is down, and a stale
// group member would start steering a group.
func (s *server) detachDependents(ctx context.Context, deleted int64) {
	for _, m := range s.d.Engine.Monitors() {
		if !slices.Contains(m.Settings.DependsOn, deleted) && !slices.Contains(m.Settings.Children, deleted) {
			continue
		}
		stored, err := s.d.Store.GetMonitor(ctx, m.ID)
		if err != nil {
			continue
		}
		stored.Settings.DependsOn = slices.DeleteFunc(slices.Clone(stored.Settings.DependsOn), func(p int64) bool { return p == deleted })
		stored.Settings.Children = slices.DeleteFunc(slices.Clone(stored.Settings.Children), func(p int64) bool { return p == deleted })
		stored.UpdatedAt = s.d.Now()
		if err := s.d.Store.UpdateMonitor(ctx, stored); err != nil {
			logx.Err(s.d.Log, "detach dependents", err)
			continue
		}
		s.d.Engine.UpsertMonitor(stored)
	}
}

func (s *server) handlePauseMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if err := s.d.Store.SetPaused(r.Context(), id, true); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "pause failed", nil)
		return
	}
	s.d.Engine.SetPaused(id, true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleResumeMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if err := s.d.Store.SetPaused(r.Context(), id, false); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "resume failed", nil)
		return
	}
	s.d.Engine.SetPaused(id, false)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *server) handleDuplicateMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	stored, err := s.d.Store.GetMonitor(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	dup := stored.Clone()
	dup.ID = 0
	dup.Name = "Copy of " + stored.Name
	dup.Paused = true
	dup.PushToken = "" // gets a new token

	u, _ := auth.UserFrom(r.Context())
	dup.CreatedBy = &u.ID
	now := s.d.Now()
	dup.CreatedAt = now
	dup.UpdatedAt = now

	if err := s.d.Store.CreateMonitor(r.Context(), dup); err != nil {
		writeErr(w, http.StatusInternalServerError, "duplicate failed", nil)
		return
	}
	s.d.Engine.UpsertMonitor(dup)
	s.d.Store.Audit(r.Context(), &u.ID, "duplicate", "monitor", &dup.ID, dup.Name)

	writeJSON(w, http.StatusCreated, map[string]int64{"id": dup.ID})
}

func (s *server) handleTestMonitor(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MonitorIn
		ID *int64 `json:"id"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	m := model.NewMonitor(in.Type)
	var stored *model.Monitor
	if in.ID != nil {
		var err error
		stored, err = s.d.Store.GetMonitor(r.Context(), *in.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
			return
		}
	}
	applyMonitorIn(in.MonitorIn, m, stored)
	// A test run skips the form validation, so bound the timeout here: a huge
	// (or overflowing) value would hold the request and its connection open.
	m.TimeoutMs = min(max(m.TimeoutMs, 1000), 60_000)

	var result model.Result
	switch m.Type {
	case model.TypeGroup:
		result = s.d.Engine.EvaluateGroup(m)
	case model.TypeManual:
		result = model.Result{At: s.d.Now(), OK: true, LatencyMs: -1}
	default:
		result = s.d.Checker(r.Context(), m)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) handleBulkMonitors(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs     []int64 `json:"ids"`
		Action  string  `json:"action"`
		GroupID *int64  `json:"groupId"`
		Retries *int    `json:"retries"` // action "retries": failed checks tolerated before DOWN
		// ChannelID is the channel for the actions "add_channel" and "remove_channel".
		ChannelID *int64 `json:"channelId"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if len(in.IDs) == 0 || len(in.IDs) > 500 {
		writeErr(w, http.StatusBadRequest, "provide 1 to 500 ids", nil)
		return
	}
	switch in.Action {
	case "pause", "resume", "delete", "move":
	case "retries":
		if in.Retries == nil || *in.Retries < 0 || *in.Retries > 10 {
			writeErr(w, http.StatusBadRequest, "retries must be 0-10", nil)
			return
		}
	case "add_channel", "remove_channel":
		if in.ChannelID == nil {
			writeErr(w, http.StatusBadRequest, "channelId required", nil)
			return
		}
		if _, err := s.d.Store.GetChannel(r.Context(), *in.ChannelID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusBadRequest, "channel not found", nil)
				return
			}
			writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "invalid action", nil)
		return
	}
	ctx := r.Context()
	u, _ := auth.UserFrom(ctx)
	if in.Action == "move" && in.GroupID != nil {
		groups, err := s.d.Store.ListGroups(ctx)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
			return
		}
		found := false
		for _, group := range groups {
			if group.ID == *in.GroupID {
				found = true
				break
			}
		}
		if !found {
			writeErr(w, http.StatusBadRequest, "project not found", nil)
			return
		}
	}
	// Validate the entire selection before making any changes. Deduplicate IDs
	// so one monitor is never deleted or audited twice in a single request.
	monitors := make([]*model.Monitor, 0, len(in.IDs))
	seen := make(map[int64]bool, len(in.IDs))
	for _, id := range in.IDs {
		if id <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid monitor id", nil)
			return
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		m, err := s.d.Store.GetMonitor(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "monitor not found", nil)
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
			return
		}
		monitors = append(monitors, m)
	}
	processed := 0
	for _, m := range monitors {
		var err error
		switch in.Action {
		case "pause", "resume":
			paused := in.Action == "pause"
			err = s.d.Store.SetPaused(ctx, m.ID, paused)
			if err == nil {
				s.d.Engine.SetPaused(m.ID, paused)
			}
		case "delete":
			s.d.Engine.RemoveMonitor(m.ID)
			err = s.d.Store.DeleteMonitor(ctx, m.ID)
			if err != nil {
				s.d.Engine.UpsertMonitor(m)
			} else {
				s.detachDependents(ctx, m.ID)
				if s.d.Regions != nil {
					s.d.Regions.Forget(m.ID)
					s.d.Writer.Do(store.OpReplaceRegionResults(m.ID, nil))
				}
			}
		case "move", "retries", "add_channel", "remove_channel":
			switch in.Action {
			case "move":
				m.GroupID = in.GroupID
			case "retries":
				m.Retries = *in.Retries
			case "add_channel":
				if !slices.Contains(m.ChannelIDs, *in.ChannelID) {
					m.ChannelIDs = append(m.ChannelIDs, *in.ChannelID)
				}
			case "remove_channel":
				m.ChannelIDs = slices.DeleteFunc(slices.Clone(m.ChannelIDs), func(c int64) bool { return c == *in.ChannelID })
			}
			m.UpdatedAt = s.d.Now()
			err = s.d.Store.UpdateMonitor(ctx, m)
			if err == nil {
				s.d.Engine.UpsertMonitor(m)
			}
		}
		if err != nil {
			// Earlier successes remain committed. Make partial completion explicit
			// and never advance the engine after a failed persistence operation.
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error":     "Bulk action stopped. Some changes may have completed; review monitor states before retrying.",
				"processed": processed, "failedId": m.ID,
			})
			return
		}
		processed++
		s.d.Store.Audit(ctx, &u.ID, "bulk_"+in.Action, "monitor", &m.ID, "")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "processed": processed})
}

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	username := ""
	if c, err := s.d.Store.Credentials(r.Context(), u.ID); err == nil {
		username = c.Username
	}
	enrolled, _ := s.d.Store.HasTwoFactor(r.Context(), u.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"id":                u.ID,
		"email":             u.Email,
		"username":          username,
		"name":              u.Name,
		"avatarUrl":         u.AvatarURL,
		"role":              u.Role,
		"twoFactorEnabled":  enrolled,
		"twoFactorRequired": s.requireTwoFactor.Load() && !enrolled,
	})
}

// handleCheckNow runs a monitor's check immediately (admin only). The
// result arrives over the live stream like any scheduled check.
func (s *server) handleCheckNow(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if _, ok := s.d.Engine.Monitor(id); !ok {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	if !s.d.Engine.RunNow(id) {
		writeErr(w, http.StatusConflict, "paused, push and manual monitors can't be checked on demand", nil)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

// handleSetManualStatus sets a manual monitor's status:
// POST /api/monitors/{id}/status {"status": "up|degraded|down", "reason": "..."}.
func (s *server) handleSetManualStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	var in struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if in.Status != "up" && in.Status != "degraded" && in.Status != "down" {
		writeErr(w, http.StatusBadRequest, "validation error", model.FieldErrors{"status": "must be up, degraded or down"})
		return
	}
	if utf8.RuneCountInString(in.Reason) > 200 {
		writeErr(w, http.StatusBadRequest, "validation error", model.FieldErrors{"reason": "must be at most 200 characters"})
		return
	}
	m, ok := s.d.Engine.Monitor(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	if m.Type != model.TypeManual {
		writeErr(w, http.StatusConflict, "only manual monitors can be set by hand", nil)
		return
	}
	if !s.d.Engine.SetManual(id, in.Status, in.Reason) {
		writeErr(w, http.StatusConflict, "a paused monitor can't be set; resume it first", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "set_status", "monitor", &id, in.Status)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// validateMonitor runs the monitor's own rules plus the ones that need
// every other monitor: dependency parents must exist and form no loop.
func (s *server) validateMonitor(m *model.Monitor) model.FieldErrors {
	fe := m.Validate()
	if _, bad := fe["settings.children"]; !bad && m.Type == model.TypeGroup {
		if msg := s.d.Engine.ValidateGroup(m.ID, m.Settings.Children); msg != "" {
			fe["settings.children"] = msg
		}
	}
	if _, bad := fe["settings.dependsOn"]; !bad {
		if msg := s.d.Engine.ValidateDependencies(m.ID, m.Settings.DependsOn); msg != "" {
			fe["settings.dependsOn"] = msg
		}
	}
	if m.Settings.Vendor && len(m.Settings.DependsOn) > 0 {
		if _, bad := fe["settings.dependsOn"]; !bad {
			fe["settings.dependsOn"] = "3rd-party feeds are alert-only and cannot depend on other monitors"
		}
	}
	return fe
}
