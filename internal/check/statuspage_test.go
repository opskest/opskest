package check

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// rewriteTransport redirects every outgoing request to a fixed test server,
// preserving path and query, regardless of what scheme/host the code under
// test built the request with. This lets us test statuspage.go functions
// that hit hardcoded third-party URLs (metastatus.com, status.slack.com,
// status.firebase.google.com, ...) against an httptest.Server.
type rewriteTransport struct {
	target *url.URL
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = rt.target.Scheme
	req.URL.Host = rt.target.Host
	req.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func testClient(t *testing.T, serverURL string) *http.Client {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	return &http.Client{Transport: rewriteTransport{target: u}}
}

// ---------------------------------------------------------------------
// Atlassian statuspage.io summary.json
// ---------------------------------------------------------------------

func TestCheckStatuspageIO_NoIncident(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":{"indicator":"none","description":"All Systems Operational"},"incidents":[]}`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://example.statuspage.io/")
	client := testClient(t, ts.URL)
	handled, down, degraded, reason := checkStatuspageIO(t.Context(), client, u)
	if !handled || down || degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v reason=%q, want handled up", handled, down, degraded, reason)
	}
}

func TestCheckStatuspageIO_Minor(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":{"indicator":"minor","description":"Partial Degradation"},"incidents":[{"name":"Elevated error rates","status":"investigating","impact":"minor"}]}`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://example.statuspage.io/")
	client := testClient(t, ts.URL)
	handled, down, degraded, reason := checkStatuspageIO(t.Context(), client, u)
	if !handled || down || !degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want degraded only", handled, down, degraded)
	}
	if !strings.Contains(reason, "Elevated error rates") {
		t.Errorf("reason %q missing incident title", reason)
	}
}

func TestCheckStatuspageIO_Major(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":{"indicator":"major","description":"Major Outage"},"incidents":[{"name":"Total API outage","status":"identified","impact":"critical"}]}`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://example.statuspage.io/")
	client := testClient(t, ts.URL)
	handled, down, degraded, reason := checkStatuspageIO(t.Context(), client, u)
	if !handled || !down || degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want down only", handled, down, degraded)
	}
	if !strings.Contains(reason, "Total API outage") {
		t.Errorf("reason %q missing incident title", reason)
	}
}

// An indicator of "none" but an unresolved critical incident still on the
// feed should be treated as down, with the incident's own name surfaced.
func TestCheckStatuspageIO_UnresolvedIncidentNoIndicator(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":{"indicator":"none","description":"ok"},"incidents":[{"name":"Database failover","status":"investigating","impact":"critical"}]}`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://example.statuspage.io/")
	client := testClient(t, ts.URL)
	handled, down, _, reason := checkStatuspageIO(t.Context(), client, u)
	if !handled || !down {
		t.Fatalf("got handled=%v down=%v, want down", handled, down)
	}
	if !strings.Contains(reason, "Database failover") {
		t.Errorf("reason %q missing incident title", reason)
	}
}

func TestCheckStatuspageIO_Malformed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{not valid json`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://example.statuspage.io/")
	client := testClient(t, ts.URL)
	handled, down, degraded, reason := checkStatuspageIO(t.Context(), client, u)
	if handled || down || degraded || reason != "" {
		t.Fatalf("malformed feed should be unhandled, not panic: handled=%v down=%v degraded=%v reason=%q", handled, down, degraded, reason)
	}
}

func TestCheckStatuspageIO_NonJSONBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>not json at all</body></html>`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://example.statuspage.io/")
	client := testClient(t, ts.URL)
	handled, _, _, _ := checkStatuspageIO(t.Context(), client, u)
	if handled {
		t.Fatalf("HTML body should not be treated as a valid statuspage feed")
	}
}

func TestCheckStatuspageIO_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://example.statuspage.io/")
	client := testClient(t, ts.URL)
	handled, down, degraded, _ := checkStatuspageIO(t.Context(), client, u)
	if handled || down || degraded {
		t.Fatalf("5xx from the feed itself should be unhandled, not reported as a vendor outage")
	}
}

// ---------------------------------------------------------------------
// Google / Firebase incidents.json
// ---------------------------------------------------------------------

func TestCheckGoogleStatus_NoIncident(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[]`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://status.firebase.google.com/")
	client := testClient(t, ts.URL)
	handled, down, degraded, _ := checkGoogleStatus(t.Context(), client, u)
	if !handled || down || degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want up", handled, down, degraded)
	}
}

func TestCheckGoogleStatus_ActiveOutage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":"1","begin":"2026-01-01","end":"","external_desc":"Firestore writes failing","status_impact":"SERVICE_OUTAGE","severity":"HIGH","service_name":"Firestore","most_recent_update":{"status":"INVESTIGATING","text":"we see it"}}]`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://status.firebase.google.com/")
	client := testClient(t, ts.URL)
	handled, down, degraded, reason := checkGoogleStatus(t.Context(), client, u)
	if !handled || !down || degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want down", handled, down, degraded)
	}
	if !strings.Contains(reason, "Firestore") {
		t.Errorf("reason %q missing service name", reason)
	}
}

