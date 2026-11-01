package auth

import (
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"opskest/internal/store"
)

const localTestPassword = "a long test password only"

func localAuthRouter(a *Auth) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/status", a.HandleStatus)
	mux.HandleFunc("POST /api/auth/setup", a.HandleSetup)
	mux.HandleFunc("POST /api/auth/login", a.HandlePasswordLogin)
	mux.Handle("GET /api/auth/security", RequireUser(http.HandlerFunc(a.HandleSecurity)))
	mux.Handle("POST /api/auth/2fa/enroll", RequireUser(http.HandlerFunc(a.HandleEnrollTOTP)))
	mux.Handle("POST /api/auth/2fa/enable", RequireUser(http.HandlerFunc(a.HandleEnableTOTP)))
	mux.Handle("POST /api/auth/2fa/disable", RequireUser(http.HandlerFunc(a.HandleDisableTOTP)))
	mux.Handle("POST /api/auth/2fa/recovery", RequireUser(http.HandlerFunc(a.HandleRecoveryCodes)))
	mux.Handle("POST /api/auth/password", RequireUser(http.HandlerFunc(a.HandleChangePassword)))
	return a.Middleware(mux)
}
func authRequest(t *testing.T, h http.Handler, path string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	method := http.MethodPost
	var b []byte
	if body == nil {
		method = http.MethodGet
	} else {
		var err error
		b, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		r.AddCookie(c)
		if c.Name == CookieCSRF {
			r.Header.Set(HeaderCSRF, c.Value)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func setupLocalTest(t *testing.T, a *Auth) (http.Handler, []*http.Cookie) {
	t.Helper()
	h := localAuthRouter(a)
	r := authRequest(t, h, "/api/auth/setup", map[string]string{"username": "operator", "password": localTestPassword, "name": "Operator"}, nil)
	if r.Code != 200 {
		t.Fatalf("setup: %d %s", r.Code, r.Body.String())
	}
	return h, r.Result().Cookies()
}
func TestLocalPasswordHashAndTOTPVector(t *testing.T) {
	one, err := HashPassword(localTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	two, err := HashPassword(localTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if one == two || !verifyPassword(localTestPassword, one) || verifyPassword("wrong", one) || verifyPassword(localTestPassword, "") {
		t.Fatal("password hash salt or verification failed")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if got := totpCode([]byte("12345678901234567890"), 1); got != "287082" {
		t.Fatalf("RFC4226 counter1: %s", got)
	}
}
func TestLocalSetupLoginAndOrigin(t *testing.T) {
	st := newStore(t)
	a := New(st, testConfig(), time.Now)
	h, cookies := setupLocalTest(t, a)
	status := authRequest(t, h, "/api/auth/status", nil, nil)
	if status.Code != 200 || !bytes.Contains(status.Body.Bytes(), []byte(`"setupRequired":false`)) {
		t.Fatal(status.Body.String())
	}
	duplicate := authRequest(t, h, "/api/auth/setup", map[string]string{"username": "other", "password": localTestPassword}, nil)
	if duplicate.Code != 409 {
		t.Fatal(duplicate.Code)
	}
	login := authRequest(t, h, "/api/auth/login", map[string]string{"username": "OPERATOR", "password": localTestPassword}, nil)
	if login.Code != 200 || len(login.Result().Cookies()) != 2 {
		t.Fatal(login.Code, login.Body.String())
	}
	wrong := authRequest(t, h, "/api/auth/login", map[string]string{"username": "operator", "password": "wrong"}, nil)
	if wrong.Code != 401 {
		t.Fatal(wrong.Code)
	}
	r := httptest.NewRequest("POST", "/api/auth/login", bytes.NewBufferString(`{"username":"operator","password":"a long test password only"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://evil.invalid")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatal("cross-origin login accepted")
	}
	r = httptest.NewRequest("POST", "/api/auth/2fa/enroll", bytes.NewBufferString(`{"password":"a long test password only"}`))
	r.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	c, err := st.CredentialsByUsername(context.Background(), "operator")
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.GetUser(context.Background(), c.UserID)
	if err != nil || u.Role != store.RoleAdmin {
		t.Fatal("bootstrap user not admin", err)
	}
}
func TestLocalSetupRace(t *testing.T) {
	a := New(newStore(t), testConfig(), time.Now)
	h := localAuthRouter(a)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, username := range []string{"first", "second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := authRequest(t, h, "/api/auth/setup", map[string]string{"username": username, "password": localTestPassword}, nil)
			codes <- r.Code
		}()
	}
	wg.Wait()
	close(codes)
	success, conflict := 0, 0
	for code := range codes {
		if code == 200 {
			success++
		}
		if code == 409 {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}
func TestLocalTOTPAndRecoveryLifecycle(t *testing.T) {
	now := time.Unix(1800000000, 0)
	st := newStore(t)
	a := New(st, testConfig(), func() time.Time { return now })
	h, cookies := setupLocalTest(t, a)
	enroll := authRequest(t, h, "/api/auth/2fa/enroll", map[string]string{"password": localTestPassword}, cookies)
	if enroll.Code != 200 {
		t.Fatal(enroll.Code, enroll.Body.String())
	}
	var enrollment struct{ Secret, OTPAuthURL string }
	if err := json.Unmarshal(enroll.Body.Bytes(), &enrollment); err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrollment.Secret)
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.CredentialsByUsername(context.Background(), "operator")
	if err != nil || bytes.Equal(c.TOTPPending, secret) {
		t.Fatal("TOTP secret not encrypted", err)
	}
	enable := authRequest(t, h, "/api/auth/2fa/enable", map[string]string{"code": totpCode(secret, now.Unix()/30)}, cookies)
	if enable.Code != 200 {
		t.Fatal(enable.Code, enable.Body.String())
	}
	var recovery struct{ RecoveryCodes []string }
	if err := json.Unmarshal(enable.Body.Bytes(), &recovery); err != nil || len(recovery.RecoveryCodes) != 10 {
		t.Fatal("recovery codes missing", err)
	}
	login := func(code string) *httptest.ResponseRecorder {
		return authRequest(t, h, "/api/auth/login", map[string]string{"username": "operator", "password": localTestPassword, "code": code}, nil)
	}
	missing := login("")
	if missing.Code != 200 || !bytes.Contains(missing.Body.Bytes(), []byte(`"requiresTwoFactor":true`)) || len(missing.Result().Cookies()) != 0 {
		t.Fatal("2FA not required")
	}
	replay := login(totpCode(secret, now.Unix()/30))
	if replay.Code != 401 {
		t.Fatal("enrollment code replay accepted")
	}
	now = now.Add(30 * time.Second)
	good := login(totpCode(secret, now.Unix()/30))
	if good.Code != 200 || len(good.Result().Cookies()) != 2 {
		t.Fatal("TOTP login failed", good.Body.String())
	}
	replay = login(totpCode(secret, now.Unix()/30))
	if replay.Code != 401 {
		t.Fatal("login code replay accepted")
	}
	good = login(recovery.RecoveryCodes[0])
	if good.Code != 200 {
		t.Fatal("recovery failed", good.Body.String())
	}
	replay = login(recovery.RecoveryCodes[0])
	if replay.Code != 401 {
		t.Fatal("recovery code replay accepted")
	}
	disabled := authRequest(t, h, "/api/auth/2fa/disable", map[string]string{"password": localTestPassword, "code": recovery.RecoveryCodes[1]}, cookies)
	if disabled.Code != 200 {
		t.Fatal(disabled.Code, disabled.Body.String())
	}
	good = login("")
	if good.Code != 200 || len(good.Result().Cookies()) != 2 {
		t.Fatal("password login after disable failed")
	}
}
func TestLocalPasswordChangeRevokesSessions(t *testing.T) {
	a := New(newStore(t), testConfig(), time.Now)
	h, cookies := setupLocalTest(t, a)
	changed := authRequest(t, h, "/api/auth/password", map[string]string{"currentPassword": localTestPassword, "newPassword": "a different long password"}, cookies)
	if changed.Code != 200 {
		t.Fatal(changed.Code, changed.Body.String())
	}
	oldSession := authRequest(t, h, "/api/auth/security", nil, cookies)
	if oldSession.Code != 401 {
		t.Fatal("old session survived password change")
	}
	newSession := authRequest(t, h, "/api/auth/security", nil, changed.Result().Cookies())
	if newSession.Code != 200 {
		t.Fatal("new session missing")
	}
	bad := authRequest(t, h, "/api/auth/login", map[string]string{"username": "operator", "password": localTestPassword}, nil)
	if bad.Code != 401 {
		t.Fatal("old password still works")
	}
	good := authRequest(t, h, "/api/auth/login", map[string]string{"username": "operator", "password": "a different long password"}, nil)
	if good.Code != 200 {
		t.Fatal("new password failed")
	}
}
func TestLocalRateLimitAndPendingExpiry(t *testing.T) {
	now := time.Unix(1800000000, 0)
	a := New(newStore(t), testConfig(), func() time.Time { return now })
	h, cookies := setupLocalTest(t, a)
	enroll := authRequest(t, h, "/api/auth/2fa/enroll", map[string]string{"password": localTestPassword}, cookies)
	if enroll.Code != 200 {
		t.Fatal(enroll.Body.String())
	}
	now = now.Add(11 * time.Minute)
	expired := authRequest(t, h, "/api/auth/2fa/enable", map[string]string{"code": "123456"}, cookies)
	if expired.Code != 400 {
		t.Fatal("expired enrollment accepted")
	}
	req := httptest.NewRequest("POST", "/api/auth/login", nil)
	req.RemoteAddr = "192.0.2.1:1"
	for i := 0; i < 15; i++ {
		if !a.allowAttempt(httptest.NewRecorder(), req, "limited") {
			t.Fatal("early rate limit")
		}
	}
	w := httptest.NewRecorder()
	if a.allowAttempt(w, req, "limited") || w.Code != 429 {
		t.Fatal("username not rate limited")
	}
}

func TestSameHostLoopbackAliases(t *testing.T) {
	u := func(s string) *url.URL { v, _ := url.Parse(s); return v }
	cases := []struct {
		got, configured string
		want            bool
	}{
		{"http://localhost:8085", "http://localhost:8085", true},
		{"http://127.0.0.1:8085", "http://localhost:8085", true},
		{"http://[::1]:8085", "http://localhost:8085", true},
		{"http://localhost:8085", "http://127.0.0.1:8085", true},
		{"http://localhost:9999", "http://localhost:8085", false},      // other port
		{"http://evil.example", "http://localhost:8085", false},        // not loopback
		{"http://127.0.0.1:8085", "https://status.example.com", false}, // configured host is public
		{"http://status.example.com", "http://localhost", false},
		{"http://localhost", "http://127.0.0.1", true},
	}
	for _, c := range cases {
		if got := sameHost(u(c.got), u(c.configured)); got != c.want {
			t.Errorf("sameHost(%s, %s) = %v, want %v", c.got, c.configured, got, c.want)
		}
	}
}
