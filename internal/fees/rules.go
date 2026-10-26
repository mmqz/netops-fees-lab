package fees

// rules.go — issue #2 phase 1: the L1 configurable-rule management and the
// custom-rules scenario composition. Governance red lines:
//
//      AI 只能创建（enabled=0 观察态），人类启用/停用 —— SetRuleEnabled 是
//      人类通道（HTTP 写令牌 + CLI），MCP 无此面；
//      阈值变更留痕 —— 第一期没有更新通道（改阈值 = 停用旧规则 + 建新规
//      则，全程审计行可追溯）；
//      账单存在 ≠ 认定结论 —— 每条规则线索的 note 携带同一教义告诫。

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// RuleInput is the creation payload (the AI's JSON / the CLI's import
// file / the HTTP POST body — one shape).
type RuleInput struct {
	Name         string  `json:"name"`
	FeeType      string  `json:"feeType"`
	Metric       string  `json:"metric"`      // amount | rows
	Aggregation  string  `json:"aggregation"` // sum | avg | max | count
	Dimension    string  `json:"dimension"`   // site_month
	Comparator   string  `json:"comparator"`  // gt | gte | lt | lte | mom_ratio
	Threshold    float64 `json:"threshold"`
	WindowMonths int     `json:"windowMonths"`
	Severity     string  `json:"severity"` // high | medium | low
	Description  string  `json:"description"`
}

// ruleNameMax bounds the display name (the clue provenance rides it).
const ruleNameMax = 120

// CreateRule validates and inserts one rule (enabled=false — the
// conservative observation default) with its 'created' audit row in one
// transaction. The actor rides the audit ledger (the CLI passes the
// operator name; the HTTP face derives it from the token tier).
func (s *Service) CreateRule(ctx context.Context, in RuleInput, actor string) (domain.FeeRiskRule, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return domain.FeeRiskRule{}, refusef("规则 name 不能为空")
	}
	if len([]rune(name)) > ruleNameMax {
		return domain.FeeRiskRule{}, refusef("规则 name 过长（最多 %d 字）", ruleNameMax)
	}
	ft, err := ParseFeeType(in.FeeType)
	if err != nil {
		return domain.FeeRiskRule{}, refusef("feeType 必须是 %v 之一，收到 %q", FeeTypes(), in.FeeType)
	}
	if !domain.ValidFeeRuleEnum(in.Metric, in.Aggregation, in.Dimension, in.Comparator, in.Severity) {
		return domain.FeeRiskRule{}, refusef(
			"规则枚举越界——metric ∈ {amount,rows}，aggregation ∈ {sum,avg,max,count}，dimension ∈ {site_month}，comparator ∈ {gt,gte,lt,lte,mom_ratio}，severity ∈ {high,medium,low}；收到 metric=%q aggregation=%q dimension=%q comparator=%q severity=%q",
			in.Metric, in.Aggregation, in.Dimension, in.Comparator, in.Severity)
	}
	if in.Threshold < 0 {
		return domain.FeeRiskRule{}, refusef("threshold 不能为负（金额类阈值为分、行数类为行数、环比类为涨幅比），收到 %v", in.Threshold)
	}
	window := in.WindowMonths
	if window <= 0 {
		window = 1
	}
	if window > 12 {
		return domain.FeeRiskRule{}, refusef("windowMonths 必须在 1–12（环比基期回看月数），收到 %d", window)
	}
	if len([]rune(in.Description)) > 500 {
		return domain.FeeRiskRule{}, refusef("description 过长（最多 500 字）")
	}
	now := s.clock()
	rule := domain.FeeRiskRule{
		ID: domain.NewID("rule"), Name: name, FeeType: ft,
		Metric: in.Metric, Aggregation: in.Aggregation, Dimension: in.Dimension,
		Comparator: in.Comparator, Threshold: in.Threshold, WindowMonths: window,
		Severity: in.Severity, Description: in.Description,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.Do(ctx, func(tx *repo.Tx) error {
		// The name pre-check rides the SAME transaction (SQLite
		// serializes writers): a duplicate answers the honest 422
		// refusal text, the UNIQUE constraint below stays as the floor.
		if exists, err := tx.FeeRiskRules.NameExists(ctx, name); err != nil {
			return err
		} else if exists {
			return refusef("规则 name %q 已存在——name 唯一；改阈值 = 停用旧规则 + 建新规则", name)
		}
		if err := tx.FeeRiskRules.Create(ctx, rule); err != nil {
			return fmt.Errorf("规则创建失败: %w", err)
		}
		return tx.FeeRiskRules.InsertAudit(ctx, rule.ID, "created", actor, "")
	}); err != nil {
		return domain.FeeRiskRule{}, err
	}
	return rule, nil
}

