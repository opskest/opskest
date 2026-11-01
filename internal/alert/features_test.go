package alert

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

func featureMsg() Message {
	m := Message{
		Kind: model.EvDown,
		Items: []Item{{
			MonitorID: 7, Name: "API Gateway", Reason: "HTTP 503", Target: "https://api.example.com",
			Status: "down", URL: "https://opskest.example/monitors/7", Duration: 90 * time.Second,
		}},
		At:     time.Date(2026, 9, 25, 10, 30, 0, 0, time.UTC),
		AppURL: "https://opskest.example",
	}
	m.Title = Title(m)
	return m
}

// captured is one request a fake service received.
type captured struct {
	method, path string
	header       http.Header
	body         []byte
}

func (c captured) json(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(c.body, &out); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, c.body)
	}
	return out
}

func (c captured) form(t *testing.T) url.Values {
	t.Helper()
	v, err := url.ParseQuery(string(c.body))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

type fakeService struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []captured
}

func newFakeService(t *testing.T) *fakeService {
	t.Helper()
	f := &fakeService{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, captured{r.Method, r.URL.EscapedPath(), r.Header.Clone(), b})
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeService) only(t *testing.T) captured {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) != 1 {
		t.Fatalf("service got %d requests, want 1", len(f.reqs))
	}
	return f.reqs[0]
}

func send(t *testing.T, typ string, cfg store.ChannelConfig, m Message) error {
	t.Helper()
	s, ok := SenderFor(typ)
	if !ok {
		t.Fatalf("no sender for %s", typ)
	}
	return s.Send(context.Background(), cfg, m)
}

