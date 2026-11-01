package check

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opskest/internal/model"
)

// ── proxy ─────────────────────────────────────────────────────────

func TestHTTPViaHTTPProxyWithLogin(t *testing.T) {
	var sawAuth atomic.Value
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store(r.Header.Get("Proxy-Authorization"))
		if r.URL.Host != "app.invalid" {
			http.Error(w, "unexpected target "+r.URL.Host, http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "via proxy")
	}))
	defer proxy.Close()

	m := newHTTPMonitor("http://app.invalid/health")
	m.Settings.ProxyURL = proxy.URL
	m.Settings.ProxyUser = "bob"
	m.Secrets.ProxyPass = "s3cret"
	m.Settings.Keyword = "via proxy"
	m.ApplyDefaults()

	res := Run(context.Background(), m, time.Now)
	if !res.OK {
		t.Fatalf("via proxy: %q (%s)", res.Reason, res.ErrClass)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("bob:s3cret"))
	if got, _ := sawAuth.Load().(string); got != want {
		t.Errorf("Proxy-Authorization = %q, want %q", got, want)
	}

	// Without the proxy the unresolvable name fails: the proxy was what made it reachable.
	m.Settings.ProxyURL = ""
	if res := Run(context.Background(), m, time.Now); res.OK {
		t.Fatal("expected a direct request to app.invalid to fail")
	}
}

// connectProxy is a minimal HTTP CONNECT proxy.
func connectProxy(t *testing.T, seen *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		seen.Add(1)
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, _ := w.(http.Hijacker)
		down, buf, err := hj.Hijack()
		if err != nil {
			up.Close()
			return
		}
		_, _ = down.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		go func() { _, _ = io.Copy(up, buf); up.Close() }()
		go func() { _, _ = io.Copy(down, up); down.Close() }()
	}))
}

func TestHTTPSViaConnectProxy(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer target.Close()
	var seen atomic.Int32
	proxy := connectProxy(t, &seen)
	defer proxy.Close()

	m := newHTTPMonitor(target.URL)
	m.Settings.IgnoreTLS = true
	m.Settings.ProxyURL = proxy.URL
	if res := Run(context.Background(), m, time.Now); !res.OK {
		t.Fatalf("https via CONNECT: %q", res.Reason)
	}
	if seen.Load() != 1 {
		t.Errorf("proxy CONNECTs = %d, want 1", seen.Load())
	}
}

// socks5Server is a tiny SOCKS5 server (no auth, or username/password).
func socks5Server(t *testing.T, user, pass string, seen *atomic.Int32) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				hdr := make([]byte, 2)
				if _, err := io.ReadFull(br, hdr); err != nil || hdr[0] != 5 {
					return
				}
				methods := make([]byte, hdr[1])
				_, _ = io.ReadFull(br, methods)
				if user != "" {
					_, _ = c.Write([]byte{5, 2})
					ver := make([]byte, 2)
					if _, err := io.ReadFull(br, ver); err != nil {
						return
					}
					u := make([]byte, ver[1])
					_, _ = io.ReadFull(br, u)
					pl, _ := br.ReadByte()
					p := make([]byte, pl)
					_, _ = io.ReadFull(br, p)
					if string(u) != user || string(p) != pass {
						_, _ = c.Write([]byte{1, 1})
						return
					}
					_, _ = c.Write([]byte{1, 0})
				} else {
					_, _ = c.Write([]byte{5, 0})
				}
				req := make([]byte, 4)
				if _, err := io.ReadFull(br, req); err != nil {
					return
				}
				var host string
				switch req[3] {
				case 1:
					ip := make([]byte, 4)
					_, _ = io.ReadFull(br, ip)
					host = net.IP(ip).String()
				case 3:
					l, _ := br.ReadByte()
					h := make([]byte, l)
					_, _ = io.ReadFull(br, h)
					host = string(h)
				default:
					return
				}
				pb := make([]byte, 2)
				_, _ = io.ReadFull(br, pb)
				port := int(pb[0])<<8 | int(pb[1])
				up, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
				if err != nil {
					_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer up.Close()
				seen.Add(1)
				_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				go func() { _, _ = io.Copy(up, br) }()
				_, _ = io.Copy(c, up)
			}()
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln
}