// SetRuleEnabled flips the flag (the human channel) with the audit row.
func (s *Service) SetRuleEnabled(ctx context.Context, id string, enabled bool, actor string) error {
	return s.db.Do(ctx, func(tx *repo.Tx) error {
		if err := tx.FeeRiskRules.SetEnabled(ctx, id, enabled); err != nil {
			return err
		}
		changes := `{"enabled":{"from":1,"to":0}}`
		action := "disabled"
		if enabled {
			changes = `{"enabled":{"from":0,"to":1}}`
			action = "enabled"
		}
		return tx.FeeRiskRules.InsertAudit(ctx, id, action, actor, changes)
	})
}

// ListRules answers the rules (enabled-only when onlyEnabled).
func (s *Service) ListRules(ctx context.Context, onlyEnabled bool) ([]domain.FeeRiskRule, error) {
	return s.db.FeeRiskRules.List(ctx, onlyEnabled)
}

// RuleByID answers one rule (the CLI/HTTP read-back after a flip; the
// ErrNotFound wrapping is the 404 contract).
func (s *Service) RuleByID(ctx context.Context, id string) (domain.FeeRiskRule, error) {
	return s.db.FeeRiskRules.GetByID(ctx, id)
}

// RuleAudit answers one rule's action ledger (newest first).
func (s *Service) RuleAudit(ctx context.Context, ruleID string) ([]domain.FeeRiskRuleAudit, error) {
	return s.db.FeeRiskRules.AuditFor(ctx, ruleID)
}

