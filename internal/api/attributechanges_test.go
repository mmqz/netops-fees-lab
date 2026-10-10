package api_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xuri/excelize/v2"
)

// changeListItem mirrors one item of GET /api/attribute-changes (the exact
// §W4-D field names — a field-name regression fails to compile).
type changeListItem struct {
	ID           string  `json:"id"`
	ResourceID   string  `json:"resourceId"`
	ResourceType string  `json:"resourceType"`
	NaturalKey   string  `json:"naturalKey"`
	BatchID      string  `json:"batchId"`
	Field        string  `json:"field"`
	OldValue     string  `json:"oldValue"`
	NewValue     string  `json:"newValue"`
	Status       string  `json:"status"`
	Sheet        string  `json:"sheet"`
	RowNumber    int     `json:"rowNumber"`
	CreatedAt    string  `json:"createdAt"`
	AppliedAt    *string `json:"appliedAt"`
}

type changesPage struct {
	Items []changeListItem `json:"items"`
	Total int              `json:"total"`
}

// buildDeviceWorkbook builds the one-row device workbook with the given
// mgmt_ip value.
func buildDeviceWorkbook(t *testing.T, ip string) []byte {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "设备清单"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	rows := [][]string{
		{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"},
		{"DEV-001", "核心路由1", "杭州", "华为", "NE40E", ip, "在网"},
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr("设备清单", cell, v); err != nil {
				t.Fatalf("set cell: %v", err)
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}
	return buf.Bytes()
}

// deviceImportMapping is the confirmed mapping JSON of the device sheet.
const deviceImportMapping = `{"sheets":[
        {"sheet":"设备清单","action":"import","resourceType":"device","keyColumns":["device_code"],
         "columnMapping":{"device_code":"设备编码","device_name":"设备名称","city":"城市","vendor":"厂商","model":"型号","mgmt_ip":"管理IP","status":"状态"}}
]}`

// importDeviceWorkbook uploads the workbook and confirms the device mapping.
func importDeviceWorkbook(t *testing.T, srv *httptest.Server, ip string) {
	t.Helper()
	resp, body := uploadFile(t, srv, "devices.xlsx", buildDeviceWorkbook(t, ip))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var a importAnalysis
	decodeJSON(t, "upload", body, &a)
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/"+a.BatchID+"/mapping",
		[]byte(deviceImportMapping), "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("mapping: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var sum importSummary
	decodeJSON(t, "mapping", body, &sum)
}

// TestAPIAttributeChangesFlow walks the full W005 confirmation journey over
// HTTP: two imports of the same device with a changed mgmt_ip → pending
// proposal + W005 issue; list (filters, pagination, total, join); apply 200
// (resource attribute actually changes); apply 409 (non-pending); stale 409
// attribute_conflict with the field name and both values in the message;
// dismiss 200 / 409; unknown id 404.
func TestAPIAttributeChangesFlow(t *testing.T) {
	srv := newAPITestServer(t)

	importDeviceWorkbook(t, srv, "10.0.0.1")
	importDeviceWorkbook(t, srv, "10.0.0.9") // skipped row → 1 pending proposal + W005

	// GET list: 1 pending change with the full joined shape.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/attribute-changes?status=pending", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list pending: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var page changesPage
	decodeJSON(t, "list", body, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("pending list = %d items (total %d), want 1/1", len(page.Items), page.Total)
	}
	ch := page.Items[0]
	if ch.Field != "mgmt_ip" || ch.OldValue != "10.0.0.1" || ch.NewValue != "10.0.0.9" ||
		ch.Status != "pending" || ch.ResourceType != "device" || ch.NaturalKey != "DEV-001" ||
		ch.Sheet != "设备清单" || ch.RowNumber != 2 || ch.BatchID == "" || ch.AppliedAt != nil {
		t.Fatalf("change item = %+v, want device/DEV-001 mgmt_ip 10.0.0.1→10.0.0.9 pending", ch)
	}

	// The W005 issue is visible on the issues endpoint.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/issues?ruleCode=W005", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("issues W005: status = %d, want 200", resp.StatusCode)
	}
	var issues struct {
		Items []struct {
			RuleCode string `json:"ruleCode"`
			Severity string `json:"severity"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, "issues", body, &issues)
	if issues.Total != 1 || issues.Items[0].RuleCode != "W005" || issues.Items[0].Severity != "warning" {
		t.Fatalf("W005 issues = %+v (total %d), want 1 warning", issues.Items, issues.Total)
	}

	// List filters: status=all and an unknown resource filter.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/attribute-changes?status=all", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list all: status = %d, want 200", resp.StatusCode)
	}
	decodeJSON(t, "list all", body, &page)
	if page.Total != 1 {
		t.Fatalf("list all total = %d, want 1", page.Total)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/attribute-changes?resource_id=res_nope", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list by unknown resource: status = %d, want 200", resp.StatusCode)
	}
	decodeJSON(t, "list unknown resource", body, &page)
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("list by unknown resource = %d (total %d), want 0", len(page.Items), page.Total)
	}

	// Bad query values are 400s.
	for _, q := range []string{"status=bogus", "limit=0", "limit=abc", "offset=-1", "offset=x"} {
		resp, body = doRequest(t, srv, http.MethodGet, "/api/attribute-changes?"+q, nil, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("list %q: status = %d, want 400\nbody: %s", q, resp.StatusCode, body)
		}
	}
	// The limit window: limit=1 shows the first of the two changes.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/attribute-changes?limit=1&offset=0", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list window: status = %d, want 200", resp.StatusCode)
	}
	decodeJSON(t, "list window", body, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("list window = %d (total %d), want 1/1", len(page.Items), page.Total)
	}

	// ---- A second, competing proposal BEFORE the apply: the resource
	// still carries 10.0.0.1, so the new proposal also records
	// old 10.0.0.1 (new 10.0.0.10) — the queue of two pending changes. ----
	importDeviceWorkbook(t, srv, "10.0.0.10")
	resp, body = doRequest(t, srv, http.MethodGet, "/api/attribute-changes?status=pending", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list pending 2: status = %d, want 200", resp.StatusCode)
	}
	decodeJSON(t, "list pending 2", body, &page)
	if page.Total != 2 {
		t.Fatalf("pending after third import = %d, want 2 (10.0.0.9 + 10.0.0.10 proposals)", page.Total)
	}
	var staleCh changeListItem
	for _, item := range page.Items {
		if item.NewValue == "10.0.0.10" {
			staleCh = item
		}
	}
	if staleCh.ID == "" || staleCh.OldValue != "10.0.0.1" {
		t.Fatalf("competing proposal = %+v, want old 10.0.0.1 new 10.0.0.10", staleCh)
	}

	// Apply 200: the resource's mgmt_ip changes for real.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/attribute-changes/"+ch.ID+"/apply", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var applied changeListItem
	decodeJSON(t, "apply", body, &applied)
	if applied.Status != "applied" || applied.AppliedAt == nil || applied.Field != "mgmt_ip" {
		t.Fatalf("applied item = %+v, want applied mgmt_ip with appliedAt", applied)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=device", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resources: status = %d, want 200", resp.StatusCode)
	}
	var resources resourcesPage
	decodeJSON(t, "resources", body, &resources)
	if resources.Total != 1 || resources.Items[0].Attributes["mgmt_ip"] != "10.0.0.9" {
		t.Fatalf("resource after apply = %+v, want mgmt_ip 10.0.0.9", resources.Items)
	}

	// Apply of the now-applied change → 409 conflict.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/attribute-changes/"+ch.ID+"/apply", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-apply: status = %d, want 409\nbody: %s", resp.StatusCode, body)
	}
	// Unknown id → 404 (apply and dismiss).
	for _, op := range []string{"apply", "dismiss"} {
		resp, body = doRequest(t, srv, http.MethodPost, "/api/attribute-changes/chg_nope/"+op, nil, "")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s unknown: status = %d, want 404\nbody: %s", op, resp.StatusCode, body)
		}
	}

	// ---- Stale conflict: the competing proposal's old value
	// (10.0.0.1) no longer matches the applied current value
	// (10.0.0.9). ----
	resp, body = doRequest(t, srv, http.MethodPost, "/api/attribute-changes/"+staleCh.ID+"/apply", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale apply: status = %d, want 409\nbody: %s", resp.StatusCode, body)
	}
	var errEnvelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeJSON(t, "stale apply", body, &errEnvelope)
	if errEnvelope.Error.Code != "attribute_conflict" {
		t.Errorf("stale apply code = %q, want attribute_conflict", errEnvelope.Error.Code)
	}
	for _, want := range []string{"mgmt_ip", "10.0.0.9", "10.0.0.1"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("stale apply message %q must contain %q", errEnvelope.Error.Message, want)
		}
	}

	// Dismiss 200: the proposal flips to dismissed, the resource untouched.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/attribute-changes/"+staleCh.ID+"/dismiss", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dismiss: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var dismissed changeListItem
	decodeJSON(t, "dismiss", body, &dismissed)
	if dismissed.Status != "dismissed" || dismissed.AppliedAt != nil {
		t.Fatalf("dismissed item = %+v, want dismissed with NULL appliedAt", dismissed)
	}
	// Re-dismiss → 409.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/attribute-changes/"+staleCh.ID+"/dismiss", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-dismiss: status = %d, want 409\nbody: %s", resp.StatusCode, body)
	}
	// The resource still carries the applied 10.0.0.9.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=device", nil, "")
	decodeJSON(t, "resources 2", body, &resources)
	if resources.Items[0].Attributes["mgmt_ip"] != "10.0.0.9" {
		t.Errorf("resource after dismiss = %+v, want mgmt_ip still 10.0.0.9", resources.Items[0])
	}

	// Final state: 1 applied + 1 dismissed, 0 pending.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/attribute-changes?status=pending", nil, "")
	decodeJSON(t, "final pending", body, &page)
	if page.Total != 0 {
		t.Errorf("final pending total = %d, want 0", page.Total)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/attribute-changes", nil, "")
	decodeJSON(t, "final all", body, &page)
	if page.Total != 2 {
		t.Errorf("final all total = %d, want 2 (1 applied + 1 dismissed)", page.Total)
	}
}