func TestHTTPViaSocks5(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer target.Close()
	var seen atomic.Int32
	ln := socks5Server(t, "", "", &seen)

	m := newHTTPMonitor(target.URL)
	m.Settings.ProxyURL = "socks5://" + ln.Addr().String()
	if res := Run(context.Background(), m, time.Now); !res.OK {
		t.Fatalf("via socks5: %q", res.Reason)
	}
	if seen.Load() != 1 {
		t.Errorf("socks5 connections = %d, want 1", seen.Load())
	}
}

func TestHTTPViaSocks5WithLogin(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") }))
	defer target.Close()
	var seen atomic.Int32
	ln := socks5Server(t, "alice", "pw", &seen)

	m := newHTTPMonitor(target.URL)
	m.Settings.ProxyURL = "socks5://" + ln.Addr().String()
	m.Settings.ProxyUser = "alice"
	m.Secrets.ProxyPass = "pw"
	if res := Run(context.Background(), m, time.Now); !res.OK {
		t.Fatalf("socks5 with login: %q", res.Reason)
	}
	m.Secrets.ProxyPass = "wrong"
	res := Run(context.Background(), m, time.Now)
	if res.OK {
		t.Fatal("a wrong proxy password must fail")
	}
	if strings.Contains(res.Reason, "wrong") {
		t.Errorf("reason leaks the password: %q", res.Reason)
	}
}

// ── mTLS and custom CA ────────────────────────────────────────────

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  string
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testCA{cert: cert, key: key, pem: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
}

// issue returns a client certificate and key as PEM.
func (ca *testCA) issueClient(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "opskest-client"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

func mtlsServer(t *testing.T, clientCA *testCA) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.TLS.PeerCertificates[0].Subject.CommonName)
	}))
	pool := x509.NewCertPool()
	pool.AddCert(clientCA.cert)
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPMutualTLSAndCustomCA(t *testing.T) {
	ca := newTestCA(t)
	srv := mtlsServer(t, ca)
	serverPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	certPEM, keyPEM := ca.issueClient(t)

	m := newHTTPMonitor(srv.URL)
	m.Settings.CACert = serverPEM // trusts the test server's self-signed certificate; no IgnoreTLS
	m.Settings.Keyword = "opskest-client"

	// No client certificate: the server rejects the handshake.
	if res := Run(context.Background(), m, time.Now); res.OK {
		t.Fatal("expected failure without a client certificate")
	}

	m.Secrets.ClientCert, m.Secrets.ClientKey = certPEM, keyPEM
	if res := Run(context.Background(), m, time.Now); !res.OK {
		t.Fatalf("with client certificate: %q (%s)", res.Reason, res.ErrClass)
	}

	// Without the custom CA the server certificate is untrusted.
	m.Settings.CACert = ""
	res := Run(context.Background(), m, time.Now)
	if res.OK || res.ErrClass != model.ErrTLS {
		t.Fatalf("without the CA: ok=%v class=%q reason=%q, want a TLS failure", res.OK, res.ErrClass, res.Reason)
	}

	// A broken pair is reported without echoing the key.
	m.Settings.CACert = serverPEM
	m.Secrets.ClientKey = "-----BEGIN EC PRIVATE KEY-----\nAAAA\n-----END EC PRIVATE KEY-----"
	res = Run(context.Background(), m, time.Now)
	if res.OK || strings.Contains(res.Reason, "AAAA") || strings.Contains(res.Reason, "PRIVATE") {
		t.Fatalf("broken pair: ok=%v reason=%q", res.OK, res.Reason)
	}
}

// ── OAuth2 client credentials ─────────────────────────────────────

type tokenServer struct {
	*httptest.Server
	hits    atomic.Int32
	lastReq atomic.Value // url.Values of the last body
	auth    atomic.Value // Authorization header of the last request
	expires int
}

func newTokenServer(t *testing.T, expiresIn int, token string) *tokenServer {
	t.Helper()
	ts := &tokenServer{expires: expiresIn}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.hits.Add(1)
		_ = r.ParseForm()
		ts.lastReq.Store(r.PostForm)
		ts.auth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":%d}`, token, ts.expires)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func oauthMonitor(api, tokenURL, secret string) *model.Monitor {
	m := newHTTPMonitor(api)
	m.Settings.AuthType = "oauth2"
	m.Settings.OAuthTokenURL = tokenURL
	m.Settings.OAuthClientID = "my client"
	m.Settings.OAuthScope = "read"
	m.Secrets.OAuthSecret = secret
	m.ApplyDefaults()
	return m
}

