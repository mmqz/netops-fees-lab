package workbook

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-fees-lab/internal/testfixtures"
)

// generatedXLSX resolves a file under testdata/ relative to this package's
// directory (go test runs with the package source dir as CWD). The files
// are produced by `go run ./cmd/testdatagen -out testdata` and committed.
func generatedXLSX(name string) string {
	return filepath.Join("..", "..", "testdata", name)
}

// cellsOf flattens typed fixture rows into raw string rows.
func cellsOf[T interface{ Cells() []string }](rows []T) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = r.Cells()
	}
	return out
}

// assertGeneratedWorkbook checks the full contract between the generator
// (cmd/testdatagen) and the parser for one generated file: single sheet
// with the expected name, exact header row in order, one Values entry per
// header per row, real Excel row numbers (header = 1, data from 2), all
// values trimmed, no skipped rows, and SHA-256 equal to the hash of the
// raw file bytes.
func assertGeneratedWorkbook(t *testing.T, file, sheet string, headers []string, rows [][]string) {
	t.Helper()
	path := generatedXLSX(file)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)

	wb, err := Read(path)
	if err != nil {
		t.Fatalf("Read(%s): %v", path, err)
	}
	if wb.Name != file {
		t.Errorf("wb.Name = %q, want %q", wb.Name, file)
	}
	if want := hex.EncodeToString(sum[:]); wb.SHA256 != want {
		t.Errorf("wb.SHA256 = %q, want %q (hash of file bytes)", wb.SHA256, want)
	}

	if len(wb.Sheets) != 1 {
		t.Fatalf("len(wb.Sheets) = %d, want 1 (got %v)", len(wb.Sheets), sheetNames(wb))
	}
	s := wb.Sheets[0]
	if s.Name != sheet {
		t.Errorf("sheet name = %q, want %q", s.Name, sheet)
	}

	if len(s.Headers) != len(headers) {
		t.Fatalf("len(s.Headers) = %d, want %d (got %q)", len(s.Headers), len(headers), s.Headers)
	}
	for i := range headers {
		if s.Headers[i] != headers[i] {
			t.Errorf("s.Headers[%d] = %q, want %q", i, s.Headers[i], headers[i])
		}
	}

	if s.DataRows != len(rows) || len(s.Rows) != len(rows) {
		t.Fatalf("data rows = %d (len(Rows) = %d), want %d", s.DataRows, len(s.Rows), len(rows))
	}
	if s.SkippedEmptyRows != 0 {
		t.Errorf("s.SkippedEmptyRows = %d, want 0", s.SkippedEmptyRows)
	}

	for i, want := range rows {
		got := s.Rows[i]
		if got.Number != i+2 {
			t.Errorf("s.Rows[%d].Number = %d, want %d", i, got.Number, i+2)
		}
		if len(got.Values) != len(headers) {
			t.Errorf("s.Rows[%d]: len(Values) = %d, want %d", i, len(got.Values), len(headers))
		}
		for c := range headers {
			h := headers[c]
			v, ok := got.Values[h]
			if !ok {
				t.Errorf("s.Rows[%d]: Values is missing header key %q", i, h)
				continue
			}
			if v != want[c] {
				t.Errorf("s.Rows[%d].Values[%q] = %q, want %q", i, h, v, want[c])
			}
			if v != strings.TrimSpace(v) {
				t.Errorf("s.Rows[%d].Values[%q] = %q, not trimmed", i, h, v)
			}
		}
	}
}

func sheetNames(wb *Workbook) []string {
	names := make([]string, len(wb.Sheets))
	for i, s := range wb.Sheets {
		names[i] = s.Name
	}
	return names
}

func TestReadGeneratedDevicesWorkbook(t *testing.T) {
	assertGeneratedWorkbook(t, "devices.xlsx", testfixtures.DeviceSheetName,
		testfixtures.DeviceHeaders, cellsOf(testfixtures.DeviceRows))
}

func TestReadGeneratedIPPoolWorkbook(t *testing.T) {
	assertGeneratedWorkbook(t, "ippools.xlsx", testfixtures.IPPoolSheetName,
		testfixtures.IPPoolHeaders, cellsOf(testfixtures.IPPoolRows))
}

func TestReadGeneratedCircuitWorkbook(t *testing.T) {
	assertGeneratedWorkbook(t, "circuits.xlsx", testfixtures.CircuitSheetName,
		testfixtures.CircuitHeaders, cellsOf(testfixtures.CircuitRows))
}

