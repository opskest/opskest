package model

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func selfSigned(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "t"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

func httpMon() *Monitor {
	m := NewMonitor(TypeHTTP)
	m.Name, m.Target = "x", "https://example.com"
	return m
}

func wantErr(t *testing.T, fe FieldErrors, key string) {
	t.Helper()
	if fe[key] == "" {
		t.Errorf("expected an error for %q, got %v", key, fe)
	}
}

func TestValidateProxy(t *testing.T) {
	good := []string{"http://proxy.corp:3128", "https://proxy.corp", "socks5://10.0.0.1:1080", "socks5h://tor:9050", "http://proxy.corp:3128/"}
	for _, u := range good {
		m := httpMon()
		m.Settings.ProxyURL = u
		if fe := m.Validate(); len(fe) != 0 {
			t.Errorf("%q rejected: %v", u, fe)
		}
	}
	bad := []string{"proxy.corp:3128", "ftp://proxy.corp", "http://user:pw@proxy.corp:3128", "http://proxy.corp:3128/path", "http://", "socks4://x:1"}
	for _, u := range bad {
		m := httpMon()
		m.Settings.ProxyURL = u
		wantErr(t, m.Validate(), "settings.proxyUrl")
	}
	m := httpMon()
	m.Settings.ProxyUser = "bob"
	wantErr(t, m.Validate(), "settings.proxyUrl") // a login without a proxy
}

func TestValidateClientCertAndCA(t *testing.T) {
	certPEM, keyPEM := selfSigned(t)
	otherCert, otherKey := selfSigned(t)

	m := httpMon()
	m.Settings.CACert = certPEM
	m.Secrets.ClientCert, m.Secrets.ClientKey = certPEM, keyPEM
	if fe := m.Validate(); len(fe) != 0 {
		t.Fatalf("valid pair rejected: %v", fe)
	}

	m.Secrets.ClientKey = otherKey
	wantErr(t, m.Validate(), "secrets.clientCert") // mismatched pair
	m.Secrets.ClientKey = ""
	wantErr(t, m.Validate(), "secrets.clientCert") // certificate without key
	m.Secrets.ClientCert, m.Secrets.ClientKey = "", keyPEM
	wantErr(t, m.Validate(), "secrets.clientCert")
	_ = otherCert

	m = httpMon()
	m.Settings.CACert = "not a pem"
	wantErr(t, m.Validate(), "settings.caCert")
}

func TestValidateOAuth(t *testing.T) {
	m := httpMon()
	m.Settings.AuthType = "oauth2"
	m.Settings.OAuthTokenURL = "https://idp.example.com/oauth/token"
	m.Settings.OAuthClientID = "id"
	m.Secrets.OAuthSecret = "s"
	m.ApplyDefaults()
	if fe := m.Validate(); len(fe) != 0 {
		t.Fatalf("valid oauth2 rejected: %v", fe)
	}
	if m.Settings.OAuthAuth != "basic" {
		t.Errorf("OAuthAuth default = %q, want basic", m.Settings.OAuthAuth)
	}

	m.Secrets.OAuthSecret = ""
	wantErr(t, m.Validate(), "secrets.oauthSecret")
	m.Secrets.OAuthSecret = "s"
	m.Settings.OAuthClientID = " "
	wantErr(t, m.Validate(), "settings.oauthClientId")
	m.Settings.OAuthClientID = "id"
	m.Settings.OAuthTokenURL = "ftp://x"
	wantErr(t, m.Validate(), "settings.oauthTokenUrl")
	m.Settings.OAuthTokenURL = "https://user:pw@idp/token"
	wantErr(t, m.Validate(), "settings.oauthTokenUrl")
	m.Settings.OAuthTokenURL = "https://idp/token"
	m.Settings.OAuthAuth = "header"
	wantErr(t, m.Validate(), "settings.oauthAuth")
}

func TestValidateTCPExpect(t *testing.T) {
	mk := func(mut func(*Settings)) FieldErrors {
		m := NewMonitor(TypeTCP)
		m.Name, m.Target = "x", "db:5432"
		mut(&m.Settings)
		m.ApplyDefaults()
		return m.Validate()
	}
	if fe := mk(func(s *Settings) { s.Send = "PING\r\n"; s.Expect = "+PONG" }); len(fe) != 0 {
		t.Errorf("valid send/expect rejected: %v", fe)
	}
	if fe := mk(func(s *Settings) { s.Expect = `^220 .*ready`; s.ExpectMode = "regex" }); len(fe) != 0 {
		t.Errorf("valid regex rejected: %v", fe)
	}
	wantErr(t, mk(func(s *Settings) { s.Expect = `(`; s.ExpectMode = "regex" }), "settings.expect")
	wantErr(t, mk(func(s *Settings) { s.Expect = "x"; s.ExpectMode = "glob" }), "settings.expectMode")
	wantErr(t, mk(func(s *Settings) { s.Send = "hex:zz" }), "settings.send")
	wantErr(t, mk(func(s *Settings) { s.Expect = "hex:0g" }), "settings.expect")
	wantErr(t, mk(func(s *Settings) { s.ExpectMode = "regex" }), "settings.expect")
}

func TestValidateGroup(t *testing.T) {
	mk := func(mut func(*Monitor)) FieldErrors {
		m := NewMonitor(TypeGroup)
		m.Name = "g"
		m.Settings.Children = []int64{1, 2, 3}
		mut(m)
		m.ApplyDefaults()
		return m.Validate()
	}
	if fe := mk(func(m *Monitor) {}); len(fe) != 0 {
		t.Fatalf("valid group rejected: %v", fe)
	}
	if fe := mk(func(m *Monitor) { m.Settings.GroupMode = "min"; m.Settings.GroupMin = 2 }); len(fe) != 0 {
		t.Fatalf("valid min group rejected: %v", fe)
	}
	wantErr(t, mk(func(m *Monitor) { m.Settings.GroupMode = "min"; m.Settings.GroupMin = 4 }), "settings.groupMin")
	wantErr(t, mk(func(m *Monitor) { m.Settings.GroupMode = "min" }), "settings.groupMin")
	wantErr(t, mk(func(m *Monitor) { m.Settings.GroupMode = "most" }), "settings.groupMode")
	wantErr(t, mk(func(m *Monitor) { m.Settings.Children = nil }), "settings.children")
	wantErr(t, mk(func(m *Monitor) { m.Settings.Children = []int64{1, 1} }), "settings.children")
	wantErr(t, mk(func(m *Monitor) { m.ID = 2 }), "settings.children") // contains itself
	wantErr(t, mk(func(m *Monitor) { m.Target = "x" }), "target")
	wantErr(t, mk(func(m *Monitor) { m.UpsideDown = true }), "upsideDown")
	wantErr(t, mk(func(m *Monitor) { m.Settings.LatencyAlert = &LatencyAlert{ThresholdMs: 100} }), "settings.latencyAlert")
	big := make([]int64, MaxGroupChildren+1)
	for i := range big {
		big[i] = int64(i + 1)
	}
	wantErr(t, mk(func(m *Monitor) { m.Settings.Children = big }), "settings.children")
}

func TestValidateManual(t *testing.T) {
	m := NewMonitor(TypeManual)
	m.Name = "nightly import"
	if fe := m.Validate(); len(fe) != 0 {
		t.Fatalf("valid manual monitor rejected: %v", fe)
	}
	m.Target = "x"
	wantErr(t, m.Validate(), "target")
	m.Target, m.UpsideDown = "", true
	wantErr(t, m.Validate(), "upsideDown")
}

func TestValidateCertThresholds(t *testing.T) {
	mk := func(t ...int) FieldErrors {
		m := httpMon()
		m.Settings.CertThresholds = t
		return m.Validate()
	}
	if fe := mk(30, 14, 7, 1); len(fe) != 0 {
		t.Errorf("valid thresholds rejected: %v", fe)
	}
	wantErr(t, mk(0), "settings.certThresholds")
	wantErr(t, mk(366), "settings.certThresholds")
	wantErr(t, mk(7, 7), "settings.certThresholds")
	wantErr(t, mk(1, 2, 3, 4, 5, 6, 7, 8, 9), "settings.certThresholds")
}

func TestTypesAreRegistered(t *testing.T) {
	for _, ty := range []MonitorType{TypeGroup, TypeManual} {
		if !ty.Known() {
			t.Errorf("%s is not in AllTypes", ty)
		}
		if !ty.HasNoTarget() {
			t.Errorf("%s should have no target", ty)
		}
	}
}

func TestSecretsIsEmptyCoversEveryField(t *testing.T) {
	if !(Secrets{}).IsEmpty() {
		t.Fatal("zero Secrets must be empty")
	}
	for name, s := range map[string]Secrets{
		"headers": {Headers: map[string]string{"a": "b"}}, "basic": {BasicPass: "x"}, "bearer": {BearerToken: "x"},
		"proxy": {ProxyPass: "x"}, "cert": {ClientCert: "x"}, "key": {ClientKey: "x"}, "oauth": {OAuthSecret: "x"},
	} {
		if s.IsEmpty() {
			t.Errorf("Secrets with %s set reported empty: it would be dropped on save", name)
		}
	}
}

func TestSettingsCloneIsDeepForNewSlices(t *testing.T) {
	m := NewMonitor(TypeGroup)
	m.Settings.Children = []int64{1, 2}
	m.Settings.CertThresholds = []int{7}
	c := m.Clone()
	c.Settings.Children[0], c.Settings.CertThresholds[0] = 99, 99
	if m.Settings.Children[0] != 1 || m.Settings.CertThresholds[0] != 7 {
		t.Fatal("Clone shares slices with the original")
	}
}
