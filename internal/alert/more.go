package alert

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"opskest/internal/model"
	"opskest/internal/store"
)

// More channels, all plain HTTPS APIs. Like the senders in chat.go they share
// the plain-text and Markdown renderings, bound every field they send, and go
// through doRequest, which never follows a redirect and never puts the URL
// (which can carry a key) into an error. Each takes an optional URL that
// replaces the service's API origin, for self-hosted copies and for tests.
// None of them receives grouped messages: one message per monitor.

// base returns cfg.URL without a trailing slash, or def when it is empty.
func base(cfg store.ChannelConfig, def string) string {
	if b := strings.TrimRight(cfg.URL, "/"); b != "" {
		return b
	}
	return def
}

func basicAuth(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func formHeaders(extra map[string]string) map[string]string {
	h := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

// ── Zulip ─────────────────────────────────────────────────────────

type zulipSender struct{}

// Send posts to a stream topic. URL is the Zulip server, Username the bot's
// email, APIKey the bot's API key, Target the stream and Topic the topic.
func (zulipSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	topic := cfg.Topic
	if topic == "" {
		topic = "Opskest alerts"
	}
	form := url.Values{}
	form.Set("type", "stream")
	form.Set("to", cfg.Target)
	form.Set("topic", truncateRunes(topic, 60))
	form.Set("content", truncateRunes(markdownMessage(m), 9000))
	return doRequest(ctx, http.MethodPost, base(cfg, "")+"/api/v1/messages",
		formHeaders(map[string]string{"Authorization": basicAuth(cfg.Username, cfg.APIKey)}), []byte(form.Encode()))
}

// ── Webex ─────────────────────────────────────────────────────────

const webexAPI = "https://webexapis.com"

type webexSender struct{}

// Send posts a Markdown message to a room as a bot. APIKey is the bot's
// access token and Target the room id.
func (webexSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	return postJSON(ctx, base(cfg, webexAPI)+"/v1/messages",
		map[string]string{"Authorization": "Bearer " + cfg.APIKey},
		map[string]any{"roomId": cfg.Target, "markdown": truncateRunes(markdownMessage(m), 3500)})
}

// ── Pushbullet ────────────────────────────────────────────────────

const pushbulletAPI = "https://api.pushbullet.com"

type pushbulletSender struct{}

// Send pushes a note. APIKey is the access token; Target optionally names one
// device (its iden) or a contact (an email address), otherwise every device
// gets it.
func (pushbulletSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	payload := map[string]any{
		"type":  "note",
		"title": truncateRunes(Title(m), 250),
		"body":  truncateRunes(bodyLines(m), 1000),
	}
	if link := linkFor(m); link != "" {
		payload["type"], payload["url"] = "link", link
	}
	switch {
	case strings.Contains(cfg.Target, "@"):
		payload["email"] = cfg.Target
	case cfg.Target != "":
		payload["device_iden"] = cfg.Target
	}
	return postJSON(ctx, base(cfg, pushbulletAPI)+"/v2/pushes",
		map[string]string{"Access-Token": cfg.APIKey}, payload)
}

// ── IFTTT ─────────────────────────────────────────────────────────

const iftttAPI = "https://maker.ifttt.com"

type iftttSender struct{}

// Send triggers a Webhooks event. APIKey is the Webhooks key and Target the
// event name; value1 is the headline, value2 the details and value3 the link.
func (iftttSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	endpoint := base(cfg, iftttAPI) + "/trigger/" + url.PathEscape(cfg.Target) + "/with/key/" + url.PathEscape(cfg.APIKey)
	return postJSON(ctx, endpoint, nil, map[string]any{
		"value1": truncateRunes(Title(m), 250),
		"value2": truncateRunes(bodyLines(m), 1000),
		"value3": linkFor(m),
	})
}

// ── Apprise API ───────────────────────────────────────────────────

type appriseSender struct{}

// Send notifies through a stored configuration of an Apprise API server: URL
// is the server, Target the configuration key. Username and Password are
// optional basic authentication in front of the server.
func (appriseSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	kind := "info"
	switch {
	case m.Kind == model.EvDown || m.Kind == model.EvReminder:
		kind = "failure"
	case isProblem(m.Kind):
		kind = "warning"
	case m.Kind != model.EvTest:
		kind = "success"
	}
	headers := map[string]string{}
	if cfg.Username != "" {
		headers["Authorization"] = basicAuth(cfg.Username, cfg.Password)
	}
	body := truncateRunes(bodyLines(m), 3500)
	if link := linkFor(m); link != "" {
		body += "\n\n" + link
	}
	return postJSON(ctx, base(cfg, "")+"/notify/"+url.PathEscape(cfg.Target), headers, map[string]any{
		"title":  truncateRunes(Title(m), 250),
		"body":   strings.TrimSpace(body),
		"type":   kind,
		"format": "text",
	})
}

// ── Twilio SMS ────────────────────────────────────────────────────

const twilioAPI = "https://api.twilio.com"

// smsLimit keeps a text to a handful of segments.
const smsLimit = 480

type twilioSender struct{}

// Send texts every recipient in To from the number (or messaging service id)
// in From. Username is the account SID and Password the auth token. It tries
// every recipient and reports the first failure.
func (twilioSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	endpoint := base(cfg, twilioAPI) + "/2010-04-01/Accounts/" + url.PathEscape(cfg.Username) + "/Messages.json"
	text := truncateRunes(smsText(m), smsLimit)
	var first error
	for _, to := range cfg.To {
		form := url.Values{}
		form.Set("To", to)
		if strings.HasPrefix(cfg.From, "MG") {
			form.Set("MessagingServiceSid", cfg.From)
		} else {
			form.Set("From", cfg.From)
		}
		form.Set("Body", text)
		err := doRequest(ctx, http.MethodPost, endpoint,
			formHeaders(map[string]string{"Authorization": basicAuth(cfg.Username, cfg.Password)}), []byte(form.Encode()))
		if err != nil && first == nil {
			first = err
		}
	}
	return first
}

// smsText is the headline and the reason: a text message has no room for the rest.
func smsText(m Message) string {
	t := Title(m)
	if !m.Grouped && len(m.Items) == 1 {
		if m.Custom != "" {
			return t + "\n" + m.Custom
		}
		if r := ReasonLine(m.Items[0]); r != "" {
			t += "\n" + r
		}
	}
	return t
}

// ── Home Assistant ────────────────────────────────────────────────

type homeAssistantSender struct{}

// Send calls a notify service. URL is the Home Assistant address, APIKey a
// long-lived access token and Target the service name after "notify.", for
// example mobile_app_pixel or notify.
func (homeAssistantSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	payload := map[string]any{
		"title":   truncateRunes(Title(m), 250),
		"message": truncateRunes(bodyLines(m), 1000),
	}
	if link := linkFor(m); link != "" {
		payload["data"] = map[string]any{"url": link, "clickAction": link}
	}
	return postJSON(ctx, base(cfg, "")+"/api/services/notify/"+url.PathEscape(cfg.Target),
		map[string]string{"Authorization": "Bearer " + cfg.APIKey}, payload)
}

// ── Bark ──────────────────────────────────────────────────────────

const barkAPI = "https://api.day.app"

type barkSender struct{}

// Send pushes to an iOS device through a Bark server. APIKey is the device key.
func (barkSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	level := "active"
	if m.Kind == model.EvDown || m.Kind == model.EvReminder {
		level = "timeSensitive"
	}
	payload := map[string]any{
		"device_key": cfg.APIKey,
		"title":      truncateRunes(Title(m), 250),
		"body":       truncateRunes(bodyLines(m), 1000),
		"group":      "opskest",
		"level":      level,
	}
	if link := linkFor(m); link != "" {
		payload["url"] = link
	}
	return postJSON(ctx, base(cfg, barkAPI)+"/push", nil, payload)
}

// ── Signal (signal-cli REST API) ──────────────────────────────────

type signalSender struct{}

// Send messages recipients through a signal-cli-rest-api server. URL is the
// server, From the registered sender number and To the recipients. Username
// and Password are optional basic authentication in front of the server.
func (signalSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	headers := map[string]string{}
	if cfg.Username != "" {
		headers["Authorization"] = basicAuth(cfg.Username, cfg.Password)
	}
	text := truncateRunes(plainMessage(m), 3000)
	return postJSON(ctx, base(cfg, "")+"/v2/send", headers, map[string]any{
		"message":    text,
		"number":     cfg.From,
		"recipients": cfg.To,
	})
}

// ── validation ────────────────────────────────────────────────────

var (
	tokenRe   = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,200}$`)
	serviceRe = regexp.MustCompile(`^[a-z0-9_]{1,100}$`)
	phoneRe   = regexp.MustCompile(`^\+[0-9]{6,15}$`)
	sidRe     = regexp.MustCompile(`^[A-Za-z0-9]{10,64}$`)
)

// optionalURL accepts an empty URL, or an http(s) one that replaces the
// service's API origin.
func optionalURL(fe model.FieldErrors, raw string) {
	if raw != "" {
		requireURL(fe, raw, false)
	}
}

// validateMore checks the channel types defined in this file. It reports
// whether t is one of them.
func validateMore(t string, cfg store.ChannelConfig, fe model.FieldErrors) bool {
	switch t {
	case "zulip":
		requireURL(fe, cfg.URL, false)
		if cfg.Username == "" {
			fe["username"] = "required (the bot's email)"
		}
		if cfg.APIKey == "" {
			fe["apiKey"] = "required (the bot's API key)"
		}
		if cfg.Target == "" {
			fe["target"] = "required (the stream name)"
		}
		if len([]rune(cfg.Topic)) > 60 {
			fe["topic"] = "at most 60 characters"
		}

	case "webex":
		optionalURL(fe, cfg.URL)
		if cfg.APIKey == "" {
			fe["apiKey"] = "required (the bot's access token)"
		}
		if cfg.Target == "" {
			fe["target"] = "required (the room id)"
		}

	case "pushbullet":
		optionalURL(fe, cfg.URL)
		if cfg.APIKey == "" {
			fe["apiKey"] = "required (the access token)"
		}

	case "ifttt":
		optionalURL(fe, cfg.URL)
		if cfg.APIKey == "" {
			fe["apiKey"] = "required (the Webhooks key)"
		}
		if !tokenRe.MatchString(cfg.Target) {
			fe["target"] = "required: the event name (letters, digits, - _ . ~)"
		}

	case "apprise":
		requireURL(fe, cfg.URL, false)
		if !tokenRe.MatchString(cfg.Target) {
			fe["target"] = "required: the configuration key (letters, digits, - _ . ~)"
		}

	case "twilio":
		optionalURL(fe, cfg.URL)
		if !sidRe.MatchString(cfg.Username) {
			fe["username"] = "required (the account SID)"
		}
		if cfg.Password == "" {
			fe["password"] = "required (the auth token)"
		}
		if !phoneRe.MatchString(cfg.From) && !strings.HasPrefix(cfg.From, "MG") {
			fe["from"] = "required: a number like +15551234567, or a messaging service id (MG…)"
		}
		if len(cfg.To) == 0 || len(cfg.To) > 10 {
			fe["to"] = "1 to 10 numbers like +15551234567"
		}
		for _, n := range cfg.To {
			if !phoneRe.MatchString(n) {
				fe["to"] = "numbers must look like +15551234567"
			}
		}

	case "homeassistant":
		requireURL(fe, cfg.URL, false)
		if cfg.APIKey == "" {
			fe["apiKey"] = "required (a long-lived access token)"
		}
		if !serviceRe.MatchString(cfg.Target) {
			fe["target"] = "required: the notify service name, e.g. mobile_app_pixel (lowercase letters, digits, _)"
		}

	case "bark":
		optionalURL(fe, cfg.URL)
		if cfg.APIKey == "" {
			fe["apiKey"] = "required (the device key)"
		}

	case "signal":
		requireURL(fe, cfg.URL, false)
		if !phoneRe.MatchString(cfg.From) {
			fe["from"] = "required: the registered sender number, like +15551234567"
		}
		if len(cfg.To) == 0 || len(cfg.To) > 10 {
			fe["to"] = "1 to 10 recipients like +15551234567"
		}
		for _, n := range cfg.To {
			if !phoneRe.MatchString(n) {
				fe["to"] = "recipients must look like +15551234567"
			}
		}

	default:
		return false
	}
	return true
}
