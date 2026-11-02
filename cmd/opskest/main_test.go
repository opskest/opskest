package main

import (
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMainRunVersion(t *testing.T) {
	code := mainRun([]string{"version"}, func(string) string { return "" })
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestMainRunDefaultsToServe(t *testing.T) {
	// An invalid setting makes config.Load fail fast, so runServe reports
	// exit code 1 rather than panicking or blocking. (With nothing set at
	// all the server now starts on defaults, so it can't be used here.)
	code := mainRun(nil, func(k string) string {
		if k == "APP_URL" {
			return "not a url"
		}
		return ""
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (invalid config)", code)
	}
}

func TestMainRunUnknownCommand(t *testing.T) {
	code := mainRun([]string{"bogus"}, func(string) string { return "" })
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}

func TestRunHealthcheckNoServer(t *testing.T) {
	// Claim 127.0.0.1:8080 ourselves so we know its state, then release it
	// immediately so the follow-up GET gets a real connection-refused
	// rather than hitting whatever else might be listening. If something
	// else already holds the port, we can't control its behavior, so skip.
	ln, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Skipf("127.0.0.1:8080 is occupied by something outside this test: %v", err)
	}
	ln.Close()

	if code := runHealthcheck(func(string) string { return "" }); code != 1 {
		t.Fatalf("exit code = %d, want 1 when nothing is listening", code)
	}
}

func TestRunHealthcheckAgainstRealServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:8080")
	if err != nil {
		t.Skipf("127.0.0.1:8080 unavailable in this environment: %v", err)
	}
	defer ln.Close()

	mux := http.NewServeMux()
	var status atomic.Int32
	status.Store(http.StatusOK)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	// Give the server a moment to accept.
	for i := 0; i < 50; i++ {
		if _, err := http.Get("http://127.0.0.1:8080/healthz"); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if code := runHealthcheck(func(string) string { return "" }); code != 0 {
		t.Fatalf("exit code = %d, want 0 for 200 response", code)
	}

	status.Store(http.StatusInternalServerError)
	if code := runHealthcheck(func(string) string { return "" }); code != 1 {
		t.Fatalf("exit code = %d, want 1 for non-200 response", code)
	}
}

func TestVersionDefault(t *testing.T) {
	if strings.TrimSpace(version) == "" {
		t.Fatal("version must not be empty")
	}
}
