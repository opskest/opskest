package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

func testCertPair(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "client"},
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

// monBody is a minimal valid monitor request as a map, so a test controls
// exactly which secret fields are present.
func monBody(name string, typ model.MonitorType, target string, settings map[string]any, secrets map[string]any) map[string]any {
	b := map[string]any{
		"name": name, "type": typ, "target": target,
		"intervalS": 60, "retryIntervalS": 20, "retries": 0, "timeoutMs": 5000, "sslWarnDays": 14,
		"settings": settings, "headers": []any{},
	}
	if secrets != nil {
		b["secrets"] = secrets
	}
	return b
}

func TestHTTPTransportAndOAuthSecretsAreWriteOnly(t *testing.T) {
	env := newTestEnv(t)
	certPEM, keyPEM := testCertPair(t)
	const (
		proxyPW = "proxy-pass-zz9"
		oauthSc = "oauth-secret-zz9"
	)
	resp := env.do(http.MethodPost, "/api/monitors", monBody("secure", model.TypeHTTP, "https://example.invalid/", map[string]any{
		"authType": "oauth2", "oauthTokenUrl": "https://idp.invalid/token", "oauthClientId": "cid", "oauthScope": "read",
		"proxyUrl": "socks5://proxy.invalid:1080", "proxyUser": "bob",
	}, map[string]any{"proxyPass": proxyPW, "oauthSecret": oauthSc, "clientCert": certPEM, "clientKey": keyPEM}), "admin")
	if resp.status != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.status, resp.body)
	}
	var out MonitorOut
	resp.json(t, &out)
	for _, leak := range []string{proxyPW, oauthSc, "PRIVATE KEY", "BEGIN CERTIFICATE"} {
		if strings.Contains(string(resp.body), leak) {
			t.Fatalf("create response leaks %q", leak)
		}
	}
	if !out.Secrets.ProxyPass.IsSet || !out.Secrets.OAuthSecret.IsSet || !out.Secrets.ClientCert.IsSet || !out.Secrets.ClientKey.IsSet {
		t.Fatalf("secrets not reported as set: %+v", out.Secrets)
	}
	if out.Settings.ProxyURL != "socks5://proxy.invalid:1080" || out.Settings.ProxyUser != "bob" || out.Settings.OAuthAuth != "basic" {
		t.Errorf("settings = %+v", out.Settings)
	}

	get := env.do(http.MethodGet, "/api/monitors/"+itoa(out.ID), nil, "admin")
	for _, leak := range []string{proxyPW, oauthSc, "PRIVATE KEY"} {
		if strings.Contains(string(get.body), leak) {
			t.Fatalf("GET leaks %q", leak)
		}
	}
	// They are sealed at rest.
	stored, err := env.store.GetMonitor(context.Background(), out.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Secrets.ProxyPass != proxyPW || stored.Secrets.OAuthSecret != oauthSc || stored.Secrets.ClientKey != keyPEM {
		t.Fatalf("secrets were not stored: %+v", stored.Secrets)
	}

	// An update that leaves the secrets out keeps them.
	upd := monBody("secure renamed", model.TypeHTTP, "https://example.invalid/", map[string]any{
		"authType": "oauth2", "oauthTokenUrl": "https://idp.invalid/token", "oauthClientId": "cid",
		"proxyUrl": "socks5://proxy.invalid:1080", "proxyUser": "bob",
	}, nil)
	if r := env.do(http.MethodPut, "/api/monitors/"+itoa(out.ID), upd, "admin"); r.status != http.StatusOK {
		t.Fatalf("update: %d %s", r.status, r.body)
	}
	stored, _ = env.store.GetMonitor(context.Background(), out.ID)
	if stored.Secrets.ProxyPass != proxyPW || stored.Secrets.OAuthSecret != oauthSc || stored.Secrets.ClientCert != certPEM {
		t.Fatalf("an update without secrets dropped them: %+v", stored.Secrets)
	}

	// Removing the proxy removes its login too.
	upd = monBody("secure renamed", model.TypeHTTP, "https://example.invalid/", map[string]any{
		"authType": "oauth2", "oauthTokenUrl": "https://idp.invalid/token", "oauthClientId": "cid",
	}, map[string]any{"clientCert": "", "clientKey": ""})
	if r := env.do(http.MethodPut, "/api/monitors/"+itoa(out.ID), upd, "admin"); r.status != http.StatusOK {
		t.Fatalf("update 2: %d %s", r.status, r.body)
	}
	stored, _ = env.store.GetMonitor(context.Background(), out.ID)
	if stored.Secrets.ProxyPass != "" || stored.Settings.ProxyUser != "" || stored.Secrets.ClientCert != "" || stored.Secrets.ClientKey != "" {
		t.Fatalf("cleared fields survived: %+v / %+v", stored.Secrets, stored.Settings)
	}
	if stored.Secrets.OAuthSecret != oauthSc {
		t.Error("the OAuth secret should have been kept")
	}
}

