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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/app"
	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"github.com/xuri/excelize/v2"
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
	SHA256           string   `json:"sha256"`
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

// TestAPIFeesRoutesInOpenAPI pins the anti-drift contract: the fee
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
		"/api/fees/batches/{id}/evidence",
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
	// #90: the paged contract — total is the FULL count (not the page
	// size), page/pageSize echo back, and a small pageSize walks pages.
	var paged struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Total    int `json:"total"`
		Page     int `json:"page"`
		PageSize int `json:"pageSize"`
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/links?pageSize=1&page=2", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("links paged: status = %d\nbody: %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &paged); err != nil {
		t.Fatal(err)
	}
	if paged.Total != 2 || paged.Page != 2 || paged.PageSize != 1 || len(paged.Items) != 1 {
		t.Fatalf("paged links = %+v, want total 2 / page 2 / pageSize 1 / 1 item (the newest-first tail)", paged)
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

// TestFeesRisksEndpoint pins the risk-scenario surface's HTTP contract on
// default builds: the closed scenario/parameter validation (400s, firing
// before the engine gate) and the honest 501 engine_not_built across all
// scenarios (#64 acceptance + #114's idle-billing — the loop derives from
// the closed enum) — the duckdb-tagged fees package tests cover the real
// preset outputs.
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

	// valid parameters on the default build → 501 engine_not_built (every scenario).
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

// TestAPIFeesEvidenceReadback pins #86: the evidence chain's third tier.
// A clue / import error cites (sourceFile, sourceRow); GET
// /api/fees/batches/{id}/evidence?row=N resolves that anchor to the
// snapshot's original row — header + raw cells — for AI and human consumers
// who have no filesystem access. The snapshot survives rollback by design,
// so a rolled-back batch stays readable.
func TestAPIFeesEvidenceReadback(t *testing.T) {
	srv := newAPITestServer(t)

	csv := "站址编码,账期,电费(元)\n0230101ABCDEFG01,2024-05,1234.56\n0230101ABCDEFG02,2024-05,654.00\n"
	resp, body := uploadFeeFile(t, srv, "may.csv", csv, "electricity", "2024", "5", "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("import: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var sum feeImportSummary
	if err := json.Unmarshal(body, &sum); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	get := func(row string) (*http.Response, []byte) {
		return doRequest(t, srv, http.MethodGet, "/api/fees/batches/"+sum.BatchID+"/evidence?row="+row, nil, "")
	}

	// row=1 → the header row reads back as itself.
	resp, body = get("1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("evidence row=1: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var ev struct {
		BatchID      string   `json:"batchId"`
		SourceFile   string   `json:"sourceFile"`
		OriginalName string   `json:"originalName"`
		FeeType      string   `json:"feeType"`
		Year         int      `json:"year"`
		Month        int      `json:"month"`
		Row          int      `json:"row"`
		Header       []string `json:"header"`
		Cells        []string `json:"cells"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("decode evidence: %v", err)
	}
	if len(ev.Header) != 3 || ev.Header[0] != "站址编码" || ev.Header[2] != "电费(元)" {
		t.Fatalf("evidence header = %v", ev.Header)
	}
	if len(ev.Cells) != 3 || ev.Cells[0] != "站址编码" {
		t.Fatalf("row 1 must read back the header row, cells = %v", ev.Cells)
	}
	if ev.SourceFile == "" || !strings.Contains(ev.SourceFile, "may.csv") {
		t.Fatalf("sourceFile = %q, want <sha256>_may.csv (the anchor every clue carries)", ev.SourceFile)
	}
	if ev.OriginalName != "may.csv" || ev.FeeType != "electricity" || ev.Year != 2024 || ev.Month != 5 || ev.Row != 1 {
		t.Fatalf("evidence envelope = %+v", ev)
	}

	// row=2 → the first data row, header still attached (the two anchors a
	// clue actually carries: the row + the header to read it against).
	resp, body = get("2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("evidence row=2: status = %d\nbody: %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		t.Fatalf("decode evidence row 2: %v", err)
	}
	if len(ev.Cells) != 3 || ev.Cells[0] != "0230101ABCDEFG01" || ev.Cells[2] != "1234.56" {
		t.Fatalf("row 2 cells = %v, want the original data row verbatim", ev.Cells)
	}
	if ev.Header == nil || ev.Header[0] != "站址编码" {
		t.Fatalf("row 2 must carry the header, got %v", ev.Header)
	}
	if ev.Row != 2 {
		t.Fatalf("row echo = %d, want 2", ev.Row)
	}

	// beyond the file → 404 (the anchor is explicit, not invented).
	resp, body = get("99")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("evidence row=99: status = %d, want 404\nbody: %s", resp.StatusCode, body)
	}
	// garbage / non-positive row → 400.
	resp, body = get("abc")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("evidence row=abc: status = %d, want 400", resp.StatusCode)
	}
	resp, body = get("0")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("evidence row=0: status = %d, want 400", resp.StatusCode)
	}
	// missing row param → 400.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/batches/"+sum.BatchID+"/evidence", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("evidence without row: status = %d, want 400", resp.StatusCode)
	}
	// unknown batch → 404.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/batches/fee_nope/evidence?row=1", nil, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("evidence unknown batch: status = %d, want 404", resp.StatusCode)
	}

	// rollback keeps the evidence readable — the snapshot is the survivor
	// by design (#60), and the read-back must honor that (#86's acceptance).
	resp, body = doRequest(t, srv, http.MethodPost, "/api/fees/batches/"+sum.BatchID+"/rollback", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback: status = %d\nbody: %s", resp.StatusCode, body)
	}
	resp, body = get("2")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("evidence after rollback: status = %d, want 200 — the snapshot survives rollback\nbody: %s", resp.StatusCode, body)
	}
}

// TestAPIFeesImportErrorClassification pins #104: the import channel tells
// the operator's file apart from the server's fault. Refusals (unreadable
// shape / missing mapping / all rows failed) answer 422 with the honest
// text; SERVER faults (snapshot layer unwritable) answer 500 with the
// generic message — the raw error (with filesystem paths) goes to the log,
// never the wire, and the operator is no longer told to fix their file
// when the disk is the problem.
func TestAPIFeesImportErrorClassification(t *testing.T) {
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

	csv := "站址编码,账期,电费(元)\nS1,2024-08,100.00\n"

	// A completed import first: it creates the snapshot tree we make
	// unwritable below (the staging area stays writable so the failure is
	// the SNAPSHOT layer's, not the staging one).
	if resp, body := uploadFeeFile(t, srv, "base.csv", csv, "electricity", "2024", "8", ""); resp.StatusCode != http.StatusCreated {
		t.Fatalf("base import: status = %d\n%s", resp.StatusCode, body)
	}

	// A: refusal — headers that resolve neither siteCode nor amount (no
	// map) answer 422 fee_import_refused with the honest text.
	resp, body := uploadFeeFile(t, srv, "mystery.csv", "列一,列二,列三\n1,2,3\n", "electricity", "2024", "9", "")
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("refusal: status = %d, want 422\n%s", resp.StatusCode, body)
	}
	var env struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "fee_import_refused" {
		t.Errorf("refusal code = %q, want fee_import_refused", env.Error.Code)
	}
	if !strings.Contains(env.Error.Message, "siteCode") {
		t.Errorf("refusal message = %q, want the honest missing-mapping text", env.Error.Message)
	}

	// B: server fault — the snapshot tree goes unwritable; the file is
	// fine. The OLD channel answered 422 fee_import_failed (fix your file
	// — misleading); the classified channel answers 500 internal with the
	// generic message and the path-carrying detail in the log.
	// #126: the fault is injected by placing a regular FILE at the new
	// snapshot DIRECTORY's path — os.MkdirAll fails with ENOTDIR on every
	// platform (the pre-#126 os.Chmod 0o500 variant only produced a write
	// refusal on POSIX: Windows ACLs ignore the mode bits, the injection
	// never happened, and the product's 201 was CORRECT — the third
	// POSIX-assumption leak this repo has hit, see issue-126). The file
	// occupies the exact slot the month-07 snapshot must create, so the
	// failure stays at the SNAPSHOT layer (the staging area keeps
	// working — that separation is the classification this test pins).
	snapBlock := filepath.Join(dir, "fee-imports", "electricity", "2024", "07")
	if err := os.WriteFile(snapBlock, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("place snapshot-dir blocker: %v", err)
	}
	resp, body = uploadFeeFile(t, srv, "good-but-stranded.csv", strings.ReplaceAll(csv, "2024-08", "2024-07"), "electricity", "2024", "7", "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("server fault: status = %d, want 500\n%s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "internal" {
		t.Errorf("server fault code = %q, want internal", env.Error.Code)
	}
	if strings.Contains(env.Error.Message, dir) || strings.Contains(env.Error.Message, "fee-imports") {
		t.Errorf("server fault message leaks the filesystem path: %q", env.Error.Message)
	}
	if !strings.Contains(env.Error.Message, "服务器内部错误") {
		t.Errorf("server fault message = %q, want the generic internal text", env.Error.Message)
	}
}

// buildFeeMultipart serializes one fee import multipart body (the same
// shape uploadFeeFile posts) and returns its content type — a body built
// once can be posted by several goroutines, each with its own reader.
func buildFeeMultipart(t *testing.T, filename, content, feeType, year, month string) (string, []byte) {
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
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return mw.FormDataContentType(), buf.Bytes()
}

// hashFile returns the hex SHA-256 of a file on disk.
func hashFile(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestAPIFeesConcurrentSameUpload pins #106-A: two CONCURRENT POSTs of the
// same content (and name) must leave BOTH content-addressed copies honest
// — the staged upload and the immutable snapshot hash to exactly the
// SHA256 the batch records. Pre-#106 the staging write was an in-place
// O_TRUNC: one request truncated and rewrote the file while the other was
// hashing/reading it, so the loser could record a SHA256 that does not
// match the bytes it actually imported — the evidence hash lied. The write
// now lands under a per-request temp name and renames into place only once
// complete; the idempotent-duplicate semantics still admit exactly one
// completed batch (the MarkCompleted race loser answers 200
// informational).
func TestAPIFeesConcurrentSameUpload(t *testing.T) {
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

	for round := 0; round < 6; round++ {
		// ~48KB: large enough that the snapshot layer's io.Copy crosses
		// several 32KB chunks — the old shared-temp write interleaved at
		// chunk granularity, so a bigger file gives the (now fixed) race a
		// realistic surface if it ever regresses.
		var sb strings.Builder
		fmt.Fprintf(&sb, "站址编码,账期,电费(元)\n")
		for row := 0; row < 400; row++ {
			fmt.Fprintf(&sb, "R%d-%03d,2024-05,%d.00\n", round, row, 100+row%50)
		}
		content := sb.String()
		sum := sha256.Sum256([]byte(content))
		contentSHA := hex.EncodeToString(sum[:])
		contentType, body := buildFeeMultipart(t, "race.csv", content, "electricity", "2024", "5")

		// Two goroutines post the SAME bytes at the same instant; only
		// Errorf (goroutine-safe) fires inside them, FailNow stays with
		// the test goroutine.
		start := make(chan struct{})
		results := make([]struct {
			status int
			body   []byte
		}, 2)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				req, rerr := http.NewRequest(http.MethodPost, srv.URL+"/api/fees/imports", bytes.NewReader(body))
				if rerr != nil {
					t.Errorf("round %d: build request: %v", round, rerr)
					return
				}
				req.Header.Set("Content-Type", contentType)
				resp, rerr := http.DefaultClient.Do(req)
				if rerr != nil {
					t.Errorf("round %d: post: %v", round, rerr)
					return
				}
				defer func() { _ = resp.Body.Close() }()
				b, rerr := io.ReadAll(resp.Body)
				if rerr != nil {
					t.Errorf("round %d: read body: %v", round, rerr)
					return
				}
				results[i].status = resp.StatusCode
				results[i].body = b
			}(i)
		}
		close(start)
		wg.Wait()

		codes := map[int]int{}
		var winnerBatch string
		for _, r := range results {
			if r.status != http.StatusCreated && r.status != http.StatusOK {
				t.Fatalf("round %d: concurrent import status = %d (want 201/200)\nbody: %s", round, r.status, r.body)
			}
			codes[r.status]++
			var sum feeImportSummary
			if err := json.Unmarshal(r.body, &sum); err != nil {
				t.Fatalf("round %d: decode summary: %v\n%s", round, err, r.body)
			}
			if sum.SHA256 != contentSHA {
				t.Errorf("round %d: recorded sha256 = %s, want the content hash %s — the evidence hash must never lie", round, sum.SHA256, contentSHA)
			}
			if r.status == http.StatusCreated {
				winnerBatch = sum.BatchID
				if sum.RowsImported != 400 || sum.Status != "completed" {
					t.Errorf("round %d: fresh import = %+v, want completed with 400 rows", round, sum)
				}
			}
		}
		if codes[http.StatusCreated] != 1 || codes[http.StatusOK] != 1 {
			t.Fatalf("round %d: status codes = %v, want exactly one 201 and one informational 200 duplicate", round, codes)
		}

		// Both content-addressed copies are byte-honest: the staged upload
		// under uploads/fees and the immutable snapshot recorded by the
		// completed batch hash to the recorded SHA256.
		staged := filepath.Join(dir, "uploads", "fees", contentSHA+"_race.csv")
		if got := hashFile(t, staged); got != contentSHA {
			t.Errorf("round %d: staged upload hashes to %s, want %s", round, got, contentSHA)
		}
		resp, body := doRequest(t, srv, http.MethodGet, "/api/fees/batches?status=completed", nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("round %d: batches list: status = %d\n%s", round, resp.StatusCode, body)
		}
		var list struct {
			Items []struct {
				ID           string `json:"id"`
				SnapshotPath string `json:"snapshotPath"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			t.Fatalf("round %d: decode batches: %v", round, err)
		}
		var snapPath string
		for _, it := range list.Items {
			if it.ID == winnerBatch {
				snapPath = it.SnapshotPath
			}
		}
		if snapPath == "" {
			t.Fatalf("round %d: completed batch %s not in the list", round, winnerBatch)
		}
		snapshot := filepath.Join(dir, filepath.FromSlash(snapPath))
		if got := hashFile(t, snapshot); got != contentSHA {
			t.Errorf("round %d: snapshot %s hashes to %s, want %s (the copy the evidence anchors point at)", round, snapshot, got, contentSHA)
		}
	}

	// No staging temp leaks: every rename consumed its temp file.
	feesDir := filepath.Join(dir, "uploads", "fees")
	entries, err := os.ReadDir(feesDir)
	if err != nil {
		t.Fatalf("read fees staging dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("staging temp %s leaked after the concurrent uploads", e.Name())
		}
	}
}

// TestAPIFeesClueDecisionEndpoints pins #119's human channel on the HTTP
// face: the POST validates the closed sets (scenario/feeType/status) and
// the period bounds BEFORE any write, the UPSERT flip keeps one ledger
// row, and the GET ledger answers newest-first with the filters — all on
// the DEFAULT build (decisions persist without the analysis engine; the
// suppression applies the next time an engine-built binary runs).
func TestAPIFeesClueDecisionEndpoints(t *testing.T) {
	srv := newAPITestServer(t)

	post := func(body string) (*http.Response, []byte) {
		return doRequest(t, srv, http.MethodPost, "/api/fees/risk-clue-decisions", []byte(body), "application/json")
	}

	// the closed sets refuse first (400, no write).
	for _, bad := range []string{
		`{"scenario":"bogus","feeType":"electricity","siteCode":"S-1","year":2024,"month":5,"status":"verified"}`,
		`{"scenario":"spike","feeType":"bogus","siteCode":"S-1","year":2024,"month":5,"status":"verified"}`,
		`{"scenario":"spike","feeType":"electricity","siteCode":"S-1","year":2024,"month":5,"status":"bogus"}`,
		`{"scenario":"spike","feeType":"electricity","siteCode":"","year":2024,"month":5,"status":"verified"}`,
		`{"scenario":"spike","feeType":"electricity","siteCode":"S-1","year":1999,"month":5,"status":"verified"}`,
		`{"scenario":"spike","feeType":"electricity","siteCode":"S-1","year":2024,"month":13,"status":"verified"}`,
		`not json at all`,
	} {
		resp, body := post(bad)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("POST %q: status = %d, want 400\nbody: %s", bad, resp.StatusCode, body)
		}
	}
	// nothing was written by the refused requests.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/fees/risk-clue-decisions", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ledger: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var empty struct {
		Items []any `json:"items"`
		Total int   `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Total != 0 || len(empty.Items) != 0 {
		t.Fatalf("ledger after refusals = %d / %d items, want 0/0", empty.Total, len(empty.Items))
	}

	// the first decision lands (200, full item shape).
	resp, body = post(`{"scenario":"spike","feeType":"electricity","siteCode":"S-1","year":2024,"month":5,"status":"dismissed","note":"合同+账单双台账，双份合法"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST dismiss: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var item struct {
		ID         string `json:"id"`
		Scenario   string `json:"scenario"`
		ScenarioCn string `json:"scenarioCn"`
		FeeType    string `json:"feeType"`
		SiteCode   string `json:"siteCode"`
		Status     string `json:"status"`
		Note       string `json:"note"`
		DecidedAt  string `json:"decidedAt"`
	}
	if err := json.Unmarshal(body, &item); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(item.ID, "rcd_") || item.Scenario != "spike" || item.ScenarioCn != "电费环比异常上涨" ||
		item.FeeType != "electricity" || item.SiteCode != "S-1" || item.Status != "dismissed" ||
		item.Note != "合同+账单双台账，双份合法" || item.DecidedAt == "" {
		t.Fatalf("item = %+v", item)
	}

	// the flip: the SAME identity re-decided as verified keeps ONE row.
	resp, body = post(`{"scenario":"spike","feeType":"electricity","siteCode":"S-1","year":2024,"month":5,"status":"verified"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST flip: status = %d\nbody: %s", resp.StatusCode, body)
	}
	var flipped struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &flipped); err != nil || flipped.Status != "verified" {
		t.Fatalf("flip = %s (%v)", body, err)
	}
	// a second, distinct decision.
	post(`{"scenario":"missing-billing","feeType":"tower_lease","siteCode":"S-2","year":2024,"month":0,"status":"dismissed"}`)

	// the ledger: two rows, newest first; the filters narrow.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/risk-clue-decisions", nil, "")
	var ledger struct {
		Items []struct {
			SiteCode string `json:"siteCode"`
			Status   string `json:"status"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.Total != 2 || len(ledger.Items) != 2 {
		t.Fatalf("ledger = %+v, want 2 rows", ledger)
	}
	// decided_at is RFC3339 SECOND-granular: two same-second decisions tie
	// and fall to the random-uuid id DESC tiebreak — the order BETWEEN
	// same-second rows is not a contract (the repo test pins the ordered
	// part across distinct seconds). Here: the SET is the contract.
	gotSet := map[string]string{}
	for _, it := range ledger.Items {
		gotSet[it.SiteCode] = it.Status
	}
	if gotSet["S-1"] != "verified" || gotSet["S-2"] != "dismissed" {
		t.Fatalf("ledger set = %v, want S-1 verified + S-2 dismissed", gotSet)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/risk-clue-decisions?status=verified", nil, "")
	if err := json.Unmarshal([]byte(body), &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.Total != 1 || ledger.Items[0].SiteCode != "S-1" {
		t.Fatalf("status filter = %+v, want only S-1 verified", ledger)
	}
	// bogus filters refuse (the closed-set validation runs on GET too).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/risk-clue-decisions?scenario=bogus", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("GET bogus scenario: status = %d, want 400\nbody: %s", resp.StatusCode, body)
	}
	// the risks route's includeDecided parses honestly.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/risks/spike?feeType=electricity&year=2024&includeDecided=bogus", nil, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("risks includeDecided=bogus: status = %d, want 400\nbody: %s", resp.StatusCode, body)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/risks/spike?feeType=electricity&year=2024&includeDecided=1", nil, "")
	// The 501 expectation is the default build's contract; a -tags duckdb
	// build's real engine answers the (empty-dir) preset — the same split
	// the scenarios half above makes explicit (found by running the api
	// package under -tags duckdb: this assertion was the only
	// tag-unaware one).
	if !fees.Query(context.Background()).Available() {
		if resp.StatusCode != http.StatusNotImplemented {
			t.Errorf("risks includeDecided=1 (default build): status = %d, want 501 engine_not_built\nbody: %s", resp.StatusCode, body)
		}
	} else if resp.StatusCode != http.StatusOK {
		t.Errorf("risks includeDecided=1 (duckdb build): status = %d, want 200 (engine built, empty dir answers the empty preset)\nbody: %s", resp.StatusCode, body)
	}
}

// uploadFeeSheetsFile posts the multi-sheet import multipart (file + year
// + month — NO feeType) to /api/fees/imports/sheets (#125).
func uploadFeeSheetsFile(t *testing.T, srv *httptest.Server, filename string, content []byte, year, month string, extra map[string]string) (*http.Response, []byte) {
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
	for k, v := range map[string]string{"year": year, "month": month} {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	for k, v := range extra {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/fees/imports/sheets", &buf)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post sheets import: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	_ = resp.Body.Close()
	return resp, body
}

// buildLedgerWorkbook builds the #125 acceptance workbook in memory:
// sheets 电费(2 rows) / 铁塔租费(1 row: 5000.00) / 修理费(1 row) / 说明(cover).
func buildLedgerWorkbook(t *testing.T) []byte {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName(f.GetSheetList()[0], "电费"); err != nil {
		t.Fatal(err)
	}
	for _, spec := range []struct {
		sheet string
		rows  [][]string
	}{
		{"电费", [][]string{{"站址编码", "站址名称", "电费(元)", "账期"}, {"S1", "一站", "12345.67", "2024-05"}, {"S2", "二站", "678.90", "2024-05"}}},
		{"铁塔租费", [][]string{{"站址编码", "站址名称", "塔租费(元)", "账期"}, {"S1", "一站", "5000.00", "2024-05"}}},
		{"修理费", [][]string{{"站址编码", "站址名称", "修理费(元)", "账期", "工单号"}, {"S2", "二站", "999.99", "2024-05", "GD-001"}}},
		{"说明", [][]string{{"本表由财务部编制"}}},
	} {
		if spec.sheet != "电费" {
			if _, err := f.NewSheet(spec.sheet); err != nil {
				t.Fatal(err)
			}
		}
		for i, row := range spec.rows {
			for j, cell := range row {
				ref, err := excelize.CoordinatesToCellName(j+1, i+1)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.SetCellValue(spec.sheet, ref, cell); err != nil {
					t.Fatalf("set %s!%s: %v", spec.sheet, ref, err)
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		t.Fatalf("write workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// sheetsImportSummary mirrors the POST /api/fees/imports/sheets body.
type sheetsImportSummary struct {
	WorkbookName string `json:"workbookName"`
	Status       string `json:"status"`
	Batches      []struct {
		Sheet            string `json:"sheet"`
		FeeType          string `json:"feeType"`
		BatchID          string `json:"batchId"`
		Status           string `json:"status"`
		RowsImported     int    `json:"rowsImported"`
		SkippedDuplicate bool   `json:"skippedDuplicate"`
		ExistingBatch    string `json:"existingBatch"`
	} `json:"batches"`
	SkippedSheets []string `json:"skippedSheets"`
}

// TestAPIFeesImportSheets pins the #125 multi-sheet HTTP contract: 201
// with one batch per fee-type sheet (each type from its own evidence),
// the cosmetic sheet named, 200 with per-sheet skippedDuplicate on the
// idempotent re-import, 400 when a feeType misunderstands the channel,
// and 422 for the CSV that has no sheet names to classify from.
func TestAPIFeesImportSheets(t *testing.T) {
	srv := newAPITestServer(t)
	ledger := buildLedgerWorkbook(t)

	// fresh import → 201 + three batches in workbook order.
	resp, body := uploadFeeSheetsFile(t, srv, "2024年05月费用.xlsx", ledger, "2024", "5", nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sheets import: status = %d, want 201\nbody: %s", resp.StatusCode, body)
	}
	var sum sheetsImportSummary
	if err := json.Unmarshal(body, &sum); err != nil {
		t.Fatalf("decode summary: %v\n%s", err, body)
	}
	if sum.Status != "completed" || len(sum.Batches) != 3 {
		t.Fatalf("summary = %+v, want completed with 3 batches", sum)
	}
	for i, want := range []struct {
		sheet, typ string
		rows       int
	}{{"电费", "electricity", 2}, {"铁塔租费", "tower_lease", 1}, {"修理费", "repair", 1}} {
		b := sum.Batches[i]
		if b.Sheet != want.sheet || b.FeeType != want.typ || b.RowsImported != want.rows || b.Status != "completed" {
			t.Fatalf("batches[%d] = %+v, want %s/%s rows %d", i, b, want.sheet, want.typ, want.rows)
		}
	}
	if len(sum.SkippedSheets) != 1 || sum.SkippedSheets[0] != "说明" {
		t.Fatalf("skippedSheets = %v, want [说明]", sum.SkippedSheets)
	}

	// the batches surface in the shared batches list (query/rollback face).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/fees/batches?status=completed", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batches list: status = %d\nbody: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), sum.Batches[1].BatchID) {
		t.Fatalf("batches list must carry the tower batch %s", sum.Batches[1].BatchID)
	}

	// idempotent re-import → 200, every sheet a duplicate skip.
	resp, body = uploadFeeSheetsFile(t, srv, "2024年05月费用.xlsx", ledger, "2024", "5", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("duplicate sheets import: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var dup sheetsImportSummary
	if err := json.Unmarshal(body, &dup); err != nil {
		t.Fatalf("decode duplicate: %v", err)
	}
	for i := range dup.Batches {
		if !dup.Batches[i].SkippedDuplicate || dup.Batches[i].ExistingBatch != sum.Batches[i].BatchID {
			t.Fatalf("duplicate batches[%d] = %+v, want skip → %s", i, dup.Batches[i], sum.Batches[i].BatchID)
		}
	}

	// a feeType field misunderstands the channel → 400 with the pointer.
	resp, body = uploadFeeSheetsFile(t, srv, "2024年05月费用.xlsx", ledger, "2024", "5", map[string]string{"feeType": "electricity"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("sheets import with feeType: status = %d, want 400\nbody: %s", resp.StatusCode, body)
	}

	// CSV has no sheet names to classify from → 422 fee_import_refused.
	csv := "站址编码,账期,电费(元)\nS1,2024-05,10.00\n"
	resp, body = uploadFeeSheetsFile(t, srv, "may.csv", []byte(csv), "2024", "5", nil)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("CSV sheets import: status = %d, want 422\nbody: %s", resp.StatusCode, body)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != "fee_import_refused" {
		t.Fatalf("CSV refusal code = %q, want fee_import_refused", env.Error.Code)
	}
}
