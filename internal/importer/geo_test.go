package importer

// Geo pipeline tests: the combined 经纬度 column split (both component
// orders, all separators), direct 纬度/经度 columns, the explicit-columns-win
// rule, and the E004 rejections of invalid coordinates. Each test builds a
// minimal device workbook with excelize (the suggested mapping picks up the
// geo headers via the synonym ladder) and runs the real Analyze + Execute
// pipeline, asserting the resulting resource ATTRIBUTES — the storage the
// workspace materialization later reads geo_lat/geo_lng from.

import (
	"context"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/mapping"
	"github.com/xuri/excelize/v2"
)

// writeGeoWorkbook writes one single-sheet workbook (sheet name, headers,
// rows) into the test's temp dir and returns its path. Cells are plain
// strings, header row 1, data from row 2 — the same shape the committed
// testdata workbooks use.
func writeGeoWorkbook(t *testing.T, name, sheet string, headers []string, rows [][]string) string {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		t.Fatalf("SetSheetName(%q): %v", sheet, err)
	}
	for c, h := range headers {
		cell, err := excelize.CoordinatesToCellName(c+1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStr(sheet, cell, h); err != nil {
			t.Fatalf("SetCellStr(%s): %v", cell, err)
		}
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+2)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellStr(sheet, cell, v); err != nil {
				t.Fatalf("SetCellStr(%s): %v", cell, err)
			}
		}
	}
	path := filepath.Join(testsupport.TempDir(t), name)
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs(%s): %v", path, err)
	}
	return path
}

// geoDeviceHeaders is the device sheet header of the geo test workbooks.
var geoDeviceHeaders = []string{"设备编码", "设备名称", "状态", "经纬度"}

// importGeoWorkbook analyzes + executes one geo workbook with its suggested
// mapping and returns the summary.
func importGeoWorkbook(t *testing.T, svc *Service, path string) Summary {
	t.Helper()
	ctx := context.Background()
	a, err := svc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze(%s): %v", path, err)
	}
	// The suggested mapping must map the geo column — otherwise the test
	// would silently import without geo and assert nothing.
	sm := a.SuggestedMapping.Sheets[0]
	if _, ok := sm.ColumnMapping["geo"]; !ok {
		t.Fatalf("suggested mapping does not map the 经纬度 column: %+v", sm.ColumnMapping)
	}
	sum, err := svc.Execute(ctx, a.BatchID, a.SuggestedMapping)
	if err != nil {
		t.Fatalf("Execute(%s): %v", path, err)
	}
	return sum
}

// TestImportGeoCombinedSplit proves the combined 经纬度 column split on the
// real pipeline: rows with (经度,纬度) order, (纬度,经度) order and the
// Chinese-comma separator all land as geo_lat/geo_lng attributes (plain
// decimal strings), while the raw combined value stays traceable in the
// attributes.
func TestImportGeoCombinedSplit(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	rows := [][]string{
		{"DEV-A", "站点A", "在网", "126.636,45.773"}, // (经度,纬度) — Gaode order
		{"DEV-B", "站点B", "在网", "45.748，126.638"}, // (纬度,经度) + Chinese comma
		{"DEV-C", "站点C", "在网", "124.114 50.424"}, // (经度,纬度) + space separator
	}
	path := writeGeoWorkbook(t, "geo-combined.xlsx", "设备清单", geoDeviceHeaders, rows)
	sum := importGeoWorkbook(t, svc, path)
	if sum.RowsImported != 3 || sum.RowsFailed != 0 {
		t.Fatalf("summary = %+v, want 3 imported / 0 failed", sum)
	}
	want := map[string][2]string{
		"DEV-A": {"45.773", "126.636"},
		"DEV-B": {"45.748", "126.638"},
		"DEV-C": {"50.424", "124.114"},
	}
	for code, w := range want {
		res, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, code)
		if err != nil {
			t.Fatalf("GetByTypeKey(%s): %v", code, err)
		}
		if gotLat := res.Attributes["geo_lat"]; gotLat != w[0] {
			t.Errorf("%s geo_lat = %q, want %q (attributes %+v)", code, gotLat, w[0], res.Attributes)
		}
		if gotLng := res.Attributes["geo_lng"]; gotLng != w[1] {
			t.Errorf("%s geo_lng = %q, want %q (attributes %+v)", code, gotLng, w[1], res.Attributes)
		}
		if raw := res.Attributes["geo"]; raw == "" {
			t.Errorf("%s raw geo attribute missing (traceability)", code)
		}
	}
}

