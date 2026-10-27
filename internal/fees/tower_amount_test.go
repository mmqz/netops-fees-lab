package fees

// tower_amount_test.go — issue #29's regression net: the tower settlement
// detail (塔类产品服务费结算详单) carries several 产品服务费合计 variants
// plus 罚责赠费合计 behind multi-line xlsx headers, and the generic
// contains-fallback used to grab the FIRST 费用-bearing header — on the
// real 157-column sheet that was an all-zero variant column: 454 rows
// imported, totalCents all 0, zero warnings. These tests pin the three
// fixes: header normalization (line breaks fold), the type-scoped exact
// 口径 table, and the stop-the-bleed all-zero import warning.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// TestHeaderMatchKeyFoldsLineBreaks pins the #29 normalization: a cell
// wrapped header (Excel's Alt+Enter) folds to its single-line key, and a
// CRLF export folds identically. Without this the scoped table's exact
// match could never see the settlement's own header fingerprint.
func TestHeaderMatchKeyFoldsLineBreaks(t *testing.T) {
	cases := []struct {
		a, b string
		same bool
	}{
		{"产品服务费合计\n（出账费用）（不含税）", "产品服务费合计（出账费用）（不含税）", true},
		{"产品服务费合计\r\n（出账费用）（不含税）", "产品服务费合计（出账费用）（不含税）", true},
		{"产品服务费合计\t（出账费用）（不含税）", "产品服务费合计（出账费用）（不含税）", true},
		{"产品服务费合计（出账费用）（不含税）", "产品服务费合计(出账费用)(不含税)", false},
		{"Site\nCode", "sitecode", true},
	}
	for _, c := range cases {
		if got := headerMatchKey(c.a) == headerMatchKey(c.b); got != c.same {
			t.Errorf("headerMatchKey(%q) == headerMatchKey(%q) = %v, want %v", c.a, c.b, got, c.same)
		}
	}
}

// TestRecognizeAmountForTypeTowerSettlement pins the scoped 口径 table:
// exact normalized-key equality in 口径 priority (出账费用（不含税） first),
// the all-zero DECOY columns (罚责赠费合计 / 历史待退回) never win the amount
// slot, the half-width paren form matches its own entry, and the
// non-tower types carry no scoped table at all.
func TestRecognizeAmountForTypeTowerSettlement(t *testing.T) {
	// the compressed real-sheet shape: the 费用-bearing all-zero decoy
	// columns sit BEFORE the real one — the generic contains-fallback's
	// first-hit-wins order is exactly how the wrong column used to win.
	headers := []string{
		"账期月份", "产品业务确认单编号", "站址编码",
		"罚责赠费合计",
		"产品服务费合计（历史待退回费用）（不含税）",
		"产品服务费合计\n（出账费用）（不含税）",
		"产品服务费合计（出账费用+历史调增/已抵扣费用）（不含税）",
	}
	h, hit, variants := RecognizeAmountForType(FeeTypeTowerLease, headers)
	if !hit {
		t.Fatal("scoped table must hit the wrapped settlement header")
	}
	if want := headers[5]; h != want {
		t.Fatalf("amount header = %q, want %q", h, want)
	}
	if variants != 3 {
		t.Errorf("variants = %d, want 3 (three 产品服务费合计 columns)", variants)
	}
	// half-width paren form resolves through its own synonym entry
	half := []string{"站址编码", "产品服务费合计(出账费用)(不含税)", "罚责赠费合计"}
	if h, hit, _ := RecognizeAmountForType(FeeTypeTowerLease, half); !hit || h != half[1] {
		t.Fatalf("half-width form: hit=%v header=%q", hit, h)
	}
	// a bare 产品服务费合计 (no paren variant) still resolves on the
	// last-priority entry
	bare := []string{"站址编码", "产品服务费合计", "罚责赠费合计"}
	if h, hit, _ := RecognizeAmountForType(FeeTypeTowerLease, bare); !hit || h != bare[1] {
		t.Fatalf("bare stem form: hit=%v header=%q", hit, h)
	}
	// no scoped table for the other types; a tower sheet without any
	// scoped hit falls back to the generic recognizer (hit=false)
	if _, hit, _ := RecognizeAmountForType(FeeTypeElectricity, headers); hit {
		t.Error("electricity must carry no scoped amount table")
	}
	if _, hit, _ := RecognizeAmountForType(FeeTypeTowerLease, []string{"站址编码", "金额"}); hit {
		t.Error("no scoped hit on a generic 金额 header — the fallback stays generic")
	}
}

