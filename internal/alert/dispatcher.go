package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"opskest/internal/logx"
	"opskest/internal/model"
	"opskest/internal/store"
)

// intakeQueueCap is the capacity of the dispatcher's main event queue
// . When full, Publish drops the oldest queued
// event to make room for the new one, so a storm of incoming events never
// blocks the caller (the check pipeline).
const intakeQueueCap = 10000

// channelQueueCap is the capacity of each channel's own delivery queue.
// When full, the newest message for that channel is dropped and counted.
const channelQueueCap = 1000

// defaultTimeout is how long a single delivery attempt is allowed to run.
const defaultTimeout = 10 * time.Second

// defaultHold is how long Down/Recovered events are held to detect a
// storm before deciding whether to group them.
const defaultHold = 3 * time.Second

// defaultRetry is the backoff schedule after a failed attempt.
var defaultRetry = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}

// Sender delivers one rendered Message to one channel. Implementations
// must never log cfg or the message body; a failure is reported through
// the returned error, which the dispatcher records to alert_log and logs
// (via logx.Err) with only the channel id/type as context.
type Sender interface {
	Send(ctx context.Context, cfg store.ChannelConfig, m Message) error
}

// senders maps a channel type to its Sender. Every value is a stateless,
// zero-size struct, so the same instance is safe for concurrent use
// across every channel worker.
var senders = map[string]Sender{
	"slack":     slackSender{},
	"email":     emailSender{},
	"teams":     teamsSender{},
	"gchat":     gchatSender{},
	"webhook":   webhookSender{},
	"pagerduty": pagerdutySender{},
	"opsgenie":  opsgenieSender{},

	"discord":    discordSender{},
	"mattermost": markdownWebhookSender{},
	"rocketchat": markdownWebhookSender{},
	"telegram":   telegramSender{},
	"ntfy":       ntfySender{},
	"gotify":     gotifySender{},
	"pushover":   pushoverSender{},
	"matrix":     matrixSender{},

	"zulip":         zulipSender{},
	"webex":         webexSender{},
	"pushbullet":    pushbulletSender{},
	"ifttt":         iftttSender{},
	"apprise":       appriseSender{},
	"twilio":        twilioSender{},
	"homeassistant": homeAssistantSender{},
	"bark":          barkSender{},
	"signal":        signalSender{},
}

// SenderFor returns the Sender registered for channelType, or false if the
// type is unknown.
func SenderFor(channelType string) (Sender, bool) {
	s, ok := senders[channelType]
	return s, ok
}

// httpClientKey is the context key an HTTP-based Sender's doRequest uses
// to find the *http.Client to use. Sender's Send signature (fixed by the
// package's public interface) carries no client of its own, so the
// Dispatcher attaches its configured client to the context for each
// delivery attempt instead of mutating a package-level variable — the
// client is a Dispatcher-owned value, not shared mutable global state.
type httpClientKey struct{}

// withHTTPClient returns a context carrying c for doRequest to find.
func withHTTPClient(ctx context.Context, c *http.Client) context.Context {
	return context.WithValue(ctx, httpClientKey{}, c)
}

// clientFor returns the HTTP client attached to ctx, or deliveryClient
// when none was attached (e.g. a Sender invoked directly, as the golden
// tests do, with a bare context).
func clientFor(ctx context.Context) *http.Client {
	if c, ok := ctx.Value(httpClientKey{}).(*http.Client); ok && c != nil {
		return c
	}
	return deliveryClient
}