func basic(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// ── providers ─────────────────────────────────────────────────────

func TestZulipSender(t *testing.T) {
	f := newFakeService(t)
	cfg := store.ChannelConfig{URL: f.URL + "/", Username: "bot@zulip.example", APIKey: "KEY123", Target: "ops", Topic: "uptime"}
	if err := send(t, "zulip", cfg, featureMsg()); err != nil {
		t.Fatal(err)
	}
	c := f.only(t)
	form := c.form(t)
	if c.method != "POST" || c.path != "/api/v1/messages" {
		t.Errorf("request = %s %s", c.method, c.path)
	}
	if c.header.Get("Authorization") != basic("bot@zulip.example", "KEY123") {
		t.Errorf("Authorization = %q", c.header.Get("Authorization"))
	}
	if form.Get("type") != "stream" || form.Get("to") != "ops" || form.Get("topic") != "uptime" || !strings.Contains(form.Get("content"), "API Gateway") {
		t.Errorf("form = %v", form)
	}
}

func TestWebexSender(t *testing.T) {
	f := newFakeService(t)
	if err := send(t, "webex", store.ChannelConfig{URL: f.URL, APIKey: "BOTTOKEN", Target: "Y2lzY29zcGFyazovL3Jvb20"}, featureMsg()); err != nil {
		t.Fatal(err)
	}
	c := f.only(t)
	body := c.json(t)
	if c.path != "/v1/messages" || c.header.Get("Authorization") != "Bearer BOTTOKEN" || body["roomId"] != "Y2lzY29zcGFyazovL3Jvb20" {
		t.Errorf("request = %s %v %v", c.path, c.header.Get("Authorization"), body)
	}
	if !strings.Contains(body["markdown"].(string), "API Gateway") {
		t.Errorf("markdown = %v", body["markdown"])
	}
}

func TestPushbulletSender(t *testing.T) {
	for _, tc := range []struct{ target, field string }{{"", ""}, {"me@example.com", "email"}, {"ujpah72o0sjAoRtnM0jc", "device_iden"}} {
		f := newFakeService(t)
		if err := send(t, "pushbullet", store.ChannelConfig{URL: f.URL, APIKey: "o.TOKEN", Target: tc.target}, featureMsg()); err != nil {
			t.Fatal(err)
		}
		c := f.only(t)
		body := c.json(t)
		if c.path != "/v2/pushes" || c.header.Get("Access-Token") != "o.TOKEN" {
			t.Errorf("request = %s token=%q", c.path, c.header.Get("Access-Token"))
		}
		if body["type"] != "link" || body["url"] != "https://opskest.example/monitors/7" || !strings.Contains(body["title"].(string), "API Gateway") {
			t.Errorf("body = %v", body)
		}
		for _, k := range []string{"email", "device_iden"} {
			_, has := body[k]
			if has != (k == tc.field) {
				t.Errorf("target %q: field %s present=%v", tc.target, k, has)
			}
		}
	}
}

func TestIFTTTSender(t *testing.T) {
	f := newFakeService(t)
	if err := send(t, "ifttt", store.ChannelConfig{URL: f.URL, APIKey: "dKEY", Target: "opskest_alert"}, featureMsg()); err != nil {
		t.Fatal(err)
	}
	c := f.only(t)
	body := c.json(t)
	if c.path != "/trigger/opskest_alert/with/key/dKEY" {
		t.Errorf("path = %s", c.path)
	}
	if !strings.Contains(body["value1"].(string), "API Gateway") || body["value3"] != "https://opskest.example/monitors/7" {
		t.Errorf("body = %v", body)
	}
}

func TestAppriseSender(t *testing.T) {
	f := newFakeService(t)
	cfg := store.ChannelConfig{URL: f.URL, Target: "ops-team", Username: "u", Password: "p"}
	if err := send(t, "apprise", cfg, featureMsg()); err != nil {
		t.Fatal(err)
	}
	c := f.only(t)
	body := c.json(t)
	if c.path != "/notify/ops-team" || body["type"] != "failure" || c.header.Get("Authorization") != basic("u", "p") {
		t.Errorf("request = %s %v auth=%q", c.path, body, c.header.Get("Authorization"))
	}
	rec := featureMsg()
	rec.Kind = model.EvRecovered
	f2 := newFakeService(t)
	cfg.URL = f2.URL
	cfg.Username, cfg.Password = "", ""
	if err := send(t, "apprise", cfg, rec); err != nil {
		t.Fatal(err)
	}
	c2 := f2.only(t)
	if c2.json(t)["type"] != "success" || c2.header.Get("Authorization") != "" {
		t.Errorf("recovery = %v auth=%q", c2.json(t), c2.header.Get("Authorization"))
	}
}

func TestTwilioSender(t *testing.T) {
	f := newFakeService(t)
	m := featureMsg()
	m.Items[0].Reason = strings.Repeat("very long reason ", 100)
	cfg := store.ChannelConfig{URL: f.URL, Username: "AC0123456789abcdef0123456789abcdef", Password: "authtoken", From: "+15550001111", To: []string{"+15552223333", "+15554445555"}}
	if err := send(t, "twilio", cfg, m); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	reqs := append([]captured(nil), f.reqs...)
	f.mu.Unlock()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want one per recipient", len(reqs))
	}
	for i, c := range reqs {
		form := c.form(t)
		if c.path != "/2010-04-01/Accounts/AC0123456789abcdef0123456789abcdef/Messages.json" {
			t.Errorf("path = %s", c.path)
		}
		if c.header.Get("Authorization") != basic("AC0123456789abcdef0123456789abcdef", "authtoken") {
			t.Errorf("Authorization = %q", c.header.Get("Authorization"))
		}
		if form.Get("To") != cfg.To[i] || form.Get("From") != "+15550001111" {
			t.Errorf("form = %v", form)
		}
		if n := len([]rune(form.Get("Body"))); n > smsLimit || n < 20 {
			t.Errorf("Body is %d runes, want a bounded text under %d", n, smsLimit)
		}
	}

	// A messaging service id is sent as such.
	f2 := newFakeService(t)
	cfg.URL, cfg.From, cfg.To = f2.URL, "MG0123456789abcdef", []string{"+15552223333"}
	if err := send(t, "twilio", cfg, featureMsg()); err != nil {
		t.Fatal(err)
	}
	form := f2.only(t).form(t)
	if form.Get("MessagingServiceSid") != "MG0123456789abcdef" || form.Get("From") != "" {
		t.Errorf("messaging service form = %v", form)
	}
}

