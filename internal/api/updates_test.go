package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"opskest/internal/auth"
	"opskest/internal/store"
	"opskest/internal/update"
)

// ── payload mirror ────────────────────────────────────────────────────

type updatesResp struct {
	Current             string `json:"current"`
	InstallMethod       string `json:"installMethod"`
	CanInstall          bool   `json:"canInstall"`
	CannotInstallReason string `json:"cannotInstallReason"`
	Settings            struct {
		Enabled              bool   `json:"enabled"`
		FeedURL              string `json:"feedUrl"`
		Mode                 string `json:"mode"`
		AutoHour             int    `json:"autoHour"`
		PublicKeySet         bool   `json:"publicKeySet"`
		PublicKeySource      string `json:"publicKeySource"`
		PublicKeyFingerprint string `json:"publicKeyFingerprint"`
	} `json:"settings"`
	Check struct {
		CheckedAt         *int64 `json:"checkedAt"`
		Error             string `json:"error"`
		Latest            string `json:"latest"`
		Available         bool   `json:"available"`
		Notes             string `json:"notes"`
		PublishedAt       *int64 `json:"publishedAt"`
		SignatureVerified bool   `json:"signatureVerified"`
		Asset             *struct {
			OS   string `json:"os"`
			Arch string `json:"arch"`
			Size int64  `json:"size"`
		} `json:"asset"`
	} `json:"check"`
	State struct {
		Phase      string `json:"phase"`
		Percent    int    `json:"percent"`
		Error      string `json:"error"`
		BackupName string `json:"backupName"`
	} `json:"state"`
	CanRollback     bool   `json:"canRollback"`
	PreviousVersion string `json:"previousVersion"`
	DockerHint      string `json:"dockerHint"`
}

func getUpdates(t *testing.T, env *testEnv) updatesResp {
	t.Helper()
	r := env.do(http.MethodGet, "/api/updates", nil, "admin")
	if r.status != http.StatusOK {
		t.Fatalf("GET /api/updates: %d %s", r.status, r.body)
	}
	var out updatesResp
	r.json(t, &out)
	return out
}

func updatePubKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, base64.StdEncoding.EncodeToString(pub)
}

// ── access control ────────────────────────────────────────────────────

func TestUpdatesAdminOnly(t *testing.T) {
	env := newTestEnv(t)
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/updates"},
		{http.MethodPut, "/api/updates/settings"},
		{http.MethodPost, "/api/updates/check"},
		{http.MethodPost, "/api/updates/install"},
		{http.MethodPost, "/api/updates/rollback"},
	}
	for _, rt := range routes {
		var body any
		if rt.method == http.MethodPut {
			body = map[string]any{"mode": "off"}
		}
		if r := env.do(rt.method, rt.path, body, ""); r.status != http.StatusUnauthorized {
			t.Errorf("anonymous %s %s = %d, want 401", rt.method, rt.path, r.status)
		}
		if r := env.do(rt.method, rt.path, body, "viewer"); r.status != http.StatusForbidden {
			t.Errorf("viewer %s %s = %d, want 403", rt.method, rt.path, r.status)
		}
	}
	// Nothing the viewer sent may have changed anything.
	if got := getUpdates(t, env); got.Settings.Mode != "notify" {
		t.Fatalf("viewer changed settings: %+v", got.Settings)
	}
}

func TestUpdatesRequireCSRF(t *testing.T) {
	env := newTestEnv(t)
	req, _ := http.NewRequest(http.MethodPost, env.srv.URL+"/api/updates/check", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieSession, Value: env.adminSID})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without CSRF header = %d, want 403", resp.StatusCode)
	}
}

// ── GET / PUT ─────────────────────────────────────────────────────────

