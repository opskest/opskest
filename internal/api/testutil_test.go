package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"opskest/internal/alert"
	"opskest/internal/auth"
	"opskest/internal/config"
	"opskest/internal/engine"
	"opskest/internal/hub"
	"opskest/internal/model"
	"opskest/internal/notify"
	"opskest/internal/scheduler"
	"opskest/internal/secret"
	"opskest/internal/store"
)

// checkerBox is a scriptable, inspectable stand-in for check.Run: tests
// set fn to control what a check "finds" and read calls to see exactly
// which *model.Monitor (headers, secrets and all) the handler built.
type checkerBox struct {
	fn    func(ctx context.Context, m *model.Monitor) model.Result
	calls []*model.Monitor
}

func (c *checkerBox) run(ctx context.Context, m *model.Monitor) model.Result {
	c.calls = append(c.calls, m)
	if c.fn != nil {
		return c.fn(ctx, m)
	}
	return model.Result{OK: true, LatencyMs: 5}
}

// fakeLookup adapts engine.AlertInfo to alert.Lookup, mirroring the
// production adapter in internal/app/app.go.
type fakeLookup struct{ eng *engine.Engine }

func (l *fakeLookup) AlertInfo(id int64) (alert.Info, bool) {
	info, ok := l.eng.AlertInfo(id)
	if !ok {
		return alert.Info{}, false
	}
	return alert.Info{
		Name: info.Name, Group: info.Group, Target: info.Target,
		Runbook: info.Runbook, ChannelIDs: info.ChannelIDs,
	}, true
}

// testEnv is a full API stack — real store, real writer, real engine, real
// dispatcher, real hub — wired the same way internal/app.Run wires them,
// but served over httptest with a fake OIDC provider and a scriptable
// checker so tests never touch the network. Every sub-test gets its own
// temp SQLite database.
type testEnv struct {
	t *testing.T

	srv     *httptest.Server
	store   *store.Store
	writer  *store.Writer
	engine  *engine.Engine
	alerts  *alert.Dispatcher
	cfg     *config.Config
	checker *checkerBox
	now     func() time.Time

	// Subscriber notifications: a real notifier delivering to fakes.
	notifier *notify.Notifier
	mail     *fakeMail
	slack    *fakeSlack

	adminID, userID, viewerID int64
	adminSID, adminCSRF       string
	userSID, userCSRF         string
	viewerSID, viewerCSRF     string

	// policy is the permission table the server registered its routes with.
	policy []routePolicy
}

// newTestEnv builds a testEnv with an admin and a viewer session already
// minted. cancel is called automatically at test cleanup.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	key := make([]byte, 32)
	for i := range key {
		key[i] = 0x42
	}
	box, err := secret.New(key)
	if err != nil {
		t.Fatalf("secret.New: %v", err)
	}
	loc := time.FixedZone("IST", 19800)
	st, err := store.Open(t.TempDir(), box, loc)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w := st.NewWriter(log)
	ctx, cancel := context.WithCancel(context.Background())
	w.Start(ctx)
	t.Cleanup(func() {
		cancel()
		w.Close()
	})

	nowFn := time.Now

	checker := &checkerBox{}
	eng := engine.New(engine.Deps{
		Store: st, Writer: w, Now: nowFn, Log: log, Checker: checker.run,
	})
	if err := eng.Load(context.Background()); err != nil {
		t.Fatalf("engine.Load: %v", err)
	}

	dispatcher := alert.NewDispatcher(st, w, &fakeLookup{eng: eng}, alert.Options{
		AppURL: "http://app.invalid",
		Now:    nowFn,
		Log:    log,
		// Channel tests deliver to an httptest.NewTLSServer (a self-signed
		// cert), so the dispatcher's HTTP client must not verify it. This
		// only ever runs against test servers.
		HTTP: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}},
		Settings: func() (int, time.Duration) {
			return 5, 30 * time.Second
		},
	})
	dispatcher.Start(ctx)
	t.Cleanup(dispatcher.Stop)

	h := hub.New(eng, "test", nowFn, log)
	go h.Run(ctx)

	backupDir := t.TempDir()
	cfg := &config.Config{
		AppURL:           "http://app.invalid",
		ListenAddr:       "127.0.0.1:0",
		DataDir:          t.TempDir(),
		BackupDir:        backupDir,
		SecretKey:        key,
		CheckConcurrency: 10,
		DisplayTZ:        loc,
		SecureCookies:    false,
	}

	a := auth.New(st, cfg, nowFn)

	schedStats := func() scheduler.Stats { return scheduler.Stats{} }

	mail, slack := &fakeMail{}, &fakeSlack{}
	notifier := notify.New(st, mail, slack, cfg.AppURL, log)
	notifier.Start(ctx)

	handler, policy := build(Deps{
		Cfg:        cfg,
		Store:      st,
		Writer:     w,
		Engine:     eng,
		Hub:        h,
		Auth:       a,
		Alerts:     dispatcher,
		SchedStats: schedStats,
		Version:    "test",
		Started:    nowFn(),
		Log:        log,
		Now:        nowFn,
		Checker:    checker.run,
		Notify:     notifier,
		SlackPost:  slack.PostJSON,
	})

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	env := &testEnv{
		t: t, srv: srv, store: st, writer: w, engine: eng, alerts: dispatcher,
		cfg: cfg, checker: checker, now: nowFn, policy: policy,
		notifier: notifier, mail: mail, slack: slack,
	}

	uctx := context.Background()
	admin, err := st.CreateUser(uctx, "admin@example.com", "Admin", store.RoleViewer, nowFn())
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := st.SetUserRole(uctx, admin.ID, store.RoleAdmin); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	env.adminID = admin.ID
	env.adminSID, env.adminCSRF, err = a.MintSession(uctx, admin.ID)
	if err != nil {
		t.Fatalf("mint admin session: %v", err)
	}

	operator, err := st.CreateUser(uctx, "user@example.com", "User", store.RoleUser, nowFn())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	env.userID = operator.ID
	env.userSID, env.userCSRF, err = a.MintSession(uctx, operator.ID)
	if err != nil {
		t.Fatalf("mint user session: %v", err)
	}

	viewer, err := st.CreateUser(uctx, "viewer@example.com", "Viewer", store.RoleViewer, nowFn())
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	env.viewerID = viewer.ID
	env.viewerSID, env.viewerCSRF, err = a.MintSession(uctx, viewer.ID)
	if err != nil {
		t.Fatalf("mint viewer session: %v", err)
	}

	return env
}

