package auth

import (
	"encoding/base32"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"opskest/internal/store"
)

type attemptWindow struct {
	count int
	until time.Time
}
type localState struct {
	mu       sync.Mutex
	attempts map[string]attemptWindow
	// Serializes credential-changing operations and code consumption within a process.
	securityMu sync.Mutex
}

func authJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func readAuthJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeJSONError(w, 415, "use application/json")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if err = d.Decode(v); err != nil {
		writeJSONError(w, 400, "invalid request")
		return false
	}
	if err = d.Decode(new(any)); err != io.EOF {
		writeJSONError(w, 400, "invalid request")
		return false
	}
	return true
}

// Auth POSTs need explicit same-origin protection before there is a session.
// Origin may be absent for native clients, but JSON disallows cross-site form posts.
func (a *Auth) sameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeJSONError(w, 403, "cross-site request rejected")
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		configured, err := url.Parse(a.cfg.AppURL)
		got, e := url.Parse(origin)
		if err != nil || e != nil || got.Scheme != configured.Scheme || !sameHost(got, configured) {
			writeJSONError(w, 403, "cross-site request rejected")
			return false
		}
	}
	return true
}

// sameHost reports whether two origins name the same host and port. When the
// configured address is a loopback one (localhost, 127.0.0.1, [::1]), the other
// loopback spellings with the same port count as the same place: someone who
// opens 127.0.0.1:8085 for an install configured as localhost:8085 is on the
// same machine and port, and refusing them only produces a confusing 403.
func sameHost(got, configured *url.URL) bool {
	if strings.EqualFold(got.Host, configured.Host) {
		return true
	}
	return isLoopbackName(configured.Hostname()) && isLoopbackName(got.Hostname()) && got.Port() == configured.Port()
}

