package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// This file pins the v0.10-R9 OpenAPI contract: the document at
// /api/openapi.json is the faithful projection of the single route
// table — no drift between the spec, the mux and the live server.

type specDoc struct {
	OpenAPI string                    `json:"openapi"`
	Info    map[string]any            `json:"info"`
	Tags    []map[string]string       `json:"tags"`
	Paths   map[string]map[string]any `json:"paths"`
}

// specMethodsOf extracts the registered methods of one spec path.
func specMethodsOf(doc *specDoc, path string) []string {
	var methods []string
	item, ok := doc.Paths[path]
	if !ok {
		return nil
	}
	for _, m := range []string{"get", "post", "patch", "delete", "put"} {
		if _, ok := item[m]; ok {
			methods = append(methods, strings.ToUpper(m))
		}
	}
	return methods
}

// TestOpenAPISpecBasics pins the document envelope: valid OpenAPI 3.0,
// the app version (the SAME constant /api/health reports), tags and
// every operation carrying a summary (a route without a summary is a
// registration bug, the table makes it impossible to compile but the
// marshal could still drop it).
func TestOpenAPISpecBasics(t *testing.T) {
	srv := newAPITestServer(t)

	resp, body := doRequest(t, srv, http.MethodGet, "/api/openapi.json", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi.json: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var doc specDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.0") {
		t.Errorf("openapi = %q, want 3.0.x", doc.OpenAPI)
	}
	if v, _ := doc.Info["version"].(string); v != domain.AppVersion {
		t.Errorf("info.version = %q, want %q (the health constant)", v, domain.AppVersion)
	}
	if len(doc.Tags) == 0 {
		t.Error("no tags in the document")
	}

	// Every operation carries a non-empty summary.
	missing := 0
	for path, item := range doc.Paths {
		for m, op := range item {
			oper, _ := op.(map[string]any)
			sum, _ := oper["summary"].(string)
			if sum == "" {
				t.Errorf("%s %s has no summary", strings.ToUpper(m), path)
				missing++
			}
		}
	}
	if missing > 0 {
		t.Fatalf("%d operations without summaries", missing)
	}
	if len(doc.Paths) < 50 {
		t.Errorf("paths = %d, want the full surface (>= 50)", len(doc.Paths))
	}
}

// TestOpenAPISpecMatchesLiveServer is the ANTI-DRIFT walk: every
// (path, method) of the document is probed against the live server
// with placeholder path params — a registered route answers anything
// but the unknown-path fallback 404 (whose message is the fixed
// "未知的 API 路径" string). A route present in the spec but absent
// from the mux fails here; the reverse direction is impossible because
// both are generated from the same table.
func TestOpenAPISpecMatchesLiveServer(t *testing.T) {
	srv := newAPITestServer(t)

	resp, body := doRequest(t, srv, http.MethodGet, "/api/openapi.json", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi.json: %d", resp.StatusCode)
	}
	var doc specDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("openapi.json: %v", err)
	}

	// The known fallback message prefix of unregistered /api/ paths.
	const unknown = "未知的 API 路径"
	probed := 0
	for path, item := range doc.Paths {
		for m := range item {
			method := strings.ToUpper(m)
			// Replace path params with a benign placeholder id.
			probe := path
			probe = strings.ReplaceAll(probe, "{id}", "zz_probe")
			probe = strings.ReplaceAll(probe, "{runId}", "zz_probe")
			probe = strings.ReplaceAll(probe, "{cpId}", "zz_probe")
			var respProbe *http.Response
			var bodyProbe []byte
			switch method {
			case http.MethodGet:
				respProbe, bodyProbe = doRequest(t, srv, http.MethodGet, probe, nil, "")
			case http.MethodPost, http.MethodPatch, http.MethodDelete:
				respProbe, bodyProbe = doRequest(t, srv, method, probe, []byte(`{}`), "application/json")
			default:
				continue
			}
			probed++
			if strings.Contains(string(bodyProbe), unknown) {
				t.Errorf("%s %s: the mux does not serve this documented route (fallback 404)\n%s",
					method, probe, bodyProbe)
			}
			if respProbe.StatusCode == http.StatusNotFound && !strings.Contains(string(bodyProbe), unknown) {
				// A registered 404 (e.g. probe id not found) is fine —
				// it proves the route matched and the handler answered.
				_ = respProbe
			}
		}
	}
	if probed < 50 {
		t.Errorf("probed %d operations, want the full surface", probed)
	}
}

// TestOpenAPISpecDeterministic pins the memoization: two requests
// return byte-identical documents (the cache is built once; a rebuilt
// document would also be deterministic, but the cache is the contract
// for cheap repeated reads by AI agents).
func TestOpenAPISpecDeterministic(t *testing.T) {
	srv := newAPITestServer(t)
	_, first := doRequest(t, srv, http.MethodGet, "/api/openapi.json", nil, "")
	_, second := doRequest(t, srv, http.MethodGet, "/api/openapi.json", nil, "")
	if string(first) != string(second) {
		t.Fatal("two openapi.json reads differ — the memoized document must be stable")
	}
	if len(first) < 1024 {
		t.Errorf("document is suspiciously small: %d bytes", len(first))
	}
}
