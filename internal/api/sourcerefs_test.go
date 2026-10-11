package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// importDeviceWorkbookBatch uploads one device workbook and confirms the
// device mapping, returning the executed batch id (the HTTP journey of the
// W7-D source-ref versioning test).
func importDeviceWorkbookBatch(t *testing.T, srv *httptest.Server, ip string) string {
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
	if sum.Summary.RowsImported+sum.Summary.RowsSkipped != 1 || sum.Summary.RowsFailed != 0 {
		t.Fatalf("import summary = %+v, want 1 imported-or-skipped / 0 failed", sum.Summary)
	}
	return a.BatchID
}

// TestAPIResourceDetailSourceRefVersions walks the W7-D read side over the
// full HTTP stack: three imports of the same device (the 2nd and 3rd are
// diff/pure skips) → GET /api/resources/{id} returns sourceRefs in VERSION
// order (creation batch first, then each re-import), each version carrying
// the raw row as its workbook said it; the rollback deleted-counts
// (DELETE /api/batches/{id}, backed by SourceRefRepo.CountByBatch) honestly
// count the skip-appended versions, and rolling a batch back removes its
// version (refs back to 2, then to 1 when both re-imports are gone).
func TestAPIResourceDetailSourceRefVersions(t *testing.T) {
	srv := newAPITestServer(t)

	b1 := importDeviceWorkbookBatch(t, srv, "10.0.0.1")
	b2 := importDeviceWorkbookBatch(t, srv, "10.0.0.9") // diff-skip
	b3 := importDeviceWorkbookBatch(t, srv, "10.0.0.9") // pure skip

	// Find DEV-001's id via the search endpoint.
	resp, body := doRequest(t, srv, http.MethodGet, "/api/resources?q=DEV-001", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resources search: status = %d, want 200", resp.StatusCode)
	}
	var page resourcesPage
	decodeJSON(t, "resources search", body, &page)
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("search DEV-001: total = %d, want 1", page.Total)
	}
	devID := page.Items[0].ID

	// Detail: 3 source refs in version order.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources/"+devID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource detail: status = %d, want 200", resp.StatusCode)
	}
	var det resourceDetail
	decodeJSON(t, "resource detail", body, &det)
	if len(det.SourceRefs) != 3 {
		t.Fatalf("resource detail: %d sourceRefs, want 3 (one version per import)", len(det.SourceRefs))
	}
	if det.FirstBatchID != b1 {
		t.Errorf("firstBatchId = %q, want %q (creation batch unchanged)", det.FirstBatchID, b1)
	}
	wantBatches := []string{b1, b2, b3}
	for i, ref := range det.SourceRefs {
		if ref.BatchID != wantBatches[i] {
			t.Errorf("sourceRefs[%d].batchId = %q, want %q (version order = import order)", i, ref.BatchID, wantBatches[i])
		}
		if ref.Workbook != "devices.xlsx" || ref.Sheet != "设备清单" || ref.RowNumber != 2 {
			t.Errorf("sourceRefs[%d] = %s/%s/row %d, want devices.xlsx/设备清单/row 2", i, ref.Workbook, ref.Sheet, ref.RowNumber)
		}
	}
	// The version rows record what each workbook said — including the 2nd
	// import's 10.0.0.9 (the diff proposal stayed pending, the provenance
	// did not wait for the human decision).
	if det.SourceRefs[0].Raw["管理IP"] != "10.0.0.1" || det.SourceRefs[1].Raw["管理IP"] != "10.0.0.9" ||
		det.SourceRefs[2].Raw["管理IP"] != "10.0.0.9" {
		t.Errorf("version raws = %q / %q / %q, want 10.0.0.1 then 10.0.0.9 / 10.0.0.9",
			det.SourceRefs[0].Raw["管理IP"], det.SourceRefs[1].Raw["管理IP"], det.SourceRefs[2].Raw["管理IP"])
	}
	// The resource attribute itself is untouched by the skips.
	if det.Attributes["mgmt_ip"] != "10.0.0.1" {
		t.Errorf("mgmt_ip = %q, want 10.0.0.1 (skip never writes the resource)", det.Attributes["mgmt_ip"])
	}

	// Batch rollback counts honestly count the refs the batch recorded —
	// one skip-appended version each (SourceRefRepo.CountByBatch over the
	// DELETE response's deleted.sourceRefs).
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/batches/"+b3, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback batch 3: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	var rb rollbackResp
	decodeJSON(t, "rollback", body, &rb)
	if rb.Deleted.SourceRefs != 1 {
		t.Errorf("rollback deleted sourceRefs = %d, want 1 (the version row of batch 3)", rb.Deleted.SourceRefs)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources/"+devID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource detail after rollback: status = %d, want 200", resp.StatusCode)
	}
	decodeJSON(t, "resource detail after rollback", body, &det)
	if len(det.SourceRefs) != 2 || det.SourceRefs[0].BatchID != b1 || det.SourceRefs[1].BatchID != b2 {
		t.Fatalf("sourceRefs after rollback = %+v, want [b1, b2] (the version died with its batch)", det.SourceRefs)
	}

	// Rolling back the second batch too leaves exactly the creation ref.
	resp, body = doRequest(t, srv, http.MethodDelete, "/api/batches/"+b2, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rollback batch 2: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	decodeJSON(t, "rollback 2", body, &rb)
	if rb.Deleted.SourceRefs != 1 {
		t.Errorf("rollback 2 deleted sourceRefs = %d, want 1", rb.Deleted.SourceRefs)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/resources/"+devID, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resource detail after both rollbacks: status = %d, want 200", resp.StatusCode)
	}
	decodeJSON(t, "resource detail after both rollbacks", body, &det)
	if len(det.SourceRefs) != 1 || det.SourceRefs[0].BatchID != b1 {
		t.Fatalf("sourceRefs after both rollbacks = %+v, want exactly the creation ref of b1", det.SourceRefs)
	}
}
