package check

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"opskest/internal/model"
)

// This file holds what an HTTP monitor needs beyond a plain request: an
// outbound proxy, extra CA certificates, an mTLS client certificate and the
// OAuth2 client-credentials grant. Everything is built from the standard
// library; a proxy login, client key and client secret come from
// m.Secrets and are never put in a failure reason.

// httpTLSConfig builds the TLS settings of an http monitor: verification
// (unless ignored), extra trusted CAs and an optional client certificate.
func httpTLSConfig(m *model.Monitor) (*tls.Config, error) {
	s := m.Settings
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: s.IgnoreTLS}
	if s.CACert != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(s.CACert)) {
			return nil, errors.New("the custom CA certificate is not valid PEM")
		}
		cfg.RootCAs = pool
	}
	if m.Secrets.ClientCert != "" || m.Secrets.ClientKey != "" {
		cert, err := tls.X509KeyPair([]byte(m.Secrets.ClientCert), []byte(m.Secrets.ClientKey))
		if err != nil {
			return nil, errors.New("the client certificate and key are not a valid pair")
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// httpProxy returns the transport's Proxy function for the monitor's proxy
// settings, or nil for a direct connection. net/http speaks http and https
// (CONNECT) and socks5 / socks5h itself.
func httpProxy(m *model.Monitor) (func(*http.Request) (*url.URL, error), error) {
	s := m.Settings
	if s.ProxyURL == "" {
		return nil, nil
	}
	u, err := url.Parse(s.ProxyURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("the proxy URL is not valid")
	}
	if s.ProxyUser != "" || m.Secrets.ProxyPass != "" {
		u.User = url.UserPassword(s.ProxyUser, m.Secrets.ProxyPass)
	}
	return http.ProxyURL(u), nil
}

// newHTTPTransport builds a one-shot transport for a check. The caller
// closes its idle connections.
func newHTTPTransport(m *model.Monitor) (*http.Transport, error) {
	cfg, err := httpTLSConfig(m)
	if err != nil {
		return nil, err
	}
	proxy, err := httpProxy(m)
	if err != nil {
		return nil, err
	}
	return &http.Transport{
		DisableKeepAlives: true,
		ForceAttemptHTTP2: true,
		TLSClientConfig:   cfg,
		Proxy:             proxy,
		DialContext:       (&net.Dialer{}).DialContext,
	}, nil
}

// setupFailure is the Result for a monitor whose own settings cannot be
// turned into a request (a bad certificate pair, an unusable proxy URL).
func setupFailure(err error) model.Result {
	return model.Result{LatencyMs: -1, ErrClass: model.ErrOther, Reason: model.TrimReason(err.Error())}
}

// ── OAuth2 client credentials ─────────────────────────────────────

const (
	oauthBodyLimit   = 64 << 10
	oauthDefaultTTL  = 5 * time.Minute
	oauthRefreshSkew = 30 * time.Second
	oauthCacheMax    = 256
)

type oauthEntry struct {
	token   string
	expires time.Time
}

var oauthCache = struct {
	mu sync.Mutex
	m  map[string]oauthEntry
}{m: map[string]oauthEntry{}}

// oauthKey identifies one token: it covers every input that changes what the
// token endpoint returns. The secret is hashed in, never stored in the clear
// as a key.
func oauthKey(m *model.Monitor) string {
	s := m.Settings
	h := sha256.New()
	for _, p := range []string{s.OAuthTokenURL, s.OAuthClientID, s.OAuthScope, s.OAuthAudience, s.OAuthAuth, m.Secrets.OAuthSecret} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func oauthForget(key string) {
	oauthCache.mu.Lock()
	delete(oauthCache.m, key)
	oauthCache.mu.Unlock()
}

func oauthStore(key string, e oauthEntry, now time.Time) {
	oauthCache.mu.Lock()
	defer oauthCache.mu.Unlock()
	if len(oauthCache.m) >= oauthCacheMax {
		for k, v := range oauthCache.m {
			if !v.expires.After(now) {
				delete(oauthCache.m, k)
			}
		}
		for k := range oauthCache.m { // still full: drop one
			if len(oauthCache.m) < oauthCacheMax {
				break
			}
			delete(oauthCache.m, k)
		}
	}
	oauthCache.m[key] = e
}

var oauthErrCode = regexp.MustCompile(`^[a-z_]{1,40}$`)

// oauthToken returns a bearer token for the monitor, from the cache while it
// is valid or from the token endpoint. fromCache tells the caller whether a
// 401 should evict it. Errors are safe to show: they carry the HTTP status
// and the standard OAuth error code at most, never the body or the secret.
func oauthToken(ctx context.Context, m *model.Monitor, tr http.RoundTripper, now time.Time) (token, key string, fromCache bool, err error) {
	key = oauthKey(m)
	oauthCache.mu.Lock()
	e, ok := oauthCache.m[key]
	oauthCache.mu.Unlock()
	if ok && e.expires.After(now) {
		return e.token, key, true, nil
	}

	s := m.Settings
	form := url.Values{"grant_type": {"client_credentials"}}
	if s.OAuthScope != "" {
		form.Set("scope", s.OAuthScope)
	}
	if s.OAuthAudience != "" {
		form.Set("audience", s.OAuthAudience)
	}
	post := s.OAuthAuth == "post"
	if post {
		form.Set("client_id", s.OAuthClientID)
		form.Set("client_secret", m.Secrets.OAuthSecret)
	}
	req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, s.OAuthTokenURL, strings.NewReader(form.Encode()))
	if rerr != nil {
		return "", key, false, errors.New("OAuth2: invalid token URL")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "opskest/"+Version)
	if !post {
		// RFC 6749 section 2.3.1: the id and secret are form-encoded first.
		cred := url.QueryEscape(s.OAuthClientID) + ":" + url.QueryEscape(m.Secrets.OAuthSecret)
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(cred)))
	}

	client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, derr := client.Do(req)
	if derr != nil {
		_, reason := ClassifyErr(derr)
		// The error text names the token URL; keep only the cause.
		var ue *url.Error
		if errors.As(derr, &ue) && ue.Err != nil {
			reason = ue.Err.Error()
		}
		return "", key, false, fmt.Errorf("OAuth2 token request failed: %s", reason)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, oauthBodyLimit))
	var out struct {
		AccessToken string          `json:"access_token"`
		TokenType   string          `json:"token_type"`
		ExpiresIn   json.RawMessage `json:"expires_in"`
		Error       string          `json:"error"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if oauthErrCode.MatchString(out.Error) {
			return "", key, false, fmt.Errorf("OAuth2 token request failed: HTTP %d (%s)", resp.StatusCode, out.Error)
		}
		return "", key, false, fmt.Errorf("OAuth2 token request failed: HTTP %d", resp.StatusCode)
	}
	if out.AccessToken == "" {
		return "", key, false, errors.New("OAuth2 token response had no access_token")
	}
	if out.TokenType != "" && !strings.EqualFold(out.TokenType, "bearer") {
		return "", key, false, errors.New("OAuth2 token type is not bearer")
	}
	ttl := oauthDefaultTTL
	if secs, ok := oauthSeconds(out.ExpiresIn); ok {
		ttl = time.Duration(secs) * time.Second
	}
	if ttl > oauthRefreshSkew {
		oauthStore(key, oauthEntry{token: out.AccessToken, expires: now.Add(ttl - oauthRefreshSkew)}, now)
	}
	return out.AccessToken, key, false, nil
}

// oauthSeconds reads expires_in, which servers send as a number or a string.
func oauthSeconds(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		if v, err := n.Int64(); err == nil && v >= 0 {
			return min(v, 86400*7), true
		}
		return 0, false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		var v int64
		if _, err := fmt.Sscanf(s, "%d", &v); err == nil && v >= 0 {
			return min(v, 86400*7), true
		}
	}
	return 0, false
}
