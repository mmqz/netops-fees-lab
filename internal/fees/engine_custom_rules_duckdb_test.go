//go:build duckdb

package fees

// engine_custom_rules_duckdb_test.go — issue #2 phase 1 on the REAL
// engine: the closed-enum SQL fragments, the four comparators, the rows
// metric, the mom_ratio lag window, the injection guard (a rule row
// carrying a foreign token answers an ERROR, never a query), and the
// deterministic aggregation semantics (GROUP BY (site, period) over every
// batch — a site's period total IS the sum across its ledgers; the
// multi-batch pairs are duplicate-settlement's own clue domain).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// importCustomRulesFixture seeds the smallest honest table for the rule
// suite (reuses the risk-fixture shapes):
//
//   - S-SPIKE: 2024-05 = 100.00 → 2024-06 = 250.00 (ratio 1.5)
//   - S-FLAT:  2024-05 = 100.00 → 2024-06 = 105.00 (ratio 0.05)
//   - S-DUP:   two batches of 100.00 in 2024-06 (per-period total 200.00,
//     rows 2 — the rows-metric and the no-exclusion semantics case)
func importCustomRulesFixture(t *testing.T, svc *Service) {
	t.Helper()
	ctx := context.Background()
	// #20: one file per declared month (the row-local 账期 column agrees
	// with the declaration — the partition follows the declaration).
	files := []struct {
		name string
		m    int
		body string
	}{
		{"may.csv", 5, "站址编码,账期,电费(元)\nS-SPIKE,2024-05,100.00\nS-FLAT,2024-05,100.00\n"},
		{"june.csv", 6, "站址编码,账期,电费(元)\nS-SPIKE,2024-06,250.00\nS-FLAT,2024-06,105.00\n"},
		{"dup-a.csv", 6, "站址编码,账期,电费(元),备注\nS-DUP,2024-06,100.00,台账A\n"},
		{"dup-b.csv", 6, "站址编码,账期,电费(元),备注\nS-DUP,2024-06,100.00,台账B\n"},
	}
	for _, f := range files {
		p := filepath.Join(t.TempDir(), f.name)
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Import(ctx, ImportOptions{Path: p, FeeType: FeeTypeElectricity, Year: 2024, Month: f.m}); err != nil {
			t.Fatalf("import %s: %v", f.name, err)
		}
	}
}

// ruleOf builds one enabled rule of the fixture's fee type.
func ruleOf(id string, comparator string, threshold float64, metric, agg string, window int) domain.FeeRiskRule {
	return domain.FeeRiskRule{
		ID: id, Name: "规则-" + id, FeeType: domain.FeeTypeElectricity,
		Metric: metric, Aggregation: agg, Dimension: "site_month",
		Comparator: comparator, Threshold: threshold, WindowMonths: window,
		Severity: "high", Enabled: true,
	}
}