func TestCheckGoogleStatus_ActiveDegraded(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":"1","begin":"2026-01-01","end":"","external_desc":"Elevated latency","status_impact":"SERVICE_DISRUPTION","severity":"LOW","service_name":"Firestore","most_recent_update":{"status":"INVESTIGATING","text":"we see it"}}]`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://status.firebase.google.com/")
	client := testClient(t, ts.URL)
	handled, down, degraded, _ := checkGoogleStatus(t.Context(), client, u)
	if !handled || down || !degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want degraded", handled, down, degraded)
	}
}

func TestCheckGoogleStatus_ResolvedIsUp(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":"1","begin":"2026-01-01","end":"2026-01-02","external_desc":"done","status_impact":"SERVICE_OUTAGE","severity":"HIGH","service_name":"Firestore","most_recent_update":{"status":"AVAILABLE","text":"resolved"}}]`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://status.firebase.google.com/")
	client := testClient(t, ts.URL)
	handled, down, degraded, _ := checkGoogleStatus(t.Context(), client, u)
	if !handled || down || degraded {
		t.Fatalf("resolved incident should read as up, got down=%v degraded=%v", down, degraded)
	}
}

func TestCheckGoogleStatus_Malformed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"this":"is not an array"}`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://status.firebase.google.com/")
	client := testClient(t, ts.URL)
	handled, down, degraded, reason := checkGoogleStatus(t.Context(), client, u)
	if handled || down || degraded || reason != "" {
		t.Fatalf("malformed feed should be unhandled, not panic: handled=%v down=%v degraded=%v reason=%q", handled, down, degraded, reason)
	}
}

// ---------------------------------------------------------------------
// metastatus.com
// ---------------------------------------------------------------------

func TestCheckMetaStatus_NoIncident(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":"whatsapp","name":"WhatsApp","services":[{"name":"Messaging","status":"No known issues"}]}]`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://metastatus.com/whatsapp")
	client := testClient(t, ts.URL)
	handled, down, degraded, _ := checkMetaStatus(t.Context(), client, u)
	if !handled || down || degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want up", handled, down, degraded)
	}
}

func TestCheckMetaStatus_Outage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":"whatsapp","name":"WhatsApp","services":[{"name":"Messaging","status":"Service outage"}]}]`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://metastatus.com/whatsapp")
	client := testClient(t, ts.URL)
	handled, down, degraded, reason := checkMetaStatus(t.Context(), client, u)
	if !handled || !down || degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want down", handled, down, degraded)
	}
	if !strings.Contains(reason, "Messaging") {
		t.Errorf("reason %q missing affected service name", reason)
	}
}

func TestCheckMetaStatus_Malformed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://metastatus.com/whatsapp")
	client := testClient(t, ts.URL)
	handled, down, degraded, reason := checkMetaStatus(t.Context(), client, u)
	if handled || down || degraded || reason != "" {
		t.Fatalf("malformed feed should be unhandled, not panic: handled=%v down=%v degraded=%v reason=%q", handled, down, degraded, reason)
	}
}

// ---------------------------------------------------------------------
// status.slack.com
// ---------------------------------------------------------------------

func TestCheckSlackStatus_NoIncident(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"ok","active_incidents":[]}`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://status.slack.com/")
	client := testClient(t, ts.URL)
	handled, down, degraded, _ := checkSlackStatus(t.Context(), client, u)
	if !handled || down || degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want up", handled, down, degraded)
	}
}

func TestCheckSlackStatus_ActiveIncident(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"active","active_incidents":[{"title":"Messages delayed","type":"incident","status":"investigating"}]}`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://status.slack.com/")
	client := testClient(t, ts.URL)
	handled, down, _, reason := checkSlackStatus(t.Context(), client, u)
	if !handled || !down {
		t.Fatalf("got handled=%v down=%v, want down", handled, down)
	}
	if !strings.Contains(reason, "Messages delayed") {
		t.Errorf("reason %q missing incident title", reason)
	}
}

func TestCheckSlackStatus_Malformed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{{{`)
	}))
	defer ts.Close()

	u, _ := url.Parse("https://status.slack.com/")
	client := testClient(t, ts.URL)
	handled, down, degraded, reason := checkSlackStatus(t.Context(), client, u)
	if handled || down || degraded || reason != "" {
		t.Fatalf("malformed feed should be unhandled, not panic: handled=%v down=%v degraded=%v reason=%q", handled, down, degraded, reason)
	}
}

// ---------------------------------------------------------------------
// RSS / Atom feeds
// ---------------------------------------------------------------------

func TestCheckRSSFeed_NoIncident(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><rss><channel><title>Status</title><item><title>All clear</title><description>everything resolved</description></item></channel></rss>`)
	}))
	defer ts.Close()

	handled, down, degraded, _ := checkRSSOrAtomFeed(t.Context(), testClient(t, ts.URL), ts.URL+"/history.rss")
	if !handled || down || degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want up", handled, down, degraded)
	}
}

