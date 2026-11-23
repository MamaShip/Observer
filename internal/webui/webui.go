// Package webui serves the web app (web/, built by Vite into dist/app) from
// the binary: hashed assets with long caching, and index.html for every
// other path so the client-side router can handle it (docs/design.md §10).
package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// CSP is the Content-Security-Policy of the app's pages. Snapshots are
// embedded in an iframe from snapOrigin; nothing else is loaded from
// elsewhere, and no inline script or style is needed.
func CSP(snapOrigin string) string {
	return strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self' data:",
		"connect-src 'self'",
		"frame-src " + snapOrigin,
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

// Handler serves the embedded build with the given CSP on pages.
func Handler(csp string) http.Handler {
	app, _ := fs.Sub(dist, "dist/app") // never fails for a valid path
	return handler(app, csp)
}

func handler(app fs.FS, csp string) http.Handler {
	index, indexErr := fs.ReadFile(app, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if b, err := fs.ReadFile(app, name); err == nil {
				serveFile(w, r, name, b)
				return
			}
			// A missing asset is a 404, not the app: a stale page asking for
			// an old chunk must fail visibly.
			if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		if indexErr != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("The web app is not built into this binary. Run `make web` and rebuild.\n"))
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Set("Content-Security-Policy", csp)
		if r.Method == http.MethodGet {
			_, _ = w.Write(index)
		}
	})
}

func serveFile(w http.ResponseWriter, r *http.Request, name string, b []byte) {
	h := w.Header()
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
		h.Set("Content-Type", ct)
	} else {
		h.Set("Content-Type", http.DetectContentType(b))
	}
	if strings.HasPrefix(name, "assets/") {
		h.Set("Cache-Control", "public, max-age=31536000, immutable") // content-hashed names
	} else {
		h.Set("Cache-Control", "public, max-age=3600")
	}
	if r.Method == http.MethodGet {
		_, _ = w.Write(b)
	}
}
