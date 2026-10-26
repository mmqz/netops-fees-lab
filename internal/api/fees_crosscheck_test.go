package api_test

// fees_crosscheck_test.go — #12's HTTP channel matrix: alias create =
// declarer tier (the human confirm channel, audited), remove = admin
// tier (governance), list/scan/cross-reconcile read-open (scan is a
// POST shape with a read's heart — the engine face answers 501 on
// default builds).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestFeesAliasesHTTPChannelMatrix(t *testing.T) {
	const declarer, admin = "decl-1", "adm-1"
	client, base := newTwoTierTestServer(t, declarer, admin)

	// no token: 401.
	resp := postDetailJSON(t, client, http.MethodPost, base+"/api/fees/site-code-aliases", "",
		`{"canonical":"HLJ-0001","alias":"hlj001","source":"tower_lease","confidence":"rule"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token create: %d", resp.StatusCode)
	}
	// declarer: 201 with the audit columns.
	resp = postDetailJSON(t, client, http.MethodPost, base+"/api/fees/site-code-aliases", declarer,
		`{"canonical":"HLJ-0001","alias":"hlj001","source":"tower_lease","confidence":"rule"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("declarer create: %d\n", resp.StatusCode)
	}
	item := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		t.Fatal(err)
	}
	id, _ := item["id"].(string)
	if item["createdBy"] != "declarer" || id == "" {
		t.Fatalf("created = %v", item)
	}
	// duplicate triple: 422.
	resp = postDetailJSON(t, client, http.MethodPost, base+"/api/fees/site-code-aliases", declarer,
		`{"canonical":"HLJ-0001","alias":"hlj001","source":"tower_lease","confidence":"manual"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate: %d, want 422", resp.StatusCode)
	}
	// mandate refusals: 422 the shared vocabulary.
	for _, bad := range []string{
		`{"canonical":"","alias":"x","source":"manual","confidence":"manual"}`,
		`{"canonical":"x","alias":"x","source":"manual","confidence":"manual"}`,
		`{"canonical":"x","alias":"y","source":"groceries","confidence":"manual"}`,
		`{"canonical":"x","alias":"y","source":"manual","confidence":"bogus"}`,
	} {
		resp = postDetailJSON(t, client, http.MethodPost, base+"/api/fees/site-code-aliases", declarer, bad)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("refusal %s: %d, want 422", bad[:40], resp.StatusCode)
		}
	}
	// list: read open.
	resp = postDetailJSON(t, client, http.MethodGet, base+"/api/fees/site-code-aliases", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open list: %d", resp.StatusCode)
	}
	// remove at the declarer tier: 403 (governance is admin-only).
	resp = postDetailJSON(t, client, http.MethodDelete,
		fmt.Sprintf("%s/api/fees/site-code-aliases/%s", base, id), declarer, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("declarer remove: %d, want 403", resp.StatusCode)
	}
	// remove unknown: 404.
	resp = postDetailJSON(t, client, http.MethodDelete,
		base+"/api/fees/site-code-aliases/fca_nope", admin, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown remove: %d, want 404", resp.StatusCode)
	}
	// remove at the admin tier: 200.
	resp = postDetailJSON(t, client, http.MethodDelete,
		fmt.Sprintf("%s/api/fees/site-code-aliases/%s", base, id), admin, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin remove: %d, want 200", resp.StatusCode)
	}
}

func TestFeesCrossReconcileHTTP(t *testing.T) {
	const declarer, admin = "decl-1", "adm-1"
	client, base := newTwoTierTestServer(t, declarer, admin)

	// the scan: 501 on default builds, the real proposal list on duckdb.
	resp := postDetailJSON(t, client, http.MethodPost, base+"/api/fees/site-code-aliases/scan", declarer, "")
	if resp.StatusCode == http.StatusNotImplemented {
		var out struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		if out.Error.Code != "engine_not_built" {
			t.Fatalf("scan 501 code = %q", out.Error.Code)
		}
	} else if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan: %d", resp.StatusCode)
	}
	// cross-reconcile: the same engine contract + the param discipline.
	resp = postDetailJSON(t, client, http.MethodGet, base+"/api/fees/cross-reconcile?year=2026&month=8", "", "")
	if resp.StatusCode == http.StatusNotImplemented {
		t.Log("default build: the engine face answers 501 (covered by the duckdb fees tests)")
	} else if resp.StatusCode != http.StatusOK {
		t.Fatalf("cross-reconcile: %d", resp.StatusCode)
	}
	for _, q := range []string{
		base + "/api/fees/cross-reconcile",
		base + "/api/fees/cross-reconcile?year=1999",
	} {
		resp = postDetailJSON(t, client, http.MethodGet, q, "", "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", q, resp.StatusCode)
		}
	}
}
