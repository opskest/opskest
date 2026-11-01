// Package webui serves the embedded, prebuilt React single-page app.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

const uiNotBuilt = "UI not built. Run scripts/web to build web/ into internal/webui/dist.\n"

// Handler serves the embedded dist/ directory as a single-page app: an
// unknown path with no file extension falls back to index.html. If
// dist/index.html hasn't been built yet, it serves a plain-text
// placeholder with status 200 instead of a 404.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// dist is embedded at build time via go:embed; this can only fail
		// if the embed directive itself is broken.
		panic(err)
	}
	return newHandler(sub)
}

// newHandler builds the handler over fsys. Split out from Handler so tests
// can exercise the full SPA-fallback and cache-header behavior against an
// in-memory filesystem, since the real embedded dist/ is empty until the
// frontend is built (a later task).
func newHandler(sub fs.FS) http.Handler {
	hasIndex := fileExists(sub, "index.html")
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasIndex {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(uiNotBuilt))
			return
		}

		p := r.URL.Path
		// Unknown API paths are a 404, never the app shell: a client (or a
		// probe) must be able to tell a missing endpoint from a page.
		if p == "/api" || strings.HasPrefix(p, "/api/") {
			http.NotFound(w, r)
			return
		}
		known := fileExists(sub, strings.TrimPrefix(p, "/"))

		if strings.HasPrefix(p, "/assets/") && known {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && path.Ext(p) == "" && !known && p != "/" {
			// SPA fallback: unknown, extensionless paths are client-side
			// routes, so serve index.html for them. Rewriting to "/" (not
			// "/index.html") lets FileServer serve the directory index
			// inline instead of issuing its built-in redirect for paths
			// ending in "/index.html".
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}

		fileServer.ServeHTTP(w, r)
	})
}

func fileExists(fsys fs.FS, name string) bool {
	if name == "" {
		name = "."
	}
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}
