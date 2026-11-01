package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"opskest/internal/config"
	"opskest/internal/secret"
	"opskest/internal/store"
)

// -- test helpers ------------------------------------------------------

// testKey is a fixed, obviously-not-secret AES-256 key.
func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = 0x01
	}
	return key
}

// newStore opens a Store backed by a fresh temp directory, cleaned up
// automatically when the test ends. store.newTestStore is package-private
// to the store package, so auth's tests build their own.
func newStore(t *testing.T) *store.Store {
	t.Helper()
	box, err := secret.New(testKey())
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	s, err := store.Open(t.TempDir(), box, time.UTC)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	return s
}

func testConfig() *config.Config {
	return &config.Config{
		AppURL:        "https://uptime.example.com",
		SecureCookies: true,
	}
}

// -- middleware: CSRF ------------------------------------------------------

func TestMiddlewareCSRF(t *testing.T) {
	st := newStore(t)
	cfg := testConfig()
	a := New(st, cfg, time.Now)
	ctx := context.Background()

	u, err := st.CreateUser(ctx, "user@example.com", "User", store.RoleViewer, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	token, csrf, err := a.MintSession(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := a.Middleware(next)

	// POST with a session but no header -> 403 {"error":"csrf"}.
	called = false
	req := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req.AddCookie(&http.Cookie{Name: CookieSession, Value: token})
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no header: status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if called {
		t.Fatal("no header: handler must not run")
	}
	if body := rec.Body.String(); body != `{"error":"csrf"}`+"\n" {
		t.Fatalf("no header: body = %q", body)
	}

	// POST with a session and the matching header -> passes.
	called = false
	req2 := httptest.NewRequest(http.MethodPost, "/api/x", nil)
	req2.AddCookie(&http.Cookie{Name: CookieSession, Value: token})
	req2.Header.Set(HeaderCSRF, csrf)
	rec2 := httptest.NewRecorder()
	mw.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK || !called {
		t.Fatalf("with header: status = %d called = %v", rec2.Code, called)
	}

	// GET with a session and no header -> passes.
	called = false
	req3 := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req3.AddCookie(&http.Cookie{Name: CookieSession, Value: token})
	rec3 := httptest.NewRecorder()
	mw.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK || !called {
		t.Fatalf("GET: status = %d called = %v", rec3.Code, called)
	}
}

// -- middleware: sliding session expiry -------------------------------------

func TestSessionSliding(t *testing.T) {
	st := newStore(t)
	cfg := testConfig()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	a := New(st, cfg, now)
	ctx := context.Background()

	u, err := st.CreateUser(ctx, "user@example.com", "User", store.RoleViewer, clock)
	if err != nil {
		t.Fatal(err)
	}

	mkSession := func(remaining time.Duration) (token string, hash []byte) {
		t.Helper()
		tok, err := randomToken(32)
		if err != nil {
			t.Fatal(err)
		}
		hash = hashToken(tok)
		if err := st.CreateSession(ctx, hash, u.ID, "csrf-"+tok[:8], clock.Add(remaining)); err != nil {
			t.Fatal(err)
		}
		return tok, hash
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mw := a.Middleware(next)

	hit := func(token string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		req.AddCookie(&http.Cookie{Name: CookieSession, Value: token})
		mw.ServeHTTP(httptest.NewRecorder(), req)
	}

	// 10 days left: touched, extended to a full 30 days from now.
	tok10, hash10 := mkSession(10 * 24 * time.Hour)
	hit(tok10)
	sess, _, err := st.GetSession(ctx, hash10, clock)
	if err != nil {
		t.Fatal(err)
	}
	touchedExpiry := clock.Add(sessionTTL)
	if !sess.ExpiresAt.Equal(touchedExpiry) {
		t.Fatalf("10 days left: expiresAt = %v, want %v", sess.ExpiresAt, touchedExpiry)
	}

	// 29.99 days left: left untouched.
	almost := 29*24*time.Hour + 23*time.Hour + 45*time.Minute // > 29 days remaining
	tokAlmost, hashAlmost := mkSession(almost)
	hit(tokAlmost)
	sess2, _, err := st.GetSession(ctx, hashAlmost, clock)
	if err != nil {
		t.Fatal(err)
	}
	if want := clock.Add(almost); !sess2.ExpiresAt.Equal(want) {
		t.Fatalf("29.99 days left: expiresAt = %v, want unchanged %v", sess2.ExpiresAt, want)
	}

	// A just-touched session isn't touched again on the very next
	// request: a touch resets expiry to a full 30 days out, so a request
	// a couple of hours later still sees far more than 29 days
	// remaining and must leave the expiry exactly as the first touch
	// left it (no unbounded per-session throttle state needed for this).
	clock = clock.Add(2 * time.Hour)
	hit(tok10)
	sess3, _, err := st.GetSession(ctx, hash10, clock)
	if err != nil {
		t.Fatal(err)
	}
	if !sess3.ExpiresAt.Equal(touchedExpiry) {
		t.Fatalf("just-touched session touched again: expiresAt = %v, want unchanged %v", sess3.ExpiresAt, touchedExpiry)
	}
}

// -- guards ------------------------------------------------------------

func TestRequireAdmin(t *testing.T) {
	st := newStore(t)
	cfg := testConfig()
	a := New(st, cfg, time.Now)
	ctx := context.Background()

	viewer, err := st.CreateUser(ctx, "viewer@example.com", "V", store.RoleViewer, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	admin, err := st.CreateUser(ctx, "admin2@example.com", "A", store.RoleViewer, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserRole(ctx, admin.ID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	viewerTok, _, err := a.MintSession(ctx, viewer.ID)
	if err != nil {
		t.Fatal(err)
	}
	adminTok, _, err := a.MintSession(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := a.Middleware(RequireAdmin(next))

	cases := []struct {
		name   string
		cookie *http.Cookie
		want   int
	}{
		{"no session", nil, http.StatusUnauthorized},
		{"viewer", &http.Cookie{Name: CookieSession, Value: viewerTok}, http.StatusForbidden},
		{"admin", &http.Cookie{Name: CookieSession, Value: adminTok}, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
			if c.cookie != nil {
				req.AddCookie(c.cookie)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
		})
	}
}

// -- logout --------------------------------------------------------------

func TestLogout(t *testing.T) {
	st := newStore(t)
	cfg := testConfig()
	a := New(st, cfg, time.Now)
	ctx := context.Background()

	u, err := st.CreateUser(ctx, "user@example.com", "U", store.RoleViewer, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := a.MintSession(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: CookieSession, Value: tok})
	rec := httptest.NewRecorder()
	a.HandleLogout(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	if _, _, err := st.GetSession(ctx, hashToken(tok), time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session row still present: err = %v", err)
	}
}
