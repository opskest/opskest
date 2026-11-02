// Package notify delivers status page subscriber mail and Slack posts:
// the double opt-in confirmation, and every public incident update on a
// page to its confirmed subscribers. One worker drains a bounded queue so
// a burst of updates never blocks an API request; each delivery retries
// twice. Addresses, webhook URLs and message bodies are never logged.
package notify

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net/url"
	"strings"
	"sync"
	"time"

	"opskest/internal/logx"
	"opskest/internal/store"
)

const (
	queueSize   = 1000
	sendTimeout = 15 * time.Second
	attempts    = 3
	parallel    = 8 // deliveries in flight per notification
)

// Mailer sends one rendered message through an email channel's SMTP server.
type Mailer interface {
	SendMail(ctx context.Context, cfg store.ChannelConfig, rcpts []string, msg []byte) error
}

// Poster POSTs a JSON payload (Slack incoming webhooks).
type Poster interface {
	PostJSON(ctx context.Context, endpoint string, payload any) error
}

// Store is what the notifier reads.
type Store interface {
	GetPage(ctx context.Context, id int64) (*store.Page, error)
	GetChannel(ctx context.Context, id int64) (*store.Channel, error)
	ConfirmedSubscribers(ctx context.Context, pageID int64) ([]store.Subscriber, error)
	DeleteSubscriber(ctx context.Context, pageID, id int64) error
}

// Incident is the public content of one incident update.
type Incident struct {
	ID     int64
	Title  string
	Status string // investigating, identified, monitoring, resolved
	Impact string
	Body   string // the update's text (public updates only)
	At     time.Time
}

type job struct {
	confirm  *store.Subscriber
	page     *store.Page
	pageID   int64
	incident *Incident
}

// Notifier queues and delivers subscriber notifications.
type Notifier struct {
	st     Store
	mail   Mailer
	post   Poster
	appURL string
	log    *slog.Logger
	jobs   chan job
	wg     sync.WaitGroup
	retry  time.Duration
}

func New(st Store, mail Mailer, post Poster, appURL string, log *slog.Logger) *Notifier {
	return &Notifier{st: st, mail: mail, post: post, appURL: strings.TrimRight(appURL, "/"), log: log,
		jobs: make(chan job, queueSize), retry: 2 * time.Second}
}

// Start runs the worker until ctx ends.
func (n *Notifier) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case j := <-n.jobs:
				n.run(ctx, j)
				n.wg.Done()
			}
		}
	}()
}

// Wait blocks until every queued job has been handled (tests).
func (n *Notifier) Wait() { n.wg.Wait() }

func (n *Notifier) enqueue(j job) bool {
	n.wg.Add(1)
	select {
	case n.jobs <- j:
		return true
	default:
		n.wg.Done()
		n.log.Warn("subscriber notification queue full; dropped one job")
		return false
	}
}

// SendConfirmation queues the opt-in email for a pending subscriber.
func (n *Notifier) SendConfirmation(page *store.Page, sub store.Subscriber) bool {
	return n.enqueue(job{confirm: &sub, page: page})
}

// IncidentUpdate queues a notification of inc to every subscriber of
// each page in pageIDs (pages with subscriptions off are skipped).
func (n *Notifier) IncidentUpdate(pageIDs []int64, inc Incident) {
	for _, id := range pageIDs {
		c := inc
		n.enqueue(job{pageID: id, incident: &c})
	}
}

