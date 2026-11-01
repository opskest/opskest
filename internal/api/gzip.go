package api

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

var gzipPool = sync.Pool{New: func() any {
	zw, _ := gzip.NewWriterLevel(nil, 5)
	return zw
}}

// compressible reports whether a response of this content type shrinks
// enough to be worth compressing. Images and fonts are already compressed.
func compressible(ct string) bool {
	ct = strings.ToLower(ct)
	for _, p := range []string{"text/", "application/json", "application/javascript", "application/xml", "application/atom+xml", "image/svg+xml"} {
		if strings.HasPrefix(ct, p) {
			return ct != "text/event-stream"
		}
	}
	return false
}

// gzipResponses compresses JSON, HTML, CSS, JavaScript and SVG for clients
// that ask for it. A status page's JSON runs to a few hundred KB and the
// app bundle to about 230 KB, so this is most of the load time on a slow
// link. Live streams are never wrapped: a buffering writer would hold events
// back.
func gzipResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || r.Header.Get("Upgrade") != "" ||
			strings.HasSuffix(r.URL.Path, "/stream") ||
			strings.Contains(r.Header.Get("Accept"), "text/event-stream") ||
			!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	zw      *gzip.Writer
	decided bool
	status  int
}

func (g *gzipWriter) decide() {
	if g.decided {
		return
	}
	g.decided = true
	h := g.Header()
	if h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" || g.status == http.StatusNoContent ||
		g.status == http.StatusNotModified || g.status == http.StatusPartialContent || !compressible(h.Get("Content-Type")) {
		return
	}
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	g.zw = gzipPool.Get().(*gzip.Writer)
	g.zw.Reset(g.ResponseWriter)
}

func (g *gzipWriter) WriteHeader(code int) {
	if g.status == 0 {
		g.status = code
	}
	g.decide()
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if g.status == 0 {
		g.status = http.StatusOK
	}
	g.decide()
	if g.zw != nil {
		return g.zw.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipWriter) Flush() {
	g.decide()
	if g.zw != nil {
		_ = g.zw.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipWriter) finish() {
	if g.zw != nil {
		_ = g.zw.Close()
		gzipPool.Put(g.zw)
		g.zw = nil
	}
}
