// End-to-end HTTP API tests over the real app wiring (internal/app) mounted
// with httptest.NewServer. The response structs below re-declare the exact
// PROJECT_BRIEF §9 field names, so a field-name regression fails to compile.
//
// The import-flow test walks the full user journey — multipart upload → 202
// analysis (sheets/columns/suggestedMapping), mapping confirmation → 202 row
// accounting with the automatic set-level validation folded into the issue
// counts, then the read-side endpoints (resources, detail, relations,
// stats, issues, validate, report, topology), batch endpoints and rollback
// (409 batch_in_use refusal) — against the committed seeded workbooks
// (testdata/*.xlsx) whose expected outcomes are pinned in
// testdata/expected-summary.json and TASK_PROTOCOL §4.
package api_test

import (
	"bytes"
	"encoding/json"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-fees-lab/internal/api"
	"github.com/mmqz/netops-fees-lab/internal/app"
	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/mapping"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/services"
	"github.com/mmqz/netops-fees-lab/internal/testfixtures"
)

// newAPITestServer wires the full application (DB + services + HTTP routes)
// against a throwaway database and upload directory and serves it over
// httptest. Both are closed at test cleanup.
func newAPITestServer(t *testing.T) *httptest.Server {
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
		if err := a.Close(); err != nil {
			t.Errorf("app.Close: %v", err)
		}
	})
	return srv
}

// doRequest performs one HTTP request against srv and returns the response
// with its fully read body.
func doRequest(t *testing.T, srv *httptest.Server, method, path string, body []byte, contentType string) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("%s %s: build request: %v", method, path, err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: do: %v", method, path, err)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	resp.Body.Close()
	return resp, data
}

// uploadFile posts one multipart upload (field "file") to /api/imports.
func uploadFile(t *testing.T, srv *httptest.Server, filename string, content []byte) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
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
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/imports", &buf)
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

// readTestdata returns the bytes of a committed testdata artifact.
func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return data
}

// decodeJSON unmarshals body into v, failing the test on any error.
func decodeJSON(t *testing.T, ctx string, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("%s: decode JSON: %v\nbody: %s", ctx, err, body)
	}
}

// ---------------------------------------------------------------- §9 shapes —

// pageEnvelope is the shared pagination block of every list response.
type pageEnvelope struct {
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

// columnInfo mirrors the columns[] entry of the import analysis.
type columnInfo struct {
	Header         string   `json:"header"`
	Index          int      `json:"index"`
	Samples        []string `json:"samples"`
	SuggestedField string   `json:"suggestedField"`
	Confidence     float64  `json:"confidence"`
}

// sheetInfo mirrors the sheets[] entry of the import analysis.
type sheetInfo struct {
	Sheet            string       `json:"sheet"`
	RowCount         int          `json:"rowCount"`
	SkippedEmptyRows int          `json:"skippedEmptyRows"`
	Columns          []columnInfo `json:"columns"`
}

// importAnalysis mirrors the 202 body of POST /api/imports.
type importAnalysis struct {
	BatchID          string          `json:"batchId"`
	WorkbookName     string          `json:"workbookName"`
	SHA256           string          `json:"sha256"`
	Sheets           []sheetInfo     `json:"sheets"`
	SuggestedMapping json.RawMessage `json:"suggestedMapping"`
}

// suggestedMapping mirrors the prefilled mapping model (§8).
type suggestedMapping struct {
	Sheets []struct {
		Sheet         string            `json:"sheet"`
		Action        string            `json:"action"`
		ResourceType  string            `json:"resourceType"`
		KeyColumns    []string          `json:"keyColumns"`
		ColumnMapping map[string]string `json:"columnMapping"`
	} `json:"sheets"`
}

// importSummary mirrors the 202 body of POST /api/imports/{id}/mapping.
type importSummary struct {
	BatchID string `json:"batchId"`
	Status  string `json:"status"`
	Summary struct {
		RowsTotal    int `json:"rowsTotal"`
		RowsImported int `json:"rowsImported"`
		RowsSkipped  int `json:"rowsSkipped"`
		RowsFailed   int `json:"rowsFailed"`
	} `json:"summary"`
	Issues map[string]int `json:"issues"`
}

// resourceItem mirrors the GET /api/resources items[] entry.
type resourceItem struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	NaturalKey   string            `json:"naturalKey"`
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	Attributes   map[string]string `json:"attributes"`
	FirstBatchID string            `json:"firstBatchId"`
	UpdatedAt    string            `json:"updatedAt"`
}

// resourcesPage mirrors the GET /api/resources envelope.
type resourcesPage struct {
	Items []resourceItem `json:"items"`
	pageEnvelope
}

// sourceRef mirrors the sourceRefs[] entry of GET /api/resources/{id}.
type sourceRef struct {
	BatchID   string            `json:"batchId"`
	Workbook  string            `json:"workbook"`
	Sheet     string            `json:"sheet"`
	RowNumber int               `json:"rowNumber"`
	Raw       map[string]string `json:"raw"`
}

// resourceDetail mirrors the GET /api/resources/{id} body.
type resourceDetail struct {
	resourceItem
	SourceRefs    []sourceRef `json:"sourceRefs"`
	RelationCount int         `json:"relationCount"`
}

// statsResp mirrors GET /api/stats.
type statsResp struct {
	TotalResources   int            `json:"totalResources"`
	ByType           map[string]int `json:"byType"`
	ByStatus         map[string]int `json:"byStatus"`
	ByRelationType   map[string]int `json:"byRelationType"`
	IssuesBySeverity map[string]int `json:"issuesBySeverity"`
	LatestBatchID    string         `json:"latestBatchId"`
}

// issueSource mirrors the resolved source block of an issue.
type issueSource struct {
	Workbook  string `json:"workbook"`
	Sheet     string `json:"sheet"`
	RowNumber int    `json:"rowNumber"`
}

// issueItem mirrors the GET /api/issues items[] entry.
type issueItem struct {
	ID                   string            `json:"id"`
	RuleCode             string            `json:"ruleCode"`
	Severity             string            `json:"severity"`
	Message              string            `json:"message"`
	EntityKey            string            `json:"entityKey"`
	Source               *issueSource      `json:"source"`
	ApplicationGenerated bool              `json:"applicationGenerated"`
	Evidence             map[string]string `json:"evidence"`
}

// issuesPage mirrors the GET /api/issues envelope.
type issuesPage struct {
	Items []issueItem `json:"items"`
	pageEnvelope
}

// validateResp mirrors POST /api/validate.
type validateResp struct {
	Issues map[string]int `json:"issues"`
	Score  int            `json:"score"`
}

// reportResp mirrors the summary block of GET /api/report?format=json.
type reportResp struct {
	Scope   string `json:"scope"`
	Summary struct {
		TotalResources   int            `json:"totalResources"`
		ByType           map[string]int `json:"byType"`
		IssuesBySeverity map[string]int `json:"issuesBySeverity"`
		QualityScore     int            `json:"qualityScore"`
		Batches          []struct {
			Workbook     string `json:"workbook"`
			RowsTotal    int    `json:"rowsTotal"`
			RowsImported int    `json:"rowsImported"`
			RowsSkipped  int    `json:"rowsSkipped"`
			RowsFailed   int    `json:"rowsFailed"`
		} `json:"batches"`
	} `json:"summary"`
}