func TestHTTPTransportValidationOverAPI(t *testing.T) {
	env := newTestEnv(t)
	certPEM, _ := testCertPair(t)
	_, otherKey := testCertPair(t)

	post := func(settings, secrets map[string]any) *apiResponse {
		return env.do(http.MethodPost, "/api/monitors", monBody("v", model.TypeHTTP, "https://example.invalid/", settings, secrets), "admin")
	}
	if r := post(map[string]any{"proxyUrl": "http://user:pw@proxy.invalid:3128"}, nil); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "settings.proxyUrl") {
		t.Errorf("credentials in the proxy URL: %d %s", r.status, r.body)
	}
	if r := post(nil, map[string]any{"clientCert": certPEM, "clientKey": otherKey}); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "secrets.clientCert") {
		t.Errorf("mismatched pair: %d %s", r.status, r.body)
	}
	if r := post(map[string]any{"authType": "oauth2", "oauthTokenUrl": "https://idp.invalid/t", "oauthClientId": "c"}, nil); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "secrets.oauthSecret") {
		t.Errorf("oauth2 without a secret: %d %s", r.status, r.body)
	}
	if strings.Contains(string(post(nil, map[string]any{"clientCert": certPEM, "clientKey": otherKey}).body), "PRIVATE KEY") {
		t.Error("validation error echoes the key")
	}
}

func TestGroupMonitorAPI(t *testing.T) {
	env := newTestEnv(t)
	a := env.createMonitor(httpMonitorIn("a", "https://a.example.invalid/"))
	b := env.createMonitor(httpMonitorIn("b", "https://b.example.invalid/"))

	create := func(name string, mode string, min int, children ...int64) *apiResponse {
		return env.do(http.MethodPost, "/api/monitors", monBody(name, model.TypeGroup, "", map[string]any{
			"children": children, "groupMode": mode, "groupMin": min,
		}, nil), "admin")
	}
	resp := create("front", "all", 0, a.ID, b.ID)
	if resp.status != http.StatusCreated {
		t.Fatalf("create group: %d %s", resp.status, resp.body)
	}
	var g MonitorOut
	resp.json(t, &g)
	if g.Type != model.TypeGroup || !slices.Equal(g.Settings.Children, []int64{a.ID, b.ID}) {
		t.Fatalf("group = %+v", g)
	}

	if r := create("bad child", "all", 0, 9999); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "doesn't exist") {
		t.Errorf("missing child: %d %s", r.status, r.body)
	}
	if r := create("bad min", "min", 5, a.ID, b.ID); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "settings.groupMin") {
		t.Errorf("min above the child count: %d %s", r.status, r.body)
	}
	withTarget := env.do(http.MethodPost, "/api/monitors", monBody("t", model.TypeGroup, "https://x.invalid", map[string]any{"children": []int64{a.ID}, "groupMode": "all"}, nil), "admin")
	if withTarget.status != http.StatusBadRequest {
		t.Errorf("a group with a target: %d", withTarget.status)
	}

	// A group may contain another group, but not itself through it.
	outer := create("outer", "any", 0, g.ID)
	if outer.status != http.StatusCreated {
		t.Fatalf("nested group: %d %s", outer.status, outer.body)
	}
	var o MonitorOut
	outer.json(t, &o)
	loop := env.do(http.MethodPut, "/api/monitors/"+itoa(g.ID), monBody("front", model.TypeGroup, "", map[string]any{
		"children": []int64{o.ID}, "groupMode": "all",
	}, nil), "admin")
	if loop.status != http.StatusBadRequest || !strings.Contains(string(loop.body), "contain itself") {
		t.Errorf("group loop: %d %s", loop.status, loop.body)
	}

	// The form's test button evaluates the group from live state.
	test := env.do(http.MethodPost, "/api/monitors/test", monBody("front", model.TypeGroup, "", map[string]any{
		"children": []int64{a.ID, b.ID}, "groupMode": "all",
	}, nil), "admin")
	if test.status != http.StatusOK {
		t.Fatalf("test group: %d %s", test.status, test.body)
	}
	var res model.Result
	test.json(t, &res)
	if !res.OK {
		t.Errorf("a group of unchecked monitors tested as %+v, want OK", res)
	}

	// Importing cannot carry a group (child ids are not portable).
	rows := []exportRow{{Name: "g", Type: "group", IntervalS: 60, RetryIntervalS: 20, TimeoutMs: 5000, Settings: model.Settings{Children: []int64{1}, GroupMode: "all"}}}
	body, _ := marshalJSON(rows)
	imp := env.doRaw(http.MethodPost, "/api/import/monitors?dryRun=true", "application/json", body, "admin")
	if imp.status != http.StatusOK || !strings.Contains(string(imp.body), "can't be imported") {
		t.Errorf("import of a group: %d %s", imp.status, imp.body)
	}
}