// deliveryClient is the default client for channel deliveries. It never
// follows a redirect: net/http would replay a 307/308 POST (with its body, which
// for Pushover carries the app token) and custom auth headers such as
// X-Gotify-Key (only Authorization and cookies are dropped on a cross-host
// hop) to whatever host the destination names. A redirect surfaces as
// "http 3xx" in the notification log instead.
var deliveryClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// ValidateConfig checks the fields a channel of channelType actually
// uses. URLs must be https, except an email host (a bare hostname, not a
// URL).
func ValidateConfig(channelType string, cfg store.ChannelConfig) model.FieldErrors {
	fe := model.FieldErrors{}
	switch channelType {
	case "slack", "teams", "gchat", "webhook":
		if cfg.URL == "" {
			fe["url"] = "required"
		} else if !strings.HasPrefix(cfg.URL, "https://") {
			fe["url"] = "must be https"
		}

	case "discord":
		requireURL(fe, cfg.URL, true)

	case "mattermost", "rocketchat":
		requireURL(fe, cfg.URL, false)

	case "telegram":
		if cfg.APIKey == "" {
			fe["apiKey"] = "required (the bot token)"
		}
		if cfg.Target == "" {
			fe["target"] = "required (the chat id)"
		}
		if cfg.URL != "" {
			requireURL(fe, cfg.URL, false)
		}

	case "ntfy":
		requireURL(fe, cfg.URL, false)
		if u, err := url.Parse(cfg.URL); err == nil && u.Host != "" && strings.Trim(u.Path, "/") == "" {
			fe["url"] = "include the topic, e.g. https://ntfy.sh/my-alerts"
		}

	case "gotify":
		requireURL(fe, cfg.URL, false)
		if cfg.APIKey == "" {
			fe["apiKey"] = "required (the application token)"
		}

	case "pushover":
		if cfg.APIKey == "" {
			fe["apiKey"] = "required (the application token)"
		}
		if cfg.Target == "" {
			fe["target"] = "required (the user or group key)"
		}
		if cfg.URL != "" {
			requireURL(fe, cfg.URL, false)
		}

	case "matrix":
		requireURL(fe, cfg.URL, false)
		if cfg.APIKey == "" {
			fe["apiKey"] = "required (an access token)"
		}
		if cfg.Target == "" {
			fe["target"] = "required (the room id, e.g. !abc:example.org)"
		}

	case "pagerduty":
		if cfg.RoutingKey == "" {
			fe["routingKey"] = "required"
		}

	case "opsgenie":
		if cfg.APIKey == "" {
			fe["apiKey"] = "required"
		}
		if cfg.Region != "" && cfg.Region != "us" && cfg.Region != "eu" {
			fe["region"] = "must be us or eu"
		}

	case "email":
		if cfg.Host == "" {
			fe["host"] = "required"
		}
		if cfg.From == "" {
			fe["from"] = "required"
		}
		if len(cfg.To) == 0 {
			fe["to"] = "required"
		}
		if cfg.TLSMode != "" && cfg.TLSMode != "starttls" && cfg.TLSMode != "tls" && cfg.TLSMode != "none" {
			fe["tlsMode"] = "must be starttls, tls or none"
		}

	default:
		if !validateMore(channelType, cfg, fe) {
			fe["type"] = "unknown channel type"
		}
	}
	if p := TemplateProblem(cfg.Template); p != "" {
		fe["template"] = p
	}
	for k, v := range QuietProblems(cfg.Quiet) {
		fe[k] = v
	}
	return fe
}

// requireURL records an error unless raw is an https URL, or, for services
// people commonly self-host on a private network (httpsOnly false), http.
func requireURL(fe model.FieldErrors, raw string, httpsOnly bool) {
	switch {
	case raw == "":
		fe["url"] = "required"
	case strings.HasPrefix(raw, "https://"):
	case !httpsOnly && strings.HasPrefix(raw, "http://"):
	case httpsOnly:
		fe["url"] = "must be https"
	default:
		fe["url"] = "must be http(s)"
	}
}

// Options configures a Dispatcher. Every field has a
// default, applied by setDefaults when left zero.
type Options struct {
	AppURL   string
	Loc      *time.Location // display time zone for alert "Time:" lines
	Now      func() time.Time
	Log      *slog.Logger
	HTTP     *http.Client
	Retry    []time.Duration // default 5s, 30s, 2m
	Timeout  time.Duration   // default 10s; per delivery attempt
	Hold     time.Duration   // default 3s
	Settings func() (threshold int, window time.Duration)
}

func (o *Options) setDefaults() {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Retry == nil {
		o.Retry = defaultRetry
	}
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	if o.Hold <= 0 {
		o.Hold = defaultHold
	}
	if o.HTTP == nil {
		o.HTTP = deliveryClient
	}
}

// workItem is one message queued for delivery to a specific channel. The
// channel's config is captured at enqueue time, so an edit picked up by a
// later ReloadChannels never rewrites a message already in flight.
type workItem struct {
	channel store.Channel
	msg     Message
}

