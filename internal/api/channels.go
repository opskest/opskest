package api

import (
	"errors"
	"net/http"
	"slices"

	"opskest/internal/alert"
	"opskest/internal/auth"
	"opskest/internal/store"
)

// ── Channel DTOs ──────────────────────────────────────────────────

type channelOut struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	IsDefault bool           `json:"isDefault"`
	Config    map[string]any `json:"config"`
	CreatedAt int64          `json:"createdAt"`
}

func maskChannelConfig(ch store.Channel) map[string]any {
	out := map[string]any{}
	cfg := ch.Config

	// Secret fields → {isSet: true/false}
	if cfg.URL != "" {
		out["url"] = map[string]bool{"isSet": true}
	}
	if cfg.RoutingKey != "" {
		out["routingKey"] = map[string]bool{"isSet": true}
	}
	if cfg.APIKey != "" {
		out["apiKey"] = map[string]bool{"isSet": true}
	}
	if cfg.Password != "" {
		out["password"] = map[string]bool{"isSet": true}
	}
	if cfg.HMACSecret != "" {
		out["hmacSecret"] = map[string]bool{"isSet": true}
	}
	if len(cfg.Headers) > 0 {
		out["headers"] = map[string]bool{"isSet": true}
	}

	// Plain fields
	if cfg.Host != "" {
		out["host"] = cfg.Host
	}
	if cfg.Port != 0 {
		out["port"] = cfg.Port
	}
	if cfg.Username != "" {
		out["username"] = cfg.Username
	}
	if cfg.From != "" {
		out["from"] = cfg.From
	}
	if len(cfg.To) > 0 {
		out["to"] = cfg.To
	}
	if cfg.TLSMode != "" {
		out["tlsMode"] = cfg.TLSMode
	}
	if cfg.Region != "" {
		out["region"] = cfg.Region
	}
	if cfg.Target != "" {
		out["target"] = cfg.Target
	}
	if cfg.Topic != "" {
		out["topic"] = cfg.Topic
	}
	if cfg.Template != "" {
		out["template"] = cfg.Template
	}
	if cfg.Quiet != nil {
		out["quiet"] = cfg.Quiet
	}

	return out
}

func toChannelOut(ch store.Channel) channelOut {
	return channelOut{
		ID:        ch.ID,
		Name:      ch.Name,
		Type:      ch.Type,
		IsDefault: ch.IsDefault,
		Config:    maskChannelConfig(ch),
		CreatedAt: ch.CreatedAt.UnixMilli(),
	}
}

// ── Handlers ──────────────────────────────────────────────────────