func TestOAuth2ClientCredentialsCachesToken(t *testing.T) {
	ts := newTokenServer(t, 3600, "tok-123")
	var apiAuth atomic.Value
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiAuth.Store(r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer api.Close()

	m := oauthMonitor(api.URL, ts.URL, "secret-A1")
	for i := 0; i < 3; i++ {
		if res := Run(context.Background(), m, time.Now); !res.OK {
			t.Fatalf("check %d: %q", i, res.Reason)
		}
	}
	if ts.hits.Load() != 1 {
		t.Errorf("token endpoint hit %d times for 3 checks, want 1 (cached)", ts.hits.Load())
	}
	// client_secret_basic: id and secret form-encoded, then base64.
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("my+client:secret-A1"))
	if got, _ := ts.auth.Load().(string); got != want {
		t.Errorf("token request Authorization = %q, want %q", got, want)
	}
	form := ts.lastReq.Load().(interface{ Get(string) string })
	if form.Get("grant_type") != "client_credentials" || form.Get("scope") != "read" || form.Get("client_secret") != "" {
		t.Errorf("token form unexpected: grant=%q scope=%q client_secret=%q", form.Get("grant_type"), form.Get("scope"), form.Get("client_secret"))
	}
}

func TestOAuth2PostBodyAndRefresh(t *testing.T) {
	ts := newTokenServer(t, 3600, "tok-post")
	m := oauthMonitor("http://unused.invalid", ts.URL, "secret-B2")
	m.Settings.OAuthAuth = "post"
	now := time.Now()
	tr := http.DefaultTransport

	if _, _, _, err := oauthToken(context.Background(), m, tr, now); err != nil {
		t.Fatal(err)
	}
	form := ts.lastReq.Load().(interface{ Get(string) string })
	if form.Get("client_id") != "my client" || form.Get("client_secret") != "secret-B2" {
		t.Errorf("post-mode form missing credentials")
	}
	if a, _ := ts.auth.Load().(string); a != "" {
		t.Errorf("post mode must not send an Authorization header, got %q", a)
	}
	// Still cached just before the refresh point, refetched after it.
	if _, _, cached, _ := oauthToken(context.Background(), m, tr, now.Add(3500*time.Second)); !cached {
		t.Error("token should still be cached at 3500s of 3600s")
	}
	if _, _, cached, _ := oauthToken(context.Background(), m, tr, now.Add(3580*time.Second)); cached {
		t.Error("token should be refreshed inside the last 30s")
	}
	if ts.hits.Load() != 2 {
		t.Errorf("token endpoint hits = %d, want 2", ts.hits.Load())
	}
}

func TestOAuth2ShortLivedTokenIsNotCached(t *testing.T) {
	ts := newTokenServer(t, 20, "tok-short")
	m := oauthMonitor("http://unused.invalid", ts.URL, "secret-C3")
	for i := 0; i < 2; i++ {
		if _, _, _, err := oauthToken(context.Background(), m, http.DefaultTransport, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if ts.hits.Load() != 2 {
		t.Errorf("hits = %d, want 2: a token valid for less than the refresh margin is not cached", ts.hits.Load())
	}
}

func TestOAuth2FailureNeverLeaksBodyOrSecret(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid_client","error_description":"bad secret secret-D4 for client"}`)
	}))
	defer ts.Close()
	m := oauthMonitor("http://unused.invalid", ts.URL, "secret-D4")
	res := Run(context.Background(), m, time.Now)
	if res.OK {
		t.Fatal("expected failure")
	}
	if !strings.Contains(res.Reason, "HTTP 401") || !strings.Contains(res.Reason, "invalid_client") {
		t.Errorf("reason = %q, want the status and the OAuth error code", res.Reason)
	}
	if strings.Contains(res.Reason, "secret-D4") || strings.Contains(res.Reason, "bad secret") || strings.Contains(res.Reason, ts.URL) {
		t.Errorf("reason leaks: %q", res.Reason)
	}
}

