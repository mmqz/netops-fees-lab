package api_test

// fees_details_test.go — #16's completion line item 6: the detail layer's
// HTTP channel matrix. Entry rides the DECLARER tier (the AI/operator
// channel — 录入不需要管理级), verification rides the ADMIN tier (the
// human review channel), list/reconcile read open. The 422
// detail_refused vocabulary carries the three-mandate refusals; the
// reconcile answers the honest engine-absent shape on default builds.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func postDetailJSON(t *testing.T, client *http.Client, method, url, token, body string) *http.Response {
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

func decodeDetailItem(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var item map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	return item
}

const detailCreateBody = `{"linkKey":"W8522883","period":"202609","feeType":"maintenance",
"category":"低值易耗品","amountYuan":150.00,"itemName":"卡钉、胶带","reason":"抢修"}`

func TestFeesDetailsHTTPChannelMatrix(t *testing.T) {
	const declarer, admin = "decl-1", "adm-1"
	client, base := newTwoTierTestServer(t, declarer, admin)

	// entry WITHOUT a token: 401.
	resp := postDetailJSON(t, client, http.MethodPost, base+"/api/fees/details", "", detailCreateBody)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-token entry: %d, want 401", resp.StatusCode)
	}

	// entry at the DECLARER tier: 201 — 录入是 AI/运营通道.
	resp = postDetailJSON(t, client, http.MethodPost, base+"/api/fees/details", declarer, detailCreateBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("declarer entry: %d, want 201\n", resp.StatusCode)
	}
	item := decodeDetailItem(t, resp)
	if item["status"] != "pending" || item["createdBy"] != "declarer" {
		t.Fatalf("created detail = %v — want pending/declarer", item)
	}
	if item["amountCents"].(float64) != 15000 {
		t.Fatalf("integer cents door: %v", item["amountCents"])
	}
	id := item["id"].(string)

	// duplicate fingerprint: 422 detail_refused with the existing id.
	resp = postDetailJSON(t, client, http.MethodPost, base+"/api/fees/details", declarer, detailCreateBody)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate entry: %d, want 422", resp.StatusCode)
	}
	var refused struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&refused); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if refused.Error.Code != "detail_refused" {
		t.Fatalf("refusal code = %q, want detail_refused", refused.Error.Code)
	}

	// the three mandates answer 422s.
	for _, bad := range []string{
		`{"period":"202609","feeType":"maintenance","category":"x","amountYuan":1}`,
		`{"linkKey":"W","period":"2026-09","feeType":"maintenance","category":"x","amountYuan":1}`,
		`{"linkKey":"W","period":"202609","feeType":"groceries","category":"x","amountYuan":1}`,
		`{"linkKey":"W","period":"202609","feeType":"maintenance","category":"","amountYuan":1}`,
		`{"linkKey":"W","period":"202609","feeType":"maintenance","category":"x"}`,
	} {
		resp = postDetailJSON(t, client, http.MethodPost, base+"/api/fees/details", declarer, bad)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("mandate refusal %s: %d, want 422", bad[:40], resp.StatusCode)
		}
	}

	// list: read open, filters narrow.
	resp = postDetailJSON(t, client, http.MethodGet, base+"/api/fees/details?linkKey=W8522883&period=202609", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open list: %d", resp.StatusCode)
	}
	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Total != 1 || list.Items[0]["id"] != id {
		t.Fatalf("list = %+v", list)
	}

	// verification at the DECLARER tier: 403 — 审核是管理级人类通道.
	resp = postDetailJSON(t, client, http.MethodPost,
		fmt.Sprintf("%s/api/fees/details/%s/status", base, id), declarer, `{"status":"confirmed"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("declarer status: %d, want 403", resp.StatusCode)
	}
	// reject without a reason: 422.
	resp = postDetailJSON(t, client, http.MethodPost,
		fmt.Sprintf("%s/api/fees/details/%s/status", base, id), admin, `{"status":"rejected"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("reject without reason: %d, want 422", resp.StatusCode)
	}
	// the happy verification: 200 + the trail rides.
	resp = postDetailJSON(t, client, http.MethodPost,
		fmt.Sprintf("%s/api/fees/details/%s/status", base, id), admin, `{"status":"confirmed"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin confirm: %d, want 200", resp.StatusCode)
	}
	item = decodeDetailItem(t, resp)
	if item["status"] != "confirmed" || item["verifiedBy"] != "admin" || item["verifiedAt"] == "" {
		t.Fatalf("verified detail = %v", item)
	}

	// unknown id: 404.
	resp = postDetailJSON(t, client, http.MethodPost,
		base+"/api/fees/details/fd_nope/status", admin, `{"status":"confirmed"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown status id: %d, want 404", resp.StatusCode)
	}

	// reconcile: read open; the default build answers the honest
	// engine-absent shape (detail side + note, no fake system zero).
	resp = postDetailJSON(t, client, http.MethodGet,
		base+"/api/fees/details/reconcile?linkKey=W8522883&period=202609&feeType=maintenance", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reconcile: %d, want 200 (half an answer is an answer)", resp.StatusCode)
	}
	rec := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		t.Fatalf("decode reconcile: %v", err)
	}
	if rec["detailSumCents"].(float64) != 15000 || rec["rows"].(float64) != 1 {
		t.Fatalf("reconcile detail side = %v", rec)
	}
	// The engine-state branch (the 501-or-200 contract pattern): the
	// DEFAULT build reports engineCentsAvailable=false with the system
	// side honestly absent; the duckdb build reports true — and for THIS
	// key (no analysis rows in the fresh test server) systemCents stays
	// nil on BOTH builds (absent key = 明细有系统无, never a fake zero).
	avail, _ := rec["engineCentsAvailable"].(bool)
	if avail {
		t.Log("this api test binary was built with -tags duckdb: the engine answered")
	}
	if rec["systemCents"] != nil {
		t.Fatalf("an analysis-absent key must keep systemCents nil on every build: %v", rec)
	}

	// reconcile refusals: 422 the same vocabulary.
	resp = postDetailJSON(t, client, http.MethodGet,
		base+"/api/fees/details/reconcile?linkKey=&period=202609&feeType=maintenance", "", "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("empty reconcile linkKey: %d, want 422", resp.StatusCode)
	}
}