// batchItem mirrors one GET /api/batches entry.
type batchItem struct {
	ID           string `json:"id"`
	WorkbookName string `json:"workbookName"`
	Status       string `json:"status"`
	RowsTotal    int    `json:"rowsTotal"`
	RowsImported int    `json:"rowsImported"`
	RowsSkipped  int    `json:"rowsSkipped"`
	RowsFailed   int    `json:"rowsFailed"`
}

// batchesPage mirrors the GET /api/batches envelope.
type batchesPage struct {
	Items []batchItem `json:"items"`
	pageEnvelope
}

// batchDetailResp mirrors GET /api/batches/{id}.
type batchDetailResp struct {
	batchItem
	RuleVersion string          `json:"ruleVersion"`
	Mapping     json.RawMessage `json:"mapping"`
	Sheets      []struct {
		Sheet        string `json:"sheet"`
		RowsTotal    int    `json:"rowsTotal"`
		RowsImported int    `json:"rowsImported"`
		RowsSkipped  int    `json:"rowsSkipped"`
		RowsFailed   int    `json:"rowsFailed"`
	} `json:"sheets"`
}

// rowResultItem mirrors one GET /api/batches/{id}/rows entry.
type rowResultItem struct {
	BatchID   string `json:"batchId"`
	Sheet     string `json:"sheet"`
	RowNumber int    `json:"rowNumber"`
	Status    string `json:"status"`
	RuleCode  string `json:"ruleCode"`
	Message   string `json:"message"`
}

// rowsPage mirrors the GET /api/batches/{id}/rows envelope.
type rowsPage struct {
	Items []rowResultItem `json:"items"`
	pageEnvelope
}

// rollbackResp mirrors the DELETE /api/batches/{id} success body.
type rollbackResp struct {
	BatchID string `json:"batchId"`
	Status  string `json:"status"`
	Deleted struct {
		Resources  int `json:"resources"`
		Relations  int `json:"relations"`
		SourceRefs int `json:"sourceRefs"`
		RowResults int `json:"rowResults"`
		Issues     int `json:"issues"`
	} `json:"deleted"`
}

// errorResp mirrors the uniform error envelope.
type errorResp struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// assertIssues counts the three severity keys exactly.
func assertIssues(t *testing.T, ctx string, got, want map[string]int) {
	t.Helper()
	for _, sev := range []string{"error", "warning", "info"} {
		if got[sev] != want[sev] {
			t.Errorf("%s: issues[%q] = %d, want %d (got %v)", ctx, sev, got[sev], want[sev], got)
		}
	}
}

// sheetExpectation pins the §9 analysis shape of the one sheet each seeded
// workbook carries.
type sheetExpectation struct {
	name        string
	rowCount    int
	columns     int
	header      string  // a known column header
	field       string  // its suggested target field
	confidence  float64 // the expected suggestion confidence
	firstSample string  // the column's first sample value
}

// uploadAndConfirm walks one workbook through the full import flow: multipart
// upload → 202 analysis (sheets/columns/suggestedMapping per §9), then
// mapping confirmation with the SUGGESTED mapping (the UI prefills exactly
// this) → 202 summary + issue counts (row-phase + the set-level issues of
// the automatic post-import validation).
func uploadAndConfirm(t *testing.T, srv *httptest.Server, file string, wantSheet sheetExpectation, wantRows [4]int, wantIssues map[string]int) string {
	t.Helper()
	resp, body := uploadFile(t, srv, file, readTestdata(t, file))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload %s: status = %d, want 202\nbody: %s", file, resp.StatusCode, body)
	}
	var a importAnalysis
	decodeJSON(t, "upload "+file, body, &a)

	if a.BatchID == "" {
		t.Fatalf("upload %s: batchId is empty", file)
	}
	if a.WorkbookName != file {
		t.Errorf("upload %s: workbookName = %q, want %q", file, a.WorkbookName, file)
	}
	if len(a.SHA256) != 64 {
		t.Errorf("upload %s: sha256 length = %d, want 64", file, len(a.SHA256))
	}
	if len(a.Sheets) != 1 {
		t.Fatalf("upload %s: %d sheets, want 1", file, len(a.Sheets))
	}
	sh := a.Sheets[0]
	if sh.Sheet != wantSheet.name || sh.RowCount != wantSheet.rowCount || sh.SkippedEmptyRows != 0 {
		t.Errorf("upload %s: sheet = {%s %d %d}, want {%s %d 0}", file, sh.Sheet, sh.RowCount, sh.SkippedEmptyRows, wantSheet.name, wantSheet.rowCount)
	}
	if len(sh.Columns) != wantSheet.columns {
		t.Fatalf("upload %s: %d columns, want %d", file, len(sh.Columns), wantSheet.columns)
	}
	if sh.Columns[0].Index != 1 {
		t.Errorf("upload %s: columns[0].index = %d, want 1 (1-based)", file, sh.Columns[0].Index)
	}
	sawKnown := false
	for _, c := range sh.Columns {
		if c.Header != wantSheet.header {
			continue
		}
		sawKnown = true
		if c.SuggestedField != wantSheet.field || c.Confidence != wantSheet.confidence {
			t.Errorf("upload %s: column %q → %q@%v, want %q@%v", file, c.Header, c.SuggestedField, c.Confidence, wantSheet.field, wantSheet.confidence)
		}
		if len(c.Samples) == 0 || len(c.Samples) > 3 || c.Samples[0] != wantSheet.firstSample {
			t.Errorf("upload %s: column %q samples = %v, want ≤3 starting with %q", file, c.Header, c.Samples, wantSheet.firstSample)
		}
	}
	if !sawKnown {
		t.Errorf("upload %s: no column with header %q", file, wantSheet.header)
	}

	var sm suggestedMapping
	decodeJSON(t, "suggested mapping of "+file, a.SuggestedMapping, &sm)
	if len(sm.Sheets) != 1 {
		t.Fatalf("upload %s: suggested mapping has %d sheets, want 1", file, len(sm.Sheets))
	}
	if sm.Sheets[0].Action != "import" {
		t.Errorf("upload %s: suggested action = %q, want import", file, sm.Sheets[0].Action)
	}
	if sm.Sheets[0].Sheet != sh.Sheet {
		t.Errorf("upload %s: suggested sheet %q != analysis sheet %q", file, sm.Sheets[0].Sheet, sh.Sheet)
	}
	if sm.Sheets[0].ResourceType == "" || len(sm.Sheets[0].KeyColumns) == 0 || len(sm.Sheets[0].ColumnMapping) == 0 {
		t.Errorf("upload %s: suggested mapping incomplete: %+v", file, sm.Sheets[0])
	}

	// Confirm the suggested mapping.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/"+a.BatchID+"/mapping", a.SuggestedMapping, "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("mapping %s: status = %d, want 202\nbody: %s", file, resp.StatusCode, body)
	}
	var sum importSummary
	decodeJSON(t, "mapping "+file, body, &sum)
	if sum.BatchID != a.BatchID {
		t.Errorf("mapping %s: batchId = %q, want %q", file, sum.BatchID, a.BatchID)
	}
	if sum.Status != "completed" {
		t.Errorf("mapping %s: status = %q, want completed", file, sum.Status)
	}
	if got := [4]int{sum.Summary.RowsTotal, sum.Summary.RowsImported, sum.Summary.RowsSkipped, sum.Summary.RowsFailed}; got != wantRows {
		t.Errorf("mapping %s: summary = %+v, want {total:%d imported:%d skipped:%d failed:%d}",
			file, got, wantRows[0], wantRows[1], wantRows[2], wantRows[3])
	}
	assertIssues(t, "mapping "+file, sum.Issues, wantIssues)
	return a.BatchID
}