// --- BuildSheet unit tests (pure function, synthetic raw input) ---

func TestBuildSheetSkipsInteriorEmptyRows(t *testing.T) {
	s := BuildSheet("S", [][]string{
		{"h1", "h2"},
		{"a", "1"},
		{},
		{"b", "2"},
	})
	if len(s.Rows) != 2 || s.DataRows != 2 {
		t.Fatalf("rows = %d/%d, want 2/2", len(s.Rows), s.DataRows)
	}
	if s.SkippedEmptyRows != 1 {
		t.Errorf("SkippedEmptyRows = %d, want 1", s.SkippedEmptyRows)
	}
	// Row numbers must stay real Excel row numbers, gaps included.
	if s.Rows[0].Number != 2 || s.Rows[1].Number != 4 {
		t.Errorf("row numbers = %d,%d, want 2,4", s.Rows[0].Number, s.Rows[1].Number)
	}
}

func TestBuildSheetSkipsWhitespaceOnlyRows(t *testing.T) {
	s := BuildSheet("S", [][]string{
		{"h1", "h2"},
		{"  ", "\t"},
		{"a", "1"},
	})
	if len(s.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (whitespace-only row is empty)", len(s.Rows))
	}
	if s.SkippedEmptyRows != 1 {
		t.Errorf("SkippedEmptyRows = %d, want 1", s.SkippedEmptyRows)
	}
}

func TestBuildSheetTrimsTrailingEmptyRows(t *testing.T) {
	s := BuildSheet("S", [][]string{
		{"h1", "h2"},
		{"a", "1"},
		{},
		{""},
		{" "},
	})
	if len(s.Rows) != 1 || s.DataRows != 1 {
		t.Fatalf("rows = %d/%d, want 1/1", len(s.Rows), s.DataRows)
	}
	// Trailing empty rows are trimmed, not counted as skipped.
	if s.SkippedEmptyRows != 0 {
		t.Errorf("SkippedEmptyRows = %d, want 0", s.SkippedEmptyRows)
	}
}

func TestBuildSheetTrimsHeaderCells(t *testing.T) {
	s := BuildSheet("S", [][]string{
		{" h1 ", "h2", "h3"},
		{"a", "b", "c"},
	})
	want := []string{"h1", "h2", "h3"}
	if len(s.Headers) != len(want) {
		t.Fatalf("Headers = %q, want %q", s.Headers, want)
	}
	for i := range want {
		if s.Headers[i] != want[i] {
			t.Errorf("Headers[%d] = %q, want %q", i, s.Headers[i], want[i])
		}
	}
	if v := s.Rows[0].Values[" h1 "]; v != "" {
		t.Errorf("Values must be keyed by the trimmed header, got %q under the raw header", v)
	}
	if v := s.Rows[0].Values["h1"]; v != "a" {
		t.Errorf("Values[h1] = %q, want %q", v, "a")
	}
}

func TestBuildSheetRemovesTrailingEmptyHeaders(t *testing.T) {
	s := BuildSheet("S", [][]string{
		{"h1", "h2", "", "  "},
		{"a", "b"},
	})
	want := []string{"h1", "h2"}
	if len(s.Headers) != len(want) {
		t.Fatalf("Headers = %q, want %q", s.Headers, want)
	}
	for i := range want {
		if s.Headers[i] != want[i] {
			t.Errorf("Headers[%d] = %q, want %q", i, s.Headers[i], want[i])
		}
	}
	if len(s.Rows[0].Values) != len(want) {
		t.Errorf("len(Values) = %d, want %d", len(s.Rows[0].Values), len(want))
	}
}

func TestBuildSheetIgnoresCellsBeyondHeader(t *testing.T) {
	s := BuildSheet("S", [][]string{
		{"h1", "h2"},
		{"a", "b", "junk", "more junk"},
	})
	if len(s.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(s.Rows))
	}
	if len(s.Rows[0].Values) != 2 {
		t.Fatalf("len(Values) = %d, want 2", len(s.Rows[0].Values))
	}
	want := map[string]string{"h1": "a", "h2": "b"}
	for k, v := range s.Rows[0].Values {
		if want[k] != v {
			t.Errorf("Values[%q] = %q, want %q", k, v, want[k])
		}
	}
}