// TestRiskCustomRulesPresetDuck walks the engine contract: gt/gte/lt/lte
// fragments, the rows metric (count overrides the aggregation), the
// mom_ratio lag chain with its window, and the month narrowing.
func TestRiskCustomRulesPresetDuck(t *testing.T) {
	svc, _, dir := newService(t)
	importCustomRulesFixture(t, svc)
	engine := newQueryEngine()
	ctx := context.Background()
	q := RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Limit: 100}

	// sum(amount) > 24000分: S-SPIKE June (25000) fires, S-FLAT (10500)
	// and S-DUP (20000) stay silent.
	got, err := engine.RiskCustomRulesPreset(ctx, filepath.Join(dir, "analysis"), q, []domain.FeeRiskRule{
		ruleOf("r_gt", "gt", 24000, "amount", "sum", 1),
	})
	if err != nil {
		t.Fatalf("gt: %v", err)
	}
	if len(got) != 1 || got[0].SiteCode != "S-SPIKE" || got[0].Year != 2024 || got[0].Month != 6 {
		t.Fatalf("gt findings = %+v, want S-SPIKE 2024-06", got)
	}
	if got[0].MetricValue != 25000 {
		t.Fatalf("gt metric = %v, want 25000 cents", got[0].MetricValue)
	}
	if got[0].RuleID != "r_gt" || got[0].Severity != "high" {
		t.Fatalf("provenance = %+v", got[0])
	}

	// gte at the exact value fires too; lt/lt invert (the low-side rules:
	// a clue may be abnormally LOW — the outlier doctrine).
	got, err = engine.RiskCustomRulesPreset(ctx, filepath.Join(dir, "analysis"), q, []domain.FeeRiskRule{
		ruleOf("r_gte", "gte", 25000, "amount", "sum", 1),
		ruleOf("r_lt", "lt", 11000, "amount", "sum", 1),
	})
	if err != nil {
		t.Fatalf("gte+lt: %v", err)
	}
	// r_gte: S-SPIKE June (25000 ≥ 25000); r_lt: S-FLAT June (10500 < 11000)
	// and every May row (10000 < 11000 — S-SPIKE 05 + S-FLAT 05).
	if len(got) != 4 {
		t.Fatalf("gte+lt findings = %d (%+v), want 4", len(got), got)
	}

	// rows metric: the aggregation is IGNORED (count IS the metric) —
	// S-DUP June carries 2 rows across its two ledger batches.
	got, err = engine.RiskCustomRulesPreset(ctx, filepath.Join(dir, "analysis"), q, []domain.FeeRiskRule{
		ruleOf("r_rows", "gt", 1, "rows", "avg", 1),
	})
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != 1 || got[0].SiteCode != "S-DUP" || got[0].MetricValue != 2 {
		t.Fatalf("rows findings = %+v, want S-DUP count 2", got)
	}

	// mom_ratio window=1, threshold 0.30: S-SPIKE June rises 1.5 → fires;
	// S-FLAT (0.05) stays silent; S-DUP has no May base (prev NULL) → the
	// lag chain skips it (the spike semantics: no base, no ratio).
	got, err = engine.RiskCustomRulesPreset(ctx, filepath.Join(dir, "analysis"), q, []domain.FeeRiskRule{
		ruleOf("r_mom", "mom_ratio", 0.30, "amount", "sum", 1),
	})
	if err != nil {
		t.Fatalf("mom_ratio: %v", err)
	}
	if len(got) != 1 || got[0].SiteCode != "S-SPIKE" || got[0].Month != 6 {
		t.Fatalf("mom findings = %+v, want S-SPIKE June only", got)
	}
	if got[0].MetricValue < 1.49 || got[0].MetricValue > 1.51 {
		t.Fatalf("mom ratio = %v, want 1.5", got[0].MetricValue)
	}

	// month narrowing: the same mom rule scoped to June answers the same
	// single clue (the lag chain still sees May — the base month must
	// stay in the sequence); scoped to May it answers nothing.
	qm := q
	qm.Month = 6
	got, err = engine.RiskCustomRulesPreset(ctx, filepath.Join(dir, "analysis"), qm, []domain.FeeRiskRule{
		ruleOf("r_mom6", "mom_ratio", 0.30, "amount", "sum", 1),
	})
	if err != nil {
		t.Fatalf("mom June: %v", err)
	}
	if len(got) != 1 || got[0].Month != 6 {
		t.Fatalf("mom June findings = %+v", got)
	}
	qm.Month = 5
	got, err = engine.RiskCustomRulesPreset(ctx, filepath.Join(dir, "analysis"), qm, []domain.FeeRiskRule{
		ruleOf("r_mom5", "mom_ratio", 0.30, "amount", "sum", 1),
	})
	if err != nil {
		t.Fatalf("mom May: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("mom May findings = %+v, want none (no April base)", got)
	}

	// The limit cap is PER RULE (each rule is its own query): a union cap
	// would silently drop later rules' findings by iteration order — the
	// #83 doctrine (标注/计数，绝不静默丢弃) applies to the merge too. Two
	// firing rules at limit=1 -> one finding EACH.
	qcap := q
	qcap.Limit = 1
	got, err = engine.RiskCustomRulesPreset(ctx, filepath.Join(dir, "analysis"), qcap, []domain.FeeRiskRule{
		ruleOf("r_cap1", "gt", 24000, "amount", "sum", 1),
		ruleOf("r_cap2", "lt", 11000, "amount", "sum", 1),
	})
	if err != nil {
		t.Fatalf("cap: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("capped findings = %d, want 2 (one per rule — the cap is per-rule, never a silent union drop)", len(got))
	}

	// Empty rules slice: no queries run at all.
	got, err = engine.RiskCustomRulesPreset(ctx, filepath.Join(dir, "analysis"), q, nil)
	if err != nil || got != nil {
		t.Fatalf("nil rules = %v, %v — want nil, nil", got, err)
	}
}

// TestRiskCustomRulesInjectionGuardDuck: a rule row that somehow carries
// a foreign token answers an ERROR naming the rule — never a query. This
// is the 不可注入 red line's engine-side floor (the CHECKs and the
// service gate above it; the builder trusts NOTHING).
func TestRiskCustomRulesInjectionGuardDuck(t *testing.T) {
	svc, _, dir := newService(t)
	importCustomRulesFixture(t, svc)
	engine := newQueryEngine()
	ctx := context.Background()

	malicious := domain.FeeRiskRule{
		ID: "r_evil", Name: "x", FeeType: domain.FeeTypeElectricity,
		Metric: "amount", Aggregation: "sum; DROP TABLE fees", Dimension: "site_month",
		Comparator: "gt", Threshold: 1, WindowMonths: 1, Severity: "high", Enabled: true,
	}
	_, err := engine.RiskCustomRulesPreset(ctx, filepath.Join(dir, "analysis"), RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Limit: 10}, []domain.FeeRiskRule{malicious})
	if err == nil {
		t.Fatal("a foreign aggregation token must refuse, never query")
	}
	if !strings.Contains(err.Error(), "r_evil") || !strings.Contains(err.Error(), "拒绝执行") {
		t.Fatalf("guard error must name the rule and the refusal: %v", err)
	}

	// Same floor for a foreign comparator.
	malicious.Aggregation = "sum"
	malicious.Comparator = "gt OR 1=1"
	_, err = engine.RiskCustomRulesPreset(ctx, filepath.Join(dir, "analysis"), RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Limit: 10}, []domain.FeeRiskRule{malicious})
	if err == nil || !strings.Contains(err.Error(), "拒绝执行") {
		t.Fatalf("comparator guard: the enum re-validation must refuse the foreign comparator: %v", err)
	}
}