// TestAPIImportFlow walks the complete §9 import + read + rollback journey
// over the three seeded workbooks (canonical order devices → ippools →
// circuits).
func TestAPIImportFlow(t *testing.T) {
	srv := newAPITestServer(t)

	// The automatic post-import validation folds the current set-level
	// findings into the mapping response counts: after devices only, all 27
	// imported devices are unlinked (I001) and the 10.10.10.5 pair fires
	// E006; the ippools import adds the E005 overlap and links 15 devices
	// (POOL-01..08, 09..13, 15, 16 all carry assigned devices); the circuits
	// import links the chain, leaving DEV-030 as the only unlinked device.
	devicesBatch := uploadAndConfirm(t, srv, "devices.xlsx",
		sheetExpectation{
			name: testfixtures.DeviceSheetName, rowCount: 30, columns: len(testfixtures.DeviceHeaders),
			header: "设备编码", field: "device_code", confidence: 0.75, firstSample: "DEV-001",
		},
		[4]int{30, 27, 0, 3}, map[string]int{"error": 4, "warning": 1, "info": 27})
	poolsBatch := uploadAndConfirm(t, srv, "ippools.xlsx",
		sheetExpectation{
			name: testfixtures.IPPoolSheetName, rowCount: 20, columns: len(testfixtures.IPPoolHeaders),
			header: "CIDR", field: "cidr", confidence: 0.9, firstSample: "10.40.1.0/24",
		},
		[4]int{20, 16, 0, 4}, map[string]int{"error": 6, "warning": 1, "info": 12})
	circuitsBatch := uploadAndConfirm(t, srv, "circuits.xlsx",
		sheetExpectation{
			name: testfixtures.CircuitSheetName, rowCount: 25, columns: len(testfixtures.CircuitHeaders),
			header: "带宽Mbps", field: "bandwidth_mbps", confidence: 0.75, firstSample: "1000",
		},
		[4]int{25, 21, 0, 4}, map[string]int{"error": 6, "warning": 1, "info": 1})

	// Analysis structure spot-check is covered by the upload assertions in
	// uploadAndConfirm and the batch-detail assertions below.

	// GET /api/stats after the three imports.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/stats", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats: status = %d, want 200", resp.StatusCode)
	}
	var stats statsResp
	decodeJSON(t, "stats", body, &stats)
	if stats.TotalResources != 64 {
		t.Errorf("stats: totalResources = %d, want 64", stats.TotalResources)
	}
	for typ, want := range map[string]int{"device": 27, "ip_pool": 16, "circuit": 21} {
		if stats.ByType[typ] != want {
			t.Errorf("stats: byType[%s] = %d, want %d", typ, stats.ByType[typ], want)
		}
	}
	for rel, want := range map[string]int{"assigned_to": 15, "a_end": 21, "z_end": 21} {
		if stats.ByRelationType[rel] != want {
			t.Errorf("stats: byRelationType[%s] = %d, want %d", rel, stats.ByRelationType[rel], want)
		}
	}
	assertIssues(t, "stats", stats.IssuesBySeverity, map[string]int{"error": 13, "warning": 3, "info": 1})
	if stats.LatestBatchID != circuitsBatch {
		t.Errorf("stats: latestBatchId = %q, want the circuits batch %q", stats.LatestBatchID, circuitsBatch)
	}
	// Seeded statuses: 23 在网 devices + 20 在网 circuits; DEV-016 and CKT-007 停用.
	if stats.ByStatus["在网"] != 43 {
		t.Errorf("stats: byStatus[在网] = %d, want 43", stats.ByStatus["在网"])
	}
	if stats.ByStatus["停用"] != 2 {
		t.Errorf("stats: byStatus[停用] = %d, want 2", stats.ByStatus["停用"])
	}

	// GET /api/resources: default pagination shape.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resources: status = %d, want 200", resp.StatusCode)
	}
	var page resourcesPage
	decodeJSON(t, "resources default", body, &page)
	if page.Total != 64 || page.Page != 1 || page.PageSize != 20 || len(page.Items) != 20 {
		t.Errorf("resources default: total/page/pageSize/len = %d/%d/%d/%d, want 64/1/20/20",
			page.Total, page.Page, page.PageSize, len(page.Items))
	}
	// Ordering is (type, natural_key): circuits first, CKT-001 leading.
	if page.Items[0].Type != "circuit" || page.Items[0].NaturalKey != "CKT-001" {
		t.Errorf("resources default: items[0] = %s/%s, want circuit/CKT-001", page.Items[0].Type, page.Items[0].NaturalKey)
	}

	// Type filter.
	_, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=device", nil, "")
	decodeJSON(t, "resources type filter", body, &page)
	if page.Total != 27 || len(page.Items) != 20 {
		t.Errorf("resources type=device: total/len = %d/%d, want 27/20", page.Total, len(page.Items))
	}
	for _, it := range page.Items {
		if it.Type != "device" {
			t.Fatalf("resources type=device: item %s has type %q", it.ID, it.Type)
		}
	}

	// q filter: "DEV-001" matches the device natural key plus every resource
	// whose attributes mention it (CKT-001 a_end, POOL-01 assigned device).
	_, body = doRequest(t, srv, http.MethodGet, "/api/resources?q=DEV-001", nil, "")
	decodeJSON(t, "resources q filter", body, &page)
	if page.Total != 3 {
		t.Errorf("resources q=DEV-001: total = %d, want 3", page.Total)
	}
	// Status filter: DEV-016 and CKT-007 are the two 停用 resources.
	_, body = doRequest(t, srv, http.MethodGet, "/api/resources?status="+url.QueryEscape("停用"), nil, "")
	decodeJSON(t, "resources status filter", body, &page)
	if page.Total != 2 {
		t.Errorf("resources status=停用: total = %d, want 2", page.Total)
	}

	// Pagination: page 3 with pageSize 30 covers the last 4 of 64.
	_, body = doRequest(t, srv, http.MethodGet, "/api/resources?page=3&pageSize=30", nil, "")
	decodeJSON(t, "resources page 3", body, &page)
	if page.Total != 64 || page.Page != 3 || page.PageSize != 30 || len(page.Items) != 4 {
		t.Errorf("resources page=3 pageSize=30: total/page/pageSize/len = %d/%d/%d/%d, want 64/3/30/4",
			page.Total, page.Page, page.PageSize, len(page.Items))
	}
	// pageSize is clamped to the §9 maximum of 100.
	_, body = doRequest(t, srv, http.MethodGet, "/api/resources?pageSize=500", nil, "")
	decodeJSON(t, "resources pageSize clamp", body, &page)
	if page.PageSize != 100 || len(page.Items) != 64 {
		t.Errorf("resources pageSize=500: pageSize/len = %d/%d, want 100/64", page.PageSize, len(page.Items))
	}

	// GET /api/resources/{id}: DEV-001 with sourceRefs and relationCount.
	_, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=device&q=DEV-001", nil, "")
	decodeJSON(t, "find DEV-001", body, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("find DEV-001: total = %d, want 1", page.Total)
	}
	dev001 := page.Items[0].ID
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources/"+dev001, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource detail: status = %d, want 200", resp.StatusCode)
	}
	var det resourceDetail
	decodeJSON(t, "resource detail", body, &det)
	if det.NaturalKey != "DEV-001" || det.Type != "device" || det.Name != "杭州-核心路由器-001" || det.Status != "在网" {
		t.Errorf("resource detail: %s/%s/%q/%q, want DEV-001/device/杭州-核心路由器-001/在网",
			det.Type, det.NaturalKey, det.Name, det.Status)
	}
	if det.FirstBatchID != devicesBatch {
		t.Errorf("resource detail: firstBatchId = %q, want devices batch %q", det.FirstBatchID, devicesBatch)
	}
	if det.Attributes["mgmt_ip"] != "10.10.1.1" || det.Attributes["city"] != "杭州" {
		t.Errorf("resource detail: attributes = %v, want mgmt_ip 10.10.1.1, city 杭州", det.Attributes)
	}
	if det.RelationCount != 2 { // DEV-001: a_end of CKT-001 + to-end of POOL-01 assigned_to.
		t.Errorf("resource detail: relationCount = %d, want 2", det.RelationCount)
	}
	if len(det.SourceRefs) != 1 {
		t.Fatalf("resource detail: %d sourceRefs, want 1", len(det.SourceRefs))
	}
	ref := det.SourceRefs[0]
	if ref.Workbook != "devices.xlsx" || ref.Sheet != testfixtures.DeviceSheetName || ref.RowNumber != 2 {
		t.Errorf("resource detail: sourceRef = %+v, want devices.xlsx/%s/row 2", ref, testfixtures.DeviceSheetName)
	}
	if ref.Raw["设备编码"] != "DEV-001" {
		t.Errorf("resource detail: sourceRef raw 设备编码 = %q, want DEV-001", ref.Raw["设备编码"])
	}

	// GET /api/issues?severity=error: the 13 error issues with evidence.
	_, body = doRequest(t, srv, http.MethodGet, "/api/issues?severity=error&pageSize=100", nil, "")
	var ip issuesPage
	decodeJSON(t, "issues error", body, &ip)
	if ip.Total != 13 || len(ip.Items) != 13 {
		t.Fatalf("issues severity=error: total/len = %d/%d, want 13/13", ip.Total, len(ip.Items))
	}
	var sawE004Source, sawSetLevel bool
	for _, it := range ip.Items {
		if it.Severity != "error" {
			t.Errorf("issues severity=error: item %s severity %q", it.ID, it.Severity)
		}
		if it.RuleCode == domain.RuleInvalidFormat && it.Source != nil &&
			it.Source.Workbook == "devices.xlsx" && it.Source.Sheet == testfixtures.DeviceSheetName &&
			it.Source.RowNumber == testfixtures.DeviceInvalidIPRow {
			sawE004Source = true
		}
		if it.RuleCode == domain.RuleSubnetOverlap && it.ApplicationGenerated && it.Source == nil {
			sawSetLevel = true
		}
	}
	if !sawE004Source {
		t.Errorf("issues severity=error: no E004 issue with source devices.xlsx/%s/row %d",
			testfixtures.DeviceSheetName, testfixtures.DeviceInvalidIPRow)
	}
	if !sawSetLevel {
		t.Error("issues severity=error: no applicationGenerated set-level E005 issue")
	}

	// No filter: all 17 issues (14 row-phase + 3 set-level).
	_, body = doRequest(t, srv, http.MethodGet, "/api/issues?pageSize=100", nil, "")
	decodeJSON(t, "issues all", body, &ip)
	if ip.Total != 17 {
		t.Errorf("issues all: total = %d, want 17", ip.Total)
	}
	// batchId filter: the devices batch's 4 row-phase issues.
	_, body = doRequest(t, srv, http.MethodGet, "/api/issues?batchId="+devicesBatch, nil, "")
	decodeJSON(t, "issues by batch", body, &ip)
	if ip.Total != 4 {
		t.Errorf("issues batchId=devices: total = %d, want 4", ip.Total)
	}
	// ruleCode filter.
	_, body = doRequest(t, srv, http.MethodGet, "/api/issues?ruleCode="+domain.RuleSubnetOverlap, nil, "")
	decodeJSON(t, "issues by rule", body, &ip)
	if ip.Total != 1 || ip.Items[0].RuleCode != domain.RuleSubnetOverlap {
		t.Errorf("issues ruleCode=E005: total = %d, want 1", ip.Total)
	}

	// POST /api/validate: recomputes and answers counts + score 32.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/validate", nil, "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("validate: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var val validateResp
	decodeJSON(t, "validate", body, &val)
	assertIssues(t, "validate", val.Issues, map[string]int{"error": 13, "warning": 3, "info": 1})
	if val.Score != 32 {
		t.Errorf("validate: score = %d, want 32", val.Score)
	}

	// POST /api/validate with a batchId body: devices-batch scope (its row
	// issues + all set-level issues) scores 74, the T6 report cross-check.
	_, body = doRequest(t, srv, http.MethodPost, "/api/validate",
		[]byte(`{"batchId":"`+devicesBatch+`"}`), "application/json")
	decodeJSON(t, "validate batch scope", body, &val)
	assertIssues(t, "validate batch scope", val.Issues, map[string]int{"error": 5, "warning": 1, "info": 1})
	if val.Score != 74 {
		t.Errorf("validate batch scope: score = %d, want 74", val.Score)
	}

	// GET /api/report?format=html: standalone HTML with the disclaimer.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/report?format=html", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("report html: status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("report html: content-type = %q, want text/html; charset=utf-8", ct)
	}
	// issue #57②：免责声明已改为来源中立文案（不再断言「合成测试数据」——
	// 报告无法验证数据库内容物的来源，断言哪个都是越权）。
	if !strings.Contains(string(body), "本报告基于当前已导入的数据生成") {
		t.Error("report html: provenance-neutral disclaimer missing")
	}

	// GET /api/report?format=json: parses, score 32, inventory 64.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/report?format=json", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("report json: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("report json: content-type = %q, want application/json; charset=utf-8", ct)
	}
	var rep reportResp
	decodeJSON(t, "report json", body, &rep)
	if rep.Scope != "global" {
		t.Errorf("report json: scope = %q, want global", rep.Scope)
	}
	if rep.Summary.QualityScore != 32 {
		t.Errorf("report json: qualityScore = %d, want 32", rep.Summary.QualityScore)
	}
	if rep.Summary.TotalResources != 64 {
		t.Errorf("report json: totalResources = %d, want 64", rep.Summary.TotalResources)
	}
	assertIssues(t, "report json", rep.Summary.IssuesBySeverity, map[string]int{"error": 13, "warning": 3, "info": 1})
	if len(rep.Summary.Batches) != 3 {
		t.Fatalf("report json: %d batches, want 3", len(rep.Summary.Batches))
	}
	for _, b := range rep.Summary.Batches {
		var want [4]int
		switch b.Workbook {
		case "devices.xlsx":
			want = [4]int{30, 27, 0, 3}
		case "ippools.xlsx":
			want = [4]int{20, 16, 0, 4}
		case "circuits.xlsx":
			want = [4]int{25, 21, 0, 4}
		default:
			t.Errorf("report json: unexpected batch workbook %q", b.Workbook)
			continue
		}
		if got := [4]int{b.RowsTotal, b.RowsImported, b.RowsSkipped, b.RowsFailed}; got != want {
			t.Errorf("report json: batch %s rows = %+v, want %+v", b.Workbook, got, want)
		}
	}

	// GET /api/batches: newest first (circuits, ippools, devices).
	_, body = doRequest(t, srv, http.MethodGet, "/api/batches", nil, "")
	var bp batchesPage
	decodeJSON(t, "batches list", body, &bp)
	if bp.Total != 3 || len(bp.Items) != 3 {
		t.Fatalf("batches: total/len = %d/%d, want 3/3", bp.Total, len(bp.Items))
	}
	wantOrder := []string{circuitsBatch, poolsBatch, devicesBatch}
	for i, want := range wantOrder {
		if bp.Items[i].ID != want {
			t.Errorf("batches: items[%d] = %q, want %q (desc order)", i, bp.Items[i].ID, want)
		}
		if bp.Items[i].Status != "completed" {
			t.Errorf("batches: items[%d] status = %q, want completed", i, bp.Items[i].Status)
		}
	}
	if bp.Items[0].WorkbookName != "circuits.xlsx" || bp.Items[0].RowsTotal != 25 ||
		bp.Items[0].RowsImported != 21 || bp.Items[0].RowsFailed != 4 {
		t.Errorf("batches: circuits item = %+v", bp.Items[0])
	}

	// GET /api/batches/{id}: per-sheet summary + echoed mapping.
	_, body = doRequest(t, srv, http.MethodGet, "/api/batches/"+devicesBatch, nil, "")
	var bd batchDetailResp
	decodeJSON(t, "batch detail", body, &bd)
	if bd.ID != devicesBatch || bd.RuleVersion != domain.RuleVersion {
		t.Errorf("batch detail: id/ruleVersion = %q/%q", bd.ID, bd.RuleVersion)
	}
	if len(bd.Sheets) != 1 {
		t.Fatalf("batch detail: %d sheets, want 1", len(bd.Sheets))
	}
	if bd.Sheets[0].Sheet != testfixtures.DeviceSheetName ||
		bd.Sheets[0].RowsTotal != 30 || bd.Sheets[0].RowsImported != 27 || bd.Sheets[0].RowsFailed != 3 {
		t.Errorf("batch detail: sheets[0] = %+v, want {设备清单 30/27/0/3}", bd.Sheets[0])
	}
	var echoed suggestedMapping
	decodeJSON(t, "batch detail mapping", bd.Mapping, &echoed)
	if len(echoed.Sheets) != 1 || echoed.Sheets[0].Sheet != testfixtures.DeviceSheetName {
		t.Errorf("batch detail: echoed mapping = %s", bd.Mapping)
	}

	// GET /api/batches/{id}/rows?status=failed: the 3 failed device rows.
	_, body = doRequest(t, srv, http.MethodGet, "/api/batches/"+devicesBatch+"/rows?status=failed&pageSize=10", nil, "")
	var rp rowsPage
	decodeJSON(t, "batch rows", body, &rp)
	if rp.Total != 3 || len(rp.Items) != 3 {
		t.Fatalf("batch rows failed: total/len = %d/%d, want 3/3", rp.Total, len(rp.Items))
	}
	gotRows := map[int]string{}
	for _, rr := range rp.Items {
		if rr.Status != "failed" || rr.Sheet != testfixtures.DeviceSheetName || rr.Message == "" {
			t.Errorf("batch rows failed: %+v", rr)
		}
		gotRows[rr.RowNumber] = rr.RuleCode
	}
	wantRows := map[int]string{
		testfixtures.DeviceInvalidIPRow:   domain.RuleInvalidFormat,        // E004
		testfixtures.DeviceMissingNameRow: domain.RuleMissingRequiredField, // E002
		testfixtures.DeviceDupSecondRow:   domain.RuleDuplicateKey,         // E001
	}
	for row, code := range wantRows {
		if gotRows[row] != code {
			t.Errorf("batch rows failed: row %d ruleCode = %q, want %q", row, gotRows[row], code)
		}
	}

	// DELETE /api/batches/{circuits}: rollback OK, 21 circuits removed.
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/batches/"+circuitsBatch, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback circuits: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var rb rollbackResp
	decodeJSON(t, "rollback circuits", body, &rb)
	wantDeleted := struct {
		Resources  int `json:"resources"`
		Relations  int `json:"relations"`
		SourceRefs int `json:"sourceRefs"`
		RowResults int `json:"rowResults"`
		Issues     int `json:"issues"`
	}{Resources: 21, Relations: 42, SourceRefs: 63, RowResults: 25, Issues: 5}
	if rb.BatchID != circuitsBatch || rb.Status != "rolled_back" {
		t.Errorf("rollback circuits: batchId/status = %q/%q, want %q/rolled_back", rb.BatchID, rb.Status, circuitsBatch)
	}
	if rb.Deleted.Resources != wantDeleted.Resources ||
		rb.Deleted.Relations != wantDeleted.Relations ||
		rb.Deleted.SourceRefs != wantDeleted.SourceRefs ||
		rb.Deleted.RowResults != wantDeleted.RowResults ||
		rb.Deleted.Issues != wantDeleted.Issues {
		t.Errorf("rollback circuits: deleted = %+v, want %+v", rb.Deleted, wantDeleted)
	}

	// After the rollback the circuits are gone.
	_, body = doRequest(t, srv, http.MethodGet, "/api/stats", nil, "")
	decodeJSON(t, "stats after rollback", body, &stats)
	if stats.ByType["circuit"] != 0 || stats.TotalResources != 43 {
		t.Errorf("stats after rollback: circuit=%d total=%d, want 0/43", stats.ByType["circuit"], stats.TotalResources)
	}

	// DELETE /api/batches/{devices}: 409 batch_in_use — the ippool batch's
	// assigned_to relations reference devices created by this batch.
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/batches/"+devicesBatch, nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("rollback devices: status = %d, want 409\nbody: %s", resp.StatusCode, body)
	}
	var er errorResp
	decodeJSON(t, "rollback devices error", body, &er)
	if er.Error.Code != "batch_in_use" || er.Error.Message == "" {
		t.Errorf("rollback devices: error = %+v, want code batch_in_use with a message", er.Error)
	}
}

