// Package regions checks monitors from other countries on the public
// Globalping probe network (nothing to host or maintain).
//
// Two jobs share one hourly budget of Globalping tests (one test = one
// probe in one country):
//
//   - Worldwide rotation: every eligible monitor set to Worldwide (or
//     Custom) is checked from its countries in turn, paced so the whole
//     rotation fits the budget. This server checks from its deployed location
//     every interval; the rotation adds the configured countries. A country
//     failing while others succeed is a regional issue: it's re-checked
//     once, recorded, and alerted ("failing from China, Russia").
//   - Outage confirmation: when a monitor goes down, check it from its
//     countries right away and record the verdict on the outage. These
//     have priority over the rotation.
//
// Only http, ping and dns monitors on public hostnames are eligible, and
// an HTTP monitor only when it needs no custom headers, auth or body —
// only the hostname and path are ever sent, never credentials.
package regions

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"opskest/internal/check"
	"opskest/internal/logx"
	"opskest/internal/model"
	"opskest/internal/store"
)

// Sink is engine.Sink, restated to avoid an import cycle.
type Sink interface{ Publish(ev model.Event) }

// Config is what Settings → Multi-region decides; read on every step so a
// change applies at once.
type Config struct {
	Enabled      bool // confirm outages
	Sweep        bool // worldwide rotation
	Alerts       bool // alert on regional issues
	Countries    []string
	TestsPerHour int
	Token        string
}

// Hooks give the confirmer read access to the engine, which is built
// after it.
type Hooks struct {
	Monitors func() []*model.Monitor
	Monitor  func(id int64) (*model.Monitor, bool)
	Status   func(id int64) model.Status
}

const (
	queueSize  = 64
	jobTimeout = 30 * time.Second
)

type job struct{ outageID, monitorID int64 }

type spend struct {
	at time.Time
	n  int
}

// Confirmer wraps the alert sink: every event passes straight through;
// DOWN events with an outage also queue a confirmation.
type Confirmer struct {
	next   Sink
	st     *store.Store
	w      *store.Writer
	config func() Config
	log    *slog.Logger
	now    func() time.Time
	run    func(ctx context.Context, req check.GPRequest) ([]check.GPProbe, error)

	mu        sync.Mutex
	hooks     Hooks
	spent     []spend
	lastSwept map[int64]time.Time
	results   map[int64][]store.RegionResult
	failing   map[int64]map[string]bool

	q chan job
}

func New(next Sink, st *store.Store, w *store.Writer, config func() Config, log *slog.Logger, now func() time.Time) *Confirmer {
	return &Confirmer{
		next: next, st: st, w: w, config: config, log: log, now: now, run: check.Globalping,
		lastSwept: map[int64]time.Time{}, results: map[int64][]store.RegionResult{}, failing: map[int64]map[string]bool{},
		q: make(chan job, queueSize),
	}
}

// SetHooks is called once the engine exists.
func (c *Confirmer) SetHooks(h Hooks) {
	c.mu.Lock()
	c.hooks = h
	c.mu.Unlock()
}

func (c *Confirmer) Publish(ev model.Event) {
	c.next.Publish(ev)
	if ev.Kind != model.EvDown || ev.OutageID == 0 {
		return
	}
	select {
	case c.q <- job{outageID: ev.OutageID, monitorID: ev.MonitorID}:
	default: // queue full: skip rather than block the engine
	}
}

// Start restores the last worldwide results and runs both workers until
// ctx ends.
func (c *Confirmer) Start(ctx context.Context) {
	if c.st != nil {
		if all, err := c.st.RegionResults(ctx); err == nil {
			c.mu.Lock()
			for id, rs := range all {
				c.results[id] = rs
				fail := map[string]bool{}
				var last int64
				for _, r := range rs {
					if !r.OK {
						fail[r.Country] = true
					}
					last = max(last, r.CheckedAt)
				}
				c.failing[id] = fail
				c.lastSwept[id] = time.UnixMilli(last)
			}
			c.mu.Unlock()
		}
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case j := <-c.q:
				c.confirm(ctx, j)
			}
		}
	}()
	go c.sweepLoop(ctx)
}

