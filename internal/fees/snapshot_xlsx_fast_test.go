package fees

// snapshot_xlsx_fast_test.go — the equivalence pins of the #39 fast xlsx
// row reader: for every shape the operator's real ledgers take, the
// byte-scanning two-row path must answer EXACTLY what the original
// excelize walk answers — and must SAY when it cannot (the bail), with
// the walk still answering the same bytes.
//
// The shapes pinned here (each one born from an actual measurement of
// what excelize v2.11.0 renders — the experiment recorded in
// snapshot_xlsx_fast.go's header):
//   - shared strings, styled and unstyled (numFmtId 0);
//   - numbers through the isNumeric normalization ("123.45" -> "123.45",
//     0.1 -> "0.1", 42 -> "42") and number-LOOKING strings that stay
//     verbatim ("0123.00", "0.300");
//   - booleans (t="b": TRUE/FALSE);
//   - inline strings (SetCellDefault's channel);
//   - formula cells (extend the row, render "");
//   - empty middle cells (pad "") and trailing empties (truncate);
//   - whitespace-only leading rows (header = first NON-BLANK row);
//   - date-styled numerics (numFmtId 14): the fast path MUST bail and the
//     walk must still answer — proving the bail is a routing decision,
//     never a fidelity loss.

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"github.com/xuri/excelize/v2"
)

// fastVsAnchor asserts the fast path and the excelize walk agree, byte
// for byte, on every requested row — and that the fast path was actually
// taken (taken=false expects the bail: the date-styled shape).
func fastVsAnchor(t *testing.T, path string, rows []int, taken bool) {
	t.Helper()
	for _, rn := range rows {
		fast, handled, ferr := readSnapshotRowXLSXFast(path, rn)
		if ferr != nil && handled {
			t.Fatalf("row %d: fast path answered an error it claims to handle: %v", rn, ferr)
		}
		if handled != taken {
			t.Fatalf("row %d: fast path handled=%v, want %v (bail contract)", rn, handled, taken)
		}
		if !handled {
			continue // the bail: dispatch falls to the walk; equivalence holds by construction
		}
		anchor, aerr := readSnapshotRowXLSXExcelize(path, rn)
		if aerr != nil {
			t.Fatalf("row %d: anchor walk errored: %v", rn, aerr)
		}
		if len(fast.Header) != len(anchor.Header) {
			t.Fatalf("row %d: header width fast=%d anchor=%d\nfast:   %v\nanchor: %v", rn, len(fast.Header), len(anchor.Header), fast.Header, anchor.Header)
		}
		for i := range anchor.Header {
			if fast.Header[i] != anchor.Header[i] {
				t.Fatalf("row %d header[%d]: fast=%q anchor=%q", rn, i, fast.Header[i], anchor.Header[i])
			}
		}
		if len(fast.Cells) != len(anchor.Cells) {
			t.Fatalf("row %d: cells width fast=%d anchor=%d\nfast:   %v\nanchor: %v", rn, len(fast.Cells), len(anchor.Cells), fast.Cells, anchor.Cells)
		}
		for i := range anchor.Cells {
			if fast.Cells[i] != anchor.Cells[i] {
				t.Fatalf("row %d cell[%d]: fast=%q anchor=%q", rn, i, fast.Cells[i], anchor.Cells[i])
			}
		}
	}
}

func buildShapeWorkbook(t *testing.T, dir, name string) string {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	bold, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		t.Fatalf("NewStyle: %v", err)
	}
	// row 1: styled header (shared strings, numFmtId 0)
	_ = f.SetSheetRow(sheet, "A1", &[]any{"站址编码", "账期", "电费(元)", "折扣生效日期"})
	_ = f.SetCellStyle(sheet, "A1", "D1", bold)
	// row 2: styled shared strings + styled + unstyled numbers
	_ = f.SetSheetRow(sheet, "A2", &[]any{"SITE-0001", "2026-09", "0123.00", "2017-01-10"})
	_ = f.SetCellStyle(sheet, "A2", "B2", bold)
	_ = f.SetSheetRow(sheet, "A3", &[]any{123.45, 0.10, 42, 12345.678})
	_ = f.SetCellStyle(sheet, "A3", "A3", bold)
	// row 4: booleans + a number-looking string
	_ = f.SetSheetRow(sheet, "A4", &[]any{true, false, "0.300", 1e10})
	// row 5: inline strings (SetCellDefault), empty middle, trailing empty
	_ = f.SetCellDefault(sheet, "A5", "inline-甲")
	_ = f.SetCellDefault(sheet, "B5", "inline-乙")
	_ = f.SetCellDefault(sheet, "D5", "inline-丁")
	// row 6: formula cell (extends the row, renders "")
	if err := f.SetCellFormula(sheet, "A6", "SUM(A3:D3)"); err != nil {
		t.Fatalf("SetCellFormula: %v", err)
	}
	_ = f.SetCellStr(sheet, "B6", "after-formula")
	p := filepath.Join(dir, name)
	if err := f.SaveAs(p); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return p
}

