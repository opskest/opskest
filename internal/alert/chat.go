package alert

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

// The senders in this file cover the common self-hostable and consumer
// chat/push services. They share one plain-text rendering (Title, body,
// link) so an alert reads the same everywhere, and none needs a dependency.

// plainMessage is the headline, the descriptive body and the link, as one
// plain-text block.
func plainMessage(m Message) string {
	var b strings.Builder
	b.WriteString(Title(m))
	if body := chatBody(m); body != "" {
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	if link := linkFor(m); link != "" {
		b.WriteString("\n\n")
		b.WriteString(link)
	}
	return b.String()
}

// markdownMessage is plainMessage with a bold title and a link label, for
// services that render Markdown (Mattermost, Rocket.Chat, Discord).
func markdownMessage(m Message) string {
	var b strings.Builder
	b.WriteString("**" + Title(m) + "**")
	if body := chatBody(m); body != "" {
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	if link := linkFor(m); link != "" {
		b.WriteString("\n\n[Open in opskest](" + link + ")")
	}
	return b.String()
}

// chatBodyLimit keeps a grouped (storm) message under the smallest post limit
// of the services that take plainMessage / markdownMessage: Rocket.Chat's
// default is 5000 characters, Telegram's 4096. A storm of a few hundred
// monitors is otherwise rejected whole, and the alert is lost.
const chatBodyLimit = 3500

func chatBody(m Message) string { return truncateRunes(bodyLines(m), chatBodyLimit) }

// truncateRunes shortens s to at most n runes, ending with an ellipsis.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// isProblem reports whether m announces something that needs attention
// (as opposed to a recovery or an all-clear).
func isProblem(kind model.EventKind) bool {
	switch kind {
	case model.EvRecovered, model.EvDegradedCleared, model.EvRegionalCleared, model.EvLatencyCleared, model.EvTest:
		return false
	}
	return true
}

// ── Discord ───────────────────────────────────────────────────────

type discordSender struct{}

func (discordSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	color := 0x059669
	if m.Kind == model.EvDown || m.Kind == model.EvReminder {
		color = 0xdc2626
	} else if isProblem(m.Kind) {
		color = 0xb45309
	}
	embed := map[string]any{
		"title":       truncateRunes(Title(m), 256),
		"description": truncateRunes(bodyLines(m), 4000),
		"color":       color,
	}
	if link := linkFor(m); link != "" {
		embed["url"] = link
	}
	return postJSON(ctx, cfg.URL, nil, map[string]any{
		"username":         "Opskest",
		"allowed_mentions": map[string]any{"parse": []string{}},
		"embeds":           []any{embed},
	})
}

// ── Mattermost and Rocket.Chat (Slack-style incoming webhooks) ────

type markdownWebhookSender struct{}

func (markdownWebhookSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	return postJSON(ctx, cfg.URL, nil, map[string]any{
		"username": "Opskest",
		"text":     markdownMessage(m),
	})
}

// ── Telegram ──────────────────────────────────────────────────────

const telegramAPI = "https://api.telegram.org"

type telegramSender struct{}

// Send posts through the Bot API. APIKey is the bot token and Target the
// chat id; URL optionally replaces the API origin (a self-hosted Bot API
// server).
func (telegramSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	base := strings.TrimRight(cfg.URL, "/")
	if base == "" {
		base = telegramAPI
	}
	return postJSON(ctx, base+"/bot"+url.PathEscape(cfg.APIKey)+"/sendMessage", nil, map[string]any{
		"chat_id":                  cfg.Target,
		"text":                     truncateRunes(plainMessage(m), 4000),
		"disable_web_page_preview": true,
	})
}

// ── ntfy ──────────────────────────────────────────────────────────

type ntfySender struct{}

// Send publishes as JSON to the server root so the emoji in the title
// survive; cfg.URL is the topic URL (https://ntfy.sh/my-topic).
func (ntfySender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("alert: invalid ntfy topic url")
	}
	topic := strings.Trim(u.Path, "/")
	u.Path, u.RawQuery = "/", ""

	priority, tag := 3, "white_check_mark"
	if m.Kind == model.EvDown || m.Kind == model.EvReminder {
		priority, tag = 5, "rotating_light"
	} else if isProblem(m.Kind) {
		priority, tag = 4, "warning"
	}
	payload := map[string]any{
		"topic":    topic,
		"title":    Title(m),
		"message":  truncateRunes(bodyLines(m), 3500),
		"priority": priority,
		"tags":     []string{tag},
	}
	if link := linkFor(m); link != "" {
		payload["click"] = link
	}
	headers := map[string]string{}
	switch {
	case cfg.APIKey != "":
		headers["Authorization"] = "Bearer " + cfg.APIKey
	case cfg.Username != "":
		headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.Username+":"+cfg.Password))
	}
	return postJSON(ctx, u.String(), headers, payload)
}

// ── Gotify ────────────────────────────────────────────────────────

type gotifySender struct{}

// Send posts to a Gotify server: cfg.URL is the server, APIKey the
// application token (sent as a header so it never lands in a URL).
func (gotifySender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	priority := 5
	if m.Kind == model.EvDown || m.Kind == model.EvReminder {
		priority = 8
	}
	extras := map[string]any{"client::display": map[string]any{"contentType": "text/plain"}}
	if link := linkFor(m); link != "" {
		extras["client::notification"] = map[string]any{"click": map[string]any{"url": link}}
	}
	return postJSON(ctx, strings.TrimRight(cfg.URL, "/")+"/message",
		map[string]string{"X-Gotify-Key": cfg.APIKey},
		map[string]any{
			"title":    Title(m),
			"message":  truncateRunes(bodyLines(m), 3500),
			"priority": priority,
			"extras":   extras,
		})
}

// ── Pushover ──────────────────────────────────────────────────────

const pushoverAPI = "https://api.pushover.net"

type pushoverSender struct{}

// Send posts to Pushover: APIKey is the application token, Target the user
// or group key. URL optionally replaces the API origin.
func (pushoverSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	base := strings.TrimRight(cfg.URL, "/")
	if base == "" {
		base = pushoverAPI
	}
	form := url.Values{}
	form.Set("token", cfg.APIKey)
	form.Set("user", cfg.Target)
	form.Set("title", truncateRunes(Title(m), 250))
	form.Set("message", truncateRunes(bodyLines(m), 1000))
	if link := linkFor(m); link != "" {
		form.Set("url", link)
		form.Set("url_title", "Open in opskest")
	}
	if m.Kind == model.EvDown || m.Kind == model.EvReminder {
		form.Set("priority", "1")
	}
	return doRequest(ctx, http.MethodPost, base+"/1/messages.json",
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, []byte(form.Encode()))
}

// ── Matrix ────────────────────────────────────────────────────────

type matrixSender struct{}

// Send posts an m.text message: cfg.URL is the homeserver, APIKey a user
// access token and Target the room id (!room:server).
func (matrixSender) Send(ctx context.Context, cfg store.ChannelConfig, m Message) error {
	txn := strconv.FormatInt(time.Now().UnixNano(), 36)
	endpoint := strings.TrimRight(cfg.URL, "/") + "/_matrix/client/v3/rooms/" + url.PathEscape(cfg.Target) + "/send/m.room.message/opskest-" + txn
	body, err := json.Marshal(map[string]any{"msgtype": "m.text", "body": plainMessage(m)})
	if err != nil {
		return fmt.Errorf("alert: marshal matrix payload: %w", err)
	}
	return doRequest(ctx, http.MethodPut, endpoint, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + cfg.APIKey,
	}, body)
}