// TestImportGeoDirectColumns proves the split columns work without any
// combined column: 纬度/经度 map straight into the attributes, and an
// out-of-range latitude fails the row with E004 (kind-level float range
// check, exactly like vlan/bandwidth ranges).
func TestImportGeoDirectColumns(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	headers := []string{"设备编码", "设备名称", "状态", "纬度", "经度"}
	rows := [][]string{
		{"DEV-A", "站点A", "在网", "45.773", "126.636"},
		{"DEV-BAD", "站点B", "在网", "91.5", "126.6"}, // lat out of range → E004
	}
	path := writeGeoWorkbook(t, "geo-direct.xlsx", "设备清单", headers, rows)
	ctx2 := context.Background()
	a, err := svc.Analyze(ctx2, path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if _, ok := a.SuggestedMapping.Sheets[0].ColumnMapping["geo_lat"]; !ok {
		t.Fatalf("suggested mapping does not map the 纬度 column: %+v", a.SuggestedMapping.Sheets[0].ColumnMapping)
	}
	sum, err := svc.Execute(ctx, a.BatchID, a.SuggestedMapping)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if sum.RowsImported != 1 || sum.RowsFailed != 1 {
		t.Fatalf("summary = %+v, want 1 imported / 1 failed", sum)
	}
	res, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-A")
	if err != nil {
		t.Fatalf("GetByTypeKey(DEV-A): %v", err)
	}
	if res.Attributes["geo_lat"] != "45.773" || res.Attributes["geo_lng"] != "126.636" {
		t.Errorf("DEV-A attributes = %+v, want geo_lat 45.773 / geo_lng 126.636", res.Attributes)
	}
	if raw := res.Attributes["geo"]; raw != "" {
		t.Errorf("DEV-A must carry no raw geo attribute when no combined column is mapped, got %q", raw)
	}
	// The out-of-range row failed with E004 and a Chinese reason.
	issues := batchIssues(t, db, sum.BatchID)
	iss := findIssue(t, issues, domain.RuleInvalidFormat)
	if !strings.Contains(iss.Message, "geo_lat") || !strings.Contains(iss.Message, "纬度") {
		t.Errorf("E004 issue message = %q, want it to name geo_lat and the 纬度 range", iss.Message)
	}
	if iss.Evidence["field"] != "geo_lat" || iss.Evidence["reason"] == "" {
		t.Errorf("E004 evidence = %+v, want field geo_lat with a reason", iss.Evidence)
	}
}

// TestImportGeoExplicitColumnsWin proves the precedence rule: when 纬度/经度
// columns are BOTH mapped and non-empty, the combined 经纬度 column is kept
// as a raw attribute but does NOT overwrite the explicit split values.
func TestImportGeoExplicitColumnsWin(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	headers := []string{"设备编码", "设备名称", "状态", "纬度", "经度", "经纬度"}
	rows := [][]string{
		// Explicit columns say 哈尔滨枢纽; the combined value says 大庆 —
		// the explicit pair must win.
		{"DEV-A", "站点A", "在网", "45.773", "126.636", "125.103,46.59"},
	}
	path := writeGeoWorkbook(t, "geo-explicit.xlsx", "设备清单", headers, rows)
	a, err := svc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	sum, err := svc.Execute(ctx, a.BatchID, a.SuggestedMapping)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if sum.RowsImported != 1 || sum.RowsFailed != 0 {
		t.Fatalf("summary = %+v, want 1 imported / 0 failed", sum)
	}
	res, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-A")
	if err != nil {
		t.Fatalf("GetByTypeKey(DEV-A): %v", err)
	}
	if res.Attributes["geo_lat"] != "45.773" || res.Attributes["geo_lng"] != "126.636" {
		t.Errorf("explicit columns lost: attributes = %+v, want geo_lat 45.773 / geo_lng 126.636", res.Attributes)
	}
	if res.Attributes["geo"] != "125.103,46.59" {
		t.Errorf("raw geo attribute = %q, want the untouched combined value", res.Attributes["geo"])
	}
}

// TestImportGeoInvalidRejected proves the invalid combined values fail the
// row with E004: an unparseable value fails on the geo field, and a pair
// whose components cannot form any (lat, lng) assignment fails on the
// derived geo_lat/geo_lng checks.
func TestImportGeoInvalidRejected(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	rows := [][]string{
		{"DEV-OK", "站点A", "在网", "126.636,45.773"},
		{"DEV-NOTPAIR", "站点B", "在网", "126.636"},   // one component → geo E004
		{"DEV-NOTNUM", "站点C", "在网", "abc,45.75"},  // unparsable → geo E004
		{"DEV-NOORD", "站点D", "在网", "100.5,100.5"}, // neither can be a latitude → E004
	}
	path := writeGeoWorkbook(t, "geo-invalid.xlsx", "设备清单", geoDeviceHeaders, rows)
	sum := importGeoWorkbook(t, svc, path)
	if sum.RowsImported != 1 || sum.RowsFailed != 3 {
		t.Fatalf("summary = %+v, want 1 imported / 3 failed", sum)
	}
	failed := failedRowCodes(t, db, sum.BatchID)
	for _, row := range []int{3, 4, 5} { // Excel rows of the three invalid rows
		if failed[row] != domain.RuleInvalidFormat {
			t.Errorf("failed row %d = %q, want E004", row, failed[row])
		}
	}
	// The good row imported with its split coordinates.
	res, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-OK")
	if err != nil {
		t.Fatalf("GetByTypeKey(DEV-OK): %v", err)
	}
	if res.Attributes["geo_lat"] != "45.773" || res.Attributes["geo_lng"] != "126.636" {
		t.Errorf("DEV-OK attributes = %+v, want split coordinates", res.Attributes)
	}
	// None of the invalid rows created a resource.
	for _, code := range []string{"DEV-NOTPAIR", "DEV-NOTNUM", "DEV-NOORD"} {
		if _, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, code); err == nil {
			t.Errorf("invalid row %s must not create a resource", code)
		}
	}
}

