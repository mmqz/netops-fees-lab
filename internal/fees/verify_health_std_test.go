//go:build !duckdb

package fees

// verify_health_std_test.go — issue #32's default-build half: the data
// face CANNOT run without the duckdb engine, and the report must SAY so
// (DataChecksSkipped=true) instead of silently answering a half-audit —
// the silent skip is exactly the false all-clear this tool must never
// hand out. The duckdb half (verify_health_duckdb_test.go) proves the
// checks themselves.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestVerifyBatchesDataChecksSkippedOnDefault pins the honest skip: on
// the std engine the period face still runs (its checks are pure
// file/DB walks), the data face is named as not-run, and a clean ledger
// keeps answering zero problems.
func TestVerifyBatchesDataChecksSkippedOnDefault(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	p := filepath.Join(dir, "s-elec.csv")
	if err := os.WriteFile(p, []byte("站址编码,账期,电费(元)\nA,2026-01,100.00\nB,2026-01,200.00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: p, FeeType: FeeTypeElectricity, Year: 2026, Month: 1}); err != nil {
		t.Fatalf("import: %v", err)
	}
	// the std engine — exactly what fees.Query(ctx) answers on default
	// builds.
	rep, err := svc.VerifyBatches(ctx, stdQueryEngine{})
	if err != nil {
		t.Fatalf("verify-batches: %v", err)
	}
	if !rep.DataChecksSkipped {
		t.Error("DataChecksSkipped must be true on the default build (no engine, no data face)")
	}
	if rep.BatchesChecked != 1 {
		t.Fatalf("batchesChecked = %d, want 1 (the period face still runs)", rep.BatchesChecked)
	}
	if len(rep.PeriodProblems) != 0 || len(rep.Problems) != 0 {
		t.Errorf("problems = %+v, want clean period faces", rep.Problems)
	}
}
