//go:build duckdb

package fees

// tower_amount_duckdb_test.go — the duckdb-build half of issue #29's
// acceptance: the money itself. The default-build test
// (tower_amount_test.go) pins the mapping/warning faces; only the real
// engine can prove the wrapped settlement header's amounts landed in the
// analysis layer — the reported production shape imported 454 rows whose
// every totalCents read 0, and no mapping-level assertion would have
// caught the money being wrong.

import (
	"context"
	"strings"
	"testing"
)

// TestTowerSettlementAmountsLandInAnalysis imports the compressed real
// sheet and reads the totals back through the real engine: three rows,
// 6346.17 yuan = 634617 cents, per site. The pre-#29 import answered 0
// for every month of the fee type on this exact shape.
func TestTowerSettlementAmountsLandInAnalysis(t *testing.T) {
	svc, dir := newFeeService(t)
	path := writeTowerSettlementXLSX(t, dir, "tower-settlement.xlsx")
	sum, err := svc.Import(context.Background(), ImportOptions{
		Path: path, FeeType: FeeTypeTowerLease, Year: 2026, Month: 9,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if sum.AmountColumn != "产品服务费合计\n（出账费用）（不含税）" {
		t.Fatalf("amountColumn = %q", sum.AmountColumn)
	}
	rows, err := svc.QueryTotals(context.Background(), Query(context.Background()), FeeTypeTowerLease, 2026, 9, 10)
	if err != nil {
		t.Fatalf("query totals: %v", err)
	}
	var total int64
	for _, r := range rows {
		total += r.TotalCents
	}
	if total != 634617 {
		t.Errorf("tower totals = %d cents, want 634617 (1000.50+2345.67+3000.00 yuan)", total)
	}
	if len(rows) != 3 {
		t.Errorf("site rows = %d, want 3", len(rows))
	}
	for _, w := range sum.Warnings {
		if strings.Contains(w, "全部为 0") {
			t.Errorf("the correct-口径 import must not warn all-zero: %s", w)
		}
	}
}
