package api_test

// auth_admin_test.go — the two-tier privilege model of issue #9 (v0.18):
// --auth-token (declarer) / --admin-token (admin). The matrix under test:
//
//	both tokens   : declarer → 403 on Admin routes, OK on plain writes;
//	                admin → OK everywhere; missing/wrong → 401.
//	auth only     : single-operator compat — the one token keeps its v0.11
//	                full access (Admin routes included, nobody is locked out).
//	admin only    : the admin token is the single accepted write token.
//
// The Admin-route reachability probe uses a nonexistent id where the route
// carries one: the wrapper runs BEFORE the handler, so 403 = refused at the
// gate while 404 = the tier passed and the handler answered "no such
// object" — no seeding needed to prove the privilege verdict.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/app"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// newTwoTierTestServer boots a server with the write gate configured from
// the given tokens ("" = that tier is off).
func newTwoTierTestServer(t *testing.T, authToken, adminToken string) (*http.Client, string) {
	t.Helper()
	dir := testsupport.TempDir(t)
	a, err := app.New(app.Config{
		DBPath:     filepath.Join(dir, "netops.db"),
		DataDir:    dir,
		AuthToken:  authToken,
		AdminToken: adminToken,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	srv := newAPITestServerFromApp(t, a)
	return srv.Client(), srv.URL
}

// doBearer issues a request with the given bearer token ("" = none).
func doBearer(t *testing.T, client *http.Client, method, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// TestTwoTierAdminRoutes pins the issue #9 acceptance matrix: with both
// tokens configured the Admin-flagged routes (delete/rollback/审核) answer
// 403 forbidden for the declarer token and let the admin token through;
// plain writes accept either tier; unknown tokens stay 401.
func TestTwoTierAdminRoutes(t *testing.T) {
	const declarer = "i9-declarer-token"
	const admin = "i9-admin-token"
	client, base := newTwoTierTestServer(t, declarer, admin)

	// The acceptance probe pair of issue #9: DELETE /api/batches/{id}.
	// Declarer → 403 forbidden (valid token, insufficient tier)…
	resp := doBearer(t, client, http.MethodDelete, base+"/api/batches/bat_i9_none", declarer)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("DELETE batch with declarer token: status = %d, want 403", resp.StatusCode)
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode 403 body: %v", err)
	}
	if envelope.Error.Code != "forbidden" {
		t.Errorf("403 code = %q, want forbidden", envelope.Error.Code)
	}
	if !strings.Contains(envelope.Error.Message, "管理") {
		t.Errorf("403 message = %q, want it to name the admin tier", envelope.Error.Message)
	}

	// …admin → the tier passes; the bogus id then answers 404 from the
	// handler itself (proof the wrapper let the request through).
	resp = doBearer(t, client, http.MethodDelete, base+"/api/batches/bat_i9_none", admin)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE batch with admin token: status = %d, want 404 (tier passed, object absent)", resp.StatusCode)
	}

	// The other Admin-classified routes refuse the declarer the same way
	// (the classification lives in the route table — one wrapper, all
	// routes).
	for _, target := range []struct{ method, path string }{
		{http.MethodDelete, "/api/mapping-templates/mt_i9_none"},
		{http.MethodPost, "/api/metrics/batches/mbt_i9_none/rollback"},
		{http.MethodPost, "/api/fees/batches/fb_i9_none/rollback"},
		{http.MethodPost, "/api/fees/links/lnk_i9_none/confirm"},
		{http.MethodPost, "/api/fees/links/lnk_i9_none/dismiss"},
	} {
		resp := doBearer(t, client, target.method, base+target.path, declarer)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s with declarer token: status = %d, want 403", target.method, target.path, resp.StatusCode)
		}
	}

	// The {id}-less Admin route — the clue-decision POST: the declarer is
	// refused at the gate (403), the admin's well-formed probe body
	// answers 200 (standalone UPSERT, #119).
	resp = doBearer(t, client, http.MethodPost, base+"/api/fees/risk-clue-decisions", declarer)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST clue-decision with declarer token: status = %d, want 403", resp.StatusCode)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/fees/risk-clue-decisions", bytes.NewReader([]byte(gateProbeBody)))
	if err != nil {
		t.Fatalf("build admin probe: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+admin)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST clue-decision with admin token: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("POST clue-decision with admin token: status = %d, want 200", resp2.StatusCode)
	}

	// Plain writes accept BOTH tiers (the declarer's everyday work: the
	// validate/scan family stays declarer-level).
	for _, token := range []string{declarer, admin} {
		for _, path := range []string{"/api/validate", "/api/fees/links/scan"} {
			resp := doBearer(t, client, http.MethodPost, base+path, token)
			if resp.StatusCode != http.StatusOK {
				t.Errorf("POST %s with %q: status = %d, want 200", path, token, resp.StatusCode)
			}
		}
	}

	// Missing / wrong tokens stay 401 at the first tier — on Admin and
	// plain routes alike.
	for _, target := range []struct{ method, path string }{
		{http.MethodDelete, "/api/batches/bat_i9_none"},
		{http.MethodPost, "/api/validate"},
		{http.MethodPost, "/api/fees/risk-clue-decisions"},
	} {
		resp := doBearer(t, client, target.method, base+target.path, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without token: status = %d, want 401", target.method, target.path, resp.StatusCode)
		}
		resp = doBearer(t, client, target.method, base+target.path, "i9-wrong-token")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s with wrong token: status = %d, want 401", target.method, target.path, resp.StatusCode)
		}
	}
}

// TestTwoTierSingleOperatorCompat pins the backward-compat row: with ONLY
// --auth-token configured the v0.11 behavior is preserved — that one token
// keeps full access to the Admin routes too (an operator cannot be locked
// out of destructive routes by forgetting the second flag).
func TestTwoTierSingleOperatorCompat(t *testing.T) {
	const only = "i9-single-operator"
	client, base := newTwoTierTestServer(t, only, "")

	resp := doBearer(t, client, http.MethodDelete, base+"/api/batches/bat_i9_none", only)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE batch with the single auth token: status = %d, want 404 (full access kept, object absent)", resp.StatusCode)
	}
	// A stranger is still locked out of everything mutating.
	resp = doBearer(t, client, http.MethodDelete, base+"/api/batches/bat_i9_none", "i9-not-configured")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("DELETE batch with a foreign token: status = %d, want 401", resp.StatusCode)
	}
}

// TestTwoTierAdminOnlyGate pins the third configuration row: with ONLY
// --admin-token configured, that token is the single accepted write token
// on every mutating route (there is no declarer tier to serve).
func TestTwoTierAdminOnlyGate(t *testing.T) {
	const admin = "i9-admin-only"
	client, base := newTwoTierTestServer(t, "", admin)

	resp := doBearer(t, client, http.MethodPost, base+"/api/validate", admin)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST validate with the admin token: status = %d, want 200", resp.StatusCode)
	}
	resp = doBearer(t, client, http.MethodDelete, base+"/api/batches/bat_i9_none", admin)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("DELETE batch with the admin token: status = %d, want 404 (tier passed)", resp.StatusCode)
	}
	resp = doBearer(t, client, http.MethodPost, base+"/api/validate", "i9-anyone-else")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST validate with a foreign token: status = %d, want 401", resp.StatusCode)
	}
}