// TestAPIHealthAndSPA covers GET /api/health, the embedded SPA root, its
// asset serving and the index.html fallback for client-side routes.
func TestAPIHealthAndSPA(t *testing.T) {
	srv := newAPITestServer(t)

	resp, body := doRequest(t, srv, http.MethodGet, "/api/health", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("health: content-type = %q", ct)
	}
	var health struct {
		Status      string `json:"status"`
		Version     string `json:"version"`
		RuleVersion string `json:"ruleVersion"`
	}
	decodeJSON(t, "health", body, &health)
	if health.Status != "ok" || health.Version != domain.AppVersion || health.RuleVersion != domain.RuleVersion {
		t.Errorf("health: %+v, want ok/%s/%s", health, domain.AppVersion, domain.RuleVersion)
	}
	// API responses must never be browser-cacheable (regression: stale
	// heuristic caching once showed outdated statistics in the SPA).
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("health: cache-control = %q, want no-store", cc)
	}

	// SPA root serves the built index.html.
	resp, body = doRequest(t, srv, http.MethodGet, "/", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spa root: status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("spa root: content-type = %q", resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(string(body), `<div id="root">`) {
		t.Error("spa root: index.html marker missing")
	}

	// SPA fallback: a client-side route answers the same index.html.
	resp, body = doRequest(t, srv, http.MethodGet, "/import", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("spa fallback: status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), `<div id="root">`) {
		t.Error("spa fallback: index.html marker missing")
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("spa fallback: cache-control = %q, want no-cache", cc)
	}

	// A built asset is served from the embedded dist tree.
	re := regexp.MustCompile(`src="(/assets/[^"]+\.js)"`)
	m := re.FindSubmatch(body)
	if m == nil {
		t.Fatalf("spa fallback: no asset script in index.html:\n%s", body)
	}
	resp, body = doRequest(t, srv, http.MethodGet, string(m[1]), nil, "")
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Errorf("spa asset %s: status = %d, %d bytes", m[1], resp.StatusCode, len(body))
	}

	// Unknown /api/ paths answer the JSON 404 envelope, never the SPA.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/unknown", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown api path: status = %d, want 404", resp.StatusCode)
	}
	var er errorResp
	decodeJSON(t, "unknown api path", body, &er)
	if er.Error.Code != "not_found" {
		t.Errorf("unknown api path: code = %q, want not_found", er.Error.Code)
	}
}