// writeTowerSettlementXLSX builds the compressed real-sheet shape: the
// all-zero 费用 decoys BEFORE the real wrapped amount header, so any
// first-hit-contains regression lands on the decoys exactly like the
// 157-column production sheet did.
func writeTowerSettlementXLSX(t *testing.T, dir, name string) string {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	headers := []string{
		"账期月份", "产品业务确认单编号", "站址编码",
		"罚责赠费合计",
		"产品服务费合计（历史待退回费用）（不含税）",
		"产品服务费合计\n（出账费用）（不含税）",
		"产品服务费合计（出账费用+历史调增/已抵扣费用）（不含税）",
	}
	rows := [][]any{
		{"202609", "ORD-1", "HLJ-01", 0, 0, 1000.50, 1000.50},
		{"202609", "ORD-2", "HLJ-02", 0, 0, 2345.67, 2345.67},
		{"202609", "ORD-3", "HLJ-03", 0, 0, 3000.00, 3000.00},
	}
	if err := f.SetSheetRow(sheet, "A1", &headers); err != nil {
		t.Fatalf("write header: %v", err)
	}
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+2)
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			t.Fatalf("write row %d: %v", i+2, err)
		}
	}
	p := filepath.Join(dir, name)
	if err := f.SaveAs(p); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return p
}

// TestImportTowerSettlementMultiLineHeader is the #29 acceptance core:
// the wrapped (Alt+Enter) settlement header imports with the RIGHT 口径
// (出账费用/不含税), the decoy all-zero columns never win, the answer
// names the amount column, and the multi-variant 口径 note rides the
// warnings channel. The pre-#29 import answered totalCents 0 on this
// exact shape with zero warnings.
func TestImportTowerSettlementMultiLineHeader(t *testing.T) {
	svc, dir := newFeeService(t)
	path := writeTowerSettlementXLSX(t, dir, "tower-settlement.xlsx")
	sum, err := svc.Import(context.Background(), ImportOptions{
		Path: path, FeeType: FeeTypeTowerLease, Year: 2026, Month: 9,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if sum.Status != "completed" || sum.RowsImported != 3 {
		t.Fatalf("summary = %+v", sum)
	}
	if sum.AmountColumn != "产品服务费合计\n（出账费用）（不含税）" {
		t.Fatalf("amountColumn = %q, want the wrapped 出账费用 header", sum.AmountColumn)
	}
	// the 口径 note names the chosen column AND the sibling count. The
	// money-total assertion itself lives in the duckdb test build
	// (tower_amount_duckdb_test.go): the default build carries no query
	// engine, and asserting totals here would test a 501, not the money.
	var scopeNote bool
	for _, w := range sum.Warnings {
		if strings.Contains(w, "产品服务费合计") && strings.Contains(w, "3 个同族金额列") {
			scopeNote = true
		}
	}
	if !scopeNote {
		t.Errorf("warnings = %v, want the multi-variant 口径 note (chosen column + 3 siblings)", sum.Warnings)
	}
	if got := sum.PeriodBasis; strings.Contains(got, "全部为 0") {
		t.Errorf("periodBasis must not carry an all-zero note here: %s", got)
	}
}

// TestImportExplicitMappingOutranksScopedTable pins the precedence: the
// operator's --map IS the 口径 declaration; the scoped table only narrows
// the AUTO resolution. An explicit mapping to the sibling variant stays
// untouched (which 口径 to read is the operator's call, never re-guessed).
func TestImportExplicitMappingOutranksScopedTable(t *testing.T) {
	svc, dir := newFeeService(t)
	path := writeTowerSettlementXLSX(t, dir, "explicit.xlsx")
	sum, err := svc.Import(context.Background(), ImportOptions{
		Path: path, FeeType: FeeTypeTowerLease, Year: 2026, Month: 9,
		Mapping: map[string]string{
			"siteCode": "站址编码",
			"amount":   "产品服务费合计（历史待退回费用）（不含税）",
		},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if sum.AmountColumn != "产品服务费合计（历史待退回费用）（不含税）" {
		t.Fatalf("amountColumn = %q, want the explicit mapping to survive", sum.AmountColumn)
	}
	// the explicit 口径 column is all-zero on this sheet — the
	// stop-the-bleed warning must fire (and say WHY it likely happened).
	var zeroNote bool
	for _, w := range sum.Warnings {
		if strings.Contains(w, "金额全部为 0") {
			zeroNote = true
		}
	}
	if !zeroNote {
		t.Errorf("warnings = %v, want the all-zero stop-the-bleed note", sum.Warnings)
	}
}

// TestImportAllZeroAmountWarns is the #29 stop-the-bleed acceptance: a
// fresh import whose EVERY row carries 0 must answer completed WITH the
// named warning — completed-and-silent was the reported defect (454 rows,
// zero warnings, a whole fee type dead in every downstream face).
func TestImportAllZeroAmountWarns(t *testing.T) {
	svc, dir := newFeeService(t)
	path := writeFeeCSVP(t, dir, "zero.csv", "站址编码,金额\nA,0\nB,0\nC,0\n")
	sum, err := svc.Import(context.Background(), ImportOptions{
		Path: path, FeeType: FeeTypeMaintenance, Year: 2026, Month: 1,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var zeroNote bool
	for _, w := range sum.Warnings {
		if strings.Contains(w, "3 行金额全部为 0") && strings.Contains(w, "金额列") {
			zeroNote = true
		}
	}
	if !zeroNote {
		t.Errorf("warnings = %v, want the all-zero note naming the row count and the mapping suspect", sum.Warnings)
	}
}