// risksCustomRules composes the custom-rules scenario (#2): the enabled
// rules of the queried fee type run through the engine; each finding
// becomes a RiskClue carrying the rule provenance (name in the metric
// label, severity + description + caveat in the note, the six-tuple in
// the threshold label) and the shared evidence anchors.
func (s *Service) risksCustomRules(ctx context.Context, engine QueryEngine, q RiskQuery) (cluesPack, error) {
	rules, err := s.db.FeeRiskRules.List(ctx, true)
	if err != nil {
		return cluesPack{}, err
	}
	enabled := make([]domain.FeeRiskRule, 0, len(rules))
	for _, r := range rules {
		if r.FeeType == q.FeeType {
			enabled = append(enabled, r)
		}
	}
	if len(enabled) == 0 {
		return cluesPack{
			clues: []RiskClue{}, excluded: 0,
			note: customRulesNote(0) + "——当前该费用类型无已启用规则；规则经 fees rules 创建后默认停用（观察态），需人类启用后参与分析",
		}, nil
	}
	raw, err := engine.RiskCustomRulesPreset(ctx, s.analysisDir(), q, enabled)
	if err != nil {
		return cluesPack{}, err
	}
	if len(raw) == 0 {
		return cluesPack{clues: []RiskClue{}, excluded: 0, note: customRulesNote(len(enabled))}, nil
	}
	keys := make([]EvidenceKey, 0, len(raw))
	seen := map[EvidenceKey]bool{}
	for _, c := range raw {
		k := EvidenceKey{SiteCode: c.SiteCode, Year: c.Year, Month: c.Month}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	anchors, err := engine.EvidenceFor(ctx, s.analysisDir(), q.FeeType, keys, q.MaxEvidence)
	if err != nil {
		return cluesPack{}, err
	}
	clues := make([]RiskClue, 0, len(raw))
	for _, c := range raw {
		k := EvidenceKey{SiteCode: c.SiteCode, Year: c.Year, Month: c.Month}
		var rule *domain.FeeRiskRule
		for i := range enabled {
			if enabled[i].ID == c.RuleID {
				rule = &enabled[i]
				break
			}
		}
		thresh := rule.Threshold
		clue := RiskClue{
			SiteCode: c.SiteCode, Year: c.Year, Month: c.Month,
			MetricValue:    c.MetricValue,
			MetricLabel:    ruleLabel(rule.Metric, rule.Aggregation, rule.Comparator),
			Threshold:      &thresh,
			ThresholdLabel: fmt.Sprintf("rule: %s（%s %s %s %s%s）", rule.Name, rule.Aggregation, metricLabelOf(rule.Metric), comparatorLabelOf(rule.Comparator), formatRuleThreshold(rule.Threshold), windowLabelOf(rule)),
			Evidence:       anchors[k], EvidenceTotal: c.Rows,
		}
		note := fmt.Sprintf("severity: %s", rule.Severity)
		if rule.Description != "" {
			note += "；" + rule.Description
		}
		note += "；可配置规则线索（" + rule.ID + "），口径与阈值见 thresholdLabel——账单存在不构成认定结论，请人工核实"
		clue.Note = note
		clues = append(clues, clue)
	}
	return cluesPack{clues: clues, excluded: 0, note: customRulesNote(len(enabled))}, nil
}

// formatRuleThreshold renders a threshold WITHOUT scientific notation:
// the %g family answers 5e+05 for 500000 — an operator-facing money
// threshold must read as the integer cents it is (and a ratio keeps
// its decimals: 0.3, 0.55).
func formatRuleThreshold(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ruleLabel renders the metric label (what the number IS).
func ruleLabel(metric, agg, comparator string) string {
	if comparator == "mom_ratio" {
		return "momRiseRatio"
	}
	unit := "cents"
	if metric == "rows" {
		unit = "rows"
	}
	return agg + "(" + metric + ") " + unit
}

func metricLabelOf(metric string) string {
	if metric == "rows" {
		return "行数"
	}
	return "金额(分)"
}

func comparatorLabelOf(comparator string) string {
	switch comparator {
	case "gt":
		return ">"
	case "gte":
		return "≥"
	case "lt":
		return "<"
	case "lte":
		return "≤"
	case "mom_ratio":
		return "环比涨幅 ≥"
	}
	return comparator
}

func windowLabelOf(rule *domain.FeeRiskRule) string {
	if rule.Comparator != "mom_ratio" || rule.WindowMonths <= 1 {
		return ""
	}
	return fmt.Sprintf("，基期回看 %d 个月", rule.WindowMonths)
}

// customRulesNote builds the 口径 note of the custom-rules scenario —
// the caveat is part of the contract (账单存在 ≠ 认定结论). Lives
// untagged (the service renders it in every build; only the ENGINE run
// is duckdb-tagged).
func customRulesNote(enabled int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "线索口径：可配置规则（L1 六元组：度量/聚合/维度/算子/阈值/窗口）——本期范围：度量 amount/rows、聚合 sum/avg/max/count、维度 site_month、算子 gt/gte/lt/lte/环比；")
	fmt.Fprintf(&b, "规则由 AI/运营创建、人类启用（新规则默认停用观察态）；每条线索携带规则名与严重度标签；阈值与口径见各线索 thresholdLabel；")
	fmt.Fprintf(&b, "账单存在不构成认定结论，请人工核实（与内置场景同一教义）；启用规则数 %d", enabled)
	return b.String()
}