// TestAPISchemaEndpoint pins the GET /api/schema contract: JSON envelope,
// no-store, the full Specs registry (type order, per-type key/name rules,
// field-level facts) and the 13-code rule catalog with severities — as
// generated by mapping.BuildSchemaDocument, not hand-copied.
func TestAPISchemaEndpoint(t *testing.T) {
	srv := newAPITestServer(t)

	resp, body := doRequest(t, srv, http.MethodGet, "/api/schema", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("schema: status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("schema: content-type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("schema: cache-control = %q, want no-store", cc)
	}

	type schemaFieldJSON struct {
		Name       string   `json:"name"`
		Label      string   `json:"label"`
		Required   bool     `json:"required"`
		NaturalKey bool     `json:"naturalKey"`
		Kind       string   `json:"kind"`
		Enum       []string `json:"enum"`
		IntMin     int      `json:"intMin"`
		IntMax     int      `json:"intMax"`
		RefType    string   `json:"refType"`
		Synonyms   []string `json:"synonyms"`
	}
	type schemaTypeJSON struct {
		Type              string            `json:"type"`
		Label             string            `json:"label"`
		SheetHint         string            `json:"sheetHint"`
		NameField         string            `json:"nameField"`
		NameFieldOverride string            `json:"nameFieldOverride"`
		NaturalKeyFields  []string          `json:"naturalKeyFields"`
		KeyFallbackFields []string          `json:"keyFallbackFields"`
		Fields            []schemaFieldJSON `json:"fields"`
	}
	var doc struct {
		Types []schemaTypeJSON `json:"types"`
		Rules []struct {
			Code        string `json:"code"`
			Severity    string `json:"severity"`
			Description string `json:"description"`
		} `json:"rules"`
	}
	decodeJSON(t, "schema", body, &doc)

	// The types are the Specs registry, in registry order.
	if len(doc.Types) != len(mapping.Specs) {
		t.Fatalf("schema: %d types, want %d (len of mapping.Specs)", len(doc.Types), len(mapping.Specs))
	}
	byType := map[string]*schemaTypeJSON{}
	for i := range doc.Types {
		st := &doc.Types[i]
		if st.Type != string(mapping.Specs[i].Type) {
			t.Errorf("schema types[%d].type = %q, want %q (Specs order)", i, st.Type, mapping.Specs[i].Type)
		}
		byType[st.Type] = st
	}

	dev := byType["device"]
	if dev == nil {
		t.Fatal("schema: device type missing")
	}
	if dev.Label != "网元设备" || dev.NameField != "device_name" || dev.NameFieldOverride != "" {
		t.Errorf("schema device label/nameField/override = %q/%q/%q", dev.Label, dev.NameField, dev.NameFieldOverride)
	}
	if len(dev.NaturalKeyFields) != 1 || dev.NaturalKeyFields[0] != "device_code" {
		t.Errorf("schema device naturalKeyFields = %v, want [device_code]", dev.NaturalKeyFields)
	}
	if len(dev.KeyFallbackFields) != 0 {
		t.Errorf("schema device keyFallbackFields = %v, want absent", dev.KeyFallbackFields)
	}
	var statusEnum []string
	var mgmtIPKind string
	for _, f := range dev.Fields {
		switch f.Name {
		case "status":
			statusEnum = f.Enum
		case "mgmt_ip":
			mgmtIPKind = f.Kind
		}
	}
	if len(statusEnum) != len(mapping.StatusEnum) {
		t.Errorf("schema device.status enum = %v, want %v", statusEnum, mapping.StatusEnum)
	}
	if mgmtIPKind != "ipv4" {
		t.Errorf("schema device.mgmt_ip kind = %q, want ipv4", mgmtIPKind)
	}

	port := byType["port"]
	if port == nil {
		t.Fatal("schema: port type missing")
	}
	if len(port.NaturalKeyFields) != 2 || port.NaturalKeyFields[0] != "device_name" || port.NaturalKeyFields[1] != "port_name" {
		t.Errorf("schema port naturalKeyFields = %v, want [device_name port_name]", port.NaturalKeyFields)
	}
	if port.NameField != "port_name" || port.NameFieldOverride != "port_name" {
		t.Errorf("schema port nameField/override = %q/%q, want port_name twice", port.NameField, port.NameFieldOverride)
	}

	bs := byType["base_station"]
	if bs == nil {
		t.Fatal("schema: base_station type missing")
	}
	if len(bs.KeyFallbackFields) != 2 || bs.KeyFallbackFields[0] != "city" || bs.KeyFallbackFields[1] != "base_station_name" {
		t.Errorf("schema base_station keyFallbackFields = %v, want [city base_station_name]", bs.KeyFallbackFields)
	}

	// W9-D: the int bounds of ip_pool.vlan_id survive the JSON round trip
	// (intMax joined the projection this round; intMin was already there).
	pool := byType["ip_pool"]
	if pool == nil {
		t.Fatal("schema: ip_pool type missing")
	}
	var vlanMin, vlanMax int
	for _, f := range pool.Fields {
		if f.Name == "vlan_id" {
			vlanMin, vlanMax = f.IntMin, f.IntMax
		}
	}
	if vlanMin != 1 || vlanMax != 4094 {
		t.Errorf("schema ip_pool.vlan_id intMin/intMax = %d/%d, want 1/4094", vlanMin, vlanMax)
	}

	// W9-D (W5-Q P3-5): the ptp_port sheetHint must decode non-empty — a
	// misspelled JSON tag on the projection would silently emit "". The real
	// spec value is "PTP" (the substring anchor that matches the synthesized
	// virtual sheet "PTP配置" — the sheet NAME is PTP配置, the HINT is PTP);
	// the W8-D api.md example quoted the sheet name here and is corrected to
	// the projected value this round.
	ptp := byType["ptp_port"]
	if ptp == nil {
		t.Fatal("schema: ptp_port type missing")
	}
	if ptp.SheetHint != "PTP" {
		t.Errorf("schema ptp_port sheetHint = %q, want PTP (the substring hint; JSON tag decode)", ptp.SheetHint)
	}
	if ptp.NameField != "port_name" || ptp.NameFieldOverride != "port_name" {
		t.Errorf("schema ptp_port nameField/override = %q/%q, want port_name twice", ptp.NameField, ptp.NameFieldOverride)
	}

	mr := byType["machine_room"]
	if mr == nil {
		t.Fatal("schema: machine_room type missing")
	}
	siteRef := ""
	for _, f := range mr.Fields {
		if f.Name == "site_id" {
			siteRef = f.RefType
		}
	}
	if siteRef != "site" {
		t.Errorf("schema machine_room.site_id refType = %q, want site", siteRef)
	}

	// The rules are the domain catalog, each with a severity and a gloss.
	if len(doc.Rules) != len(domain.AllRuleCodes()) {
		t.Fatalf("schema: %d rules, want %d (len of domain.AllRuleCodes)", len(doc.Rules), len(domain.AllRuleCodes()))
	}
	for i, want := range domain.AllRuleCodes() {
		got := doc.Rules[i]
		if got.Code != want {
			t.Errorf("schema rules[%d].code = %q, want %q", i, got.Code, want)
		}
		sev, ok := domain.SeverityForCode(want)
		if !ok || got.Severity != sev.String() {
			t.Errorf("schema rule %s: severity = %q, want %q", want, got.Severity, sev)
		}
		if got.Description == "" {
			t.Errorf("schema rule %s: description is empty", want)
		}
	}
}

// TestAPIErrorPaths covers the error envelope across status codes: 400
// (bad parameters), 404 (unknown ids), 409 (non-completed rollback) and
// 422 (non-xlsx uploads, by extension and by content).
func TestAPIErrorPaths(t *testing.T) {
	srv := newAPITestServer(t)

	assertError := func(ctx string, resp *http.Response, body []byte, wantStatus int, wantCode string) {
		t.Helper()
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s: status = %d, want %d\nbody: %s", ctx, resp.StatusCode, wantStatus, body)
		}
		var er errorResp
		decodeJSON(t, ctx, body, &er)
		if er.Error.Code != wantCode {
			t.Errorf("%s: error.code = %q, want %q", ctx, er.Error.Code, wantCode)
		}
		if er.Error.Message == "" {
			t.Errorf("%s: error.message is empty", ctx)
		}
	}

	// 404 unknown resource id.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/resources/res_missing", nil, "")
	assertError("resource 404", resp, body, http.StatusNotFound, "not_found")

	// 404 unknown resource id for relations.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources/res_missing/relations?depth=1", nil, "")
	assertError("relations 404", resp, body, http.StatusNotFound, "not_found")

	// 404 unknown batch.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/batches/bat_missing", nil, "")
	assertError("batch 404", resp, body, http.StatusNotFound, "not_found")
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/batches/bat_missing", nil, "")
	assertError("rollback 404", resp, body, http.StatusNotFound, "not_found")

	// 404 unknown batch for mapping confirmation.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/bat_missing/mapping",
		[]byte(`{"sheets":[]}`), "application/json")
	assertError("mapping 404", resp, body, http.StatusNotFound, "not_found")

	// 404 unknown device in the topology check.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/topology/check?fromDevice=DEV-001&toDevice=DEV-9999", nil, "")
	assertError("topology unknown device", resp, body, http.StatusNotFound, "not_found")

	// 404 validate with an unknown batchId body.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/validate",
		[]byte(`{"batchId":"bat_missing"}`), "application/json")
	assertError("validate 404", resp, body, http.StatusNotFound, "not_found")

	// 400 parameter validation.
	cases := []struct {
		path string
		ctx  string
	}{
		{"/api/resources?type=bogus", "bad type"},
		{"/api/resources?page=0", "page 0"},
		{"/api/resources?pageSize=abc", "pageSize non-numeric"},
		{"/api/issues?severity=bogus", "bad severity"},
		{"/api/report?format=xml", "bad format"},
	}
	for _, tc := range cases {
		resp, body = doRequest(t, srv, http.MethodGet, tc.path, nil, "")
		assertError(tc.ctx, resp, body, http.StatusBadRequest, "bad_request")
	}

	// 400 upload without the "file" multipart field.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.Close(); err != nil {
		t.Fatalf("close empty multipart: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/imports", &buf)
	if err != nil {
		t.Fatalf("build empty multipart: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	eresp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("empty multipart: %v", err)
	}
	eb, _ := io.ReadAll(eresp.Body)
	eresp.Body.Close()
	assertError("missing file field", eresp, eb, http.StatusBadRequest, "bad_request")

	// 422 non-xlsx by extension.
	resp, body = uploadFile(t, srv, "devices.csv", []byte("a,b,c\n1,2,3\n"))
	assertError("csv upload", resp, body, http.StatusUnprocessableEntity, "not_xlsx")

	// 422 non-xlsx by content (xlsx extension, garbage bytes).
	resp, body = uploadFile(t, srv, "fake.xlsx", []byte("this is not an xlsx container"))
	assertError("garbage xlsx upload", resp, body, http.StatusUnprocessableEntity, "not_xlsx")

	// 409 rollback of a non-completed batch (the freshly analyzed batch is
	// awaiting_mapping) — a plain conflict, not batch_in_use.
	aresp, abody := uploadFile(t, srv, "devices.xlsx", readTestdata(t, "devices.xlsx"))
	if aresp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload for 409: status = %d, want 202", aresp.StatusCode)
	}
	var a importAnalysis
	decodeJSON(t, "upload for 409", abody, &a)
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/batches/"+a.BatchID, nil, "")
	assertError("rollback awaiting batch", resp, body, http.StatusConflict, "conflict")
}

