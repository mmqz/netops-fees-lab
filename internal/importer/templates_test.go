package importer

import (
	"context"
	"errors"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/repo"

	"github.com/xuri/excelize/v2"
)

// tplSheetName / tplHeaders are the deterministic device-sheet shape of the
// unattended journey tests (the testfixtures header row).
var (
	tplSheetName = "设备清单"
	tplHeaders   = []string{"设备编码", "设备名称", "城市", "厂商", "型号", "管理IP", "状态"}
)

// writeDeviceWorkbook writes ONE device-sheet workbook with the given header
// row and data rows to dir and returns its path. Values are raw strings
// (valid: unique codes, dotted-quad IPs, in-enum statuses) so rows import
// clean.
func writeDeviceWorkbook(t *testing.T, dir, name, sheet string, headers []string, rows [][]string) string {
	t.Helper()
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		t.Fatalf("rename sheet: %v", err)
	}
	grid := append([][]string{headers}, rows...)
	for r, row := range grid {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr(sheet, cell, v); err != nil {
				t.Fatalf("set cell: %v", err)
			}
		}
	}
	path := filepath.Join(dir, name)
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close workbook: %v", err)
	}
	return path
}

// tplDeviceRows builds n valid device rows (unique codes and IPs).
func tplDeviceRows(prefix string, n int) [][]string {
	rows := make([][]string, n)
	for i := 0; i < n; i++ {
		rows[i] = []string{
			prefix + "-00" + string(rune('1'+i)),
			"模板设备-" + prefix + string(rune('1'+i)),
			"杭州", "华为", "NE40E",
			"10.60." + prefixDigits(prefix) + "." + itoa(i+1),
			"在网",
		}
	}
	return rows
}

// tplFixedRows returns rows whose values are byte-stable across regenerations
// (the day-1 cohort re-imported as skips on later days).
func tplFixedRows(prefix string, n int) [][]string {
	rows := make([][]string, n)
	for i := 0; i < n; i++ {
		rows[i] = []string{
			prefix + "-F0" + string(rune('1'+i)),
			"固定设备-" + prefix + string(rune('1'+i)),
			"杭州", "华为", "NE40E",
			"10.61." + prefixDigits(prefix) + "." + itoa(i+1),
			"在网",
		}
	}
	return rows
}

func prefixDigits(prefix string) string {
	switch prefix {
	case "D2":
		return "2"
	case "D3":
		return "3"
	default:
		return "1"
	}
}

func itoa(n int) string {
	return string(rune('0' + n))
}

