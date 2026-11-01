package api

import (
	"context"
	"io"
	"mime/quotedprintable"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"opskest/internal/store"
)

var tokenRE = regexp.MustCompile(`token=([0-9a-f]{64})`)

// mailText is a sent message's headers plus its decoded (quoted-printable)
// body, so assertions see the text a reader sees.
func mailText(raw string) string {
	i := strings.Index(raw, "\r\n\r\n")
	if i < 0 {
		return raw
	}
	body, _ := io.ReadAll(quotedprintable.NewReader(strings.NewReader(raw[i+4:])))
	return raw[:i] + "\r\n\r\n" + string(body)
}

// subscribePage creates a public page with subscriptions on, sending mail
// through a (fake) email channel, plus a monitor on it.
func subscribePage(t *testing.T, env *testEnv, slug, visibility string, enabled bool) (pageID, monitorID int64) {
	t.Helper()
	ch := &store.Channel{Name: "Status mail", Type: "email", Config: store.ChannelConfig{
		Host: "smtp.invalid", Port: 25, From: "status@example.com", To: []string{"ops@example.com"},
	}}
	if err := env.store.CreateChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	mon := env.createMonitor(httpMonitorIn("checkout "+slug, "https://secret-host.internal/"+slug))
	r := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": slug, "title": "Acme", "visibility": visibility, "layout": "list",
		"subscriptionsEnabled": enabled, "subscriberChannelId": ch.ID,
		"items": []map[string]any{{"monitorId": mon.ID, "sort": 0}},
	}, "admin")
	if r.status != http.StatusCreated {
		t.Fatalf("create page: %d %s", r.status, r.body)
	}
	var p struct {
		ID int64 `json:"id"`
	}
	r.json(t, &p)
	return p.ID, mon.ID
}

func (e *testEnv) subscribe(slug string, body map[string]any) *apiResponse {
	return e.do(http.MethodPost, "/api/public/pages/"+slug+"/subscribe", body, "")
}

