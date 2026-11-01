package check

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"opskest/internal/model"
)

// A vendor feed that cannot be read must not alert, but must say it could not be read.
func TestVendorUnreadableFeedSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := &model.Monitor{Type: model.TypeHTTP, Target: srv.URL + "/feed/1.xml", TimeoutMs: 2000}
	m.Settings.Vendor = true
	res := checkVendor(context.Background(), m)
	if !res.OK || res.Degraded {
		t.Fatalf("an unreadable feed must not alert: %+v", res)
	}
	if res.Reason != VendorUnreadableReason {
		t.Fatalf("reason = %q, want %q", res.Reason, VendorUnreadableReason)
	}

	// Nothing is listening at all: same answer.
	srv.Close()
	res = checkVendor(context.Background(), m)
	if !res.OK || res.Reason != VendorUnreadableReason {
		t.Fatalf("unreachable feed: %+v", res)
	}
}
