package api

import (
	"net/http"
	"strings"

	"opskest/internal/auth"
	"opskest/internal/store"
)

const errTwoFactorRequired = "two-factor authentication required"

// twoFactorGate enforces the workspace policy "require two-factor
// authentication". While it is on, a signed-in user without an authenticator
// app can reach only what is needed to set one up (sign-in state, their own
// account, and for an administrator the settings that switch the policy off
// again, so a lost device can never lock everyone out). Public status page
// endpoints keep working but treat that user as anonymous, so a private page
// stays private.
func (s *server) twoFactorGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requireTwoFactor.Load() || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		u, ok := auth.UserFrom(r.Context())
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		enrolled, err := s.d.Store.HasTwoFactor(r.Context(), u.ID)
		if err != nil || enrolled {
			// A read error fails open: a database hiccup must not lock the
			// whole team out. The next request checks again.
			next.ServeHTTP(w, r)
			return
		}
		path := r.URL.Path
		switch {
		case strings.HasPrefix(path, "/api/auth/"), path == "/api/me", path == "/api/runtime":
			next.ServeHTTP(w, r)
		case path == "/api/settings" && u.Role == store.RoleAdmin:
			next.ServeHTTP(w, r)
		case strings.HasPrefix(path, "/api/public/"):
			next.ServeHTTP(w, r.WithContext(auth.WithoutUser(r.Context())))
		default:
			writeJSON(w, http.StatusForbidden, map[string]string{"error": errTwoFactorRequired, "code": "two_factor_required"})
		}
	})
}

// keepTwoFactor refuses to switch an authenticator off while the policy
// requires one.
func (s *server) keepTwoFactor(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.requireTwoFactor.Load() {
			writeErr(w, http.StatusConflict, "two-factor authentication is required by your administrator", nil)
			return
		}
		next(w, r)
	}
}

// handleResetTwoFactor removes another user's authenticator app and recovery
// codes (a lost device). With the policy on they are asked to set it up again
// at their next request.
func (s *server) handleResetTwoFactor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id", nil)
		return
	}
	me, _ := auth.UserFrom(r.Context())
	if me != nil && me.ID == id {
		writeErr(w, http.StatusBadRequest, "use your own account security page to change your two-factor settings", nil)
		return
	}
	if _, err := s.d.Store.GetUser(r.Context(), id); err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	if err := s.d.Store.DisableTOTP(r.Context(), id); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not reset two-factor", nil)
		return
	}
	if me != nil {
		s.d.Store.Audit(r.Context(), &me.ID, "reset_2fa", "user", &id, "")
	}
	w.WriteHeader(http.StatusNoContent)
}