// TestAPIImportBodyLimit covers the 10MB import body limit (§9): an
// oversized multipart upload answers 413 too_large.
func TestAPIImportBodyLimit(t *testing.T) {
	srv := newAPITestServer(t)

	big := bytes.Repeat([]byte{0x78}, 10<<20+64) // 10MB + 64B
	resp, body := uploadFile(t, srv, "big.xlsx", big)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize upload: status = %d, want 413\nbody: %s", resp.StatusCode, body)
	}
	var er errorResp
	decodeJSON(t, "oversize upload", body, &er)
	if er.Error.Code != "too_large" {
		t.Errorf("oversize upload: error.code = %q, want too_large", er.Error.Code)
	}
}

// TestAPIUploadWithoutDataDirFailsFast pins the R2-P3-4 fix: a server wired
// with an EMPTY dataDir rejects the upload at the handler entry with a 500
// explaining the missing configuration, instead of silently storing to a
// relative "uploads/…" path that the analyze step could never read back.
func TestAPIUploadWithoutDataDirFailsFast(t *testing.T) {
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(api.New(log, services.New(db), "").Routes())
	t.Cleanup(srv.Close)

	resp, body := uploadFile(t, srv, "devices.xlsx", readTestdata(t, "devices.xlsx"))
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("upload without dataDir: status = %d, want 500\nbody: %s", resp.StatusCode, body)
	}
	var er errorResp
	decodeJSON(t, "upload without dataDir", body, &er)
	if er.Error.Code != "internal" {
		t.Errorf("upload without dataDir: error.code = %q, want internal", er.Error.Code)
	}
	if !strings.Contains(er.Error.Message, "dataDir") {
		t.Errorf("upload without dataDir: message %q should name the missing dataDir configuration", er.Error.Message)
	}
}

