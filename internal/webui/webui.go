// Package webui embeds the built frontend (the Vite output directory
// internal/webui/dist) and serves it as a single-page application: unknown
// non-API paths fall back to index.html so client-side routes (/, /import,
// /resources, …) work on hard navigation.
//
// The dist directory is filled by the frontend build (web/ →
// `vite build --outDir ../internal/webui/dist`, T8); until then it only
// contains the committed .gitkeep placeholder and the handler serves 404s
// for everything but the root (which the API mux wires to this handler).
package webui

import (
	"io/fs"
	"net/http"
	"strings"

	"embed"
)

// distFS is the embedded frontend build output. `all:` includes files that
// begin with "." or "_" so the .gitkeep placeholder embeds too.
//
//go:embed all:dist
var distFS embed.FS

// Handler returns the SPA file server over the embedded dist tree: existing
// files are served as-is, directory paths resolve to index.html, and any
// unknown path falls back to index.html (client-side routing). When the
// frontend has not been built yet (no index.html in dist), unknown paths
// answer 404 instead of looping.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Cannot happen: the embed directive guarantees the subtree exists.
		panic("webui: embedded dist tree is missing: " + err.Error())
	}
	fileServer := http.FileServerFS(sub)
	index, errIndex := fs.ReadFile(sub, "index.html")
	hasIndex := errIndex == nil

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(sub, path); err != nil {
			// Unknown path (or a directory): SPA fallback to index.html.
			if !hasIndex {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(index)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
