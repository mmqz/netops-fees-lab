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
	"path"
	"strings"

	"embed"
)

// distFS is the embedded frontend build output. `all:` includes files that
// begin with "." or "_" so the .gitkeep placeholder embeds too.
//
//go:embed all:dist
var distFS embed.FS

// mimeOverrides pins modern asset types that mime.TypeByExtension may miss.
// On Windows the Go runtime consults the registry table, where entries like
// .mjs are commonly absent (falls back to text/plain), which makes browsers
// reject the MapLibre worker as an ES Module (issue #2). On Linux the builtin
// table covers .mjs, so this is a cross-platform no-op there — the explicit
// map keeps every platform identical.
var mimeOverrides = map[string]string{
	".mjs":     "text/javascript; charset=utf-8",
	".js":      "text/javascript; charset=utf-8",
	".geojson": "application/geo+json",
	".wasm":    "application/wasm",
	".webp":    "image/webp",
	".avif":    "image/avif",
}

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
	return serveTree(sub)
}

// serveTree serves the dist tree with three cross-platform behaviors that
// http.FileServerFS does not provide on its own:
//
//   - pinned MIME types for modern assets (.mjs & friends; see mimeOverrides);
//   - pre-compressed *.geojson.gz map data served with Content-Encoding: gzip
//     so browsers decompress transparently while MapLibre fetches JSON;
//   - modest caching for the immutable mapdata/ assets (hash-less filenames,
//     refreshed per release) — no-store stays an /api-only contract (ADR-0006).
func serveTree(sub fs.FS) http.Handler {
	fileServer := http.FileServerFS(sub)
	index, errIndex := fs.ReadFile(sub, "index.html")
	hasIndex := errIndex == nil

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}
		if info, err := fs.Stat(sub, path.Clean(reqPath)); err != nil || info.IsDir() {
			// Unknown path (or a directory — including a trailing-slash
			// request to an asset directory): SPA fallback to index.html.
			// This also keeps FileServerFS from ever emitting a directory
			// listing for /assets/ or /mapdata/.
			if !hasIndex {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(index)
			return
		}

		// Pre-compressed GeoJSON (web/public/mapdata → dist/mapdata): the
		// .gz suffix must be hidden from the client — declare the payload
		// inside the archive and let the browser transparently gunzip.
		// The pre-set Content-Type/Content-Encoding win over whatever the
		// .gz extension would otherwise make ServeContent guess.
		if strings.HasSuffix(reqPath, ".geojson.gz") {
			w.Header().Set("Content-Type", "application/geo+json")
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			fileServer.ServeHTTP(w, r)
			return
		}

		if ctype, ok := mimeOverrides[strings.ToLower(path.Ext(reqPath))]; ok {
			w.Header().Set("Content-Type", ctype)
		}
		fileServer.ServeHTTP(w, r)
	})
}