func isLoopbackName(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *Auth) allowAttempt(w http.ResponseWriter, r *http.Request, username string) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	// Trust only the socket peer; forwarded headers are forgeable without an explicit proxy trust policy.
	keys := []string{"ip:" + host}
	limits := []int{60}
	if username != "" {
		keys = append(keys, "user:"+username)
		limits = append(limits, 15)
	}
	a.local.mu.Lock()
	defer a.local.mu.Unlock()
	now := a.now()
	if a.local.attempts == nil {
		a.local.attempts = make(map[string]attemptWindow)
	}
	for key, win := range a.local.attempts {
		if !now.Before(win.until) {
			delete(a.local.attempts, key)
		}
	}
	if len(a.local.attempts) > 10000 {
		w.Header().Set("Retry-After", "600")
		writeJSONError(w, 429, "too many attempts; try again later")
		return false
	}
	for i, key := range keys {
		if a.local.attempts[key].count >= limits[i] {
			w.Header().Set("Retry-After", "600")
			writeJSONError(w, 429, "too many attempts; try again later")
			return false
		}
	}
	for _, key := range keys {
		win := a.local.attempts[key]
		if win.count == 0 {
			win.until = now.Add(10 * time.Minute)
		}
		win.count++
		a.local.attempts[key] = win
	}
	return true
}
func (a *Auth) signIn(w http.ResponseWriter, r *http.Request, id int64) {
	if err := a.st.RecordLocalLogin(r.Context(), id, a.now()); err != nil {
		writeJSONError(w, 500, "sign in failed")
		return
	}
	token, csrf, err := a.MintSession(r.Context(), id)
	if err != nil {
		writeJSONError(w, 500, "sign in failed")
		return
	}
	setCookie(w, a.cfg, CookieSession, token, sessionTTL, true)
	setCookie(w, a.cfg, CookieCSRF, csrf, sessionTTL, false)
	authJSON(w, 200, map[string]bool{"ok": true})
}
func (a *Auth) HandleStatus(w http.ResponseWriter, r *http.Request) {
	has, err := a.st.HasLocalAccounts(r.Context())
	if err != nil {
		writeJSONError(w, 500, "unable to read setup status")
		return
	}
	authJSON(w, 200, map[string]bool{"setupRequired": !has})
}
func (a *Auth) HandleSetup(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(w, r) || !a.allowAttempt(w, r, "") {
		return
	}
	var in struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !readAuthJSON(w, r, &in) {
		return
	}
	in.Username = NormalizeUsername(in.Username)
	if err := ValidateCredentials(in.Username, in.Password); err != nil {
		writeJSONError(w, 400, err.Error())
		return
	}
	if len(in.Name) > 120 {
		writeJSONError(w, 400, "name must be at most 120 characters")
		return
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		writeJSONError(w, 500, "account creation failed")
		return
	}
	u, err := a.st.CreateLocalUser(r.Context(), in.Username, strings.TrimSpace(in.Name), hash, store.RoleAdmin, true, a.now())
	if errors.Is(err, store.ErrConflict) {
		writeJSONError(w, 409, "setup is already complete")
		return
	}
	if err != nil {
		writeJSONError(w, 500, "account creation failed")
		return
	}
	a.signIn(w, r, u.ID)
}
func (a *Auth) HandlePasswordLogin(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(w, r) {
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readAuthJSON(w, r, &in) {
		return
	}
	in.Username = NormalizeUsername(in.Username)
	if !a.allowAttempt(w, r, in.Username) {
		return
	}
	a.local.securityMu.Lock()
	defer a.local.securityMu.Unlock()
	c, err := a.st.CredentialsByUsername(r.Context(), in.Username)
	hash := ""
	if err == nil {
		hash = c.PasswordHash
	}
	valid := verifyPassword(in.Password, hash)
	if err != nil || !valid {
		writeJSONError(w, 401, "invalid username, password or verification code")
		return
	}
	u, err := a.st.GetUser(r.Context(), c.UserID)
	if err != nil || u.Role == store.RoleDisabled {
		writeJSONError(w, 401, "invalid username, password or verification code")
		return
	}
	if len(c.TOTPSecret) > 0 {
		if strings.TrimSpace(in.Code) == "" {
			authJSON(w, 200, map[string]bool{"requiresTwoFactor": true})
			return
		}
		if !a.verifySecondFactor(r, c, in.Code) {
			writeJSONError(w, 401, "invalid username, password or verification code")
			return
		}
	}
	a.signIn(w, r, c.UserID)
}
func (a *Auth) verifySecondFactor(r *http.Request, c *store.Credentials, code string) bool {
	code = strings.TrimSpace(code)
	if len(c.TOTPSecret) == 0 {
		return false
	}
	if len(code) == 6 {
		secret, err := a.st.Box.Open(c.TOTPSecret)
		if err != nil {
			return false
		}
		step, ok := matchTOTP(secret, code, a.now())
		if !ok {
			return false
		}
		used, err := a.st.ConsumeTOTP(r.Context(), c.UserID, step, c.TOTPSecret)
		return err == nil && used
	}
	used, err := a.st.ConsumeRecovery(r.Context(), c.UserID, recoveryHash(code))
	return err == nil && used
}
func (a *Auth) securityCredentials(w http.ResponseWriter, r *http.Request) *store.Credentials {
	u, ok := UserFrom(r.Context())
	if !ok {
		writeJSONError(w, 401, "unauthenticated")
		return nil
	}
	c, err := a.st.Credentials(r.Context(), u.ID)
	if err != nil {
		writeJSONError(w, 400, "local account required")
		return nil
	}
	return c
}
func (a *Auth) HandleSecurity(w http.ResponseWriter, r *http.Request) {
	c := a.securityCredentials(w, r)
	if c == nil {
		return
	}
	authJSON(w, 200, map[string]any{"username": c.Username, "twoFactorEnabled": len(c.TOTPSecret) > 0, "recoveryCodesRemaining": len(c.RecoveryHashes)})
}
func (a *Auth) HandleEnrollTOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if !readAuthJSON(w, r, &in) || !a.allowAttempt(w, r, "") {
		return
	}
	a.local.securityMu.Lock()
	defer a.local.securityMu.Unlock()
	c := a.securityCredentials(w, r)
	if c == nil {
		return
	}
	if !verifyPassword(in.Password, c.PasswordHash) {
		writeJSONError(w, 401, "incorrect password")
		return
	}
	if len(c.TOTPSecret) > 0 {
		writeJSONError(w, 409, "two-factor authentication is already enabled")
		return
	}
	secret, err := newTOTPSecret()
	if err != nil {
		writeJSONError(w, 500, "enrollment failed")
		return
	}
	encrypted, err := a.st.Box.Seal(secret)
	if err != nil {
		writeJSONError(w, 500, "enrollment failed")
		return
	}
	if err = a.st.SetTOTPPending(r.Context(), c.UserID, encrypted, a.now().Add(10*time.Minute).Unix()); err != nil {
		writeJSONError(w, 500, "enrollment failed")
		return
	}
	encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	q := url.Values{"secret": {encoded}, "issuer": {"Opskest"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	uri := "otpauth://totp/" + url.PathEscape("Opskest:"+c.Username) + "?" + q.Encode()
	authJSON(w, 200, map[string]string{"secret": encoded, "otpauthUrl": uri})
}
func (a *Auth) HandleEnableTOTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !readAuthJSON(w, r, &in) || !a.allowAttempt(w, r, "") {
		return
	}
	a.local.securityMu.Lock()
	defer a.local.securityMu.Unlock()
	c := a.securityCredentials(w, r)
	if c == nil {
		return
	}
	if len(c.TOTPPending) == 0 || c.PendingExpires <= a.now().Unix() || len(c.TOTPSecret) > 0 {
		writeJSONError(w, 400, "start a new enrollment")
		return
	}
	secret, err := a.st.Box.Open(c.TOTPPending)
	if err != nil {
		writeJSONError(w, 500, "enrollment failed")
		return
	}
	step, valid := matchTOTP(secret, strings.TrimSpace(in.Code), a.now())
	if !valid {
		writeJSONError(w, 400, "incorrect verification code")
		return
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		writeJSONError(w, 500, "enrollment failed")
		return
	}
	if err = a.st.EnableTOTP(r.Context(), c.UserID, c.TOTPPending, step, hashes, a.now().Unix()); err != nil {
		writeJSONError(w, 409, "enrollment expired; start again")
		return
	}
	a.revokeOtherSessions(r, c.UserID)
	authJSON(w, 200, map[string]any{"recoveryCodes": codes})
}
func (a *Auth) HandleDisableTOTP(w http.ResponseWriter, r *http.Request)   { a.changeTOTP(w, r, false) }
func (a *Auth) HandleRecoveryCodes(w http.ResponseWriter, r *http.Request) { a.changeTOTP(w, r, true) }
func (a *Auth) changeTOTP(w http.ResponseWriter, r *http.Request, regenerate bool) {
	var in struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !readAuthJSON(w, r, &in) || !a.allowAttempt(w, r, "") {
		return
	}
	a.local.securityMu.Lock()
	defer a.local.securityMu.Unlock()
	c := a.securityCredentials(w, r)
	if c == nil {
		return
	}
	if !verifyPassword(in.Password, c.PasswordHash) || !a.verifySecondFactor(r, c, in.Code) {
		writeJSONError(w, 401, "incorrect password or verification code")
		return
	}
	if regenerate {
		codes, hashes, err := newRecoveryCodes()
		if err != nil {
			writeJSONError(w, 500, "recovery code update failed")
			return
		}
		if err = a.st.ReplaceRecovery(r.Context(), c.UserID, hashes); err != nil {
			writeJSONError(w, 500, "recovery code update failed")
			return
		}
		authJSON(w, 200, map[string]any{"recoveryCodes": codes})
		return
	}
	if err := a.st.DisableTOTP(r.Context(), c.UserID); err != nil {
		writeJSONError(w, 500, "two-factor update failed")
		return
	}
	a.revokeOtherSessions(r, c.UserID)
	authJSON(w, 200, map[string]bool{"ok": true})
}
func (a *Auth) HandleChangePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
		Code            string `json:"code"`
	}
	if !readAuthJSON(w, r, &in) || !a.allowAttempt(w, r, "") {
		return
	}
	if err := ValidatePassword(in.NewPassword); err != nil {
		writeJSONError(w, 400, err.Error())
		return
	}
	a.local.securityMu.Lock()
	defer a.local.securityMu.Unlock()
	c := a.securityCredentials(w, r)
	if c == nil {
		return
	}
	if !verifyPassword(in.CurrentPassword, c.PasswordHash) || (len(c.TOTPSecret) > 0 && !a.verifySecondFactor(r, c, in.Code)) {
		writeJSONError(w, 401, "incorrect password or verification code")
		return
	}
	hash, err := HashPassword(in.NewPassword)
	if err != nil {
		writeJSONError(w, 500, "password update failed")
		return
	}
	if err = a.st.ChangePassword(r.Context(), c.UserID, c.PasswordHash, hash); err != nil {
		writeJSONError(w, 500, "password update failed")
		return
	}
	a.signIn(w, r, c.UserID)
}
func (a *Auth) revokeOtherSessions(r *http.Request, id int64) {
	cookie, err := r.Cookie(CookieSession)
	if err != nil {
		return
	}
	_ = a.st.DeleteOtherUserSessions(r.Context(), id, hashToken(cookie.Value))
}
