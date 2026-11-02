package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

// TestChannelMaskAndKeep proves a channel's secret URL never appears in a
// GET response, that PUT with url:null keeps it, and that /test actually
// delivers to the configured endpoint.
func TestChannelMaskAndKeep(t *testing.T) {
	env := newTestEnv(t)

	var received bool
	// TLS: channel URLs must be https (alert.ValidateConfig), and the test
	// dispatcher's HTTP client (newTestEnv) is configured to skip
	// certificate verification for exactly this self-signed server.
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	createResp := env.do(http.MethodPost, "/api/channels", map[string]any{
		"name": "webhook channel", "type": "webhook", "isDefault": false,
		"config": map[string]any{"url": ts.URL},
	}, "admin")
	if createResp.status != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", createResp.status, createResp.body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	createResp.json(t, &created)

	listResp := env.do(http.MethodGet, "/api/channels", nil, "admin")
	if listResp.status != http.StatusOK {
		t.Fatalf("list: status = %d, body = %s", listResp.status, listResp.body)
	}
	if bytesContains(listResp.body, ts.URL) {
		t.Errorf("channel list leaks the URL: %s", listResp.body)
	}
	if !bytesContains(listResp.body, `"isSet":true`) {
		t.Errorf("channel list missing isSet:true: %s", listResp.body)
	}

	// PUT with url:null keeps the stored URL.
	putResp := env.do(http.MethodPut, "/api/channels/"+itoa(created.ID), map[string]any{
		"name": "webhook channel renamed", "type": "webhook", "isDefault": false,
		"config": map[string]any{"url": nil},
	}, "admin")
	if putResp.status != http.StatusOK {
		t.Fatalf("update: status = %d, body = %s", putResp.status, putResp.body)
	}
	stored, err := env.store.GetChannel(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if stored.Config.URL != ts.URL {
		t.Errorf("stored URL = %q, want unchanged %q", stored.Config.URL, ts.URL)
	}

	testResp := env.do(http.MethodPost, "/api/channels/"+itoa(created.ID)+"/test", nil, "admin")
	if testResp.status != http.StatusOK {
		t.Fatalf("test: status = %d, body = %s", testResp.status, testResp.body)
	}
	var testBody struct {
		OK bool `json:"ok"`
	}
	testResp.json(t, &testBody)
	if !testBody.OK {
		t.Errorf("test: ok = false, body = %s", testResp.body)
	}
	if !received {
		t.Error("test channel endpoint never received a request")
	}
}

// downMonitor creates an HTTP monitor with Retries=0 and drives it DOWN
// with an open outage via a single direct engine.Apply, bypassing the
// (faked) checker entirely. It flushes the writer so the outage row is
// actually committed before the caller acts on it.
func downMonitor(t *testing.T, env *testEnv, name, target, reason string) MonitorOut {
	t.Helper()
	in := httpMonitorIn(name, target)
	in.Retries = 0
	in.Runbook = "internal runbook: see wiki/secret-runbook"
	out := env.createMonitor(in)

	env.engine.Apply(out.ID, model.Result{
		At: time.Now(), OK: false, LatencyMs: -1,
		Reason: reason, ErrClass: model.ErrHTTPStatus,
	})
	env.writer.Flush()
	return out
}

// TestOutageAckByViewer proves a viewer can acknowledge an outage and that
// the ack is reflected in Engine.Summary.
func TestOutageAckByViewer(t *testing.T) {
	env := newTestEnv(t)
	out := downMonitor(t, env, "ack monitor", "https://example.com/health", "HTTP 503")

	sum, ok := env.engine.Summary(out.ID)
	if !ok || sum.OutageID == nil {
		t.Fatalf("monitor did not open an outage: summary = %+v ok=%v", sum, ok)
	}
	outageID := *sum.OutageID

	resp := env.do(http.MethodPost, "/api/outages/"+itoa(outageID)+"/ack", nil, "viewer")
	if resp.status != http.StatusOK {
		t.Fatalf("ack status = %d, body = %s", resp.status, resp.body)
	}

	sum2, ok := env.engine.Summary(out.ID)
	if !ok || sum2.Ack == nil {
		t.Fatalf("summary after ack: %+v ok=%v, want Ack set", sum2, ok)
	}
	if sum2.Ack.By != "Viewer" && sum2.Ack.By != "viewer@example.com" {
		t.Errorf("ack.by = %q, want the viewer's name or email", sum2.Ack.By)
	}
}

// TestIncidentFlow exercises create -> add update -> resolve.
func TestIncidentFlow(t *testing.T) {
	env := newTestEnv(t)

	createResp := env.do(http.MethodPost, "/api/incidents", map[string]any{
		"title": "Elevated errors", "impact": "major", "body": "Investigating elevated error rates.",
	}, "admin")
	if createResp.status != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", createResp.status, createResp.body)
	}
	var created store.Incident
	createResp.json(t, &created)
	if created.Status != "investigating" {
		t.Fatalf("initial status = %q, want investigating", created.Status)
	}

	updResp := env.do(http.MethodPost, "/api/incidents/"+itoa(created.ID)+"/updates", map[string]any{
		"status": "identified", "body": "Found the root cause.",
	}, "admin")
	if updResp.status != http.StatusCreated {
		t.Fatalf("update: status = %d, body = %s", updResp.status, updResp.body)
	}

	resolveResp := env.do(http.MethodPost, "/api/incidents/"+itoa(created.ID)+"/updates", map[string]any{
		"status": "resolved", "body": "Fixed.",
	}, "admin")
	if resolveResp.status != http.StatusCreated {
		t.Fatalf("resolve: status = %d, body = %s", resolveResp.status, resolveResp.body)
	}

	getResp := env.do(http.MethodGet, "/api/incidents/"+itoa(created.ID), nil, "viewer")
	if getResp.status != http.StatusOK {
		t.Fatalf("get: status = %d, body = %s", getResp.status, getResp.body)
	}
	var got store.Incident
	getResp.json(t, &got)
	if got.Status != "resolved" || got.ResolvedAt == nil {
		t.Fatalf("incident after resolve: status=%q resolvedAt=%v", got.Status, got.ResolvedAt)
	}
}