// TestRiskCustomRulesScenarioEndToEndDuck: the full service composition
// on the real engine — create (disabled) → enable via the human channel
// → risks custom-rules answers clues with the rule provenance and
// evidence anchors.
func TestRiskCustomRulesScenarioEndToEndDuck(t *testing.T) {
	svc, _, _ := newService(t)
	importCustomRulesFixture(t, svc)
	ctx := context.Background()

	rule, err := svc.CreateRule(ctx, RuleInput{
		Name: "电费单月超240元", FeeType: "electricity", Metric: "amount",
		Aggregation: "sum", Dimension: "site_month", Comparator: "gt",
		Threshold: 24000, WindowMonths: 1, Severity: "high",
		Description: "单站单月电费合计超过 240 元",
	}, "cli")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// The observation default: a fresh rule fires nothing.
	res, err := svc.Risks(ctx, newQueryEngine(), RiskCustomRules, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Limit: 100})
	if err != nil {
		t.Fatalf("risks (disabled): %v", err)
	}
	if len(res.Clues) != 0 || !strings.Contains(res.Note, "无已启用规则") {
		t.Fatalf("disabled rule must fire nothing, note = %q", res.Note)
	}
	// The human flip.
	if err := svc.SetRuleEnabled(ctx, rule.ID, true, "admin"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	res, err = svc.Risks(ctx, newQueryEngine(), RiskCustomRules, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024, Limit: 100})
	if err != nil {
		t.Fatalf("risks (enabled): %v", err)
	}
	if len(res.Clues) != 1 {
		t.Fatalf("clues = %d, want 1 (S-SPIKE June)", len(res.Clues))
	}
	c := res.Clues[0]
	if c.SiteCode != "S-SPIKE" || c.Month != 6 || c.MetricValue != 25000 {
		t.Fatalf("clue = %+v", c)
	}
	if !strings.Contains(c.ThresholdLabel, "电费单月超240元") || !strings.Contains(c.ThresholdLabel, "24000") {
		t.Fatalf("thresholdLabel = %q", c.ThresholdLabel)
	}
	if len(c.Evidence) == 0 || c.Evidence[0].SourceFile == "" {
		t.Fatalf("evidence anchors missing: %+v", c.Evidence)
	}
}