// apiResponse is the result of a do() call.
type apiResponse struct {
	status int
	header http.Header
	body   []byte
}

// json unmarshals the response body into v, failing the test on error.
func (r *apiResponse) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("unmarshal response body: %v\nbody: %s", err, r.body)
	}
}

// do issues an HTTP request against the test server. as selects the
// session to send: "admin", "user", "viewer" or "" for no session at all. body is
// marshaled as the JSON request body when non-nil.
func (e *testEnv) do(method, path string, body any, as string) *apiResponse {
	e.t.Helper()

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, e.srv.URL+path, reader)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	var sid, csrf string
	switch as {
	case "admin":
		sid, csrf = e.adminSID, e.adminCSRF
	case "user":
		sid, csrf = e.userSID, e.userCSRF
	case "viewer":
		sid, csrf = e.viewerSID, e.viewerCSRF
	case "":
		// anonymous
	default:
		e.t.Fatalf("do: unknown role %q", as)
	}
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieSession, Value: sid})
		req.AddCookie(&http.Cookie{Name: auth.CookieCSRF, Value: csrf})
		req.Header.Set(auth.HeaderCSRF, csrf)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read response body: %v", err)
	}
	return &apiResponse{status: resp.StatusCode, header: resp.Header, body: b}
}

// doRaw is like do, but sends body verbatim under the given Content-Type
// instead of JSON-marshaling it — used for CSV bodies and multipart
// uploads, neither of which is JSON.
func (e *testEnv) doRaw(method, path, contentType string, body []byte, as string) *apiResponse {
	e.t.Helper()

	req, err := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	var sid, csrf string
	switch as {
	case "admin":
		sid, csrf = e.adminSID, e.adminCSRF
	case "user":
		sid, csrf = e.userSID, e.userCSRF
	case "viewer":
		sid, csrf = e.viewerSID, e.viewerCSRF
	case "":
		// anonymous
	default:
		e.t.Fatalf("doRaw: unknown role %q", as)
	}
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieSession, Value: sid})
		req.AddCookie(&http.Cookie{Name: auth.CookieCSRF, Value: csrf})
		req.Header.Set(auth.HeaderCSRF, csrf)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("doRaw %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read response body: %v", err)
	}
	return &apiResponse{status: resp.StatusCode, header: resp.Header, body: b}
}

// createMonitor is a small helper: it POSTs in as admin and fails the test
// on anything but 201, returning the decoded MonitorOut.
func (e *testEnv) createMonitor(in MonitorIn) MonitorOut {
	e.t.Helper()
	resp := e.do(http.MethodPost, "/api/monitors", in, "admin")
	if resp.status != http.StatusCreated {
		e.t.Fatalf("createMonitor: status = %d, body = %s", resp.status, resp.body)
	}
	var out MonitorOut
	resp.json(e.t, &out)
	return out
}

// httpMonitorIn returns a minimal valid HTTP MonitorIn targeting an
// httptest server URL.
func httpMonitorIn(name, target string) MonitorIn {
	in := MonitorIn{
		Name:           name,
		Type:           model.TypeHTTP,
		Target:         target,
		IntervalS:      60,
		RetryIntervalS: 20,
		Retries:        2,
		TimeoutMs:      5000,
		SSLWarnDays:    14,
	}
	in.Settings.Method = "GET"
	in.Settings.BodyType = "none"
	in.Settings.AuthType = "none"
	in.Settings.AcceptedCodes = "200-299"
	return in
}

// fakeMail records every message the notifier sends.
type fakeMail struct {
	mu   sync.Mutex
	sent []sentMail
}

type sentMail struct {
	to  []string
	msg string
}

func (f *fakeMail) SendMail(_ context.Context, _ store.ChannelConfig, rcpts []string, msg []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMail{to: rcpts, msg: string(msg)})
	return nil
}

func (f *fakeMail) take() []sentMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

// fakeSlack records webhook posts; a non-empty failWith makes posts fail
// with that error text (e.g. "http 410: no_service").
type fakeSlack struct {
	mu       sync.Mutex
	posts    []string
	failWith string
}

func (f *fakeSlack) PostJSON(_ context.Context, url string, payload any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWith != "" {
		return errors.New(f.failWith)
	}
	b, _ := json.Marshal(payload)
	f.posts = append(f.posts, url+" "+string(b))
	return nil
}

func (f *fakeSlack) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.posts
	f.posts = nil
	return out
}
