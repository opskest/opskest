package model

import (
	"net"
	"net/url"
	"time"
)

// MonitorType identifies what kind of check a monitor performs.
type MonitorType string

const (
	TypeHTTP      MonitorType = "http"
	TypeTCP       MonitorType = "tcp"
	TypePing      MonitorType = "ping"
	TypeDNS       MonitorType = "dns"
	TypePush      MonitorType = "push"
	TypeWebSocket MonitorType = "websocket"

	// Service monitors: each speaks a little of its protocol and needs no
	// credentials (see internal/check/services.go).
	TypeTLS      MonitorType = "tls"
	TypeSMTP     MonitorType = "smtp"
	TypeSSH      MonitorType = "ssh"
	TypeRedis    MonitorType = "redis"
	TypePostgres MonitorType = "postgres"
	TypeMySQL    MonitorType = "mysql"
	TypeMongoDB  MonitorType = "mongodb"
	TypeMQTT     MonitorType = "mqtt"
	TypeNTP      MonitorType = "ntp"
	TypeUDP      MonitorType = "udp"
	TypeAMQP     MonitorType = "amqp"
	TypeMemcache MonitorType = "memcached"
	TypeBanner   MonitorType = "banner"
	TypeGRPC     MonitorType = "grpc"

	// Database monitors log in and run queries: a custom query, or a health
	// probe for slow queries, deadlocks, lock waits or connection use.
	TypePostgresSQL MonitorType = "postgres_sql"
	TypeMySQLSQL    MonitorType = "mysql_sql"

	// TypeGroup is up when its child monitors are (all, any or at least N);
	// TypeManual has no check: its status is set by hand or through the API.
	// Neither has a target.
	TypeGroup  MonitorType = "group"
	TypeManual MonitorType = "manual"
)

// AllTypes lists every monitor type the server accepts (3rd-party status
// feeds are http monitors with Settings.Vendor, not a type of their own).
var AllTypes = []MonitorType{
	TypeHTTP, TypeTCP, TypePing, TypeDNS, TypePush, TypeWebSocket,
	TypeTLS, TypeSMTP, TypeSSH, TypeRedis, TypePostgres, TypeMySQL, TypeMongoDB, TypeMQTT, TypeNTP, TypeUDP,
	TypeAMQP, TypeMemcache, TypeBanner, TypeGRPC, TypePostgresSQL, TypeMySQLSQL,
	TypeGroup, TypeManual,
}

// Known reports whether t is a type the server can run.
func (t MonitorType) Known() bool {
	for _, k := range AllTypes {
		if k == t {
			return true
		}
	}
	return false
}

// IsSQL reports whether t is a database monitor that runs queries.
func (t MonitorType) IsSQL() bool { return t == TypePostgresSQL || t == TypeMySQLSQL }

// HasNoTarget reports whether t has no target at all (push, group, manual).
func (t MonitorType) HasNoTarget() bool { return t == TypePush || t == TypeGroup || t == TypeManual }

// IsHostPortType reports whether the type's target is "host:port".
func (t MonitorType) IsHostPortType() bool {
	switch t {
	case TypeTCP, TypeSMTP, TypeSSH, TypeRedis, TypePostgres, TypeMySQL, TypeMongoDB, TypeMQTT, TypeUDP,
		TypeAMQP, TypeMemcache, TypeBanner, TypeGRPC, TypePostgresSQL, TypeMySQLSQL:
		return true
	}
	return false
}

// Header is one HTTP header sent with an http or websocket check.
// Assertion is one check on an HTTP response. Source picks what is
// tested: status (code), latency (ms), header (Property = header name),
// body (raw text) or json (Property = gjson path).
type Assertion struct {
	Source   string `json:"source"`
	Property string `json:"property,omitempty"`
	Op       string `json:"op"` // eq ne contains not_contains gt gte lt lte regex exists empty not_empty
	Value    string `json:"value,omitempty"`
}

type Header struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}