// TestMaintenanceStateComputed proves the maintenance list includes a
// computed state field.
func TestMaintenanceStateComputed(t *testing.T) {
	env := newTestEnv(t)
	future := time.Now().Add(1 * time.Hour).UnixMilli()
	end := time.Now().Add(2 * time.Hour).UnixMilli()

	mon := env.createMonitor(httpMonitorIn("maint target", "https://example.com/m"))
	createResp := env.do(http.MethodPost, "/api/maintenance", map[string]any{
		"title": "Upgrade window", "kind": "once", "startsAt": future, "endsAt": end,
		"active": true, "tz": "UTC", "monitorIds": []int64{mon.ID},
	}, "admin")
	if r := env.do(http.MethodPost, "/api/maintenance", map[string]any{"title": "No targets", "kind": "once", "startsAt": future, "endsAt": end, "tz": "UTC"}, "admin"); r.status != http.StatusBadRequest {
		t.Fatalf("window without targets = %d, want 400", r.status)
	}
	if createResp.status != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", createResp.status, createResp.body)
	}

	listResp := env.do(http.MethodGet, "/api/maintenance", nil, "viewer")
	if listResp.status != http.StatusOK {
		t.Fatalf("list: status = %d, body = %s", listResp.status, listResp.body)
	}
	var body struct {
		Maintenance []struct {
			State string `json:"state"`
		} `json:"maintenance"`
	}
	listResp.json(t, &body)
	if len(body.Maintenance) != 1 {
		t.Fatalf("maintenance count = %d, want 1", len(body.Maintenance))
	}
	if body.Maintenance[0].State != "upcoming" {
		t.Errorf("state = %q, want upcoming", body.Maintenance[0].State)
	}
}