// ── Budget ─────────────────────────────────────────────────────────────

func (c *Confirmer) usedLocked(now time.Time) int {
	keep := c.spent[:0]
	n := 0
	for _, s := range c.spent {
		if now.Sub(s.at) < time.Hour {
			keep = append(keep, s)
			n += s.n
		}
	}
	c.spent = keep
	return n
}

// take reserves n tests if the hourly budget (minus reserve) and
// Globalping's own remaining allowance both have room.
func (c *Confirmer) take(n, reserve, perHour int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.usedLocked(now)+n > perHour-reserve {
		return false
	}
	if l, ok := check.GlobalpingLimit(); ok && now.Before(l.ResetAt) && l.Remaining < n+reserve {
		return false
	}
	c.spent = append(c.spent, spend{at: now, n: n})
	return true
}

// Usage reports Globalping tests used by opskest in the last hour.
func (c *Confirmer) Usage() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usedLocked(c.now())
}

// ── Eligibility ────────────────────────────────────────────────────────

// Plan is where and how a monitor is checked from other countries.
type Plan struct {
	Countries []string
	Req       check.GPRequest
	Reason    string // why it isn't eligible ("" when it is)
}

// PlanFor decides a monitor's worldwide checks.
func PlanFor(m *model.Monitor, cfg Config) Plan {
	switch {
	case m.Settings.Vendor:
		return Plan{Reason: "3rd-party status feeds are not checked worldwide"}
	case m.Settings.RegionMode == "local" || m.Settings.RegionMode == "india": // "india" is the legacy persisted spelling of "local"
		return Plan{Reason: "Monitoring region is this server only"}
	case m.Type == model.TypeHTTP:
		s := m.Settings
		if len(s.Headers) > 0 || (s.AuthType != "" && s.AuthType != "none") || (s.Method != "" && s.Method != "GET" && s.Method != "HEAD") || s.Body != "" {
			return Plan{Reason: "needs custom headers, auth or a request body, which are never sent to other networks"}
		}
		if s.ProxyURL != "" || s.CACert != "" || m.Secrets.ClientCert != "" {
			return Plan{Reason: "uses a proxy, a custom CA or a client certificate, which other networks cannot reproduce"}
		}
	case m.Type == model.TypePing, m.Type == model.TypeDNS:
	default:
		return Plan{Reason: "only HTTP, ping and DNS monitors can be checked from other countries"}
	}
	countries := cfg.Countries
	if m.Settings.RegionMode == "custom" {
		countries = m.Settings.RegionCountries
	}
	req, ok := Request(m.Type, m.Target, countries)
	if !ok {
		return Plan{Reason: "private or internal hostname: only this server can reach it"}
	}
	req.Token = cfg.Token
	return Plan{Countries: req.Countries, Req: req}
}

// Request builds the Globalping request for a target, or ok=false when it
// isn't eligible (wrong type, unparsable, or a private/internal host).
func Request(typ model.MonitorType, target string, countries []string) (check.GPRequest, bool) {
	var u *url.URL
	var kind string
	switch typ {
	case model.TypeHTTP:
		p, err := url.Parse(target)
		if err != nil || (p.Scheme != "http" && p.Scheme != "https") {
			return check.GPRequest{}, false
		}
		// Host and path only: query strings and userinfo often carry
		// tokens, and reachability doesn't need them.
		u, kind = &url.URL{Scheme: p.Scheme, Host: p.Host, Path: p.Path}, "http"
	case model.TypePing:
		u, kind = &url.URL{Host: bareHost(target)}, "ping"
	case model.TypeDNS:
		u, kind = &url.URL{Host: bareHost(target)}, "dns"
	default:
		return check.GPRequest{}, false
	}
	if !PublicHost(u.Hostname()) {
		return check.GPRequest{}, false
	}
	if len(countries) == 0 {
		countries = store.DefaultRegionCountries
	}
	return check.GPRequest{Kind: kind, Target: u, Countries: countries}, true
}

