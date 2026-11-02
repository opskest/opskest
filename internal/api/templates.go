package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"opskest/internal/auth"
	"opskest/internal/model"
	"opskest/internal/store"
)

// Monitor templates are saved starting points, made by the people who run
// the workspace because every service has different needs. A template holds
// the monitor form's settings and nothing identifying or secret.

const maxTemplateBytes = 64 * 1024

type templateOut struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Type        string          `json:"type"`
	Config      json.RawMessage `json:"config"`
	CreatedAt   int64           `json:"createdAt"`
	UpdatedAt   int64           `json:"updatedAt"`
}

func toTemplateOut(t store.Template) templateOut {
	return templateOut{t.ID, t.Name, t.Description, t.Type, t.Config, t.CreatedAt.UnixMilli(), t.UpdatedAt.UnixMilli()}
}

// templateStrip lists the top-level fields a template never keeps: who the
// monitor is, where it points, and anything secret.
var templateStrip = []string{"id", "name", "target", "groupId", "secrets", "pushToken", "createdAt", "updatedAt"}

// cleanTemplateConfig returns raw as a JSON object without the fields a
// template must not carry: identity, target, group, secrets, secret header
// values and parent monitors (ids only mean something in this workspace's
// monitor list). The monitor type must be one the server knows.
func cleanTemplateConfig(raw json.RawMessage) (json.RawMessage, model.MonitorType, error) {
	if len(raw) == 0 || len(raw) > maxTemplateBytes {
		return nil, "", errors.New("config must be a JSON object under 64 KiB")
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil || cfg == nil {
		return nil, "", errors.New("config must be a JSON object")
	}
	for _, k := range templateStrip {
		delete(cfg, k)
	}
	if hs, ok := cfg["headers"].([]any); ok {
		kept := make([]any, 0, len(hs))
		for _, h := range hs {
			if hm, ok := h.(map[string]any); ok && hm["secret"] != true {
				kept = append(kept, h)
			}
		}
		cfg["headers"] = kept
	}
	if st, ok := cfg["settings"].(map[string]any); ok {
		delete(st, "dependsOn")
	}
	typ, _ := cfg["type"].(string)
	if !model.MonitorType(typ).Known() {
		return nil, "", errors.New("unknown monitor type")
	}
	out, err := json.Marshal(cfg)
	if err != nil {
		return nil, "", err
	}
	return out, model.MonitorType(typ), nil
}

type templateIn struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Config      json.RawMessage `json:"config"`
}

func (in *templateIn) normalize() (model.FieldErrors, json.RawMessage, model.MonitorType) {
	fe := model.FieldErrors{}
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 80 {
		fe["name"] = "must be 1-80 characters"
	}
	if utf8.RuneCountInString(in.Description) > 300 {
		fe["description"] = "must be at most 300 characters"
	}
	cfg, typ, err := cleanTemplateConfig(in.Config)
	if err != nil {
		fe["config"] = err.Error()
	}
	return fe, cfg, typ
}

func (s *server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.d.Store.ListTemplates(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	out := make([]templateOut, 0, len(list))
	for _, t := range list {
		out = append(out, toTemplateOut(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": out})
}

func (s *server) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var in templateIn
	if err := decode(r, &in, maxTemplateBytes+4096); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	fe, cfg, typ := in.normalize()
	if len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "validation error", fe)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	t := &store.Template{Name: in.Name, Description: in.Description, Type: string(typ), Config: cfg}
	if err := s.d.Store.CreateTemplate(r.Context(), t, &u.ID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "a template with that name already exists", model.FieldErrors{"name": "already in use"})
			return
		}
		writeErr(w, http.StatusInternalServerError, "create failed", nil)
		return
	}
	s.d.Store.Audit(r.Context(), &u.ID, "create", "template", &t.ID, t.Name)
	writeJSON(w, http.StatusCreated, toTemplateOut(*t))
}

func (s *server) handleUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	var in templateIn
	if err := decode(r, &in, maxTemplateBytes+4096); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	fe, cfg, typ := in.normalize()
	if len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "validation error", fe)
		return
	}
	t := &store.Template{ID: id, Name: in.Name, Description: in.Description, Type: string(typ), Config: cfg}
	if err := s.d.Store.UpdateTemplate(r.Context(), t); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErr(w, http.StatusNotFound, "not found", nil)
		case errors.Is(err, store.ErrConflict):
			writeErr(w, http.StatusConflict, "a template with that name already exists", model.FieldErrors{"name": "already in use"})
		default:
			writeErr(w, http.StatusInternalServerError, "update failed", nil)
		}
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update", "template", &id, t.Name)
	got, err := s.d.Store.GetTemplate(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, toTemplateOut(*got))
}

func (s *server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if err := s.d.Store.DeleteTemplate(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "delete", "template", &id, "")
	w.WriteHeader(http.StatusNoContent)
}
