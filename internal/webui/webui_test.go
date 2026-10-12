package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// fakeTree builds an in-memory dist tree covering every serving branch:
// SPA shell, hashed asset, ES-module worker, pre-compressed map data.
func fakeTree() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                           &fstest.MapFile{Data: []byte("<html>netops-lab</html>")},
		"assets/index-abc123.js":               &fstest.MapFile{Data: []byte("main chunk")},
		"assets/maplibre-gl-worker-abc123.mjs": &fstest.MapFile{Data: []byte("worker")},
		"mapdata/hlj-railways.geojson.gz":      &fstest.MapFile{Data: []byte("gzip-json-bytes")},
		"favicon.svg":                          &fstest.MapFile{Data: []byte("<svg/>")},
	}
}

func TestServeTreeMimePins(t *testing.T) {
	h := serveTree(fakeTree())
	ts := httptest.NewServer(h)
	defer ts.Close()

	cases := []struct {
		path     string
		wantType string
		wantBody string
	}{
		{"/assets/maplibre-gl-worker-abc123.mjs", "text/javascript; charset=utf-8", "worker"},
		{"/assets/index-abc123.js", "text/javascript; charset=utf-8", "main chunk"},
	}
	for _, tc := range cases {
		resp, err := http.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		defer resp.Body.Close()
		if got := resp.Header.Get("Content-Type"); got != tc.wantType {
			t.Errorf("GET %s Content-Type = %q, want %q", tc.path, got, tc.wantType)
		}
	}
}

func TestServeTreeGeojsonGz(t *testing.T) {
	h := serveTree(fakeTree())
	ts := httptest.NewServer(h)
	defer ts.Close()

	// The Go HTTP client transparently decompresses Content-Encoding: gzip
	// and strips the header — disable that to inspect the raw wire format,
	// which is what non-Go clients (browsers, MapLibre fetch) receive.
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Get(ts.URL + "/mapdata/hlj-railways.geojson.gz")
	if err != nil {
		t.Fatalf("GET mapdata: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/geo+json" {
		t.Errorf("Content-Type = %q, want application/geo+json", got)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("Cache-Control = %q, want max-age caching for mapdata", cc)
	}
}

func TestServeTreeSPAFallback(t *testing.T) {
	h := serveTree(fakeTree())
	ts := httptest.NewServer(h)
	defer ts.Close()

	// Unknown route → SPA shell (client-side routing), not 404/loop.
	resp, err := http.Get(ts.URL + "/lab/some-workspace")
	if err != nil {
		t.Fatalf("GET spa route: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SPA fallback)", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html (SPA shell)", got)
	}

	// Directory paths (asset dirs, trailing slash) must fall back to the SPA
	// shell too — never a directory listing.
	for _, dir := range []string{"/assets", "/assets/", "/mapdata/"} {
		resp, err := http.Get(ts.URL + dir)
		if err != nil {
			t.Fatalf("GET %s: %v", dir, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200 (SPA fallback)", dir, resp.StatusCode)
		}
	}
}

// TestHandlerRealDistSmoke runs against the real embedded build output so the
// MIME pin is verified on the actual MapLibre worker emitted by Vite (the
// file the Windows MIME defect broke, issue #2).
func TestHandlerRealDistSmoke(t *testing.T) {
	h := Handler()
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: err=%v status=%v", err, resp)
	}
	resp.Body.Close()

	// Find the hashed worker file in the embedded tree.
	entries, err := distFS.ReadDir("dist/assets")
	if err != nil {
		t.Fatalf("readdir assets: %v", err)
	}
	found := false
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".mjs") {
			continue
		}
		found = true
		resp, err := http.Get(ts.URL + "/assets/" + e.Name())
		if err != nil {
			t.Fatalf("GET worker: %v", err)
		}
		got := resp.Header.Get("Content-Type")
		resp.Body.Close()
		if got != "text/javascript; charset=utf-8" {
			t.Errorf("worker %s Content-Type = %q, want text/javascript (issue #2)", e.Name(), got)
		}
	}
	if !found {
		t.Skip("no .mjs asset in embedded dist (frontend not built with map worker)")
	}
}

// TestHandlerRealDistDEMTiles verifies the embedded offline terrain tiles
// (W17: web/public/mapdata/dem, 97 terrarium PNGs) serve with the PNG MIME
// type and the immutable mapdata caching — the contract the maplibre
// raster-dem source depends on.
func TestHandlerRealDistDEMTiles(t *testing.T) {
	h := Handler()
	ts := httptest.NewServer(h)
	defer ts.Close()

	// A real z5 tile inside the Heilongjiang bbox (see
	// scripts/download_dem_tiles.py for the ranges).
	resp, err := http.Get(ts.URL + "/mapdata/dem/5/27/10.png")
	if err != nil {
		t.Fatalf("GET dem tile: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET dem tile: status = %d, want 200 (tile missing from dist?)", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("dem tile Content-Type = %q, want image/png", got)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age") {
		t.Errorf("dem tile Cache-Control = %q, want max-age caching for mapdata", cc)
	}
	// PNG magic bytes (a valid terrarium tile, not an error page).
	var magic [4]byte
	if _, err := resp.Body.Read(magic[:]); err != nil {
		t.Fatalf("read dem tile magic: %v", err)
	}
	if magic != [4]byte{0x89, 'P', 'N', 'G'} {
		t.Errorf("dem tile magic = %v, want PNG signature", magic)
	}
}