func TestOAuth2RevokedTokenIsEvicted(t *testing.T) {
	ts := newTokenServer(t, 3600, "tok-rev")
	var valid atomic.Bool
	valid.Store(true)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !valid.Load() {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer api.Close()
	m := oauthMonitor(api.URL, ts.URL, "secret-E5")
	if res := Run(context.Background(), m, time.Now); !res.OK {
		t.Fatal(res.Reason)
	}
	valid.Store(false)
	if res := Run(context.Background(), m, time.Now); res.OK { // 401 with the cached token
		t.Fatal("expected 401 to fail")
	}
	valid.Store(true)
	if res := Run(context.Background(), m, time.Now); !res.OK {
		t.Fatal(res.Reason)
	}
	if ts.hits.Load() != 2 {
		t.Errorf("token endpoint hits = %d, want 2 (re-fetched after the 401)", ts.hits.Load())
	}
}

// ── TCP send and expect ───────────────────────────────────────────

// tcpServer calls handle for every connection.
func tcpServer(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var wg sync.WaitGroup
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() { defer wg.Done(); defer c.Close(); handle(c) }()
		}
	}()
	return ln.Addr().String()
}

func tcpMonitor(addr string) *model.Monitor {
	m := model.NewMonitor(model.TypeTCP)
	m.Target = addr
	m.TimeoutMs = 1500
	return m
}

func TestTCPSendExpect(t *testing.T) {
	addr := tcpServer(t, func(c net.Conn) {
		line, _ := bufio.NewReader(c).ReadString('\n')
		if strings.TrimSpace(line) == "PING" {
			_, _ = io.WriteString(c, "+PONG secret-banner-text\r\n")
		}
	})

	m := tcpMonitor(addr)
	m.Settings.Send = "PING\r\n"
	m.Settings.Expect = "+PONG"
	m.ApplyDefaults()
	if res := Run(context.Background(), m, time.Now); !res.OK {
		t.Fatalf("contains: %q", res.Reason)
	}

	m.Settings.ExpectMode = "regex"
	m.Settings.Expect = `^\+PONG\s+\S+`
	if res := Run(context.Background(), m, time.Now); !res.OK {
		t.Fatalf("regex: %q", res.Reason)
	}

	m.Settings.Expect = `^NOPE`
	res := Run(context.Background(), m, time.Now)
	if res.OK || res.ErrClass != model.ErrAssertion {
		t.Fatalf("mismatch: ok=%v class=%q reason=%q", res.OK, res.ErrClass, res.Reason)
	}
	if strings.Contains(res.Reason, "secret-banner-text") {
		t.Errorf("reason leaks the reply: %q", res.Reason)
	}

	// Hex bytes are sent as-is: "PING\n" is 50 49 4e 47 0a.
	m.Settings.ExpectMode = "contains"
	m.Settings.Send = "hex:50494e470a"
	m.Settings.Expect = "hex:504f4e47" // PONG
	if res := Run(context.Background(), m, time.Now); !res.OK {
		t.Fatalf("hex: %q", res.Reason)
	}
}

func TestTCPExpectBannerWithoutSend(t *testing.T) {
	addr := tcpServer(t, func(c net.Conn) { _, _ = io.WriteString(c, "220 ftp ready\r\n") })
	m := tcpMonitor(addr)
	m.Settings.Expect = "220"
	if res := Run(context.Background(), m, time.Now); !res.OK {
		t.Fatalf("banner: %q", res.Reason)
	}
}

func TestTCPExpectTimeoutAndClose(t *testing.T) {
	silent := tcpServer(t, func(c net.Conn) { time.Sleep(3 * time.Second) })
	m := tcpMonitor(silent)
	m.TimeoutMs = 1000
	m.Settings.Expect = "hello"
	res := Run(context.Background(), m, time.Now)
	if res.OK || res.ErrClass != model.ErrTimeout {
		t.Fatalf("silent server: ok=%v class=%q reason=%q, want a timeout", res.OK, res.ErrClass, res.Reason)
	}

	closer := tcpServer(t, func(c net.Conn) { _, _ = io.WriteString(c, "bye") })
	m = tcpMonitor(closer)
	m.Settings.Expect = "hello"
	res = Run(context.Background(), m, time.Now)
	if res.OK || res.ErrClass != model.ErrAssertion {
		t.Fatalf("closed early: ok=%v class=%q reason=%q", res.OK, res.ErrClass, res.Reason)
	}
}

func TestTCPPlainConnectStillWorks(t *testing.T) {
	addr := tcpServer(t, func(c net.Conn) {})
	m := tcpMonitor(addr)
	if res := Run(context.Background(), m, time.Now); !res.OK {
		t.Fatalf("plain connect: %q", res.Reason)
	}
}
