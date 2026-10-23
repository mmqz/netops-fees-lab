package fees

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/xuri/excelize/v2"
)

// sheetSpec is one sheet of a test workbook: its name and its rows
// (row 0 is the header row).
type sheetSpec struct {
	name string
	rows [][]string
}

// writeLedgerXLSX builds a workbook whose sheets appear in the given
// ORDER (writeXLSX's map-based extraSheets cannot promise order, and the
// multi-sheet channel reports batches in workbook order — the assertion
// needs determinism). The default "Sheet1" is renamed to the first spec.
func writeLedgerXLSX(t *testing.T, dir, name string, sheets []sheetSpec) string {
	t.Helper()
	if len(sheets) == 0 {
		t.Fatal("writeLedgerXLSX needs at least one sheet")
	}
	f := excelize.NewFile()
	first := f.GetSheetList()[0]
	if err := f.SetSheetName(first, sheets[0].name); err != nil {
		t.Fatalf("rename first sheet: %v", err)
	}
	for _, spec := range sheets {
		sheet := spec.name
		if sheet != sheets[0].name {
			if _, err := f.NewSheet(sheet); err != nil {
				t.Fatalf("new sheet %s: %v", sheet, err)
			}
		}
		for i, row := range spec.rows {
			for j, cell := range row {
				ref, err := excelize.CoordinatesToCellName(j+1, i+1)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.SetCellValue(sheet, ref, cell); err != nil {
					t.Fatalf("set %s!%s: %v", sheet, ref, err)
				}
			}
		}
	}
	p := filepath.Join(dir, name)
	if err := f.SaveAs(p); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// threeTypeLedger builds the #125 acceptance workbook verbatim: one file
// = one month, one sheet per fee type, plus the cosmetic cover sheet a
// real ledger carries. The first sheet is 电费 — the sheet the OLD
// first-sheet-only reader imported while silently losing the other two.
func threeTypeLedger(t *testing.T, dir string) string {
	t.Helper()
	return writeLedgerXLSX(t, dir, "2024年05月费用.xlsx", []sheetSpec{
		{"电费", [][]string{
			{"站址编码", "站址名称", "电费(元)", "账期"},
			{"S1", "一站", "12345.67", "2024-05"},
			{"S2", "二站", "678.90", "2024-05"},
		}},
		{"铁塔租费", [][]string{
			{"站址编码", "站址名称", "塔租费(元)", "账期"},
			{"S1", "一站", "5000.00", "2024-05"},
		}},
		{"修理费", [][]string{
			{"站址编码", "站址名称", "修理费(元)", "账期", "工单号"},
			{"S2", "二站", "999.99", "2024-05", "GD-001"},
		}},
		{"说明", [][]string{{"本表由财务部编制"}}},
	})
}

// TestImportSheetsThreeTypeLedger is THE #125 acceptance: the real
// monthly ledger shape imports as THREE batches with correct types, the
// cosmetic sheet is named (never silently dropped), and — the headline —
// the tower_lease partition holds the 铁塔租费 sheet's 5000.00, NOT the
// first sheet's 12345.67 (the exact pollution the issue demonstrated).
func TestImportSheetsThreeTypeLedger(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	path := threeTypeLedger(t, t.TempDir())

	sum, err := svc.ImportSheets(ctx, SheetsImportOptions{
		Path: path, OriginalName: "2024年05月费用.xlsx", Year: 2024, Month: 5,
	})
	if err != nil {
		t.Fatalf("ImportSheets: %v", err)
	}
	if sum.Status != "completed" {
		t.Fatalf("status = %q, want completed", sum.Status)
	}
	if len(sum.Batches) != 3 {
		t.Fatalf("batches = %d, want 3 (one per fee-type sheet)\n%+v", len(sum.Batches), sum.Batches)
	}
	// workbook order, one batch per sheet, each type from its own evidence.
	want := []struct {
		sheet string
		typ   FeeType
		rows  int
	}{{"电费", FeeTypeElectricity, 2}, {"铁塔租费", FeeTypeTowerLease, 1}, {"修理费", FeeTypeRepair, 1}}
	for i, w := range want {
		b := sum.Batches[i]
		if b.Sheet != w.sheet || b.FeeType != string(w.typ) || b.Status != "completed" || b.RowsImported != w.rows {
			t.Fatalf("batches[%d] = %+v, want sheet %s type %s rows %d", i, b, w.sheet, w.typ, w.rows)
		}
		if b.BatchID == "" {
			t.Fatalf("batches[%d] has no batch id", i)
		}
	}
	if len(sum.SkippedSheets) != 1 || sum.SkippedSheets[0] != "说明" {
		t.Fatalf("skippedSheets = %v, want [说明] — cosmetic sheets are NAMED", sum.SkippedSheets)
	}

	// The headline: the tower_lease batch's parquet rows are the 塔租费
	// sheet's own amounts with per-sheet evidence anchors.
	tower := sum.Batches[1]
	files, err := BatchParquetFiles(filepath.Join(dir, "analysis"), tower.BatchID)
	if err != nil || len(files) != 1 {
		t.Fatalf("tower parquet files = %v (err %v), want 1", files, err)
	}
	rows := readParquetRows(t, files[0])
	if len(rows) != 1 {
		t.Fatalf("tower partition rows = %d, want 1", len(rows))
	}
	if rows[0].SiteCode != "S1" || rows[0].AmountCents != 500000 {
		t.Fatalf("tower row = %s %.2f, want S1 5000.00 — the 电费 sheet's 12345.67 must never reach the tower ledger",
			rows[0].SiteCode, float64(rows[0].AmountCents)/100)
	}
	// the anchor names the staged file (content-addressed name carries the
	// sha prefix) plus the sheet suffix — rows of different sheets share
	// sheet-local row numbers, so the sheet MUST be in the anchor.
	if !strings.HasSuffix(rows[0].SourceFile, "2024年05月费用.xlsx#铁塔租费") {
		t.Fatalf("tower source_file = %q, want it to end with the per-sheet anchor", rows[0].SourceFile)
	}
	if rows[0].SourceRow != 2 {
		t.Fatalf("tower source_row = %d, want 2 (the sheet-local data row)", rows[0].SourceRow)
	}
	// and the electricity batch holds ITS sheet's rows — both of them.
	elec := sum.Batches[0]
	elecFiles, err := BatchParquetFiles(filepath.Join(dir, "analysis"), elec.BatchID)
	if err != nil || len(elecFiles) != 1 {
		t.Fatalf("electricity parquet files = %v (err %v)", elecFiles, err)
	}
	elecRows := readParquetRows(t, elecFiles[0])
	if len(elecRows) != 2 || elecRows[0].AmountCents != 1234567 || elecRows[1].AmountCents != 67890 {
		t.Fatalf("electricity rows = %+v, want S1 12345.67 + S2 678.90", elecRows)
	}
}

// readParquetRows reads every row of one parquet file (the pure-Go
// reader — the same assertion path parquet_test.go uses).
func readParquetRows(t *testing.T, path string) []feeParquetRow {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open parquet %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	r := parquet.NewGenericReader[feeParquetRow](f)
	defer func() { _ = r.Close() }()
	var out []feeParquetRow
	buf := make([]feeParquetRow, 8)
	for {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	return out
}

// TestImportSheetsIdempotency pins acceptance #4: re-importing the same
// workbook is a per-sheet duplicate skip — every entry answers
// skippedDuplicate with the existing batch, and no new parquet files
// appear.
func TestImportSheetsIdempotency(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	path := threeTypeLedger(t, t.TempDir())

	first, err := svc.ImportSheets(ctx, SheetsImportOptions{
		Path: path, OriginalName: "2024年05月费用.xlsx", Year: 2024, Month: 5,
	})
	if err != nil {
		t.Fatalf("first ImportSheets: %v", err)
	}
	before := 0
	for _, b := range first.Batches {
		files, err := BatchParquetFiles(filepath.Join(dir, "analysis"), b.BatchID)
		if err != nil {
			t.Fatalf("list files of %s: %v", b.BatchID, err)
		}
		before += len(files)
	}

	second, err := svc.ImportSheets(ctx, SheetsImportOptions{
		Path: path, OriginalName: "2024年05月费用.xlsx", Year: 2024, Month: 5,
	})
	if err != nil {
		t.Fatalf("re-import must be informational, got %v", err)
	}
	if len(second.Batches) != len(first.Batches) {
		t.Fatalf("re-import batches = %d, want %d", len(second.Batches), len(first.Batches))
	}
	for i := range second.Batches {
		got, want := second.Batches[i], first.Batches[i]
		if !got.SkippedDuplicate || got.ExistingBatch != want.BatchID {
			t.Fatalf("re-import batches[%d] = %+v, want skippedDuplicate → %s", i, got, want.BatchID)
		}
	}
	after := 0
	for _, b := range first.Batches {
		files, err := BatchParquetFiles(filepath.Join(dir, "analysis"), b.BatchID)
		if err != nil {
			t.Fatal(err)
		}
		after += len(files)
	}
	if after != before {
		t.Fatalf("parquet files after re-import = %d, want %d (no duplicates landed)", after, before)
	}
}

// TestImportSheetsRefusesCSV: the multi-sheet channel's premise is sheet
// names as type declarations; a CSV has none, so it refuses with the
// honest pointer to the single-sheet channel.
func TestImportSheetsRefusesCSV(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	path := writeCSV(t, dir, "may.csv", "站址编码,账期,电费(元)\nS1,2024-05,10.00\n")

	_, err := svc.ImportSheets(ctx, SheetsImportOptions{
		Path: path, OriginalName: "may.csv", Year: 2024, Month: 5,
	})
	if !errors.Is(err, ErrImportRefused) {
		t.Fatalf("CSV in sheets mode err = %v, want ErrImportRefused", err)
	}
}

// TestImportSheetsRefusesUnclassifiableSheet pins the all-or-nothing
// doctrine: a sheet with no type evidence refuses the WHOLE import
// BEFORE any sheet lands — "completed with missing data" is impossible,
// and the refusal names the offender.
func TestImportSheetsRefusesUnclassifiableSheet(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	path := writeLedgerXLSX(t, dir, "mixed.xlsx", []sheetSpec{
		{"电费", [][]string{
			{"站址编码", "账期", "电费(元)"},
			{"S1", "2024-05", "10.00"},
		}},
		{"Sheet5", [][]string{
			{"站址编码", "账期", "金额"},
			{"S1", "2024-05", "20.00"},
		}},
	})

	_, err := svc.ImportSheets(ctx, SheetsImportOptions{
		Path: path, OriginalName: "mixed.xlsx", Year: 2024, Month: 5,
	})
	if !errors.Is(err, ErrImportRefused) {
		t.Fatalf("unclassifiable sheet err = %v, want ErrImportRefused", err)
	}
	// NOTHING landed — not even the classifiable first sheet.
	batches, err := db.FeeBatches.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 0 {
		t.Fatalf("batches after refused import = %d, want 0 (all-or-nothing)", len(batches))
	}
}

// TestImportSheetsModeConflictMultiThenSingle: after the multi-sheet
// channel owns a file, the single-sheet channel refuses it — the two
// channels key the same data differently, and both landing would
// double-count the first sheet's rows past every idempotency check.
func TestImportSheetsModeConflictMultiThenSingle(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	path := threeTypeLedger(t, t.TempDir())

	if _, err := svc.ImportSheets(ctx, SheetsImportOptions{
		Path: path, OriginalName: "2024年05月费用.xlsx", Year: 2024, Month: 5,
	}); err != nil {
		t.Fatalf("ImportSheets: %v", err)
	}
	_, err := svc.Import(ctx, ImportOptions{
		Path: path, OriginalName: "2024年05月费用.xlsx",
		FeeType: FeeTypeElectricity, Year: 2024, Month: 5,
	})
	if !errors.Is(err, ErrImportRefused) {
		t.Fatalf("single after multi err = %v, want ErrImportRefused", err)
	}
}

// TestImportSheetsModeConflictSingleThenMulti is the mirror: the file
// already imported through the single-sheet channel refuses the
// multi-sheet channel — the operator's honest paths (rollback first, or
// stay on the original channel) are in the refusal text.
func TestImportSheetsModeConflictSingleThenMulti(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	path := threeTypeLedger(t, t.TempDir())

	if _, err := svc.Import(ctx, ImportOptions{
		Path: path, OriginalName: "2024年05月费用.xlsx",
		FeeType: FeeTypeElectricity, Year: 2024, Month: 5,
	}); err != nil {
		t.Fatalf("single Import (first sheet, declared type): %v", err)
	}
	_, err := svc.ImportSheets(ctx, SheetsImportOptions{
		Path: path, OriginalName: "2024年05月费用.xlsx", Year: 2024, Month: 5,
	})
	if !errors.Is(err, ErrImportRefused) {
		t.Fatalf("multi after single err = %v, want ErrImportRefused", err)
	}
}

// TestCrossCheckDeclaredType pins the single-sheet channel's plan-C
// guard (#125 blocking 2): a declared feeType that CONTRADICTS the
// sheet's own evidence refuses (the name 电费 + header 电费(元) declared
// as tower_lease used to import electricity rows into the tower ledger);
// agreeing or silent evidence never refuses.
func TestCrossCheckDeclaredType(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	path := threeTypeLedger(t, t.TempDir())

	// contradiction: sheet 电费 declared tower_lease → refuse.
	if err := crossCheckDeclaredType(FeeTypeTowerLease, SheetData{
		Name: "电费", Headers: []string{"站址编码", "电费(元)", "账期"},
	}); !errors.Is(err, ErrImportRefused) {
		t.Fatalf("declared tower_lease over 电费 sheet = %v, want refusal", err)
	}
	// agreement: declared electricity over its own sheet → pass.
	if err := crossCheckDeclaredType(FeeTypeElectricity, SheetData{
		Name: "电费", Headers: []string{"站址编码", "电费(元)", "账期"},
	}); err != nil {
		t.Fatalf("declared electricity over 电费 sheet = %v, want nil", err)
	}
	// silent sheet: no evidence at all → the operator's word wins.
	if err := crossCheckDeclaredType(FeeTypeTowerLease, SheetData{
		Name: "Sheet1", Headers: []string{"站址编码", "金额", "账期"},
	}); err != nil {
		t.Fatalf("silent sheet declared tower_lease = %v, want nil", err)
	}

	// and through the real channel: the contradiction refuses end to end.
	_, err := svc.Import(ctx, ImportOptions{
		Path: path, OriginalName: "2024年05月费用.xlsx",
		FeeType: FeeTypeTowerLease, Year: 2024, Month: 5,
	})
	if !errors.Is(err, ErrImportRefused) {
		t.Fatalf("single Import with contradicting declared type = %v, want ErrImportRefused", err)
	}
}
