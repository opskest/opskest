package model

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// FieldErrors maps a camelCase JSON field path, e.g. "target" or
// "settings.jsonOp", to a human-readable validation message.
type FieldErrors map[string]string

// Err returns nil when there are no errors, or a single error summarizing
// every field error otherwise.
func (f FieldErrors) Err() error {
	if len(f) == 0 {
		return nil
	}
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(f[k])
	}
	return errors.New(b.String())
}

var headerNameRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`)

const maxBodyBytes = 64 * 1024
const maxHeaderValueBytes = 8 * 1024
const maxHeaders = 30

var validMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true,
	"PATCH": true, "DELETE": true, "OPTIONS": true,
}

var validBodyTypes = map[string]bool{"none": true, "raw": true, "json": true, "form": true}
var validAuthTypes = map[string]bool{"none": true, "basic": true, "bearer": true, "oauth2": true}
var validKeywordModes = map[string]bool{"contains": true, "not_contains": true, "regex": true}
var validJSONOps = map[string]bool{"==": true, "!=": true, "contains": true, ">": true, "<": true, "exists": true}
var validDNSRecords = map[string]bool{"A": true, "AAAA": true, "CNAME": true, "MX": true, "TXT": true, "NS": true}
var validDNSMatch = map[string]bool{"equals": true, "contains": true, "all": true, "exact": true}

// maxDNSExpected caps a dns monitor's expected list; an ALB has at most a
// handful of addresses per family.
const maxDNSExpected = 32

// Validate checks the monitor against the monitor rules and
// returns every field-keyed error found. An empty result means the
// monitor is valid.
func (m *Monitor) Validate() FieldErrors {
	fe := FieldErrors{}

	nameLen := utf8.RuneCountInString(m.Name)
	if nameLen < 1 || nameLen > 120 {
		fe["name"] = "must be 1-120 characters"
	}
	if utf8.RuneCountInString(m.Description) > 1000 {
		fe["description"] = "must be at most 1000 characters"
	}
	if utf8.RuneCountInString(m.Runbook) > 1000 {
		fe["runbook"] = "must be at most 1000 characters"
	}
	if m.ReminderMin < 0 || m.ReminderMin > 1440 {
		fe["reminderMin"] = "must be 0-1440"
	}
	if m.DegradedMs != 0 && (m.DegradedMs < 1 || m.DegradedMs > 120000) {
		fe["degradedMs"] = "must be 0 or 1-120000"
	}
	if m.SSLWarnDays < 0 || m.SSLWarnDays > 90 {
		fe["sslWarnDays"] = "must be 0-90"
	}
	if m.Retries < 0 || m.Retries > 10 {
		fe["retries"] = "must be 0-10"
	}
	if m.RetryIntervalS < 10 || m.RetryIntervalS > 86400 {
		fe["retryIntervalS"] = "must be 10-86400"
	}
	if m.IntervalS < 10 || m.IntervalS > 86400 {
		fe["intervalS"] = "must be 10-86400"
	}
	if m.TimeoutMs < 1000 || m.TimeoutMs > m.IntervalS*1000-500 {
		fe["timeoutMs"] = "must be 1000 to intervalS*1000-500"
	}

	switch m.Type {
	case TypeHTTP:
		m.validateHTTP(fe)
	case TypeTCP, TypeSMTP, TypeSSH, TypeRedis, TypePostgres, TypeMySQL, TypeMongoDB, TypeMQTT,
		TypeAMQP, TypeMemcache, TypeBanner, TypeGRPC:
		m.validateTCP(fe)
		m.validateService(fe)
	case TypePostgresSQL, TypeMySQLSQL:
		m.validateTCP(fe)
		m.validateSQL(fe)
	case TypeTLS, TypeNTP:
		m.validateHostOptionalPort(fe)
		m.validateService(fe)
	case TypeUDP:
		m.validateTCP(fe)
		m.validateService(fe)
	case TypePing:
		m.validatePing(fe)
	case TypeDNS:
		m.validateDNS(fe)
	case TypePush:
		m.validatePush(fe)
	case TypeGroup:
		m.validateGroup(fe)
	case TypeManual:
		m.validateManual(fe)
	case TypeWebSocket:
		m.validateWebSocket(fe)
	default:
		fe["type"] = "unknown monitor type"
	}
	m.validateDependsOn(fe)
	m.validateLatencyAlert(fe)
	m.validateCertThresholds(fe)

	switch m.Settings.RegionMode {
	case "", "local", "india":
	case "custom":
		if n := len(m.Settings.RegionCountries); n < 1 || n > 25 {
			fe["settings.regionCountries"] = "pick 1 to 25 countries"
		}
		for _, c := range m.Settings.RegionCountries {
			if len(c) != 2 || strings.ToUpper(c) != c {
				fe["settings.regionCountries"] = "must be 2-letter country codes"
			}
		}
	default:
		fe["settings.regionMode"] = "must be worldwide, local or custom"
	}

	return fe
}

func (m *Monitor) validateHTTP(fe FieldErrors) {
	u, err := url.Parse(m.Target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		fe["target"] = "must be an absolute http(s) URL with a host"
	}
	if !validMethods[m.Settings.Method] {
		fe["settings.method"] = "invalid method"
	}
	if !validBodyTypes[m.Settings.BodyType] {
		fe["settings.bodyType"] = "must be none, raw, json or form"
	}
	if len(m.Settings.Body) > maxBodyBytes {
		fe["settings.body"] = "must be at most 64 KiB"
	}
	if !validAuthTypes[m.Settings.AuthType] {
		fe["settings.authType"] = "must be none, basic, bearer or oauth2"
	}
	m.validateHTTPTransport(fe)
	if m.Settings.AuthType == "oauth2" {
		m.validateOAuth(fe)
	}
	if _, err := ParseCodes(m.Settings.AcceptedCodes); err != nil {
		fe["settings.acceptedCodes"] = err.Error()
	}
	if m.Settings.MaxRedirects != nil {
		v := *m.Settings.MaxRedirects
		if v < 0 || v > 20 {
			fe["settings.maxRedirects"] = "must be 0-20"
		}
	}
	if m.Settings.KeywordMode != "" && !validKeywordModes[m.Settings.KeywordMode] {
		fe["settings.keywordMode"] = "must be contains, not_contains or regex"
	}
	if m.Settings.KeywordMode == "regex" && m.Settings.Keyword != "" {
		if _, err := regexp.Compile(m.Settings.Keyword); err != nil {
			fe["settings.keyword"] = "invalid regex: " + err.Error()
		}
	}
	validateAssertions(m.Settings.Assertions, fe)
	if m.Settings.JSONPath != "" && !validJSONOps[m.Settings.JSONOp] {
		fe["settings.jsonOp"] = "required, one of == != contains > < exists"
	}
	if len(m.Settings.Headers) > maxHeaders {
		fe["settings.headers"] = "at most 30 headers"
	}
	for i, h := range m.Settings.Headers {
		key := fmt.Sprintf("headers.%d", i)
		if !headerNameRe.MatchString(h.Name) {
			fe[key+".name"] = "invalid header name"
			fe["settings.headers"] = "invalid header name: " + h.Name
		}
		if len(h.Value) > maxHeaderValueBytes {
			fe[key+".value"] = "header value too long (max 8 KiB)"
			fe["settings.headers"] = "header value too long: " + h.Name
		}
	}
}

func (m *Monitor) validateTCP(fe FieldErrors) {
	host, port, err := net.SplitHostPort(m.Target)
	if err != nil || host == "" {
		fe["target"] = "must be host:port"
		return
	}
	p, perr := strconv.Atoi(port)
	if perr != nil || p < 1 || p > 65535 {
		fe["target"] = "port must be 1-65535"
	}
}

// validateHostOptionalPort accepts "host" or "host:port" (the default port
// belongs to the type: 443 for tls, 123 for ntp).
func (m *Monitor) validateHostOptionalPort(fe FieldErrors) {
	t := strings.TrimSpace(m.Target)
	if t == "" || strings.ContainsAny(t, " /\\") {
		fe["target"] = "must be a host or host:port"
		return
	}
	if host, port, err := net.SplitHostPort(t); err == nil {
		p, perr := strconv.Atoi(port)
		if host == "" || perr != nil || p < 1 || p > 65535 {
			fe["target"] = "port must be 1-65535"
		}
	}
}

var validSMTPSecurity = map[string]bool{"": true, "none": true, "starttls": true, "tls": true}

// validateService checks the settings only the service monitors use.
func (m *Monitor) validateService(fe FieldErrors) {
	s := m.Settings
	if m.Type == TypeTCP {
		m.validateTCPExpect(fe)
	}
	if m.Type == TypeSMTP && !validSMTPSecurity[s.Security] {
		fe["settings.security"] = "must be none, starttls or tls"
	}
	if m.Type == TypeNTP && (s.MaxOffsetMs < 0 || s.MaxOffsetMs > 86_400_000) {
		fe["settings.maxOffsetMs"] = "must be 0-86400000"
	}
	if m.Type == TypeUDP {
		if strings.TrimSpace(s.Expect) == "" {
			fe["settings.expect"] = "required: UDP has no handshake, so a matching reply is the only proof of life"
		}
		for _, k := range []struct{ key, v string }{{"settings.send", s.Send}, {"settings.expect", s.Expect}} {
			if rest, ok := strings.CutPrefix(k.v, "hex:"); ok {
				if _, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(rest), " ", "")); err != nil {
					fe[k.key] = "hex: must be followed by valid hex bytes"
				}
			}
		}
		if len(s.Send) > 1024 || len(s.Expect) > 1024 {
			fe["settings.send"] = "at most 1024 characters"
		}
	}
	if m.Type == TypeBanner && strings.TrimSpace(s.Expect) == "" {
		fe["settings.expect"] = "required: the text the server's first line should contain"
	}
	if m.Type == TypeBanner && len(s.Expect) > 256 {
		fe["settings.expect"] = "at most 256 characters"
	}
	if m.Type == TypeGRPC && len(s.GRPCService) > 256 {
		fe["settings.grpcService"] = "at most 256 characters"
	}
	if (m.Type == TypeRedis || m.Type == TypeMQTT) && len(s.BasicUser) > 128 {
		fe["settings.basicUser"] = "at most 128 characters"
	}
}

var validSQLProbes = map[string]bool{"query": true, "deadlocks": true, "long_running": true, "blocked": true, "connections": true}
var validQueryOps = map[string]bool{"": true, "eq": true, "ne": true, "gt": true, "gte": true, "lt": true, "lte": true, "contains": true}

// MaxQueryLen bounds a monitor's custom query.
const MaxQueryLen = 4000

// readOnlyStart are the statements a monitor query may begin with. The
// connection is also read-only, so this is a second line of defence and a
// clearer error, not the only one.
var readOnlyStart = regexp.MustCompile(`(?is)^\s*(select|with|show|explain|values|table)\b`)

// QueryProblem reports why q can't be a monitor query, or "" when it can: a
// single read-only statement.
func QueryProblem(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return "required"
	}
	if len(q) > MaxQueryLen {
		return fmt.Sprintf("at most %d characters", MaxQueryLen)
	}
	if !readOnlyStart.MatchString(q) {
		return "must start with SELECT, WITH, SHOW, EXPLAIN, VALUES or TABLE"
	}
	if strings.Contains(strings.TrimSuffix(q, ";"), ";") {
		return "one statement only"
	}
	// MySQL's SELECT ... INTO OUTFILE/DUMPFILE writes a file on the database
	// server and is allowed inside a READ ONLY transaction, so the connection
	// alone doesn't stop it. Matching the keyword anywhere (comments included)
	// also catches the /*!50000 INTO OUTFILE ... */ form MySQL executes.
	if fileOutput.MatchString(q) {
		return "INTO OUTFILE / DUMPFILE is not allowed"
	}
	return ""
}

var fileOutput = regexp.MustCompile(`(?i)\b(outfile|dumpfile)\b`)

// validateSQL checks the database monitors (postgres_sql, mysql_sql).
func (m *Monitor) validateSQL(fe FieldErrors) {
	s := m.Settings
	if strings.TrimSpace(s.BasicUser) == "" {
		fe["settings.basicUser"] = "required"
	}
	if len(s.BasicUser) > 128 {
		fe["settings.basicUser"] = "at most 128 characters"
	}
	if len(s.Database) > 128 {
		fe["settings.database"] = "at most 128 characters"
	}
	if !validSQLProbes[s.Probe] {
		fe["settings.probe"] = "must be query, deadlocks, long_running, blocked or connections"
		return
	}
	if s.Threshold < 0 || s.Threshold > 1_000_000 {
		fe["settings.threshold"] = "must be 0-1000000"
	}
	switch s.Probe {
	case "query":
		if p := QueryProblem(s.Query); p != "" {
			fe["settings.query"] = p
		}
		if !validQueryOps[s.QueryOp] {
			fe["settings.queryOp"] = "must be eq, ne, gt, gte, lt, lte or contains"
		}
		if s.QueryOp != "" && s.QueryOp != "contains" && s.QueryOp != "eq" && s.QueryOp != "ne" {
			if _, err := strconv.ParseFloat(strings.TrimSpace(s.QueryExpected), 64); err != nil {
				fe["settings.queryExpected"] = "must be a number"
			}
		}
		if s.QueryOp != "" && s.QueryExpected == "" {
			fe["settings.queryExpected"] = "required"
		}
		if len(s.QueryExpected) > 512 {
			fe["settings.queryExpected"] = "at most 512 characters"
		}
	case "long_running":
		if s.ThresholdSecs < 1 || s.ThresholdSecs > 86400 {
			fe["settings.thresholdSecs"] = "must be 1-86400"
		}
	case "connections":
		if s.Threshold < 1 || s.Threshold > 100 {
			fe["settings.threshold"] = "must be 1-100 (percent)"
		}
	}
}

func (m *Monitor) validatePing(fe FieldErrors) {
	if strings.TrimSpace(m.Target) == "" {
		fe["target"] = "host or IP required"
	}
	if m.Settings.Count < 1 || m.Settings.Count > 10 {
		fe["settings.count"] = "must be 1-10"
	}
	if m.Settings.MaxLossPct < 1 || m.Settings.MaxLossPct > 100 {
		fe["settings.maxLossPct"] = "must be 1-100"
	}
}

func (m *Monitor) validateDNS(fe FieldErrors) {
	if strings.TrimSpace(m.Target) == "" {
		fe["target"] = "hostname required"
	}
	if !validDNSRecords[m.Settings.Record] {
		fe["settings.record"] = "must be one of A AAAA CNAME MX TXT NS"
	}
	if m.Settings.Resolver != "" {
		host := m.Settings.Resolver
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if net.ParseIP(host) == nil {
			fe["settings.resolver"] = "must be ip[:port]"
		}
	}
	if m.Settings.Match != "" && !validDNSMatch[m.Settings.Match] {
		fe["settings.match"] = "must be equals, contains, all or exact"
	}
	values := m.Settings.DNSExpected()
	if len(values) > maxDNSExpected {
		fe["settings.expectedValues"] = fmt.Sprintf("at most %d values", maxDNSExpected)
		return
	}
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			fe["settings.expectedValues"] = "values must not be empty"
			return
		}
		if m.Settings.Match == "contains" {
			continue
		}
		switch m.Settings.Record {
		case "A":
			if ip := net.ParseIP(v); ip == nil || ip.To4() == nil {
				fe["settings.expectedValues"] = v + " is not an IPv4 address"
				return
			}
		case "AAAA":
			if ip := net.ParseIP(v); ip == nil || ip.To4() != nil {
				fe["settings.expectedValues"] = v + " is not an IPv6 address"
				return
			}
		}
	}
}

// validateTCPExpect checks the optional send and expect of a tcp monitor:
// text or hex: bytes to send, and text or a regular expression the reply
// must contain.
func (m *Monitor) validateTCPExpect(fe FieldErrors) {
	s := m.Settings
	if len(s.Send) > 1024 {
		fe["settings.send"] = "at most 1024 characters"
	}
	if rest, ok := strings.CutPrefix(s.Send, "hex:"); ok {
		if _, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(rest), " ", "")); err != nil {
			fe["settings.send"] = "hex: must be followed by valid hex bytes"
		}
	}
	if len(s.Expect) > 512 {
		fe["settings.expect"] = "at most 512 characters"
	}
	switch s.ExpectMode {
	case "", "contains":
		if rest, ok := strings.CutPrefix(s.Expect, "hex:"); ok {
			if _, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(rest), " ", "")); err != nil {
				fe["settings.expect"] = "hex: must be followed by valid hex bytes"
			}
		}
	case "regex":
		if s.Expect == "" {
			fe["settings.expect"] = "required for a regular expression"
		} else if _, err := regexp.Compile(s.Expect); err != nil {
			fe["settings.expect"] = "invalid regex: " + err.Error()
		}
	default:
		fe["settings.expectMode"] = "must be contains or regex"
	}
}

// MaxGroupChildren bounds a group monitor's children.
const MaxGroupChildren = 100

var validGroupModes = map[string]bool{"all": true, "any": true, "min": true}

// validateGroup checks a group monitor's local rules; existence of the
// children and loops need every monitor and are checked by the API
// (engine.ValidateGroup).
func (m *Monitor) validateGroup(fe FieldErrors) {
	if m.Target != "" {
		fe["target"] = "must be empty"
	}
	s := m.Settings
	if !validGroupModes[s.GroupMode] {
		fe["settings.groupMode"] = "must be all, any or min"
	}
	if n := len(s.Children); n < 1 || n > MaxGroupChildren {
		fe["settings.children"] = fmt.Sprintf("pick 1 to %d monitors", MaxGroupChildren)
	}
	seen := map[int64]bool{}
	for _, id := range s.Children {
		if id <= 0 || (m.ID != 0 && id == m.ID) {
			fe["settings.children"] = "a group can't contain itself"
			break
		}
		if seen[id] {
			fe["settings.children"] = "each monitor can be listed once"
			break
		}
		seen[id] = true
	}
	if s.GroupMode == "min" && (s.GroupMin < 1 || s.GroupMin > max(len(s.Children), 1)) {
		fe["settings.groupMin"] = "must be 1 to the number of children"
	}
	if m.UpsideDown {
		fe["upsideDown"] = "does not apply to a group"
	}
}

// validateManual checks a manual monitor: no target, no inversion.
func (m *Monitor) validateManual(fe FieldErrors) {
	if m.Target != "" {
		fe["target"] = "must be empty"
	}
	if m.UpsideDown {
		fe["upsideDown"] = "does not apply to a manual monitor"
	}
}

// validateCertThresholds checks the per-monitor certificate alert days.
func (m *Monitor) validateCertThresholds(fe FieldErrors) {
	t := m.Settings.CertThresholds
	if len(t) == 0 {
		return
	}
	if len(t) > 8 {
		fe["settings.certThresholds"] = "at most 8 thresholds"
		return
	}
	seen := map[int]bool{}
	for _, d := range t {
		if d < 1 || d > 365 {
			fe["settings.certThresholds"] = "each must be 1-365 days"
			return
		}
		if seen[d] {
			fe["settings.certThresholds"] = "each threshold can be listed once"
			return
		}
		seen[d] = true
	}
}

const maxPEMBytes = 64 * 1024

// validateHTTPTransport checks the proxy, extra CA and client certificate.
func (m *Monitor) validateHTTPTransport(fe FieldErrors) {
	s, sec := m.Settings, m.Secrets
	if s.ProxyURL == "" {
		if s.ProxyUser != "" || sec.ProxyPass != "" {
			fe["settings.proxyUrl"] = "set a proxy URL to use a proxy login"
		}
	} else {
		u, err := url.Parse(s.ProxyURL)
		switch {
		case err != nil || u.Host == "" || u.Hostname() == "":
			fe["settings.proxyUrl"] = "must be http://, https://, socks5:// or socks5h:// host:port"
		case u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h":
			fe["settings.proxyUrl"] = "scheme must be http, https, socks5 or socks5h"
		case u.User != nil:
			fe["settings.proxyUrl"] = "leave the login out of the URL; use the proxy user and password fields"
		case (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "":
			fe["settings.proxyUrl"] = "must be just scheme, host and port"
		case len(s.ProxyURL) > 512:
			fe["settings.proxyUrl"] = "at most 512 characters"
		}
		if len(s.ProxyUser) > 256 {
			fe["settings.proxyUser"] = "at most 256 characters"
		}
	}
	if s.CACert != "" {
		if len(s.CACert) > maxPEMBytes {
			fe["settings.caCert"] = "at most 64 KiB"
		} else if !x509.NewCertPool().AppendCertsFromPEM([]byte(s.CACert)) {
			fe["settings.caCert"] = "no PEM certificate found"
		}
	}
	if (sec.ClientCert == "") != (sec.ClientKey == "") {
		fe["secrets.clientCert"] = "set both the client certificate and its private key"
	} else if sec.ClientCert != "" {
		if len(sec.ClientCert) > maxPEMBytes || len(sec.ClientKey) > maxPEMBytes {
			fe["secrets.clientCert"] = "at most 64 KiB each"
		} else if _, err := tls.X509KeyPair([]byte(sec.ClientCert), []byte(sec.ClientKey)); err != nil {
			fe["secrets.clientCert"] = "certificate and private key are not a valid pair"
		}
	}
}

// validateOAuth checks the client-credentials settings of authType oauth2.
func (m *Monitor) validateOAuth(fe FieldErrors) {
	s := m.Settings
	u, err := url.Parse(s.OAuthTokenURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		fe["settings.oauthTokenUrl"] = "must be an absolute http(s) URL with a host and no login"
	} else if len(s.OAuthTokenURL) > 1024 {
		fe["settings.oauthTokenUrl"] = "at most 1024 characters"
	}
	if strings.TrimSpace(s.OAuthClientID) == "" {
		fe["settings.oauthClientId"] = "required"
	} else if len(s.OAuthClientID) > 512 {
		fe["settings.oauthClientId"] = "at most 512 characters"
	}
	if m.Secrets.OAuthSecret == "" {
		fe["secrets.oauthSecret"] = "required"
	} else if len(m.Secrets.OAuthSecret) > 2048 {
		fe["secrets.oauthSecret"] = "at most 2048 characters"
	}
	if len(s.OAuthScope) > 512 {
		fe["settings.oauthScope"] = "at most 512 characters"
	}
	if len(s.OAuthAudience) > 512 {
		fe["settings.oauthAudience"] = "at most 512 characters"
	}
	if s.OAuthAuth != "" && s.OAuthAuth != "basic" && s.OAuthAuth != "post" {
		fe["settings.oauthAuth"] = "must be basic or post"
	}
}

func (m *Monitor) validatePush(fe FieldErrors) {
	if m.Target != "" {
		fe["target"] = "must be empty"
	}
	if m.Settings.GraceS < 0 || m.Settings.GraceS > 3600 {
		fe["settings.graceS"] = "must be 0-3600"
	}
}

func (m *Monitor) validateWebSocket(fe FieldErrors) {
	u, err := url.Parse(m.Target)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		fe["target"] = "must be a ws(s) URL"
	}
}

// validateDependsOn checks the local rules for parents (count, self,
// duplicates); existence and cycles need every monitor and are checked by
// the API (engine.ValidateDependencies).
func (m *Monitor) validateDependsOn(fe FieldErrors) {
	deps := m.Settings.DependsOn
	if len(deps) > MaxParents {
		fe["settings.dependsOn"] = "at most 5 parent monitors"
		return
	}
	seen := map[int64]bool{}
	for _, id := range deps {
		if id <= 0 || (m.ID != 0 && id == m.ID) {
			fe["settings.dependsOn"] = "a monitor can't depend on itself"
			return
		}
		if seen[id] {
			fe["settings.dependsOn"] = "each parent can be listed once"
			return
		}
		seen[id] = true
	}
}

// validateLatencyAlert checks a response-time alert, filling defaults
// (p95, 15 min) for zero values.
func (m *Monitor) validateLatencyAlert(fe FieldErrors) {
	la := m.Settings.LatencyAlert
	if la == nil {
		return
	}
	if la.Percentile == 0 {
		la.Percentile = 95
	}
	if la.WindowMin == 0 {
		la.WindowMin = 15
	}
	switch la.Percentile {
	case 50, 90, 95, 99:
	default:
		fe["settings.latencyAlert.percentile"] = "must be 50, 90, 95 or 99"
	}
	if la.WindowMin < 5 || la.WindowMin > 60 {
		fe["settings.latencyAlert.windowMin"] = "must be 5-60 minutes"
	}
	if la.ThresholdMs < 0 || la.ThresholdMs > 120000 {
		fe["settings.latencyAlert.thresholdMs"] = "must be 0-120000"
	}
	if la.Factor != 0 && (la.Factor < 1.5 || la.Factor > 20) {
		fe["settings.latencyAlert.factor"] = "must be 0 or 1.5-20"
	}
	// The evaluator reads the in-memory ring of the last 100 checks, so the
	// window can't be longer than 100 intervals.
	if m.IntervalS > 0 && la.WindowMin*60 > 100*m.IntervalS {
		fe["settings.latencyAlert.windowMin"] = fmt.Sprintf("at this check interval the last 100 checks cover %d min — use that or less", 100*m.IntervalS/60)
	}
	if la.ThresholdMs == 0 && la.Factor == 0 {
		fe["settings.latencyAlert"] = "set a ms limit, a multiple of the usual value, or both"
	}
	if m.Type.HasNoTarget() {
		fe["settings.latencyAlert"] = "this monitor type has no response time"
	}
}