func TestUpdatesDefaults(t *testing.T) {
	env := newTestEnv(t)
	got := getUpdates(t, env)
	if got.Current != "test" {
		t.Errorf("current = %q", got.Current)
	}
	if got.InstallMethod != "binary" && got.InstallMethod != "docker" && got.InstallMethod != "other" {
		t.Errorf("installMethod = %q", got.InstallMethod)
	}
	if got.CanInstall || got.CannotInstallReason == "" {
		t.Errorf("a build without a version and key must not be installable: %+v", got)
	}
	s := got.Settings
	if !s.Enabled || s.FeedURL != "" || s.Mode != "notify" || s.AutoHour != 3 || s.PublicKeySet {
		t.Errorf("settings = %+v", s)
	}
	if got.Check.CheckedAt != nil || got.Check.PublishedAt != nil || got.Check.Asset != nil || got.Check.Available || got.Check.Latest != "" {
		t.Errorf("check should be empty: %+v", got.Check)
	}
	if got.State.Phase != "idle" || got.State.Percent != 0 || got.State.Error != "" {
		t.Errorf("state = %+v", got.State)
	}
	if got.CanRollback {
		t.Error("canRollback without .prev")
	}
	// Raw JSON keeps the contract's null for the unset timestamps.
	r := env.do(http.MethodGet, "/api/updates", nil, "admin")
	if !bytes.Contains(r.body, []byte(`"checkedAt":null`)) || !bytes.Contains(r.body, []byte(`"asset":null`)) {
		t.Errorf("raw body: %s", r.body)
	}
}

func TestUpdatesSettingsValidation(t *testing.T) {
	env := newTestEnv(t)
	cases := []struct {
		name  string
		body  map[string]any
		field string
	}{
		{"http feed", map[string]any{"feedUrl": "http://example.com/m.json"}, "feedUrl"},
		{"lookalike loopback feed", map[string]any{"feedUrl": "http://127.0.0.1.evil.com/m.json"}, "feedUrl"},
		{"ftp feed", map[string]any{"feedUrl": "ftp://example.com/m.json"}, "feedUrl"},
		{"credentials in feed", map[string]any{"feedUrl": "https://u:p@example.com/m.json"}, "feedUrl"},
		{"junk feed", map[string]any{"feedUrl": "not a url"}, "feedUrl"},
		{"bad mode", map[string]any{"mode": "sometimes"}, "mode"},
		{"hour 24", map[string]any{"autoHour": 24}, "autoHour"},
		{"hour -1", map[string]any{"autoHour": -1}, "autoHour"},
		{"key not base64", map[string]any{"publicKey": "!!!!"}, "publicKey"},
		{"key wrong length", map[string]any{"publicKey": base64.StdEncoding.EncodeToString(make([]byte, 31))}, "publicKey"},
	}
	for _, c := range cases {
		r := env.do(http.MethodPut, "/api/updates/settings", c.body, "admin")
		if r.status != http.StatusBadRequest {
			t.Errorf("%s: status = %d body %s", c.name, r.status, r.body)
			continue
		}
		var e struct {
			Error  string            `json:"error"`
			Fields map[string]string `json:"fields"`
		}
		r.json(t, &e)
		if e.Error == "" || e.Fields[c.field] == "" {
			t.Errorf("%s: want field error on %q, got %s", c.name, c.field, r.body)
		}
	}
	// Several problems are reported together and nothing is stored.
	r := env.do(http.MethodPut, "/api/updates/settings", map[string]any{"feedUrl": "http://x", "mode": "zzz", "autoHour": 99}, "admin")
	var e struct{ Fields map[string]string }
	r.json(t, &e)
	if r.status != 400 || len(e.Fields) != 3 {
		t.Errorf("multi-error: %d %s", r.status, r.body)
	}
	if got := getUpdates(t, env); got.Settings.Mode != "notify" || got.Settings.FeedURL != "" || got.Settings.AutoHour != 3 {
		t.Errorf("rejected PUT changed settings: %+v", got.Settings)
	}
	if r := env.doRaw(http.MethodPut, "/api/updates/settings", "application/json", []byte("{nope"), "admin"); r.status != http.StatusBadRequest {
		t.Errorf("bad JSON = %d", r.status)
	}
}