func TestManualStatusAPI(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(http.MethodPost, "/api/monitors", monBody("nightly export", model.TypeManual, "", map[string]any{}, nil), "admin")
	if resp.status != http.StatusCreated {
		t.Fatalf("create manual: %d %s", resp.status, resp.body)
	}
	var m MonitorOut
	resp.json(t, &m)
	web := env.createMonitor(httpMonitorIn("web", "https://web.example.invalid/"))

	set := func(id int64, as string, body any) *apiResponse {
		return env.do(http.MethodPost, "/api/monitors/"+itoa(id)+"/status", body, as)
	}
	if r := set(m.ID, "admin", map[string]any{"status": "down", "reason": "export failed"}); r.status != http.StatusOK {
		t.Fatalf("set down: %d %s", r.status, r.body)
	}
	sum, ok := env.engine.Summary(m.ID)
	if !ok || sum.Status != model.StatusDown || sum.Reason != "export failed" {
		t.Fatalf("summary = %+v", sum)
	}
	if r := set(m.ID, "admin", map[string]any{"status": "up"}); r.status != http.StatusOK {
		t.Fatalf("set up: %d %s", r.status, r.body)
	}
	if sum, _ := env.engine.Summary(m.ID); sum.Status != model.StatusUp {
		t.Fatalf("after up: %v", sum.Status)
	}

	if r := set(m.ID, "viewer", map[string]any{"status": "down"}); r.status == http.StatusOK {
		t.Error("a viewer set a manual status")
	}
	if r := set(m.ID, "", map[string]any{"status": "down"}); r.status == http.StatusOK {
		t.Error("an anonymous caller set a manual status")
	}
	if r := set(m.ID, "admin", map[string]any{"status": "sideways"}); r.status != http.StatusBadRequest {
		t.Errorf("bad status: %d", r.status)
	}
	if r := set(web.ID, "admin", map[string]any{"status": "down"}); r.status != http.StatusConflict {
		t.Errorf("non-manual monitor: %d", r.status)
	}
	if r := set(987654, "admin", map[string]any{"status": "down"}); r.status != http.StatusNotFound {
		t.Errorf("unknown monitor: %d", r.status)
	}
	if r := set(m.ID, "admin", map[string]any{"status": "down", "reason": strings.Repeat("x", 201)}); r.status != http.StatusBadRequest {
		t.Errorf("long reason: %d", r.status)
	}
	// A paused monitor cannot be set.
	if r := env.do(http.MethodPost, "/api/monitors/"+itoa(m.ID)+"/pause", nil, "admin"); r.status != http.StatusOK {
		t.Fatalf("pause: %d", r.status)
	}
	if r := set(m.ID, "admin", map[string]any{"status": "down"}); r.status != http.StatusConflict {
		t.Errorf("paused manual monitor: %d", r.status)
	}
	// Manual monitors reject a target and a ping check.
	if r := env.do(http.MethodPost, "/api/monitors", monBody("x", model.TypeManual, "https://x.invalid", map[string]any{}, nil), "admin"); r.status != http.StatusBadRequest {
		t.Errorf("manual with a target: %d", r.status)
	}
}

