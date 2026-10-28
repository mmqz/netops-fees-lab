//go:build duckdb

package fees

// verify_dq_duckdb_test.go — the #33 DQ face on the real engine: the new
// data-quality shapes (DQ3 single-site, DQ5 majority-negative) and the
// id/severity labeling of the inherited #32 checks (DQ1/DQ2), plus the
// json field contract the issue's §4.2 example asks for (dataProblems
// entries carry check + severity). DQ4's row-count cliff is a pure DB
// face and has its own both-builds tests in verify_dq_test.go (the
// importCSV seeder lives there too, untagged).

import (
	"context"
	"strings"
	"testing"
)

// findDataProblem returns the first data problem of the given kind.
func findDataProblem(rep VerifyBatchesReport, kind string) (VerifyBatchProblem, bool) {
	for _, d := range rep.DataProblems {
		if d.Kind == kind {
			return d, true
		}
	}
	return VerifyBatchProblem{}, false
}

// TestDuckVerifyDQ3SingleSite pins the new distinctness fingerprint: a
// batch whose every row belongs to ONE site answers single-site-code with
// check DQ3 / severity high — while a multi-site batch of the same shape
// stays clean, and the all-blank shape stays DQ2's alone (no DQ3
// double-report next to it).
func TestDuckVerifyDQ3SingleSite(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()

	// 3 rows, all site S-ONE: the paste-error shape.
	importCSV(t, svc, dir, "one.csv", "站址编码,账期,电费(元)\nS-ONE,2026-01,10.00\nS-ONE,2026-01,20.00\nS-ONE,2026-01,30.00\n", 2026, 1)
	// the healthy twin: same rows, three distinct sites.
	importCSV(t, svc, dir, "many.csv", "站址编码,账期,电费(元)\nS-A,2026-02,10.00\nS-B,2026-02,20.00\nS-C,2026-02,30.00\n", 2026, 2)

	rep, err := svc.VerifyBatches(ctx, newQueryEngine())
	if err != nil {
		t.Fatal(err)
	}
	prob, ok := findDataProblem(rep, "single-site-code")
	if !ok {
		t.Fatalf("dataProblems = %+v, want the single-site-code finding", rep.DataProblems)
	}
	if prob.Check != "DQ3" || prob.Severity != "high" {
		t.Fatalf("DQ3 labeling: %+v", prob)
	}
	if !strings.Contains(prob.Detail, "同一站址") {
		t.Errorf("DQ3 detail = %q", prob.Detail)
	}
	// the multi-site batch earns no DQ3 (and no other finding).
	if n := len(rep.DataProblems); n != 1 {
		t.Fatalf("dataProblems = %+v, want exactly the one DQ3 finding", rep.DataProblems)
	}
}

// TestDuckVerifyDQ5MajorityNegative pins the medium band: strictly more
// than 30% negative rows (and not all of them) answers
// majority-negative-amount with check DQ5 / severity medium — the
// all-negative batch keeps its high band, and the json field contract
// (check + severity on data problems) is asserted on both.
func TestDuckVerifyDQ5MajorityNegative(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()

	// 2 of 3 rows negative = 67% — the medium band (not 100%: one positive).
	importCSV(t, svc, dir, "mostly.csv", "站址编码,账期,电费(元)\nS-A,2026-01,100.00\nS-B,2026-01,(40.00)\nS-C,2026-01,(60.00)\n", 2026, 1)

	rep, err := svc.VerifyBatches(ctx, newQueryEngine())
	if err != nil {
		t.Fatal(err)
	}
	prob, ok := findDataProblem(rep, "majority-negative-amount")
	if !ok {
		t.Fatalf("dataProblems = %+v, want majority-negative-amount", rep.DataProblems)
	}
	if prob.Check != "DQ5" || prob.Severity != "medium" {
		t.Fatalf("DQ5 medium labeling: %+v", prob)
	}
	if !strings.Contains(prob.Detail, "30") || !strings.Contains(prob.Detail, "66%") {
		t.Errorf("DQ5 medium detail = %q (want the 30%% bar and the actual share)", prob.Detail)
	}
	// exactly one finding: the all-negative high band did NOT co-fire.
	if n := len(rep.DataProblems); n != 1 {
		t.Fatalf("dataProblems = %+v, want exactly the one DQ5 medium finding", rep.DataProblems)
	}
}

// TestDuckVerifyDQInheritedLabels pins the labeling of the inherited #32
// checks: zero-amount answers DQ1/high, and the merged Problems list
// carries the same labeled entries (the pre-#32 consumer contract only
// asked for kind — additive fields keep it).
func TestDuckVerifyDQInheritedLabels(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()

	importCSV(t, svc, dir, "zero.csv", "站址编码,账期,费用(元)\nS-A,2026-01,0\nS-B,2026-01,0\nS-C,2026-01,0\n", 2026, 1)

	rep, err := svc.VerifyBatches(ctx, newQueryEngine())
	if err != nil {
		t.Fatal(err)
	}
	prob, ok := findDataProblem(rep, "zero-amount")
	if !ok {
		t.Fatalf("dataProblems = %+v, want zero-amount", rep.DataProblems)
	}
	if prob.Check != "DQ1" || prob.Severity != "high" {
		t.Fatalf("DQ1 labeling: %+v", prob)
	}
	if rep.Problems[0].Check != "DQ1" || rep.Problems[0].Severity != "high" {
		t.Fatalf("merged Problems lost the DQ labeling: %+v", rep.Problems[0])
	}
}