// bareHost brackets an IPv6 literal so url.URL.Hostname() doesn't mistake its
// last group for a port ("fd00::10" would otherwise read as host "fd00:").
func bareHost(target string) string {
	t := strings.TrimSpace(target)
	if ip := net.ParseIP(t); ip != nil && strings.Contains(t, ":") {
		return "[" + t + "]"
	}
	return t
}

// PublicHost reports whether host looks reachable from the internet:
// not an IP in a private/loopback/link-local range, and not a bare or
// internal-suffix hostname. It does not resolve DNS: a public-looking name
// that resolves to a private address (split-horizon DNS) still passes, and
// only its name is ever sent to Globalping.
func PublicHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	// Ideographic and fullwidth full stops are label separators to IDNA.
	host = strings.NewReplacer("。", ".", "．", ".", "｡", ".").Replace(host)
	host = strings.TrimRight(host, ".") // "host.internal." and "host.internal.." are the same name
	if host == "" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return publicIP(ip)
	}
	if !strings.Contains(host, ".") {
		return false
	}
	// A real top-level domain starts with a letter. Digits there mean an IPv4
	// shorthand a resolver expands ("127.1", "0x7f.0.0.1", "010.1.2.3").
	if r, _ := utf8.DecodeRuneInString(host[strings.LastIndexByte(host, '.')+1:]); !unicode.IsLetter(r) {
		return false
	}
	for _, suf := range []string{".local", ".internal", ".lan", ".home", ".corp", ".intranet", ".localhost", ".svc", ".cluster.local", ".home.arpa",
		".localdomain", ".test", ".invalid", ".example", ".private", ".domain", ".arpa", ".onion", ".ts.net"} {
		if strings.HasSuffix(host, suf) {
			return false
		}
	}
	return true
}

func publicIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil && (v4[0] == 0 || v4[0] >= 240) { // "this network", reserved and broadcast
		return false
	}
	return !(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || isCGNAT(ip))
}

func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64
}

// evaluate judges one probe the way the monitor would: HTTP status in
// the accepted codes, and the keyword / JSON check on the body when the
// probe returned one.
func evaluate(m *model.Monitor, p check.GPProbe) (bool, string) {
	if !p.OK && p.Code == 0 {
		if p.Detail == "" {
			return false, "no response"
		}
		return false, p.Detail
	}
	if m.Type != model.TypeHTTP {
		return p.OK, p.Detail
	}
	s := m.Settings
	cs, err := model.ParseCodes(s.AcceptedCodes)
	if err != nil {
		cs, _ = model.ParseCodes("200-299")
	}
	if !cs.Match(p.Code) {
		return false, fmt.Sprintf("HTTP %d", p.Code)
	}
	if len(p.Body) > 0 && s.Keyword != "" {
		mode := s.KeywordMode
		if mode == "" {
			mode = "contains"
		}
		if ok, reason := check.EvalKeyword(p.Body, s.Keyword, mode, s.CaseSensitive); !ok {
			return false, reason
		}
	}
	if len(p.Body) > 0 && s.JSONPath != "" {
		if ok, reason := check.EvalJSON(p.Body, s.JSONPath, s.JSONOp, s.JSONExpected); !ok {
			return false, reason
		}
	}
	return true, ""
}

// ── Redirects ──────────────────────────────────────────────────────────

const maxRedirectHops = 3