func TestChannelTemplateAndQuietHoursOverAPI(t *testing.T) {
	env := newTestEnv(t)
	create := func(cfg map[string]any) *apiResponse {
		return env.do(http.MethodPost, "/api/channels", map[string]any{"name": "ops", "type": "webhook", "config": cfg}, "admin")
	}
	resp := create(map[string]any{
		"url": "https://hooks.example.invalid/x", "template": "{name} is {status}",
		"quiet": map[string]any{"enabled": true, "days": []int{1, 2}, "start": "22:00", "end": "07:00", "bypassCritical": true},
	})
	if resp.status != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.status, resp.body)
	}
	var ch channelOut
	resp.json(t, &ch)
	if ch.Config["template"] != "{name} is {status}" {
		t.Errorf("config = %v", ch.Config)
	}
	q, _ := ch.Config["quiet"].(map[string]any)
	if q["start"] != "22:00" || q["bypassCritical"] != true {
		t.Errorf("quiet = %v", q)
	}
	if strings.Contains(string(resp.body), "hooks.example.invalid") {
		t.Error("the webhook URL leaked")
	}

	// A PUT that does not mention them keeps them; enabled:false clears quiet hours.
	put := func(cfg map[string]any) *apiResponse {
		return env.do(http.MethodPut, "/api/channels/"+itoa(ch.ID), map[string]any{"name": "ops", "type": "webhook", "config": cfg}, "admin")
	}
	if r := put(map[string]any{}); r.status != http.StatusOK {
		t.Fatalf("put: %d %s", r.status, r.body)
	}
	stored, _ := env.store.GetChannel(context.Background(), ch.ID)
	if stored.Config.Template == "" || stored.Config.Quiet == nil {
		t.Fatalf("an update dropped template/quiet: %+v", stored.Config)
	}
	if r := put(map[string]any{"quiet": map[string]any{"enabled": false}, "template": ""}); r.status != http.StatusOK {
		t.Fatalf("put clear: %d %s", r.status, r.body)
	}
	stored, _ = env.store.GetChannel(context.Background(), ch.ID)
	if stored.Config.Template != "" || stored.Config.Quiet != nil {
		t.Fatalf("not cleared: %+v", stored.Config)
	}

	// Validation.
	if r := create(map[string]any{"url": "https://h.invalid/x", "template": "{nope}"}); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "template") {
		t.Errorf("bad placeholder: %d %s", r.status, r.body)
	}
	if r := create(map[string]any{"url": "https://h.invalid/x", "quiet": map[string]any{"enabled": true, "start": "10pm", "end": "07:00"}}); r.status != http.StatusBadRequest || !strings.Contains(string(r.body), "quiet.start") {
		t.Errorf("bad quiet start: %d %s", r.status, r.body)
	}
	// A disabled block is stored as nothing.
	if r := create(map[string]any{"url": "https://h.invalid/x", "quiet": map[string]any{"enabled": false, "start": "garbage"}}); r.status != http.StatusCreated {
		t.Errorf("disabled quiet block: %d %s", r.status, r.body)
	}
}

