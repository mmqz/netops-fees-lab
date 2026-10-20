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
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/workbook"
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
	dir := t.TempDir()
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
	dir := t.TempDir()
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
	dir := t.TempDir()
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
	dst := filepath.Join(t.TempDir(), "rewritten.xlsx")
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
	dir := t.TempDir()
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
	dir := t.TempDir()
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