// Dispatcher resolves model.Events into channel messages, groups storms
// of Down/Recovered events, and delivers them on one worker
// goroutine per channel so a single slow or failing channel never delays
// delivery to any other channel.
type Dispatcher struct {
	st   *store.Store
	w    *store.Writer
	lk   Lookup
	opts Options

	intake chan model.Event
	storm  stormState

	mu       sync.RWMutex
	channels map[int64]store.Channel

	wmu     sync.Mutex
	workers map[int64]chan workItem

	dropped    atomic.Uint64
	suppressed atomic.Uint64
	stopped    atomic.Bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewDispatcher builds a Dispatcher. Call Start to begin processing.
func NewDispatcher(st *store.Store, w *store.Writer, lk Lookup, o Options) *Dispatcher {
	o.setDefaults()
	return &Dispatcher{
		st:       st,
		w:        w,
		lk:       lk,
		opts:     o,
		intake:   make(chan model.Event, intakeQueueCap),
		channels: make(map[int64]store.Channel),
		workers:  make(map[int64]chan workItem),
	}
}

// Start loads the channel cache and begins the intake and per-channel
// worker goroutines. A failed initial load is logged, not fatal:
// ReloadChannels can recover later.
func (d *Dispatcher) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	d.ctx = ctx
	d.cancel = cancel

	if err := d.ReloadChannels(ctx); err != nil {
		logx.Err(d.opts.Log, "alert: initial channel load failed", err)
	}

	d.wg.Add(1)
	go d.runIntake(ctx)
}

// Stop cancels every in-flight operation and waits for the intake and
// channel worker goroutines to exit. After Stop, ReloadChannels is a
// no-op: nothing would consume the refreshed cache since every worker has
// already exited.
func (d *Dispatcher) Stop() {
	if d.cancel == nil {
		return
	}
	d.stopped.Store(true)
	d.cancel()
	d.wg.Wait()
}

// Publish queues ev for processing. It never blocks: when the intake
// queue is full, the oldest queued event is dropped to make room.
func (d *Dispatcher) Publish(ev model.Event) {
	select {
	case d.intake <- ev:
		return
	default:
	}

	select {
	case <-d.intake:
		d.dropped.Add(1)
		logx.Err(d.opts.Log, "alert: intake queue full, dropped oldest event", errors.New("queue full"))
	default:
	}

	select {
	case d.intake <- ev:
	default:
		d.dropped.Add(1)
	}
}

// Suppressed returns how many messages quiet hours have held back so far.
func (d *Dispatcher) Suppressed() uint64 { return d.suppressed.Load() }

// displayNow is the current time in the display time zone, which is where
// quiet hours are read.
func (d *Dispatcher) displayNow() time.Time {
	now := d.opts.Now()
	if d.opts.Loc != nil {
		return now.In(d.opts.Loc)
	}
	return now.UTC()
}

// Dropped returns the number of events/messages dropped so far because a
// queue (intake or per-channel) was full.
func (d *Dispatcher) Dropped() uint64 {
	return d.dropped.Load()
}

// ReloadChannels refreshes the in-memory channel cache from the store and
// starts a worker for any channel that doesn't have one yet.
func (d *Dispatcher) ReloadChannels(ctx context.Context) error {
	if d.stopped.Load() {
		return nil
	}
	list, err := d.st.ListChannels(ctx)
	if err != nil {
		return fmt.Errorf("alert: list channels: %w", err)
	}

	d.mu.Lock()
	d.channels = make(map[int64]store.Channel, len(list))
	for _, c := range list {
		d.channels[c.ID] = c
	}
	d.mu.Unlock()

	for _, c := range list {
		d.ensureWorker(c.ID)
	}
	return nil
}

func (d *Dispatcher) channel(id int64) (store.Channel, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	c, ok := d.channels[id]
	return c, ok
}

// ensureWorker returns id's delivery queue, starting its worker goroutine
// on first use.
func (d *Dispatcher) ensureWorker(id int64) chan workItem {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if q, ok := d.workers[id]; ok {
		return q
	}
	q := make(chan workItem, channelQueueCap)
	d.workers[id] = q
	d.wg.Add(1)
	go d.runWorker(d.ctx, q)
	return q
}

// enqueueToChannel looks up id's current config and queues msg for
// delivery. It never blocks: a full channel queue drops the message and
// counts it.
func (d *Dispatcher) enqueueToChannel(id int64, msg Message) {
	ch, ok := d.channel(id)
	if !ok {
		return
	}
	// Regional and response-time notices are informational: paging tools
	// would open an incident that only a Recovered resolves, so they skip them.
	if informational(msg.Kind) && (ch.Type == "pagerduty" || ch.Type == "opsgenie") {
		return
	}
	if quietHolds(ch, msg.Kind, d.displayNow()) {
		d.suppressed.Add(1)
		d.opts.Log.Debug("alert: held back by quiet hours", "channel_id", id, "channel_type", ch.Type, "event", string(msg.Kind))
		return
	}
	msg = withChannelTemplate(ch.Config, msg)
	q := d.ensureWorker(id)
	select {
	case q <- workItem{channel: ch, msg: msg}:
	default:
		d.dropped.Add(1)
		logx.Err(d.opts.Log, "alert: channel queue full, dropped message", errors.New("queue full"), "channel_id", id, "channel_type", ch.Type)
	}
}

