package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
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
	if len(doc.Paths) < 30 {
		t.Errorf("paths = %d, want the full fee surface (>= 30)", len(doc.Paths))
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
	if probed < 30 {
		t.Errorf("probed %d operations, want the full fee surface", probed)
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

// TestOpenAPISpecStatusProjection pins #103: the document projects each
// route's HONEST status set — the declared success status (201/202 where the
// handler answers them, 200 elsewhere), the derived defaults (400; 404 on
// id patterns; 401 on mutating verbs; 500), and the declared extras (501 on
// the engine-gated fees routes, 422 on the file-semantic importers, 409 on
// the conflict-carrying mutations). The old document claimed 200 for every
// route — a discovery lie for any agent client.
func TestOpenAPISpecStatusProjection(t *testing.T) {
	srv := newAPITestServer(t)
	resp, body := doRequest(t, srv, http.MethodGet, "/api/openapi.json", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi.json: %d", resp.StatusCode)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("openapi.json: %v", err)
	}
	get := func(path, method string) map[string]json.RawMessage {
		p, ok := doc.Paths[path]
		if !ok {
			t.Fatalf("path %s missing from the document", path)
		}
		op, ok := p[method]
		if !ok {
			t.Fatalf("%s %s missing from the document", method, path)
		}
		return op.Responses
	}

	// The success-status truths verified against the handlers (2026-10-21):
	created := []struct{ path, method string }{
		{"/api/fees/imports", "post"},
		{"/api/fees/imports/sheets", "post"},
		{"/api/imports/{id}/template", "post"},
	}
	for _, c := range created {
		if _, ok := get(c.path, c.method)["201"]; !ok {
			t.Errorf("%s %s: no 201 in responses — the handler answers StatusCreated", c.method, c.path)
		}
		if _, ok := get(c.path, c.method)["200"]; ok {
			t.Errorf("%s %s: still claims 200 — the success status is 201", c.method, c.path)
		}
	}
	accepted := []struct{ path, method string }{
		{"/api/imports", "post"},
		{"/api/imports/{id}/mapping", "post"},
		{"/api/metrics/imports", "post"},
	}
	for _, a := range accepted {
		if _, ok := get(a.path, a.method)["202"]; !ok {
			t.Errorf("%s %s: no 202 in responses — the handler answers StatusAccepted", a.method, a.path)
		}
		if _, ok := get(a.path, a.method)["200"]; ok {
			t.Errorf("%s %s: still claims 200 — the success status is 202", a.method, a.path)
		}
	}
	// 501 engine_not_built: the three engine-gated fees routes.
	for _, path := range []string{"/api/fees/query/totals", "/api/fees/query/duplicates", "/api/fees/risks/{scenario}"} {
		if _, ok := get(path, "get")["501"]; !ok {
			t.Errorf("GET %s: no 501 — default builds answer engine_not_built", path)
		}
	}
	// 422 on the file-semantic importers.
	for _, path := range []string{"/api/imports", "/api/metrics/imports", "/api/fees/imports"} {
		if _, ok := get(path, "post")["422"]; !ok {
			t.Errorf("POST %s: no 422 — semantic file errors answer 422", path)
		}
	}
	// 409 on the conflict-carrying mutations.
	for _, tc := range []struct{ path, method string }{
		{"/api/batches/{id}", "delete"},
	} {
		if _, ok := get(tc.path, tc.method)["409"]; !ok {
			t.Errorf("%s %s: no 409 — the handler answers conflicts", tc.method, tc.path)
		}
	}
	// Derived defaults: 404 on {id} patterns; 401 on mutations; 400+500 everywhere.
	if _, ok := get("/api/batches/{id}", "get")["404"]; !ok {
		t.Error("GET /api/batches/{id}: no 404")
	}
	if _, ok := get("/api/batches/{id}", "get")["401"]; ok {
		t.Error("GET /api/batches/{id}: 401 must not appear on read routes")
	}
	if _, ok := get("/api/fees/imports", "post")["401"]; !ok {
		t.Error("POST /api/fees/imports: no 401 — the --auth-token write gate covers mutations")
	}
	if _, ok := get("/api/health", "get")["400"]; !ok {
		t.Error("GET /api/health: no 400")
	}
	if _, ok := get("/api/health", "get")["500"]; !ok {
		t.Error("GET /api/health: no 500")
	}
	// Read routes never claim 401; the batches DELETE carries the
	// in-use protection 409.
	if _, ok := get("/api/resources", "get")["401"]; ok {
		t.Error("GET /api/resources: 401 must not appear on read routes")
	}
	if _, ok := get("/api/batches/{id}", "delete")["409"]; !ok {
		t.Error("DELETE /api/batches/{id}: no 409 — the in-use protection answers conflicts")
	}
}
