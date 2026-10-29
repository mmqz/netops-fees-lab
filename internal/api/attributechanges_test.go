package api_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/app"
	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"github.com/xuri/excelize/v2"
)

// newAPITestServerWithDB is newAPITestServer plus the DB handle — the
// ref-retarget test verifies the belongs_to edge directly on the repo
// because the fees route table carries no /relations read endpoint (the
// fork cut the IPRAN topology surface; edge verification is repo-level
// here, the HTTP face stays the confirmation channel under test).
func newAPITestServerWithDB(t *testing.T) (*httptest.Server, *app.App) {
	t.Helper()
	dir := testsupport.TempDir(t)
	a, err := app.New(app.Config{
		DBPath:  filepath.Join(dir, "netops.db"),
		DataDir: dir,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	srv := httptest.NewServer(a.Routes())
	t.Cleanup(func() {
		srv.Close()
		_ = a.Close()
	})
	return srv, a
}

// changeListItem mirrors one item of GET /api/attribute-changes (the exact
// §W4-D field names — a field-name regression fails to compile).
type changeListItem struct {
	ID                 string  `json:"id"`
	ResourceID         string  `json:"resourceId"`
	ResourceType       string  `json:"resourceType"`
	NaturalKey         string  `json:"naturalKey"`
	BatchID            string  `json:"batchId"`
	Field              string  `json:"field"`
	OldValue           string  `json:"oldValue"`
	NewValue           string  `json:"newValue"`
	Status             string  `json:"status"`
	Sheet              string  `json:"sheet"`
	RowNumber          int     `json:"rowNumber"`
	CreatedAt          string  `json:"createdAt"`
	AppliedAt          *string `json:"appliedAt"`
	RelationRetargeted bool    `json:"relationRetargeted"`
}

type changesPage struct {
	Items []changeListItem `json:"items"`
	Total int              `json:"total"`
}

// buildDeviceWorkbook builds the one-row device workbook with the given
// mgmt_ip value.
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
	_, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=device", nil, "")
	decodeJSON(t, "resources 2", body, &resources)
	if resources.Items[0].Attributes["mgmt_ip"] != "10.0.0.9" {
		t.Errorf("resource after dismiss = %+v, want mgmt_ip still 10.0.0.9", resources.Items[0])
	}

	// Final state: 1 applied + 1 dismissed, 0 pending.
	_, body = doRequest(t, srv, http.MethodGet, "/api/attribute-changes?status=pending", nil, "")
	decodeJSON(t, "final pending", body, &page)
	if page.Total != 0 {
		t.Errorf("final pending total = %d, want 0", page.Total)
	}
	_, body = doRequest(t, srv, http.MethodGet, "/api/attribute-changes", nil, "")
	decodeJSON(t, "final all", body, &page)
	if page.Total != 2 {
		t.Errorf("final all total = %d, want 2 (1 applied + 1 dismissed)", page.Total)
	}
}

// buildSiteWorkbook builds the one-row site workbook (局站资源表) with the
// given site_id.
func buildSiteWorkbook(t *testing.T, siteID string) []byte {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "局站资源表"); err != nil {
		t.Fatalf("rename default sheet: %v", err)
	}
	rows := [][]string{
		{"局站ID", "局站名称", "地市"},
		{siteID, "站" + siteID, "杭州"},
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr("局站资源表", cell, v); err != nil {
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

// buildRoomWorkbook builds the one-row machine room workbook (机房资源表)
// with the given site_id ref value.
func buildRoomWorkbook(t *testing.T, siteID string) []byte {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "机房资源表"); err != nil {
		t.Fatalf("rename default sheet: %v", err)
	}
	rows := [][]string{
		{"机房ID", "机房名称", "局站ID"},
		{"RM-001", "机房一", siteID},
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr("机房资源表", cell, v); err != nil {
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

// siteImportMapping / roomImportMapping are the confirmed mappings of the
// two readings (site pass + machine_room pass of the 机房局站 story).
const siteImportMapping = `{"sheets":[
        {"sheet":"局站资源表","action":"import","resourceType":"site","keyColumns":["site_id"],
         "columnMapping":{"site_id":"局站ID","site_name":"局站名称","city":"地市"}}
]}`

const roomImportMapping = `{"sheets":[
        {"sheet":"机房资源表","action":"import","resourceType":"machine_room","keyColumns":["machine_room_id"],
         "columnMapping":{"machine_room_id":"机房ID","machine_room_name":"机房名称","site_id":"局站ID"}}
]}`

// importMapped uploads the workbook and confirms the given mapping JSON,
// returning the import summary (with the batch id).
func importMapped(t *testing.T, srv *httptest.Server, filename string, content []byte, mapping string) importSummary {
	t.Helper()
	resp, body := uploadFile(t, srv, filename, content)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload %s: status = %d, want 202\nbody: %s", filename, resp.StatusCode, body)
	}
	var a importAnalysis
	decodeJSON(t, "upload "+filename, body, &a)
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/"+a.BatchID+"/mapping",
		[]byte(mapping), "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("mapping %s: status = %d, want 202\nbody: %s", filename, resp.StatusCode, body)
	}
	var sum importSummary
	decodeJSON(t, "mapping "+filename, body, &sum)
	return sum
}

// TestAPIAttributeChangesRefRetarget walks the W13-D ref-field
// confirmation journey over HTTP: two site imports (SITE-B in its own
// batch), a machine room whose site_id re-import moves SITE-A → SITE-B
// (one pending proposal whose list preview carries relationRetargeted),
// the target site's batch rolled back AFTER the proposal exists (the
// reachable trigger of the guard) → apply answers 409 ref_target_missing
// with the field / target key / referenced type in the message and ZERO
// changes (attribute, edge, proposal status all unchanged); the site is
// re-imported → the same apply now succeeds, the attribute AND the
// belongs_to edge retarget to SITE-B and the 200 body carries
// relationRetargeted=true; the second apply keeps the plain conflict 409.
func TestAPIAttributeChangesRefRetarget(t *testing.T) {
	srv, a := newAPITestServerWithDB(t)

	importMapped(t, srv, "site-a.xlsx", buildSiteWorkbook(t, "SITE-A"), siteImportMapping)
	siteB := importMapped(t, srv, "site-b.xlsx", buildSiteWorkbook(t, "SITE-B"), siteImportMapping)
	importMapped(t, srv, "room-v1.xlsx", buildRoomWorkbook(t, "SITE-A"), roomImportMapping)
	importMapped(t, srv, "room-v2.xlsx", buildRoomWorkbook(t, "SITE-B"), roomImportMapping)

	// The room resource and both site resources (for edge endpoint checks).
	resp, body := doRequest(t, srv, http.MethodGet, "/api/resources?type=machine_room", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resources: status = %d, want 200", resp.StatusCode)
	}
	var rooms resourcesPage
	decodeJSON(t, "rooms", body, &rooms)
	if rooms.Total != 1 {
		t.Fatalf("machine_room count = %d, want 1", rooms.Total)
	}
	roomID := rooms.Items[0].ID
	siteIDByKey := map[string]string{}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=site", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sites: status = %d, want 200", resp.StatusCode)
	}
	var sites resourcesPage
	decodeJSON(t, "sites", body, &sites)
	for _, s := range sites.Items {
		siteIDByKey[s.NaturalKey] = s.ID
	}

	// The pending proposal with the retarget PREVIEW (the web hint's
	// predicate) — and the W001-free happy path edge exists to SITE-A.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/attribute-changes?status=pending", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list pending: status = %d, want 200", resp.StatusCode)
	}
	var page changesPage
	decodeJSON(t, "list pending", body, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("pending list = %d items (total %d), want 1/1", len(page.Items), page.Total)
	}
	ch := page.Items[0]
	if ch.Field != "site_id" || ch.OldValue != "SITE-A" || ch.NewValue != "SITE-B" {
		t.Fatalf("change item = %+v, want site_id SITE-A→SITE-B", ch)
	}
	if !ch.RelationRetargeted {
		t.Errorf("pending list preview relationRetargeted = false, want true (ref field with an edge)")
	}
	edgeTo := func() string {
		rels, err := a.DB.Relations.ListByResource(context.Background(), roomID)
		if err != nil {
			t.Fatalf("list relations of room: %v", err)
		}
		for _, rel := range rels {
			if rel.Type == domain.RelationTypeBelongsTo && rel.FromResourceID == roomID {
				return rel.ToResourceID
			}
		}
		return ""
	}
	if got := edgeTo(); got != siteIDByKey["SITE-A"] {
		t.Fatalf("belongs_to edge before apply points at %q, want SITE-A (%q)", got, siteIDByKey["SITE-A"])
	}

	// Roll back the SITE-B batch AFTER the proposal exists: legal (no
	// relation references SITE-B; the proposal belongs to the room's
	// re-import batch) — the reachable trigger of the ref-target guard.
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/batches/"+siteB.BatchID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback site-B batch: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}

	// Apply → 409 ref_target_missing with the field / target key / type in
	// the message, and ZERO changes on the resource and the edge.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/attribute-changes/"+ch.ID+"/apply", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("apply with vanished target: status = %d, want 409\nbody: %s", resp.StatusCode, body)
	}
	var errEnvelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeJSON(t, "ref target missing", body, &errEnvelope)
	if errEnvelope.Error.Code != "ref_target_missing" {
		t.Errorf("apply refused: error.code = %q, want ref_target_missing", errEnvelope.Error.Code)
	}
	for _, want := range []string{"site_id", "SITE-B", "site"} {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("ref_target_missing message %q must contain %q", errEnvelope.Error.Message, want)
		}
	}
	_, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=machine_room", nil, "")
	decodeJSON(t, "rooms after refusal", body, &rooms)
	if rooms.Items[0].Attributes["site_id"] != "SITE-A" {
		t.Errorf("resource after refusal = %v, want site_id still SITE-A (zero changes)", rooms.Items[0].Attributes)
	}
	if got := edgeTo(); got != siteIDByKey["SITE-A"] {
		t.Errorf("edge after refusal points at %q, want still SITE-A (zero changes)", got)
	}
	_, body = doRequest(t, srv, http.MethodGet, "/api/attribute-changes?status=pending", nil, "")
	decodeJSON(t, "pending after refusal", body, &page)
	if page.Total != 1 {
		t.Errorf("pending after refusal = %d, want 1 (the proposal stays pending)", page.Total)
	}

	// Re-import the site (a NEW batch — SITE-A's batch is untouched) and
	// apply again: the attribute AND the belongs_to edge follow SITE-B.
	importMapped(t, srv, "site-b-again.xlsx", buildSiteWorkbook(t, "SITE-B"), siteImportMapping)
	_, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=site", nil, "")
	decodeJSON(t, "sites after re-import", body, &sites)
	for _, s := range sites.Items {
		siteIDByKey[s.NaturalKey] = s.ID
	}
	resp, body = doRequest(t, srv, http.MethodPost, "/api/attribute-changes/"+ch.ID+"/apply", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("apply after re-import: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var applied changeListItem
	decodeJSON(t, "apply", body, &applied)
	if applied.Status != "applied" || applied.Field != "site_id" {
		t.Fatalf("applied item = %+v, want applied site_id", applied)
	}
	if !applied.RelationRetargeted {
		t.Errorf("applied item relationRetargeted = false, want true (the edge moved)")
	}
	_, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=machine_room", nil, "")
	decodeJSON(t, "rooms after apply", body, &rooms)
	if rooms.Items[0].Attributes["site_id"] != "SITE-B" {
		t.Errorf("resource after apply = %v, want site_id SITE-B", rooms.Items[0].Attributes)
	}
	if got := edgeTo(); got != siteIDByKey["SITE-B"] {
		t.Errorf("belongs_to edge after apply points at %q, want SITE-B (%q)", got, siteIDByKey["SITE-B"])
	}

	// The second apply of the now-applied proposal → the plain conflict.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/attribute-changes/"+ch.ID+"/apply", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-apply: status = %d, want 409\nbody: %s", resp.StatusCode, body)
	}
	decodeJSON(t, "re-apply", body, &errEnvelope)
	if errEnvelope.Error.Code != "conflict" {
		t.Errorf("re-apply error.code = %q, want conflict", errEnvelope.Error.Code)
	}
}
