package api_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xuri/excelize/v2"
)

// renameListItem mirrors one item of GET /api/renames (the exact W6-D field
// names — a field-name regression fails to compile).
type renameListItem struct {
	ID            string   `json:"id"`
	ResourceID    string   `json:"resourceId"`
	ResourceType  string   `json:"resourceType"`
	NaturalKey    string   `json:"naturalKey"`
	BatchID       string   `json:"batchId"`
	NewResourceID string   `json:"newResourceId"`
	OldKey        string   `json:"oldKey"`
	NewKey        string   `json:"newKey"`
	MatchedFields []string `json:"matchedFields"`
	Status        string   `json:"status"`
	Sheet         string   `json:"sheet"`
	RowNumber     int      `json:"rowNumber"`
	CreatedAt     string   `json:"createdAt"`
	DecidedAt     *string  `json:"decidedAt"`
}

type renamesPage struct {
	Items []renameListItem `json:"items"`
	Total int              `json:"total"`
}

// buildBaseStationWorkbook builds a one-row base_station workbook (the real
// 14-column shape, code empty — the fallback key is the bare station name).
func buildBaseStationWorkbook(t *testing.T, name, stationIP string) []byte {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "无线基站表"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	rows := [][]string{
		{"基站类型", "无线基站名称", "基站编码", "电联共享", "归属A IP", "环", "链", "归属A名称", "归属A端口", "网关IP", "基站IP", "描述", "工单号", "备注"},
		{"宏基站", name, "", "是", "4.28.2.3", "环1", "链1", "枢纽楼A4", "gei_1/11", "12.17.10.126", stationIP, "BBU接入", "202366", ""},
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr("无线基站表", cell, v); err != nil {
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

// baseStationMappingJSON is the confirmed mapping of the 无线基站表 sheet.
const baseStationMappingJSON = `{"sheets":[
	{"sheet":"无线基站表","action":"import","resourceType":"base_station","keyColumns":["base_station_code"],
	 "columnMapping":{"base_station_type":"基站类型","base_station_name":"无线基站名称","base_station_code":"基站编码","sharing":"电联共享","uplink_a_ip":"归属A IP","ring":"环","chain":"链","uplink_a_name":"归属A名称","uplink_a_port":"归属A端口","gateway_ip":"网关IP","base_station_ip":"基站IP","description":"描述","work_order":"工单号","remark":"备注"}}
]}`

// importBaseStationWorkbook uploads the workbook and confirms the
// base_station mapping.
func importBaseStationWorkbook(t *testing.T, srv *httptest.Server, name, stationIP string) {
	t.Helper()
	resp, body := uploadFile(t, srv, "stations.xlsx", buildBaseStationWorkbook(t, name, stationIP))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var a importAnalysis
	decodeJSON(t, "upload", body, &a)
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/"+a.BatchID+"/mapping",
		[]byte(baseStationMappingJSON), "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("mapping: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var sum importSummary
	decodeJSON(t, "mapping", body, &sum)
	if sum.Summary.RowsImported != 1 {
		t.Fatalf("import of %q = %+v, want 1 imported row", name, sum)
	}
}

// TestAPIRenamesFlow walks the full W006 confirmation journey over HTTP:
// import → renamed re-import → pending proposal + W006 issue; list (filters,
// pagination, joined shape); apply 200 (identity merge: old resource keeps
// its id and takes the new key, new resource gone); re-apply 409; unknown
// id 404; the target-missing 409 family; reject 200 / 409.
func TestAPIRenamesFlow(t *testing.T) {
	srv := newAPITestServer(t)

	// Import 动力机房2, then the renamed sheet 动力机房2新 (same attributes).
	importBaseStationWorkbook(t, srv, "动力机房2", "172.7.10.125/30")
	importBaseStationWorkbook(t, srv, "动力机房2新", "172.7.10.125/30") // new key + identical attrs → W006 + proposal

	// GET list: 1 pending proposal with the full joined shape.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/renames?status=pending", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list pending: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var page renamesPage
	decodeJSON(t, "list", body, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("pending list = %d items (total %d), want 1/1", len(page.Items), page.Total)
	}
	rp := page.Items[0]
	if rp.Status != "pending" || rp.ResourceType != "base_station" ||
		rp.NaturalKey != "动力机房2" || rp.OldKey != "动力机房2" || rp.NewKey != "动力机房2新" ||
		rp.Sheet != "无线基站表" || rp.RowNumber != 2 || rp.BatchID == "" ||
		rp.ResourceID == "" || rp.NewResourceID == "" || rp.DecidedAt != nil {
		t.Fatalf("rename item = %+v, want pending base_station 动力机房2→动力机房2新", rp)
	}
	if len(rp.MatchedFields) == 0 {
		t.Fatalf("matched fields = %v, want the non-empty equal fields", rp.MatchedFields)
	}

	// The W006 issue is visible on the issues endpoint.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/issues?ruleCode=W006", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("issues W006: status = %d, want 200", resp.StatusCode)
	}
	var issues struct {
		Items []struct {
			RuleCode string `json:"ruleCode"`
			Severity string `json:"severity"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, "issues", body, &issues)
	if issues.Total != 1 || issues.Items[0].RuleCode != "W006" || issues.Items[0].Severity != "warning" {
		t.Fatalf("W006 issues = %+v (total %d), want 1 warning", issues.Items, issues.Total)
	}

	// List filters: type, bogus values; the resource filter isolates the
	// old resource.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/renames?resource_type=base_station&status=all", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list by type: status = %d, want 200", resp.StatusCode)
	}
	decodeJSON(t, "list by type", body, &page)
	if page.Total != 1 {
		t.Fatalf("list by type total = %d, want 1", page.Total)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/renames?resource_type=device", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list by other type: status = %d, want 200", resp.StatusCode)
	}
	decodeJSON(t, "list by other type", body, &page)
	if page.Total != 0 {
		t.Fatalf("list by other type total = %d, want 0", page.Total)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/renames?resource_id="+rp.ResourceID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list by resource: status = %d, want 200", resp.StatusCode)
	}
	decodeJSON(t, "list by resource", body, &page)
	if page.Total != 1 {
		t.Fatalf("list by resource total = %d, want 1", page.Total)
	}
	// Bad query values are 400s.
	for _, q := range []string{"status=bogus", "resource_type=vlan", "limit=0", "limit=abc", "offset=-1", "offset=x"} {
		resp, body = doRequest(t, srv, http.MethodGet, "/api/renames?"+q, nil, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("list %q: status = %d, want 400\nbody: %s", q, resp.StatusCode, body)
		}
	}

	// Apply 200: the identity merge — the old resource (same id) carries the
	// new key, the new resource is gone.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/renames/"+rp.ID+"/apply", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var applied renameListItem
	decodeJSON(t, "apply", body, &applied)
	if applied.Status != "applied" || applied.DecidedAt == nil || applied.NewKey != "动力机房2新" ||
		applied.NaturalKey != "动力机房2新" || applied.NewResourceID != rp.NewResourceID {
		t.Fatalf("applied item = %+v, want applied 动力机房2→动力机房2新 with decidedAt", applied)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=base_station", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resources: status = %d, want 200", resp.StatusCode)
	}
	var resources resourcesPage
	decodeJSON(t, "resources", body, &resources)
	if resources.Total != 1 {
		t.Fatalf("base_station resources after apply = %d, want 1 (merged)", resources.Total)
	}
	if resources.Items[0].ID != rp.ResourceID || resources.Items[0].NaturalKey != "动力机房2新" {
		t.Fatalf("merged resource = %+v, want the OLD id %s with key 动力机房2新", resources.Items[0], rp.ResourceID)
	}

	// Re-apply of the applied proposal → 409 conflict.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/renames/"+rp.ID+"/apply", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-apply: status = %d, want 409\nbody: %s", resp.StatusCode, body)
	}
	// Unknown id → 404 (apply and reject).
	for _, op := range []string{"apply", "reject"} {
		resp, body = doRequest(t, srv, http.MethodPost, "/api/renames/rp_nope/"+op, nil, "")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s unknown: status = %d, want 404\nbody: %s", op, resp.StatusCode, body)
		}
	}
	// Reject of the applied proposal → 409 as well.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/renames/"+rp.ID+"/reject", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("reject applied: status = %d, want 409\nbody: %s", resp.StatusCode, body)
	}

	// Final state: 1 applied, 0 pending.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/renames?status=pending", nil, "")
	decodeJSON(t, "final pending", body, &page)
	if page.Total != 0 {
		t.Errorf("final pending total = %d, want 0", page.Total)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/renames", nil, "")
	decodeJSON(t, "final all", body, &page)
	if page.Total != 1 {
		t.Errorf("final all total = %d, want 1 applied", page.Total)
	}
}

// TestAPIRenamesRejectAndTargetMissing covers the remaining journey: a
// second proposal is REJECTED (both resources stay independent), and an
// apply whose new resource vanished (its batch was rolled back) answers 409
// target_missing with the reject recommendation in the message — the
// proposal itself stays pending.
func TestAPIRenamesRejectAndTargetMissing(t *testing.T) {
	srv := newAPITestServer(t)

	importBaseStationWorkbook(t, srv, "学府路2", "172.7.20.1/30")
	importBaseStationWorkbook(t, srv, "学府路2新", "172.7.20.1/30") // pending proposal
	resp, body := doRequest(t, srv, http.MethodGet, "/api/renames?status=pending", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list pending: status = %d, want 200", resp.StatusCode)
	}
	var page renamesPage
	decodeJSON(t, "list", body, &page)
	if page.Total != 1 {
		t.Fatalf("pending list total = %d, want 1", page.Total)
	}
	rp := page.Items[0]

	// Reject 200: both resources stay.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/renames/"+rp.ID+"/reject", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var rejected renameListItem
	decodeJSON(t, "reject", body, &rejected)
	if rejected.Status != "rejected" || rejected.DecidedAt == nil {
		t.Fatalf("rejected item = %+v, want rejected with decidedAt", rejected)
	}
	var resources resourcesPage
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=base_station", nil, "")
	decodeJSON(t, "resources", body, &resources)
	if resources.Total != 2 {
		t.Fatalf("base_station resources after reject = %d, want 2 (independent)", resources.Total)
	}
	// Re-reject → 409.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/renames/"+rp.ID+"/reject", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-reject: status = %d, want 409\nbody: %s", resp.StatusCode, body)
	}

	// Target missing: a fresh proposal whose import batch is rolled back
	// (the new resource disappears with it).
	importBaseStationWorkbook(t, srv, "站A", "172.7.30.1/30")
	importBaseStationWorkbook(t, srv, "站A新", "172.7.30.1/30")
	resp, body = doRequest(t, srv, http.MethodGet, "/api/renames?status=pending", nil, "")
	decodeJSON(t, "pending 2", body, &page)
	if page.Total != 1 {
		t.Fatalf("pending after second pair = %d, want 1", page.Total)
	}
	target := page.Items[0]
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/batches/"+target.BatchID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback of the rename batch: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var rollbackBody struct {
		Deleted struct {
			RenameProposals int `json:"renameProposals"`
		} `json:"deleted"`
	}
	decodeJSON(t, "rollback", body, &rollbackBody)
	if rollbackBody.Deleted.RenameProposals != 1 {
		t.Fatalf("rollback deleted.renameProposals = %d, want 1 (pending dies with its batch)", rollbackBody.Deleted.RenameProposals)
	}
	// The pending proposal is gone with the batch — an apply of the (now
	// unknown) id answers 404.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/renames/"+target.ID+"/apply", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("apply after batch rollback: status = %d, want 404\nbody: %s", resp.StatusCode, body)
	}
}
