package fees

// rules_test.go — issue #2 phase 1: the service-level contract of the
// configurable rules. Creation validates the closed sets and bounds
// BEFORE any SQL (the operator-facing refusal texts), the enable flip is
// the human channel with its audit row, and the custom-rules scenario
// composes the engine findings with the rule provenance — plus the two
// honest-empty paths (no enabled rules at all; enabled but nothing
// fires). The engine execution itself has its own duckdb-tagged suite
// (engine_custom_rules_duckdb_test.go); here the fakeRiskEngine scripts
// the findings so the composition contract holds in every build.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// validRuleInput is one well-formed creation payload (the six-tuple of
// the 电费单月超阈值 shape).
func validRuleInput() RuleInput {
	return RuleInput{
		Name: "电费单月超阈值", FeeType: "electricity", Metric: "amount",
		Aggregation: "sum", Dimension: "site_month", Comparator: "gt",
		Threshold: 500000, WindowMonths: 1, Severity: "high",
		Description: "单站单月电费超 5000 元",
	}
}

// TestCreateRuleValidation walks the refusal matrix: every enum/bounds
// violation answers an ErrImportRefused whose text carries the received
// values (the operator-facing vocabulary shared with HTTP 422 / MCP).
func TestCreateRuleValidation(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		mut   func(*RuleInput)
		match string
	}{
		{"empty name", func(in *RuleInput) { in.Name = "  " }, "name 不能为空"},
		{"name over 120 runes", func(in *RuleInput) { in.Name = strings.Repeat("超", 121) }, "name 过长"},
		{"unknown fee type", func(in *RuleInput) { in.FeeType = "rent" }, "feeType"},
		{"foreign metric", func(in *RuleInput) { in.Metric = "amount_cents" }, "枚举越界"},
		{"foreign aggregation", func(in *RuleInput) { in.Aggregation = "median" }, "枚举越界"},
		{"foreign dimension", func(in *RuleInput) { in.Dimension = "site_year" }, "枚举越界"},
		{"foreign comparator", func(in *RuleInput) { in.Comparator = "between" }, "枚举越界"},
		{"foreign severity", func(in *RuleInput) { in.Severity = "extreme" }, "枚举越界"},
		{"negative threshold", func(in *RuleInput) { in.Threshold = -0.01 }, "threshold 不能为负"},
		{"window 13", func(in *RuleInput) { in.WindowMonths = 13 }, "windowMonths 必须在 1–12"},
		{"description over 500", func(in *RuleInput) { in.Description = strings.Repeat("描", 501) }, "description 过长"},
	}
	for _, tc := range cases {
		in := validRuleInput()
		tc.mut(&in)
		_, err := svc.CreateRule(ctx, in, "cli")
		if err == nil {
			t.Fatalf("%s: must refuse", tc.name)
		}
		if !errors.Is(err, ErrImportRefused) {
			t.Fatalf("%s: refusal must wrap ErrImportRefused, got %v", tc.name, err)
		}
		if !strings.Contains(err.Error(), tc.match) {
			t.Fatalf("%s: text %q must contain %q", tc.name, err.Error(), tc.match)
		}
	}

	// windowMonths 0 → the honest default 1 (NOT a refusal).
	in := validRuleInput()
	in.WindowMonths = 0
	rule, err := svc.CreateRule(ctx, in, "cli")
	if err != nil {
		t.Fatalf("window 0 must default to 1, got %v", err)
	}
	if rule.WindowMonths != 1 {
		t.Fatalf("window 0 → %d, want 1", rule.WindowMonths)
	}
}