func TestUpdatesSettingsRoundTripKeyNeverReturned(t *testing.T) {
	env := newTestEnv(t)
	_, keyB64 := updatePubKey(t)
	feed := "https://updates.example.com/opskest/manifest.json?token=SECRETTOKEN"

	r := env.do(http.MethodPut, "/api/updates/settings", map[string]any{
		"enabled": false, "feedUrl": feed, "mode": "auto", "autoHour": 22, "publicKey": keyB64,
	}, "admin")
	if r.status != http.StatusOK {
		t.Fatalf("PUT = %d %s", r.status, r.body)
	}
	if bytes.Contains(r.body, []byte(keyB64)) {
		t.Fatal("PUT response contains the public key")
	}
	var put updatesResp
	r.json(t, &put)
	s := put.Settings
	if s.Enabled || s.FeedURL != feed || s.Mode != "auto" || s.AutoHour != 22 || !s.PublicKeySet || s.PublicKeySource != "settings" {
		t.Fatalf("PUT payload settings = %+v", s)
	}
	if len(s.PublicKeyFingerprint) != 8 {
		t.Fatalf("fingerprint = %q", s.PublicKeyFingerprint)
	}

	g := env.do(http.MethodGet, "/api/updates", nil, "admin")
	if bytes.Contains(g.body, []byte(keyB64)) {
		t.Fatal("GET response contains the public key")
	}
	var got updatesResp
	g.json(t, &got)
	if got.Settings != s {
		t.Fatalf("GET settings %+v != PUT settings %+v", got.Settings, s)
	}

	// null (and absent) keeps the stored key; other fields still change.
	r = env.do(http.MethodPut, "/api/updates/settings", map[string]any{"publicKey": nil, "mode": "notify"}, "admin")
	if r.status != 200 {
		t.Fatalf("PUT null key = %d %s", r.status, r.body)
	}
	r.json(t, &got)
	if !got.Settings.PublicKeySet || got.Settings.Mode != "notify" || got.Settings.AutoHour != 22 || got.Settings.FeedURL != feed {
		t.Fatalf("null key / partial update: %+v", got.Settings)
	}
	r = env.do(http.MethodPut, "/api/updates/settings", map[string]any{"autoHour": 4}, "admin")
	r.json(t, &got)
	if !got.Settings.PublicKeySet || got.Settings.AutoHour != 4 {
		t.Fatalf("absent key: %+v", got.Settings)
	}

	// "" clears it; feed can be cleared too.
	r = env.do(http.MethodPut, "/api/updates/settings", map[string]any{"publicKey": "", "feedUrl": ""}, "admin")
	r.json(t, &got)
	if got.Settings.PublicKeySet || got.Settings.FeedURL != "" || got.Settings.PublicKeyFingerprint != "" {
		t.Fatalf("cleared: %+v", got.Settings)
	}

	// Every change is in the audit log, and the log holds neither the key
	// nor the feed's path or query.
	entries, err := env.store.ListAudit(context.Background(), 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		raw, _ := json.Marshal(e)
		if bytes.Contains(raw, []byte(keyB64)) || bytes.Contains(raw, []byte("SECRETTOKEN")) || bytes.Contains(raw, []byte("manifest.json")) {
			t.Fatalf("audit row leaks secrets: %s", raw)
		}
		if e.Action == "update_settings" {
			n++
			if e.UserID == nil || *e.UserID != env.adminID {
				t.Errorf("audit row without the acting admin: %+v", e)
			}
		}
	}
	if n != 4 {
		t.Fatalf("update_settings audit rows = %d, want 4", n)
	}
}

func TestUpdatesBuildKeyFallback(t *testing.T) {
	env := newTestEnv(t)
	_, keyB64 := updatePubKey(t)
	update.SetBuiltinPublicKey(keyB64)
	defer update.SetBuiltinPublicKey("")

	got := getUpdates(t, env)
	if !got.Settings.PublicKeySet || got.Settings.PublicKeySource != "build" {
		t.Fatalf("build key not reported: %+v", got.Settings)
	}
	// A settings key wins over the build key.
	_, other := updatePubKey(t)
	r := env.do(http.MethodPut, "/api/updates/settings", map[string]any{"publicKey": other}, "admin")
	r.json(t, &got)
	if got.Settings.PublicKeySource != "settings" || got.Settings.PublicKeyFingerprint != update.KeyFingerprint(other) {
		t.Fatalf("settings key does not win: %+v", got.Settings)
	}
	// Clearing it falls back to the build key.
	r = env.do(http.MethodPut, "/api/updates/settings", map[string]any{"publicKey": ""}, "admin")
	r.json(t, &got)
	if got.Settings.PublicKeySource != "build" {
		t.Fatalf("fallback: %+v", got.Settings)
	}
}