func TestNewChannelTypesOverAPI(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(http.MethodPost, "/api/channels", map[string]any{"name": "zulip", "type": "zulip", "config": map[string]any{
		"url": "https://zulip.example.invalid", "username": "bot@zulip.example.invalid", "apiKey": "ZULIPKEY-0123456789", "target": "ops", "topic": "alerts", // gitleaks:allow (dummy value)
	}}, "admin")
	if resp.status != http.StatusCreated {
		t.Fatalf("create zulip: %d %s", resp.status, resp.body)
	}
	if strings.Contains(string(resp.body), "ZULIPKEY") || strings.Contains(string(resp.body), "https://zulip.example.invalid") {
		t.Errorf("secret fields leaked: %s", resp.body)
	}
	var ch channelOut
	resp.json(t, &ch)
	for k, want := range map[string]any{"username": "bot@zulip.example.invalid", "target": "ops", "topic": "alerts"} {
		if ch.Config[k] != want {
			t.Errorf("config[%s] = %v, want %v", k, ch.Config[k], want)
		}
	}
	if m, _ := ch.Config["apiKey"].(map[string]any); m["isSet"] != true {
		t.Errorf("apiKey not reported as set: %v", ch.Config["apiKey"])
	}
	// Twilio: the auth token is a masked password, the SID is visible.
	tw := env.do(http.MethodPost, "/api/channels", map[string]any{"name": "sms", "type": "twilio", "config": map[string]any{
		"username": "AC0123456789abcdef", "password": "AUTHTOKEN-xyz", "from": "+15550001111", "to": []string{"+15552223333"},
	}}, "admin")
	if tw.status != http.StatusCreated || strings.Contains(string(tw.body), "AUTHTOKEN") {
		t.Errorf("twilio: %d %s", tw.status, tw.body)
	}
	if r := env.do(http.MethodPost, "/api/channels", map[string]any{"name": "x", "type": "nonesuch", "config": map[string]any{}}, "admin"); r.status != http.StatusBadRequest {
		t.Errorf("unknown type: %d", r.status)
	}
}