func TestBuildSheetTrimsValuesAndFillsMissingCells(t *testing.T) {
	s := BuildSheet("S", [][]string{
		{"h1", "h2", "h3"},
		{"  a ", "b"}, // third cell entirely missing
	})
	if len(s.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(s.Rows))
	}
	v := s.Rows[0].Values
	if v["h1"] != "a" {
		t.Errorf("Values[h1] = %q, want %q", v["h1"], "a")
	}
	if v["h2"] != "b" {
		t.Errorf("Values[h2] = %q, want %q", v["h2"], "b")
	}
	if v["h3"] != "" {
		t.Errorf("Values[h3] = %q, want empty", v["h3"])
	}
}

func TestBuildSheetEmptyInput(t *testing.T) {
	s := BuildSheet("S", nil)
	if s.Name != "S" || s.Headers != nil || s.Rows != nil || s.DataRows != 0 || s.SkippedEmptyRows != 0 {
		t.Errorf("BuildSheet(nil) = %+v, want zero sheet named S", s)
	}
	s = BuildSheet("S", [][]string{{"h1"}})
	if s.DataRows != 0 || len(s.Headers) != 1 {
		t.Errorf("BuildSheet(header-only) = %+v, want 0 data rows", s)
	}
}

// --- multi-sheet parsing ---

