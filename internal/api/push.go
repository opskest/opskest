package api

import (
	"net/http"
)

// handlePush serves GET|POST /push/{token}. Public, rate-limited at 1 req/s
// per token. An unknown token → 404.
func (s *server) handlePush(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}

	// Look up the monitor by push token.
	_, err := s.d.Store.MonitorByPushToken(r.Context(), token)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}

	// Rate limit: 1 req/s per token. Even when rate-limited, return 200
	// (the push was "accepted" in the sense that it wasn't an error), but
	// don't forward to the engine.
	if !s.pushLimit.allow(token, s.d.Now()) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	status := r.URL.Query().Get("status")
	msg := r.URL.Query().Get("msg")
	ping := queryInt(r, "ping", 0)

	if status == "" {
		status = "up"
	}

	s.d.Engine.Push(token, status, msg, ping)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
