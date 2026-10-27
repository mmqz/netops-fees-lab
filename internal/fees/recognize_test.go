package fees

// recognize_test.go — the xlsx reading channel of ReadFeeFile (#70).
// The CSV channel is exercised end-to-end by every service test (writeCSV
// helpers), but the xlsx branch (excelize open / first-sheet contract /
// evidence row numbers / blank-row skip) had ZERO coverage: the recognizer
// tests only ever saw header strings, never a real workbook. If excelize
// changed its Rows iterator or sheet semantics, nothing would go red —
// while source_row (the evidence coordinate that points back into the
// snapshot) silently drifted.
//
// The pinned contract (recognize.go doc): SourceRow.Number is the SOURCE
// FILE's own row number — an xlsx header sits on sheet row 1, so the first
// data row is Number 2 (exactly what an operator sees in Excel), and a
// blank row keeps its number (it occupies a row even though it carries no
// data — the coordinate must stay usable in the original file).

import (
	"archive/zip"
	"errors"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/workbook"
	"github.com/xuri/excelize/v2"
)

// writeXLSX builds a real workbook on disk: rows go to the FIRST sheet in
// the given order; extraSheets (name → rows) are appended after it.
func writeXLSX(t *testing.T, dir, name string, rows [][]string, extraSheets map[string][][]string) string {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetList()[0] // "Sheet1"
	for i, row := range rows {
		for j, cell := range row {
			ref, err := excelize.CoordinatesToCellName(j+1, i+1)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellValue(sheet, ref, cell); err != nil {
				t.Fatal(err)
			}
		}
	}
	for sName, sRows := range extraSheets {
		if _, err := f.NewSheet(sName); err != nil {
			t.Fatal(err)
		}
		for i, row := range sRows {
			for j, cell := range row {
				ref, err := excelize.CoordinatesToCellName(j+1, i+1)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.SetCellValue(sName, ref, cell); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	p := filepath.Join(dir, name)
	if err := f.SaveAs(p); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadFeeFileXLSX(t *testing.T) {
	dir := testsupport.TempDir(t)
	// Sheet row 1 = header; row 2 and 4/5 = data; row 3 = BLANK (occupied
	// but skipped — its number must not leak into the data rows').
	src := writeXLSX(t, dir, "may-bills.xlsx", [][]string{
		{"站址编码", "账期", "电费(元)"},
		{"SITE-A1", "2024-05", "1234.56"},
		{"", "", ""},
		{"SITE-A2", "2024-05", "999.00"},
		{"SITE-A3", "2024-05", "88.88"},
	}, nil)

	headers, rows, err := ReadFeeFile(src)
	if err != nil {
		t.Fatalf("ReadFeeFile(xlsx): %v", err)
	}
	if len(headers) != 3 || headers[0] != "站址编码" || headers[1] != "账期" || headers[2] != "电费(元)" {
		t.Fatalf("headers = %v, want the three core columns", headers)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (the blank row is skipped, not counted)", len(rows))
	}
	// Evidence coordinates: the source file's OWN numbering — first data
	// row is sheet row 2, the blank row keeps number 3, then 4 and 5.
	wantNums := []int{2, 4, 5}
	wantCodes := []string{"SITE-A1", "SITE-A2", "SITE-A3"}
	for i, r := range rows {
		if r.Number != wantNums[i] {
			t.Errorf("rows[%d].Number = %d, want %d (source-file numbering; blank rows keep their numbers)", i, r.Number, wantNums[i])
		}
		if r.Cells[0] != wantCodes[i] {
			t.Errorf("rows[%d].Cells[0] = %q, want %q", i, r.Cells[0], wantCodes[i])
		}
	}
}

func TestReadFeeFileXLSXMultiSheet(t *testing.T) {
	dir := testsupport.TempDir(t)
	// First-sheet contract: a workbook whose SECOND sheet carries far more
	// data than the first must read ONLY the first — the contract is
	// documented (recognize.go: "xlsx: the first sheet only") but was never
	// pinned; a silent change would swallow real fee rows.
	src := writeXLSX(t, dir, "two-sheets.xlsx",
		[][]string{
			{"站址编码", "账期", "电费(元)"},
			{"SITE-FIRST", "2024-05", "10.00"},
		},
		map[string][][]string{
			"汇总说明页": {
				{"站址编码", "账期", "电费(元)"},
				{"SITE-SECOND-1", "2024-05", "1.00"},
				{"SITE-SECOND-2", "2024-05", "2.00"},
				{"SITE-SECOND-3", "2024-05", "3.00"},
			},
		})

	headers, rows, err := ReadFeeFile(src)
	if err != nil {
		t.Fatalf("ReadFeeFile(xlsx multi-sheet): %v", err)
	}
	if len(rows) != 1 || rows[0].Cells[0] != "SITE-FIRST" {
		t.Fatalf("rows = %v — want ONLY the first sheet's single row (first-sheet contract)", rows)
	}
	if len(headers) != 3 {
		t.Fatalf("headers = %v", headers)
	}
}

func TestReadFeeFileXLSXNoHeader(t *testing.T) {
	dir := testsupport.TempDir(t)
	// A sheet with no values at all: the first-non-blank-row header search
	// finds nothing — the refusal copy must name the sheet.
	src := writeXLSX(t, dir, "empty.xlsx", [][]string{{"", "", ""}}, nil)

	_, _, err := ReadFeeFile(src)
	if err == nil {
		t.Fatal("ReadFeeFile(empty xlsx) = nil error, want the no-header refusal")
	}
	if !strings.Contains(err.Error(), "没有可识别的表头行") {
		t.Fatalf("err = %v, want the no-recognizable-header copy", err)
	}
}

// --- honest import guard (#81): the fees channel's own copy of the corruption scenarios ---

// rewriteZipEntryFees rewrites one entry of an xlsx zip into a new file
// (test-local copy of the workbook package's helper — test files do not
// cross packages).
func rewriteZipEntryFees(t *testing.T, src, name string, transform func([]byte) []byte) string {
	t.Helper()
	zr, err := zip.OpenReader(src)
	if err != nil {
		t.Fatalf("OpenReader(%s): %v", src, err)
	}
	defer func() { _ = zr.Close() }()
	dst := filepath.Join(testsupport.TempDir(t), "rewritten.xlsx")
	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	zw := zip.NewWriter(out)
	for _, ze := range zr.File {
		rc, err := ze.Open()
		if err != nil {
			t.Fatalf("open %s: %v", ze.Name, err)
		}
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read %s: %v", ze.Name, err)
		}
		_ = rc.Close()
		if ze.Name == name {
			data = transform(data)
		}
		w, err := zw.Create(ze.Name)
		if err != nil {
			t.Fatalf("create %s: %v", ze.Name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("write %s: %v", ze.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("file Close: %v", err)
	}
	return dst
}

// TestReadFeeFileRejectsTruncatedSheet pins the fee channel to the same
// honest-import contract (#81): evidence anchors (source_file + source_row)
// are only as trustworthy as the row count, so a sheet cut mid-file must
// surface as an error — never as a clean partial import.
func TestReadFeeFileRejectsTruncatedSheet(t *testing.T) {
	dir := testsupport.TempDir(t)
	src := writeXLSX(t, dir, "trunc.xlsx", [][]string{
		{"站址编码", "电费", "账期"},
		{"HLJ0001", "100", "2025-01"},
		{"HLJ0002", "200", "2025-01"},
		{"HLJ0003", "300", "2025-01"},
	}, nil)
	corrupt := rewriteZipEntryFees(t, src, "xl/worksheets/sheet1.xml", func(b []byte) []byte {
		return b[:len(b)*6/10]
	})
	_, _, err := ReadFeeFile(corrupt)
	if err == nil {
		t.Fatal("ReadFeeFile(truncated sheet) error = nil, want wrapped workbook.ErrTruncatedSheet")
	}
	if !errors.Is(err, workbook.ErrTruncatedSheet) {
		t.Fatalf("ReadFeeFile(truncated sheet) error = %v, want wrapped workbook.ErrTruncatedSheet", err)
	}
	if err := errors.Unwrap(err); err == nil {
		t.Errorf("error must wrap (not merely mention) the sentinel: %v", err)
	}
	if !strings.Contains(err.Error(), "fees:") {
		t.Errorf("error text should carry the fees channel prefix: %v", err)
	}
}

// TestReadFeeFileAcceptsValidXLSXAfterGuard pins the no-false-positive side:
// the guard runs on every xlsx read now, so the pre-existing happy-path
// tests keep passing (TestReadFeeFileXLSX et al.) and this explicit one
// guards the multi-sheet case too.
func TestReadFeeFileAcceptsValidXLSXAfterGuard(t *testing.T) {
	dir := testsupport.TempDir(t)
	p := writeXLSX(t, dir, "valid.xlsx", [][]string{
		{"站址编码", "电费", "账期"},
		{"HLJ0001", "100", "2025-01"},
	}, map[string][][]string{"Sheet2": {{"x"}, {"y"}}})
	headers, rows, err := ReadFeeFile(p)
	if err != nil {
		t.Fatalf("ReadFeeFile(valid) = %v, want nil", err)
	}
	if len(headers) != 3 || len(rows) != 1 || rows[0].Number != 2 {
		t.Fatalf("headers=%v rows=%v, want 3 headers + 1 data row at Number 2", headers, rows)
	}
}

// ---- #98: the CSV reading channel's correctness defects ----

// writeFeeCSV writes a raw CSV with exact bytes (no trailing tweaks).
func writeFeeCSV(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestReadFeeFileCSVBOMTolerated (#98-B): Excel prepends a UTF-8 BOM to its
// CSV exports; the doc contract says "UTF-8 with BOM tolerated", but the
// BOM used to survive into the first header cell ("\ufeff站址编码"), the
// recognizer missed siteCode, and the file bounced out of auto-recognize.
func TestReadFeeFileCSVBOMTolerated(t *testing.T) {
	dir := testsupport.TempDir(t)
	body := []byte("站址编码,账期,电费(元)\nHLJ-0001,2024-05,100.00\n")
	src := writeFeeCSV(t, dir, "bom.csv", append([]byte{0xEF, 0xBB, 0xBF}, body...))

	headers, rows, err := ReadFeeFile(src)
	if err != nil {
		t.Fatalf("ReadFeeFile(BOM csv): %v", err)
	}
	if headers[0] != "站址编码" {
		t.Fatalf("headers[0] = %q, want the BOM stripped (\"站址编码\")", headers[0])
	}
	if len(rows) != 1 || rows[0].Cells[0] != "HLJ-0001" {
		t.Fatalf("rows = %+v, want the one data row intact", rows)
	}
	// and the recognizer sees the core columns through the BOM file
	typ, cols := Recognize(headers)
	if cols["siteCode"] != "站址编码" || cols["amount"] == "" {
		t.Fatalf("Recognize(BOM headers) = type %q cols %v, want siteCode/amount recognized", typ, cols)
	}
}

// TestReadFeeFileCSVThousandsReassembled (#98-A): the unquoted thousands
// separator is the classic real-world CSV defect — "1,234.56" splits into
// two cells, the amount column reads "1", and the batch used to import at
// 1/1000 of the truth with ZERO failures. The reader now reassembles the
// accounting grouping (1-3 digit head + exactly-3-digit tail) whenever the
// row count can equalize with the header; the amounts come back exact.
func TestReadFeeFileCSVThousandsReassembled(t *testing.T) {
	dir := testsupport.TempDir(t)
	src := writeFeeCSV(t, dir, "thousands.csv", []byte(
		"站址编码,账期,电费(元),电量(kWh)\n"+
			"HLJ-0001,2024-05,1,234.56,3200\n"+
			"HLJ-0002,2024-05,12,345.00,4100\n"+
			"HLJ-0003,2024-05,9,876.50,5300\n"+
			"HLJ-0004,2024-05,100.00,6000\n")) // plain row: no merge needed

	headers, rows, err := ReadFeeFile(src)
	if err != nil {
		t.Fatalf("ReadFeeFile(thousands csv): %v", err)
	}
	if len(headers) != 4 {
		t.Fatalf("headers = %v, want 4", headers)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(rows))
	}
	want := []string{"1234.56", "12345.00", "9876.50", "100.00"}
	for i, w := range want {
		if got := rows[i].Cells[2]; got != w {
			t.Errorf("row %d amount cell = %q, want the reassembled %q", i+2, got, w)
		}
	}
	// the merged amount parses to the TRUE cents (the old channel: 100)
	if cents, err := NormalizeAmount(rows[0].Cells[2]); err != nil || cents != 123456 {
		t.Errorf("row 2 amount = %d cents (err %v), want 123456 — no 1000x shrink", cents, err)
	}
}

// TestReadFeeFileCSVRaggedRefused (#98-A): a row with more cells than the
// header that CANNOT be reassembled as thousands groups must fail LOUD with
// the row number — misaligned data never reaches the analysis table.
func TestReadFeeFileCSVRaggedRefused(t *testing.T) {
	dir := testsupport.TempDir(t)
	src := writeFeeCSV(t, dir, "ragged.csv", []byte(
		"站址编码,账期,电费(元)\n"+
			"HLJ-0001,2024-05,100.00\n"+
			"HLJ-0002,2024-05,100.00,extra,garbage\n"))
	_, _, err := ReadFeeFile(src)
	if err == nil {
		t.Fatal("the unreassemblable ragged row must fail the read loudly")
	}
	if !strings.Contains(err.Error(), "第 3 行") || !strings.Contains(err.Error(), "5 列") {
		t.Fatalf("error = %v, want the row number and cell count as evidence", err)
	}
}

// TestReadFeeFileCSVShortRowsStillTolerated: rows SHORTER than the header
// keep the tolerant "" cells (a missing trailing optional column stays
// legal) — the #98 refusal is only for the OVER-count misalignment.
func TestReadFeeFileCSVShortRowsStillTolerated(t *testing.T) {
	dir := testsupport.TempDir(t)
	src := writeFeeCSV(t, dir, "short.csv", []byte(
		"站址编码,账期,电费(元),备注\n"+
			"HLJ-0001,2024-05,100.00\n"))
	headers, rows, err := ReadFeeFile(src)
	if err != nil {
		t.Fatalf("ReadFeeFile(short row): %v", err)
	}
	if len(headers) != 4 || len(rows) != 1 || len(rows[0].Cells) != 3 {
		t.Fatalf("headers=%v rows=%+v, want the short row imported as-is", headers, rows)
	}
}

// TestReassembleThousandsUnits pins the grouping rules directly: the tail
// is EXACTLY three digits (a ("100", "50") pair never merges), the fraction
// is at most two digits, and overshooting merges refuse.
func TestReassembleThousandsUnits(t *testing.T) {
	// ("1", "234.56") merges
	if out, ok := reassembleThousands([]string{"a", "1", "234.56", "b"}, 3); !ok || out[1] != "1234.56" {
		t.Fatalf("(1,234.56) = %v ok=%v", out, ok)
	}
	// ("100", "50") never merges: the tail must be exactly 3 digits
	if _, ok := reassembleThousands([]string{"x", "100", "50", "y"}, 3); ok {
		t.Fatal("(100,50) must not merge — a 2-digit tail is not a thousands group")
	}
	// "234.567" is not a tail: three fraction digits refuse
	if _, ok := reassembleThousands([]string{"x", "1", "234.567"}, 2); ok {
		t.Fatal("234.567 must not merge (fraction beyond cents)")
	}
	// no candidate pair → false even when the count could match
	if _, ok := reassembleThousands([]string{"a", "b", "c", "d"}, 3); ok {
		t.Fatal("non-numeric cells must never merge")
	}
	// multi-group number: 1,234,567.89 needs TWO merges
	if out, ok := reassembleThousands([]string{"c", "1", "234", "567.89"}, 2); !ok || out[1] != "1234567.89" {
		t.Fatalf("(1,234,567.89) = %v ok=%v", out, ok)
	}
}