// probe runs req and, for HTTP, follows redirects the way the monitor
// does (Globalping itself doesn't): countries answering 3xx with a
// Location are re-checked against that URL, up to maxRedirectHops, and
// only when the monitor allows redirects. Each hop counts toward usage.
func (c *Confirmer) probe(ctx context.Context, req check.GPRequest, m *model.Monitor) ([]check.GPProbe, error) {
	probes, err := c.run(ctx, req)
	if err != nil || req.Kind != "http" || (m != nil && m.Settings.MaxRedirects != nil && *m.Settings.MaxRedirects == 0) {
		return probes, err
	}
	for hop := 0; hop < maxRedirectHops; hop++ {
		groups := map[string][]int{} // absolute Location -> probe indexes
		for i, p := range probes {
			if p.Code >= 300 && p.Code < 400 && p.Location != "" {
				if next, ok := resolveLocation(req.Target, p.Location); ok {
					groups[next.String()] = append(groups[next.String()], i)
				}
			}
		}
		if len(groups) == 0 {
			break
		}
		for loc, idx := range groups {
			next, _ := url.Parse(loc)
			if !PublicHost(next.Hostname()) {
				continue
			}
			countries := make([]string, 0, len(idx))
			for _, i := range idx {
				countries = append(countries, probes[i].Country)
			}
			r := req
			r.Target, r.Countries = &url.URL{Scheme: next.Scheme, Host: next.Host, Path: next.Path}, countries
			c.record(len(countries))
			again, err := c.run(ctx, r)
			if err != nil {
				continue
			}
			byCountry := map[string]check.GPProbe{}
			for _, p := range again {
				byCountry[p.Country] = p
			}
			for _, i := range idx {
				if p, ok := byCountry[probes[i].Country]; ok {
					if p.City == "" {
						p.City = probes[i].City
					}
					probes[i] = p
				}
			}
		}
	}
	return probes, nil
}

// resolveLocation turns a Location header into an absolute http(s) URL,
// relative to base.
func resolveLocation(base *url.URL, loc string) (*url.URL, bool) {
	u, err := url.Parse(strings.TrimSpace(loc))
	if err != nil {
		return nil, false
	}
	abs := base.ResolveReference(u)
	if abs.Scheme != "http" && abs.Scheme != "https" || abs.Host == "" {
		return nil, false
	}
	return abs, true
}

// ── Outage confirmation ────────────────────────────────────────────────

func (c *Confirmer) confirm(ctx context.Context, j job) {
	cfg := c.config()
	if !cfg.Enabled {
		return
	}
	c.mu.Lock()
	h := c.hooks
	c.mu.Unlock()
	if h.Monitor == nil {
		return
	}
	m, ok := h.Monitor(j.monitorID)
	if !ok {
		return
	}
	plan := PlanFor(m, cfg)
	if plan.Reason != "" || !c.take(len(plan.Countries), 0, cfg.TestsPerHour) {
		return
	}
	rc := c.measure(ctx, plan.Req, m)
	c.w.DoFor(j.monitorID, store.OpSetOutageRegions(j.outageID, rc))
}

// measure runs one Globalping measurement and summarises it for an
// outage; m (optional) applies the monitor's own success rules.
func (c *Confirmer) measure(ctx context.Context, req check.GPRequest, m *model.Monitor) store.RegionCheck {
	jctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()
	probes, err := c.probe(jctx, req, m)
	rc := store.RegionCheck{CheckedAt: c.now().UnixMilli(), Probes: []store.RegionProbe{}}
	if err != nil {
		logx.Err(c.log, "region check failed", err)
		rc.Error = gpError(err)
	}
	for _, p := range probes {
		ok, detail := p.OK, p.Detail
		if m != nil {
			ok, detail = evaluate(m, p)
		}
		rc.Total++
		if ok {
			rc.Reachable++
		}
		rc.Probes = append(rc.Probes, store.RegionProbe{
			Country: p.Country, City: p.City, OK: ok, Code: p.Code, LatencyMs: p.LatencyMs,
			Detail: model.TrimReason(detail),
		})
	}
	return rc
}

func gpError(err error) string {
	if strings.Contains(err.Error(), "rate limited") {
		return "Globalping hourly limit reached; try again later"
	}
	return "could not reach the Globalping network"
}