func buildDateStyledWorkbook(t *testing.T, dir, name string) string {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	dateStyle, err := f.NewStyle(&excelize.Style{NumFmt: 14})
	if err != nil {
		t.Fatalf("NewStyle(date): %v", err)
	}
	_ = f.SetSheetRow(sheet, "A1", &[]any{"站址编码", "生效日期", "备注"})
	_ = f.SetCellStyle(sheet, "B1", "B100", dateStyle)
	_ = f.SetCellInt(sheet, "B2", 46235) // a date serial under the numFmt 14 style
	_ = f.SetCellStr(sheet, "A2", "SITE-0002")
	_ = f.SetCellStr(sheet, "C2", "styled-date-shape")
	p := filepath.Join(dir, name)
	if err := f.SaveAs(p); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return p
}

func buildLeadingBlankWorkbook(t *testing.T, dir, name string) string {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	_ = f.SetSheetRow(sheet, "A1", &[]any{"", "", ""}) // blank row 1 (empty strings)
	_ = f.SetCellStr(sheet, "A2", "  ")                // whitespace-only row 2
	_ = f.SetSheetRow(sheet, "A3", &[]any{"站址编码", "账期", "电费(元)"})
	_ = f.SetSheetRow(sheet, "A4", &[]any{"SITE-0003", "2026-09", "88.00"})
	p := filepath.Join(dir, name)
	if err := f.SaveAs(p); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return p
}

func TestFastXLSXRowReaderEquivalence(t *testing.T) {
	dir := testsupport.TempDir(t)
	p := buildShapeWorkbook(t, dir, "shapes.xlsx")
	fastVsAnchor(t, p, []int{1, 2, 3, 4, 5, 6}, true)

	// The dispatch itself answers the same bytes (the contract callers see).
	for _, rn := range []int{1, 2, 3, 4, 5, 6} {
		viaDispatch, derr := ReadSnapshotRow(p, rn)
		anchor, aerr := readSnapshotRowXLSXExcelize(p, rn)
		if derr != nil || aerr != nil {
			t.Fatalf("row %d: dispatch=%v anchor=%v", rn, derr, aerr)
		}
		if len(viaDispatch.Cells) != len(anchor.Cells) || len(viaDispatch.Header) != len(anchor.Header) {
			t.Fatalf("row %d: dispatch shape drifted", rn)
		}
		for i := range anchor.Cells {
			if viaDispatch.Cells[i] != anchor.Cells[i] {
				t.Fatalf("row %d cell %d: dispatch=%q anchor=%q", rn, i, viaDispatch.Cells[i], anchor.Cells[i])
			}
		}
	}

	// The date-styled shape: the fast path MUST bail (numFmt 14) and the
	// dispatch must still answer the walk's bytes.
	dp := buildDateStyledWorkbook(t, dir, "date-styled.xlsx")
	fastVsAnchor(t, dp, []int{1, 2}, false)
	d2, derr := ReadSnapshotRow(dp, 2)
	if derr != nil {
		t.Fatalf("date-styled row 2 via dispatch: %v", derr)
	}
	if len(d2.Cells) != 3 || d2.Cells[0] != "SITE-0002" || d2.Cells[2] != "styled-date-shape" {
		t.Fatalf("date-styled row 2 = %v", d2.Cells)
	}
	if d2.Cells[1] == "" {
		t.Fatalf("date-styled row 2 rendered an empty date cell: %v", d2.Cells)
	}
	t.Logf("date-styled row 2 renders: %v (numFmt 14 machinery owns the date cell — the walk answered it)", d2.Cells)

	// The leading-blank shape: header = first NON-BLANK row, both paths.
	bp := buildLeadingBlankWorkbook(t, dir, "leading-blank.xlsx")
	fastVsAnchor(t, bp, []int{1, 2, 3, 4}, true)
	hb, err := ReadSnapshotRow(bp, 4)
	if err != nil {
		t.Fatalf("leading-blank row 4: %v", err)
	}
	if len(hb.Header) != 3 || hb.Header[0] != "站址编码" {
		t.Fatalf("leading-blank header = %v, want row 3's cells", hb.Header)
	}

	// The #86 vertical slice through the FAST path: the service answers
	// the same evidence as before (the wide-sheet fidelity pin in
	// snapshot_xlsx_test.go covers the byte-for-byte corpus).
	svc, _, sdir := newService(t)
	src := filepath.Join(sdir, "fast-evidence.xlsx")
	rows := [][]any{
		{"站址编码", "账期", "电费(元)"},
		{"0230101ABCDEFG01", "2026-10", "1234.56"},
	}
	writeFeeXLSX(t, sdir, "fast-evidence.xlsx", rows)
	_ = src
	sum, ierr := svc.Import(context.Background(), ImportOptions{
		Path: src, FeeType: FeeTypeElectricity, Year: 2026, Month: 10,
	})
	if ierr != nil {
		t.Fatalf("import: %v", ierr)
	}
	ev, eerr := svc.EvidenceRow(context.Background(), sum.BatchID, 2)
	if eerr != nil {
		t.Fatalf("EvidenceRow(2): %v", eerr)
	}
	if ev.Cells[0] != "0230101ABCDEFG01" || ev.Cells[2] != "1234.56" {
		t.Fatalf("fast evidence cells = %v", ev.Cells)
	}
	// a row past the end keeps the typed not-found
	if _, err := svc.EvidenceRow(context.Background(), sum.BatchID, 99); err == nil {
		t.Fatalf("row 99 must answer not-found")
	}
}

