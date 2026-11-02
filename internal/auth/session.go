package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"time"

	"opskest/internal/config"
)

// Cookie names (these are namespaced and must
// not change).
const (
	CookieSession = "opskest.sid"  // HttpOnly session token
	CookieCSRF    = "opskest.csrf" // readable by JS; copied into HeaderCSRF
)

const (
	// sessionTTL is the sliding session lifetime.
	sessionTTL = 30 * 24 * time.Hour
	// sessionRefreshAt is the remaining-lifetime threshold below which a
	// session's expiry is slid forward again. Keeping this a day short of
	// sessionTTL means an active session is never touched more than about
	// once a day: see the comment on Auth.maybeTouch.
	sessionRefreshAt = 29 * 24 * time.Hour
)

// randomToken returns n cryptographically random bytes, base64url-encoded
// with no padding.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is the SHA-256 of a session token: what the database actually
// stores. The raw token itself never touches the database.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// setCookie sets name=value scoped to Path=/, SameSite=Lax, Secure from
// cfg.SecureCookies, expiring after ttl.
func setCookie(w http.ResponseWriter, cfg *config.Config, name, value string, ttl time.Duration, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: httpOnly,
		Secure:   cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

// clearCookie deletes name by issuing an immediately-expiring, empty
// replacement with the same scope.
func clearCookie(w http.ResponseWriter, cfg *config.Config, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}
