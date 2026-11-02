package api

import (
	"net/http"
	"strings"
)

// Search engines should find the public status pages and nothing else: the
// dashboard, sign-in, settings, the API, badges and every private page are
// not for search results. One index.html serves every route, so a fixed tag
// in it would hide the public pages too; the decision is made per request.
//
// X-Robots-Tag is honoured by Google and Bing for any response type (HTML,
// JSON, SVG). robots.txt deliberately does not block the pages themselves:
// a crawler that is not allowed to fetch a page cannot see its noindex
// header and may still list the bare address. Only /api/ is disallowed, as
// it holds data and images, not pages.

const (
	robotsNoIndex = "noindex, nofollow, noarchive"
	robotsTxt     = "User-agent: *\nAllow: /status/\nDisallow: /api/\n"
)

// robotsPolicy answers /robots.txt and marks every response noindex except
// the pages of public status pages.
func (s *server) robotsPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			_, _ = w.Write([]byte(robotsTxt))
			return
		}
		if !s.indexable(r) {
			w.Header().Set("X-Robots-Tag", robotsNoIndex)
		}
		next.ServeHTTP(w, r)
	})
}

// indexable reports whether r asks for a page of a public status page: its
// overview, history or an incident. Subscribe confirmation, unsubscribe and
// the wallboard are for people who were sent there, not for search results.
func (s *server) indexable(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/status/")
	if !ok {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	slug := parts[0]
	if slug == "" {
		return false
	}
	switch {
	case len(parts) == 1:
	case len(parts) == 2 && parts[1] == "history":
	case len(parts) == 3 && parts[1] == "incidents" && parts[2] != "":
	default:
		return false
	}
	page, err := s.d.Store.GetPageBySlug(r.Context(), slug)
	return err == nil && page.Visibility == "public"
}

// notFoundWriter turns the first status the wrapped handler writes into a 404
// so the SPA shell can answer for a view that does not exist.
type notFoundWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *notFoundWriter) WriteHeader(int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.ResponseWriter.WriteHeader(http.StatusNotFound)
}

func (w *notFoundWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusNotFound)
	return w.ResponseWriter.Write(b)
}

// boardGate serves the wallboard view of /status/{slug}/board only for a page
// created with the board layout. For a list page, and for an address that is
// no page at all, the answer is the same 404 (the layout is never revealed
// beyond what the public page JSON already says).
func (s *server) boardGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, err := s.d.Store.GetPageBySlug(r.Context(), r.PathValue("slug"))
		if err != nil || page.Layout != "board" {
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(&notFoundWriter{ResponseWriter: w}, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
