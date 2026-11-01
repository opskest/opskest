package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"opskest/internal/model"
	"opskest/internal/store"
)

// diagnosticsBeats is how many recent checks GET /api/monitors/{id}/
// diagnostics returns. It equals the store's list cap, so the response is
// bounded the same way every other list endpoint is.
const diagnosticsBeats = 200

// diagnosticsCert is the certificate the last TLS-carrying check saw. The
// engine keeps it in monitor_runtime (it must survive restarts for expiry
// alerts); nothing else on the API exposed more than the days left.
type diagnosticsCert struct {
	NotAfter int64  `json:"notAfter"` // unix ms
	Issuer   string `json:"issuer"`
	Subject  string `json:"subject"`
}

// diagnosticsConfig is the non-secret subset of a monitor's configuration
// the Diagnostics tab needs to describe what is checked and how to read the
// results. It deliberately leaves out headers, bodies, credentials, SQL
// query text, expected query values, keywords, JSON paths and send/expect
// payloads: where such a setting matters the flag says only that it is set.
type diagnosticsConfig struct {
	Type           model.MonitorType `json:"type"`
	IntervalS      int               `json:"intervalS"`
	RetryIntervalS int               `json:"retryIntervalS"`
	Retries        int               `json:"retries"`
	TimeoutMs      int               `json:"timeoutMs"`
	DegradedMs     int               `json:"degradedMs"`
	SSLWarnDays    int               `json:"sslWarnDays"`
	UpsideDown     bool              `json:"upsideDown"`
	Paused         bool              `json:"paused"`

	Method          string   `json:"method,omitempty"`
	AcceptedCodes   string   `json:"acceptedCodes,omitempty"`
	MaxRedirects    *int     `json:"maxRedirects,omitempty"`
	IgnoreTLS       bool     `json:"ignoreTls,omitempty"`
	HasKeyword      bool     `json:"hasKeyword,omitempty"`
	KeywordMode     string   `json:"keywordMode,omitempty"`
	HasJSONQuery    bool     `json:"hasJsonQuery,omitempty"`
	AssertionCount  int      `json:"assertionCount,omitempty"`
	Vendor          bool     `json:"vendor,omitempty"`
	TLS             bool     `json:"tls,omitempty"`
	Security        string   `json:"security,omitempty"`
	GRPCService     string   `json:"grpcService,omitempty"`
	MaxOffsetMs     int      `json:"maxOffsetMs,omitempty"`
	Count           int      `json:"count,omitempty"`
	MaxLossPct      int      `json:"maxLossPct,omitempty"`
	Record          string   `json:"record,omitempty"`
	Resolver        string   `json:"resolver,omitempty"`
	ExpectedValues  []string `json:"expectedValues,omitempty"`
	Match           string   `json:"match,omitempty"`
	GraceS          int      `json:"graceS,omitempty"`
	HasSend         bool     `json:"hasSend,omitempty"`
	HasExpect       bool     `json:"hasExpect,omitempty"`
	Probe           string   `json:"probe,omitempty"`
	HasQuery        bool     `json:"hasQuery,omitempty"`
	QueryOp         string   `json:"queryOp,omitempty"`
	Threshold       int      `json:"threshold,omitempty"`
	ThresholdSecs   int      `json:"thresholdSecs,omitempty"`
	DependencyCount int      `json:"dependencyCount,omitempty"`
}

type diagnosticsOut struct {
	// Now is the server clock (unix ms), so lateness is judged against the
	// same clock that stamped the beats.
	Now     int64             `json:"now"`
	Monitor diagnosticsConfig `json:"monitor"`
	Cert    *diagnosticsCert  `json:"cert"`
	Beats   []store.Heartbeat `json:"beats"` // newest first, at most diagnosticsBeats
}

// typeMayCarryCert reports whether a monitor type's checker can observe a TLS
// certificate (see internal/check). A leftover certificate row from before a
// monitor changed type is not shown for any other type.
func typeMayCarryCert(t model.MonitorType) bool {
	switch t {
	case model.TypeHTTP, model.TypeWebSocket, model.TypeTCP, model.TypeTLS, model.TypeSMTP,
		model.TypeRedis, model.TypeMongoDB, model.TypeMQTT, model.TypeAMQP, model.TypeBanner, model.TypeGRPC:
		return true
	}
	return false
}

func diagnosticsConfigOf(m *model.Monitor) diagnosticsConfig {
	s := m.Settings
	c := diagnosticsConfig{
		Type: m.Type, IntervalS: m.IntervalS, RetryIntervalS: m.RetryIntervalS, Retries: m.Retries,
		TimeoutMs: m.TimeoutMs, DegradedMs: m.DegradedMs, SSLWarnDays: m.SSLWarnDays,
		UpsideDown: m.UpsideDown, Paused: m.Paused,

		Method: s.Method, AcceptedCodes: s.AcceptedCodes, MaxRedirects: s.MaxRedirects, IgnoreTLS: s.IgnoreTLS,
		HasKeyword: s.Keyword != "", HasJSONQuery: s.JSONPath != "", AssertionCount: len(s.Assertions),
		Vendor: s.Vendor, TLS: s.TLS, Security: s.Security, GRPCService: s.GRPCService, MaxOffsetMs: s.MaxOffsetMs,
		Count: s.Count, MaxLossPct: s.MaxLossPct, Record: s.Record, Resolver: s.Resolver, Match: s.Match,
		GraceS: s.GraceS, HasSend: s.Send != "", HasExpect: s.Expect != "",
		Probe: s.Probe, HasQuery: s.Query != "", QueryOp: s.QueryOp, Threshold: s.Threshold, ThresholdSecs: s.ThresholdSecs,
		DependencyCount: len(s.DependsOn),
	}
	if s.Keyword != "" {
		c.KeywordMode = s.KeywordMode
	}
	if m.Type == model.TypeDNS {
		c.ExpectedValues = append([]string(nil), s.DNSExpected()...)
	}
	return c
}

// monitorCert reads one monitor's remembered certificate. It is a plain
// single-row read; the runtime row is only ever written through the writer.
func (s *server) monitorCert(ctx context.Context, id int64) (*diagnosticsCert, error) {
	var notAfter sql.NullInt64
	var issuer, subject sql.NullString
	err := s.d.Store.R.QueryRowContext(ctx,
		`SELECT cert_not_after, cert_issuer, cert_subject FROM monitor_runtime WHERE monitor_id=?`, id,
	).Scan(&notAfter, &issuer, &subject)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !notAfter.Valid) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &diagnosticsCert{NotAfter: notAfter.Int64, Issuer: issuer.String, Subject: subject.String}, nil
}

// handleDiagnostics serves GET /api/monitors/{id}/diagnostics: the last 200
// checks plus the non-secret settings and remembered certificate needed to
// read them. Unknown ids answer 404, like every other monitor read. It runs no
// check and writes nothing.
func (s *server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	m, ok := s.d.Engine.Monitor(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found", nil)
		return
	}
	beats, err := s.d.Store.BeatsPage(r.Context(), id, 0, diagnosticsBeats)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
		return
	}
	if beats == nil {
		beats = []store.Heartbeat{}
	}
	out := diagnosticsOut{Now: s.d.Now().UnixMilli(), Monitor: diagnosticsConfigOf(m), Beats: beats}
	if typeMayCarryCert(m.Type) {
		cert, err := s.monitorCert(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "fetch failed", nil)
			return
		}
		out.Cert = cert
	}
	writeJSON(w, http.StatusOK, out)
}
