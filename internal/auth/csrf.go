package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// HeaderCSRF is the request header the SPA copies the opskest.csrf cookie
// value into on every mutating /api request (double-submit CSRF).
const HeaderCSRF = "X-CSRF-Token"

// csrfRequired reports whether r is a mutating /api request that must
// carry a matching X-CSRF-Token header. GET/HEAD requests and anything
// outside /api are exempt.
func csrfRequired(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	return strings.HasPrefix(r.URL.Path, "/api/")
}

// checkCSRF reports whether r's X-CSRF-Token header matches sessionCSRF,
// using a constant-time comparison.
func checkCSRF(r *http.Request, sessionCSRF string) bool {
	header := r.Header.Get(HeaderCSRF)
	return subtle.ConstantTimeCompare([]byte(header), []byte(sessionCSRF)) == 1
}
