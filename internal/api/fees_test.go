package api_test

// The HTTP surface of the fee analysis layer (#58/#60/#61): the import
// journey (201 fresh / 200 duplicate / 422 all-rows-failed / 400 params),
// the batch list, the rollback (Parquet gone + snapshot kept + 409
// re-rollback), and the query presets' engine-state contract (501
// engine_not_built on default builds — the duckdb-tagged build covers
// the real engine path in internal/fees).

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