// TestCreateRuleObservationDefaultAndAudit pins the channel doctrine: a
// fresh rule lands DISABLED, the 'created' audit row names the actor,
// and ListRules filters by the flag.
func TestCreateRuleObservationDefaultAndAudit(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	rule, err := svc.CreateRule(ctx, validRuleInput(), "cli:ops")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rule.Enabled {
		t.Fatal("a fresh rule must land DISABLED (观察态)")
	}
	if !strings.HasPrefix(rule.ID, "rule_") {
		t.Fatalf("rule id = %q, want the rule_<uuid> shape", rule.ID)
	}
	// duplicate name refuses (the UNIQUE made honest)
	if _, err := svc.CreateRule(ctx, validRuleInput(), "cli:ops"); err == nil {
		t.Fatal("duplicate name must refuse")
	}
	// the audit ledger carries the created row with the actor
	entries, err := svc.RuleAudit(ctx, rule.ID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit = %d rows (%v), want 1", len(entries), err)
	}
	if entries[0].Action != "created" || entries[0].Actor != "cli:ops" {
		t.Fatalf("audit row = %+v, want created/cli:ops", entries[0])
	}

	// the human flip: enable lands the enabled row, disable the disabled
	// one — newest first.
	if err := svc.SetRuleEnabled(ctx, rule.ID, true, "admin"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := svc.SetRuleEnabled(ctx, rule.ID, false, "admin"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	enabled, err := svc.ListRules(ctx, true)
	if err != nil || len(enabled) != 0 {
		t.Fatalf("enabled-only list after disable = %d (%v), want 0", len(enabled), err)
	}
	all, err := svc.ListRules(ctx, false)
	if err != nil || len(all) != 1 {
		t.Fatalf("full list = %d (%v), want 1 (the disabled rule stays visible)", len(all), err)
	}
	entries, err = svc.RuleAudit(ctx, rule.ID)
	if err != nil || len(entries) != 3 {
		t.Fatalf("audit = %d rows (%v), want 3 (created+enabled+disabled)", len(entries), err)
	}
	want := []string{"disabled", "enabled", "created"}
	for i, a := range entries {
		if a.Action != want[i] {
			t.Fatalf("audit[%d].action = %s, want %s (newest first)", i, a.Action, want[i])
		}
	}
	// unknown id → the 404 contract
	if err := svc.SetRuleEnabled(ctx, "rule_missing", true, "admin"); err == nil || !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown id must wrap domain.ErrNotFound, got %v", err)
	}
	if _, err := svc.RuleByID(ctx, "rule_missing"); err == nil || !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown RuleByID must wrap domain.ErrNotFound, got %v", err)
	}
}

// TestRisksCustomRulesComposition scripts the engine and pins the clue
// composition: the rule provenance rides the clue (metricLabel, the
// thresholdLabel six-tuple, the severity+description+教义 note), and the
// envelope note counts the enabled rules of the fee type.
func TestRisksCustomRulesComposition(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	rule, err := svc.CreateRule(ctx, validRuleInput(), "cli")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SetRuleEnabled(ctx, rule.ID, true, "admin"); err != nil {
		t.Fatalf("enable: %v", err)
	}

	engine := &fakeRiskEngine{customRules: []CustomRuleClue{
		{RuleID: rule.ID, RuleName: rule.Name, Severity: "high",
			SiteCode: "S-X", Year: 2024, Month: 6, MetricValue: 612340, Rows: 7},
	}, evidence: map[EvidenceKey][]EvidenceAnchor{
		{SiteCode: "S-X", Year: 2024, Month: 6}: {{SourceFile: "a.csv", SourceRow: 3, ImportBatch: "b1"}},
	}}

	res, err := svc.Risks(ctx, engine, RiskCustomRules, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024})
	if err != nil {
		t.Fatalf("risks: %v", err)
	}
	if len(res.Clues) != 1 {
		t.Fatalf("clues = %d, want 1", len(res.Clues))
	}
	c := res.Clues[0]
	if c.SiteCode != "S-X" || c.Year != 2024 || c.Month != 6 {
		t.Fatalf("clue period = %s %d-%d", c.SiteCode, c.Year, c.Month)
	}
	if c.MetricValue != 612340 {
		t.Fatalf("metricValue = %v, want the fired sum", c.MetricValue)
	}
	if c.Threshold == nil || *c.Threshold != 500000 {
		t.Fatalf("threshold = %v, want the rule's 500000", c.Threshold)
	}
	if !strings.Contains(c.MetricLabel, "sum(amount)") {
		t.Fatalf("metricLabel = %q, want the rule口径", c.MetricLabel)
	}
	if !strings.Contains(c.ThresholdLabel, rule.Name) || !strings.Contains(c.ThresholdLabel, "500000") {
		t.Fatalf("thresholdLabel = %q, must carry the rule name and threshold", c.ThresholdLabel)
	}
	for _, want := range []string{"severity: high", "单站单月电费超 5000 元", rule.ID, "人工核实"} {
		if !strings.Contains(c.Note, want) {
			t.Fatalf("note = %q, must contain %q", c.Note, want)
		}
	}
	if len(c.Evidence) != 1 || c.Evidence[0].SourceFile != "a.csv" {
		t.Fatalf("evidence anchors = %+v", c.Evidence)
	}
	if c.EvidenceTotal != 7 {
		t.Fatalf("evidenceTotal = %d, want 7", c.EvidenceTotal)
	}
	if !strings.Contains(res.Note, "启用规则数 1") {
		t.Fatalf("envelope note = %q, must count the enabled rules", res.Note)
	}
	if !strings.Contains(res.Note, "人工核实") {
		t.Fatalf("envelope note = %q, must carry the 教义 caveat", res.Note)
	}
}