func (s *server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.d.Store.ListChannels(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	// Only an admin sees a channel's configuration. Any other signed-in role
	// gets id, name and type (plus the default flag) so monitors can be wired
	// to channels, and never a URL, address, key or header.
	if u, ok := auth.UserFrom(r.Context()); !ok || u.Role != auth.RoleAdmin {
		type channelRef struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Type      string `json:"type"`
			IsDefault bool   `json:"isDefault"`
		}
		refs := make([]channelRef, 0, len(channels))
		for _, ch := range channels {
			refs = append(refs, channelRef{ID: ch.ID, Name: ch.Name, Type: ch.Type, IsDefault: ch.IsDefault})
		}
		writeJSON(w, http.StatusOK, map[string]any{"channels": refs})
		return
	}
	out := make([]channelOut, 0, len(channels))
	for _, ch := range channels {
		out = append(out, toChannelOut(ch))
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

func (s *server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string              `json:"name"`
		Type      string              `json:"type"`
		IsDefault bool                `json:"isDefault"`
		Config    store.ChannelConfig `json:"config"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required", nil)
		return
	}
	in.Config.Quiet = normalizeQuiet(in.Config.Quiet)
	if fe := alert.ValidateConfig(in.Type, in.Config); len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "validation error", fe)
		return
	}

	ch := &store.Channel{
		Name:      in.Name,
		Type:      in.Type,
		Config:    in.Config,
		IsDefault: in.IsDefault,
	}
	if err := s.d.Store.CreateChannel(r.Context(), ch); err != nil {
		writeErr(w, http.StatusInternalServerError, "create failed", nil)
		return
	}
	_ = s.d.Alerts.ReloadChannels(r.Context())
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "create", "channel", &ch.ID, ch.Name)
	writeJSON(w, http.StatusCreated, toChannelOut(*ch))
}

func (s *server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	stored, err := s.d.Store.GetChannel(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}

	// Use a wrapper with *string for nullable/merge fields.
	var in struct {
		Name      string `json:"name"`
		Type      string `json:"type"`
		IsDefault bool   `json:"isDefault"`
		Config    struct {
			URL        *string            `json:"url"`
			Headers    *map[string]string `json:"headers"`
			HMACSecret *string            `json:"hmacSecret"`
			RoutingKey *string            `json:"routingKey"`
			APIKey     *string            `json:"apiKey"`
			Region     *string            `json:"region"`
			Host       *string            `json:"host"`
			Port       *int               `json:"port"`
			Username   *string            `json:"username"`
			Password   *string            `json:"password"`
			From       *string            `json:"from"`
			To         *[]string          `json:"to"`
			TLSMode    *string            `json:"tlsMode"`
			Target     *string            `json:"target"`
			Topic      *string            `json:"topic"`
			Template   *string            `json:"template"`
			Quiet      *store.QuietHours  `json:"quiet"`
		} `json:"config"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required", nil)
		return
	}
	cfg := stored.Config
	if in.Type != "" && in.Type != stored.Type {
		// A different service: the old secrets and URL mean nothing to it, and
		// would be sent to it (a Telegram token posted to a former Slack hook).
		cfg = store.ChannelConfig{}
	}
	if in.Config.URL != nil {
		cfg.URL = *in.Config.URL
	}
	if in.Config.Headers != nil {
		cfg.Headers = *in.Config.Headers
	}
	if in.Config.HMACSecret != nil {
		cfg.HMACSecret = *in.Config.HMACSecret
	}
	if in.Config.RoutingKey != nil {
		cfg.RoutingKey = *in.Config.RoutingKey
	}
	if in.Config.APIKey != nil {
		cfg.APIKey = *in.Config.APIKey
	}
	if in.Config.Region != nil {
		cfg.Region = *in.Config.Region
	}
	if in.Config.Host != nil {
		cfg.Host = *in.Config.Host
	}
	if in.Config.Port != nil {
		cfg.Port = *in.Config.Port
	}
	if in.Config.Username != nil {
		cfg.Username = *in.Config.Username
	}
	if in.Config.Password != nil {
		cfg.Password = *in.Config.Password
	}
	if in.Config.From != nil {
		cfg.From = *in.Config.From
	}
	if in.Config.To != nil {
		cfg.To = *in.Config.To
	}
	if in.Config.TLSMode != nil {
		cfg.TLSMode = *in.Config.TLSMode
	}
	if in.Config.Target != nil {
		cfg.Target = *in.Config.Target
	}
	if in.Config.Topic != nil {
		cfg.Topic = *in.Config.Topic
	}
	if in.Config.Template != nil {
		cfg.Template = *in.Config.Template
	}
	if in.Config.Quiet != nil { // enabled: false switches quiet hours off
		cfg.Quiet = normalizeQuiet(in.Config.Quiet)
	}

	if in.Type == "" {
		in.Type = stored.Type
	}
	// An update goes through the same checks as a create, so a saved channel
	// can never hold a config the create form would have rejected.
	if fe := alert.ValidateConfig(in.Type, cfg); len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "validation error", fe)
		return
	}

	ch := &store.Channel{
		ID:        id,
		Name:      in.Name,
		Type:      in.Type,
		Config:    cfg,
		IsDefault: in.IsDefault,
	}
	if err := s.d.Store.UpdateChannel(r.Context(), ch); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "update failed", nil)
		return
	}
	_ = s.d.Alerts.ReloadChannels(r.Context())
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update", "channel", &id, ch.Name)
	writeJSON(w, http.StatusOK, toChannelOut(*ch))
}

func (s *server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if err := s.d.Store.DeleteChannel(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete failed", nil)
		return
	}
	_ = s.d.Alerts.ReloadChannels(r.Context())
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "delete", "channel", &id, "")
	w.WriteHeader(http.StatusNoContent)
}

// normalizeQuiet turns a disabled quiet-hours block into none, so a stored
// channel either has working quiet hours or no field at all.
func normalizeQuiet(q *store.QuietHours) *store.QuietHours {
	if q == nil || !q.Enabled {
		return nil
	}
	return q
}

// handleApplyChannelToAll attaches a channel to every monitor
// (POST /api/channels/{id}/apply-all). Monitors that already have it are left
// alone.
func (s *server) handleApplyChannelToAll(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if _, err := s.d.Store.GetChannel(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	n, err := s.d.Store.AssignChannelToAll(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "assign failed", nil)
		return
	}
	// Bring the engine's copies in line with the database.
	for _, m := range s.d.Engine.Monitors() {
		if !slices.Contains(m.ChannelIDs, id) {
			m.ChannelIDs = append(m.ChannelIDs, id)
			s.d.Engine.UpsertMonitor(m)
		}
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "apply_all", "channel", &id, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "assigned": n})
}

func (s *server) handleTestChannel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	ch, err := s.d.Store.GetChannel(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	if err := s.d.Alerts.SendTest(r.Context(), *ch); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