func TestHomeAssistantSender(t *testing.T) {
	f := newFakeService(t)
	if err := send(t, "homeassistant", store.ChannelConfig{URL: f.URL, APIKey: "LLAT", Target: "mobile_app_pixel"}, featureMsg()); err != nil {
		t.Fatal(err)
	}
	c := f.only(t)
	body := c.json(t)
	if c.path != "/api/services/notify/mobile_app_pixel" || c.header.Get("Authorization") != "Bearer LLAT" {
		t.Errorf("request = %s %q", c.path, c.header.Get("Authorization"))
	}
	if !strings.Contains(body["title"].(string), "API Gateway") || body["data"].(map[string]any)["url"] != "https://opskest.example/monitors/7" {
		t.Errorf("body = %v", body)
	}
}

func TestBarkSender(t *testing.T) {
	f := newFakeService(t)
	if err := send(t, "bark", store.ChannelConfig{URL: f.URL, APIKey: "DEVICEKEY"}, featureMsg()); err != nil {
		t.Fatal(err)
	}
	c := f.only(t)
	body := c.json(t)
	if c.path != "/push" || body["device_key"] != "DEVICEKEY" || body["level"] != "timeSensitive" || body["group"] != "opskest" {
		t.Errorf("request = %s %v", c.path, body)
	}
}

func TestSignalSender(t *testing.T) {
	f := newFakeService(t)
	cfg := store.ChannelConfig{URL: f.URL, From: "+15550001111", To: []string{"+15552223333"}, Username: "u", Password: "p"}
	if err := send(t, "signal", cfg, featureMsg()); err != nil {
		t.Fatal(err)
	}
	c := f.only(t)
	body := c.json(t)
	rcpts, _ := body["recipients"].([]any)
	if c.path != "/v2/send" || body["number"] != "+15550001111" || len(rcpts) != 1 || rcpts[0] != "+15552223333" {
		t.Errorf("request = %s %v", c.path, body)
	}
	if !strings.Contains(body["message"].(string), "API Gateway") || c.header.Get("Authorization") != basic("u", "p") {
		t.Errorf("message/auth wrong: %v %q", body["message"], c.header.Get("Authorization"))
	}
}

// providerConfigs is one valid config per new channel type, pointing at url.
func providerConfigs(url string) map[string]store.ChannelConfig {
	return map[string]store.ChannelConfig{
		"zulip":         {URL: url, Username: "bot@x", APIKey: "KEY", Target: "ops"},
		"webex":         {URL: url, APIKey: "KEY", Target: "room"},
		"pushbullet":    {URL: url, APIKey: "KEY"},
		"ifttt":         {URL: url, APIKey: "KEY", Target: "evt"},
		"apprise":       {URL: url, Target: "cfg"},
		"twilio":        {URL: url, Username: "AC0123456789abcdef", Password: "tok", From: "+15550001111", To: []string{"+15552223333"}},
		"homeassistant": {URL: url, APIKey: "KEY", Target: "notify"},
		"bark":          {URL: url, APIKey: "KEY"},
		"signal":        {URL: url, From: "+15550001111", To: []string{"+15552223333"}},
	}
}

func TestNewProvidersNeverFollowRedirects(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer elsewhere.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/stolen", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	for typ, cfg := range providerConfigs(redirector.URL) {
		err := send(t, typ, cfg, featureMsg())
		if err == nil || !strings.Contains(err.Error(), "http 307") {
			t.Errorf("%s: err = %v, want a failed delivery reporting the 307", typ, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("a redirect was followed %d times", hits.Load())
	}
}

func TestNewProviderErrorsNeverEchoTheURLOrKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	for typ, cfg := range providerConfigs(srv.URL) {
		err := send(t, typ, cfg, featureMsg())
		if err == nil {
			t.Errorf("%s: expected an error", typ)
			continue
		}
		if strings.Contains(err.Error(), srv.URL) || strings.Contains(err.Error(), "KEY") || strings.Contains(err.Error(), "127.0.0.1") {
			t.Errorf("%s: error leaks the URL or key: %v", typ, err)
		}
	}
}

