package alert

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

// A destination that answers with a redirect must not receive the request
// again elsewhere: net/http keeps custom headers (X-Gotify-Key) on a
// cross-host hop and replays a 307 POST body (Pushover's token).
func TestDeliveryNeverFollowsRedirects(t *testing.T) {
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
	}))
	defer other.Close()
	// "localhost" vs 127.0.0.1: a different host, so only Authorization-style
	// headers would be stripped by net/http.
	target := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+"/message", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	s, _ := SenderFor("gotify")
	err := s.Send(context.Background(), store.ChannelConfig{URL: redirector.URL, APIKey: "app-token"}, chatDownMessage())
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("a redirect must be reported as a failed delivery, got %v", err)
	}
	if leaked.Load() != 0 {
		t.Fatal("the redirect target received the request (with the app token header)")
	}

	d := NewDispatcher(nil, nil, nil, Options{})
	if d.opts.HTTP == http.DefaultClient {
		t.Fatal("the dispatcher default must not be http.DefaultClient (it follows redirects)")
	}
}

// A storm lists every monitor; the post must stay under the smallest limit
// among the markdown/plain services (Rocket.Chat 5000 characters).
func TestChatMessagesStayUnderPostLimits(t *testing.T) {
	var items []Item
	for i := 0; i < 400; i++ {
		items = append(items, Item{MonitorID: int64(i), Name: fmt.Sprintf("service-%03d", i), Group: "prod", Reason: "connection refused", Since: time.Unix(1_700_000_000, 0)})
	}
	m := Message{Kind: model.EvDown, Items: items, Grouped: true, At: time.Unix(1_700_000_000, 0), AppURL: "https://opskest.example.com"}
	for name, text := range map[string]string{"markdown": markdownMessage(m), "plain": plainMessage(m)} {
		if n := len([]rune(text)); n > 4000 {
			t.Errorf("%s message is %d characters", name, n)
		}
		if !strings.Contains(text, "https://opskest.example.com") {
			t.Errorf("%s message lost its link", name)
		}
	}
}

func TestTelegramEscapesTheTokenInThePath(t *testing.T) {
	var escaped, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped, query = r.URL.EscapedPath(), r.URL.RawQuery
	}))
	defer srv.Close()
	s, _ := SenderFor("telegram")
	if err := s.Send(context.Background(), store.ChannelConfig{URL: srv.URL, APIKey: "123:abc/../../x?y#z", Target: "1"}, chatDownMessage()); err != nil {
		t.Fatal(err)
	}
	if query != "" || !strings.HasSuffix(escaped, "/sendMessage") || strings.Count(escaped, "/") != 2 {
		t.Fatalf("a token with / ? # must stay inside one path segment: path=%q query=%q", escaped, query)
	}
}

func TestPushoverURLIsValidated(t *testing.T) {
	cfg := store.ChannelConfig{APIKey: "t", Target: "u", URL: "ftp://x"}
	if fe := ValidateConfig("pushover", cfg); fe["url"] == "" {
		t.Fatal("a custom Pushover origin must be http(s)")
	}
	cfg.URL = ""
	if fe := ValidateConfig("pushover", cfg); len(fe) != 0 {
		t.Fatalf("the default origin needs no url: %v", fe)
	}
}