// runWorker delivers every message queued for one channel, in order,
// with retries. One worker per channel means a slow or failing channel
// can never delay delivery to any other channel.
func (d *Dispatcher) runWorker(ctx context.Context, q chan workItem) {
	defer d.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-q:
			if !ok {
				return
			}
			d.deliver(ctx, item.channel, item.msg)
		}
	}
}

// deliver sends msg to ch, retrying on failure per opts.Retry, and writes
// every attempt to alert_log so the UI can show why a channel failed.
func (d *Dispatcher) deliver(ctx context.Context, ch store.Channel, msg Message) {
	sender, ok := SenderFor(ch.Type)
	if !ok {
		logx.Err(d.opts.Log, "alert: no sender for channel type", errors.New("unknown channel type"), "channel_id", ch.ID, "channel_type", ch.Type)
		return
	}

	var monitorID *int64
	if len(msg.Items) == 1 {
		id := msg.Items[0].MonitorID
		monitorID = &id
	}

	attempts := 1 + len(d.opts.Retry)
	for attempt := 1; attempt <= attempts; attempt++ {
		actx, cancel := context.WithTimeout(ctx, d.opts.Timeout)
		err := sender.Send(withHTTPClient(actx, d.opts.HTTP), ch.Config, msg)
		cancel()

		d.logAttempt(ch, monitorID, msg.Kind, attempt, err)
		if err == nil {
			return
		}
		logx.Err(d.opts.Log, "alert: delivery attempt failed", err, "channel_id", ch.ID, "channel_type", ch.Type, "attempt", attempt)

		if attempt == attempts {
			return
		}
		select {
		case <-time.After(d.opts.Retry[attempt-1]):
		case <-ctx.Done():
			return
		}
	}
}

// logAttempt records one delivery attempt to alert_log via the writer's
// hot path: never blocking, batched with everything else.
// monitorID is nil for a grouped message (many monitors) and for a test
// send (no monitor at all), which alert_log stores as NULL.
func (d *Dispatcher) logAttempt(ch store.Channel, monitorID *int64, kind model.EventKind, attempt int, err error) {
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	chID := ch.ID
	d.w.Do(store.OpAlertLog(store.AlertLogEntry{
		TS:        d.opts.Now().UnixMilli(),
		ChannelID: &chID,
		MonitorID: monitorID,
		Event:     string(kind),
		OK:        err == nil,
		Attempt:   attempt,
		Error:     errText,
	}))
}

// SendTest sends a one-off test message to ch synchronously, with no
// retries, logs the single attempt to alert_log, and returns the send
// error to the caller so the UI can show it immediately. The logged
// attempt always has a NULL monitor_id: a test send isn't about any real
// monitor.
func (d *Dispatcher) SendTest(ctx context.Context, ch store.Channel) error {
	sender, ok := SenderFor(ch.Type)
	if !ok {
		return fmt.Errorf("alert: unknown channel type %q", ch.Type)
	}

	msg := Message{
		Kind: model.EvTest,
		Items: []Item{{
			Name:   "opskest",
			Reason: "This is a test alert from opskest.",
			URL:    d.opts.AppURL,
		}},
		At:     d.opts.Now(),
		AppURL: d.opts.AppURL,
		Loc:    d.opts.Loc,
	}
	msg = withChannelTemplate(ch.Config, msg)
	msg.Title = Title(msg)

	actx, cancel := context.WithTimeout(ctx, d.opts.Timeout)
	defer cancel()
	err := sender.Send(withHTTPClient(actx, d.opts.HTTP), ch.Config, msg)

	d.logAttempt(ch, nil, msg.Kind, 1, err)
	return err
}

// responseSnippetLimit is how much of a failed response body is kept in
// the sanitized error, so the UI can show why a channel failed.
const responseSnippetLimit = 120

// redactURLPattern matches an absolute http(s) URL anywhere in text — a
// channel's webhook URL (which can carry a secret token/path) must never
// reach alert_log.
var redactURLPattern = regexp.MustCompile(`https?://\S+`)

