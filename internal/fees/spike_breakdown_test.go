package fees

// spike_breakdown_test.go — issue #30's regression net: the spike
// scenario's excluded count used to merge two classes into one int and
// the envelope's advice pointed every operator at 重复导入/多批次 —
// including the real-world run where 2437 pairs sat on zero BASES (the
// #29 wrong-column import's mirror image), sending the operator to
// verify-batches to find nothing and conclude 「无异常」. The advice must
// name BOTH classes with their OWN counts and audit actions; the
// outlier scenario (whose only exclusion class IS cross-batch pairs)
// keeps its straight advice.

import (
	"context"
	"strings"
	"testing"
)

// TestSpikeExcludedBreakdownRidesEnvelope pins the whole #30 chain on the
// reported shape: zero clues, 2 cross-batch pairs, 2437 zero-base pairs.
// The excluded total keeps the pre-#30 arithmetic (2439); the breakdown
// serializes both classes; the warning names both AND each class's audit
// action — the zero-base half pointing at the amount column, the exact
// direction the old text never gave.
func TestSpikeExcludedBreakdownRidesEnvelope(t *testing.T) {
	svc, _ := newFeeService(t)
	engine := &fakeRiskEngine{spikeExcluded: &SpikeExcluded{CrossBatch: 2, ZeroBase: 2437}}
	res, err := svc.Risks(context.Background(), engine, RiskSpike, RiskQuery{
		FeeType: FeeTypeElectricity, Year: 2026,
	})
	if err != nil {
		t.Fatalf("risks: %v", err)
	}
	if res.Excluded != 2439 {
		t.Errorf("excluded = %d, want 2439 (2+2437, the pre-#30 arithmetic)", res.Excluded)
	}
	if res.ExcludedBreakdown == nil {
		t.Fatal("excludedBreakdown = nil, want the two-class breakdown on spike")
	}
	if res.ExcludedBreakdown.CrossBatch != 2 || res.ExcludedBreakdown.ZeroBase != 2437 {
		t.Errorf("breakdown = %+v, want {2 2437}", *res.ExcludedBreakdown)
	}
	var advice string
	for _, w := range res.Warnings {
		if strings.Contains(w, "排除分类") {
			advice = w
		}
	}
	if advice == "" {
		t.Fatalf("warnings = %v, want the classified-exclusion advice", res.Warnings)
	}
	for _, want := range []string{"跨批次 (站址, 账期) 2 对", "环比基期 ≤ 0 的对组 2437 个", "verify-batches", "金额列映射"} {
		if !strings.Contains(advice, want) {
			t.Errorf("advice %q missing %q", advice, want)
		}
	}
	// acceptance 4: the zero-base-majority answer must NOT be the old
	// one-directional 查重复导入 text alone.
	if strings.HasSuffix(advice, "请核对是否存在重复导入/多批次（可用 fees verify-batches 审计跨账期重复）") {
		t.Errorf("advice is the pre-#30 one-directional form: %q", advice)
	}
	// the note carries the same text (#21 acceptance 1: CLI/MCP read the note)
	if !strings.Contains(res.Note, "排除分类") {
		t.Errorf("note must carry the classified advice too, got: %s", res.Note)
	}
}

// TestSpikeBreakdownOmittedForOtherScenarios pins the omitempty contract:
// the breakdown is the SPIKE scenario's own exclusion semantics; the
// other scenarios answer without it (a zero struct would be its own
// misinformation — as if every scenario had two exclusion classes).
func TestSpikeBreakdownOmittedForOtherScenarios(t *testing.T) {
	svc, _ := newFeeService(t)
	engine := &fakeRiskEngine{outlierExcluded: 5}
	res, err := svc.Risks(context.Background(), engine, RiskOutlier, RiskQuery{
		FeeType: FeeTypeElectricity, Year: 2026,
	})
	if err != nil {
		t.Fatalf("risks: %v", err)
	}
	if res.ExcludedBreakdown != nil {
		t.Errorf("excludedBreakdown = %+v on outlier, want nil (spike-only field)", *res.ExcludedBreakdown)
	}
	// the outlier's single exclusion class IS cross-batch pairs — its
	// advice stays the straight verify-batches direction.
	var advice string
	for _, w := range res.Warnings {
		if strings.Contains(w, "重复导入/多批次") {
			advice = w
		}
	}
	if advice == "" {
		t.Fatalf("warnings = %v, want the cross-batch advice for the single-class scenario", res.Warnings)
	}
	if strings.Contains(advice, "环比基期") {
		t.Errorf("outlier advice must not borrow the spike zero-base half: %q", advice)
	}
}

// TestExclusionAdviceForms pins exclusionAdvice per scenario directly:
// spike (breakdown named), ledger-consistency (the #8 rule-scope rows —
// a 口径, not missing data), the generic cross-batch default.
func TestExclusionAdviceForms(t *testing.T) {
	spike := exclusionAdvice(RiskSpike, &SpikeExcluded{CrossBatch: 3, ZeroBase: 0})
	if !strings.Contains(spike, "跨批次 (站址, 账期) 3 对") || !strings.Contains(spike, "0 个") {
		t.Errorf("spike advice = %q", spike)
	}
	// spike with nil breakdown (never happens through Risks, the guard
	// keeps the advice honest anyway)
	if got := exclusionAdvice(RiskSpike, nil); !strings.Contains(got, "重复导入/多批次") {
		t.Errorf("spike advice without breakdown must fall back, got %q", got)
	}
	lc := exclusionAdvice(RiskLedgerConsistency, nil)
	if !strings.Contains(lc, "一表多户") || !strings.Contains(lc, "换表") {
		t.Errorf("ledger-consistency advice = %q, want the rule-scope explanation", lc)
	}
	if got := exclusionAdvice(RiskOutlier, nil); !strings.Contains(got, "重复导入/多批次") {
		t.Errorf("generic advice = %q", got)
	}
}

// TestSpikeExcludedTotal pins the arithmetic: Total() is the two-class
// sum, byte-compatible with the pre-#30 merged int.
func TestSpikeExcludedTotal(t *testing.T) {
	if got := (SpikeExcluded{CrossBatch: 7, ZeroBase: 5}).Total(); got != 12 {
		t.Errorf("Total() = %d, want 12", got)
	}
	if got := (SpikeExcluded{}).Total(); got != 0 {
		t.Errorf("zero Total() = %d, want 0", got)
	}
}