func channelIDsOf(t *testing.T, env *testEnv, id int64) []int64 {
	t.Helper()
	m, err := env.store.GetMonitor(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// The engine's copy is what alerts use: it must agree with the database.
	em, _ := env.engine.Monitor(id)
	a, b := slices.Clone(m.ChannelIDs), slices.Clone(em.ChannelIDs)
	slices.Sort(a)
	slices.Sort(b)
	if !slices.Equal(a, b) {
		t.Fatalf("monitor %d: database channels %v, engine channels %v", id, a, b)
	}
	return a
}

func TestApplyChannelToAllMonitors(t *testing.T) {
	env := newTestEnv(t)
	ch := &store.Channel{Name: "ops", Type: "webhook", Config: store.ChannelConfig{URL: "https://h.invalid/x"}}
	if err := env.store.CreateChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	other := &store.Channel{Name: "other", Type: "webhook", Config: store.ChannelConfig{URL: "https://o.invalid/x"}}
	if err := env.store.CreateChannel(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	in := httpMonitorIn("has it", "https://1.example.invalid/")
	in.ChannelIDs = []int64{ch.ID}
	m1 := env.createMonitor(in)
	in = httpMonitorIn("has other", "https://2.example.invalid/")
	in.ChannelIDs = []int64{other.ID}
	m2 := env.createMonitor(in)
	m3 := env.createMonitor(httpMonitorIn("has none", "https://3.example.invalid/"))

	if r := env.do(http.MethodPost, "/api/channels/"+itoa(ch.ID)+"/apply-all", nil, "viewer"); r.status == http.StatusOK {
		t.Fatal("a viewer applied a channel to every monitor")
	}
	resp := env.do(http.MethodPost, "/api/channels/"+itoa(ch.ID)+"/apply-all", nil, "admin")
	if resp.status != http.StatusOK || !strings.Contains(string(resp.body), `"assigned":2`) {
		t.Fatalf("apply-all: %d %s", resp.status, resp.body)
	}
	if got := channelIDsOf(t, env, m1.ID); !slices.Equal(got, []int64{ch.ID}) {
		t.Errorf("m1 channels = %v", got)
	}
	if got := channelIDsOf(t, env, m2.ID); !slices.Equal(got, []int64{ch.ID, other.ID}) {
		t.Errorf("m2 channels = %v, want its own channel kept and the new one added", got)
	}
	if got := channelIDsOf(t, env, m3.ID); !slices.Equal(got, []int64{ch.ID}) {
		t.Errorf("m3 channels = %v", got)
	}
	if info, _ := env.engine.AlertInfo(m3.ID); !slices.Contains(info.ChannelIDs, ch.ID) {
		t.Error("alerts for m3 would not reach the channel")
	}
	// Repeating changes nothing.
	if r := env.do(http.MethodPost, "/api/channels/"+itoa(ch.ID)+"/apply-all", nil, "admin"); !strings.Contains(string(r.body), `"assigned":0`) {
		t.Errorf("second apply-all: %s", r.body)
	}
	if r := env.do(http.MethodPost, "/api/channels/99999/apply-all", nil, "admin"); r.status != http.StatusNotFound {
		t.Errorf("unknown channel: %d", r.status)
	}
}

func TestBulkAddAndRemoveChannel(t *testing.T) {
	env := newTestEnv(t)
	ch := &store.Channel{Name: "ops", Type: "webhook", Config: store.ChannelConfig{URL: "https://h.invalid/x"}}
	if err := env.store.CreateChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	a := env.createMonitor(httpMonitorIn("a", "https://a.example.invalid/"))
	b := env.createMonitor(httpMonitorIn("b", "https://b.example.invalid/"))
	c := env.createMonitor(httpMonitorIn("c", "https://c.example.invalid/"))

	bulk := func(action string, channel any, ids ...int64) *apiResponse {
		return env.do(http.MethodPost, "/api/monitors/bulk", map[string]any{"ids": ids, "action": action, "channelId": channel}, "admin")
	}
	if r := bulk("add_channel", ch.ID, a.ID, b.ID); r.status != http.StatusOK {
		t.Fatalf("add_channel: %d %s", r.status, r.body)
	}
	if got := channelIDsOf(t, env, a.ID); !slices.Equal(got, []int64{ch.ID}) {
		t.Errorf("a = %v", got)
	}
	if got := channelIDsOf(t, env, c.ID); len(got) != 0 {
		t.Errorf("c = %v, want untouched", got)
	}
	// Adding twice does not duplicate.
	if r := bulk("add_channel", ch.ID, a.ID); r.status != http.StatusOK {
		t.Fatal(r.status)
	}
	if got := channelIDsOf(t, env, a.ID); !slices.Equal(got, []int64{ch.ID}) {
		t.Errorf("a after a repeat = %v", got)
	}
	if r := bulk("remove_channel", ch.ID, a.ID); r.status != http.StatusOK {
		t.Fatalf("remove_channel: %d %s", r.status, r.body)
	}
	if got := channelIDsOf(t, env, a.ID); len(got) != 0 {
		t.Errorf("a after remove = %v", got)
	}
	if got := channelIDsOf(t, env, b.ID); !slices.Equal(got, []int64{ch.ID}) {
		t.Errorf("b = %v, want it kept", got)
	}
	if r := bulk("add_channel", 424242, a.ID); r.status != http.StatusBadRequest {
		t.Errorf("unknown channel: %d", r.status)
	}
	if r := bulk("add_channel", nil, a.ID); r.status != http.StatusBadRequest {
		t.Errorf("missing channelId: %d", r.status)
	}
}

func TestDeletingAChildDetachesItFromGroups(t *testing.T) {
	env := newTestEnv(t)
	a := env.createMonitor(httpMonitorIn("a", "https://a.example.invalid/"))
	b := env.createMonitor(httpMonitorIn("b", "https://b.example.invalid/"))
	resp := env.do(http.MethodPost, "/api/monitors", monBody("g", model.TypeGroup, "", map[string]any{
		"children": []int64{a.ID, b.ID}, "groupMode": "all",
	}, nil), "admin")
	if resp.status != http.StatusCreated {
		t.Fatalf("create group: %d %s", resp.status, resp.body)
	}
	var g MonitorOut
	resp.json(t, &g)

	if r := env.do(http.MethodDelete, "/api/monitors/"+itoa(a.ID), nil, "admin"); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d", r.status)
	}
	stored, err := env.store.GetMonitor(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored.Settings.Children, []int64{b.ID}) {
		t.Fatalf("children after deleting a = %v, want [%d]: a reused id must not steer the group", stored.Settings.Children, b.ID)
	}
	if em, _ := env.engine.Monitor(g.ID); !slices.Equal(em.Settings.Children, []int64{b.ID}) {
		t.Fatalf("the engine still has children %v", em.Settings.Children)
	}
}