// Settings holds every type-specific field for every monitor type. Only
// the fields relevant to Monitor.Type are meaningful; the rest stay zero.
type Settings struct {
	// http
	Method        string   `json:"method,omitempty"`
	Headers       []Header `json:"headers,omitempty"`
	Body          string   `json:"body,omitempty"`
	BodyType      string   `json:"bodyType,omitempty"` // none|raw|json|form
	AuthType      string   `json:"authType,omitempty"` // none|basic|bearer|oauth2
	BasicUser     string   `json:"basicUser,omitempty"`
	AcceptedCodes string   `json:"acceptedCodes,omitempty"`
	MaxRedirects  *int     `json:"maxRedirects,omitempty"`
	IgnoreTLS     bool     `json:"ignoreTls,omitempty"`
	Keyword       string   `json:"keyword,omitempty"`
	KeywordMode   string   `json:"keywordMode,omitempty"` // contains|not_contains|regex
	CaseSensitive bool     `json:"caseSensitive,omitempty"`
	JSONPath      string   `json:"jsonPath,omitempty"`
	JSONOp        string   `json:"jsonOp,omitempty"`
	JSONExpected  string   `json:"jsonExpected,omitempty"`

	CheckDomainExpiry bool `json:"checkDomainExpiry,omitempty"`

	// http: an outbound proxy (http, https or socks5 URL, without credentials)
	// and its optional login (ProxyUser here, the password in Secrets).
	ProxyURL  string `json:"proxyUrl,omitempty"`
	ProxyUser string `json:"proxyUser,omitempty"`
	// http: PEM of extra trusted CA certificates. The client certificate and
	// its key are secrets (Secrets.ClientCert, Secrets.ClientKey).
	CACert string `json:"caCert,omitempty"`
	// http, authType oauth2: the client-credentials grant. The client secret
	// is Secrets.OAuthSecret; OAuthAuth is basic (default) or post.
	OAuthTokenURL string `json:"oauthTokenUrl,omitempty"`
	OAuthClientID string `json:"oauthClientId,omitempty"`
	OAuthScope    string `json:"oauthScope,omitempty"`
	OAuthAudience string `json:"oauthAudience,omitempty"`
	OAuthAuth     string `json:"oauthAuth,omitempty"`

	// CertThresholds are the days-before-expiry at which a certificate alert
	// is sent, once each (empty = the default 14/7/3/1).
	CertThresholds []int `json:"certThresholds,omitempty"`

	// group: the monitors it aggregates and how. GroupMode is all (default:
	// down as soon as one child is down), any (down only when every child is
	// down) or min (up while at least GroupMin children are up).
	Children  []int64 `json:"children,omitempty"`
	GroupMode string  `json:"groupMode,omitempty"`
	GroupMin  int     `json:"groupMin,omitempty"`

	// Vendor marks an http monitor as a 3rd-party status feed: it only
	// reports (and alerts on) incidents the vendor itself publishes, and
	// isn't counted among your own monitors.
	Vendor bool `json:"vendor,omitempty"`

	// Assertions are extra checks on an HTTP response; every one must
	// pass. They run after accepted codes, keyword and JSON query.
	Assertions []Assertion `json:"assertions,omitempty"`

	// RegionMode picks where worldwide checks run from: "" = Worldwide
	// (the configured global countries), "local" = only this server
	// (the legacy persisted value "india" has the same meaning),
	// "custom" = RegionCountries.
	RegionMode      string   `json:"regionMode,omitempty"`
	RegionCountries []string `json:"regionCountries,omitempty"`

	// DependsOn lists up to MaxParents parent monitors. While any of them
	// is down (or in maintenance) this monitor's outages are recorded but
	// their alerts are held; see engine.openOutage.
	DependsOn []int64 `json:"dependsOn,omitempty"`

	// LatencyAlert, when set, alerts when a response-time percentile over
	// a recent window exceeds an absolute limit and/or a multiple of the
	// monitor's usual value (internal/latalert).
	LatencyAlert *LatencyAlert `json:"latencyAlert,omitempty"`

	// tcp, redis, mongodb, mqtt
	TLS bool `json:"tls,omitempty"`

	// smtp: none | starttls | tls (implicit TLS).
	Security string `json:"security,omitempty"`

	// ntp: fail when this host's clock is further off than this (0 = off).
	MaxOffsetMs int `json:"maxOffsetMs,omitempty"`

	// grpc: the service name asked of grpc.health.v1 ("" = the server overall).
	GRPCService string `json:"grpcService,omitempty"`

	// postgres_sql, mysql_sql. The login is BasicUser plus Secrets.BasicPass;
	// TLS and IgnoreTLS secure the connection.
	Database string `json:"database,omitempty"`
	// Probe is query (run Query), deadlocks (new deadlocks since the last
	// check), long_running (queries running longer than ThresholdSecs),
	// blocked (sessions waiting on a lock) or connections (percent of the
	// connection limit in use).
	Probe string `json:"probe,omitempty"`
	// Query is a single read-only statement. QueryOp and QueryExpected compare
	// the first column of the first row; with no QueryOp it only has to run.
	Query         string `json:"query,omitempty"`
	QueryOp       string `json:"queryOp,omitempty"`
	QueryExpected string `json:"queryExpected,omitempty"`
	// Threshold is how many (deadlocks, long-running queries, blocked
	// sessions) or what percent (connections) is tolerated; above it is down.
	Threshold     int `json:"threshold,omitempty"`
	ThresholdSecs int `json:"thresholdSecs,omitempty"`

	// ping
	Count      int `json:"count,omitempty"`
	MaxLossPct int `json:"maxLossPct,omitempty"`

	// dns
	Record   string `json:"record,omitempty"`
	Resolver string `json:"resolver,omitempty"`
	Expected string `json:"expected,omitempty"` // legacy single value; see DNSExpected
	// ExpectedValues is the set of answers the record should hold, e.g.
	// every IP behind an ALB. Supersedes Expected.
	ExpectedValues []string `json:"expectedValues,omitempty"`
	Match          string   `json:"match,omitempty"` // equals|contains|all|exact

	// push
	GraceS int `json:"graceS,omitempty"`

	// websocket, udp, tcp
	Send   string `json:"send,omitempty"`
	Expect string `json:"expect,omitempty"`
	// ExpectMode (tcp): contains (default) or regex.
	ExpectMode string `json:"expectMode,omitempty"`
}