// redactTokenPattern matches a 24-or-more character run of URL-safe token
// characters: the shape of an API key or session token, so one can't leak
// even when it isn't wrapped in a URL.
var redactTokenPattern = regexp.MustCompile(`[A-Za-z0-9_-]{24,}`)

// redact strips anything that looks like a URL or a long token from s,
// replacing each with "…".
func redact(s string) string {
	s = redactURLPattern.ReplaceAllString(s, "…")
	s = redactTokenPattern.ReplaceAllString(s, "…")
	return s
}

// firstRunes returns the first n runes of s (all of s if it's shorter).
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// doRequest sends an HTTP request and, on failure, returns an error safe
// to log: the request URL and body are never included (a channel
// URL or its body can carry a secret), but the HTTP status and a redacted
// snippet of the response body are kept, since that's exactly the detail
// the UI needs to show why a channel failed (e.g. "http 404:
// channel_not_found").
func doRequest(ctx context.Context, method, endpoint string, headers map[string]string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("build request failed")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := clientFor(ctx).Do(req)
	if err != nil {
		return sanitizeHTTPErr(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		snippet := redact(firstRunes(strings.TrimSpace(string(raw)), responseSnippetLimit))
		if snippet == "" {
			return fmt.Errorf("http %d", resp.StatusCode)
		}
		return fmt.Errorf("http %d: %s", resp.StatusCode, snippet)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// postJSON marshals payload and POSTs it as application/json, merging in
// any extra headers (e.g. Authorization, X-Opskest-Signature).
func postJSON(ctx context.Context, endpoint string, extraHeaders map[string]string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("alert: marshal payload: %w", err)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	for k, v := range extraHeaders {
		headers[k] = v
	}
	return doRequest(ctx, http.MethodPost, endpoint, headers, body)
}

// sanitizeHTTPErr reduces a net/http client error to a message safe to
// log. A failed http.Client.Do returns a *url.Error{Op, URL, Err}; URL can
// carry a channel secret, so only Err's own text is ever used — which for
// the common cases (dial/TLS failures) already reads exactly like the
// underlying cause: "connection refused", "x509: certificate has expired
// or is not yet valid". It's redacted anyway, in case the transport ever
// echoes a redirect target or similar.
func sanitizeHTTPErr(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("request timed out")
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("request canceled")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return errors.New("request timed out")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return errors.New(redact(urlErr.Err.Error()))
	}
	return errors.New("request failed")
}

// Resend delivers ev to one channel right now, once (no retry schedule),
// and logs the attempt like any other. It backs "Resend" on a failed
// entry in the notification log, so an alert that never arrived (a
// broken webhook, since fixed) can be pushed again by hand.
func (d *Dispatcher) Resend(ctx context.Context, ch store.Channel, ev model.Event) error {
	sender, ok := SenderFor(ch.Type)
	if !ok {
		return fmt.Errorf("alert: unknown channel type %q", ch.Type)
	}
	it, _, ok := d.resolve(ev)
	if !ok {
		return errors.New("alert: monitor no longer exists")
	}
	msg := withChannelTemplate(ch.Config, Message{Kind: ev.Kind, Items: []Item{it}, At: d.opts.Now(), AppURL: d.opts.AppURL, Loc: d.opts.Loc})
	msg.Title = Title(msg)
	actx, cancel := context.WithTimeout(ctx, d.opts.Timeout)
	defer cancel()
	err := sender.Send(withHTTPClient(actx, d.opts.HTTP), ch.Config, msg)
	id := ev.MonitorID
	d.logAttempt(ch, &id, msg.Kind, 1, err)
	return err
}

// Redact strips URLs and long tokens from s (see redact), for error text
// shown to users.
func Redact(s string) string { return redact(s) }

// informational kinds never page (PagerDuty/Opsgenie skip them).
func informational(k model.EventKind) bool {
	switch k {
	case model.EvRegional, model.EvRegionalCleared, model.EvLatencyHigh, model.EvLatencyCleared:
		return true
	}
	return false
}

// webhookClient never follows redirects: a third-party webhook URL (a
// status page subscriber's Slack hook) must not be able to bounce the
// request somewhere else.
var webhookClient = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// PostJSON POSTs payload as JSON to endpoint with a no-redirect client and
// sanitized errors that never echo the URL ("http 404: …" on a bad status).
func PostJSON(ctx context.Context, endpoint string, payload any) error {
	if _, ok := ctx.Value(httpClientKey{}).(*http.Client); !ok {
		ctx = context.WithValue(ctx, httpClientKey{}, webhookClient)
	}
	return postJSON(ctx, endpoint, nil, payload)
}