// TestUnattendedTemplateJourney walks the §12-B7 方案 A lifecycle at the
// service level: day 1 the ordinary human flow (analyze → execute → capture);
// then the unattended auto import of a SAME-header workbook (executes with
// the stored mapping, idempotent skips on the re-imported cohort); then the
// honest refusal of a RENAMED-column workbook (different fingerprint →
// NoMatchingTemplateError, batch STAYS awaiting_mapping — the human
// fallback, never an error state, no writes).
func TestUnattendedTemplateJourney(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	dir := testsupport.TempDir(t)

	// Day 1: the ordinary flow.
	day1 := writeDeviceWorkbook(t, dir, "day1.xlsx", tplSheetName, tplHeaders, tplFixedRows("D1", 3))
	a1, err := svc.Analyze(ctx, day1)
	if err != nil {
		t.Fatalf("Analyze day1: %v", err)
	}
	if a1.Sheets[0].TemplateMatched != nil {
		t.Fatalf("day1 templateMatched = %+v, want nil (no template yet)", a1.Sheets[0].TemplateMatched)
	}
	sum1, err := svc.Execute(ctx, a1.BatchID, a1.SuggestedMapping)
	if err != nil {
		t.Fatalf("Execute day1: %v", err)
	}
	if sum1.RowsImported != 3 {
		t.Fatalf("day1 imported = %d, want 3", sum1.RowsImported)
	}

	// Capture: the one-click save of the completed batch.
	captured, err := svc.CaptureTemplate(ctx, a1.BatchID, "")
	if err != nil {
		t.Fatalf("CaptureTemplate: %v", err)
	}
	if len(captured) != 1 {
		t.Fatalf("captured templates = %d, want 1", len(captured))
	}
	tpl := captured[0]
	wantFP := mapping.Fingerprint(tplSheetName, tplHeaders)
	if tpl.HeaderFingerprint != wantFP {
		t.Errorf("captured fingerprint = %s, want %s (recomputed from the workbook headers)", tpl.HeaderFingerprint, wantFP)
	}
	if tpl.ResourceType != domain.ResourceTypeDevice || tpl.SheetName != tplSheetName {
		t.Errorf("captured template = %+v, want device/设备清单", tpl)
	}
	if tpl.ConfirmedBatch != a1.BatchID {
		t.Errorf("captured confirmed_batch = %s, want the creating batch %s", tpl.ConfirmedBatch, a1.BatchID)
	}
	if tpl.MappingJSON == "" || !strings.Contains(tpl.MappingJSON, `"设备编码"`) {
		t.Errorf("captured mapping_json = %q, want the confirmed single-sheet mapping", tpl.MappingJSON)
	}
	// Source name defaults to the batch's workbook name.
	if tpl.SourceName != "day1.xlsx" {
		t.Errorf("captured source_name = %q, want day1.xlsx (batch workbook name fallback)", tpl.SourceName)
	}

	// A fresh analyze of the SAME shape now reports the match.
	aX, err := svc.Analyze(ctx, day1)
	if err != nil {
		t.Fatalf("re-Analyze day1: %v", err)
	}
	if aX.Sheets[0].TemplateMatched == nil || aX.Sheets[0].TemplateMatched.TemplateID != tpl.TemplateID {
		t.Fatalf("templateMatched after capture = %+v, want the captured template", aX.Sheets[0].TemplateMatched)
	}

	// Day 2: same headers, different rows — the unattended import executes.
	day2Rows := append(append([][]string(nil), tplFixedRows("D1", 3)...), tplDeviceRows("D2", 2)...)
	day2 := writeDeviceWorkbook(t, dir, "day2.xlsx", tplSheetName, tplHeaders, day2Rows)
	res, err := svc.ImportAuto(ctx, day2, "day2.xlsx")
	if err != nil {
		t.Fatalf("ImportAuto day2: %v", err)
	}
	if !res.Executed {
		t.Fatalf("ImportAuto day2 refused: %+v (warning %+v)", res.Analysis.AutoWarning, res.Analysis.AutoWarning)
	}
	// 5 rows: the 3 day-1 devices are duplicate keys → skipped; 2 new.
	if res.Summary.RowsTotal != 5 || res.Summary.RowsImported != 2 || res.Summary.RowsSkipped != 3 || res.Summary.RowsFailed != 0 {
		t.Errorf("day2 summary = %+v, want 5 total / 2 imported / 3 skipped / 0 failed", res.Summary)
	}
	if res.Summary.Status != domain.BatchStatusCompleted {
		t.Errorf("day2 status = %s, want completed", res.Summary.Status)
	}
	// The auto batch recorded the merged template mapping (import_batches.mapping continuity).
	b2, err := db.Batches.Get(ctx, res.Summary.BatchID)
	if err != nil || !strings.Contains(b2.Mapping, "设备编码") {
		t.Errorf("auto batch mapping = %q (err %v), want the merged template mapping stored", b2.Mapping, err)
	}
	// No duplicate resources: 3 + 2 devices.
	if n := countResources(t, db, domain.ResourceTypeDevice); n != 5 {
		t.Errorf("devices after day2 = %d, want 5 (no duplicates)", n)
	}

	// Day 3: ONE RENAMED column — the fingerprint changes, the auto path
	// REFUSES, the batch stays awaiting_mapping, nothing is written.
	renamed := append([]string(nil), tplHeaders...)
	renamed[1] = "设备名称新"
	day3 := writeDeviceWorkbook(t, dir, "day3.xlsx", tplSheetName, renamed, tplDeviceRows("D3", 4))
	before := countResources(t, db, domain.ResourceTypeDevice)
	res3, err := svc.ImportAuto(ctx, day3, "day3.xlsx")
	if err == nil {
		t.Fatalf("ImportAuto day3: want refusal, got executed=%v", res3.Executed)
	}
	var refuse *NoMatchingTemplateError
	if !errors.As(err, &refuse) {
		t.Fatalf("ImportAuto day3 error = %v, want *NoMatchingTemplateError", err)
	}
	if refuse.Code != AutoRefusalNoTemplate {
		t.Errorf("refusal code = %q, want %q", refuse.Code, AutoRefusalNoTemplate)
	}
	if len(refuse.UnmatchedSheets) != 1 || refuse.UnmatchedSheets[0] != tplSheetName {
		t.Errorf("refusal sheets = %v, want [%s] (表头变更感知 names the sheet)", refuse.UnmatchedSheets, tplSheetName)
	}
	if res3.Analysis.BatchID == "" {
		t.Fatalf("refused analysis has no batch — the refusal must leave the batch for the human flow")
	}
	b3, err := db.Batches.Get(ctx, res3.Analysis.BatchID)
	if err != nil {
		t.Fatalf("load refused batch: %v", err)
	}
	if b3.Status != domain.BatchStatusAwaitingMapping {
		t.Errorf("refused batch status = %s, want awaiting_mapping (the human fallback, not an error state)", b3.Status)
	}
	// The refusal warning is attached to the analysis (表头变更感知, never silent).
	if res3.Analysis.AutoWarning == nil || res3.Analysis.AutoWarning.Code != AutoRefusalNoTemplate ||
		!strings.Contains(res3.Analysis.AutoWarning.Message, tplSheetName) {
		t.Errorf("autoWarning = %+v, want no_matching_template naming the sheet", res3.Analysis.AutoWarning)
	}
	// The renamed sheet reports NO template match (renamed column ⇒ different fingerprint).
	for i := range res3.Analysis.Sheets {
		if res3.Analysis.Sheets[i].TemplateMatched != nil {
			t.Errorf("renamed sheet templateMatched = %+v, want nil", res3.Analysis.Sheets[i].TemplateMatched)
		}
	}
	// No rows were written by the refusal.
	if n := countResources(t, db, domain.ResourceTypeDevice); n != before {
		t.Errorf("devices after refusal = %d, want %d (no writes on refusal)", n, before)
	}

	// The human fallback still works: confirm the refused batch manually
	// with a fixed mapping, and it completes.
	fixed := res3.Analysis.SuggestedMapping
	for i := range fixed.Sheets {
		if fixed.Sheets[i].Action == mapping.ActionImport {
			delete(fixed.Sheets[i].ColumnMapping, "device_name")
			fixed.Sheets[i].ColumnMapping["device_name"] = "设备名称新"
		}
	}
	if _, err := svc.Execute(ctx, res3.Analysis.BatchID, fixed); err != nil {
		t.Fatalf("manual confirm of the refused batch: %v", err)
	}
	if n := countResources(t, db, domain.ResourceTypeDevice); n != before+4 {
		t.Errorf("devices after manual confirm = %d, want %d", n, before+4)
	}
}