func TestEmailSubscriptionLifecycle(t *testing.T) {
	env := newTestEnv(t)
	pageID, _ := subscribePage(t, env, "acme", "public", true)

	// The page tells visitors which options exist.
	pg := env.do(http.MethodGet, "/api/public/pages/acme", nil, "")
	if !bytesContains(pg.body, `"subscribeEmail":true`) || !bytesContains(pg.body, `"subscribeSlack":true`) {
		t.Fatalf("page doesn't advertise subscriptions: %s", pg.body)
	}

	first := env.subscribe("acme", map[string]any{"email": "Alice@Example.com"})
	if first.status != http.StatusAccepted {
		t.Fatalf("subscribe: %d %s", first.status, first.body)
	}
	env.notifier.Wait()
	sent := env.mail.take()
	if len(sent) != 1 || sent[0].to[0] != "alice@example.com" || !strings.Contains(mailText(sent[0].msg), "/status/acme/subscribe/confirm?token=") {
		t.Fatalf("confirmation mail = %+v", sent)
	}
	token := tokenRE.FindStringSubmatch(mailText(sent[0].msg))[1]

	// Same address again: identical answer (no "already subscribed" leak).
	again := env.subscribe("acme", map[string]any{"email": "alice@example.com"})
	if again.status != first.status || string(again.body) != string(first.body) {
		t.Fatalf("duplicate answered differently: %d %s vs %s", again.status, again.body, first.body)
	}
	env.notifier.Wait()
	if n := len(env.mail.take()); n != 0 {
		t.Fatalf("re-submitting inside the cooldown sent %d more mails (mail bombing)", n)
	}

	// Unconfirmed: an incident update reaches nobody.
	inc := env.do(http.MethodPost, "/api/incidents", map[string]any{
		"title": "Checkout errors", "impact": "major", "body": "Looking into it.", "pageIds": []int64{pageID},
	}, "admin")
	var created struct {
		ID int64 `json:"id"`
	}
	inc.json(t, &created)
	env.notifier.Wait()
	if n := len(env.mail.take()); n != 0 {
		t.Fatalf("unconfirmed subscriber got %d mails", n)
	}

	if r := env.do(http.MethodPost, "/api/public/subscriptions/confirm", map[string]any{"token": strings.Repeat("0", 64)}, ""); r.status != http.StatusNotFound {
		t.Errorf("bogus token: %d, want 404", r.status)
	}
	if r := env.do(http.MethodPost, "/api/public/subscriptions/confirm", map[string]any{"token": token}, ""); r.status != http.StatusOK {
		t.Fatalf("confirm: %d %s", r.status, r.body)
	}

	// Public update → mailed, with an unsubscribe link; internal → not.
	if r := env.do(http.MethodPost, "/api/incidents/"+itoa(created.ID)+"/updates", map[string]any{
		"status": "identified", "body": "internal: db primary wedged", "internal": true,
	}, "admin"); r.status != http.StatusCreated {
		t.Fatalf("internal update: %d %s", r.status, r.body)
	}
	env.notifier.Wait()
	if n := len(env.mail.take()); n != 0 {
		t.Fatalf("internal update mailed %d subscribers", n)
	}
	env.do(http.MethodPost, "/api/incidents/"+itoa(created.ID)+"/updates", map[string]any{"status": "monitoring", "body": "A fix is rolling out."}, "admin")
	env.notifier.Wait()
	got := env.mail.take()
	if len(got) != 1 || !strings.Contains(mailText(got[0].msg), "A fix is rolling out.") || !strings.Contains(got[0].msg, "List-Unsubscribe: <http://app.invalid/status/acme/unsubscribe?token=") {
		t.Fatalf("update mail = %+v", got)
	}
	if txt := mailText(got[0].msg); strings.Contains(txt, "secret-host") || strings.Contains(txt, "wedged") {
		t.Fatalf("update mail leaks private data: %s", txt)
	}

	// Admin sees counts and the address, never the token.
	list := env.do(http.MethodGet, "/api/pages/"+itoa(pageID)+"/subscribers", nil, "admin")
	if !bytesContains(list.body, `"confirmed":1`) || !bytesContains(list.body, "alice@example.com") || bytesContains(list.body, token) {
		t.Fatalf("admin list = %s", list.body)
	}
	if r := env.do(http.MethodGet, "/api/pages/"+itoa(pageID)+"/subscribers", nil, "viewer"); r.status == http.StatusOK {
		t.Errorf("viewer can list subscribers")
	}

	// Unsubscribe: no more mail; the token is then unknown.
	if r := env.do(http.MethodPost, "/api/public/subscriptions/unsubscribe", map[string]any{"token": token}, ""); r.status != http.StatusOK {
		t.Fatalf("unsubscribe: %d", r.status)
	}
	if r := env.do(http.MethodPost, "/api/public/subscriptions/unsubscribe", map[string]any{"token": token}, ""); r.status != http.StatusNotFound {
		t.Errorf("second unsubscribe: %d, want 404", r.status)
	}
	env.do(http.MethodPost, "/api/incidents/"+itoa(created.ID)+"/updates", map[string]any{"status": "resolved", "body": "Fixed."}, "admin")
	env.notifier.Wait()
	if n := len(env.mail.take()); n != 0 {
		t.Fatalf("unsubscribed address got %d mails", n)
	}
}

func TestSlackSubscription(t *testing.T) {
	env := newTestEnv(t)
	pageID, _ := subscribePage(t, env, "slk", "public", true)

	for _, bad := range []string{"http://hooks.slack.com/services/x", "https://evil.example.com/services/x", "https://hooks.slack.com/other"} {
		if r := env.subscribe("slk", map[string]any{"slackWebhookUrl": bad}); r.status != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", bad, r.status)
		}
	}
	env.slack.failWith = "webhook said no"
	if r := env.subscribe("slk", map[string]any{"slackWebhookUrl": "https://hooks.slack.com/services/T/B/x"}); r.status != http.StatusBadRequest {
		t.Errorf("unreachable webhook accepted: %d", r.status)
	}
	env.slack.failWith = ""
	env.slack.take()

	hook := "https://hooks.slack.com/services/T000/B000/secretsecret"
	if r := env.subscribe("slk", map[string]any{"slackWebhookUrl": hook}); r.status != http.StatusCreated {
		t.Fatalf("subscribe slack: %d %s", r.status, r.body)
	}
	if posts := env.slack.take(); len(posts) != 1 || !strings.HasPrefix(posts[0], hook) {
		t.Fatalf("welcome post = %v", posts)
	}
	env.do(http.MethodPost, "/api/incidents", map[string]any{
		"title": "Slow checkout", "impact": "minor", "body": "Investigating.", "pageIds": []int64{pageID},
	}, "admin")
	env.notifier.Wait()
	posts := env.slack.take()
	if len(posts) != 1 || !strings.Contains(posts[0], "Slow checkout") || !strings.Contains(posts[0], "Unsubscribe") {
		t.Fatalf("incident post = %v", posts)
	}
	// The webhook URL is a secret: never in the admin list.
	list := env.do(http.MethodGet, "/api/pages/"+itoa(pageID)+"/subscribers", nil, "admin")
	if bytesContains(list.body, "secretsecret") {
		t.Fatalf("admin list leaks the webhook: %s", list.body)
	}

	// The same webhook again is stored once.
	env.subscribe("slk", map[string]any{"slackWebhookUrl": hook})
	env.slack.take()
	if l := env.do(http.MethodGet, "/api/pages/"+itoa(pageID)+"/subscribers", nil, "admin"); !bytesContains(l.body, `"confirmed":1`) {
		t.Fatalf("duplicate webhook stored twice: %s", l.body)
	}

	// Slack deleted the webhook (410): it's dropped, not retried forever.
	env.slack.failWith = "http 410: no_service"
	env.do(http.MethodPost, "/api/incidents", map[string]any{
		"title": "Again", "impact": "minor", "body": "x", "pageIds": []int64{pageID},
	}, "admin")
	env.notifier.Wait()
	if l := env.do(http.MethodGet, "/api/pages/"+itoa(pageID)+"/subscribers", nil, "admin"); !bytesContains(l.body, `"confirmed":0`) {
		t.Fatalf("dead webhook kept: %s", l.body)
	}
}

