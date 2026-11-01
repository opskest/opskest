package check

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"opskest/internal/model"
)

// A 3rd-party status feed only goes down/degraded on a vendor-reported
// incident: an unreachable feed or an error page is not the vendor's
// incident and must not alert.
func TestVendorFeedFailureIsNotAnIncident(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	for _, target := range []string{srv.URL + "/status", "http://127.0.0.1:1/feed"} {
		m := model.NewMonitor(model.TypeHTTP)
		m.Target, m.TimeoutMs, m.DegradedMs = target, 2000, 1
		m.Settings.Vendor = true
		res := httpChecker{}.Check(context.Background(), m)
		applyDegradation(&res, m)
		if !res.OK || res.Degraded {
			t.Fatalf("%s: vendor result = %+v, want OK and not degraded", target, res)
		}
	}

	// The same 502 on a normal monitor is a failure.
	m := model.NewMonitor(model.TypeHTTP)
	m.Target, m.TimeoutMs = srv.URL+"/status", 2000
	if res := (httpChecker{}).Check(context.Background(), m); res.OK {
		t.Fatalf("normal monitor = %+v, want failure", res)
	}
}