// CheckNow checks a URL / host from the configured countries right away
// (Settings → Multi-region "Check now"). It counts toward the hourly
// usage but isn't blocked by it. ok=false when the target isn't eligible.
func (c *Confirmer) CheckNow(ctx context.Context, typ model.MonitorType, target string) (store.RegionCheck, bool) {
	cfg := c.config()
	req, ok := Request(typ, target, cfg.Countries)
	if !ok {
		return store.RegionCheck{}, false
	}
	req.Token = cfg.Token
	c.record(len(req.Countries))
	return c.measure(ctx, req, nil), true
}

func (c *Confirmer) record(n int) {
	c.mu.Lock()
	c.spent = append(c.spent, spend{at: c.now(), n: n})
	c.mu.Unlock()
}

// ── Worldwide rotation ─────────────────────────────────────────────────

func (c *Confirmer) sweepLoop(ctx context.Context) {
	wait := 30 * time.Second // let the engine settle after boot
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = c.sweepNext(ctx)
	}
}

// sweepNext checks the monitor that has waited longest and returns how
// long to wait before the next one so the rotation stays within budget.
func (c *Confirmer) sweepNext(ctx context.Context) time.Duration {
	cfg := c.config()
	if !cfg.Sweep {
		return time.Minute
	}
	m, plan := c.nextDue(cfg)
	if m == nil {
		return 2 * time.Minute
	}
	cost := len(plan.Countries)
	reserve := len(cfg.Countries) // keep room for one outage confirmation
	if !c.take(cost, reserve, cfg.TestsPerHour) {
		return time.Minute
	}
	c.sweep(ctx, m, plan, cfg)
	return pace(cost, cfg.TestsPerHour-reserve)
}

func pace(cost, perHour int) time.Duration {
	if perHour < 1 {
		perHour = 1
	}
	return max(10*time.Second, time.Hour*time.Duration(cost)/time.Duration(perHour))
}

func (c *Confirmer) nextDue(cfg Config) (*model.Monitor, Plan) {
	c.mu.Lock()
	h := c.hooks
	c.mu.Unlock()
	if h.Monitors == nil {
		return nil, Plan{}
	}
	var best *model.Monitor
	var bestPlan Plan
	var bestAt time.Time
	for _, m := range h.Monitors() {
		if m.Paused {
			continue
		}
		plan := PlanFor(m, cfg)
		if plan.Reason != "" {
			continue
		}
		c.mu.Lock()
		at := c.lastSwept[m.ID]
		c.mu.Unlock()
		if best == nil || at.Before(bestAt) || (at.Equal(bestAt) && m.ID < best.ID) {
			best, bestPlan, bestAt = m, plan, at
		}
	}
	return best, bestPlan
}

// sweep checks m from its countries, re-checks any failing ones once to
// rule out a blip, stores the results and alerts on a change.
func (c *Confirmer) sweep(ctx context.Context, m *model.Monitor, plan Plan, cfg Config) []store.RegionResult {
	jctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()
	probes, err := c.probe(jctx, plan.Req, m)
	now := c.now()
	c.mu.Lock()
	c.lastSwept[m.ID] = now // even on error, so one bad monitor can't stall the rotation
	c.mu.Unlock()
	if err != nil {
		logx.Err(c.log, "worldwide check failed", err)
		return nil
	}
	byCountry := map[string]store.RegionResult{}
	for _, p := range probes {
		if _, dup := byCountry[p.Country]; dup {
			continue
		}
		ok, detail := evaluate(m, p)
		byCountry[p.Country] = store.RegionResult{MonitorID: m.ID, Country: p.Country, City: p.City, OK: ok, Code: p.Code,
			LatencyMs: p.LatencyMs, Detail: model.TrimReason(detail), CheckedAt: now.UnixMilli()}
	}

	// Re-check failing countries once before believing them.
	var failing []string
	for cc, r := range byCountry {
		if !r.OK {
			failing = append(failing, cc)
		}
	}
	if len(failing) > 0 && c.take(len(failing), 0, cfg.TestsPerHour) {
		req := plan.Req
		req.Countries = failing
		if again, err := c.probe(jctx, req, m); err == nil {
			for _, p := range again {
				if ok, _ := evaluate(m, p); ok {
					r := byCountry[p.Country]
					r.OK, r.Detail, r.Code, r.LatencyMs = true, "", p.Code, p.LatencyMs
					byCountry[p.Country] = r
				}
			}
		}
	}

	rs := make([]store.RegionResult, 0, len(byCountry))
	nowFailing := map[string]bool{}
	for _, r := range byCountry {
		rs = append(rs, r)
		if !r.OK {
			nowFailing[r.Country] = true
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Country < rs[j].Country })
	c.w.DoFor(m.ID, store.OpReplaceRegionResults(m.ID, rs))

	c.mu.Lock()
	prev := c.failing[m.ID]
	c.failing[m.ID] = nowFailing
	c.results[m.ID] = rs
	h := c.hooks
	c.mu.Unlock()

	if cfg.Alerts {
		c.alertChange(m, h, prev, nowFailing, rs, now)
	}
	return rs
}