// TestFastXLSXRowReaderScanGuards pins the scan's boundary contracts on
// synthetic XML: rows without the r attribute (sequential numbering) and
// the row-past-the-end error shape.
func TestFastXLSXRowReaderScanGuards(t *testing.T) {
	dir := testsupport.TempDir(t)
	p := filepath.Join(dir, "no-r-attr.xlsx")
	// excelize always writes r= attrs; synthesize a minimal r-less file to
	// pin the sequential fallback (some third-party generators omit it).
	body := `<?xml version="1.0" encoding="UTF-8"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>
<row><c t="s"><v>0</v></c><c t="s"><v>1</v></c></row>
<row><c t="s"><v>2</v></c><c><v>42</v></c></row>
</sheetData></worksheet>`
	writeMinimalXLSX(t, p, []string{"站址编码", "账期", "SITE-0004"}, body)

	ev, err := ReadSnapshotRow(p, 2)
	if err != nil {
		t.Fatalf("r-less row 2: %v", err)
	}
	if len(ev.Cells) != 2 || ev.Cells[0] != "SITE-0004" || ev.Cells[1] != "42" {
		t.Fatalf("r-less row 2 cells = %v", ev.Cells)
	}
	if len(ev.Header) != 2 || ev.Header[0] != "站址编码" {
		t.Fatalf("r-less header = %v", ev.Header)
	}
	if _, err := ReadSnapshotRow(p, 3); err == nil {
		t.Fatalf("row 3 past the end must answer the typed not-found, got %v", err)
	} else if !strings.Contains(err.Error(), "第 3 行不存在") {
		t.Fatalf("row 3 past the end error = %v, want the typed not-found text", err)
	}
}

// writeMinimalXLSX packs a valid minimal workbook whose first sheet is the
// given body XML — the r-attr-less generator shape excelize itself never
// writes, so the pin has to synthesize it.
func writeMinimalXLSX(t *testing.T, path string, sst []string, sheetBody string) {
	t.Helper()
	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationship+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/></Types>`
	rootRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`
	workbook := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`
	wbRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/></Relationships>`
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="`)
	sb.WriteString(strings.Repeat("0", 0))
	sb.WriteString(`">`)
	for _, item := range sst {
		sb.WriteString("<si><t>")
		sb.WriteString(item)
		sb.WriteString("</t></si>")
	}
	sb.WriteString(`</sst>`)
	styles := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="1"><font/></fonts><fills count="1"><fill/></fills><borders count="1"><border/></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/></cellXfs></styleSheet>`

	out, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	files := map[string]string{
		"[Content_Types].xml":        contentTypes,
		"_rels/.rels":                rootRels,
		"xl/workbook.xml":            workbook,
		"xl/_rels/workbook.xml.rels": wbRels,
		"xl/sharedStrings.xml":       sb.String(),
		"xl/styles.xml":              styles,
		"xl/worksheets/sheet1.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + sheetBody,
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/sharedStrings.xml", "xl/styles.xml", "xl/worksheets/sheet1.xml"} {
		w, werr := zw.Create(name)
		if werr != nil {
			t.Fatalf("zip create %s: %v", name, werr)
		}
		if _, werr := w.Write([]byte(files[name])); werr != nil {
			t.Fatalf("zip write %s: %v", name, werr)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
}