func (n *Notifier) run(ctx context.Context, j job) {
	if j.confirm != nil {
		n.sendConfirmation(ctx, j.page, *j.confirm)
		return
	}
	page, err := n.st.GetPage(ctx, j.pageID)
	if err != nil || !page.SubscriptionsEnabled || page.Visibility != "public" {
		return
	}
	subs, err := n.st.ConfirmedSubscribers(ctx, page.ID)
	if err != nil {
		logx.Err(n.log, "subscriber list", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	var smtp *store.ChannelConfig
	if page.SubscriberChannelID != nil {
		if ch, err := n.st.GetChannel(ctx, *page.SubscriberChannelID); err == nil && ch.Type == "email" {
			smtp = &ch.Config
		}
	}
	// Deliver with bounded parallelism, so a few slow or dead endpoints
	// can't hold the queue (and the confirmations behind it) for long.
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, sub := range subs {
		if sub.Kind == "email" && smtp == nil {
			continue
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(sub store.Subscriber) {
			defer func() { <-sem; wg.Done() }()
			switch sub.Kind {
			case "email":
				msg := n.incidentEmail(*smtp, page, sub, *j.incident)
				n.try(ctx, "subscriber email", func(c context.Context) error {
					return n.mail.SendMail(c, *smtp, []string{sub.Email}, msg)
				})
			case "slack":
				payload := n.incidentSlack(page, sub, *j.incident)
				err := n.try(ctx, "subscriber slack", func(c context.Context) error {
					return n.post.PostJSON(c, sub.SlackURL, payload)
				})
				// Slack answers 404/410 for a deleted webhook: it will never
				// work again, so stop sending to it.
				if err != nil && (strings.HasPrefix(err.Error(), "http 404") || strings.HasPrefix(err.Error(), "http 410")) {
					if derr := n.st.DeleteSubscriber(ctx, page.ID, sub.ID); derr != nil {
						logx.Err(n.log, "remove dead slack subscriber", derr)
					}
				}
			}
		}(sub)
	}
	wg.Wait()
}

func (n *Notifier) sendConfirmation(ctx context.Context, page *store.Page, sub store.Subscriber) {
	if page.SubscriberChannelID == nil {
		return
	}
	ch, err := n.st.GetChannel(ctx, *page.SubscriberChannelID)
	if err != nil || ch.Type != "email" {
		return
	}
	confirm := n.appURL + "/status/" + page.Slug + "/subscribe/confirm?token=" + url.QueryEscape(sub.Token)
	subject := fmt.Sprintf("Confirm your subscription to %s status updates", page.Title)
	text := fmt.Sprintf("Someone (hopefully you) asked to get incident updates for %s by email.\n\n"+
		"Confirm here: %s\n\nIf it wasn't you, ignore this email — nothing happens unless the link is opened within 48 hours.\n",
		page.Title, confirm)
	html := fmt.Sprintf(`<p>Someone (hopefully you) asked to get incident updates for <strong>%s</strong> by email.</p>`+
		`<p><a href="%s">Confirm your subscription</a></p><p style="color:#666">If it wasn't you, ignore this email — nothing happens unless the link is opened within 48 hours.</p>`,
		htmlEscape(page.Title), htmlEscape(confirm))
	msg := buildMail(ch.Config.From, sub.Email, subject, text, html, "")
	n.try(ctx, "subscriber confirmation", func(c context.Context) error {
		return n.mail.SendMail(c, ch.Config, []string{sub.Email}, msg)
	})
}

func (n *Notifier) unsubscribeURL(page *store.Page, sub store.Subscriber) string {
	return n.appURL + "/status/" + page.Slug + "/unsubscribe?token=" + url.QueryEscape(sub.Token)
}

func (n *Notifier) incidentURL(page *store.Page, inc Incident) string {
	return fmt.Sprintf("%s/status/%s/incidents/%d", n.appURL, page.Slug, inc.ID)
}

var statusWord = map[string]string{"investigating": "Investigating", "identified": "Identified", "monitoring": "Monitoring", "resolved": "Resolved"}

func label(status string) string {
	if w, ok := statusWord[status]; ok {
		return w
	}
	return status
}

func (n *Notifier) incidentEmail(cfg store.ChannelConfig, page *store.Page, sub store.Subscriber, inc Incident) []byte {
	link, unsub := n.incidentURL(page, inc), n.unsubscribeURL(page, sub)
	subject := fmt.Sprintf("[%s] %s: %s", page.Title, label(inc.Status), inc.Title)
	text := fmt.Sprintf("%s — %s\n%s\n\n%s\n\nFull timeline: %s\n\n—\nYou're getting this because you subscribed to %s status updates.\nUnsubscribe: %s\n",
		label(inc.Status), inc.Title, inc.At.UTC().Format("2006-01-02 15:04 UTC"), inc.Body, link, page.Title, unsub)
	html := fmt.Sprintf(`<p><strong>%s</strong> — %s<br><span style="color:#666">%s</span></p><p style="white-space:pre-wrap">%s</p>`+
		`<p><a href="%s">Full timeline</a></p><hr><p style="color:#666;font-size:12px">You're getting this because you subscribed to %s status updates. <a href="%s">Unsubscribe</a></p>`,
		htmlEscape(label(inc.Status)), htmlEscape(inc.Title), inc.At.UTC().Format("2006-01-02 15:04 UTC"), htmlEscape(inc.Body),
		htmlEscape(link), htmlEscape(page.Title), htmlEscape(unsub))
	return buildMail(cfg.From, sub.Email, subject, text, html, unsub)
}

func (n *Notifier) incidentSlack(page *store.Page, sub store.Subscriber, inc Incident) map[string]any {
	link, unsub := n.incidentURL(page, inc), n.unsubscribeURL(page, sub)
	text := fmt.Sprintf("*%s* — <%s|%s>\n%s", slackEscape(label(inc.Status)), link, slackEscape(inc.Title), slackEscape(inc.Body))
	return map[string]any{
		"text": fmt.Sprintf("[%s] %s: %s", page.Title, label(inc.Status), inc.Title),
		"blocks": []any{
			map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": text}},
			map[string]any{"type": "context", "elements": []any{map[string]any{"type": "mrkdwn",
				"text": fmt.Sprintf("%s status · <%s|Unsubscribe>", slackEscape(page.Title), unsub)}}},
		},
	}
}

// try runs send up to attempts times with a pause between (not retrying
// a 4xx, which won't change), logging only the sanitized error string.
// It returns the last error.
func (n *Notifier) try(ctx context.Context, what string, send func(context.Context) error) error {
	var err error
	for i := 0; i < attempts; i++ {
		c, cancel := context.WithTimeout(ctx, sendTimeout)
		err = send(c)
		cancel()
		if err == nil || strings.HasPrefix(err.Error(), "http 4") {
			break
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(n.retry):
		}
	}
	if err != nil {
		logx.Err(n.log, what+" failed", err)
	}
	return err
}

// buildMail renders a multipart/alternative message. Header values are
// stripped of CR/LF (no header injection from titles), the subject is
// RFC 2047-encoded, both parts are quoted-printable (so no body line can
// fake a MIME boundary or exceed SMTP's 998-character limit), and the
// boundary is random per message.
func buildMail(from, to, subject, text, html, unsubscribe string) []byte {
	clean := func(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }
	boundary := "opskest-" + store.NewToken()[:24]
	domain := "opskest.local"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		domain = strings.Trim(clean(from[i+1:]), "<> ")
	}
	qp := func(s string) string {
		var buf bytes.Buffer
		w := quotedprintable.NewWriter(&buf)
		_, _ = w.Write([]byte(s))
		_ = w.Close()
		return buf.String()
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", clean(from))
	fmt.Fprintf(&b, "To: %s\r\n", clean(to))
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", clean(subject)))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", store.NewToken()[:32], domain)
	if unsubscribe != "" {
		fmt.Fprintf(&b, "List-Unsubscribe: <%s>\r\n", clean(unsubscribe))
	}
	fmt.Fprintf(&b, "Auto-Submitted: auto-generated\r\nMIME-Version: 1.0\r\n")
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n%s\r\n", boundary, qp(text))
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n%s\r\n", boundary, qp(html))
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;").Replace(s)
}

func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