func (c *Confirmer) alertChange(m *model.Monitor, h Hooks, prev, now map[string]bool, rs []store.RegionResult, at time.Time) {
	// A monitor that's down here already alerted; a regional notice would
	// only repeat it.
	if h.Status != nil && h.Status(m.ID) == model.StatusDown {
		return
	}
	added := false
	for cc := range now {
		if !prev[cc] {
			added = true
		}
	}
	switch {
	case len(now) > 0 && added:
		c.next.Publish(model.Event{Kind: model.EvRegional, MonitorID: m.ID, At: at, Status: model.StatusDegraded, Reason: FailingSummary(rs)})
	case len(now) == 0 && len(prev) > 0:
		c.next.Publish(model.Event{Kind: model.EvRegionalCleared, MonitorID: m.ID, At: at, Status: model.StatusUp,
			Reason: fmt.Sprintf("reachable again from all %d locations", len(rs))})
	}
}

// FailingSummary is "Failing from 2 of 16: China (Beijing) — timeout;
// Russia (Moscow) — HTTP 403".
func FailingSummary(rs []store.RegionResult) string {
	var parts []string
	for _, r := range rs {
		if r.OK {
			continue
		}
		where := CountryName(r.Country)
		if r.City != "" {
			where += " (" + r.City + ")"
		}
		if r.Detail != "" {
			where += " — " + r.Detail
		}
		parts = append(parts, where)
	}
	return model.TrimReason(fmt.Sprintf("Failing from %d of %d: %s", len(parts), len(rs), strings.Join(parts, "; ")))
}

// CheckMonitor runs a worldwide check of one monitor now (the monitor
// page's "Check worldwide now"), outside the rotation's pacing.
func (c *Confirmer) CheckMonitor(ctx context.Context, id int64) ([]store.RegionResult, string, error) {
	c.mu.Lock()
	h := c.hooks
	c.mu.Unlock()
	if h.Monitor == nil {
		return nil, "", fmt.Errorf("not ready")
	}
	m, ok := h.Monitor(id)
	if !ok {
		return nil, "", fmt.Errorf("not found")
	}
	cfg := c.config()
	plan := PlanFor(m, cfg)
	if plan.Reason != "" {
		return nil, plan.Reason, nil
	}
	c.record(len(plan.Countries))
	return c.sweep(ctx, m, plan, cfg), "", nil
}

// ── Read side (API) ────────────────────────────────────────────────────

// Status is a monitor's worldwide picture for the UI.
type Status struct {
	Eligible  bool                 `json:"eligible"`
	Reason    string               `json:"reason,omitempty"`
	Countries []string             `json:"countries"`
	Results   []store.RegionResult `json:"results"`
	LastAt    int64                `json:"lastAt"`
	NextInS   int64                `json:"nextInS"` // estimate
}