func TestMultiSheetWorkbook(t *testing.T) {
	const sheetA = "设备清单"
	const sheetB = "地址池"

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if _, err := f.NewSheet(sheetB); err != nil {
		t.Fatalf("NewSheet(%q): %v", sheetB, err)
	}
	if err := f.SetSheetName("Sheet1", sheetA); err != nil {
		t.Fatalf("SetSheetName: %v", err)
	}

	// Sheet A: full device header + one data row.
	for c, h := range testfixtures.DeviceHeaders {
		cell, err := excelize.CoordinatesToCellName(c+1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStr(sheetA, cell, h); err != nil {
			t.Fatalf("SetCellStr %s: %v", cell, err)
		}
	}
	for c, v := range testfixtures.DeviceRows[0].Cells() {
		cell, err := excelize.CoordinatesToCellName(c+1, 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStr(sheetA, cell, v); err != nil {
			t.Fatalf("SetCellStr %s: %v", cell, err)
		}
	}

	// Sheet B: full pool header + one data row (with trailing empty cell,
	// like the W001 orphan seed).
	for c, h := range testfixtures.IPPoolHeaders {
		cell, err := excelize.CoordinatesToCellName(c+1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStr(sheetB, cell, h); err != nil {
			t.Fatalf("SetCellStr %s: %v", cell, err)
		}
	}
	for c, v := range testfixtures.IPPoolRows[0].Cells() {
		cell, err := excelize.CoordinatesToCellName(c+1, 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.SetCellStr(sheetB, cell, v); err != nil {
			t.Fatalf("SetCellStr %s: %v", cell, err)
		}
	}

	path := filepath.Join(t.TempDir(), "multi.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}

	wb, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(wb.Sheets) != 2 {
		t.Fatalf("len(wb.Sheets) = %d, want 2 (got %q)", len(wb.Sheets), sheetNames(wb))
	}
	if wb.Sheets[0].Name != sheetA || wb.Sheets[1].Name != sheetB {
		t.Fatalf("sheet names = %q, want [%q %q]", sheetNames(wb), sheetA, sheetB)
	}

	byName := wb.SheetsByName()
	sa, ok := byName[sheetA]
	if !ok {
		t.Fatalf("SheetsByName is missing %q", sheetA)
	}
	sb, ok := byName[sheetB]
	if !ok {
		t.Fatalf("SheetsByName is missing %q", sheetB)
	}
	if sa.DataRows != 1 || sb.DataRows != 1 {
		t.Errorf("data rows = %d/%d, want 1/1", sa.DataRows, sb.DataRows)
	}
	if len(sa.Headers) != len(testfixtures.DeviceHeaders) {
		t.Errorf("sheet A headers = %q, want %q", sa.Headers, testfixtures.DeviceHeaders)
	}
	if len(sb.Headers) != len(testfixtures.IPPoolHeaders) {
		t.Errorf("sheet B headers = %q, want %q", sb.Headers, testfixtures.IPPoolHeaders)
	}
	if got := sa.Rows[0].Values[testfixtures.DeviceHeaders[0]]; got != testfixtures.DeviceRows[0].DeviceCode {
		t.Errorf("sheet A row 2 设备编码 = %q, want %q", got, testfixtures.DeviceRows[0].DeviceCode)
	}
	if got := sb.Rows[0].Values[testfixtures.IPPoolHeaders[0]]; got != testfixtures.IPPoolRows[0].PoolName {
		t.Errorf("sheet B row 2 地址池名称 = %q, want %q", got, testfixtures.IPPoolRows[0].PoolName)
	}
}

// --- error paths ---

func TestReadRejectsNonXLSX(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bogus.xlsx")
	if err := os.WriteFile(path, []byte("this is not a zip/xlsx file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrNotXLSX) {
		t.Errorf("Read(non-xlsx) error = %v, want wrapped ErrNotXLSX", err)
	}
}

func TestReadRejectsWorkbookWithoutData(t *testing.T) {
	f := excelize.NewFile()
	if err := f.SetCellStr("Sheet1", "A1", "设备编码"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "header-only.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); !errors.Is(err, ErrEmptyWorkbook) {
		t.Errorf("Read(header-only) error = %v, want wrapped ErrEmptyWorkbook", err)
	}
}

func TestReadMissingFile(t *testing.T) {
	_, err := Read(filepath.Join(t.TempDir(), "does-not-exist.xlsx"))
	if err == nil || errors.Is(err, ErrNotXLSX) || errors.Is(err, ErrEmptyWorkbook) {
		t.Errorf("Read(missing file) error = %v, want a plain read error", err)
	}
}

// --- parse limits (zip-bomb guard, R2-P2-2) ---

// setLimits lowers the parse limits for this test and restores the
// production defaults afterwards.
func setLimits(t *testing.T, rows, cells int) {
	t.Helper()
	oldRows, oldCells := MaxRowsPerSheet, MaxCellsPerWorkbook
	MaxRowsPerSheet, MaxCellsPerWorkbook = rows, cells
	t.Cleanup(func() {
		MaxRowsPerSheet, MaxCellsPerWorkbook = oldRows, oldCells
	})
}

// writeSheetWorkbook saves an xlsx whose sheets (in order) have the given
// row counts, every row carrying cols filled cells (row 1 is the header).
func writeSheetWorkbook(t *testing.T, cols int, sheetRows ...int) string {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	for si, n := range sheetRows {
		sheet := "Sheet1"
		if si > 0 {
			name := fmt.Sprintf("Sheet%d", si+1)
			if _, err := f.NewSheet(name); err != nil {
				t.Fatalf("NewSheet(%q): %v", name, err)
			}
			sheet = name
		}
		for r := 1; r <= n; r++ {
			values := make([]string, cols)
			for c := range values {
				values[c] = fmt.Sprintf("s%dr%dc%d", si, r, c)
			}
			if err := f.SetSheetRow(sheet, fmt.Sprintf("A%d", r), &values); err != nil {
				t.Fatalf("SetSheetRow(%q, A%d): %v", sheet, r, err)
			}
		}
	}
	path := filepath.Join(t.TempDir(), "sized.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return path
}

// TestReadRejectsWorkbookExceedingRowLimit lowers MaxRowsPerSheet and
// asserts that a workbook with more worksheet rows is rejected through the
// ErrWorkbookTooLarge error chain (R2-P2-2).
func TestReadRejectsWorkbookExceedingRowLimit(t *testing.T) {
	setLimits(t, 10, 1_000_000)
	// 12 rows > limit 10 — the abort must fire while streaming, before
	// the whole sheet is materialized.
	path := writeSheetWorkbook(t, 2, 12)
	_, err := Read(path)
	if !errors.Is(err, ErrWorkbookTooLarge) {
		t.Fatalf("Read(12 rows, limit 10) error = %v, want wrapped ErrWorkbookTooLarge", err)
	}
	if err != nil && (errors.Is(err, ErrNotXLSX) || errors.Is(err, ErrEmptyWorkbook)) {
		t.Errorf("row-limit error also matches another sentinel: %v", err)
	}
	// Within the limit the same workbook parses fine.
	setLimits(t, 20, 1_000_000)
	wb, err := Read(writeSheetWorkbook(t, 2, 12))
	if err != nil {
		t.Fatalf("Read(12 rows, limit 20): %v", err)
	}
	if wb.Sheets[0].DataRows != 11 {
		t.Errorf("data rows = %d, want 11 (12 rows incl. header)", wb.Sheets[0].DataRows)
	}
}

// TestReadRejectsWorkbookExceedingCellLimit lowers MaxCellsPerWorkbook and
// asserts the limit accumulates ACROSS sheets: each sheet alone stays under
// it, only the sum breaches and aborts with ErrWorkbookTooLarge.
func TestReadRejectsWorkbookExceedingCellLimit(t *testing.T) {
	setLimits(t, 1_000_000, 20)
	// One sheet of 5 rows × 3 cols = 15 cells ≤ 20: parses fine.
	if _, err := Read(writeSheetWorkbook(t, 3, 5)); err != nil {
		t.Fatalf("Read(single sheet, 15 cells, limit 20): %v", err)
	}
	// Two sheets (15 + 12 = 27 cells > 20): rejected while reading the
	// second sheet — the workbook-wide counter is what fires.
	_, err := Read(writeSheetWorkbook(t, 3, 5, 4))
	if !errors.Is(err, ErrWorkbookTooLarge) {
		t.Fatalf("Read(two sheets, 27 cells, limit 20) error = %v, want wrapped ErrWorkbookTooLarge", err)
	}
}

// --- honest import guards (#81) ---

// rewriteZipEntry rewrites one entry of an xlsx zip into a new file with the
// entry's bytes transformed, recompressing only the touched entry (raw copy
// for the rest). Test helper for corruption scenarios.
func rewriteZipEntry(t *testing.T, src, name string, transform func([]byte) []byte) string {
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

// TestReadRejectsTruncatedSheetXML is the #81 core scenario: a valid xlsx
// whose sheet stream is CUT mid-file (bit rot, interrupted copy, manual
// tampering). excelize v2.11's iterator swallows mid-stream decode errors
// (Next/Columns discard xml.Decoder errors; Error() only reports ErrMaxRows),
// so before the tail guard this file imported as a silent row prefix with a
// nil error — the caller could not tell "3 data rows" from "file cut after
// row 4".
func TestReadRejectsTruncatedSheetXML(t *testing.T) {
	src := writeSheetWorkbook(t, 3, 4) // header + 3 data rows
	corrupt := rewriteZipEntry(t, src, "xl/worksheets/sheet1.xml", func(b []byte) []byte {
		// cut mid-row well before the closing tags
		return b[:len(b)*6/10]
	})
	_, err := Read(corrupt)
	if !errors.Is(err, ErrTruncatedSheet) {
		t.Fatalf("Read(truncated sheet) error = %v, want wrapped ErrTruncatedSheet", err)
	}
}

// TestReadRejectsGarbageTailedSheet covers the corruption variant where the
// sheet stream keeps its length but the tail is garbage — no close tag.
func TestReadRejectsGarbageTailedSheet(t *testing.T) {
	src := writeSheetWorkbook(t, 3, 4)
	corrupt := rewriteZipEntry(t, src, "xl/worksheets/sheet1.xml", func(b []byte) []byte {
		return append(b[:len(b)-64], 'x') // valid prefix + junk tail, no </worksheet>
	})
	_, err := Read(corrupt)
	if !errors.Is(err, ErrTruncatedSheet) {
		t.Fatalf("Read(garbage-tailed sheet) error = %v, want wrapped ErrTruncatedSheet", err)
	}
}

// TestReadRejectsCorruptSharedStrings exercises the Rows.Columns error path:
// a corrupt sharedStrings table surfaces through sharedStringsReader inside
// Columns (excelize rows.go:165) — before #81, sheetRows broke out of the
// loop and discarded that error, importing a silent prefix.
func TestReadRejectsCorruptSharedStrings(t *testing.T) {
	src := writeSheetWorkbook(t, 3, 4)
	corrupt := rewriteZipEntry(t, src, "xl/sharedStrings.xml", func(b []byte) []byte {
		return []byte("<sst unclosed...")
	})
	_, err := Read(corrupt)
	if err == nil {
		t.Fatal("Read(corrupt sharedStrings) error = nil, want the Columns error to propagate")
	}
	if errors.Is(err, ErrTruncatedSheet) {
		t.Errorf("corrupt sharedStrings misreported as truncation: %v", err)
	}
}

// TestCheckWorksheetTailsAcceptsValidFile pins the no-false-positive side of
// the guard: every legitimate file produced by a real OOXML writer passes.
func TestCheckWorksheetTailsAcceptsValidFile(t *testing.T) {
	data, err := os.ReadFile(writeSheetWorkbook(t, 3, 4, 5))
	if err != nil {
		t.Fatalf("read workbook: %v", err)
	}
	if err := CheckWorksheetTails(data); err != nil {
		t.Fatalf("CheckWorksheetTails(valid file) = %v, want nil", err)
	}
}
