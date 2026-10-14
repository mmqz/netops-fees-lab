package api_test

import (
	"bytes"
	"context"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-lab/internal/app"
)

// newExportTestServer wires the full application like newAPITestServer but
// ALSO returns the App handle — the export round-trip assertions read the
// database directly (relation counts) through it.
func newExportTestServer(t *testing.T) (*httptest.Server, *app.App) {
	t.Helper()
	dir := testsupport.TempDir(t)
	a, err := app.New(app.Config{
		DBPath:  dir + "/netops.db",
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
	return srv, a
}

// importForExport uploads one workbook and confirms its suggested mapping,
// asserting only the row-phase outcome (imported/failed) — the set-level
// issue counts depend on the whole database and are deliberately not
// pinned here (TestAPIImportFlow owns them).
func importForExport(t *testing.T, srv *httptest.Server, file string, wantImported, wantFailed int) {
	t.Helper()
	resp, body := uploadFile(t, srv, file, readTestdata(t, file))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("upload %s: status = %d, want 202\nbody: %s", file, resp.StatusCode, body)
	}
	var a importAnalysis
	decodeJSON(t, "upload "+file, body, &a)
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/"+a.BatchID+"/mapping", a.SuggestedMapping, "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("mapping %s: status = %d, want 202\nbody: %s", file, resp.StatusCode, body)
	}
	var sum importSummary
	decodeJSON(t, "mapping "+file, body, &sum)
	if sum.Summary.RowsImported != wantImported || sum.Summary.RowsFailed != wantFailed {
		t.Fatalf("mapping %s: imported/failed = %d/%d, want %d/%d",
			file, sum.Summary.RowsImported, sum.Summary.RowsFailed, wantImported, wantFailed)
	}
}

// downloadExport GETs the export route and returns the response with its
// fully read body.
func downloadExport(t *testing.T, srv *httptest.Server, query string) (*http.Response, []byte) {
	t.Helper()
	return doRequest(t, srv, http.MethodGet, "/api/exports/resources"+query, nil, "")
}