// TestPageSlugRules proves an invalid slug 400s, a reserved slug 400s and
// a duplicate slug 409s.
func TestPageSlugRules(t *testing.T) {
	env := newTestEnv(t)

	invalid := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "A", "title": "Bad slug", "visibility": "public", "layout": "list",
	}, "admin")
	if invalid.status != http.StatusBadRequest {
		t.Fatalf("invalid slug: status = %d, want 400", invalid.status)
	}

	reserved := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "admin", "title": "Reserved", "visibility": "public", "layout": "list",
	}, "admin")
	if reserved.status != http.StatusBadRequest {
		t.Fatalf("reserved slug: status = %d, want 400", reserved.status)
	}

	first := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "services", "title": "Services", "visibility": "public", "layout": "list",
	}, "admin")
	if first.status != http.StatusCreated {
		t.Fatalf("first page: status = %d, body = %s", first.status, first.body)
	}
	dup := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "services", "title": "Services Again", "visibility": "public", "layout": "list",
	}, "admin")
	if dup.status != http.StatusConflict {
		t.Fatalf("duplicate slug: status = %d, want 409, body = %s", dup.status, dup.body)
	}
}

// TestPublicPageNeverLeaks proves a public page's JSON body, and the first
// SSE snapshot from its stream, carry no target, reason, runbook or tag
// data for a monitor that is actively down with a sensitive reason.
func TestPublicPageNeverLeaks(t *testing.T) {
	env := newTestEnv(t)
	out := downMonitor(t, env, "secret monitor", "https://secret-host.internal/x", "HTTP 503 from secret-host")

	pageResp := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "leaktest", "title": "Leak Test", "visibility": "public", "layout": "list",
		"items": []map[string]any{{"monitorId": out.ID, "sort": 0}},
	}, "admin")
	if pageResp.status != http.StatusCreated {
		t.Fatalf("create page: status = %d, body = %s", pageResp.status, pageResp.body)
	}

	getResp := env.do(http.MethodGet, "/api/public/pages/leaktest", nil, "")
	if getResp.status != http.StatusOK {
		t.Fatalf("get public page: status = %d, body = %s", getResp.status, getResp.body)
	}
	assertNoLeak(t, getResp.body)

	streamBody := firstSSEBody(t, env.srv.URL+"/api/public/pages/leaktest/stream")
	assertNoLeak(t, streamBody)
}

func assertNoLeak(t *testing.T, body []byte) {
	t.Helper()
	for _, needle := range []string{"secret-host", "runbook", "target", "HTTP 503"} {
		if bytesContains(body, needle) {
			t.Errorf("response leaks %q: %s", needle, body)
		}
	}
}

// firstSSEBody connects to an SSE endpoint and returns the raw bytes of
// the first event (up to the blank line that terminates it), then closes
// the connection.
func firstSSEBody(t *testing.T, url string) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status = %d", url, resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var b strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// TestPrivatePage404WithoutSession proves a private page 404s with no
// session and 200s with a viewer session.
func TestPrivatePage404WithoutSession(t *testing.T) {
	env := newTestEnv(t)
	out := env.createMonitor(httpMonitorIn("private page monitor", "https://example.com/health"))

	pageResp := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "internal-only", "title": "Internal", "visibility": "private", "layout": "list",
		"items": []map[string]any{{"monitorId": out.ID, "sort": 0}},
	}, "admin")
	if pageResp.status != http.StatusCreated {
		t.Fatalf("create page: status = %d, body = %s", pageResp.status, pageResp.body)
	}

	anon := env.do(http.MethodGet, "/api/public/pages/internal-only", nil, "")
	if anon.status != http.StatusNotFound {
		t.Fatalf("anonymous: status = %d, want 404", anon.status)
	}

	viewer := env.do(http.MethodGet, "/api/public/pages/internal-only", nil, "viewer")
	if viewer.status != http.StatusOK {
		t.Fatalf("viewer: status = %d, want 200, body = %s", viewer.status, viewer.body)
	}
}