func TestNewProvidersBoundTheMessage(t *testing.T) {
	huge := featureMsg()
	huge.Items[0].Reason = strings.Repeat("é", 20000)
	huge.Items[0].Response = strings.Repeat("x", 20000)
	for typ, cfg := range providerConfigs("") {
		f := newFakeService(t)
		cfg.URL = f.URL
		if err := send(t, typ, cfg, huge); err != nil {
			t.Errorf("%s: %v", typ, err)
			continue
		}
		f.mu.Lock()
		for _, c := range f.reqs {
			if len(c.body) > 16*1024 {
				t.Errorf("%s: request body is %d bytes for a huge alert, want it bounded", typ, len(c.body))
			}
		}
		f.mu.Unlock()
	}
}

func TestNewProviderValidation(t *testing.T) {
	for typ, cfg := range providerConfigs("https://example.com") {
		if fe := ValidateConfig(typ, cfg); len(fe) != 0 {
			t.Errorf("%s: valid config rejected: %v", typ, fe)
		}
		if fe := ValidateConfig(typ, store.ChannelConfig{}); len(fe) == 0 {
			t.Errorf("%s: an empty config was accepted", typ)
		}
		bad := cfg
		bad.URL = "ftp://example.com"
		if fe := ValidateConfig(typ, bad); fe["url"] == "" {
			t.Errorf("%s: a ftp:// URL was accepted", typ)
		}
	}
	// Specific rules.
	for _, tc := range []struct {
		typ string
		mut func(*store.ChannelConfig)
		key string
	}{
		{"ifttt", func(c *store.ChannelConfig) { c.Target = "a/b" }, "target"},
		{"apprise", func(c *store.ChannelConfig) { c.Target = "../etc" }, "target"},
		{"homeassistant", func(c *store.ChannelConfig) { c.Target = "Notify.Bad" }, "target"},
		{"twilio", func(c *store.ChannelConfig) { c.To = []string{"5551234"} }, "to"},
		{"twilio", func(c *store.ChannelConfig) { c.From = "555" }, "from"},
		{"signal", func(c *store.ChannelConfig) { c.To = nil }, "to"},
		{"zulip", func(c *store.ChannelConfig) { c.Topic = strings.Repeat("t", 61) }, "topic"},
	} {
		cfg := providerConfigs("https://example.com")[tc.typ]
		tc.mut(&cfg)
		if fe := ValidateConfig(tc.typ, cfg); fe[tc.key] == "" {
			t.Errorf("%s: expected an error for %q, got %v", tc.typ, tc.key, fe)
		}
	}
}

func TestNewProvidersAreSingleMessageChannels(t *testing.T) {
	for typ := range providerConfigs("") {
		if isChatType(typ) {
			t.Errorf("%s must not receive grouped storm messages", typ)
		}
	}
}

// ── templates ─────────────────────────────────────────────────────