// TestAPIExportResourcesFlow walks the export download over a seeded
// database (devices + circuits → relations a_end/z_end), the response
// contract (status, content type, content disposition, deterministic
// bytes), the types filter, the 400 of an unknown type, and the IN-PLACE
// round trip: re-uploading the exported workbook into the SAME database is
// a pure skip (0 imported / N skipped / 0 failed) with zero row-phase
// issues — the exported values re-hash to the same natural keys AND
// reproduce the stored attribute values exactly (a W005 attribute diff
// would otherwise fire).
func TestAPIExportResourcesFlow(t *testing.T) {
	srv, a := newExportTestServer(t)

	// Seed: 27 devices, then 21 circuits referencing them (42 relations).
	importForExport(t, srv, "devices.xlsx", 27, 3)
	importForExport(t, srv, "circuits.xlsx", 21, 4)

	// Download contract.
	resp, body := downloadExport(t, srv, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: status = %d, want 200\nbody: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Errorf("export: content-type = %q", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="netops-resources.xlsx"` {
		t.Errorf("export: content-disposition = %q", cd)
	}
	f, err := excelize.OpenReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("export body is not a readable xlsx: %v", err)
	}
	sheets := f.GetSheetList()
	if len(sheets) != 2 || sheets[0] != "网元设备" || sheets[1] != "传输电路" {
		t.Fatalf("export sheets = %v, want [网元设备 传输电路] (Specs order, empty types absent)", sheets)
	}
	deviceRows, err := f.GetRows("网元设备")
	if err != nil || len(deviceRows) != 28 {
		t.Fatalf("device sheet rows = %d (err %v), want 28 (header + 27)", len(deviceRows), err)
	}
	if deviceRows[0][0] != "设备编码" || deviceRows[0][1] != "设备名称" {
		t.Errorf("device header row = %v, want field Labels (设备编码, 设备名称, …)", deviceRows[0])
	}
	circuitRows, err := f.GetRows("传输电路")
	if err != nil || len(circuitRows) != 22 {
		t.Fatalf("circuit sheet rows = %d (err %v), want 22 (header + 21)", len(circuitRows), err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}

	// Determinism: the same database state exports byte-identical bytes.
	_, body2 := downloadExport(t, srv, "")
	if !bytes.Equal(body, body2) {
		t.Error("two exports of the same state must be byte-identical")
	}

	// types filter: device only.
	resp, body = downloadExport(t, srv, "?types=device")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export ?types=device: status = %d, want 200", resp.StatusCode)
	}
	f, err = excelize.OpenReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("open filtered export: %v", err)
	}
	if got := f.GetSheetList(); len(got) != 1 || got[0] != "网元设备" {
		t.Errorf("filtered export sheets = %v, want [网元设备]", got)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}

	// A filter selecting a type with no resources behaves like the empty
	// export: only the neutral placeholder sheet (no per-type sheet).
	resp, body = downloadExport(t, srv, "?types=ip_pool")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export ?types=ip_pool: status = %d, want 200", resp.StatusCode)
	}
	f, err = excelize.OpenReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("open ip_pool-filtered export: %v", err)
	}
	if got := f.GetSheetList(); len(got) != 1 || got[0] != "资源导出" {
		t.Errorf("ip_pool-filtered export sheets = %v, want the placeholder [资源导出]", got)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}

	// Unknown type value: 400 bad_request envelope naming the value.
	resp, body = downloadExport(t, srv, "?types=device,bogus")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("export ?types=bogus: status = %d, want 400", resp.StatusCode)
	}
	var er errorResp
	decodeJSON(t, "export bad types", body, &er)
	if er.Error.Code != "bad_request" || !bytes.Contains(body, []byte("bogus")) {
		t.Errorf("export bad types: envelope = %s, want bad_request naming bogus", body)
	}

	// In-place round trip: the export re-imported into the SAME database
	// must be a pure skip — 0 imported / 48 skipped / 0 failed.
	resp, body = uploadFile(t, srv, "netops-resources.xlsx", body2)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("re-upload export: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var a2 importAnalysis
	decodeJSON(t, "re-upload export", body, &a2)
	// Both sheets must be suggested for import with the right types.
	var sm suggestedMapping
	decodeJSON(t, "suggested mapping of export", a2.SuggestedMapping, &sm)
	if len(sm.Sheets) != 2 {
		t.Fatalf("export suggested mapping sheets = %d, want 2", len(sm.Sheets))
	}
	for i, want := range []string{"device", "circuit"} {
		if sm.Sheets[i].Action != "import" || sm.Sheets[i].ResourceType != want {
			t.Errorf("export suggested mapping sheet %d = %q/%q, want import/%s", i, sm.Sheets[i].Action, sm.Sheets[i].ResourceType, want)
		}
	}
	resp, body = doRequest(t, srv, http.MethodPost, "/api/imports/"+a2.BatchID+"/mapping", a2.SuggestedMapping, "application/json")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("re-import export: status = %d, want 202\nbody: %s", resp.StatusCode, body)
	}
	var sum importSummary
	decodeJSON(t, "re-import export", body, &sum)
	if sum.Summary.RowsImported != 0 || sum.Summary.RowsSkipped != 48 || sum.Summary.RowsFailed != 0 {
		t.Errorf("re-import export: imported/skipped/failed = %d/%d/%d, want 0/48/0 (pure skip)",
			sum.Summary.RowsImported, sum.Summary.RowsSkipped, sum.Summary.RowsFailed)
	}
	// Zero row-phase issues of the re-import batch: no E-rule failure, no
	// W005 attribute diff (the exported attribute values equal the stored
	// ones), no W004 (no base_station here).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/issues?batchId="+a2.BatchID+"&pageSize=100", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("issues of re-import batch: status = %d, want 200", resp.StatusCode)
	}
	var page issuesPage
	decodeJSON(t, "issues of re-import batch", body, &page)
	if page.Total != 0 {
		t.Errorf("re-import batch row-phase issues = %d, want 0\nitems: %s", page.Total, body)
	}

	// Relations are unchanged by the re-import (idempotency: no duplicate
	// a_end/z_end rows were derived from the skipped rows).
	if rels, err := a.DB.Relations.ListAll(context.Background()); err != nil {
		t.Fatalf("list relations: %v", err)
	} else if len(rels) != 42 {
		t.Errorf("relations after re-import = %d, want 42 (21 circuits × 2 ends)", len(rels))
	}
}

// TestAPIExportResourcesEmpty pins the empty-database export: 200 with a
// valid xlsx that carries ONLY the neutral placeholder sheet (the agreed
// empty-export shape — no per-type sheets, nothing importable).
func TestAPIExportResourcesEmpty(t *testing.T) {
	srv, _ := newExportTestServer(t)
	resp, body := downloadExport(t, srv, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty export: status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Errorf("empty export: content-type = %q", ct)
	}
	f, err := excelize.OpenReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("empty export body is not a readable xlsx: %v", err)
	}
	if got := f.GetSheetList(); len(got) != 1 || got[0] != "资源导出" {
		t.Errorf("empty export sheets = %v, want the single placeholder [资源导出]", got)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}
	// The empty export is byte-deterministic too.
	_, body2 := downloadExport(t, srv, "")
	if !bytes.Equal(body, body2) {
		t.Error("two empty exports must be byte-identical")
	}
}
