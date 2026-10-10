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

	"github.com/mmqz/netops-lab/internal/api"
	"github.com/mmqz/netops-lab/internal/app"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/testfixtures"
)

// newAPITestServer wires the full application (DB + services + HTTP routes)
// against a throwaway database and upload directory and serves it over
// httptest. Both are closed at test cleanup.
func newAPITestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
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

// graphNode mirrors the nodes[] entry of the relations subgraph.
type graphNode struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	NaturalKey string `json:"naturalKey"`
	Name       string `json:"name"`
}

// graphEdge mirrors the edges[] entry of the relations subgraph.
type graphEdge struct {
	ID           string `json:"id"`
	RelationType string `json:"relationType"`
	FromID       string `json:"fromId"`
	ToID         string `json:"toId"`
}

// relationsResp mirrors GET /api/resources/{id}/relations.
type relationsResp struct {
	Center graphNode   `json:"center"`
	Nodes  []graphNode `json:"nodes"`
	Edges  []graphEdge `json:"edges"`
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

// topologyHop mirrors one hops[] entry of the topology check.
type topologyHop struct {
	DeviceID   string `json:"deviceId"`
	DeviceCode string `json:"deviceCode"`
}

// checkResp mirrors GET /api/topology/check.
type checkResp struct {
	Reachable  bool          `json:"reachable"`
	Hops       []topologyHop `json:"hops"`
	PathLength int           `json:"pathLength"`
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
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=device", nil, "")
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
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?q=DEV-001", nil, "")
	decodeJSON(t, "resources q filter", body, &page)
	if page.Total != 3 {
		t.Errorf("resources q=DEV-001: total = %d, want 3", page.Total)
	}
	// Status filter: DEV-016 and CKT-007 are the two 停用 resources.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?status="+url.QueryEscape("停用"), nil, "")
	decodeJSON(t, "resources status filter", body, &page)
	if page.Total != 2 {
		t.Errorf("resources status=停用: total = %d, want 2", page.Total)
	}

	// Pagination: page 3 with pageSize 30 covers the last 4 of 64.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?page=3&pageSize=30", nil, "")
	decodeJSON(t, "resources page 3", body, &page)
	if page.Total != 64 || page.Page != 3 || page.PageSize != 30 || len(page.Items) != 4 {
		t.Errorf("resources page=3 pageSize=30: total/page/pageSize/len = %d/%d/%d/%d, want 64/3/30/4",
			page.Total, page.Page, page.PageSize, len(page.Items))
	}
	// pageSize is clamped to the §9 maximum of 100.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?pageSize=500", nil, "")
	decodeJSON(t, "resources pageSize clamp", body, &page)
	if page.PageSize != 100 || len(page.Items) != 64 {
		t.Errorf("resources pageSize=500: pageSize/len = %d/%d, want 100/64", page.PageSize, len(page.Items))
	}

	// GET /api/resources/{id}: DEV-001 with sourceRefs and relationCount.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources?type=device&q=DEV-001", nil, "")
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

	// GET /api/resources/{id}/relations: default depth 1.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources/"+dev001+"/relations", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("relations default: status = %d, want 200", resp.StatusCode)
	}
	var rel relationsResp
	decodeJSON(t, "relations depth default", body, &rel)
	if rel.Center.NaturalKey != "DEV-001" || rel.Center.ID != dev001 {
		t.Errorf("relations default: center = %+v, want DEV-001", rel.Center)
	}
	depth1 := nodeKeys(rel.Nodes)
	for _, key := range []string{"DEV-002", "CKT-001", "POOL-01"} {
		if !depth1[key] {
			t.Errorf("relations depth 1: DEV-002/CKT-001/POOL-01 missing node %s (nodes %v)", key, rel.Nodes)
		}
	}
	if depth1["DEV-003"] {
		t.Errorf("relations depth 1: DEV-003 must not appear (1 device hop away is 2)")
	}

	// depth=2: DEV-003 joins the neighborhood (task-pinned).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources/"+dev001+"/relations?depth=2", nil, "")
	decodeJSON(t, "relations depth 2", body, &rel)
	depth2 := nodeKeys(rel.Nodes)
	for _, key := range []string{"DEV-002", "DEV-003", "CKT-001", "CKT-002", "POOL-01"} {
		if !depth2[key] {
			t.Errorf("relations depth 2: missing node %s", key)
		}
	}
	if depth2["DEV-004"] {
		t.Errorf("relations depth 2: DEV-004 must not appear (3 device hops)")
	}
	// One a_end edge lands on DEV-001 (CKT-001).
	aEndToDev001 := false
	for _, e := range rel.Edges {
		if e.RelationType == "a_end" && e.ToID == dev001 && e.FromID != "" {
			aEndToDev001 = true
		}
		if e.FromID == "" || e.ToID == "" || e.ID == "" {
			t.Fatalf("relations depth 2: malformed edge %+v", e)
		}
	}
	if !aEndToDev001 {
		t.Errorf("relations depth 2: no a_end edge ending at DEV-001 (edges: %d)", len(rel.Edges))
	}

	// GET /api/issues?severity=error: the 13 error issues with evidence.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/issues?severity=error&pageSize=100", nil, "")
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
	resp, body = doRequest(t, srv, http.MethodGet, "/api/issues?pageSize=100", nil, "")
	decodeJSON(t, "issues all", body, &ip)
	if ip.Total != 17 {
		t.Errorf("issues all: total = %d, want 17", ip.Total)
	}
	// batchId filter: the devices batch's 4 row-phase issues.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/issues?batchId="+devicesBatch, nil, "")
	decodeJSON(t, "issues by batch", body, &ip)
	if ip.Total != 4 {
		t.Errorf("issues batchId=devices: total = %d, want 4", ip.Total)
	}
	// ruleCode filter.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/issues?ruleCode="+domain.RuleSubnetOverlap, nil, "")
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
	resp, body = doRequest(t, srv, http.MethodPost, "/api/validate",
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
	if !strings.Contains(string(body), "合成测试数据") {
		t.Error("report html: synthetic-data disclaimer missing")
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

	// GET /api/topology/check: DEV-001 → DEV-003 is a 2-hop path.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/topology/check?fromDevice=DEV-001&toDevice=DEV-003", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("topology check: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var check checkResp
	decodeJSON(t, "topology check", body, &check)
	if !check.Reachable || check.PathLength != 2 || len(check.Hops) != 3 {
		t.Fatalf("topology check DEV-001→DEV-003: reachable=%v pathLength=%d hops=%d, want true/2/3",
			check.Reachable, check.PathLength, len(check.Hops))
	}
	wantPath := []string{"DEV-001", "DEV-002", "DEV-003"}
	for i, h := range check.Hops {
		if h.DeviceCode != wantPath[i] || h.DeviceID == "" {
			t.Errorf("topology check: hops[%d] = %+v, want deviceCode %s", i, h, wantPath[i])
		}
	}

	// DEV-001 → DEV-025 with maxHops=2: DEV-025 carries no circuit edge.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/topology/check?fromDevice=DEV-001&toDevice=DEV-025&maxHops=2", nil, "")
	decodeJSON(t, "topology unreachable", body, &check)
	if check.Reachable || check.PathLength != 0 || len(check.Hops) != 0 {
		t.Errorf("topology check DEV-001→DEV-025 maxHops=2: reachable=%v pathLength=%d hops=%d, want false/0/0",
			check.Reachable, check.PathLength, len(check.Hops))
	}

	// Same device: trivially reachable with a single-node path.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/topology/check?fromDevice=DEV-001&toDevice=DEV-001", nil, "")
	decodeJSON(t, "topology self", body, &check)
	if !check.Reachable || check.PathLength != 0 || len(check.Hops) != 1 || check.Hops[0].DeviceCode != "DEV-001" {
		t.Errorf("topology check DEV-001→DEV-001: %+v, want reachable/0 hops [DEV-001]", check)
	}

	// GET /api/batches: newest first (circuits, ippools, devices).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/batches", nil, "")
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
	resp, body = doRequest(t, srv, http.MethodGet, "/api/batches/"+devicesBatch, nil, "")
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
	resp, body = doRequest(t, srv, http.MethodGet, "/api/batches/"+devicesBatch+"/rows?status=failed&pageSize=10", nil, "")
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
	resp, body = doRequest(t, srv, http.MethodGet, "/api/stats", nil, "")
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

// nodeKeys indexes a node list by natural key.
func nodeKeys(nodes []graphNode) map[string]bool {
	keys := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		keys[n.NaturalKey] = true
	}
	return keys
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
		{"/api/resources/whatever/relations?depth=3", "bad depth"},
		{"/api/topology/check?fromDevice=DEV-001", "missing toDevice"},
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
	db, err := repo.Open(filepath.Join(t.TempDir(), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(api.New(log, db, "").Routes())
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