// MonitorStatus returns m's worldwide results and when it's next due.
func (c *Confirmer) MonitorStatus(m *model.Monitor) Status {
	cfg := c.config()
	plan := PlanFor(m, cfg)
	st := Status{Eligible: plan.Reason == "", Reason: plan.Reason, Countries: []string{}, Results: []store.RegionResult{}}
	if plan.Countries != nil {
		st.Countries = plan.Countries
	}
	c.mu.Lock()
	if rs := c.results[m.ID]; rs != nil {
		st.Results = rs
	}
	if t := c.lastSwept[m.ID]; !t.IsZero() {
		st.LastAt = t.UnixMilli()
	}
	c.mu.Unlock()
	if st.Eligible && cfg.Sweep {
		cycle := c.Cycle(cfg)
		next := cycle
		if st.LastAt > 0 {
			next = time.Until(time.UnixMilli(st.LastAt).Add(cycle))
		}
		st.NextInS = int64(max(0, next.Seconds()))
	}
	return st
}

// Cycle estimates how long one full rotation over every eligible
// monitor takes at the configured pace.
func (c *Confirmer) Cycle(cfg Config) time.Duration {
	c.mu.Lock()
	h := c.hooks
	c.mu.Unlock()
	if h.Monitors == nil {
		return 0
	}
	cost := 0
	for _, m := range h.Monitors() {
		if m.Paused {
			continue
		}
		if p := PlanFor(m, cfg); p.Reason == "" {
			cost += len(p.Countries)
		}
	}
	perHour := cfg.TestsPerHour - len(cfg.Countries)
	if perHour < 1 {
		perHour = 1
	}
	return time.Hour * time.Duration(cost) / time.Duration(perHour)
}

// Eligible counts monitors in the rotation.
func (c *Confirmer) Eligible(cfg Config) int {
	c.mu.Lock()
	h := c.hooks
	c.mu.Unlock()
	if h.Monitors == nil {
		return 0
	}
	n := 0
	for _, m := range h.Monitors() {
		if !m.Paused && PlanFor(m, cfg).Reason == "" {
			n++
		}
	}
	return n
}

// Issues returns every monitor currently failing from some countries.
func (c *Confirmer) Issues() map[int64][]store.RegionResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[int64][]store.RegionResult{}
	for id, fail := range c.failing {
		if len(fail) == 0 {
			continue
		}
		for _, r := range c.results[id] {
			if !r.OK {
				out[id] = append(out[id], r)
			}
		}
	}
	return out
}

// Forget drops a deleted monitor's worldwide state.
func (c *Confirmer) Forget(id int64) {
	c.mu.Lock()
	delete(c.results, id)
	delete(c.failing, id)
	delete(c.lastSwept, id)
	c.mu.Unlock()
}

var countryNames = map[string]string{
	"US": "United States", "CA": "Canada", "MX": "Mexico", "BR": "Brazil", "AR": "Argentina", "CL": "Chile", "CO": "Colombia",
	"GB": "United Kingdom", "IE": "Ireland", "DE": "Germany", "FR": "France", "NL": "Netherlands", "ES": "Spain", "IT": "Italy",
	"SE": "Sweden", "PL": "Poland", "CH": "Switzerland", "FI": "Finland", "RU": "Russia", "TR": "Turkey",
	"ZA": "South Africa", "NG": "Nigeria", "KE": "Kenya", "EG": "Egypt",
	"AE": "UAE", "SA": "Saudi Arabia", "IL": "Israel", "IN": "India", "PK": "Pakistan", "BD": "Bangladesh",
	"SG": "Singapore", "MY": "Malaysia", "ID": "Indonesia", "TH": "Thailand", "VN": "Vietnam", "PH": "Philippines",
	"HK": "Hong Kong", "TW": "Taiwan", "CN": "China", "JP": "Japan", "KR": "South Korea", "AU": "Australia", "NZ": "New Zealand",
}

// CountryName returns a country's display name (the code when unknown).
func CountryName(code string) string {
	if n, ok := countryNames[code]; ok {
		return n
	}
	return code
}
