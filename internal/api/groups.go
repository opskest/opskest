package api

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"opskest/internal/auth"
	"opskest/internal/model"
	"opskest/internal/store"
)

// ── Groups ─────────────────────────────────────────────────────────

func (s *server) handleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.d.Store.ListGroups(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	if groups == nil {
		groups = []store.Group{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (s *server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Sort        *int   `json:"sort"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required", nil)
		return
	}
	g := &store.Group{Name: in.Name, Description: in.Description}
	if in.Sort != nil {
		g.Sort = *in.Sort
	} else if groups, err := s.d.Store.ListGroups(r.Context()); err == nil {
		g.Sort = len(groups) // new groups go last
	}
	if err := s.d.Store.CreateGroup(r.Context(), g); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "name already taken", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "create failed", nil)
		return
	}
	_ = s.d.Engine.ReloadGroups(r.Context())
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "create", "group", &g.ID, g.Name)
	writeJSON(w, http.StatusCreated, g)
}

func (s *server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required", nil)
		return
	}
	g := &store.Group{ID: id, Name: in.Name, Description: in.Description}
	if err := s.d.Store.UpdateGroup(r.Context(), g); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "name already taken", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "update failed", nil)
		return
	}
	_ = s.d.Engine.ReloadGroups(r.Context())
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update", "group", &id, g.Name)
	writeJSON(w, http.StatusOK, g)
}

func (s *server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if err := s.d.Store.DeleteGroup(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete failed", nil)
		return
	}
	_ = s.d.Engine.ReloadGroups(r.Context())
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "delete", "group", &id, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleReorderGroups(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if err := s.d.Store.ReorderGroups(r.Context(), in.IDs); err != nil {
		writeErr(w, http.StatusInternalServerError, "reorder failed", nil)
		return
	}
	_ = s.d.Engine.ReloadGroups(r.Context())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ── Tags ───────────────────────────────────────────────────────────

func (s *server) handleListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.d.Store.ListTags(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (s *server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	name, color, fe := cleanTag(in.Name, in.Color)
	if len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid tag", fe)
		return
	}
	tag := &store.Tag{Name: name, Color: color}
	if err := s.d.Store.CreateTag(r.Context(), tag); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "name already taken", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "create failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "create", "tag", &tag.ID, tag.Name)
	writeJSON(w, http.StatusCreated, tag)
}

func (s *server) handleUpdateTag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	var in struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := decode(r, &in, 0); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	name, color, fe := cleanTag(in.Name, in.Color)
	if len(fe) > 0 {
		writeErr(w, http.StatusBadRequest, "invalid tag", fe)
		return
	}
	tag := &store.Tag{ID: id, Name: name, Color: color}
	if err := s.d.Store.UpdateTag(r.Context(), tag); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		if errors.Is(err, store.ErrConflict) {
			writeErr(w, http.StatusConflict, "name already taken", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "update failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "update", "tag", &id, tag.Name)
	writeJSON(w, http.StatusOK, tag)
}

func (s *server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	if err := s.d.Store.DeleteTag(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		writeErr(w, http.StatusInternalServerError, "delete failed", nil)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	s.d.Store.Audit(r.Context(), &u.ID, "delete", "tag", &id, "")
	w.WriteHeader(http.StatusNoContent)
}

var tagColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// cleanTag trims a tag's name and checks both fields. An empty colour is
// allowed and means the default.
func cleanTag(name, color string) (string, string, model.FieldErrors) {
	fe := model.FieldErrors{}
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		fe["name"] = "required"
	case utf8.RuneCountInString(name) > 40:
		fe["name"] = "must be at most 40 characters"
	case strings.ContainsFunc(name, unicode.IsControl):
		fe["name"] = "must not contain control characters"
	}
	if color != "" && !tagColorRE.MatchString(color) {
		fe["color"] = "use #rrggbb"
	}
	return name, color, fe
}
