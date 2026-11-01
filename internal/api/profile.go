package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"opskest/internal/auth"
	"opskest/internal/model"
)

// This endpoint deliberately accepts presentation fields only. Administrative
// account updates and credential changes have separate, protected endpoints.
func (s *server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid profile JSON", nil)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeErr(w, http.StatusBadRequest, "unexpected trailing content", nil)
		return
	}
	name := strings.TrimSpace(input.Name)
	invalid := !utf8.ValidString(input.Name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 100
	for _, character := range input.Name {
		if unicode.IsControl(character) {
			invalid = true
		}
	}
	if invalid {
		writeErr(w, http.StatusBadRequest, "invalid display name", model.FieldErrors{"name": "Use 1–100 characters without control characters."})
		return
	}
	user, _ := auth.UserFrom(r.Context())
	if err := s.d.Store.UpdateDisplayName(r.Context(), user.ID, name); err != nil {
		writeErr(w, http.StatusInternalServerError, "profile update failed", nil)
		return
	}
	s.d.Store.Audit(r.Context(), &user.ID, "update_profile", "user", &user.ID, "Display name updated")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
}