// TestCaptureTemplateGuards pins the capture preconditions: unknown batch →
// ErrNotFound; a not-completed batch → ErrConflict (only a COMPLETED
// batch's confirmed mapping is worth saving).
func TestCaptureTemplateGuards(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	if _, err := svc.CaptureTemplate(ctx, "bat_nope", ""); err == nil || !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("capture unknown batch = %v, want ErrNotFound", err)
	}

	dir := testsupport.TempDir(t)
	day1 := writeDeviceWorkbook(t, dir, "g1.xlsx", tplSheetName, tplHeaders, tplFixedRows("D1", 2))
	a, err := svc.Analyze(ctx, day1)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if _, err := svc.CaptureTemplate(ctx, a.BatchID, ""); err == nil || !errors.Is(err, domain.ErrConflict) {
		t.Errorf("capture awaiting_mapping batch = %v, want ErrConflict (completed only)", err)
	}
}

// TestCaptureTemplateReconfirmIdempotent pins the upsert contract: capturing
// a second completed batch of the SAME sheet shape REPLACES the stored
// mapping (new confirmed_at, new confirmed_batch, same fingerprint) instead
// of adding a second row — the idempotent re-confirmation of §12-B7.
func TestCaptureTemplateReconfirmIdempotent(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	dir := testsupport.TempDir(t)

	p1 := writeDeviceWorkbook(t, dir, "r1.xlsx", tplSheetName, tplHeaders, tplFixedRows("D1", 2))
	a1, err := svc.Analyze(ctx, p1)
	if err != nil {
		t.Fatalf("Analyze r1: %v", err)
	}
	if _, err := svc.Execute(ctx, a1.BatchID, a1.SuggestedMapping); err != nil {
		t.Fatalf("Execute r1: %v", err)
	}
	first, err := svc.CaptureTemplate(ctx, a1.BatchID, "first")
	if err != nil || len(first) != 1 {
		t.Fatalf("capture r1 = %v (err %v)", first, err)
	}

	// A second completed batch of the same shape (a re-import: all skips).
	a2, err := svc.Analyze(ctx, p1)
	if err != nil {
		t.Fatalf("re-Analyze r1: %v", err)
	}
	if _, err := svc.Execute(ctx, a2.BatchID, a2.SuggestedMapping); err != nil {
		t.Fatalf("Execute re-import: %v", err)
	}
	second, err := svc.CaptureTemplate(ctx, a2.BatchID, "second")
	if err != nil || len(second) != 1 {
		t.Fatalf("capture re-import = %v (err %v)", second, err)
	}
	if second[0].HeaderFingerprint != first[0].HeaderFingerprint {
		t.Fatalf("re-confirm fingerprint changed: %s vs %s", second[0].HeaderFingerprint, first[0].HeaderFingerprint)
	}
	if second[0].ConfirmedBatch != a2.BatchID {
		t.Errorf("re-confirm confirmed_batch = %s, want the second batch %s", second[0].ConfirmedBatch, a2.BatchID)
	}
	if !second[0].ConfirmedAt.After(first[0].ConfirmedAt) && !second[0].ConfirmedAt.Equal(first[0].ConfirmedAt) {
		t.Errorf("re-confirm confirmed_at = %s, want >= first %s", second[0].ConfirmedAt, first[0].ConfirmedAt)
	}
	// Still exactly ONE template row of that fingerprint.
	all, total, err := db.Templates.ListTemplates(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	n := 0
	for _, t := range all {
		if t.HeaderFingerprint == second[0].HeaderFingerprint {
			n++
		}
	}
	if n != 1 || total != 1 {
		t.Errorf("templates of the fingerprint = %d (total %d), want 1 (idempotent re-confirmation)", n, total)
	}
}

// TestImportAutoNoImportSheets pins the honest refusal of a workbook with no
// importable sheet: an unattended run that would import NOTHING must not
// report success — the batch stays awaiting_mapping for the human.
func TestImportAutoNoImportSheets(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	dir := testsupport.TempDir(t)

	// A workbook whose sheet is confirmed first (so a template exists), then
	// an unrecognizable sheet (gibberish headers → suggested skip).
	p1 := writeDeviceWorkbook(t, dir, "n1.xlsx", tplSheetName, tplHeaders, tplFixedRows("D1", 2))
	a1, err := svc.Analyze(ctx, p1)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if _, err := svc.Execute(ctx, a1.BatchID, a1.SuggestedMapping); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, err := svc.CaptureTemplate(ctx, a1.BatchID, ""); err != nil {
		t.Fatalf("CaptureTemplate: %v", err)
	}

	unknown := writeDeviceWorkbook(t, dir, "n2.xlsx", "备注说明", []string{"甲", "乙", "丙"}, [][]string{{"1", "2", "3"}})
	res, err := svc.ImportAuto(ctx, unknown, "n2.xlsx")
	if err == nil {
		t.Fatalf("ImportAuto no-import workbook: want refusal, got executed=%v", res.Executed)
	}
	var refuse *NoMatchingTemplateError
	if !errors.As(err, &refuse) || refuse.Code != AutoRefusalNoImportSheets {
		t.Fatalf("refusal = %v, want no_import_sheets", err)
	}
	b, err := db.Batches.Get(ctx, res.Analysis.BatchID)
	if err != nil || b.Status != domain.BatchStatusAwaitingMapping {
		t.Errorf("batch after no-import refusal = %s (err %v), want awaiting_mapping", b.Status, err)
	}
}

// TestResolveTemplateHeaders pins the cosmetic-drift re-anchoring: a stored
// header differing from the current sheet's header only by case/fullwidth
// folds (the fingerprint treats them as the same column) is rewritten onto
// the CURRENT string, so the merged mapping validates; a genuinely absent
// header is kept (Validate rejects it honestly); an ambiguous fold fails.
func TestResolveTemplateHeaders(t *testing.T) {
	sm := mapping.SheetMapping{
		Sheet: "设备清单", Action: mapping.ActionImport, ResourceType: domain.ResourceTypeDevice,
		ColumnMapping: map[string]string{"device_code": "设备编码", "mgmt_ip": "管理ＩＰ"},
	}
	a := Analysis{Sheets: []SheetAnalysis{{
		Sheet: "设备清单",
		Columns: []ColumnAnalysis{
			{Header: "设备编码"}, {Header: "管理IP"},
		},
	}}}
	resolved, err := resolveTemplateHeaders(sm, a, "设备清单")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.ColumnMapping["mgmt_ip"] != "管理IP" {
		t.Errorf("re-anchored mgmt_ip = %q, want the current exact header 管理IP", resolved.ColumnMapping["mgmt_ip"])
	}
	if resolved.ColumnMapping["device_code"] != "设备编码" {
		t.Errorf("exact header rewritten = %q, want untouched 设备编码", resolved.ColumnMapping["device_code"])
	}

	// Absent header: kept for Validate to reject.
	absent := Analysis{Sheets: []SheetAnalysis{{Sheet: "设备清单", Columns: []ColumnAnalysis{{Header: "设备编码"}}}}}
	resolved, err = resolveTemplateHeaders(sm, absent, "设备清单")
	if err != nil {
		t.Fatalf("resolve absent: %v", err)
	}
	if resolved.ColumnMapping["mgmt_ip"] != "管理ＩＰ" {
		t.Errorf("absent header = %q, want the stored string (honest Validate failure)", resolved.ColumnMapping["mgmt_ip"])
	}

	// Ambiguous fold: two current headers normalize identically.
	amb := Analysis{Sheets: []SheetAnalysis{{Sheet: "设备清单", Columns: []ColumnAnalysis{
		{Header: "管理IP"}, {Header: "管理ｉｐ"},
	}}}}
	if _, err := resolveTemplateHeaders(sm, amb, "设备清单"); err == nil || !strings.Contains(err.Error(), "ambiguously") {
		t.Errorf("ambiguous fold = %v, want ambiguity error", err)
	}
}

// countResources counts the resources of one type.
func countResources(t *testing.T, db *repo.DB, typ domain.ResourceType) int {
	t.Helper()
	rows, err := db.Resources.ListByType(context.Background(), typ)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	return len(rows)
}
