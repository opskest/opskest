package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func gzipTestHandler(ct string) http.Handler {
	body := strings.Repeat(`{"name":"monitor","status":"up"}`, 200)
	return gzipResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ct)
		_, _ = io.WriteString(w, body)
	}))
}

func TestGzipResponses(t *testing.T) {
	t.Run("json is compressed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.Header.Set("Accept-Encoding", "gzip, br")
		gzipTestHandler("application/json").ServeHTTP(rec, req)
		if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Fatalf("headers = %v", rec.Header())
		}
		zr, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatal(err)
		}
		out, _ := io.ReadAll(zr)
		if !strings.HasPrefix(string(out), `{"name":"monitor"`) || rec.Body.Len() >= len(out) {
			t.Fatalf("not smaller: %d vs %d", rec.Body.Len(), len(out))
		}
	})
	t.Run("client without gzip gets plain", func(t *testing.T) {
		rec := httptest.NewRecorder()
		gzipTestHandler("application/json").ServeHTTP(rec, httptest.NewRequest("GET", "/api/x", nil))
		if rec.Header().Get("Content-Encoding") != "" {
			t.Fatal("compressed without Accept-Encoding")
		}
	})
	t.Run("streams and images are left alone", func(t *testing.T) {
		for _, c := range []struct{ path, ct, accept string }{
			{"/api/stream", "text/event-stream", ""},
			{"/api/public/pages/x/stream", "text/plain", ""},
			{"/api/y", "text/event-stream", "text/event-stream"},
			{"/logo.png", "image/png", ""},
		} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", c.path, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			if c.accept != "" {
				req.Header.Set("Accept", c.accept)
			}
			gzipTestHandler(c.ct).ServeHTTP(rec, req)
			if rec.Header().Get("Content-Encoding") != "" {
				t.Errorf("%s was compressed", c.path)
			}
		}
	})
}
