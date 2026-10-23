package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/app"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// gateProbeBody is the well-formed risk-clue decision the write-gate tests
// POST: valid closed-set scenario/feeType/status + in-range period, so the
// ONLY variable under test is the gate itself (the decision UPSERTs
// standalone — engine-independent, #119).
const gateProbeBody = `{"scenario":"spike","feeType":"electricity","siteCode":"GATE-PROBE","year":2026,"month":1,"status":"verified","note":"write-gate probe"}`

// newAuthTestServer boots a server with the write gate configured.
func newAuthTestServer(t *testing.T, token string) (*http.Client, string) {
	t.Helper()
	dir := testsupport.TempDir(t)
	a, err := app.New(app.Config{
		DBPath:    filepath.Join(dir, "netops.db"),
		DataDir:   dir,
		AuthToken: token,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	srv := newAPITestServerFromApp(t, a)
	return srv.Client(), srv.URL
}

// TestAuthTokenWriteGate pins the opt-in write gate matrix: POSTs without a
// token are 401 unauthorized; the correct Bearer passes; a wrong token
// fails; reads / health / OpenAPI / the SPA root never require the token;
// and the 401 uses the standard error envelope.
func TestAuthTokenWriteGate(t *testing.T) {
	const token = "r13-secret-token"
	client, base := newAuthTestServer(t, token)

	// The 401 envelope: standard error shape, code unauthorized.
	resp, err := client.Post(base+"/api/fees/risk-clue-decisions", "application/json", bytes.NewReader([]byte(gateProbeBody)))
	if err != nil {
		t.Fatalf("post without token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST without token: status = %d, want 401", resp.StatusCode)
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode 401 body: %v", err)
	}
	if envelope.Error.Code != "unauthorized" {
		t.Errorf("401 code = %q, want unauthorized", envelope.Error.Code)
	}

	// Reads stay open (the demo/AI surface).
	for _, path := range []string{"/api/health", "/api/fees/batches", "/api/openapi.json", "/"} {
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s (gated server) = %d, want 200 (reads open)", path, resp.StatusCode)
		}
	}

	// The correct Bearer passes the gate and records the decision.
	req, err := http.NewRequest(http.MethodPost, base+"/api/fees/risk-clue-decisions", bytes.NewReader([]byte(gateProbeBody)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("post with token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST with correct token: status = %d, want 200", resp.StatusCode)
	}
	var saved struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&saved); err != nil {
		t.Fatalf("decode decision: %v", err)
	}
	if saved.ID == "" {
		t.Error("decision envelope id empty, want the receipt")
	}

	// A wrong token is still 401 (constant-time compare, but the verdict
	// is the same).
	req, err = http.NewRequest(http.MethodPost, base+"/api/fees/risk-clue-decisions", bytes.NewReader([]byte(gateProbeBody)))
	if err != nil {
		t.Fatalf("build wrong-token request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer not-the-token")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("post with wrong token: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST with wrong token: status = %d, want 401", resp.StatusCode)
	}

	// Non-Bearer schemes do not pass either.
	req, err = http.NewRequest(http.MethodPost, base+"/api/fees/risk-clue-decisions", bytes.NewReader([]byte(gateProbeBody)))
	if err != nil {
		t.Fatalf("build basic-auth request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+token)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("post with basic scheme: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST with Basic scheme: status = %d, want 401 (Bearer only)", resp.StatusCode)
	}
}

// TestAuthOffByDefault pins the OFF channel: no --auth-token means the
// ungated behavior — a bare POST succeeds (zero-diff for existing users).
func TestAuthOffByDefault(t *testing.T) {
	srv := newAPITestServer(t)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/fees/risk-clue-decisions", []byte(gateProbeBody), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bare POST on ungated server: %d\n%s", resp.StatusCode, body)
	}
}
