package check

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newRDAPTestServer serves a bootstrap document mapping the "com" TLD to
// itself, and answers /domain/example.com with an expiration event. It
// counts how many times the bootstrap path is requested.
func newRDAPTestServer(t *testing.T) (srv *httptest.Server, bootstrapHits *int32) {
	t.Helper()
	var hits int32
	mux := http.NewServeMux()
	srv = httptest.NewServer(mux)

	mux.HandleFunc("/bootstrap.json", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"services":[[["com"],["%s/"]]]}`, srv.URL)
	})
	mux.HandleFunc("/domain/example.com", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rdap+json")
		w.Write([]byte(`{"events":[{"eventAction":"expiration","eventDate":"2027-03-01T00:00:00Z"}]}`))
	})

	return srv, &hits
}

func TestRDAPExpiry(t *testing.T) {
	srv, hits := newRDAPTestServer(t)
	defer srv.Close()

	r := NewRDAP(srv.Client(), srv.URL+"/bootstrap.json")

	exp, err := r.Expiry(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("Expiry: %v", err)
	}
	want := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	if !exp.Equal(want) {
		t.Errorf("Expiry = %v, want %v", exp, want)
	}

	// A second call, including through a subdomain, must not refetch the
	// bootstrap document since it's cached for 24h.
	if _, err := r.Expiry(context.Background(), "www.example.com"); err != nil {
		t.Fatalf("second Expiry: %v", err)
	}

	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("bootstrap fetched %d times, want 1", got)
	}
}

func TestRDAPNoServer(t *testing.T) {
	srv, _ := newRDAPTestServer(t)
	defer srv.Close()

	r := NewRDAP(srv.Client(), srv.URL+"/bootstrap.json")

	_, err := r.Expiry(context.Background(), "example.io")
	if err != ErrNoRDAP {
		t.Fatalf("err = %v, want ErrNoRDAP", err)
	}
}

func TestRDAPRegistrableDomain(t *testing.T) {
	cases := map[string]string{
		"example.com":         "example.com",
		"www.example.com":     "example.com",
		"api.www.example.com": "example.com",
		"example.com.in":      "example.com.in",
		"www.example.com.in":  "example.com.in",
		"example.org":         "example.org",
	}
	for in, want := range cases {
		if got := registrableDomain(in); got != want {
			t.Errorf("registrableDomain(%q) = %q, want %q", in, got, want)
		}
	}
}