// TestImportGeoSplitSemanticsViaMapping pins the split heuristic through the
// exported mapping function from the importer's perspective: the values the
// importer will write for the canonical workbook examples (哈尔滨/大庆/
// foreign sites), guarding against accidental reordering.
func TestImportGeoSplitSemanticsViaMapping(t *testing.T) {
	cases := []struct {
		value   string
		wantLat string
		wantLng string
	}{
		{"126.636,45.773", "45.773", "126.636"}, // Gaode order (哈尔滨)
		{"46.590,125.103", "46.59", "125.103"},  // (纬度,经度) order (大庆)
		{"123.918，47.354", "47.354", "123.918"}, // Chinese comma (齐齐哈尔)
	}
	for _, tc := range cases {
		lat, lng, ok := mapping.SplitGeoLatLng(tc.value)
		if !ok {
			t.Fatalf("SplitGeoLatLng(%q) not ok", tc.value)
		}
		if got := mapping.FormatGeoFloat(lat); got != tc.wantLat {
			t.Errorf("SplitGeoLatLng(%q) lat = %q, want %q", tc.value, got, tc.wantLat)
		}
		if got := mapping.FormatGeoFloat(lng); got != tc.wantLng {
			t.Errorf("SplitGeoLatLng(%q) lng = %q, want %q", tc.value, got, tc.wantLng)
		}
	}
}
