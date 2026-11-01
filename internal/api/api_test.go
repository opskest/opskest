package api

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

// TestCreateGetUpdateDeleteMonitor drives the full monitor CRUD lifecycle
// as admin and checks the store, the API responses and Engine.Summary
// agree at every step.
func TestCreateGetUpdateDeleteMonitor(t *testing.T) {
	env := newTestEnv(t)

	out := env.createMonitor(httpMonitorIn("api monitor", "https://example.com/health"))
	if out.ID == 0 {
		t.Fatal("createMonitor: got id 0")
	}
	if _, ok := env.engine.Summary(out.ID); !ok {
		t.Fatal("Engine.Summary missing after create")
	}

	// GET returns the same fields.
	getResp := env.do(http.MethodGet, "/api/monitors/"+itoa(out.ID), nil, "admin")
	if getResp.status != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", getResp.status, getResp.body)
	}
	var got MonitorOut
	getResp.json(t, &got)
	if got.Name != out.Name || got.Target != out.Target {
		t.Fatalf("GET mismatch: got %+v, want name/target from %+v", got, out)
	}

	// PUT changes the name.
	upd := httpMonitorIn("renamed monitor", "https://example.com/health")
	putResp := env.do(http.MethodPut, "/api/monitors/"+itoa(out.ID), upd, "admin")
	if putResp.status != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", putResp.status, putResp.body)
	}
	var updated MonitorOut
	putResp.json(t, &updated)
	if updated.Name != "renamed monitor" {
		t.Fatalf("PUT: name = %q, want %q", updated.Name, "renamed monitor")
	}
	if sum, ok := env.engine.Summary(out.ID); !ok || sum.Name != "renamed monitor" {
		t.Fatalf("Engine.Summary after PUT: %+v ok=%v", sum, ok)
	}

	// DELETE -> 204, then GET -> 404, and the engine forgets it.
	delResp := env.do(http.MethodDelete, "/api/monitors/"+itoa(out.ID), nil, "admin")
	if delResp.status != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, body = %s", delResp.status, delResp.body)
	}
	if resp := env.do(http.MethodGet, "/api/monitors/"+itoa(out.ID), nil, "admin"); resp.status != http.StatusNotFound {
		t.Fatalf("GET after delete: status = %d, want 404", resp.status)
	}
	if _, ok := env.engine.Summary(out.ID); ok {
		t.Fatal("Engine.Summary still present after delete")
	}
}

// TestMonitorValidationErrors proves a bad body gets a 400 with per-field
// messages, not a generic error.
func TestMonitorValidationErrors(t *testing.T) {
	env := newTestEnv(t)

	in := httpMonitorIn("", "not-a-url")
	resp := env.do(http.MethodPost, "/api/monitors", in, "admin")
	if resp.status != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var body struct {
		Error  string            `json:"error"`
		Fields map[string]string `json:"fields"`
	}
	resp.json(t, &body)
	if _, ok := body.Fields["target"]; !ok {
		t.Errorf("fields = %+v, want a \"target\" entry", body.Fields)
	}
	if _, ok := body.Fields["name"]; !ok {
		t.Errorf("fields = %+v, want a \"name\" entry", body.Fields)
	}
}

// TestViewerCannotMutate proves every mutating monitor route needs admin,
// including pause/resume, while GETs work for a viewer.
func TestViewerCannotMutate(t *testing.T) {
	env := newTestEnv(t)
	out := env.createMonitor(httpMonitorIn("viewer target", "https://example.com/health"))
	id := itoa(out.ID)

	cases := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/monitors", httpMonitorIn("x", "https://example.com")},
		{http.MethodPut, "/api/monitors/" + id, httpMonitorIn("x", "https://example.com")},
		{http.MethodDelete, "/api/monitors/" + id, nil},
		{http.MethodPost, "/api/monitors/" + id + "/pause", nil},
		{http.MethodPost, "/api/monitors/" + id + "/resume", nil},
		{http.MethodPost, "/api/monitors/" + id + "/duplicate", nil},
		{http.MethodPost, "/api/monitors/bulk", map[string]any{"ids": []int64{out.ID}, "action": "pause"}},
	}
	for _, c := range cases {
		resp := env.do(c.method, c.path, c.body, "viewer")
		if resp.status != http.StatusForbidden {
			t.Errorf("%s %s as viewer: status = %d, want 403, body = %s", c.method, c.path, resp.status, resp.body)
		}
	}

	if resp := env.do(http.MethodGet, "/api/monitors", nil, "viewer"); resp.status != http.StatusOK {
		t.Errorf("GET /api/monitors as viewer: status = %d", resp.status)
	}
	if resp := env.do(http.MethodGet, "/api/monitors/"+id, nil, "viewer"); resp.status != http.StatusOK {
		t.Errorf("GET /api/monitors/{id} as viewer: status = %d", resp.status)
	}
}