func (s Settings) clone() Settings {
	c := s
	if s.Headers != nil {
		c.Headers = append([]Header(nil), s.Headers...)
	}
	if s.MaxRedirects != nil {
		v := *s.MaxRedirects
		c.MaxRedirects = &v
	}
	if s.ExpectedValues != nil {
		c.ExpectedValues = append([]string(nil), s.ExpectedValues...)
	}
	if s.CertThresholds != nil {
		c.CertThresholds = append([]int(nil), s.CertThresholds...)
	}
	if s.Children != nil {
		c.Children = append([]int64(nil), s.Children...)
	}
	if s.DependsOn != nil {
		c.DependsOn = append([]int64(nil), s.DependsOn...)
	}
	return c
}

// DNSExpected returns the values a dns monitor's answers are matched
// against: ExpectedValues, or the legacy single Expected for monitors
// saved before the list existed.
func (s Settings) DNSExpected() []string {
	if len(s.ExpectedValues) > 0 {
		return s.ExpectedValues
	}
	if s.Expected != "" {
		return []string{s.Expected}
	}
	return nil
}

// Secrets holds the values that must never be logged or shown back to the
// UI in plaintext. They are stored encrypted via secret.Box.
type Secrets struct {
	Headers     map[string]string `json:"headers,omitempty"` // secret header name -> value
	BasicPass   string            `json:"basicPass,omitempty"`
	BearerToken string            `json:"bearerToken,omitempty"`
	// ProxyPass is the proxy login's password; ClientCert and ClientKey are
	// the PEM of an mTLS client certificate and its private key; OAuthSecret
	// is the OAuth2 client secret.
	ProxyPass   string `json:"proxyPass,omitempty"`
	ClientCert  string `json:"clientCert,omitempty"`
	ClientKey   string `json:"clientKey,omitempty"`
	OAuthSecret string `json:"oauthSecret,omitempty"`
}

