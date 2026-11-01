package model

import (
	"strings"
	"testing"
)

func TestStatusOrder(t *testing.T) {
	order := []Status{StatusDown, StatusPending, StatusDegraded, StatusUnknown, StatusMaintenance, StatusUp, StatusPaused}
	for i := 0; i < len(order)-1; i++ {
		if order[i].Severity() <= order[i+1].Severity() {
			t.Fatalf("%s must outrank %s", order[i], order[i+1])
		}
	}
	if string(StatusDegraded.BeatChar()) != "g" || StatusDown.String() != "down" {
		t.Fatal("encoding")
	}
	if !StatusPending.Good() || StatusDown.Good() || !StatusDown.Bad() || StatusUnknown.Good() || StatusUnknown.Bad() {
		t.Fatal("good/bad")
	}
}

func TestParseCodes(t *testing.T) {
	cases := []struct {
		in   string
		code int
		want bool
	}{
		{"", 200, true}, {"", 301, false}, {"200-299,301", 301, true}, {"200-299,301", 302, false},
		{" 404 ", 404, true}, {"500-599", 503, true},
	}
	for _, c := range cases {
		cs, err := ParseCodes(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if cs.Match(c.code) != c.want {
			t.Errorf("%q match %d = %v", c.in, c.code, !c.want)
		}
	}
	for _, bad := range []string{"abc", "299-200", "99", "600", "200-", "200,,300"} {
		if _, err := ParseCodes(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestValidateHTTP(t *testing.T) {
	m := &Monitor{Name: "API", Type: TypeHTTP, Target: "https://api.example.com/health"}
	m.ApplyDefaults()
	if fe := m.Validate(); len(fe) != 0 {
		t.Fatalf("valid monitor: %v", fe)
	}
	if m.IntervalS != 60 || m.RetryIntervalS != 20 || m.TimeoutMs != 10000 {
		t.Fatalf("defaults %+v", m)
	}
	if m.Settings.Method != "GET" || m.Settings.AcceptedCodes != "200-299" || *m.Settings.MaxRedirects != 10 {
		t.Fatal("http defaults")
	}

	bad := m.Clone()
	bad.Target = "ftp://x"
	if bad.Validate()["target"] == "" {
		t.Error("ftp target must fail")
	}
	bad = m.Clone()
	bad.TimeoutMs = 60000
	if bad.Validate()["timeoutMs"] == "" {
		t.Error("timeout >= interval must fail")
	}
	bad = m.Clone()
	bad.IntervalS = 5
	if bad.Validate()["intervalS"] == "" {
		t.Error("interval < 10 must fail")
	}
	bad = m.Clone()
	bad.Settings.JSONPath = "status"
	bad.Settings.JSONOp = "~="
	if bad.Validate()["settings.jsonOp"] == "" {
		t.Error("bad op")
	}
	bad = m.Clone()
	bad.Settings.KeywordMode = "regex"
	bad.Settings.Keyword = "("
	if bad.Validate()["settings.keyword"] == "" {
		t.Error("bad regex")
	}
	bad = m.Clone()
	bad.Name = strings.Repeat("x", 121)
	if bad.Validate()["name"] == "" {
		t.Error("name > 120")
	}
}

func TestNewMonitorDefaults(t *testing.T) {
	h := NewMonitor(TypeHTTP)
	if h.Type != TypeHTTP || h.IntervalS != 60 || h.RetryIntervalS != 20 || h.Retries != 2 || h.TimeoutMs != 10000 || h.SSLWarnDays != 14 {
		t.Fatalf("http common defaults: %+v", h)
	}
	if h.Settings.Method != "GET" || h.Settings.BodyType != "none" || h.Settings.AuthType != "none" ||
		h.Settings.AcceptedCodes != "200-299" || h.Settings.MaxRedirects == nil || *h.Settings.MaxRedirects != 10 {
		t.Fatalf("http settings defaults: %+v", h.Settings)
	}

	p := NewMonitor(TypePing)
	if p.Retries != 2 || p.SSLWarnDays != 14 || p.Settings.Count != 3 || p.Settings.MaxLossPct != 34 {
		t.Fatalf("ping defaults: %+v", p)
	}

	push := NewMonitor(TypePush)
	if push.Retries != 2 || push.SSLWarnDays != 14 || push.Settings.GraceS != 30 {
		t.Fatalf("push defaults: %+v", push)
	}
}

func TestApplyDefaultsKeepsMeaningfulZero(t *testing.T) {
	m := &Monitor{
		Name: "API", Type: TypeHTTP, Target: "https://api.example.com/health",
		Retries: 0, SSLWarnDays: 0, DegradedMs: 0, ReminderMin: 0,
	}
	m.ApplyDefaults()
	if m.Retries != 0 || m.SSLWarnDays != 0 || m.DegradedMs != 0 || m.ReminderMin != 0 {
		t.Fatalf("ApplyDefaults must not overwrite meaningful zeros: %+v", m)
	}
	if fe := m.Validate(); len(fe) != 0 {
		t.Fatalf("meaningful zeros should still validate: %v", fe)
	}

	push := &Monitor{Name: "job", Type: TypePush, Settings: Settings{GraceS: 0}}
	push.ApplyDefaults()
	if push.Settings.GraceS != 0 {
		t.Fatalf("ApplyDefaults must not overwrite GraceS zero: %+v", push.Settings)
	}
	if fe := push.Validate(); len(fe) != 0 {
		t.Fatalf("push graceS=0 should validate: %v", fe)
	}
}

func TestFieldErrorsErr(t *testing.T) {
	var nilMap FieldErrors
	if nilMap.Err() != nil {
		t.Fatal("nil FieldErrors should have nil Err")
	}
	empty := FieldErrors{}
	if empty.Err() != nil {
		t.Fatal("empty FieldErrors should have nil Err")
	}
	withOne := FieldErrors{"name": "required"}
	if err := withOne.Err(); err == nil || err.Error() == "" {
		t.Fatalf("non-empty FieldErrors should have a message, got %v", err)
	}
}

func TestValidateOtherTypes(t *testing.T) {
	ok := []*Monitor{
		{Name: "db", Type: TypeTCP, Target: "db.internal:3306"},
		{Name: "gw", Type: TypePing, Target: "10.0.0.1"},
		{Name: "dns", Type: TypeDNS, Target: "example.com", Settings: Settings{Record: "A"}},
		{Name: "job", Type: TypePush},
		{Name: "ws", Type: TypeWebSocket, Target: "wss://rt.example.com/socket"},
	}
	for _, m := range ok {
		m.ApplyDefaults()
		if fe := m.Validate(); len(fe) != 0 {
			t.Errorf("%s: %v", m.Type, fe)
		}
	}
	bad := []*Monitor{
		{Name: "db", Type: TypeTCP, Target: "db.internal"}, // no port
		{Name: "dns", Type: TypeDNS, Target: "x.co", Settings: Settings{Record: "SRV"}},
		{Name: "ws", Type: TypeWebSocket, Target: "https://x"},
		{Name: "", Type: TypeHTTP, Target: "https://x"},
		{Name: "t", Type: "smtp", Target: "x"},
	}
	for _, m := range bad {
		m.ApplyDefaults()
		if len(m.Validate()) == 0 {
			t.Errorf("%+v should fail", m)
		}
	}
}

func TestHost(t *testing.T) {
	cases := map[string]*Monitor{
		"api.example.com": {Type: TypeHTTP, Target: "https://api.example.com:8443/x"},
		"db.internal":     {Type: TypeTCP, Target: "db.internal:3306"},
		"10.0.0.1":        {Type: TypePing, Target: "10.0.0.1"},
		"example.com":     {Type: TypeDNS, Target: "example.com"},
		"":                {Type: TypePush},
	}
	for want, m := range cases {
		if got := m.Host(); got != want {
			t.Errorf("Host(%s)=%q want %q", m.Target, got, want)
		}
	}
}

func TestTrimReason(t *testing.T) {
	long := strings.Repeat("é", 250)
	if got := []rune(TrimReason(long)); len(got) != 200 {
		t.Fatalf("len %d", len(got))
	}
	if TrimReason("a\n\n  b") != "a b" {
		t.Fatal("whitespace collapse")
	}
}

func TestCloneDeep(t *testing.T) {
	g := int64(1)
	m := &Monitor{GroupID: &g, TagIDs: []int64{1}, Secrets: Secrets{Headers: map[string]string{"A": "1"}}}
	c := m.Clone()
	*c.GroupID = 2
	c.TagIDs[0] = 9
	c.Secrets.Headers["A"] = "x"
	if *m.GroupID != 1 || m.TagIDs[0] != 1 || m.Secrets.Headers["A"] != "1" {
		t.Fatal("clone shares memory")
	}
}

func TestValidateBranchCoverage(t *testing.T) {
	type tc struct {
		name  string
		mut   func(m *Monitor)
		typ   MonitorType
		field string
	}
	cases := []tc{
		{"bad authType", func(m *Monitor) { m.Settings.AuthType = "digest" }, TypeHTTP, "settings.authType"},
		{"bad bodyType", func(m *Monitor) { m.Settings.BodyType = "xml" }, TypeHTTP, "settings.bodyType"},
		{"bad dns record", func(m *Monitor) { m.Settings.Record = "SRV" }, TypeDNS, "settings.record"},
		{"bad dns resolver", func(m *Monitor) { m.Settings.Resolver = "not a host:port:x" }, TypeDNS, "settings.resolver"},
		{"ws scheme", func(m *Monitor) { m.Target = "http://x.example.com" }, TypeWebSocket, "target"},
		{"tcp port 0", func(m *Monitor) { m.Target = "db.example.com:0" }, TypeTCP, "target"},
		{"tcp port 70000", func(m *Monitor) { m.Target = "db.example.com:70000" }, TypeTCP, "target"},
	}
	targets := map[MonitorType]string{
		TypeHTTP: "https://x.example.com", TypeDNS: "x.example.com",
		TypeWebSocket: "wss://x.example.com", TypeTCP: "db.example.com:5432",
	}
	for _, c := range cases {
		m := NewMonitor(c.typ)
		m.Name, m.Target = "t", targets[c.typ]
		if c.typ == TypeDNS {
			m.Settings.Record = "A"
		}
		c.mut(m)
		if fe := m.Validate(); fe[c.field] == "" {
			t.Errorf("%s: want error on %q, got %v", c.name, c.field, fe)
		}
	}
}

func TestValidateReportsEachBadHeader(t *testing.T) {
	m := NewMonitor(TypeHTTP)
	m.Name, m.Target = "t", "https://x.example.com"
	m.Settings.Headers = []Header{{Name: "Ok", Value: "1"}, {Name: "bad header", Value: "2"}, {Name: "Big", Value: strings.Repeat("x", 9000)}}
	fe := m.Validate()
	if fe["headers.1.name"] == "" || fe["headers.2.value"] == "" {
		t.Fatalf("per-header errors missing: %v", fe)
	}
	if fe["headers.0.name"] != "" {
		t.Errorf("valid header flagged: %v", fe)
	}
}

func TestDNSExpectedLegacyFold(t *testing.T) {
	m := &Monitor{Type: TypeDNS, Target: "x.co", Settings: Settings{Record: "A", Expected: "10.0.0.1"}}
	if got := m.Settings.DNSExpected(); len(got) != 1 || got[0] != "10.0.0.1" {
		t.Fatalf("DNSExpected before defaults = %v", got)
	}
	m.ApplyDefaults()
	if m.Settings.Expected != "" || len(m.Settings.ExpectedValues) != 1 || m.Settings.Match != "equals" {
		t.Fatalf("after ApplyDefaults: expected=%q values=%v match=%q", m.Settings.Expected, m.Settings.ExpectedValues, m.Settings.Match)
	}

	multi := &Monitor{Type: TypeDNS, Target: "x.co", Settings: Settings{Record: "A", ExpectedValues: []string{"10.0.0.1", "10.0.0.2"}}}
	multi.ApplyDefaults()
	if multi.Settings.Match != "exact" {
		t.Errorf("multi-value default match = %q, want exact", multi.Settings.Match)
	}
}

func TestDNSExpectedValidation(t *testing.T) {
	cases := []struct {
		record string
		values []string
		match  string
		bad    bool
	}{
		{"A", []string{"10.0.0.1", "10.0.0.2"}, "exact", false},
		{"A", []string{"10.0.0.1", "::1"}, "exact", true},
		{"A", []string{"not-an-ip"}, "equals", true},
		{"A", []string{"10.0.0"}, "contains", false},
		{"AAAA", []string{"2600::1"}, "all", false},
		{"AAAA", []string{"10.0.0.1"}, "all", true},
		{"A", []string{" "}, "equals", true},
		{"CNAME", []string{"alb.example.com"}, "exact", false},
	}
	for _, c := range cases {
		m := NewMonitor(TypeDNS)
		m.Name, m.Target = "d", "x.co"
		m.Settings.Record, m.Settings.ExpectedValues, m.Settings.Match = c.record, c.values, c.match
		_, has := m.Validate()["settings.expectedValues"]
		if has != c.bad {
			t.Errorf("%s %v %s: error=%v, want %v", c.record, c.values, c.match, has, c.bad)
		}
	}
}