// TestMonitorGetMasksSecrets proves a secret header value and a bearer
// token never appear anywhere in the GET response body, and isSet is true.
func TestMonitorGetMasksSecrets(t *testing.T) {
	env := newTestEnv(t)

	const secretHeaderVal = "s3cr3t-header-value"
	const bearerVal = "s3cr3t-bearer-token"

	in := httpMonitorIn("secret monitor", "https://example.com/health")
	in.Settings.AuthType = "bearer"
	in.Secrets.BearerToken = strPtr(bearerVal)
	hv := secretHeaderVal
	in.Headers = []HeaderIn{{Name: "X-Api-Key", Value: &hv, Secret: true}}

	out := env.createMonitor(in)

	resp := env.do(http.MethodGet, "/api/monitors/"+itoa(out.ID), nil, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("GET status = %d, body = %s", resp.status, resp.body)
	}
	if bytesContains(resp.body, secretHeaderVal) {
		t.Error("GET response leaks the secret header value")
	}
	if bytesContains(resp.body, bearerVal) {
		t.Error("GET response leaks the bearer token")
	}

	var got MonitorOut
	resp.json(t, &got)
	if !got.Secrets.BearerToken.IsSet {
		t.Error("secrets.bearerToken.isSet = false, want true")
	}
	found := false
	for _, h := range got.Headers {
		if h.Name == "X-Api-Key" {
			found = true
			if !h.IsSet {
				t.Error("header isSet = false, want true")
			}
			if h.Value != "" {
				t.Errorf("header value leaked in response: %q", h.Value)
			}
		}
	}
	if !found {
		t.Fatal("secret header missing from GET response")
	}
}

