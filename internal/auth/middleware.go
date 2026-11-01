package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"opskest/internal/config"
	"opskest/internal/logx"
	"opskest/internal/store"
)

// Auth wires session storage and config together to serve the logout handler
// and the request-scoped middleware that loads the signed-in user and
// enforces CSRF. Sign-in itself is the local-account flow in local.go.
type Auth struct {
	st    *store.Store
	cfg   *config.Config
	now   func() time.Time
	local localState
}

// New builds an Auth. now is injectable so tests can control session
// expiry without sleeping.
func New(st *store.Store, cfg *config.Config, now func() time.Time) *Auth {
	return &Auth{st: st, cfg: cfg, now: now}
}

type ctxKey int

const userCtxKey ctxKey = iota

func withUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

// UserFrom returns the signed-in user Auth.Middleware placed in ctx, if
// any.
func UserFrom(ctx context.Context) (*store.User, bool) {
	u, ok := ctx.Value(userCtxKey).(*store.User)
	return u, ok && u != nil
}

// WithoutUser returns ctx with any signed-in user removed, so a handler sees
// the request as anonymous.
func WithoutUser(ctx context.Context) context.Context {
	return context.WithValue(ctx, userCtxKey, (*store.User)(nil))
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// RequireUser answers 401 {"error":"unauthenticated"} unless
// Auth.Middleware found a signed-in user.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			writeJSONError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin answers 401 {"error":"unauthenticated"} with no session,
// and 403 {"error":"admin only"} for a signed-in non-admin.
func RequireAdmin(next http.Handler) http.Handler {
	return RequireRole(RoleAdmin)(next)
}

// Middleware loads the session named by the opskest.sid cookie, if any,
// places its *store.User in the request context, slides the session's
// expiry when it is getting close to expiring, and enforces the
// double-submit CSRF check on mutating /api requests. It never rejects a
// request for having no session: that is RequireUser's and RequireAdmin's
// job, layered on the specific routes that need it.
func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var sessionCSRF string

		if c, err := r.Cookie(CookieSession); err == nil {
			hash := hashToken(c.Value)
			sess, user, err := a.st.GetSession(ctx, hash, a.now())
			switch {
			case err == nil:
				ctx = withUser(ctx, user)
				sessionCSRF = sess.CSRF
				a.maybeTouch(ctx, hash, sess.ExpiresAt)
			case errors.Is(err, store.ErrNotFound):
				// No session, expired, or the user is now disabled:
				// proceed unauthenticated.
			default:
				logx.Err(slog.Default(), "auth: load session", err)
			}
		}

		// CSRF only applies to requests that carried a valid session:
		// an unauthenticated mutating request is rejected by RequireUser
		// downstream with 401, not here with 403.
		if sessionCSRF != "" && csrfRequired(r) && !checkCSRF(r, sessionCSRF) {
			writeJSONError(w, http.StatusForbidden, "csrf")
			return
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// maybeTouch slides a session's expiry forward once it has less than
// sessionRefreshAt remaining. No separate per-session rate limiter is
// needed to keep this rare: a touch resets the expiry to a full
// sessionTTL from now, and sessionTTL-sessionRefreshAt is a full day, so
// the very next request on the same session sees far more than
// sessionRefreshAt remaining and is skipped above. That naturally caps
// writes to about once a day per active session without holding any
// unbounded, never-pruned state in memory.
func (a *Auth) maybeTouch(ctx context.Context, tokenHash []byte, expiresAt time.Time) {
	now := a.now()
	if expiresAt.Sub(now) >= sessionRefreshAt {
		return
	}
	if err := a.st.TouchSession(ctx, tokenHash, now.Add(sessionTTL)); err != nil && !errors.Is(err, store.ErrNotFound) {
		logx.Err(slog.Default(), "auth: touch session", err)
	}
}

// MintSession creates a new session for userID and returns its cookie
// token and CSRF value. Used by the local sign-in and setup handlers, and by anything that
// needs to sign a test user in directly.
func (a *Auth) MintSession(ctx context.Context, userID int64) (token, csrf string, err error) {
	token, err = randomToken(32)
	if err != nil {
		return "", "", fmt.Errorf("auth: generate token: %w", err)
	}
	csrf, err = randomToken(32)
	if err != nil {
		return "", "", fmt.Errorf("auth: generate csrf: %w", err)
	}
	if err := a.st.CreateSession(ctx, hashToken(token), userID, csrf, a.now().Add(sessionTTL)); err != nil {
		return "", "", fmt.Errorf("auth: create session: %w", err)
	}
	return token, csrf, nil
}

// HandleLogout deletes the session and clears cookies: POST /api/auth/logout.
func (a *Auth) HandleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieSession); err == nil {
		if err := a.st.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			logx.Err(slog.Default(), "auth: delete session", err)
		}
	}
	clearCookie(w, a.cfg, CookieSession)
	clearCookie(w, a.cfg, CookieCSRF)
	w.WriteHeader(http.StatusNoContent)
}
