package api_test

// The HTTP surface of the fee analysis layer (#58/#60/#61): the import
// journey (201 fresh / 200 duplicate / 422 all-rows-failed / 400 params),
// the batch list, the rollback (Parquet gone + snapshot kept + 409
// re-rollback), and the query presets' engine-state contract (501
// engine_not_built on default builds — the duckdb-tagged build covers
// the real engine path in internal/fees).

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/fees"
)

// uploadFeeFile posts the fee import multipart (file + feeType + year +
// month [+ map]) to /api/fees/imports.
func uploadFeeFile(t *testing.T, srv *httptest.Server, filename, content, feeType, year, month, mapping string) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	for k, v := range map[string]string{"feeType": feeType, "year": year, "month": month} {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if mapping != "" {
		if err := mw.WriteField("map", mapping); err != nil {
			t.Fatalf("write map field: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/fees/imports", &buf)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post fee import: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	_ = resp.Body.Close()
	return resp, body
}

// feeImportSummary mirrors the POST /api/fees/imports body.
type feeImportSummary struct {
	BatchID          string   `json:"batchId"`
	Status           string   `json:"status"`
	RowsTotal        int      `json:"rowsTotal"`
	RowsImported     int      `json:"rowsImported"`
	RowsFailed       int      `json:"rowsFailed"`
	SkippedDuplicate bool     `json:"skippedDuplicate"`
	ExistingBatch    string   `json:"existingBatch"`
	Files            []string `json:"files"`
	Errors           []string `json:"errors"`
}

func TestAPIFeesImportJourney(t *testing.T) {
	srv := newAPITestServer(t)

	csv := "站址编码,账期,电费(元)\n0230101ABCDEFG01,2024-05,1234.56\n0230101ABCDEFG02,2024-05,654.00\n"

	// fresh import → 201 + summary.
	resp, body := uploadFeeFile(t, srv, "may.csv", csv, "electricity", "2024", "5", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("import: status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}
	var sum feeImportSummary
	if err := json.Unmarshal(body, &sum); err != nil {
		t.Fatalf("decode summary: %v\n%s", err, body)
	}
	if sum.Status != "completed" || sum.RowsImported != 2 || sum.RowsTotal != 2 || len(sum.Files) != 1 {
		t.Fatalf("summary = %+v, want completed 2/2 with one partition file", sum)
	}

	// idempotent duplicate → 200 + skippedDuplicate + existingBatch.
	resp, body = uploadFeeFile(t, srv, "may.csv", csv, "electricity", "2024", "5", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("duplicate import: status = %d, want 200 (informational)\nbody: %s", resp.StatusCode, body)
	}
	var dup feeImportSummary
	if err := json.Unmarshal(body, &dup); err != nil {
		t.Fatalf("decode duplicate: %v", err)
	}
	if !dup.SkippedDuplicate || dup.ExistingBatch != sum.BatchID {
		t.Fatalf("duplicate summary = %+v, want skippedDuplicate with the existing batch", dup)
	}

	// the snapshot landed under the data dir's content-addressed tree
	// (verified via the batches list's snapshotPath).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/batches?status=completed", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batches list: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var list struct {
		Items []struct {
			ID           string `json:"id"`
			FeeTypeLabel string `json:"feeTypeLabel"`
			Period       string `json:"period"`
			Status       string `json:"status"`
			SnapshotPath string `json:"snapshotPath"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode batches: %v", err)
	}
	if list.Total != 1 || list.Items[0].ID != sum.BatchID ||
		list.Items[0].FeeTypeLabel != "电费" || list.Items[0].Period != "2024-05" {
		t.Fatalf("batches = %+v, want the one completed 电费 2024-05 batch", list)
	}
	snapRel := list.Items[0].SnapshotPath
	if snapRel == "" || !strings.HasPrefix(snapRel, "fee-imports/electricity/2024/05/") {
		t.Fatalf("snapshotPath = %q, want the content-addressed tree under the declared period", snapRel)
	}

	// explicit mapping wins (non-synonym headers) → 201.
	resp, body = uploadFeeFile(t, srv, "custom.csv", "资产编号,期间,应付\nX1,2024-05,88.00\n",
		"repair", "2024", "5", `{"columns":{"siteCode":"资产编号","amount":"应付","period":"期间"}}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("explicit mapping import: status = %d\nbody: %s", resp.StatusCode, body)
	}

	// row failures counted, import succeeds.
	resp, body = uploadFeeFile(t, srv, "dirty.csv", "站址编码,账期,电费(元)\nS1,2024-05,ok-but-not-a-number\nS2,2024-05,22.00\n",
		"electricity", "2024", "6", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("dirty import: status = %d, want 201 (row failures counted)\nbody: %s", resp.StatusCode, body)
	}
	var dirty feeImportSummary
	if err := json.Unmarshal(body, &dirty); err != nil {
		t.Fatal(err)
	}
	if dirty.RowsImported != 1 || dirty.RowsFailed != 1 || len(dirty.Errors) != 1 {
		t.Fatalf("dirty summary = %+v, want 1/1 with one row error", dirty)
	}

	// all rows failed → 422 fee_rows_failed.
	resp, body = uploadFeeFile(t, srv, "broken.csv", "站址编码,账期,电费(元)\nS1,2024-05,nope\n", "electricity", "2024", "7", "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("all-failed import: status = %d, want 422\nbody: %s", resp.StatusCode, body)
	}

	// parameter errors → 400.
	for _, tc := range []struct {
		name, feeType, year, month string
	}{
		{"bogus type", "groceries", "2024", "5"},
		{"bad year", "electricity", "1999", "5"},
		{"bad month", "electricity", "2024", "13"},
	} {
		resp, body = uploadFeeFile(t, srv, "x.csv", csv, tc.feeType, tc.year, tc.month, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400\nbody: %s", tc.name, resp.StatusCode, body)
		}
	}
	// unsupported extension → 422.
	resp, body = uploadFeeFile(t, srv, "x.txt", "whatever", "electricity", "2024", "5", "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("unsupported ext: status = %d, want 422\nbody: %s", resp.StatusCode, body)
	}
	// unmappable headers without a map → 422.
	resp, body = uploadFeeFile(t, srv, "x.csv", "A,B,C\n1,2,3\n", "electricity", "2024", "5", "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("unmappable: status = %d, want 422\nbody: %s", resp.StatusCode, body)
	}
}

func TestAPIFeesRollback(t *testing.T) {
	srv := newAPITestServer(t)

	csv := "站址编码,账期,电费(元)\nS1,2024-05,10.00\n"
	resp, body := uploadFeeFile(t, srv, "rb.csv", csv, "electricity", "2024", "5", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("import: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var sum feeImportSummary
	if err := json.Unmarshal(body, &sum); err != nil {
		t.Fatal(err)
	}

	// rollback → 200 rolled_back.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/fees/batches/"+sum.BatchID+"/rollback", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var rb struct {
		Status       string `json:"status"`
		SnapshotPath string `json:"snapshotPath"`
	}
	if err := json.Unmarshal(body, &rb); err != nil {
		t.Fatal(err)
	}
	if rb.Status != "rolled_back" || rb.SnapshotPath == "" {
		t.Fatalf("rollback body = %+v, want rolled_back with the kept snapshot", rb)
	}
	// re-rollback → 409.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/fees/batches/"+sum.BatchID+"/rollback", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-rollback: status = %d, want 409\nbody: %s", resp.StatusCode, body)
	}
	// unknown id → 404.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/fees/batches/fee_nope/rollback", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("rollback unknown: status = %d, want 404\nbody: %s", resp.StatusCode, body)
	}
	// the freed key allows a clean re-import → 201.
	resp, body = uploadFeeFile(t, srv, "rb.csv", csv, "electricity", "2024", "5", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("re-import after rollback: status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}
}

func TestAPIFeesQueryEngineState(t *testing.T) {
	srv := newAPITestServer(t)

	// Default build: 501 engine_not_built with the honest message; the
	// duckdb-tagged build answers the real preset (covered by the engine
	// tests in internal/fees — here only the CONTRACT shape is pinned).
	resp, body := doRequest(t, srv, http.MethodGet, "/api/fees/query/totals?feeType=electricity&year=2024&month=5", nil, "")
	var errOut struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errOut); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if resp.StatusCode == http.StatusNotImplemented {
		if errOut.Error.Code != "engine_not_built" || !strings.Contains(errOut.Error.Message, "duckdb") {
			t.Fatalf("engine_not_built envelope = %s", body)
		}
	} else if resp.StatusCode != http.StatusOK {
		t.Fatalf("totals on duckdb build: status = %d\nbody: %s", resp.StatusCode, body)
	}

	// parameter validation fires before the engine check → 400s.
	for _, q := range []string{
		"/api/fees/query/totals?feeType=electricity",
		"/api/fees/query/totals?feeType=electricity&year=1999",
		"/api/fees/query/totals?feeType=groceries&year=2024",
		"/api/fees/query/totals?feeType=electricity&year=2024&month=13",
		"/api/fees/query/totals?feeType=electricity&year=2024&limit=x",
	} {
		resp, body = doRequest(t, srv, http.MethodGet, q, nil, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400\nbody: %s", q, resp.StatusCode, body)
		}
	}
}

// TestAPIFeesRoutesInOpenAPI pins the anti-drift contract: the five fee
// routes exist in the generated OpenAPI document under the fees tag (the
// route table is the single source of truth — see routes.go).
func TestAPIFeesRoutesInOpenAPI(t *testing.T) {
	srv := newAPITestServer(t)
	resp, body := doRequest(t, srv, http.MethodGet, "/api/openapi.json", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi: status = %d", resp.StatusCode)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Tags []string `json:"tags"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode openapi: %v", err)
	}
	for _, path := range []string{
		"/api/fees/imports", "/api/fees/batches", "/api/fees/batches/{id}/rollback",
		"/api/fees/query/totals", "/api/fees/query/duplicates",
	} {
		ops, ok := doc.Paths[path]
		if !ok {
			t.Errorf("openapi missing path %s", path)
			continue
		}
		for method, op := range ops {
			if len(op.Tags) != 1 || op.Tags[0] != "fees" {
				t.Errorf("openapi %s %s tags = %v, want [fees]", method, path, op.Tags)
			}
		}
	}
}

// TestAPIFeesWritesRideTheAuthGate pins the write-gate integration: with
// AuthToken set, POST /api/fees/* without a bearer answers 401; the
// batch-list read stays open.
func TestAPIFeesWritesRideTheAuthGate(t *testing.T) {
	client, base := newAuthTestServer(t, "sec-token")

	// no token → 401 (the multipart POST rides the gate).
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "g.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("站址编码,账期,电费(元)\nS1,2024-05,1.00\n")); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"feeType": "electricity", "year": "2024", "month": "5"} {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/fees/imports", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ungated fee import: status = %d, want 401\nbody: %s", resp.StatusCode, body)
	}
	// the read route stays open.
	resp2, err := client.Get(base + "/api/fees/batches")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("read route must stay open: status = %d", resp2.StatusCode)
	}
}

// TestAPIFeesLinkJourney walks the link lifecycle over HTTP: import fee
// codes + resources (via the resource import API) → scan (auto exact +
// fuzzy proposal) → links list (resource join) → confirm (human) → 409
// re-confirm → the scan respects the human link.
func TestAPIFeesLinkJourney(t *testing.T) {
	srv := newAPITestServer(t)

	// seed one site-like resource through the real import pipeline: a
	// base_station whose fallback natural key is the station name.
	importBaseStationWorkbook(t, srv, "SITE99", "172.7.10.9/30")
	// fee codes: the exact hit + a near miss (edit distance 1 of 6 →
	// similarity 0.833, above the 0.8 proposal threshold).
	resp, body := uploadFeeFile(t, srv, "fees.csv", "站址编码,账期,电费(元)\nSITE99,2024-05,1.00\nSITE9X,2024-05,2.00\n", "electricity", "2024", "5", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("fee import: status = %d\nbody: %s", resp.StatusCode, body)
	}

	// scan: exact (SITE99) + fuzzy (SITE9X → SITE99).
	resp, body = doRequest(t, srv, http.MethodPost, "/api/fees/links/scan", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scan: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var scan struct {
		Codes         int `json:"codes"`
		ExactLinks    int `json:"exactLinks"`
		RuleLinks     int `json:"ruleLinks"`
		FuzzyProposed int `json:"fuzzyProposed"`
		Unlinked      int `json:"unlinked"`
	}
	if err := json.Unmarshal(body, &scan); err != nil {
		t.Fatal(err)
	}
	if scan.ExactLinks != 1 || scan.FuzzyProposed != 1 {
		t.Fatalf("scan = %+v, want exact 1 + fuzzy 1", scan)
	}

	// links list: the confirmed auto link + the pending proposal; the
	// resource join carries the display fields.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/links", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("links: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var links struct {
		Items []struct {
			ID           string  `json:"id"`
			FeeSiteCode  string  `json:"feeSiteCode"`
			Grade        string  `json:"grade"`
			Status       string  `json:"status"`
			Source       string  `json:"source"`
			ResourceName string  `json:"resourceName"`
			Similarity   float64 `json:"similarity"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(body, &links); err != nil {
		t.Fatal(err)
	}
	if links.Total != 2 {
		t.Fatalf("links total = %d, want 2", links.Total)
	}
	var proposalID string
	for _, l := range links.Items {
		if l.ResourceName == "" {
			t.Errorf("link %s: resource join empty (resourceName)", l.ID)
		}
		if l.Status == "pending" {
			proposalID = l.ID
			if l.Grade != "fuzzy" || l.Similarity < 0.8 {
				t.Errorf("pending link = %+v, want fuzzy ≥0.8", l)
			}
		}
	}
	if proposalID == "" {
		t.Fatal("no pending proposal found")
	}

	// confirm → 200, source flips human.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/fees/links/"+proposalID+"/confirm", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var confirmed struct {
		Status string `json:"status"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal(body, &confirmed); err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != "confirmed" || confirmed.Source != "human" {
		t.Fatalf("confirmed = %+v, want confirmed/human", confirmed)
	}
	// re-confirm → 409.
	resp, _ = doRequest(t, srv, http.MethodPost, "/api/fees/links/"+proposalID+"/confirm", nil, "")
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("re-confirm: status = %d, want 409", resp.StatusCode)
	}
	// unknown id → 404 (confirm + dismiss).
	for _, op := range []string{"confirm", "dismiss"} {
		resp, _ = doRequest(t, srv, http.MethodPost, "/api/fees/links/fsl_nope/"+op, nil, "")
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s unknown: status = %d, want 404", op, resp.StatusCode)
		}
	}
	// the scan keeps the human link.
	resp, body = doRequest(t, srv, http.MethodPost, "/api/fees/links/scan", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-scan: status = %d", resp.StatusCode)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/links?status=confirmed&source=human", nil, "")
	_ = resp
	// (source filter is not a query param — assert via the full list)
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/links?status=confirmed", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirmed list: status = %d", resp.StatusCode)
	}
	var confirmedList struct {
		Items []struct {
			ID     string `json:"id"`
			Source string `json:"source"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &confirmedList); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range confirmedList.Items {
		if l.ID == proposalID && l.Source == "human" {
			found = true
		}
	}
	if !found {
		t.Fatal("the human-confirmed link must survive the re-scan")
	}

	// filters: grade / bogus values.
	resp, _ = doRequest(t, srv, http.MethodGet, "/api/fees/links?grade=fuzzy&status=pending", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("grade filter: status = %d", resp.StatusCode)
	}
	for _, q := range []string{"status=bogus", "grade=bogus"} {
		resp, _ = doRequest(t, srv, http.MethodGet, "/api/fees/links?"+q, nil, "")
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("links %q: status = %d, want 400", q, resp.StatusCode)
		}
	}
}

// TestFeesRisksEndpoint pins the six-scenario surface's HTTP contract on
// default builds: the closed scenario/parameter validation (400s, firing
// before the engine gate) and the honest 501 engine_not_built across all
// six scenarios (#64 acceptance) — the duckdb-tagged fees package tests
// cover the real preset outputs.
func TestFeesRisksEndpoint(t *testing.T) {
	srv := newAPITestServer(t)

	// unknown scenario / bad parameters → 400 (validation precedes the engine gate)
	for _, tc := range []struct {
		url  string
		want string
	}{
		{"/api/fees/risks/bogus?feeType=electricity&year=2024", "场景"},
		{"/api/fees/risks/spike?feeType=bogus&year=2024", "feeType"},
		{"/api/fees/risks/spike?feeType=electricity&year=1899", "year 必须是 2000"},
		{"/api/fees/risks/spike?feeType=electricity&year=2024&ratio=abc", "ratio"},
		{"/api/fees/risks/spike?feeType=electricity&year=2024&z=-1", "z"},
		{"/api/fees/risks/spike?feeType=electricity&year=2024&limit=0", "limit"},
		{"/api/fees/risks/spike?feeType=electricity&year=2024&minSites=x", "minSites"},
		{"/api/fees/risks/spike?feeType=electricity&year=2024&diffCents=-5", "diffCents"},
	} {
		resp, body := doRequest(t, srv, http.MethodGet, tc.url, nil, "")
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), tc.want) {
			t.Errorf("GET %s = %d, want 400 mentioning %q\nbody: %s", tc.url, resp.StatusCode, tc.want, body)
		}
	}

	// valid parameters on the default build → 501 engine_not_built (all six).
	// On a -tags duckdb build the engine answers the real preset — empty
	// results against the empty test dir (the analysisEmpty guard), never
	// the refusal — so this half skips explicitly; the duckdb-tagged fees
	// tests own the engine-built behavior.
	if fees.Query(context.Background()).Available() {
		t.Skip("engine built into this binary (-tags duckdb) — the 501 refusal half belongs to the default build")
	}
	for _, sc := range fees.RiskScenarios() {
		resp, body := doRequest(t, srv, http.MethodGet,
			"/api/fees/risks/"+sc+"?feeType=electricity&year=2024", nil, "")
		var errOut struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &errOut); err != nil {
			t.Fatalf("scenario %s: decode: %v\n%s", sc, err, body)
		}
		if resp.StatusCode != http.StatusNotImplemented ||
			errOut.Error.Code != "engine_not_built" || !strings.Contains(errOut.Error.Message, "duckdb") {
			t.Errorf("scenario %s on default build = %d %+v, want 501 engine_not_built", sc, resp.StatusCode, errOut.Error)
		}
	}
}