// TestPublicRateLimit proves a public page is rate limited after a burst of 40.
func TestPublicRateLimit(t *testing.T) {
	env := newTestEnv(t)
	out := env.createMonitor(httpMonitorIn("rate limit monitor", "https://example.com/health"))
	pageResp := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "ratelimited", "title": "Rate Limited", "visibility": "public", "layout": "list",
		"items": []map[string]any{{"monitorId": out.ID, "sort": 0}},
	}, "admin")
	if pageResp.status != http.StatusCreated {
		t.Fatalf("create page: status = %d, body = %s", pageResp.status, pageResp.body)
	}

	// The limiter allows a burst of 40 and refills slowly, so a slow machine
	// (the race detector) may get a token back mid-test. Send more than the
	// burst and check that the limit starts only after it.
	first429 := -1
	for i := 0; i < 90 && first429 < 0; i++ {
		if env.do(http.MethodGet, "/api/public/pages/ratelimited", nil, "").status == http.StatusTooManyRequests {
			first429 = i + 1
		}
	}
	if first429 < 0 {
		t.Fatal("90 requests in a row were never rate limited")
	}
	if first429 <= 40 {
		t.Fatalf("request %d was rate limited, the burst is 40", first429)
	}
}

// TestLastAdminProtected proves the only admin cannot be demoted.
func TestLastAdminProtected(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(http.MethodPut, "/api/users/"+itoa(env.adminID), map[string]any{"role": "viewer"}, "admin")
	if resp.status != http.StatusConflict {
		t.Fatalf("status = %d, want 409, body = %s", resp.status, resp.body)
	}
}

// TestSettingsValidation proves an out-of-range setting 400s.
func TestSettingsValidation(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(http.MethodPut, "/api/settings", map[string]any{
		"retentionRawDays": 0, "stormThreshold": 5, "stormWindowS": 30,
		"backupHour": 2, "backupKeep": 7, "defaultReminderMin": 0,
	}, "admin")
	if resp.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", resp.status, resp.body)
	}
}

// TestSettingsBackupHourKeys proves the neutral backupHour key round-trips
// and the legacy backupHourIst key is still accepted on input but never
// emitted.
func TestSettingsBackupHourKeys(t *testing.T) {
	env := newTestEnv(t)
	for _, key := range []string{"backupHour", "backupHourIst"} {
		resp := env.do(http.MethodPut, "/api/settings", map[string]any{
			"retentionRawDays": 7, "stormThreshold": 5, "stormWindowS": 30,
			key: 17, "backupKeep": 7, "defaultReminderMin": 0,
		}, "admin")
		if resp.status != http.StatusOK {
			t.Fatalf("PUT with %s: status = %d, body = %s", key, resp.status, resp.body)
		}
		got := env.do(http.MethodGet, "/api/settings", nil, "admin")
		if got.status != http.StatusOK {
			t.Fatalf("GET status = %d, body = %s", got.status, got.body)
		}
		var out map[string]any
		got.json(t, &out)
		if out["backupHour"] != float64(17) {
			t.Fatalf("after PUT with %s: backupHour = %v, want 17 (body %s)", key, out["backupHour"], got.body)
		}
		if _, ok := out["backupHourIst"]; ok {
			t.Fatalf("legacy backupHourIst must not be emitted: %s", got.body)
		}
		// Reset so the second iteration proves it changes the value itself.
		env.do(http.MethodPut, "/api/settings", map[string]any{
			"retentionRawDays": 7, "stormThreshold": 5, "stormWindowS": 30,
			"backupHour": 3, "backupKeep": 7, "defaultReminderMin": 0,
		}, "admin")
	}
}

// TestEventsEndpoint proves GET /api/events returns the events store
// records, with string status names.
func TestEventsEndpoint(t *testing.T) {
	env := newTestEnv(t)
	out := downMonitor(t, env, "events monitor", "https://example.com/health", "HTTP 503")

	resp := env.do(http.MethodGet, "/api/events?monitorId="+itoa(out.ID), nil, "viewer")
	if resp.status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.status, resp.body)
	}
	var body struct {
		Events []struct {
			MonitorID int64  `json:"monitorId"`
			From      string `json:"from"`
			To        string `json:"to"`
		} `json:"events"`
	}
	resp.json(t, &body)
	if len(body.Events) == 0 {
		t.Fatal("no events recorded for the down transition")
	}
	found := false
	for _, e := range body.Events {
		if e.MonitorID == out.ID && e.To == "down" {
			found = true
		}
	}
	if !found {
		t.Errorf("no down event for monitor %d: %+v", out.ID, body.Events)
	}
}

