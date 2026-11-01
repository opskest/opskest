package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

type chatCaptured struct {
	method, path, rawQuery string
	header                 http.Header
	body                   string
}

func chatCapture(t *testing.T) (*httptest.Server, *chatCaptured) {
	t.Helper()
	got := &chatCaptured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.rawQuery, got.header, got.body = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func chatDownMessage() Message {
	it := Item{MonitorID: 7, Name: "API", Target: "https://api.example.com", Reason: "timeout", URL: "https://opskest.example.com/monitors/7", Since: time.Unix(1_700_000_000, 0)}
	return Message{Kind: model.EvDown, Items: []Item{it}, At: it.Since, AppURL: "https://opskest.example.com"}
}

func TestChatSenders(t *testing.T) {
	m := chatDownMessage()
	cases := []struct {
		name   string
		typ    string
		cfg    func(base string) store.ChannelConfig
		check  func(t *testing.T, c *chatCaptured)
		secret string // must never appear in the request URL
	}{
		{
			name: "discord", typ: "discord",
			cfg: func(b string) store.ChannelConfig { return store.ChannelConfig{URL: b + "/hook"} },
			check: func(t *testing.T, c *chatCaptured) {
				var p struct {
					Embeds []struct {
						Title       string `json:"title"`
						Description string `json:"description"`
						Color       int    `json:"color"`
					} `json:"embeds"`
				}
				chatJSON(t, c.body, &p)
				if len(p.Embeds) != 1 || !strings.Contains(p.Embeds[0].Title, "Down") || p.Embeds[0].Color != 0xdc2626 || !strings.Contains(p.Embeds[0].Description, "timeout") {
					t.Fatalf("embed: %+v", p)
				}
			},
		},
		{
			name: "mattermost", typ: "mattermost",
			cfg: func(b string) store.ChannelConfig { return store.ChannelConfig{URL: b + "/hooks/x"} },
			check: func(t *testing.T, c *chatCaptured) {
				if !strings.Contains(c.body, "Open in opskest") || !strings.Contains(c.body, "Down") {
					t.Fatalf("body: %s", c.body)
				}
			},
		},
		{
			name: "telegram", typ: "telegram", secret: "BOT-TOKEN",
			cfg: func(b string) store.ChannelConfig {
				return store.ChannelConfig{URL: b, APIKey: "BOT-TOKEN", Target: "-1001"}
			},
			check: func(t *testing.T, c *chatCaptured) {
				if c.path != "/botBOT-TOKEN/sendMessage" {
					t.Fatalf("path %q", c.path)
				}
				var p map[string]any
				chatJSON(t, c.body, &p)
				if p["chat_id"] != "-1001" || !strings.Contains(p["text"].(string), "timeout") {
					t.Fatalf("payload %v", p)
				}
			},
		},
		{
			name: "ntfy", typ: "ntfy",
			cfg: func(b string) store.ChannelConfig { return store.ChannelConfig{URL: b + "/ops-alerts", APIKey: "tk_1"} },
			check: func(t *testing.T, c *chatCaptured) {
				if c.path != "/" || c.header.Get("Authorization") != "Bearer tk_1" {
					t.Fatalf("path %q auth %q", c.path, c.header.Get("Authorization"))
				}
				var p map[string]any
				chatJSON(t, c.body, &p)
				if p["topic"] != "ops-alerts" || p["priority"].(float64) != 5 || p["click"] != "https://opskest.example.com/monitors/7" {
					t.Fatalf("payload %v", p)
				}
			},
		},
		{
			name: "gotify", typ: "gotify", secret: "APPTOKEN",
			cfg: func(b string) store.ChannelConfig { return store.ChannelConfig{URL: b + "/", APIKey: "APPTOKEN"} },
			check: func(t *testing.T, c *chatCaptured) {
				if c.path != "/message" || c.header.Get("X-Gotify-Key") != "APPTOKEN" {
					t.Fatalf("path %q key %q", c.path, c.header.Get("X-Gotify-Key"))
				}
			},
		},
		{
			name: "pushover", typ: "pushover",
			cfg: func(b string) store.ChannelConfig { return store.ChannelConfig{URL: b, APIKey: "app", Target: "user1"} },
			check: func(t *testing.T, c *chatCaptured) {
				v, err := url.ParseQuery(c.body)
				if err != nil || c.path != "/1/messages.json" {
					t.Fatalf("path %q err %v", c.path, err)
				}
				if v.Get("token") != "app" || v.Get("user") != "user1" || v.Get("priority") != "1" || v.Get("url") == "" {
					t.Fatalf("form %v", v)
				}
			},
		},
		{
			name: "matrix", typ: "matrix", secret: "syt_token",
			cfg: func(b string) store.ChannelConfig {
				return store.ChannelConfig{URL: b, APIKey: "syt_token", Target: "!room:example.org"}
			},
			check: func(t *testing.T, c *chatCaptured) {
				if c.method != http.MethodPut || !strings.HasPrefix(c.path, "/_matrix/client/v3/rooms/!room:example.org/send/m.room.message/opskest-") {
					t.Fatalf("%s %q", c.method, c.path)
				}
				if c.header.Get("Authorization") != "Bearer syt_token" {
					t.Fatalf("auth %q", c.header.Get("Authorization"))
				}
				var p map[string]any
				chatJSON(t, c.body, &p)
				if p["msgtype"] != "m.text" {
					t.Fatalf("payload %v", p)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := chatCapture(t)
			s, ok := SenderFor(tc.typ)
			if !ok {
				t.Fatalf("no sender for %s", tc.typ)
			}
			if err := s.Send(context.Background(), tc.cfg(srv.URL), m); err != nil {
				t.Fatalf("send: %v", err)
			}
			tc.check(t, got)
			if tc.secret != "" && strings.Contains(got.rawQuery, tc.secret) {
				t.Fatalf("secret leaked into the query string: %q", got.rawQuery)
			}
		})
	}
}

func TestChatSenderErrorsDoNotLeakSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()
	err := telegramSender{}.Send(context.Background(), store.ChannelConfig{URL: srv.URL, APIKey: "SECRET-TOKEN", Target: "1"}, chatDownMessage())
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateChatChannels(t *testing.T) {
	ok := map[string]store.ChannelConfig{
		"discord":    {URL: "https://discord.com/api/webhooks/1/abc"},
		"mattermost": {URL: "http://mm.internal/hooks/x"},
		"rocketchat": {URL: "https://chat.example.com/hooks/x"},
		"telegram":   {APIKey: "123:abc", Target: "-100"},
		"ntfy":       {URL: "https://ntfy.sh/ops"},
		"gotify":     {URL: "http://gotify.lan", APIKey: "t"},
		"pushover":   {APIKey: "a", Target: "u"},
		"matrix":     {URL: "https://matrix.example.org", APIKey: "t", Target: "!r:example.org"},
	}
	for typ, cfg := range ok {
		if fe := ValidateConfig(typ, cfg); len(fe) != 0 {
			t.Errorf("%s: %v", typ, fe)
		}
		if fe := ValidateConfig(typ, store.ChannelConfig{}); len(fe) == 0 {
			t.Errorf("%s: empty config should be rejected", typ)
		}
	}
	if fe := ValidateConfig("discord", store.ChannelConfig{URL: "http://discord.com/x"}); fe["url"] == "" {
		t.Error("discord must be https")
	}
	if fe := ValidateConfig("ntfy", store.ChannelConfig{URL: "https://ntfy.sh/"}); fe["url"] == "" {
		t.Error("ntfy needs a topic")
	}
}

func chatJSON(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("json %q: %v", s, err)
	}
}
