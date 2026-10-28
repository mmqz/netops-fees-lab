package fees

// The XLSX half of the evidence channel: real ledgers arrive as .xlsx
// workbooks (#125's whole motivation), so the xlsx read path of
// ReadSnapshotRow is the production path of EvidenceRow — and the one the
// CSV-only test corpus never touched. The pin walks the REAL vertical
// slice: import an .xlsx fee workbook → EvidenceRow answers the header row
// and a data row's cells exactly as the operator's source file shows
// them. (#39 split the xlsx face into the byte-scanning fast reader + the
// excelize walk bail target — TestFastXLSXRowReaderEquivalence pins their
// equivalence; this file pins the walk's own fidelity corpus, which both
// paths must answer.)

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func writeFeeXLSX(t *testing.T, dir, name string, rows [][]any) string {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	for i, row := range rows {
		if err := f.SetSheetRow(sheet, cellAxis(i+1), &row); err != nil {
			t.Fatalf("SetSheetRow %d: %v", i+1, err)
		}
	}
	p := filepath.Join(dir, name)
	if err := f.SaveAs(p); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return p
}

func cellAxis(row int) string {
	return "A" + string(rune('0'+row)) // A1..A9 — the corpus stays under 10 rows
}

func TestEvidenceRowReadsXLSXSnapshot(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()

	rows := [][]any{
		{"站址编码", "账期", "电费(元)"},
		{"0230101ABCDEFG01", "2026-10", "1234.56"},
		{"0230101ABCDEFG02", "2026-10", "5678.90"},
	}
	src := writeFeeXLSX(t, dir, "oct-ledger.xlsx", rows)

	sum, err := svc.Import(ctx, ImportOptions{
		Path: src, FeeType: FeeTypeElectricity, Year: 2026, Month: 10,
	})
	if err != nil {
		t.Fatalf("import xlsx: %v", err)
	}
	if sum.RowsImported != 2 {
		t.Fatalf("rows imported = %d, want 2", sum.RowsImported)
	}

	// Header row (row 1 = the source file's own numbering).
	head, err := svc.EvidenceRow(ctx, sum.BatchID, 1)
	if err != nil {
		t.Fatalf("EvidenceRow(1): %v", err)
	}
	if head.Header == nil || len(head.Cells) != 3 || head.Cells[0] != "站址编码" {
		t.Fatalf("header row evidence = %+v, want the xlsx header cells", head)
	}

	// A data row: cells verbatim, header attached.
	row, err := svc.EvidenceRow(ctx, sum.BatchID, 2)
	if err != nil {
		t.Fatalf("EvidenceRow(2): %v", err)
	}
	if row.Cells[0] != "0230101ABCDEFG01" || row.Cells[1] != "2026-10" || row.Cells[2] != "1234.56" {
		t.Fatalf("row 2 cells = %v, want the ledger row verbatim", row.Cells)
	}
	if len(row.Header) != 3 {
		t.Fatalf("row 2 header = %v, want the header attached to every data row", row.Header)
	}

	// A row past the end answers the typed not-found (the CSV reader's
	// contract, held by the XLSX reader too).
	if _, err := svc.EvidenceRow(ctx, sum.BatchID, 99); err == nil {
		t.Fatalf("row 99 = nil error, want not-found")
	}
}
