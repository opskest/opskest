package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func builtFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":        {Data: []byte("<html>index</html>")},
		"assets/app.abc.js": {Data: []byte("console.log('hi')")},
		"favicon.ico":       {Data: []byte("icon")},
	}
}

// dist/ is empty (only .gitkeep) in this task, so index.html does not
// exist and every request should fall through to the "UI not built" page.

func TestHandlerServesPlaceholderWhenDistEmpty(t *testing.T) {
	h := Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("expected a non-empty placeholder body")
	}
}

func TestHandlerServesPlaceholderForUnknownPath(t *testing.T) {
	h := Handler()

	req := httptest.NewRequest(http.MethodGet, "/monitors/123", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestNewHandlerServesKnownFile(t *testing.T) {
	h := newHandler(builtFS())

	req := httptest.NewRequest(http.MethodGet, "/favicon.ico", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "icon" {
		t.Fatalf("body = %q", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache-control = %q, want no-cache", got)
	}
}

func TestNewHandlerSPAFallback(t *testing.T) {
	h := newHandler(builtFS())

	req := httptest.NewRequest(http.MethodGet, "/monitors/123", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "<html>index</html>" {
		t.Fatalf("body = %q, want index.html contents", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache-control = %q, want no-cache", got)
	}
}

func TestNewHandlerAssetsLongCache(t *testing.T) {
	h := newHandler(builtFS())

	req := httptest.NewRequest(http.MethodGet, "/assets/app.abc.js", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("cache-control = %q", got)
	}
}

func TestNewHandlerIndexNoCache(t *testing.T) {
	h := newHandler(builtFS())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("cache-control = %q, want no-cache", got)
	}
}

func TestNewHandlerUnknownExtensionIs404NotFallback(t *testing.T) {
	// A path WITH an extension that doesn't exist is a genuine 404, not an
	// SPA route: only extensionless paths fall back to index.html.
	h := newHandler(builtFS())

	req := httptest.NewRequest(http.MethodGet, "/missing.png", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