// ── a full release stack for check / install / rollback ───────────────

const apiFakeSrc = `package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	os.Exit(3)
}
`

var (
	apiFakeMu    sync.Mutex
	apiFakeCache = map[string][]byte{}
)

func apiFakeBinary(t *testing.T, version string) []byte {
	t.Helper()
	apiFakeMu.Lock()
	defer apiFakeMu.Unlock()
	if b, ok := apiFakeCache[version]; ok {
		return b
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain to build a fake binary")
	}
	dir, err := os.MkdirTemp("", "opskest-api-fake-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fake\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(apiFakeSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "fake")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command(goBin, "build", "-o", out, "-ldflags", "-X main.version="+version, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	apiFakeCache[version] = b
	return b
}

type apiRelease struct {
	t       *testing.T
	env     *testEnv
	mgr     *update.Manager
	srv     *httptest.Server
	priv    ed25519.PrivateKey
	keyB64  string
	exe     string
	oldData []byte
	newData []byte

	mu        sync.Mutex
	manifest  []byte
	sig       string
	backupGo  chan struct{}
	backups   int
	restarts  []string
	restartCh chan struct{}
}

// newAPIRelease swaps the env's server for one whose update manager points
// at a local release server and a temp "install" directory. method ""
// means a plain binary install; "docker" fakes a container.
func newAPIRelease(t *testing.T, method string) *apiRelease {
	t.Helper()
	env := newTestEnv(t)
	rel := &apiRelease{t: t, env: env, restartCh: make(chan struct{}, 4)}
	rel.priv, rel.keyB64 = updatePubKey(t)
	rel.oldData = apiFakeBinary(t, "1.3.9")
	rel.newData = apiFakeBinary(t, "1.4.0")

	dir := t.TempDir()
	rel.exe = filepath.Join(dir, "opskest")
	if runtime.GOOS == "windows" {
		rel.exe += ".exe"
	}
	if err := os.WriteFile(rel.exe, rel.oldData, 0o755); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, _ *http.Request) {
		rel.mu.Lock()
		defer rel.mu.Unlock()
		_, _ = w.Write(rel.manifest)
	})
	mux.HandleFunc("/manifest.json.sig", func(w http.ResponseWriter, _ *http.Request) {
		rel.mu.Lock()
		defer rel.mu.Unlock()
		_, _ = w.Write([]byte(rel.sig))
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(rel.newData)))
		_, _ = w.Write(rel.newData)
	})
	rel.srv = httptest.NewServer(mux)
	t.Cleanup(rel.srv.Close)
	rel.publish("1.4.0")

	envFake := update.Env{Getenv: func(string) string { return "" }, Exists: func(string) bool { return false }, ReadFile: func(string) ([]byte, error) { return nil, os.ErrNotExist }}
	if method == "docker" {
		envFake.Getenv = func(k string) string {
			if k == "OPSKEST_IN_DOCKER" {
				return "1"
			}
			return ""
		}
	}
	opts := update.Options{
		Version: "1.3.9",
		Config: func() update.Config {
			cfg, _ := effectiveUpdateConfig(context.Background(), env.store)
			return cfg
		},
		Backup: func(ctx context.Context) (string, error) {
			rel.mu.Lock()
			gate := rel.backupGo
			rel.backups++
			rel.mu.Unlock()
			if gate != nil {
				<-gate
			}
			return "opskest-20261102-1000.db", nil
		},
		Restart: func(exe string) {
			rel.mu.Lock()
			rel.restarts = append(rel.restarts, exe)
			rel.mu.Unlock()
			rel.restartCh <- struct{}{}
		},
		LoadSaved: func() update.Saved { return update.Saved{} },
		SaveSaved: func(update.Saved) {},
		ExePath:   rel.exe, Env: envFake, RestartDelay: -1,
		CheckWritable: func(string) bool { return true },
		Now:           env.now,
	}
	rel.mgr = update.New(opts)

	// Re-serve the same stack with this manager.
	h := New(Deps{
		Cfg: env.cfg, Store: env.store, Writer: env.writer, Engine: env.engine,
		Auth: auth.New(env.store, env.cfg, env.now), Version: "1.3.9", Started: env.now(),
		Log: env.store.Log, Now: env.now, Checker: env.checker.run, Updates: rel.mgr,
	})
	env.srv = httptest.NewServer(h)
	t.Cleanup(env.srv.Close)

	// feedUrl must be https through the API, so a local http feed goes in
	// through the store (the fetcher allows loopback http).
	us := store.DefaultUpdateSettings()
	us.FeedURL = rel.srv.URL + "/manifest.json"
	if err := env.store.PutUpdateSettings(context.Background(), us); err != nil {
		t.Fatal(err)
	}
	return rel
}