func TestCheckRSSFeed_Major(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><rss><channel><title>Status</title><item><title>Major outage in us-east-1</title><description>investigating</description></item></channel></rss>`)
	}))
	defer ts.Close()

	handled, down, degraded, reason := checkRSSOrAtomFeed(t.Context(), testClient(t, ts.URL), ts.URL+"/history.rss")
	if !handled || !down || degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want down", handled, down, degraded)
	}
	if !strings.Contains(reason, "Major outage in us-east-1") {
		t.Errorf("reason %q missing incident title", reason)
	}
}

func TestCheckRSSFeed_Minor(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><rss><channel><title>Status</title><item><title>Degraded performance</title><description>investigating a slowdown</description></item></channel></rss>`)
	}))
	defer ts.Close()

	handled, down, degraded, _ := checkRSSOrAtomFeed(t.Context(), testClient(t, ts.URL), ts.URL+"/history.rss")
	if !handled || down || !degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want degraded", handled, down, degraded)
	}
}

func TestCheckAtomFeed_Major(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><feed><title>Status</title><entry><title>Outage: API down</title><summary>investigating</summary></entry></feed>`)
	}))
	defer ts.Close()

	handled, down, degraded, reason := checkRSSOrAtomFeed(t.Context(), testClient(t, ts.URL), ts.URL+"/feed.atom")
	if !handled || !down || degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want down", handled, down, degraded)
	}
	if !strings.Contains(reason, "Outage: API down") {
		t.Errorf("reason %q missing incident title", reason)
	}
}

func TestCheckRSSFeed_Malformed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not xml at all <<<`)
	}))
	defer ts.Close()

	handled, down, degraded, reason := checkRSSOrAtomFeed(t.Context(), testClient(t, ts.URL), ts.URL+"/history.rss")
	if handled || down || degraded || reason != "" {
		t.Fatalf("malformed feed should be unhandled, not panic: handled=%v down=%v degraded=%v reason=%q", handled, down, degraded, reason)
	}
}

func TestCheckRSSFeed_EmptyChannel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><rss><channel><title>Status</title></channel></rss>`)
	}))
	defer ts.Close()

	handled, down, degraded, reason := checkRSSOrAtomFeed(t.Context(), testClient(t, ts.URL), ts.URL+"/history.rss")
	if handled || down || degraded || reason != "" {
		t.Fatalf("feed with no items should be unhandled, not panic: handled=%v down=%v degraded=%v reason=%q", handled, down, degraded, reason)
	}
}

// ---------------------------------------------------------------------
// Generic HTML status page fallback
// ---------------------------------------------------------------------

func TestCheckGenericHTMLStatusPage_Operational(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>All Systems Operational</body></html>`)
	}))
	defer ts.Close()

	handled, down, degraded, _ := checkGenericHTMLStatusPage(t.Context(), testClient(t, ts.URL), ts.URL+"/status")
	if !handled || down || degraded {
		t.Fatalf("got handled=%v down=%v degraded=%v, want up", handled, down, degraded)
	}
}

func TestCheckGenericHTMLStatusPage_MajorOutage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>We are experiencing a Major Outage</body></html>`)
	}))
	defer ts.Close()

	handled, down, degraded, reason := checkGenericHTMLStatusPage(t.Context(), testClient(t, ts.URL), ts.URL+"/status")
	if !handled || !down || degraded || reason == "" {
		t.Fatalf("got handled=%v down=%v degraded=%v reason=%q, want down", handled, down, degraded, reason)
	}
}

// ---------------------------------------------------------------------
// Top-level dispatch (CheckStatusPage)
// ---------------------------------------------------------------------

func TestCheckStatusPage_InvalidTarget(t *testing.T) {
	handled, down, degraded, reason := CheckStatusPage(t.Context(), http.DefaultClient, "://bad")
	if handled || down || degraded || reason != "" {
		t.Fatalf("invalid target url should not be handled, not panic: handled=%v", handled)
	}
}

func TestCheckStatusPage_Dispatch_StatuspageIO(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/summary.json" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"status":{"indicator":"major","description":"Major Outage"},"incidents":[{"name":"Full outage","status":"identified","impact":"critical"}]}`)
	}))
	defer ts.Close()

	// The dispatcher only inspects the URL's host/path to pick a checker;
	// the rewriteTransport then routes the actual request to our fixture
	// server regardless of what host it names.
	target := "https://www.githubstatus.com/"
	client := testClient(t, ts.URL)
	handled, down, _, reason := CheckStatusPage(t.Context(), client, target)
	if !handled || !down {
		t.Fatalf("got handled=%v down=%v, want down (target=%s)", handled, down, target)
	}
	if !strings.Contains(reason, "Full outage") {
		t.Errorf("reason %q missing incident title", reason)
	}
}