func TestSubscribeNotOffered404s(t *testing.T) {
	env := newTestEnv(t)
	subscribePage(t, env, "off", "public", false)
	subscribePage(t, env, "priv", "private", true)
	for _, slug := range []string{"off", "priv", "nope"} {
		if r := env.subscribe(slug, map[string]any{"email": "a@example.com"}); r.status != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", slug, r.status)
		}
	}
	if r := env.subscribe("off", map[string]any{"email": "not-an-email"}); r.status != http.StatusNotFound {
		t.Errorf("disabled page validated input before 404: %d", r.status)
	}
}

func TestSubscribeInputAndRateLimit(t *testing.T) {
	env := newTestEnv(t)
	subscribePage(t, env, "rl", "public", true)
	for _, bad := range []map[string]any{
		{"email": "not-an-email"},
		{"email": "Bob <bob@example.com>"},
		{"email": "a@example.com\r\nBcc: x@example.com"},
		{},
		{"email": "a@example.com", "slackWebhookUrl": "https://hooks.slack.com/services/x"},
	} {
		if r := env.subscribe("rl", bad); r.status != http.StatusBadRequest {
			t.Errorf("%v: %d, want 400", bad, r.status)
		}
	}
	// 5 attempts already used; the next is limited.
	if r := env.subscribe("rl", map[string]any{"email": "z@example.com"}); r.status != http.StatusTooManyRequests {
		t.Errorf("6th subscribe in 10 min: %d, want 429", r.status)
	}
}

func TestPageSubscriberChannelMustBeEmail(t *testing.T) {
	env := newTestEnv(t)
	ch := &store.Channel{Name: "Slack", Type: "slack", Config: store.ChannelConfig{URL: "https://hooks.slack.com/services/x"}}
	if err := env.store.CreateChannel(context.Background(), ch); err != nil {
		t.Fatal(err)
	}
	r := env.do(http.MethodPost, "/api/pages", map[string]any{
		"slug": "badch", "title": "B", "visibility": "public", "layout": "list",
		"subscriptionsEnabled": true, "subscriberChannelId": ch.ID,
	}, "admin")
	if r.status != http.StatusBadRequest || !bytesContains(r.body, "subscriberChannelId") {
		t.Fatalf("slack channel as mail relay: %d %s", r.status, r.body)
	}
}

// TestSubscribeRequiresJSON: a cross-site text/plain or form POST (which
// a browser sends without a CORS preflight) is refused.
func TestSubscribeRequiresJSON(t *testing.T) {
	env := newTestEnv(t)
	subscribePage(t, env, "ct", "public", true)
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded"} {
		r := env.doRaw(http.MethodPost, "/api/public/pages/ct/subscribe", ct, []byte(`{"email":"x@example.com"}`), "")
		if r.status != http.StatusUnsupportedMediaType {
			t.Errorf("%s: %d, want 415", ct, r.status)
		}
	}
	r := env.doRaw(http.MethodPost, "/api/public/subscriptions/unsubscribe", "text/plain", []byte(`{"token":"`+strings.Repeat("a", 64)+`"}`), "")
	if r.status != http.StatusUnsupportedMediaType {
		t.Errorf("unsubscribe text/plain: %d, want 415", r.status)
	}
}

func TestSLACSVDefusesFormulas(t *testing.T) {
	for in, want := range map[string]string{"=HYPERLINK(\"x\")": "'=HYPERLINK(\"x\")", "+1": "'+1", "-x": "'-x", "@sum": "'@sum", "API": "API", "": ""} {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}
