package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-fees-lab/internal/mapping"
)

// ---------------------------------------------------------------------------
// W24 — 无人值守导入（§12-B7 方案 A，issue #28）: deterministic fixtures
// ---------------------------------------------------------------------------

// w24Sheet / w24Headers are the device-sheet shape of the seven-day journey:
// the exact testfixtures header row, so the suggestion engine resolves the
// device mapping deterministically.
var (
	w24Sheet   = "设备清单"
	w24Headers = []string{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"}
)

// w24Workbook builds ONE device-sheet workbook with the given headers and
// data rows as multipart-uploadable bytes. Values are raw strings; the row
// generators guarantee unique codes / dotted-quad IPs / in-enum statuses.
func w24Workbook(t *testing.T, headers []string, rows [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", w24Sheet); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	grid := append([][]string{headers}, rows...)
	for r, row := range grid {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr(w24Sheet, cell, v); err != nil {
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

// w24FixedRows returns n byte-stable device rows (the day-1 cohort whose
// keys re-import as skipped rows on every later day).
func w24FixedRows(n int) [][]string {
	rows := make([][]string, n)
	for i := 0; i < n; i++ {
		rows[i] = []string{
			"DEV-F1-" + w24Num(i), "固定设备-" + w24Num(i), "杭州", "华为", "NE40E",
			"10.70.1." + w24Num(i+1), "在网",
		}
	}
	return rows
}

// w24NewRows returns n rows of FRESH devices for the given day (2..8):
// different content every day, same header shape — the seven-day workbook
// regeneration of the acceptance test.
func w24NewRows(day, n int) [][]string {
	rows := make([][]string, n)
	for i := 0; i < n; i++ {
		rows[i] = []string{
			"DEV-D" + w24Num(day) + "-" + w24Num(i), "日更设备-" + w24Num(day) + "-" + w24Num(i),
			"宁波", "中兴", "ATN910C",
			"10.70." + w24Num(day) + "." + w24Num(i+1), "在网",
		}
	}
	return rows
}

// w24Num renders n without zero padding (E004 rejects leading-zero IP
// octets — plain digits only).
func w24Num(n int) string {
	return strconv.Itoa(n)
}

// w24TemplateItem mirrors one item of the capture/list responses.
type w24TemplateItem struct {
	TemplateID        string          `json:"templateId"`
	SourceName        string          `json:"sourceName"`
	HeaderFingerprint string          `json:"headerFingerprint"`
	ResourceType      string          `json:"resourceType"`
	Mapping           json.RawMessage `json:"mapping"`
	SheetName         string          `json:"sheetName"`
	ConfirmedAt       string          `json:"confirmedAt"`
	ConfirmedBatch    string          `json:"confirmedBatch"`
}

// w24TemplatePage mirrors the GET /api/mapping-templates envelope.
type w24TemplatePage struct {
	Items    []w24TemplateItem `json:"items"`
	Total    int               `json:"total"`
	Page     int               `json:"page"`
	PageSize int               `json:"pageSize"`
}

// w24SheetInfo mirrors the sheets[] entry (templateMatched included — the
// nullable per-sheet field of the W24 analysis).
type w24SheetInfo struct {
	Sheet           string               `json:"sheet"`
	TemplateMatched *w24TemplateMatchRef `json:"templateMatched"`
}

// w24TemplateMatchRef mirrors templateMatched.
type w24TemplateMatchRef struct {
	TemplateID   string `json:"templateId"`
	ResourceType string `json:"resourceType"`
	ConfirmedAt  string `json:"confirmedAt"`
}

// w24Analysis mirrors the analysis body (autoWarning included).
type w24Analysis struct {
	BatchID     string          `json:"batchId"`
	Sheets      []w24SheetInfo  `json:"sheets"`
	AutoWarning *w24AutoWarning `json:"autoWarning"`
}

// w24AutoWarning mirrors the refusal block.
type w24AutoWarning struct {
	Code            string   `json:"code"`
	UnmatchedSheets []string `json:"unmatchedSheets"`
	Message         string   `json:"message"`
}

// w24DeviceTotal returns the total device count via GET /api/resources
// ?type=device (the page envelope's total ignores pagination).
func w24DeviceTotal(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	resp, body := doRequest(t, srv, http.MethodGet, "/api/resources?type=device&page=1&pageSize=1", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resources list: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var page struct {
		Total int `json:"total"`
	}
	decodeJSON(t, "resources", body, &page)
	return page.Total
}

// uploadFileAuto posts one multipart upload (field "file") to /api/imports
// with the unattended flag: queryStyle uses ?auto=1, otherwise a multipart
// form field auto=1 — both documented interfaces of the same flag, both
// pinned here.
func uploadFileAuto(t *testing.T, srv *httptest.Server, filename string, content []byte, queryStyle bool) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if !queryStyle {
		if err := mw.WriteField("auto", "1"); err != nil {
			t.Fatalf("write auto field: %v", err)
		}
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	path := "/api/imports"
	if queryStyle {
		path += "?auto=1"
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, &buf)
	if err != nil {
		t.Fatalf("build upload request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("upload %s: do: %v", filename, err)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("upload %s: read body: %v", filename, err)
	}
	resp.Body.Close()
	return resp, data
}

// w24ConfirmMapping confirms the given batch with its suggested mapping
// (echoed back — the wizard's submit), returning the summary envelope.
func w24ConfirmMapping(t *testing.T, srv *httptest.Server, batchID string, suggested []byte) importSummary {
	t.Helper()
	resp, body := doRequest(t, srv, http.MethodPost, "/api/imports/"+batchID+"/mapping",
		suggested, "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("confirm mapping: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var sum importSummary
	decodeJSON(t, "mapping", body, &sum)
	return sum
}

// w24SuggestedMapping uploads the workbook (plain, no auto) and returns the
// analysis body as raw JSON (batchId + suggestedMapping echo).
func w24SuggestedMapping(t *testing.T, srv *httptest.Server, name string, wb []byte) (string, []byte, []byte) {
	t.Helper()
	resp, body := uploadFile(t, srv, name, wb)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload %s: status = %d, want 202\nbody: %s", name, resp.StatusCode, body)
	}
	var a struct {
		BatchID          string          `json:"batchId"`
		SuggestedMapping json.RawMessage `json:"suggestedMapping"`
		Sheets           []w24SheetInfo  `json:"sheets"`
	}
	decodeJSON(t, "upload", body, &a)
	return a.BatchID, a.SuggestedMapping, body
}

// TestAPIUnattendedSevenDays is the issue #28 acceptance (criterion 1):
// day 1 the ordinary human flow (upload → confirm → capture the template);
// days 2-8 SEVEN fresh workbooks with the SAME headers but DIFFERENT rows
// import through the AUTO path with ZERO human-interaction fields — only
// the file is posted — and all complete with correct row accounting and no
// duplicate resources; the template's fingerprint matched all seven (each
// workbook was freshly regenerated). The 8th workbook carries ONE RENAMED
// column: the auto path REFUSES naming the sheet (表头变更感知, criterion 2)
// and leaves the batch awaiting_mapping — the human fallback, not an error
// state.
func TestAPIUnattendedSevenDays(t *testing.T) {
	srv := newAPITestServer(t)

	// ---- Day 1: the ordinary flow (analyze → confirm → capture). ----
	day1 := w24Workbook(t, w24Headers, w24FixedRows(10))
	batchID, suggested, day1Body := w24SuggestedMapping(t, srv, "devices-day1.xlsx", day1)
	var a1 w24Analysis
	decodeJSON(t, "day1", day1Body, &a1)
	if a1.Sheets[0].TemplateMatched != nil {
		t.Fatalf("day1 templateMatched = %+v, want nil (no template captured yet)", a1.Sheets[0].TemplateMatched)
	}
	sum := w24ConfirmMapping(t, srv, batchID, suggested)
	if sum.Summary.RowsImported != 10 || sum.Summary.RowsFailed != 0 || sum.Status != "completed" {
		t.Fatalf("day1 summary = %+v, want 10 imported / 0 failed / completed", sum)
	}

	// The one-click save: capture the confirmed mapping of the completed batch.
	resp, body := doRequest(t, srv, http.MethodPost, "/api/imports/"+batchID+"/template", nil, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("capture template: status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}
	var captured struct {
		Items []w24TemplateItem `json:"items"`
		Total int               `json:"total"`
	}
	decodeJSON(t, "capture", body, &captured)
	if captured.Total != 1 || len(captured.Items) != 1 {
		t.Fatalf("captured = %d items (total %d), want 1", len(captured.Items), captured.Total)
	}
	tpl := captured.Items[0]
	// The fingerprint is recomputed from the batch's recorded workbook and
	// equals the canonical signature of the shape.
	if want := mapping.Fingerprint(w24Sheet, w24Headers); tpl.HeaderFingerprint != want {
		t.Errorf("captured fingerprint = %s, want %s (canonical sheet signature)", tpl.HeaderFingerprint, want)
	}
	if tpl.ResourceType != "device" || tpl.SheetName != w24Sheet || tpl.ConfirmedBatch != batchID {
		t.Errorf("captured template = %+v, want device/设备清单/creating batch", tpl)
	}
	if tpl.SourceName != "devices-day1.xlsx" {
		t.Errorf("captured sourceName = %q, want the workbook name default", tpl.SourceName)
	}

	// ---- Days 2-8: seven fresh workbooks through the AUTO path. ----
	// Each carries the SAME headers, the day-1 cohort (10 rows → skipped as
	// duplicate keys, no W005: identical values) and two day-fresh devices.
	// The request carries ONLY the file — zero mapping, zero confirmation.
	var autoSummaries []importSummary
	for day := 2; day <= 8; day++ {
		rows := append(append([][]string(nil), w24FixedRows(10)...), w24NewRows(day, 2)...)
		wb := w24Workbook(t, w24Headers, rows)
		name := "devices-day" + string(rune('0'+day)) + ".xlsx"
		// Alternate the two auto-flag interfaces (query / multipart field) —
		// both are pinned.
		resp, body := uploadFileAuto(t, srv, name, wb, day%2 == 0)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("day %d auto upload: status = %d, want 202\nbody: %s", day, resp.StatusCode, body)
		}
		var sum importSummary
		decodeJSON(t, "day "+string(rune('0'+day))+" auto", body, &sum)
		if sum.Status != "completed" {
			t.Fatalf("day %d auto status = %s, want completed (executed — fingerprint matched)", day, sum.Status)
		}
		if sum.Summary.RowsTotal != 12 || sum.Summary.RowsImported != 2 ||
			sum.Summary.RowsSkipped != 10 || sum.Summary.RowsFailed != 0 {
			t.Fatalf("day %d auto summary = %+v, want 12 total / 2 imported / 10 skipped / 0 failed", day, sum)
		}
		autoSummaries = append(autoSummaries, sum)
	}
	if len(autoSummaries) != 7 {
		t.Fatalf("auto imports completed = %d, want 7", len(autoSummaries))
	}

	// No duplicate resources: 10 day-1 devices + 7×2 day-fresh devices.
	if got := w24DeviceTotal(t, srv); got != 24 {
		t.Errorf("devices after the seven auto days = %d, want 24 (10 + 7×2, same keys skip — no duplicates)", got)
	}

	// The stored template matched every regenerated workbook: its
	// fingerprint is still the canonical one, and a PLAIN analyze of the
	// same shape reports the match (the banner input of the wizard).
	plain := append(append([][]string(nil), w24FixedRows(10)...), w24NewRows(8, 2)...)
	_, _, plainBody := w24SuggestedMapping(t, srv, "devices-plain.xlsx", w24Workbook(t, w24Headers, plain))
	var aPlain w24Analysis
	decodeJSON(t, "plain analyze", plainBody, &aPlain)
	if aPlain.Sheets[0].TemplateMatched == nil {
		t.Fatalf("plain analyze templateMatched = nil, want the captured template (fingerprint matched the regenerated workbook)")
	}
	if aPlain.Sheets[0].TemplateMatched.TemplateID != tpl.TemplateID ||
		aPlain.Sheets[0].TemplateMatched.ResourceType != "device" ||
		aPlain.Sheets[0].TemplateMatched.ConfirmedAt == "" {
		t.Errorf("templateMatched = %+v, want the full {templateId, resourceType, confirmedAt} block",
			aPlain.Sheets[0].TemplateMatched)
	}

	// ---- The 8th workbook: ONE RENAMED column → honest refusal. ----
	renamedHeaders := append([]string(nil), w24Headers...)
	renamedHeaders[1] = "设备名称（新）"
	renamed := w24Workbook(t, renamedHeaders, w24NewRows(9, 4))
	devicesBefore := w24DeviceTotal(t, srv)
	resp, body = uploadFileAuto(t, srv, "devices-renamed.xlsx", renamed, true)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("renamed auto upload: status = %d, want 202 (refusal is the designed fallback, not an error)\nbody: %s", resp.StatusCode, body)
	}
	var aRenamed w24Analysis
	decodeJSON(t, "renamed refusal", body, &aRenamed)
	if aRenamed.AutoWarning == nil || aRenamed.AutoWarning.Code != "no_matching_template" {
		t.Fatalf("renamed autoWarning = %+v, want code no_matching_template", aRenamed.AutoWarning)
	}
	if len(aRenamed.AutoWarning.UnmatchedSheets) != 1 || aRenamed.AutoWarning.UnmatchedSheets[0] != w24Sheet {
		t.Errorf("renamed unmatchedSheets = %v, want [%s] (表头变更感知 names the sheet)",
			aRenamed.AutoWarning.UnmatchedSheets, w24Sheet)
	}
	if !strings.Contains(aRenamed.AutoWarning.Message, w24Sheet) {
		t.Errorf("renamed warning message = %q, want it to name the sheet", aRenamed.AutoWarning.Message)
	}
	if aRenamed.Sheets[0].TemplateMatched != nil {
		t.Errorf("renamed sheet templateMatched = %+v, want null (renamed column ⇒ different fingerprint)", aRenamed.Sheets[0].TemplateMatched)
	}
	// The refusal leaves the batch awaiting_mapping — the human CAN still
	// confirm manually (the fallback, not an error state), and nothing was
	// written.
	if aRenamed.BatchID == "" {
		t.Fatalf("refused analysis has no batch id — the fallback flow needs it")
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/batches/"+aRenamed.BatchID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refused batch detail: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var bDetail struct {
		Status string `json:"status"`
	}
	decodeJSON(t, "refused batch", body, &bDetail)
	if bDetail.Status != "awaiting_mapping" {
		t.Errorf("refused batch status = %s, want awaiting_mapping (the human fallback, not an error state)", bDetail.Status)
	}
	if got := w24DeviceTotal(t, srv); got != devicesBefore {
		t.Errorf("devices after refusal = %d, want %d (no batch executed, no writes)", got, devicesBefore)
	}
}

// TestAPIImportTemplateLifecycle pins the template surface contracts around
// the journey: capture guards (unknown batch 404, not-completed batch 409),
// the listing envelope, DELETE (200 / repeat 404), the ROLLBACK ASYMMETRY
// (rolling back the creating batch does NOT delete the template — a
// confirmed artifact, not batch data), and the fallback after template
// deletion (auto refuses; the ordinary flow keeps working).
func TestAPIImportTemplateLifecycle(t *testing.T) {
	srv := newAPITestServer(t)

	// Import day 1 (upload → confirm) and capture.
	wb := w24Workbook(t, w24Headers, w24FixedRows(4))
	batchID, suggested, _ := w24SuggestedMapping(t, srv, "lifecycle.xlsx", wb)
	w24ConfirmMapping(t, srv, batchID, suggested)

	// Capture guards BEFORE completion happen on a fresh awaiting batch.
	wb2 := w24Workbook(t, w24Headers, w24NewRows(5, 1))
	awaitingID, _, _ := w24SuggestedMapping(t, srv, "awaiting.xlsx", wb2)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/imports/"+awaitingID+"/template", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("capture of awaiting batch: status = %d, want 409\nbody: %s", resp.StatusCode, body)
	}
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/bat_nope/template", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("capture of unknown batch: status = %d, want 404", resp.StatusCode)
	}

	// Capture with an explicit sourceName.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/"+batchID+"/template",
		[]byte(`{"sourceName":"机房日更模板"}`), "application/json")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("capture: status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}
	var captured w24TemplatePage
	decodeJSON(t, "capture", body, &captured)
	if captured.Total != 1 || captured.Items[0].SourceName != "机房日更模板" {
		t.Fatalf("capture with sourceName = %+v, want the explicit label", captured.Items[0])
	}
	tplID := captured.Items[0].TemplateID

	// Listing: the page envelope with the stored mapping echoed as an object.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/mapping-templates?page=1&pageSize=10", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("templates list: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var list w24TemplatePage
	decodeJSON(t, "templates list", body, &list)
	if list.Total != 1 || len(list.Items) != 1 || list.Page != 1 || list.PageSize != 10 {
		t.Fatalf("templates list = %+v, want the page envelope with 1 item", list)
	}
	var mappingDoc map[string]any
	if err := json.Unmarshal(list.Items[0].Mapping, &mappingDoc); err != nil {
		t.Fatalf("template mapping is not a JSON object: %v\nraw: %s", err, list.Items[0].Mapping)
	}
	if sheets, ok := mappingDoc["sheets"].([]any); !ok || len(sheets) != 1 {
		t.Fatalf("template mapping sheets = %v, want the single-sheet document", mappingDoc)
	}

	// ROLLBACK ASYMMETRY: rolling back the creating batch leaves the
	// template in place (a confirmed artifact survives its batch).
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/batches/"+batchID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback: status = %d\nbody: %s", resp.StatusCode, body)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/mapping-templates", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("templates after rollback: status = %d\n", resp.StatusCode)
	}
	decodeJSON(t, "templates after rollback", body, &list)
	if list.Total != 1 || list.Items[0].TemplateID != tplID {
		t.Fatalf("template after creating-batch rollback = %+v, want survival (confirmed artifact, not batch data)", list)
	}

	// Delete the template: 200, then the listing is empty, then 404.
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/mapping-templates/"+tplID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("template delete: status = %d\nbody: %s", resp.StatusCode, body)
	}
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/mapping-templates/"+tplID, nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("repeat template delete: status = %d, want 404", resp.StatusCode)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/mapping-templates", nil, "")
	decodeJSON(t, "templates after delete", body, &list)
	if list.Total != 0 || len(list.Items) != 0 {
		t.Fatalf("templates after delete = %+v, want empty", list)
	}

	// The fallback: with no template, the auto path refuses and the batch
	// stays awaiting_mapping; the ordinary confirm flow still completes it.
	resp, body = uploadFileAuto(t, srv, "post-delete.xlsx", wb, true)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("auto without template: status = %d, want 202 (refusal)\nbody: %s", resp.StatusCode, body)
	}
	var aRefused w24Analysis
	decodeJSON(t, "auto without template", body, &aRefused)
	if aRefused.AutoWarning == nil || aRefused.AutoWarning.Code != "no_matching_template" {
		t.Fatalf("auto without template warning = %+v, want no_matching_template", aRefused.AutoWarning)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/batches/"+aRefused.BatchID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refused batch detail: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var bAfter struct {
		Status string `json:"status"`
	}
	decodeJSON(t, "refused batch state", body, &bAfter)
	if bAfter.Status != "awaiting_mapping" {
		t.Fatalf("refused batch = %s, want awaiting_mapping (the human fallback state)", bAfter.Status)
	}
}