// TestUserManagement covers pre-adding, role changes, disabling and
// removing users, and the guards around them.
func TestUserManagement(t *testing.T) {
	env := newTestEnv(t)

	// Viewers cannot manage users.
	if r := env.do(http.MethodPost, "/api/users", map[string]any{"username": "viewer-test", "password": "long test password"}, "viewer"); r.status != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403", r.status)
	}
	// Local credentials are validated.
	r := env.do(http.MethodPost, "/api/users", map[string]any{"username": "invalid username", "password": "long test password", "role": "viewer"}, "admin")
	if r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "username must") {
		t.Fatalf("invalid username create = %d %s, want 400", r.status, r.body)
	}
	// Create.
	r = env.do(http.MethodPost, "/api/users", map[string]any{"username": "New.Person", "password": "long test password", "name": "New Person", "role": "admin"}, "admin")
	if r.status != http.StatusCreated {
		t.Fatalf("create = %d %s", r.status, r.body)
	}
	var created struct {
		ID       int64  `json:"id"`
		Email    string `json:"email"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	r.json(t, &created)
	if created.Username != "new.person" || created.Role != "admin" {
		t.Fatalf("created = %+v", created)
	}
	// Duplicate.
	if r := env.do(http.MethodPost, "/api/users", map[string]any{"username": "new.person", "password": "long test password"}, "admin"); r.status != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", r.status)
	}
	// Listed.
	list := env.do(http.MethodGet, "/api/users", nil, "admin")
	if !strings.Contains(string(list.body), `"username":"new.person"`) {
		t.Fatalf("list missing new user: %s", list.body)
	}
	// Disable, then remove.
	if r := env.do(http.MethodPut, "/api/users/"+itoa(created.ID), map[string]any{"role": "disabled"}, "admin"); r.status != http.StatusOK {
		t.Fatalf("disable = %d %s", r.status, r.body)
	}
	if r := env.do(http.MethodDelete, "/api/users/"+itoa(created.ID), nil, "admin"); r.status != http.StatusNoContent {
		t.Fatalf("delete = %d %s", r.status, r.body)
	}
	if r := env.do(http.MethodDelete, "/api/users/"+itoa(created.ID), nil, "admin"); r.status != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", r.status)
	}
	// Cannot remove yourself (also the last admin).
	if r := env.do(http.MethodDelete, "/api/users/"+itoa(env.adminID), nil, "admin"); r.status != http.StatusConflict {
		t.Fatalf("self delete = %d, want 409", r.status)
	}
}

func TestCheckNow(t *testing.T) {
	env := newTestEnv(t)
	out := env.createMonitor(httpMonitorIn("check-now monitor", "https://example.com/health"))
	if r := env.do(http.MethodPost, "/api/monitors/"+itoa(out.ID)+"/check", nil, "viewer"); r.status != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403", r.status)
	}
	if r := env.do(http.MethodPost, "/api/monitors/"+itoa(out.ID)+"/check", nil, "admin"); r.status != http.StatusAccepted {
		t.Fatalf("admin status = %d %s, want 202", r.status, r.body)
	}
	if r := env.do(http.MethodPost, "/api/monitors/999999/check", nil, "admin"); r.status != http.StatusNotFound {
		t.Fatalf("unknown status = %d, want 404", r.status)
	}
	env.do(http.MethodPost, "/api/monitors/"+itoa(out.ID)+"/pause", nil, "admin")
	if r := env.do(http.MethodPost, "/api/monitors/"+itoa(out.ID)+"/check", nil, "admin"); r.status != http.StatusConflict {
		t.Fatalf("paused status = %d, want 409", r.status)
	}
}

func TestDataUsageClearCompact(t *testing.T) {
	env := newTestEnv(t)
	out := env.createMonitor(httpMonitorIn("data monitor", "https://example.com/health"))
	now := time.Now().UnixMilli()
	for i := 0; i < 30; i++ {
		ts := now - int64(i)*24*3600*1000 // one beat per day, 30 days back
		env.writer.Heartbeat(store.Heartbeat{MonitorID: out.ID, TS: ts, Status: model.StatusUp, LatencyMs: 10, Code: 200, DNSMs: -1, TCPMs: -1, TLSMs: -1, TTFBMs: -1})
	}
	env.writer.Flush()

	if r := env.do(http.MethodGet, "/api/data/usage", nil, "viewer"); r.status != http.StatusForbidden {
		t.Fatalf("viewer usage = %d, want 403", r.status)
	}
	var usage struct {
		Usage struct {
			FileBytes int64 `json:"fileBytes"`
			Tables    []struct {
				Name string `json:"name"`
				Rows int64  `json:"rows"`
			} `json:"tables"`
		} `json:"usage"`
	}
	env.do(http.MethodGet, "/api/data/usage", nil, "admin").json(t, &usage)
	rows := func() int64 {
		env.do(http.MethodGet, "/api/data/usage", nil, "admin").json(t, &usage)
		for _, tb := range usage.Usage.Tables {
			if tb.Name == "heartbeats" {
				return tb.Rows
			}
		}
		return -1
	}
	if n := rows(); n != 30 || usage.Usage.FileBytes == 0 {
		t.Fatalf("heartbeats = %d, fileBytes = %d", n, usage.Usage.FileBytes)
	}

	// Keep the last 7 days.
	r := env.do(http.MethodPost, "/api/data/clear", map[string]any{"kind": "heartbeats", "keepDays": 7}, "admin")
	if r.status != http.StatusOK {
		t.Fatalf("clear = %d %s", r.status, r.body)
	}
	if n := rows(); n < 7 || n > 8 {
		t.Fatalf("after keep=7d heartbeats = %d, want 7-8", n)
	}
	// Bad kind.
	if r := env.do(http.MethodPost, "/api/data/clear", map[string]any{"kind": "monitors"}, "admin"); r.status != http.StatusBadRequest {
		t.Fatalf("bad kind = %d, want 400", r.status)
	}
	// Clear all for this monitor.
	env.do(http.MethodPost, "/api/data/clear", map[string]any{"kind": "heartbeats", "keepDays": 0, "monitorId": out.ID}, "admin")
	if n := rows(); n != 0 {
		t.Fatalf("after clear all heartbeats = %d", n)
	}
	if r := env.do(http.MethodPost, "/api/data/compact", nil, "admin"); r.status != http.StatusOK {
		t.Fatalf("compact = %d %s", r.status, r.body)
	}
}

// TestPageAlarmSettings: the board alarm is stored on the page (so every
// screen agrees), unknown sounds fall back to the default, and the public
// payload carries it.
func TestPageAlarmSettings(t *testing.T) {
	env := newTestEnv(t)
	r := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "noc", "title": "NOC", "visibility": "public", "layout": "board",
		"alarmMuted": true, "alarmDown": "industrial-klaxon", "alarmDegraded": "bogus",
	}, "admin")
	if r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	var pub struct {
		Page struct {
			AlarmMuted    bool   `json:"alarmMuted"`
			AlarmDown     string `json:"alarmDown"`
			AlarmDegraded string `json:"alarmDegraded"`
		} `json:"page"`
	}
	env.do(http.MethodGet, "/api/public/pages/noc", nil, "").json(t, &pub)
	if !pub.Page.AlarmMuted || pub.Page.AlarmDown != "industrial-klaxon" || pub.Page.AlarmDegraded != "" {
		t.Fatalf("public alarm = %+v", pub.Page)
	}
}

// TestAlertLogFailedFilterAndResend: failed=true returns only failed
// attempts, and resend pushes the logged alert to the same channel again.
func TestAlertLogFailedFilterAndResend(t *testing.T) {
	env := newTestEnv(t)
	var got atomic.Int32
	var body atomic.Value
	hook := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body.Store(string(b))
		got.Add(1)
	}))
	defer hook.Close()
	mon := env.createMonitor(httpMonitorIn("resend me", "https://example.com/r"))
	ch := &store.Channel{Name: "hook", Type: "webhook", Config: store.ChannelConfig{URL: hook.URL}}
	if err := env.store.CreateChannel(t.Context(), ch); err != nil {
		t.Fatal(err)
	}
	now := env.now().UnixMilli()
	env.writer.Do(store.OpAlertLog(store.AlertLogEntry{TS: now - 2000, ChannelID: &ch.ID, MonitorID: &mon.ID, Event: "down", OK: false, Attempt: 4, Error: "http 404"}))
	env.writer.Do(store.OpAlertLog(store.AlertLogEntry{TS: now - 1000, ChannelID: &ch.ID, MonitorID: &mon.ID, Event: "recovered", OK: true, Attempt: 1}))
	env.writer.Flush()

	var list struct {
		Entries []struct {
			ID int64 `json:"id"`
			OK bool  `json:"ok"`
		} `json:"entries"`
	}
	env.do(http.MethodGet, "/api/alert-log?failed=true", nil, "admin").json(t, &list)
	if len(list.Entries) != 1 || list.Entries[0].OK {
		t.Fatalf("failed filter = %+v", list.Entries)
	}
	failedID := list.Entries[0].ID

	if r := env.do(http.MethodPost, fmt.Sprintf("/api/alert-log/%d/resend", failedID), nil, "viewer"); r.status != http.StatusForbidden {
		t.Fatalf("viewer resend = %d", r.status)
	}
	if r := env.do(http.MethodPost, "/api/alert-log/999999/resend", nil, "admin"); r.status != http.StatusNotFound {
		t.Fatalf("unknown resend = %d", r.status)
	}
	r := env.do(http.MethodPost, fmt.Sprintf("/api/alert-log/%d/resend", failedID), nil, "admin")
	if r.status != http.StatusOK || got.Load() != 1 || !strings.Contains(body.Load().(string), "resend me") {
		t.Fatalf("resend = %d %s; webhook got %d", r.status, r.body, got.Load())
	}
}

// TestIncidentManagement: severity + owner + postmortem + team notes, and
// none of it (nor author names) reaches a public status page.
func TestIncidentManagement(t *testing.T) {
	env := newTestEnv(t)
	env.do(http.MethodPost, "/api/pages", map[string]any{"slug": "inc-pub", "title": "Pub", "visibility": "public", "layout": "list"}, "admin")
	pages, _ := env.store.ListPages(t.Context())
	pageID := pages[0].ID

	var inc struct {
		ID int64 `json:"id"`
	}
	r := env.do(http.MethodPost, "/api/incidents", map[string]any{
		"title": "Checkout errors", "impact": "major", "body": "Looking into it.", "severity": "critical",
		"commanderId": env.adminID, "pageIds": []int64{pageID},
	}, "admin")
	if r.status != http.StatusCreated {
		t.Fatalf("create = %d %s", r.status, r.body)
	}
	r.json(t, &inc)
	if r := env.do(http.MethodPost, "/api/incidents", map[string]any{"title": "x", "severity": "sev0"}, "admin"); r.status != http.StatusBadRequest {
		t.Fatalf("bad severity = %d", r.status)
	}

	// A team note keeps the status; a public update moves it.
	env.do(http.MethodPost, fmt.Sprintf("/api/incidents/%d/updates", inc.ID), map[string]any{"status": "resolved", "body": "SECRET-NOTE db failover by hand", "internal": true}, "admin")
	env.do(http.MethodPost, fmt.Sprintf("/api/incidents/%d/updates", inc.ID), map[string]any{"status": "identified", "body": "Payment provider timeouts."}, "admin")
	if r := env.do(http.MethodPut, fmt.Sprintf("/api/incidents/%d/postmortem", inc.ID), map[string]any{"postmortem": "SECRET-PM root cause"}, "admin"); r.status != http.StatusOK {
		t.Fatalf("postmortem = %d %s", r.status, r.body)
	}

	var got struct {
		Status        string `json:"status"`
		Severity      string `json:"severity"`
		CommanderName string `json:"commanderName"`
		Postmortem    string `json:"postmortem"`
		Updates       []struct {
			Internal bool   `json:"internal"`
			Status   string `json:"status"`
		} `json:"updates"`
	}
	env.do(http.MethodGet, fmt.Sprintf("/api/incidents/%d", inc.ID), nil, "viewer").json(t, &got)
	if got.Status != "identified" || got.Severity != "critical" || got.CommanderName == "" || got.Postmortem != "SECRET-PM root cause" || len(got.Updates) != 3 || !got.Updates[1].Internal || got.Updates[1].Status != "investigating" {
		t.Fatalf("incident = %+v", got)
	}

	pub := env.do(http.MethodGet, "/api/public/pages/inc-pub", nil, "")
	body := string(pub.body)
	for _, leak := range []string{"SECRET-NOTE", "SECRET-PM", "critical", got.CommanderName, "createdByName", "commander"} {
		if strings.Contains(body, leak) {
			t.Fatalf("public page leaks %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, "Payment provider timeouts.") {
		t.Fatalf("public page missing the public update: %s", body)
	}
}

// TestRegionSettings: countries and budget round-trip through settings,
// bad values are rejected, and GET /api/regions reports them (admin only).
func TestRegionSettings(t *testing.T) {
	env := newTestEnv(t)
	var st map[string]any
	env.do(http.MethodGet, "/api/settings", nil, "admin").json(t, &st)
	st["regionConfirm"], st["regionCountries"], st["regionTestsPerHour"] = 1, []string{"jp", "AU", "BR"}, 450
	if r := env.do(http.MethodPut, "/api/settings", st, "admin"); r.status != http.StatusOK {
		t.Fatalf("put = %d %s", r.status, r.body)
	}
	var got struct {
		Enabled   bool     `json:"enabled"`
		Countries []string `json:"countries"`
		Budget    int      `json:"testsPerHour"`
	}
	env.do(http.MethodGet, "/api/regions", nil, "admin").json(t, &got)
	if !got.Enabled || strings.Join(got.Countries, ",") != "JP,AU,BR" || got.Budget != 450 {
		t.Fatalf("regions = %+v", got)
	}
	st["regionCountries"] = []string{"USA"}
	if r := env.do(http.MethodPut, "/api/settings", st, "admin"); r.status != http.StatusBadRequest {
		t.Fatalf("bad country = %d", r.status)
	}
	if r := env.do(http.MethodGet, "/api/regions", nil, "viewer"); r.status != http.StatusForbidden {
		t.Fatalf("viewer = %d", r.status)
	}
}

// An update must be validated like a create: otherwise a saved channel can
// hold a config the create form would have refused.
func TestChannelUpdateIsValidated(t *testing.T) {
	env := newTestEnv(t)
	create := env.do(http.MethodPost, "/api/channels", map[string]any{
		"name": "ntfy", "type": "ntfy", "config": map[string]any{"url": "https://ntfy.sh/ops"},
	}, "admin")
	if create.status != http.StatusCreated {
		t.Fatalf("create: %d %s", create.status, create.body)
	}
	var made struct {
		ID int64 `json:"id"`
	}
	create.json(t, &made)
	path := "/api/channels/" + itoa(made.ID)

	bad := env.do(http.MethodPut, path, map[string]any{"name": "ntfy", "type": "ntfy", "config": map[string]any{"url": "ftp://nope"}}, "admin")
	if bad.status != http.StatusBadRequest {
		t.Fatalf("an invalid update must be rejected: %d %s", bad.status, bad.body)
	}
	stored, err := env.store.GetChannel(context.Background(), made.ID)
	if err != nil || stored.Config.URL != "https://ntfy.sh/ops" {
		t.Fatalf("the rejected update must not change the channel: %+v %v", stored, err)
	}
	// Keeping the stored secrets (url:null) is still a valid update.
	ok := env.do(http.MethodPut, path, map[string]any{"name": "ntfy renamed", "type": "ntfy", "config": map[string]any{"url": nil}}, "admin")
	if ok.status != http.StatusOK {
		t.Fatalf("a valid update: %d %s", ok.status, ok.body)
	}
}
