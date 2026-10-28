//go:build duckdb

package fees

// verify_health_duckdb_test.go — issue #32's acceptance on the real
// engine: the audit must SEE the money-health shapes. The production
// story: 17 checked batches answered problems: [] while 9 of them (a
// whole fee type, every month) carried amount 0 in every row — the #29
// wrong-column import. 「检查通过」比「没检查」更危险: the clean audit on
// garbage data handed the operator a false all-clear. The default-build
// half (verify_health_std_test.go) pins the honest skip flag.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDuckVerifyBatchesDetectsZeroAmount imports a healthy electricity
// batch and an all-zero tower batch, then proves the two-tier report:
// the healthy batch earns no data problem, the all-zero batch answers
// kind zero-amount with the mapping suspicion, and the merged Problems
// keeps the pre-#32 consumer contract.
func TestDuckVerifyBatchesDetectsZeroAmount(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()

	goodPath := filepath.Join(dir, "h-elec.csv")
	if err := os.WriteFile(goodPath, []byte("站址编码,账期,电费(元)\nA,2026-01,100.00\nB,2026-01,200.00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: goodPath, FeeType: FeeTypeElectricity, Year: 2026, Month: 1}); err != nil {
		t.Fatalf("good import: %v", err)
	}
	zeroPath := filepath.Join(dir, "h-zero.csv")
	if err := os.WriteFile(zeroPath, []byte("站址编码,账期,费用(元)\nA,2026-01,0\nB,2026-01,0\nC,2026-01,0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: zeroPath, FeeType: FeeTypeTowerLease, Year: 2026, Month: 1}); err != nil {
		t.Fatalf("zero import: %v", err)
	}

	rep, err := svc.VerifyBatches(ctx, newQueryEngine())
	if err != nil {
		t.Fatalf("verify-batches: %v", err)
	}
	if rep.DataChecksSkipped {
		t.Fatal("the duckdb build must run the data face (DataChecksSkipped=false)")
	}
	if rep.BatchesChecked != 2 {
		t.Fatalf("batchesChecked = %d, want 2", rep.BatchesChecked)
	}
	if len(rep.PeriodProblems) != 0 {
		t.Errorf("periodProblems = %v, want clean (the period faces agree)", rep.PeriodProblems)
	}
	if len(rep.DataProblems) != 1 {
		t.Fatalf("dataProblems = %+v, want the single zero-amount finding", rep.DataProblems)
	}
	dp := rep.DataProblems[0]
	if dp.Kind != "zero-amount" || !strings.Contains(dp.Detail, "3 行金额全部为 0") || !strings.Contains(dp.Detail, "金额列") {
		t.Errorf("zero-amount problem = %+v", dp)
	}
	if len(rep.Problems) != 1 || rep.Problems[0].Kind != "zero-amount" {
		t.Errorf("merged Problems = %+v, want the same finding for pre-#32 consumers", rep.Problems)
	}
}

// TestDuckVerifyBatchesAllNegativeFlags pins the mid-severity shape: a
// whole batch in the negative answers all-negative-amount — an honest
// finding, never a refusal (the operator decides what the minus side
// means: refunds, corrections or a parse defect).
func TestDuckVerifyBatchesAllNegativeFlags(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	p := filepath.Join(dir, "neg.csv")
	if err := os.WriteFile(p, []byte("站址编码,账期,金额\nA,2026-01,(100.00)\nB,2026-01,(50.00)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: p, FeeType: FeeTypeRepair, Year: 2026, Month: 1}); err != nil {
		t.Fatalf("import: %v", err)
	}
	rep, err := svc.VerifyBatches(ctx, newQueryEngine())
	if err != nil {
		t.Fatalf("verify-batches: %v", err)
	}
	found := false
	for _, d := range rep.DataProblems {
		if d.Kind == "all-negative-amount" {
			found = true
		}
	}
	if !found {
		t.Errorf("dataProblems = %+v, want all-negative-amount", rep.DataProblems)
	}
}

// TestDuckVerifyBatchesMixedAmountsStayClean pins the boundaries the
// money-health checks must NOT flag: a legit zero row inside a non-zero
// batch (one site's true 0 is data, not a defect), a negative row present
// in a batch (a wash is a business state) — and, since #33's DQ5, the
// negative minority must stay UNDER the 30% medium bar: 1 negative row of
// 5 (20%) stays clean, the majority band starts strictly above 30%.
func TestDuckVerifyBatchesMixedAmountsStayClean(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	p := filepath.Join(dir, "mixed.csv")
	if err := os.WriteFile(p, []byte("站址编码,账期,金额\nA,2026-01,100.00\nB,2026-01,0\nC,2026-01,(40.00)\nD,2026-01,200.00\nE,2026-01,300.00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: p, FeeType: FeeTypeMaintenance, Year: 2026, Month: 1}); err != nil {
		t.Fatalf("import: %v", err)
	}
	rep, err := svc.VerifyBatches(ctx, newQueryEngine())
	if err != nil {
		t.Fatalf("verify-batches: %v", err)
	}
	if len(rep.DataProblems) != 0 {
		t.Errorf("dataProblems = %+v, want clean (min<0<max defeats all-zero/all-negative, and 1/5 negative rows stays under the DQ5 30%% bar)", rep.DataProblems)
	}
}