// TestRisksCustomRulesFeeTypeIsolation: an enabled rule of ANOTHER fee
// type never runs — the scenario answers the honest-empty note with the
// "无已启用规则" vocabulary, not a zero-findings from a live rule.
func TestRisksCustomRulesFeeTypeIsolation(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	in := validRuleInput()
	in.FeeType = "tower_lease"
	rule, err := svc.CreateRule(ctx, in, "cli")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SetRuleEnabled(ctx, rule.ID, true, "admin"); err != nil {
		t.Fatalf("enable: %v", err)
	}

	res, err := svc.Risks(ctx, &fakeRiskEngine{}, RiskCustomRules, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024})
	if err != nil {
		t.Fatalf("risks: %v", err)
	}
	if len(res.Clues) != 0 {
		t.Fatalf("clues = %d, want 0 (the enabled rule is tower_lease)", len(res.Clues))
	}
	if !strings.Contains(res.Note, "无已启用规则") {
		t.Fatalf("note = %q, want the fee-type isolation note", res.Note)
	}
}

// TestRisksCustomRulesDisabledNeverRuns: a DISABLED rule of the queried
// type never reaches the engine either — the observation state is the
// whole point of the channel doctrine.
func TestRisksCustomRulesDisabledNeverRuns(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	if _, err := svc.CreateRule(ctx, validRuleInput(), "cli"); err != nil {
		t.Fatalf("create (disabled): %v", err)
	}
	res, err := svc.Risks(ctx, &fakeRiskEngine{}, RiskCustomRules, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024})
	if err != nil {
		t.Fatalf("risks: %v", err)
	}
	if len(res.Clues) != 0 {
		t.Fatalf("clues = %d, want 0 (rule disabled)", len(res.Clues))
	}
	if !strings.Contains(res.Note, "无已启用规则") {
		t.Fatalf("note = %q, want the no-enabled-rules note", res.Note)
	}
}

// TestRisksCustomRulesEnabledNothingFires: an enabled rule whose engine
// pass finds nothing answers the honest-empty envelope (note counts the
// enabled rule, clues empty) — no error, no silent note swap.
func TestRisksCustomRulesEnabledNothingFires(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	rule, err := svc.CreateRule(ctx, validRuleInput(), "cli")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.SetRuleEnabled(ctx, rule.ID, true, "admin"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	res, err := svc.Risks(ctx, &fakeRiskEngine{}, RiskCustomRules, RiskQuery{FeeType: FeeTypeElectricity, Year: 2024})
	if err != nil {
		t.Fatalf("risks: %v", err)
	}
	if len(res.Clues) != 0 {
		t.Fatalf("clues = %d, want 0", len(res.Clues))
	}
	if !strings.Contains(res.Note, "启用规则数 1") {
		t.Fatalf("note = %q, want the enabled-count even on empty findings", res.Note)
	}
	if strings.Contains(res.Note, "无已启用规则") {
		t.Fatalf("note = %q, must not claim no-enabled-rules while one runs", res.Note)
	}
}
