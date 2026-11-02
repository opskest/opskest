package alert

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opskest/internal/notify"
	"opskest/internal/store"
)

// smtpCapture is a one-session SMTP server that records the envelope
// recipients and the DATA payload (dot-unstuffed, CRLF kept).
func smtpCapture(t *testing.T) (store.ChannelConfig, chan []string, chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	rcpts, data := make(chan []string, 4), make(chan string, 4)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				r := bufio.NewReader(conn)
				say := func(s string) { fmt.Fprintf(conn, "%s\r\n", s) }
				say("220 test")
				var to []string
				var body strings.Builder
				inData := false
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if inData {
						if line == ".\r\n" {
							inData = false
							rcpts <- to
							data <- body.String()
							say("250 OK")
							continue
						}
						body.WriteString(strings.TrimPrefix(line, "."))
						continue
					}
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(cmd, "EHLO"):
						say("250-test")
						say("250 OK")
					case strings.HasPrefix(cmd, "RCPT TO:"):
						to = append(to, strings.Trim(strings.TrimSpace(line)[8:], "<>"))
						say("250 OK")
					case cmd == "DATA":
						inData = true
						say("354 go")
					case cmd == "QUIT":
						say("221 bye")
						return
					default:
						say("250 OK")
					}
				}
			}(conn)
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return store.ChannelConfig{Host: host, Port: p, From: "status@acme.example", To: []string{"ops@acme.example"}, TLSMode: "none"}, rcpts, data
}

// SendMail delivers to the recipients it's given, not the channel's To list
// (subscriber mail must never go to the ops team's alert address).
func TestSendMailUsesGivenRecipients(t *testing.T) {
	cfg, rcpts, data := smtpCapture(t)
	msg := []byte("From: status@acme.example\r\nTo: a@b.example\r\nSubject: hi\r\n\r\nbody\r\n")
	if err := SendMail(context.Background(), cfg, []string{"a@b.example"}, msg); err != nil {
		t.Fatal(err)
	}
	if got := <-rcpts; len(got) != 1 || got[0] != "a@b.example" {
		t.Fatalf("RCPT = %v", got)
	}
	if !strings.Contains(<-data, "Subject: hi") {
		t.Fatal("payload not delivered")
	}
}

type subStore struct {
	page *store.Page
	ch   *store.Channel
	subs []store.Subscriber
}

func (s subStore) GetPage(context.Context, int64) (*store.Page, error)       { return s.page, nil }
func (s subStore) GetChannel(context.Context, int64) (*store.Channel, error) { return s.ch, nil }
func (s subStore) ConfirmedSubscribers(context.Context, int64) ([]store.Subscriber, error) {
	return s.subs, nil
}
func (s subStore) DeleteSubscriber(context.Context, int64, int64) error { return nil }

type realMailer struct{}

func (realMailer) SendMail(ctx context.Context, cfg store.ChannelConfig, rcpts []string, msg []byte) error {
	return SendMail(ctx, cfg, rcpts, msg)
}

type realPoster struct{}

func (realPoster) PostJSON(ctx context.Context, url string, p any) error {
	return PostJSON(ctx, url, p)
}

// The whole subscriber email path over real SMTP: one message per
// subscriber, to that subscriber only, a valid two-part MIME message whose
// decoded text carries the update and the unsubscribe link.
func TestSubscriberMailEndToEnd(t *testing.T) {
	cfg, rcpts, data := smtpCapture(t)
	chID := int64(9)
	st := subStore{
		page: &store.Page{ID: 1, Slug: "acme", Title: "Acme ✓", Visibility: "public", SubscriptionsEnabled: true, SubscriberChannelID: &chID},
		ch:   &store.Channel{ID: chID, Type: "email", Config: cfg},
		subs: []store.Subscriber{{ID: 5, PageID: 1, Kind: "email", Email: "carol@x.example", Token: strings.Repeat("ab", 32)}},
	}
	n := notify.New(st, realMailer{}, realPoster{}, "https://status.acme.example", slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.Start(ctx)
	n.IncidentUpdate([]int64{1}, notify.Incident{ID: 42, Title: "Checkout errors", Status: "identified",
		Body: "Root cause found.\n.\nA line that is only a dot, and a long one: " + strings.Repeat("é", 600), At: time.Now()})
	n.Wait()

	if got := <-rcpts; len(got) != 1 || got[0] != "carol@x.example" {
		t.Fatalf("RCPT = %v", got)
	}
	m, err := mail.ReadMessage(strings.NewReader(<-data))
	if err != nil {
		t.Fatal(err)
	}
	subj, _ := new(mime.WordDecoder).DecodeHeader(m.Header.Get("Subject"))
	if subj != "[Acme ✓] Identified: Checkout errors" {
		t.Errorf("subject = %q", subj)
	}
	if !strings.HasPrefix(m.Header.Get("List-Unsubscribe"), "<https://status.acme.example/status/acme/unsubscribe?token=") {
		t.Errorf("List-Unsubscribe = %q", m.Header.Get("List-Unsubscribe"))
	}
	_, params, _ := mime.ParseMediaType(m.Header.Get("Content-Type"))
	mr := multipart.NewReader(m.Body, params["boundary"])
	part, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(part)
	text := strings.ReplaceAll(string(raw), "\r\n", "\n") // mail is CRLF on the wire
	if !strings.Contains(text, "Root cause found.\n.\nA line") || !strings.Contains(text, strings.Repeat("é", 600)) ||
		!strings.Contains(text, "https://status.acme.example/status/acme/incidents/42") {
		t.Errorf("decoded text = %.300q", text)
	}
	if _, err := mr.NextPart(); err != nil {
		t.Errorf("no html part: %v", err)
	}
}

// PostJSON never follows a redirect (a subscriber's webhook must not be
// able to bounce the request elsewhere) and reports the status code.
func TestPostJSONNoRedirectAndStatus(t *testing.T) {
	var hit atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Store(true) }))
	defer elsewhere.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	gone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte("no_service"))
	}))
	defer gone.Close()

	err := PostJSON(context.Background(), redirect.URL, map[string]string{"text": "x"})
	if err == nil || !strings.HasPrefix(err.Error(), "http 307") || hit.Load() {
		t.Fatalf("redirect: err=%v followed=%v", err, hit.Load())
	}
	if err := PostJSON(context.Background(), gone.URL, map[string]string{"text": "x"}); err == nil || !strings.HasPrefix(err.Error(), "http 410") {
		t.Fatalf("410: err=%v (notify drops dead webhooks by this prefix)", err)
	}
	if err := PostJSON(context.Background(), redirect.URL+"/never", nil); err != nil && strings.Contains(err.Error(), redirect.URL) {
		t.Fatalf("error echoes the URL: %v", err)
	}
}