func TestRenderTemplatePlaceholders(t *testing.T) {
	m := featureMsg()
	m.Loc = time.FixedZone("IST", 19800)
	got := RenderTemplate("{name} is {status}: {reason} (for {duration}) {url} at {time}", m, m.Items[0])
	want := "API Gateway is down: HTTP 503 (for 2m) https://opskest.example/monitors/7 at 2026-09-25 16:00:00 IST"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestRenderTemplateIsInert(t *testing.T) {
	m := featureMsg()
	// A value that looks like a placeholder is not expanded again, and line
	// breaks in values cannot reshape the message.
	m.Items[0].Name = "{reason}\nINJECTED"
	m.Items[0].Reason = "{url}"
	got := RenderTemplate("{name} / {reason}", m, m.Items[0])
	if got != "{reason} INJECTED / {url}" {
		t.Fatalf("got %q", got)
	}
	// Unknown braces and code-like text stay literal; only the known names expand.
	m2 := featureMsg()
	if got := RenderTemplate(`{{.Name}} ${name} {"a": 1} {unknown}`, m2, m2.Items[0]); got != `{{.Name}} $API Gateway {"a": 1} {unknown}` {
		t.Fatalf("got %q", got)
	}
	// Output and values are bounded.
	m.Items[0].Reason = strings.Repeat("r", 5000)
	out := RenderTemplate(strings.Repeat("{reason}", 20), m, m.Items[0])
	if n := len([]rune(out)); n > templateOutMax {
		t.Fatalf("output is %d runes, want at most %d", n, templateOutMax)
	}
	// Empty values vanish rather than printing placeholders.
	m.Items[0].Duration = 0
	if got := RenderTemplate("[{duration}]", m, m.Items[0]); got != "[]" {
		t.Fatalf("got %q", got)
	}
}

func TestTemplateProblem(t *testing.T) {
	if p := TemplateProblem("{name} is {status} - {reason} {duration} {url} {time}"); p != "" {
		t.Errorf("valid template rejected: %s", p)
	}
	if p := TemplateProblem("{nmae} is down"); !strings.Contains(p, "{nmae}") {
		t.Errorf("typo not reported: %q", p)
	}
	if p := TemplateProblem(strings.Repeat("a", MaxTemplateLen+1)); p == "" {
		t.Error("an over-long template was accepted")
	}
	if p := TemplateProblem(`{"text": "x"}`); p != "" {
		t.Errorf("JSON-like text rejected: %s", p)
	}
	if fe := ValidateConfig("webhook", store.ChannelConfig{URL: "https://x.example", Template: "{nope}"}); fe["template"] == "" {
		t.Error("ValidateConfig accepted an unknown placeholder")
	}
}

func TestTemplateReachesEveryRenderer(t *testing.T) {
	m := featureMsg()
	m = withChannelTemplate(store.ChannelConfig{Template: "PAGE {name}: {reason}"}, m)
	if m.Custom != "PAGE API Gateway: HTTP 503" {
		t.Fatalf("Custom = %q", m.Custom)
	}
	if got := bodyLines(m); got != m.Custom {
		t.Errorf("bodyLines = %q", got)
	}
	if got := buildSlackPayload(m); !strings.Contains(slackBlocksText(t, got), "PAGE API Gateway: HTTP 503") {
		t.Errorf("slack payload misses the custom text")
	}
	if w := buildWebhookPayload(m); w.Message != m.Custom {
		t.Errorf("webhook message = %q", w.Message)
	}
	if body := plainMessage(m); !strings.Contains(body, "PAGE API Gateway: HTTP 503") || !strings.HasPrefix(body, m.Title) {
		t.Errorf("plainMessage = %q (the headline must stay)", body)
	}
	if tm := buildTeamsPayload(m); !strings.Contains(mustJSON(t, tm), "PAGE API Gateway") {
		t.Error("teams card misses the custom text")
	}

	// A grouped (storm) message keeps its listing.
	g := Message{Kind: model.EvDown, Grouped: true, Items: []Item{{Name: "a"}, {Name: "b"}}}
	if got := withChannelTemplate(store.ChannelConfig{Template: "X"}, g); got.Custom != "" {
		t.Error("a grouped message must not be templated")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func slackBlocksText(t *testing.T, p slackPayload) string { return mustJSON(t, p) }

func TestDispatcherAppliesChannelTemplate(t *testing.T) {
	st, w := newTestStore(t)
	cs := &captureSender{}
	swapSender(t, "webhook", cs)
	ch := createTestChannel(t, st, "c", "webhook", store.ChannelConfig{URL: "https://x.example", Template: "{name} -> {status}"})
	plain := createTestChannel(t, st, "p", "webhook", store.ChannelConfig{URL: "https://y.example"})

	lk := newFakeLookup()
	lk.set(1, Info{Name: "Mon", ChannelIDs: []int64{ch.ID, plain.ID}})
	d := NewDispatcher(st, w, lk, Options{AppURL: "https://u.example", Hold: 5 * time.Millisecond})
	d.Start(context.Background())
	defer d.Stop()

	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 1, At: time.Now(), Status: model.StatusDown, Reason: "boom"})
	waitFor(t, 2*time.Second, func() bool { return cs.count() == 2 })
	var custom, def int
	for _, m := range cs.all() {
		if m.Custom == "Mon -> down" {
			custom++
		} else if m.Custom == "" {
			def++
		}
	}
	if custom != 1 || def != 1 {
		t.Fatalf("custom=%d default=%d, want one of each", custom, def)
	}

	// The test button shows the template too.
	before := cs.count()
	if err := d.SendTest(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	if got := cs.all()[before].Custom; !strings.HasPrefix(got, "opskest -> ") {
		t.Errorf("test message custom = %q, want the channel's template applied", got)
	}
}

// ── quiet hours ───────────────────────────────────────────────────

func at(day time.Weekday, hh, mm int) time.Time {
	// 2026-09-20 is a Sunday.
	return time.Date(2026, 9, 20+int(day), hh, mm, 0, 0, time.UTC)
}

func TestQuietActive(t *testing.T) {
	night := &store.QuietHours{Enabled: true, Start: "22:00", End: "07:00"}
	weeknights := &store.QuietHours{Enabled: true, Days: []int{1, 2, 3, 4, 5}, Start: "22:00", End: "07:00"}
	office := &store.QuietHours{Enabled: true, Days: []int{6, 0}, Start: "09:00", End: "17:00"}
	for _, tc := range []struct {
		name string
		q    *store.QuietHours
		now  time.Time
		want bool
	}{
		{"night, evening", night, at(time.Wednesday, 23, 0), true},
		{"night, small hours", night, at(time.Thursday, 3, 0), true},
		{"night, exact start", night, at(time.Wednesday, 22, 0), true},
		{"night, exact end", night, at(time.Thursday, 7, 0), false},
		{"night, daytime", night, at(time.Wednesday, 12, 0), false},
		{"weeknights, Friday evening", weeknights, at(time.Friday, 23, 0), true},
		{"weeknights, Saturday small hours (Friday's window)", weeknights, at(time.Saturday, 3, 0), true},
		{"weeknights, Saturday evening", weeknights, at(time.Saturday, 23, 0), false},
		{"weeknights, Sunday small hours (Saturday's window)", weeknights, at(time.Sunday, 3, 0), false},
		{"weeknights, Monday small hours (Sunday's window)", weeknights, at(time.Monday, 3, 0), false},
		{"weeknights, Monday evening", weeknights, at(time.Monday, 22, 30), true},
		{"weekend daytime, Saturday", office, at(time.Saturday, 10, 0), true},
		{"weekend daytime, Monday", office, at(time.Monday, 10, 0), false},
		{"weekend daytime, after hours", office, at(time.Sunday, 17, 0), false},
		{"disabled", &store.QuietHours{Start: "00:00", End: "23:59"}, at(time.Monday, 12, 0), false},
		{"nil", nil, at(time.Monday, 12, 0), false},
		{"empty window", &store.QuietHours{Enabled: true, Start: "08:00", End: "08:00"}, at(time.Monday, 8, 0), false},
		{"bad clock", &store.QuietHours{Enabled: true, Start: "8am", End: "9am"}, at(time.Monday, 8, 30), false},
	} {
		if got := QuietActive(tc.q, tc.now); got != tc.want {
			t.Errorf("%s: QuietActive = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestQuietProblems(t *testing.T) {
	if fe := QuietProblems(&store.QuietHours{Enabled: true, Days: []int{0, 6}, Start: "22:00", End: "07:00"}); len(fe) != 0 {
		t.Errorf("valid rejected: %v", fe)
	}
	if fe := QuietProblems(&store.QuietHours{Enabled: false, Start: "nonsense"}); len(fe) != 0 {
		t.Errorf("a disabled block is not validated: %v", fe)
	}
	for name, q := range map[string]*store.QuietHours{
		"quiet.start": {Enabled: true, Start: "25:00", End: "07:00"},
		"quiet.end":   {Enabled: true, Start: "22:00", End: "7:00"},
		"quiet.days":  {Enabled: true, Days: []int{7}, Start: "22:00", End: "07:00"},
	} {
		if fe := QuietProblems(q); fe[name] == "" {
			t.Errorf("%s not reported: %v", name, fe)
		}
	}
	if fe := QuietProblems(&store.QuietHours{Enabled: true, Start: "08:00", End: "08:00"}); fe["quiet.end"] == "" {
		t.Error("an empty window was accepted")
	}
	if fe := ValidateConfig("webhook", store.ChannelConfig{URL: "https://x.example", Quiet: &store.QuietHours{Enabled: true, Start: "x", End: "y"}}); fe["quiet.start"] == "" {
		t.Error("ValidateConfig ignored a bad quiet window")
	}
}

func TestDispatcherQuietHours(t *testing.T) {
	st, w := newTestStore(t)
	cs := &captureSender{}
	swapSender(t, "webhook", cs)

	ist := time.FixedZone("IST", 19800)
	// 20:00 UTC is 01:30 the next day in IST: inside a 22:00-07:00 window.
	now := time.Date(2026, 9, 23, 20, 0, 0, 0, time.UTC)
	quiet := func(bypass bool) *store.QuietHours {
		return &store.QuietHours{Enabled: true, Start: "22:00", End: "07:00", BypassCritical: bypass}
	}
	silent := createTestChannel(t, st, "silent", "webhook", store.ChannelConfig{URL: "https://a.example", Quiet: quiet(false)})
	bypass := createTestChannel(t, st, "bypass", "webhook", store.ChannelConfig{URL: "https://b.example", Quiet: quiet(true)})
	open := createTestChannel(t, st, "open", "webhook", store.ChannelConfig{URL: "https://c.example"})
	dayTime := createTestChannel(t, st, "day", "webhook", store.ChannelConfig{URL: "https://d.example", Quiet: &store.QuietHours{Enabled: true, Start: "09:00", End: "17:00"}})

	lk := newFakeLookup()
	lk.set(1, Info{Name: "Mon", ChannelIDs: []int64{silent.ID, bypass.ID, open.ID, dayTime.ID}})
	d := NewDispatcher(st, w, lk, Options{AppURL: "https://u.example", Hold: 5 * time.Millisecond, Loc: ist, Now: func() time.Time { return now }})
	d.Start(context.Background())
	defer d.Stop()

	// A down alert reaches the bypass channel, the open channel and the one
	// whose quiet window is the working day; the silent one holds it.
	d.Publish(model.Event{Kind: model.EvDown, MonitorID: 1, At: now, Status: model.StatusDown, Reason: "x"})
	waitFor(t, 2*time.Second, func() bool { return cs.count() >= 3 })
	time.Sleep(60 * time.Millisecond)
	if n := cs.count(); n != 3 {
		t.Fatalf("down alert delivered %d times, want 3 (silent channel held it)", n)
	}
	if d.Suppressed() != 1 {
		t.Errorf("Suppressed = %d, want 1", d.Suppressed())
	}

	// A degraded alert is not critical: bypass does not apply.
	d.Publish(model.Event{Kind: model.EvDegraded, MonitorID: 1, At: now, Status: model.StatusDegraded, Reason: "slow"})
	waitFor(t, 2*time.Second, func() bool { return cs.count() >= 5 })
	time.Sleep(60 * time.Millisecond)
	if n := cs.count(); n != 5 {
		t.Fatalf("after the degraded alert: %d deliveries, want 5 (open and daytime only)", n)
	}

	// Recovery and reminders count as critical for a bypass channel.
	d.Publish(model.Event{Kind: model.EvReminder, MonitorID: 1, At: now, Status: model.StatusDown})
	waitFor(t, 2*time.Second, func() bool { return cs.count() >= 8 })
	time.Sleep(60 * time.Millisecond)
	if n := cs.count(); n != 8 {
		t.Fatalf("after the reminder: %d deliveries, want 8", n)
	}

	// The test button ignores quiet hours.
	before := cs.count()
	if err := d.SendTest(context.Background(), silent); err != nil {
		t.Fatal(err)
	}
	if cs.count() != before+1 {
		t.Error("SendTest was held back by quiet hours")
	}
}
