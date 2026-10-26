package api_test

// fees_rules_test.go — issue #2's HTTP pins: the two-tier channel matrix
// (create = declarer tier, enable/disable = admin tier, list/audit =
// read-open), the 422 rule_refused vocabulary of the closed sets, the
// 201/404 contracts, and the audit ledger's actor derivation (the token
// tier is the honest identity of the shared-token world).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/app"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// postRuleJSON issues a JSON POST with the given bearer token ("" = none).
func postRuleJSON(t *testing.T, client *http.Client, method, url, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader([]byte(body)))
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

const ruleCreateBody = `{"name":"电费单月超阈值","feeType":"electricity","metric":"amount",
"aggregation":"sum","dimension":"site_month","comparator":"gt",
"threshold":500000,"windowMonths":1,"severity":"high","description":"单站单月电费超 5000 元"}`

// decodeRule decodes one rule item.
func decodeRule(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var item map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		t.Fatalf("decode rule: %v", err)
	}
	return item
}

func TestFeesRulesHTTPChannelMatrix(t *testing.T) {
	const declarer, admin = "decl-1", "adm-1"
	client, base := newTwoTierTestServer(t, declarer, admin)

	// create WITHOUT a token: 401 (the first tier of the write gate).
	resp := postRuleJSON(t, client, http.MethodPost, base+"/api/fees/rules", "", ruleCreateBody)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("create without token: %d, want 401", resp.StatusCode)
	}

	// create WITH the DECLARER token: 201 — the AI/operator channel. The
	// rule lands disabled (观察态).
	resp = postRuleJSON(t, client, http.MethodPost, base+"/api/fees/rules", declarer, ruleCreateBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create as declarer: %d, want 201", resp.StatusCode)
	}
	rule := decodeRule(t, resp)
	if rule["enabled"] != false {
		t.Fatalf("a fresh rule must land disabled: %v", rule)
	}
	id, _ := rule["id"].(string)
	if id == "" {
		t.Fatalf("201 body carries no id: %v", rule)
	}

	// duplicate name: 422 rule_refused (the UNIQUE made honest).
	resp = postRuleJSON(t, client, http.MethodPost, base+"/api/fees/rules", declarer, ruleCreateBody)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate name: %d, want 422", resp.StatusCode)
	}
	var dup struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&dup)
	if dup.Error.Code != "rule_refused" {
		t.Fatalf("duplicate name code = %q, want rule_refused", dup.Error.Code)
	}

	// a foreign enum: 422 rule_refused, the text names every received value.
	resp = postRuleJSON(t, client, http.MethodPost, base+"/api/fees/rules", declarer,
		`{"name":"坏枚举","feeType":"electricity","metric":"amount_cents","aggregation":"sum","dimension":"site_month","comparator":"gt","threshold":1,"severity":"low"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("foreign enum: %d, want 422", resp.StatusCode)
	}
	var bad struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&bad)
	if bad.Error.Code != "rule_refused" || !bytes.Contains([]byte(bad.Error.Message), []byte("amount_cents")) {
		t.Fatalf("enum refusal = %q %q", bad.Error.Code, bad.Error.Message)
	}

	// malformed JSON: 400.
	resp = postRuleJSON(t, client, http.MethodPost, base+"/api/fees/rules", declarer, `{not json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad json: %d, want 400", resp.StatusCode)
	}

	// enable with the DECLARER token: 403 — the human channel is admin-only.
	resp = postRuleJSON(t, client, http.MethodPost, fmt.Sprintf("%s/api/fees/rules/%s/enable", base, id), declarer, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("enable as declarer: %d, want 403", resp.StatusCode)
	}

	// enable with the ADMIN token: 200, the read-back shows enabled.
	resp = postRuleJSON(t, client, http.MethodPost, fmt.Sprintf("%s/api/fees/rules/%s/enable", base, id), admin, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable as admin: %d, want 200", resp.StatusCode)
	}
	rule = decodeRule(t, resp)
	if rule["enabled"] != true {
		t.Fatalf("read-back after enable: %v", rule)
	}

	// list: read-open (no token), ?enabled=1 filter works.
	resp2, err := client.Get(base + "/api/fees/rules")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("list without token: %d, want 200 (read-open)", resp2.StatusCode)
	}
	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&list)
	if list.Total != 1 || list.Items[0]["enabled"] != true {
		t.Fatalf("list = %v", list)
	}
	resp2, err = client.Get(base + "/api/fees/rules?enabled=1")
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	defer resp2.Body.Close()
	_ = json.NewDecoder(resp2.Body).Decode(&list)
	if list.Total != 1 {
		t.Fatalf("enabled-only list = %v, want 1", list.Total)
	}

	// audit: read-open, newest first, actor derives from the token tier
	// (created rode the declarer token, enabled the admin token).
	resp2, err = client.Get(base + "/api/fees/rules/" + id + "/audit")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	defer resp2.Body.Close()
	var audit struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&audit)
	if audit.Total != 2 {
		t.Fatalf("audit rows = %d, want created+enabled", audit.Total)
	}
	if audit.Items[0]["action"] != "enabled" || audit.Items[0]["actor"] != "admin" {
		t.Fatalf("audit[0] = %v, want enabled/admin", audit.Items[0])
	}
	if audit.Items[1]["action"] != "created" || audit.Items[1]["actor"] != "declarer" {
		t.Fatalf("audit[1] = %v, want created/declarer", audit.Items[1])
	}

	// disable as admin; the audit grows its third row.
	resp = postRuleJSON(t, client, http.MethodPost, fmt.Sprintf("%s/api/fees/rules/%s/disable", base, id), admin, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable as admin: %d, want 200", resp.StatusCode)
	}

	// unknown id: enable and audit both answer 404.
	resp = postRuleJSON(t, client, http.MethodPost, base+"/api/fees/rules/rule_missing/enable", admin, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("enable unknown: %d, want 404", resp.StatusCode)
	}
	resp2, err = client.Get(base + "/api/fees/rules/rule_missing/audit")
	if err != nil {
		t.Fatalf("audit unknown: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("audit unknown: %d, want 404", resp2.StatusCode)
	}
}

// TestFeesRulesHTTPGateOffActor: with the gate off (the single-operator
// compat row), the audit actor records the channel itself (http) — the
// ledger never lies about who wrote even when nobody configured tiers.
func TestFeesRulesHTTPGateOffActor(t *testing.T) {
	dir := testsupport.TempDir(t)
	a, err := app.New(app.Config{
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	srv := httptest.NewServer(a.Routes())
	t.Cleanup(srv.Close)
	client, base := srv.Client(), srv.URL

	resp := postRuleJSON(t, client, http.MethodPost, base+"/api/fees/rules", "", ruleCreateBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("gate-off create: %d, want 201", resp.StatusCode)
	}
	rule := decodeRule(t, resp)
	id, _ := rule["id"].(string)
	resp2, err := client.Get(base + "/api/fees/rules/" + id + "/audit")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	defer resp2.Body.Close()
	var audit struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&audit)
	if len(audit.Items) != 1 || audit.Items[0]["actor"] != "http" {
		t.Fatalf("gate-off audit actor = %v, want http", audit.Items)
	}
}
