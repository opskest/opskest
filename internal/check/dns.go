package check

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"opskest/internal/model"
)

// dnsDialTimeout bounds a single dial attempt to a configured resolver.
const dnsDialTimeout = 2 * time.Second

func init() {
	Register(model.TypeDNS, dnsChecker{})
}

// dnsChecker implements Checker for model.TypeDNS. m.Target is the
// hostname to resolve; Settings.Resolver, empty for the system resolver,
// or "ip[:port]" (default port 53) for a specific one.
type dnsChecker struct{}

// nameRecordTypes are record types whose values are themselves hostnames,
// so an equals match against them is case-insensitive.
var nameRecordTypes = map[string]bool{"CNAME": true, "MX": true, "NS": true}

func (dnsChecker) Check(ctx context.Context, m *model.Monitor) model.Result {
	start := time.Now()
	s := m.Settings

	record := strings.ToUpper(s.Record)
	if record == "" {
		record = "A"
	}

	resolver := resolverFor(s.Resolver)

	values, err := lookupDNS(ctx, resolver, record, m.Target)
	if err != nil {
		class, reason := ClassifyErr(err)
		if class == model.ErrTimeout {
			reason = fmt.Sprintf("timeout after %d ms", m.TimeoutMs)
		}
		code := dnsRcode(err)
		if code == 3 {
			reason = fmt.Sprintf("NXDOMAIN: %s %s does not exist", record, m.Target)
		} else if code == 2 {
			reason = "SERVFAIL: " + reason
		}
		return model.Result{
			OK:        false,
			LatencyMs: -1,
			Code:      code,
			ErrClass:  class,
			Reason:    model.TrimReason(reason),
		}
	}

	res := model.Result{LatencyMs: msSince(start)}

	ok, reason := evalDNS(record, m.Target, values, s.DNSExpected(), s.Match)
	if !ok {
		res.OK = false
		res.LatencyMs = -1
		res.ErrClass = model.ErrAssertion
		res.Reason = model.TrimReason(reason)
		return res
	}

	res.OK = true
	return res
}

// lookupDNS runs the query for one supported record type and returns its
// values as plain strings (trailing dots trimmed for names).
func lookupDNS(ctx context.Context, resolver *net.Resolver, record, target string) ([]string, error) {
	switch record {
	case "A", "AAAA":
		addrs, err := resolver.LookupIPAddr(ctx, target)
		if err != nil {
			return nil, err
		}
		wantV4 := record == "A"
		var values []string
		for _, a := range addrs {
			if (a.IP.To4() != nil) == wantV4 {
				values = append(values, a.IP.String())
			}
		}
		return values, nil
	case "CNAME":
		cname, err := resolver.LookupCNAME(ctx, target)
		if err != nil {
			return nil, err
		}
		return []string{strings.TrimSuffix(cname, ".")}, nil
	case "MX":
		mxs, err := resolver.LookupMX(ctx, target)
		if err != nil {
			return nil, err
		}
		values := make([]string, 0, len(mxs))
		for _, mx := range mxs {
			values = append(values, strings.TrimSuffix(mx.Host, "."))
		}
		return values, nil
	case "TXT":
		return resolver.LookupTXT(ctx, target)
	case "NS":
		nss, err := resolver.LookupNS(ctx, target)
		if err != nil {
			return nil, err
		}
		values := make([]string, 0, len(nss))
		for _, ns := range nss {
			values = append(values, strings.TrimSuffix(ns.Host, "."))
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unsupported dns record type %q", record)
	}
}

// evalDNS matches the looked-up values against the expected list.
// With no expected values, OK means at least one record was returned.
// Comparison is case-insensitive for name records (CNAME, MX, NS).
//
//   - "equals" (and unset): any answer equals any expected value
//   - "contains": any answer contains any expected value
//   - "all": every expected value is among the answers
//   - "exact": the answers are exactly the expected values, in any order
func evalDNS(record, target string, values, expected []string, match string) (bool, string) {
	if len(expected) == 0 {
		if len(values) == 0 {
			return false, fmt.Sprintf("%s %s: no records found", record, target)
		}
		return true, ""
	}

	caseInsensitive := nameRecordTypes[record]
	eq := func(a, b string) bool {
		return a == b || (caseInsensitive && strings.EqualFold(a, b))
	}
	in := func(v string, set []string) bool {
		for _, s := range set {
			if eq(v, s) {
				return true
			}
		}
		return false
	}

	switch match {
	case "all", "exact":
		var missing, extra []string
		for _, e := range expected {
			if !in(e, values) {
				missing = append(missing, e)
			}
		}
		if match == "exact" {
			for _, v := range values {
				if !in(v, expected) {
					extra = append(extra, v)
				}
			}
		}
		if len(missing) == 0 && len(extra) == 0 {
			return true, ""
		}
		return false, dnsDiffReason(record, target, values, missing, extra)
	case "contains":
		for _, v := range values {
			for _, e := range expected {
				if strings.Contains(v, e) {
					return true, ""
				}
			}
		}
	default: // "equals" and unset
		for _, v := range values {
			if in(v, expected) {
				return true, ""
			}
		}
	}

	return false, fmt.Sprintf("%s %s = %v, expected %s", record, target, values, strings.Join(expected, ", "))
}

// dnsDiffReason describes how the answers drifted from the expected set,
// e.g. "A alb.example.com changed: missing 3.1.2.3; new 52.9.8.7".
func dnsDiffReason(record, target string, values, missing, extra []string) string {
	if len(values) == 0 {
		return fmt.Sprintf("%s %s: no records found", record, target)
	}
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing "+strings.Join(missing, ", "))
	}
	if len(extra) > 0 {
		parts = append(parts, "new "+strings.Join(extra, ", "))
	}
	return fmt.Sprintf("%s %s changed: %s", record, target, strings.Join(parts, "; "))
}

// ResolveDNS looks up target's record through resolver ("" for the
// system resolver, else ip[:port]) and returns the answers sorted, for
// "use the current answers" in the UI.
func ResolveDNS(ctx context.Context, target, record, resolver string) ([]string, error) {
	record = strings.ToUpper(record)
	if record == "" {
		record = "A"
	}
	values, err := lookupDNS(ctx, resolverFor(resolver), record, target)
	if err != nil {
		return nil, err
	}
	slices.Sort(values)
	return slices.Compact(values), nil
}

// resolverFor returns the system resolver when addr is empty, or a
// net.Resolver{PreferGo: true} that dials addr (defaulting to port 53)
// directly, with a bounded per-attempt dial timeout.
func resolverFor(addr string) *net.Resolver {
	if addr == "" {
		return net.DefaultResolver
	}

	target := addr
	if _, _, err := net.SplitHostPort(addr); err != nil {
		target = net.JoinHostPort(addr, "53")
	}

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: dnsDialTimeout}
			return d.DialContext(ctx, network, target)
		},
	}
}

// dnsRcode maps a resolver error to the DNS response code it represents:
// 3 (NXDOMAIN) for a name that does not exist, 2 (SERVFAIL) for a server
// failure, 0 when there is no rcode to report (timeouts, network errors).
// A successful lookup also reports 0 (NOERROR), which Result treats as
// "no code".
func dnsRcode(err error) int {
	var de *net.DNSError
	if !errors.As(err, &de) || de.IsTimeout {
		return 0
	}
	if de.IsNotFound {
		return 3
	}
	if de.IsTemporary || strings.Contains(strings.ToLower(de.Err), "server misbehaving") {
		return 2
	}
	return 0
}