func (rel *apiRelease) setKey() {
	rel.t.Helper()
	r := rel.env.do(http.MethodPut, "/api/updates/settings", map[string]any{"publicKey": rel.keyB64}, "admin")
	if r.status != http.StatusOK {
		rel.t.Fatalf("set key: %d %s", r.status, r.body)
	}
}

func (rel *apiRelease) publish(version string) {
	sum := sha256.Sum256(rel.newData)
	man, _ := json.Marshal(map[string]any{
		"version": version, "publishedAt": "2026-11-02T10:00:00Z", "notes": "## notes\n- fixed things",
		"assets": []map[string]any{{
			"os": runtime.GOOS, "arch": runtime.GOARCH, "url": rel.srv.URL + "/asset",
			"sha256": hex.EncodeToString(sum[:]), "size": len(rel.newData),
		}},
	})
	rel.mu.Lock()
	rel.manifest = man
	rel.sig = base64.StdEncoding.EncodeToString(ed25519.Sign(rel.priv, man))
	rel.mu.Unlock()
}

func (rel *apiRelease) waitPhase(want string) updatesResp {
	rel.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		g := getUpdates(rel.t, rel.env)
		if g.State.Phase == want {
			return g
		}
		if time.Now().After(deadline) {
			rel.t.Fatalf("phase never reached %q, last state %+v", want, g.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (rel *apiRelease) auditActions() []string {
	entries, err := rel.env.store.ListAudit(context.Background(), 100, 0)
	if err != nil {
		rel.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Action)
	}
	return out
}

func hasAction(actions []string, a string) bool {
	for _, x := range actions {
		if x == a {
			return true
		}
	}
	return false
}

func TestUpdatesCheckWithKey(t *testing.T) {
	rel := newAPIRelease(t, "")
	rel.setKey()
	r := rel.env.do(http.MethodPost, "/api/updates/check", nil, "admin")
	if r.status != http.StatusOK {
		t.Fatalf("check = %d %s", r.status, r.body)
	}
	var got updatesResp
	r.json(t, &got)
	c := got.Check
	if c.Error != "" || c.Latest != "1.4.0" || !c.Available || !c.SignatureVerified || c.CheckedAt == nil || c.PublishedAt == nil {
		t.Fatalf("check = %+v", c)
	}
	if !strings.Contains(c.Notes, "fixed things") {
		t.Errorf("notes = %q", c.Notes)
	}
	if c.Asset == nil || c.Asset.OS != runtime.GOOS || c.Asset.Arch != runtime.GOARCH || c.Asset.Size != int64(len(rel.newData)) {
		t.Errorf("asset = %+v", c.Asset)
	}
	if got.Current != "1.3.9" || !got.CanInstall || got.CannotInstallReason != "" || got.InstallMethod != "binary" {
		t.Errorf("payload = %+v", got)
	}
	// The same payload comes from GET afterwards (the result is kept).
	if g := getUpdates(t, rel.env); g.Check.Latest != "1.4.0" {
		t.Errorf("GET after check: %+v", g.Check)
	}
}

func TestUpdatesCheckRateLimit(t *testing.T) {
	rel := newAPIRelease(t, "")
	if r := rel.env.do(http.MethodPost, "/api/updates/check", nil, "admin"); r.status != http.StatusOK {
		t.Fatalf("first check = %d %s", r.status, r.body)
	}
	r := rel.env.do(http.MethodPost, "/api/updates/check", nil, "admin")
	if r.status != http.StatusTooManyRequests || r.header.Get("Retry-After") == "" {
		t.Fatalf("second check = %d (Retry-After %q) %s", r.status, r.header.Get("Retry-After"), r.body)
	}
}

func TestUpdatesCheckWithoutFeedOrWithBadSignature(t *testing.T) {
	env := newTestEnv(t)
	r := env.do(http.MethodPost, "/api/updates/check", nil, "admin")
	if r.status != http.StatusOK {
		t.Fatalf("check with no feed = %d %s", r.status, r.body)
	}
	var got updatesResp
	r.json(t, &got)
	if got.Check.Error == "" || got.Check.Available || got.Check.CheckedAt == nil {
		t.Fatalf("no-feed check = %+v", got.Check)
	}

	rel := newAPIRelease(t, "")
	rel.setKey()
	rel.mu.Lock()
	rel.sig = base64.StdEncoding.EncodeToString(make([]byte, 64))
	rel.mu.Unlock()
	r = rel.env.do(http.MethodPost, "/api/updates/check", nil, "admin")
	r.json(t, &got)
	if r.status != http.StatusOK || got.Check.Error == "" || got.Check.Available || got.Check.Latest != "" || got.Check.SignatureVerified {
		t.Fatalf("a manifest with a bad signature must not be offered: %d %+v", r.status, got.Check)
	}
	// An install attempt re-verifies the manifest itself, fails, and never
	// reaches the backup or the download.
	if r := rel.env.do(http.MethodPost, "/api/updates/install", nil, "admin"); r.status != http.StatusAccepted {
		t.Fatalf("install = %d %s", r.status, r.body)
	}
	failed := rel.waitPhase("failed")
	if failed.State.Error == "" {
		t.Fatal("failed state without an error text")
	}
	rel.mu.Lock()
	defer rel.mu.Unlock()
	if rel.backups != 0 || len(rel.restarts) != 0 || !bytes.Equal(mustRead(t, rel.exe), rel.oldData) {
		t.Fatalf("a bad-signature install changed something: backups=%d restarts=%d", rel.backups, len(rel.restarts))
	}
}

func TestUpdatesInstallRefusedWithoutKey(t *testing.T) {
	rel := newAPIRelease(t, "")
	r := rel.env.do(http.MethodPost, "/api/updates/install", nil, "admin")
	if r.status != http.StatusConflict {
		t.Fatalf("install without key = %d %s", r.status, r.body)
	}
	var e struct{ Error string }
	r.json(t, &e)
	if !strings.Contains(e.Error, "add the release public key in Settings to allow installing from here") {
		t.Fatalf("reason = %q", e.Error)
	}
	if got := getUpdates(t, rel.env); got.CanInstall || got.CannotInstallReason != e.Error || got.State.Phase != "idle" {
		t.Fatalf("payload = %+v", got)
	}
	rel.mu.Lock()
	b := rel.backups
	rel.mu.Unlock()
	if b != 0 {
		t.Fatal("a backup ran")
	}
	if hasAction(rel.auditActions(), "update_install") {
		t.Fatal("a refused install was audited as started")
	}
	// Notify mode still reports the update with no key.
	var got updatesResp
	rel.env.do(http.MethodPost, "/api/updates/check", nil, "admin").json(t, &got)
	if !got.Check.Available || got.Check.SignatureVerified {
		t.Fatalf("notify-only check = %+v", got.Check)
	}
}

func TestUpdatesInstallAndRollbackEndToEnd(t *testing.T) {
	rel := newAPIRelease(t, "")
	rel.setKey()
	rel.env.do(http.MethodPost, "/api/updates/check", nil, "admin")

	rel.mu.Lock()
	rel.backupGo = make(chan struct{})
	rel.mu.Unlock()

	r := rel.env.do(http.MethodPost, "/api/updates/install", nil, "admin")
	if r.status != http.StatusAccepted {
		t.Fatalf("install = %d %s", r.status, r.body)
	}
	// The install is held in the backup step: a second one is a 409, and
	// so is a check and a rollback.
	deadline := time.Now().Add(10 * time.Second)
	for {
		rel.mu.Lock()
		held := rel.backups > 0
		rel.mu.Unlock()
		if held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("install never reached the backup")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if g := getUpdates(t, rel.env); g.State.Phase != "backing_up" {
		t.Fatalf("phase = %q", g.State.Phase)
	}
	for _, p := range []string{"/api/updates/install", "/api/updates/rollback", "/api/updates/check"} {
		if r := rel.env.do(http.MethodPost, p, nil, "admin"); r.status != http.StatusConflict {
			t.Errorf("%s while busy = %d %s", p, r.status, r.body)
		}
	}
	close(rel.backupGo)
	select {
	case <-rel.restartCh:
	case <-time.After(30 * time.Second):
		t.Fatal("restart was never requested")
	}
	got := rel.waitPhase("restarting")
	if got.State.Error != "" {
		t.Fatalf("state = %+v", got.State)
	}
	if !bytes.Equal(mustRead(t, rel.exe), rel.newData) || !bytes.Equal(mustRead(t, rel.exe+".prev"), rel.oldData) {
		t.Fatal("binaries not swapped / .prev not kept")
	}
	rel.mu.Lock()
	nBackups, nRestarts := rel.backups, len(rel.restarts)
	rel.mu.Unlock()
	if nBackups != 1 || nRestarts != 1 {
		t.Fatalf("backups=%d restarts=%d", nBackups, nRestarts)
	}
	after := getUpdates(t, rel.env)
	if !after.CanRollback || after.PreviousVersion != "1.3.9" || after.State.BackupName != "opskest-20261102-1000.db" {
		t.Fatalf("after install: canRollback=%v previousVersion=%q backupName=%q", after.CanRollback, after.PreviousVersion, after.State.BackupName)
	}
	if !hasAction(rel.auditActions(), "update_install") {
		t.Fatalf("install not audited: %v", rel.auditActions())
	}
}

// Rollback through the API: a kept .prev (from an earlier update) is swapped
// back and the process asked to restart.
func TestUpdatesRollbackAPI(t *testing.T) {
	rel := newAPIRelease(t, "")
	prev := apiFakeBinary(t, "1.2.0")
	if err := os.WriteFile(rel.exe+".prev", prev, 0o755); err != nil {
		t.Fatal(err)
	}
	if !getUpdates(t, rel.env).CanRollback {
		t.Fatal("canRollback false with a .prev present")
	}
	r := rel.env.do(http.MethodPost, "/api/updates/rollback", nil, "admin")
	if r.status != http.StatusAccepted {
		t.Fatalf("rollback = %d %s", r.status, r.body)
	}
	select {
	case <-rel.restartCh:
	case <-time.After(30 * time.Second):
		t.Fatal("rollback restart was never requested")
	}
	rel.waitPhase("restarting")
	if !bytes.Equal(mustRead(t, rel.exe), prev) || !bytes.Equal(mustRead(t, rel.exe+".prev"), rel.oldData) {
		t.Fatal("rollback did not swap the binaries")
	}
	if !hasAction(rel.auditActions(), "update_rollback") {
		t.Fatalf("rollback not audited: %v", rel.auditActions())
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUpdatesInstallNothingNewIs409(t *testing.T) {
	rel := newAPIRelease(t, "")
	rel.setKey()
	rel.publish("1.3.9")
	rel.env.do(http.MethodPost, "/api/updates/check", nil, "admin")
	r := rel.env.do(http.MethodPost, "/api/updates/install", nil, "admin")
	if r.status != http.StatusConflict {
		t.Fatalf("install when up to date = %d %s", r.status, r.body)
	}
}

func TestUpdatesRollbackWithoutPrevIs409(t *testing.T) {
	rel := newAPIRelease(t, "")
	r := rel.env.do(http.MethodPost, "/api/updates/rollback", nil, "admin")
	if r.status != http.StatusConflict {
		t.Fatalf("rollback with no .prev = %d %s", r.status, r.body)
	}
}

func TestUpdatesDockerNeverSelfInstalls(t *testing.T) {
	rel := newAPIRelease(t, "docker")
	rel.setKey()
	got := getUpdates(t, rel.env)
	if got.InstallMethod != "docker" || got.CanInstall || got.DockerHint != "docker compose pull && docker compose up -d" {
		t.Fatalf("payload = %+v", got)
	}
	if !strings.Contains(got.CannotInstallReason, "Docker") {
		t.Fatalf("reason = %q", got.CannotInstallReason)
	}
	// Checks still work (notify), install and rollback do not.
	var chk updatesResp
	rel.env.do(http.MethodPost, "/api/updates/check", nil, "admin").json(t, &chk)
	if !chk.Check.Available || !chk.Check.SignatureVerified {
		t.Fatalf("docker check = %+v", chk.Check)
	}
	for _, p := range []string{"/api/updates/install", "/api/updates/rollback"} {
		if r := rel.env.do(http.MethodPost, p, nil, "admin"); r.status != http.StatusConflict {
			t.Errorf("%s in docker = %d %s", p, r.status, r.body)
		}
	}
	rel.mu.Lock()
	defer rel.mu.Unlock()
	if rel.backups != 0 || len(rel.restarts) != 0 {
		t.Fatal("docker install touched backup/restart")
	}
	if !bytes.Equal(mustRead(t, rel.exe), rel.oldData) {
		t.Fatal("binary changed in docker")
	}
}

func TestUpdatesNoDockerHintOutsideDocker(t *testing.T) {
	rel := newAPIRelease(t, "")
	r := rel.env.do(http.MethodGet, "/api/updates", nil, "admin")
	if bytes.Contains(r.body, []byte("dockerHint")) {
		t.Fatalf("dockerHint present for a binary install: %s", r.body)
	}
}

func TestUpdatesCheckRefusedWhenDisabled(t *testing.T) {
	rel := newAPIRelease(t, "")
	if r := rel.env.do(http.MethodPut, "/api/updates/settings", map[string]any{"enabled": false}, "admin"); r.status != 200 {
		t.Fatalf("PUT = %d %s", r.status, r.body)
	}
	r := rel.env.do(http.MethodPost, "/api/updates/check", nil, "admin")
	if r.status != http.StatusConflict {
		t.Fatalf("check while disabled = %d %s", r.status, r.body)
	}
	if got := getUpdates(t, rel.env); got.Check.CheckedAt != nil || got.Check.Latest != "" {
		t.Fatalf("a disabled check recorded something: %+v", got.Check)
	}
	var raw updatesResp
	rel.env.do(http.MethodGet, "/api/updates", nil, "admin").json(t, &raw)
	if raw.PreviousVersion != "" {
		t.Fatalf("previousVersion = %q", raw.PreviousVersion)
	}
}

// A loopback http feed can be saved (local test feeds), and the payload
// carries the restart and scheduling fields the Settings screen uses.
func TestUpdatesLoopbackFeedAndStatusFields(t *testing.T) {
	env := newTestEnv(t)
	r := env.do(http.MethodPut, "/api/updates/settings", map[string]any{"feedUrl": "http://127.0.0.1:18990/manifest.json", "enabled": true, "mode": "notify"}, "admin")
	if r.status != http.StatusOK {
		t.Fatalf("PUT loopback feed = %d %s", r.status, r.body)
	}
	var raw map[string]any
	r.json(t, &raw)
	for _, k := range []string{"restartMode", "restartHint", "nextCheckAt", "autoSkipVersion", "lastInstallFailedAt"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("payload has no %q", k)
		}
	}
	if raw["restartMode"] != "self" || raw["restartHint"] == "" {
		t.Errorf("restart fields: %v %v", raw["restartMode"], raw["restartHint"])
	}
	// The audit row names the host only.
	g := env.do(http.MethodGet, "/api/audit-log?limit=5", nil, "admin")
	if !bytes.Contains(g.body, []byte("update_settings")) || !bytes.Contains(g.body, []byte("feed=127.0.0.1:18990")) || bytes.Contains(g.body, []byte("manifest.json")) {
		t.Errorf("audit: %s", g.body)
	}
}
