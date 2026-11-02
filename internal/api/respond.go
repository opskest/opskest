// Package api is the HTTP API server for opskest.
package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"opskest/internal/model"
)

const maxBodyDefault = 1 << 20 // 1 MiB

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr writes a JSON error response.
func writeErr(w http.ResponseWriter, status int, msg string, fields model.FieldErrors) {
	resp := map[string]any{"error": msg}
	if len(fields) > 0 {
		resp["fields"] = fields
	}
	writeJSON(w, status, resp)
}

// decode reads the request body (up to maxBytes) and unmarshals it into v.
// Unknown JSON fields are ignored rather than rejected: a UI sending one
// extra field must never make a form impossible to save.
func decode(r *http.Request, v any, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = maxBodyDefault
	}
	body := http.MaxBytesReader(nil, r.Body, maxBytes)
	defer body.Close()

	dec := json.NewDecoder(body)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	// Reject bodies with trailing content.
	if dec.More() {
		return fmt.Errorf("unexpected trailing content")
	}
	return nil
}

// pathID extracts the {id} path value from r and parses it as int64.
func pathID(r *http.Request, name string) (int64, error) {
	s := r.PathValue(name)
	if s == "" {
		return 0, fmt.Errorf("missing path parameter %q", name)
	}
	return strconv.ParseInt(s, 10, 64)
}

// queryInt reads a query parameter as int, defaulting to def.
func queryInt(r *http.Request, name string, def int) int {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// queryInt64 reads a query parameter as int64, defaulting to def.
func queryInt64(r *http.Request, name string, def int64) int64 {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// clampLimit enforces default 50, max 200.
func clampLimit(n int) int {
	if n <= 0 {
		return 50
	}
	if n > 200 {
		return 200
	}
	return n
}

// discardBody drains and closes r.Body. Used by handlers that don't need the body.
func discardBody(r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	r.Body.Close()
}
