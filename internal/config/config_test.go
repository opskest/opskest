package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func valid() map[string]string {
	return map[string]string{
		"APP_URL":               "https://uptime.example.com",
		"SECRET_ENCRYPTION_KEY": strings.Repeat("ab", 32),
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != ":8080" || c.DataDir != "data" || c.BackupDir != "backups" {
		t.Fatalf("defaults: %+v", c)
	}
	if c.CheckConcurrency != 256 {
		t.Fatalf("concurrency %d", c.CheckConcurrency)
	}
	wantZone, _ := displayLocation("", "", systemTimeZone)
	if c.DisplayTZ.String() != wantZone.String() {
		t.Fatalf("tz %s", c.DisplayTZ)
	}
	if !c.SecureCookies {
		t.Fatal("https app url must set secure cookies")
	}
	if len(c.SecretKey) != 32 {
		t.Fatalf("key len %d", len(c.SecretKey))
	}
}

func TestDisplayLocationUsesServerAndExplicitOverrides(t *testing.T) {
	for _, pair := range [][2]string{{"Local", ""}, {"", "Local"}} {
		if _, err := displayLocation(pair[0], pair[1], func() string { return "America/New_York" }); err == nil {
			t.Fatal("Local is not a portable IANA timezone for runtime metadata")
		}
	}
	for _, tc := range []struct{ explicit, process, host, want string }{
		{"", "", "America/New_York", "America/New_York"},
		{"", "Europe/Berlin", "America/New_York", "Europe/Berlin"},
		{"Asia/Singapore", "Europe/Berlin", "America/New_York", "Asia/Singapore"},
		{"", "", "Unrecognized/Host", "UTC"},
	} {
		loc, err := displayLocation(tc.explicit, tc.process, func() string { return tc.host })
		if err != nil || loc.String() != tc.want {
			t.Fatalf("%+v: %v, %v", tc, loc, err)
		}
	}
	loc, _ := displayLocation("", "", func() string { return "America/New_York" })
	_, winter := time.Date(2026, 1, 10, 12, 0, 0, 0, loc).Zone()
	_, summer := time.Date(2026, 7, 10, 12, 0, 0, 0, loc).Zone()
	if winter != -5*3600 || summer != -4*3600 {
		t.Fatal("server timezone must retain DST rules")
	}
	m := valid()
	m["SERVER_REGION"] = "US East (Virginia)"
	m["TZ"] = "America/New_York"
	cfg, err := Load(env(m))
	if err != nil || cfg.ServerRegion != m["SERVER_REGION"] || cfg.DisplayTZ.String() != m["TZ"] {
		t.Fatalf("server config: %+v, %v", cfg, err)
	}
}

func TestNothingIsRequired(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.AppURL != "http://localhost:8080" || !c.AppURLDefaulted || c.SecureCookies {
		t.Fatalf("defaults: %+v", c)
	}
	if c.SecretKey != nil {
		t.Fatal("the key is generated at startup, not by Load")
	}
}

func TestPortSelectsListenAddressAndDefaultURL(t *testing.T) {
	c, err := Load(env(map[string]string{"PORT": "9100"}))
	if err != nil || c.ListenAddr != ":9100" || c.AppURL != "http://localhost:9100" {
		t.Fatalf("PORT: %+v, %v", c, err)
	}
	c, err = Load(env(map[string]string{"PORT": "9100", "LISTEN_ADDR": "127.0.0.1:7000"}))
	if err != nil || c.ListenAddr != "127.0.0.1:7000" || c.AppURL != "http://localhost:7000" {
		t.Fatalf("LISTEN_ADDR wins: %+v, %v", c, err)
	}
	for _, bad := range []string{"abc", "0", "70000"} {
		if _, err := Load(env(map[string]string{"PORT": bad})); err == nil {
			t.Errorf("PORT %q should be rejected", bad)
		}
	}
}

func TestResolveSecretKey(t *testing.T) {
	dir := t.TempDir()
	c := &Config{DataDir: dir}
	created, err := c.ResolveSecretKey()
	if err != nil || !created || len(c.SecretKey) != 32 {
		t.Fatalf("first start: created=%v err=%v key=%d", created, err, len(c.SecretKey))
	}
	first := append([]byte(nil), c.SecretKey...)

	again := &Config{DataDir: dir}
	created, err = again.ResolveSecretKey()
	if err != nil || created || string(again.SecretKey) != string(first) {
		t.Fatalf("restart must reuse the key: created=%v err=%v", created, err)
	}

	given := &Config{DataDir: dir, SecretKey: make([]byte, 32)}
	if created, err := given.ResolveSecretKey(); err != nil || created {
		t.Fatalf("an explicit key is used as is: %v %v", created, err)
	}

	if err := os.WriteFile(filepath.Join(dir, KeyFileName), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Config{DataDir: dir}).ResolveSecretKey(); err == nil {
		t.Fatal("a corrupt key file must be an error, never silently replaced")
	}
}

func TestLoadRejectsBadKey(t *testing.T) {
	m := valid()
	m["SECRET_ENCRYPTION_KEY"] = "short"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("want error for short key")
	}
}

func TestHTTPAppURLNotSecure(t *testing.T) {
	m := valid()
	m["APP_URL"] = "http://localhost:8084"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.SecureCookies {
		t.Fatal("http app url must not set secure cookies")
	}
}

func TestConcurrencyBounds(t *testing.T) {
	m := valid()
	m["CHECK_CONCURRENCY"] = "0"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("0 must be rejected")
	}
	m["CHECK_CONCURRENCY"] = "5000"
	if _, err := Load(env(m)); err == nil {
		t.Fatal(">2048 must be rejected")
	}
}

func TestAppURLTrailingSlashStripped(t *testing.T) {
	m := valid()
	m["APP_URL"] = "https://uptime.example.com/"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.AppURL != "https://uptime.example.com" {
		t.Fatalf("app url %q", c.AppURL)
	}
}

func TestAppURLInvalidScheme(t *testing.T) {
	m := valid()
	m["APP_URL"] = "ftp://example.com"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("non-http(s) scheme must be rejected")
	}
}

func TestAppURLNotAURL(t *testing.T) {
	m := valid()
	m["APP_URL"] = "not a url"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("garbage app url must be rejected")
	}
}

func TestLogLevelDefaultAndParse(t *testing.T) {
	m := valid()
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != slog.LevelInfo {
		t.Fatalf("default log level %v", c.LogLevel)
	}

	m["LOG_LEVEL"] = "debug"
	c, err = Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != slog.LevelDebug {
		t.Fatalf("log level %v", c.LogLevel)
	}

	m["LOG_LEVEL"] = "bogus"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("bogus log level must be rejected")
	}
}

func TestDisplayTZInvalid(t *testing.T) {
	m := valid()
	m["DISPLAY_TZ"] = "Not/AZone"
	if _, err := Load(env(m)); err == nil {
		t.Fatal("invalid tz must be rejected")
	}
}

func TestMetricsTokenDefaultEmpty(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.MetricsToken != "" {
		t.Fatalf("metrics token %q", c.MetricsToken)
	}
}