// IsEmpty reports whether s holds nothing worth encrypting.
func (s Secrets) IsEmpty() bool {
	return len(s.Headers) == 0 && s.BasicPass == "" && s.BearerToken == "" &&
		s.ProxyPass == "" && s.ClientCert == "" && s.ClientKey == "" && s.OAuthSecret == ""
}

func (s Secrets) clone() Secrets {
	c := s
	if s.Headers != nil {
		c.Headers = make(map[string]string, len(s.Headers))
		for k, v := range s.Headers {
			c.Headers[k] = v
		}
	}
	return c
}

// Monitor is the full definition of one thing being monitored.
type Monitor struct {
	ID      int64
	Name    string
	Type    MonitorType
	Target  string
	GroupID *int64

	IntervalS      int
	RetryIntervalS int
	Retries        int
	TimeoutMs      int
	DegradedMs     int
	SSLWarnDays    int
	ReminderMin    int

	UpsideDown     bool
	NotifyDegraded bool
	Paused         bool

	Description string
	Runbook     string

	Settings  Settings
	Secrets   Secrets
	PushToken string

	TagIDs     []int64
	ChannelIDs []int64

	CreatedBy *int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewMonitor returns a monitor of the given type with every Global
// Constraints default pre-filled, including the ones ApplyDefaults
// deliberately leaves alone (Retries, SSLWarnDays, and, for push,
// Settings.GraceS). Name and Target are left empty for the caller to
// fill in. Use this to construct a monitor from scratch (e.g. as the
// decode target for a create request) so that fields the caller omits
// get defaults while fields the caller sets to an explicit 0 survive.
func NewMonitor(t MonitorType) *Monitor {
	m := &Monitor{Type: t}
	m.Retries = 2
	m.SSLWarnDays = 14
	if t == TypePush {
		m.Settings.GraceS = 30
	}
	m.ApplyDefaults()
	return m
}

// ApplyDefaults fills only the fields whose zero value is invalid:
// IntervalS, RetryIntervalS, TimeoutMs, and, per Monitor.Type,
// Settings.Method, Settings.BodyType, Settings.AuthType,
// Settings.AcceptedCodes, Settings.MaxRedirects (only when nil),
// Settings.KeywordMode (only when Keyword is set), Settings.Match (only
// when expected values are set: exact for several, equals for one),
// Settings.Count and Settings.MaxLossPct. For dns it also folds the
// legacy Settings.Expected into Settings.ExpectedValues.
//
// It deliberately never touches Retries, SSLWarnDays, Settings.GraceS,
// DegradedMs or ReminderMin: 0 is a meaningful, valid value for each of
// these (e.g. Retries=0 means "go DOWN on first failure", GraceS=0 means
// no grace, SSLWarnDays=0/DegradedMs=0/ReminderMin=0 mean "off"), so
// defaulting them here would make those configurations unreachable.
//
// It is idempotent. Use NewMonitor to build a monitor with every
// default, including those, pre-filled from scratch.
func (m *Monitor) ApplyDefaults() {
	if m.IntervalS == 0 {
		m.IntervalS = 60
	}
	if m.RetryIntervalS == 0 {
		m.RetryIntervalS = 20
	}
	if m.TimeoutMs == 0 {
		m.TimeoutMs = 10000
	}

	switch m.Type {
	case TypeHTTP:
		if m.Settings.Method == "" {
			m.Settings.Method = "GET"
		}
		if m.Settings.BodyType == "" {
			m.Settings.BodyType = "none"
		}
		if m.Settings.AuthType == "" {
			m.Settings.AuthType = "none"
		}
		if m.Settings.AcceptedCodes == "" {
			m.Settings.AcceptedCodes = "200-299"
		}
		if m.Settings.MaxRedirects == nil {
			d := 10
			m.Settings.MaxRedirects = &d
		}
		if m.Settings.Keyword != "" && m.Settings.KeywordMode == "" {
			m.Settings.KeywordMode = "contains"
		}
		if m.Settings.AuthType == "oauth2" && m.Settings.OAuthAuth == "" {
			m.Settings.OAuthAuth = "basic"
		}
	case TypeTCP:
		if m.Settings.Expect != "" && m.Settings.ExpectMode == "" {
			m.Settings.ExpectMode = "contains"
		}
	case TypeGroup:
		if m.Settings.GroupMode == "" {
			m.Settings.GroupMode = "all"
		}
	case TypeDNS:
		// Fold the legacy single value into the list.
		if len(m.Settings.ExpectedValues) == 0 && m.Settings.Expected != "" {
			m.Settings.ExpectedValues = []string{m.Settings.Expected}
		}
		m.Settings.Expected = ""
		if m.Settings.Match == "" {
			switch n := len(m.Settings.ExpectedValues); {
			case n > 1:
				m.Settings.Match = "exact"
			case n == 1:
				m.Settings.Match = "equals"
			}
		}
	case TypePostgresSQL, TypeMySQLSQL:
		if m.Settings.Probe == "" {
			m.Settings.Probe = "query"
		}
		if m.Settings.Probe == "long_running" && m.Settings.ThresholdSecs == 0 {
			m.Settings.ThresholdSecs = 30
		}
		if m.Settings.Probe == "connections" && m.Settings.Threshold == 0 {
			m.Settings.Threshold = 80
		}
	case TypePing:
		if m.Settings.Count == 0 {
			m.Settings.Count = 3
		}
		if m.Settings.MaxLossPct == 0 {
			m.Settings.MaxLossPct = 34
		}
	}
}

// Host returns the host part of the target, used for clustering monitors
// that point at the same place: the URL host with no port for http and
// websocket, the tcp host, the ping host/IP, and the dns name. Push
// monitors have no target, so it returns "".
func (m *Monitor) Host() string {
	switch m.Type {
	case TypeHTTP, TypeWebSocket:
		u, err := url.Parse(m.Target)
		if err != nil {
			return ""
		}
		return u.Hostname()
	case TypePing, TypeDNS:
		return m.Target
	case TypeTLS, TypeNTP:
		if host, _, err := net.SplitHostPort(m.Target); err == nil {
			return host
		}
		return m.Target
	case TypePush:
		return ""
	default:
		if m.Type.IsHostPortType() {
			host, _, err := net.SplitHostPort(m.Target)
			if err != nil {
				return ""
			}
			return host
		}
		return ""
	}
}

// Clone deep-copies the monitor: slices, maps and pointers are all
// duplicated, so mutating the clone never touches the original.
func (m *Monitor) Clone() *Monitor {
	c := *m
	if m.GroupID != nil {
		g := *m.GroupID
		c.GroupID = &g
	}
	if m.CreatedBy != nil {
		cb := *m.CreatedBy
		c.CreatedBy = &cb
	}
	if m.TagIDs != nil {
		c.TagIDs = append([]int64(nil), m.TagIDs...)
	}
	if m.ChannelIDs != nil {
		c.ChannelIDs = append([]int64(nil), m.ChannelIDs...)
	}
	c.Settings = m.Settings.clone()
	c.Secrets = m.Secrets.clone()
	return &c
}

// MaxParents bounds Settings.DependsOn.
const MaxParents = 5

// LatencyAlert configures a response-time alert. At least one of
// ThresholdMs and Factor is set; the effective limit is the lower of the
// two when both are.
type LatencyAlert struct {
	Percentile  int     `json:"percentile"`  // 50, 90, 95 or 99
	ThresholdMs int     `json:"thresholdMs"` // 0 = no absolute limit
	Factor      float64 `json:"factor"`      // 0 = no relative limit; else × the usual value
	WindowMin   int     `json:"windowMin"`   // evaluation window, minutes
}