// TestAPISiteMachineRoomBelongsTo walks the two-sheet 机房局站 import story
// over the full HTTP API (one batch, the site sheet imported BEFORE the
// machine-room sheet so the ref resolves batch-locally), then pins the
// read-side contract of belongs_to: the machine_room resource detail counts
// the relation, the relations subgraph carries relationType "belongs_to"
// edge-by-edge, and /api/stats reports both new types plus the new relation
// type (v0.4 W3-D, migration v10).
func TestAPISiteMachineRoomBelongsTo(t *testing.T) {
	srv := newAPITestServer(t)

	// A two-sheet workbook in the real 8-column layout: a slim 局站表 (the
	// site pass) and the 机房局站表 (the machine-room pass) carrying the real
	// pasted row. Built in memory, uploaded as multipart.
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", "局站表"); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	if _, err := f.NewSheet("机房局站表"); err != nil {
		t.Fatalf("new sheet: %v", err)
	}
	siteHeaders := []string{"地市", "区县", "设备名称", "IP地址", "局站ID", "局站名称"}
	siteRow := []string{"大兴", "呼区", "源1", "4.8.14.8", "231001824048", "源"}
	mrHeaders := []string{"地市", "区县", "设备名称", "IP地址", "机房ID", "机房名称", "局站ID", "局站名称"}
	mrRow := []string{"大兴", "呼区", "源1", "4.8.14.8", "23100120000018049", "呼", "231001824048", "源"}
	for i, row := range [][]string{siteHeaders, siteRow} {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, i+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr("局站表", cell, v); err != nil {
				t.Fatalf("set cell: %v", err)
			}
		}
	}
	for i, row := range [][]string{mrHeaders, mrRow} {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, i+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr("机房局站表", cell, v); err != nil {
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

	// Upload → 202 analysis. The 局站表 hint suggests site; the double-hint
	// 机房局站表 ALSO suggests site (site is registered before machine_room —
	// the suggestion is a prefill, the confirmed mapping below switches the
	// second sheet to machine_room).
	resp, body := uploadFile(t, srv, "site-machine-room.xlsx", buf.Bytes())
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var a importAnalysis
	decodeJSON(t, "upload", body, &a)
	var sm suggestedMapping
	decodeJSON(t, "suggested mapping", a.SuggestedMapping, &sm)
	if len(sm.Sheets) != 2 {
		t.Fatalf("suggested mapping has %d sheets, want 2", len(sm.Sheets))
	}
	for _, s := range sm.Sheets {
		if s.Action != "import" || s.ResourceType != "site" {
			t.Errorf("suggested mapping of %q = %s/%s, want import/site (prefill; user switches pass 2)", s.Sheet, s.Action, s.ResourceType)
		}
	}

	// Confirm a CUSTOM mapping: the site sheet first, then the same physical
	// family as machine_room with the 局站ID ref — the story's two readings in
	// one batch (sheet order = ref resolution order).
	mapping := []byte(`{"sheets":[
                {"sheet":"局站表","action":"import","resourceType":"site","keyColumns":["site_id"],
                 "columnMapping":{"site_id":"局站ID","site_name":"局站名称","city":"地市","district":"区县","device_name":"设备名称","ip_address":"IP地址"}},
                {"sheet":"机房局站表","action":"import","resourceType":"machine_room","keyColumns":["machine_room_id"],
                 "columnMapping":{"machine_room_id":"机房ID","machine_room_name":"机房名称","site_id":"局站ID","city":"地市","district":"区县","device_name":"设备名称","ip_address":"IP地址"}}
        ]}`)
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/"+a.BatchID+"/mapping", mapping, "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("mapping: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var sum importSummary
	decodeJSON(t, "mapping", body, &sum)
	if sum.Status != "completed" || sum.Summary.RowsTotal != 2 || sum.Summary.RowsImported != 2 ||
		sum.Summary.RowsSkipped != 0 || sum.Summary.RowsFailed != 0 {
		t.Fatalf("summary = %+v, want completed 2 total / 2 imported / 0 skipped / 0 failed", sum)
	}

	// GET /api/resources?type=machine_room → the room keyed by 机房ID.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=machine_room", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resources: status = %d, want 200", resp.StatusCode)
	}
	var rooms resourcesPage
	decodeJSON(t, "resources", body, &rooms)
	if rooms.Total != 1 || len(rooms.Items) != 1 {
		t.Fatalf("machine_room resources = %d (total %d), want 1/1", len(rooms.Items), rooms.Total)
	}
	room := rooms.Items[0]
	if room.NaturalKey != "23100120000018049" || room.Name != "呼" || room.Type != "machine_room" {
		t.Errorf("machine_room item = %s/%s/%s, want 23100120000018049/呼/machine_room", room.Type, room.NaturalKey, room.Name)
	}
	if room.Attributes["site_id"] != "231001824048" {
		t.Errorf("machine_room attrs site_id = %q, want 231001824048 (the ref value, like ip_pool.assigned_device)", room.Attributes["site_id"])
	}

	// GET /api/resources/{id} → relationCount counts the belongs_to edge.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources/"+room.ID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource detail: status = %d, want 200", resp.StatusCode)
	}
	var detail resourceDetail
	decodeJSON(t, "resource detail", body, &detail)
	if detail.RelationCount != 1 {
		t.Fatalf("resource detail relationCount = %d, want 1", detail.RelationCount)
	}
	if len(detail.SourceRefs) != 1 || detail.SourceRefs[0].Sheet != "机房局站表" || detail.SourceRefs[0].RowNumber != 2 {
		t.Errorf("source refs = %+v, want one ref at 机房局站表 row 2", detail.SourceRefs)
	}

	// GET /api/stats → both new types counted, belongs_to in byRelationType.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/stats", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats: status = %d, want 200", resp.StatusCode)
	}
	var stats statsResp
	decodeJSON(t, "stats", body, &stats)
	if stats.ByType["site"] != 1 || stats.ByType["machine_room"] != 1 || stats.TotalResources != 2 {
		t.Errorf("stats byType = %v (total %d), want site 1 / machine_room 1 / total 2", stats.ByType, stats.TotalResources)
	}
	if stats.ByRelationType["belongs_to"] != 1 {
		t.Errorf("stats byRelationType = %v, want belongs_to 1", stats.ByRelationType)
	}

	// The type validation message accepts the new types; a bogus type still
	// 400s (closed-set enforcement).
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/resources?type=building", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("resources?type=building: status = %d, want 400", resp.StatusCode)
	}
}
