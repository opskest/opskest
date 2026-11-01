package check

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestGlobalpingPayloadAndPolling checks the create payload shape (options
// under measurementOptions) and that polling continues until finished.
func TestGlobalpingPayloadAndPolling(t *testing.T) {
	var polls atomic.Int32
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"m1"}`))
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/m1") {
			http.NotFound(w, r)
			return
		}
		if polls.Add(1) < 2 {
			_, _ = w.Write([]byte(`{"status":"in-progress","results":[{"probe":{"country":"US"},"result":{"status":"in-progress"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"finished","results":[
			{"probe":{"country":"us","city":"Ashburn"},"result":{"status":"finished","statusCode":200,"timings":{"total":81}}},
			{"probe":{"country":"de","city":"Berlin"},"result":{"status":"failed","rawOutput":"curl: (28) timeout\n"}},
			{"probe":{"country":"sg","city":"Singapore"},"result":{"status":"finished","statusCode":503,"timings":{"total":40}}}]}`))
	}))
	defer srv.Close()
	old := GlobalpingURL
	GlobalpingURL = srv.URL + "/v1/measurements"
	defer func() { GlobalpingURL = old }()

	u, _ := url.Parse("https://api.example.com:8443/health?x=1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	probes, err := Globalping(ctx, GPRequest{Kind: "http", Target: u, Countries: []string{"US", "DE", "SG"}})
	if err != nil {
		t.Fatal(err)
	}
	if body["target"] != "api.example.com" {
		t.Fatalf("target = %v", body["target"])
	}
	opts, _ := body["measurementOptions"].(map[string]any)
	req, _ := opts["request"].(map[string]any)
	if opts["protocol"] != "HTTPS" || opts["port"] != float64(8443) || req["path"] != "/health" {
		t.Fatalf("measurementOptions = %v", opts)
	}
	if len(probes) != 3 || !probes[0].OK || probes[0].Country != "US" || probes[1].OK || probes[1].Detail != "curl: (28) timeout" || probes[2].OK {
		t.Fatalf("probes = %+v", probes)
	}
}

// TestGlobalpingLive hits the real API when GLOBALPING_LIVE=1.
func TestGlobalpingLive(t *testing.T) {
	if os.Getenv("GLOBALPING_LIVE") != "1" {
		t.Skip("GLOBALPING_LIVE not set")
	}
	u, _ := url.Parse("https://example.com/")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	probes, err := Globalping(ctx, GPRequest{Kind: "http", Target: u, Countries: []string{"US", "DE", "IN"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probes {
		t.Logf("%s %s ok=%v code=%d %dms %s", p.Country, p.City, p.OK, p.Code, p.LatencyMs, p.Detail)
	}
	if len(probes) == 0 {
		t.Fatal("no probes")
	}
}