// TestUpdateKeepsSecretsWhenNull proves PUT with value:null / bearerToken:
// null keeps the previously stored secrets unchanged.
func TestUpdateKeepsSecretsWhenNull(t *testing.T) {
	env := newTestEnv(t)

	const secretHeaderVal = "s3cr3t-header-value"
	const bearerVal = "s3cr3t-bearer-token"

	in := httpMonitorIn("keep secrets", "https://example.com/health")
	in.Settings.AuthType = "bearer"
	in.Secrets.BearerToken = strPtr(bearerVal)
	hv := secretHeaderVal
	in.Headers = []HeaderIn{{Name: "X-Api-Key", Value: &hv, Secret: true}}
	out := env.createMonitor(in)

	// PUT with both secrets explicitly null (Go's zero value for *string).
	upd := httpMonitorIn("keep secrets", "https://example.com/health")
	upd.Settings.AuthType = "bearer"
	upd.Headers = []HeaderIn{{Name: "X-Api-Key", Value: nil, Secret: true}}
	resp := env.do(http.MethodPut, "/api/monitors/"+itoa(out.ID), upd, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("PUT status = %d, body = %s", resp.status, resp.body)
	}

	stored, err := env.store.GetMonitor(context.Background(), out.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if stored.Secrets.BearerToken != bearerVal {
		t.Errorf("stored bearer token = %q, want unchanged %q", stored.Secrets.BearerToken, bearerVal)
	}
	if stored.Secrets.Headers["X-Api-Key"] != secretHeaderVal {
		t.Errorf("stored header secret = %q, want unchanged %q", stored.Secrets.Headers["X-Api-Key"], secretHeaderVal)
	}
}

// TestTestNowMergesStoredSecrets proves POST /api/monitors/test, given an
// id and a null secret, hands the checker the stored secret rather than an
// empty one.
func TestTestNowMergesStoredSecrets(t *testing.T) {
	env := newTestEnv(t)

	const bearerVal = "s3cr3t-bearer-token"
	in := httpMonitorIn("test-now monitor", "https://example.com/health")
	in.Settings.AuthType = "bearer"
	in.Secrets.BearerToken = strPtr(bearerVal)
	out := env.createMonitor(in)

	env.checker.fn = func(ctx context.Context, m *model.Monitor) model.Result {
		return model.Result{OK: true, LatencyMs: 1}
	}

	body := map[string]any{
		"name": "test-now monitor", "type": "http", "target": "https://example.com/health",
		"intervalS": 60, "retryIntervalS": 20, "retries": 2, "timeoutMs": 5000, "sslWarnDays": 14,
		"settings": map[string]any{"method": "GET", "bodyType": "none", "authType": "bearer", "acceptedCodes": "200-299"},
		"secrets":  map[string]any{"basicPass": nil, "bearerToken": nil},
		"headers":  []any{},
		"id":       out.ID,
	}
	resp := env.do(http.MethodPost, "/api/monitors/test", body, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	if len(env.checker.calls) == 0 {
		t.Fatal("checker was never called")
	}
	last := env.checker.calls[len(env.checker.calls)-1]
	if last.Secrets.BearerToken != bearerVal {
		t.Errorf("checker saw bearer token %q, want stored %q", last.Secrets.BearerToken, bearerVal)
	}
}

// TestDuplicateIsPausedCopy proves Duplicate creates a paused copy named
// "Copy of <name>" and carries the secrets over.
func TestDuplicateIsPausedCopy(t *testing.T) {
	env := newTestEnv(t)
	const bearerVal = "dup-secret"
	in := httpMonitorIn("original", "https://example.com/health")
	in.Settings.AuthType = "bearer"
	in.Secrets.BearerToken = strPtr(bearerVal)
	out := env.createMonitor(in)

	resp := env.do(http.MethodPost, "/api/monitors/"+itoa(out.ID)+"/duplicate", nil, "admin")
	if resp.status != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	resp.json(t, &created)
	if created.ID == out.ID || created.ID == 0 {
		t.Fatalf("duplicate id = %d, original = %d", created.ID, out.ID)
	}

	dup, err := env.store.GetMonitor(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetMonitor: %v", err)
	}
	if !dup.Paused {
		t.Error("duplicate is not paused")
	}
	if dup.Name != "Copy of original" {
		t.Errorf("duplicate name = %q, want %q", dup.Name, "Copy of original")
	}
	if dup.Secrets.BearerToken != bearerVal {
		t.Errorf("duplicate bearer token = %q, want %q", dup.Secrets.BearerToken, bearerVal)
	}
}

// TestBulkPauseAndMove exercises the bulk pause/resume/move actions.
func TestBulkPauseAndMove(t *testing.T) {
	env := newTestEnv(t)
	m1 := env.createMonitor(httpMonitorIn("bulk 1", "https://example.com/a"))
	m2 := env.createMonitor(httpMonitorIn("bulk 2", "https://example.com/b"))

	pauseResp := env.do(http.MethodPost, "/api/monitors/bulk", map[string]any{
		"ids": []int64{m1.ID, m2.ID}, "action": "pause",
	}, "admin")
	if pauseResp.status != http.StatusOK {
		t.Fatalf("bulk pause: status = %d, body = %s", pauseResp.status, pauseResp.body)
	}
	for _, id := range []int64{m1.ID, m2.ID} {
		if sum, ok := env.engine.Summary(id); !ok || !sum.Paused {
			t.Errorf("monitor %d: paused = %v ok=%v, want true", id, sum.Paused, ok)
		}
	}

	resumeResp := env.do(http.MethodPost, "/api/monitors/bulk", map[string]any{
		"ids": []int64{m1.ID, m2.ID}, "action": "resume",
	}, "admin")
	if resumeResp.status != http.StatusOK {
		t.Fatalf("bulk resume: status = %d", resumeResp.status)
	}
	for _, id := range []int64{m1.ID, m2.ID} {
		if sum, ok := env.engine.Summary(id); !ok || sum.Paused {
			t.Errorf("monitor %d: paused = %v after resume, want false", id, sum.Paused)
		}
	}

	group := createGroup(t, env, "bulk group")
	moveResp := env.do(http.MethodPost, "/api/monitors/bulk", map[string]any{
		"ids": []int64{m1.ID, m2.ID}, "action": "move", "groupId": group,
	}, "admin")
	if moveResp.status != http.StatusOK {
		t.Fatalf("bulk move: status = %d, body = %s", moveResp.status, moveResp.body)
	}
	for _, id := range []int64{m1.ID, m2.ID} {
		sum, ok := env.engine.Summary(id)
		if !ok || sum.GroupID == nil || *sum.GroupID != group {
			t.Errorf("monitor %d: groupId = %v ok=%v, want %d", id, sum.GroupID, ok, group)
		}
	}
}

func createGroup(t *testing.T, env *testEnv, name string) int64 {
	t.Helper()
	resp := env.do(http.MethodPost, "/api/groups", map[string]any{"name": name}, "admin")
	if resp.status != http.StatusCreated {
		t.Fatalf("create group: status = %d, body = %s", resp.status, resp.body)
	}
	var g store.Group
	resp.json(t, &g)
	return g.ID
}

// TestPushEndpoint proves a valid push token records a heartbeat, an
// unknown token 404s, and a second call inside the 1s window is accepted
// but ignored (no second heartbeat).
func TestPushEndpoint(t *testing.T) {
	env := newTestEnv(t)

	in := MonitorIn{Name: "push monitor", Type: model.TypePush, Target: "", IntervalS: 60, RetryIntervalS: 20, Retries: 2, TimeoutMs: 5000}
	out := env.createMonitor(in)
	if out.PushToken == "" {
		t.Fatal("push monitor has no push token")
	}

	resp := env.do(http.MethodGet, "/push/"+out.PushToken+"?status=up", nil, "")
	if resp.status != http.StatusOK {
		t.Fatalf("push status = %d, body = %s", resp.status, resp.body)
	}

	resp404 := env.do(http.MethodGet, "/push/not-a-real-token", nil, "")
	if resp404.status != http.StatusNotFound {
		t.Fatalf("unknown token status = %d, want 404", resp404.status)
	}

	env.writer.Flush()
	n1 := countHeartbeats(t, env, out.ID)
	if n1 != 1 {
		t.Fatalf("heartbeats after 1st push = %d, want 1", n1)
	}

	// Second call within 1s: accepted (200) but ignored.
	resp2 := env.do(http.MethodGet, "/push/"+out.PushToken+"?status=up", nil, "")
	if resp2.status != http.StatusOK {
		t.Fatalf("2nd push status = %d, want 200", resp2.status)
	}
	env.writer.Flush()
	n2 := countHeartbeats(t, env, out.ID)
	if n2 != 1 {
		t.Fatalf("heartbeats after 2nd (deduped) push = %d, want still 1", n2)
	}
}

func countHeartbeats(t *testing.T, env *testEnv, monitorID int64) int {
	t.Helper()
	var n int
	if err := env.store.R.QueryRow(`SELECT COUNT(*) FROM heartbeats WHERE monitor_id=?`, monitorID).Scan(&n); err != nil {
		t.Fatalf("count heartbeats: %v", err)
	}
	return n
}

// TestSeriesRanges proves range=24h is bounded at 600 points and an
// unknown range 400s.
func TestSeriesRanges(t *testing.T) {
	env := newTestEnv(t)
	out := env.createMonitor(httpMonitorIn("series monitor", "https://example.com/health"))

	resp := env.do(http.MethodGet, "/api/monitors/"+itoa(out.ID)+"/series?range=24h", nil, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var body struct {
		Series []any `json:"series"`
	}
	resp.json(t, &body)
	if len(body.Series) > 600 {
		t.Errorf("series len = %d, want <= 600", len(body.Series))
	}

	bad := env.do(http.MethodGet, "/api/monitors/"+itoa(out.ID)+"/series?range=bad", nil, "admin")
	if bad.status != http.StatusBadRequest {
		t.Fatalf("range=bad status = %d, want 400", bad.status)
	}
}

// TestListsBounded proves beats?limit=1000 is capped at 200, even when
// more than 200 rows exist.
func TestListsBounded(t *testing.T) {
	env := newTestEnv(t)
	out := env.createMonitor(httpMonitorIn("bounded monitor", "https://example.com/health"))

	for i := 0; i < 250; i++ {
		env.writer.Heartbeat(store.Heartbeat{
			MonitorID: out.ID, TS: int64(1700000000000 + i*1000),
			Status: model.StatusUp, LatencyMs: 10, Code: 200,
			DNSMs: -1, TCPMs: -1, TLSMs: -1, TTFBMs: -1,
		})
	}
	env.writer.Flush()

	resp := env.do(http.MethodGet, "/api/monitors/"+itoa(out.ID)+"/beats?limit=1000", nil, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var body struct {
		Beats []store.Heartbeat `json:"beats"`
	}
	resp.json(t, &body)
	if len(body.Beats) != 200 {
		t.Fatalf("beats len = %d, want 200 (clamped)", len(body.Beats))
	}
}

// TestMetricsAuth proves /metrics 404s with no token configured, 401s on a
// wrong bearer, and 200s with the correct one.
func TestMetricsAuth(t *testing.T) {
	env := newTestEnv(t)
	env.createMonitor(httpMonitorIn("metrics monitor", "https://example.com/health"))

	if resp := env.do(http.MethodGet, "/metrics", nil, ""); resp.status != http.StatusNotFound {
		t.Fatalf("no token configured: status = %d, want 404", resp.status)
	}

	env.cfg.MetricsToken = "s3cr3t-metrics-token"

	req := env.do(http.MethodGet, "/metrics", nil, "")
	if req.status != http.StatusUnauthorized {
		t.Fatalf("no bearer: status = %d, want 401", req.status)
	}

	resp := env.doWithHeader(http.MethodGet, "/metrics", "Authorization", "Bearer wrong")
	if resp.status != http.StatusUnauthorized {
		t.Fatalf("wrong bearer: status = %d, want 401", resp.status)
	}

	ok := env.doWithHeader(http.MethodGet, "/metrics", "Authorization", "Bearer "+env.cfg.MetricsToken)
	if ok.status != http.StatusOK {
		t.Fatalf("correct bearer: status = %d, want 200", ok.status)
	}
	if !bytesContains(ok.body, "opskest_monitor_up") {
		t.Errorf("metrics body missing opskest_monitor_up: %s", ok.body)
	}
}

// TestSecurityHeaders proves the CSP and nosniff headers are set exactly
// as the spec requires, on an ordinary API response.
func TestSecurityHeaders(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(http.MethodGet, "/healthz", nil, "")

	wantCSP := "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'"
	if got := resp.header.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("CSP = %q, want %q", got, wantCSP)
	}
	if got := resp.header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// TestHealthz proves /healthz answers 200 with no auth.
func TestHealthz(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(http.MethodGet, "/healthz", nil, "")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d", resp.status)
	}
}

// TestNoLoginBypass guards against a sign-in bypass or the removed Google
// sign-in mechanism being accidentally exposed by the production router.
func TestNoLoginBypass(t *testing.T) {
	env := newTestEnv(t)
	for _, path := range []string{"/api/auth/dev-login", "/api/auth/google", "/api/auth/google/callback"} {
		resp := env.do(http.MethodGet, path, nil, "")
		if resp.status != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, resp.status)
		}
	}
}

// -- small local helpers, kept here rather than testutil_test.go since
// they're one-liners only api_test.go and api2_test.go need. --------------

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

func strPtr(s string) *string { return &s }

func bytesContains(b []byte, s string) bool {
	return strings.Contains(string(b), s)
}

// doWithHeader is like do but sets one arbitrary header instead of a
// session cookie; used for Authorization: Bearer tests.
func (e *testEnv) doWithHeader(method, path, headerName, headerValue string) *apiResponse {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, nil)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	req.Header.Set(headerName, headerValue)
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

// TestRollupBackedRangesReturnData guards against querying a misnamed rollup
// table: the 7d/30d/90d series and the daily bars must come back non-empty
// once heartbeats have been written (they read rollup_hourly/rollup_daily).
func TestRollupBackedRangesReturnData(t *testing.T) {
	env := newTestEnv(t)
	out := env.createMonitor(httpMonitorIn("rollup monitor", "https://example.com/health"))
	now := time.Now().UnixMilli()
	for i := 0; i < 20; i++ {
		status := model.StatusUp
		if i%5 == 0 {
			status = model.StatusDegraded
		}
		env.writer.Heartbeat(store.Heartbeat{
			MonitorID: out.ID, TS: now - int64(i)*60_000, Status: status, LatencyMs: 100 + i,
			Code: 200, DNSMs: -1, TCPMs: -1, TLSMs: -1, TTFBMs: -1,
		})
	}
	env.writer.Flush()

	for _, rng := range []string{"7d", "30d", "90d"} {
		resp := env.do(http.MethodGet, "/api/monitors/"+itoa(out.ID)+"/series?range="+rng, nil, "admin")
		if resp.status != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", rng, resp.status, resp.body)
		}
		var body struct {
			Series []any `json:"series"`
		}
		resp.json(t, &body)
		if len(body.Series) == 0 {
			t.Errorf("range=%s returned no points", rng)
		}
	}

	resp := env.do(http.MethodGet, "/api/monitors/"+itoa(out.ID)+"/daily?days=90", nil, "admin")
	if resp.status != http.StatusOK {
		t.Fatalf("daily status = %d, body = %s", resp.status, resp.body)
	}
	var days []struct {
		Uptime *float64 `json:"uptime"`
	}
	resp.json(t, &days)
	if len(days) == 0 || days[len(days)-1].Uptime == nil {
		t.Fatalf("daily bars missing or without uptime: %s", resp.body)
	}
	if got := *days[len(days)-1].Uptime; got != 100 {
		t.Errorf("uptime = %v, want 100 (degraded counts as good)", got)
	}
}
