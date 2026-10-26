package domain

// feerules.go — the L1 configurable risk rules of issue #2 (phase 1): the
// {metric, aggregation, dimension, comparator, threshold, window}
// six-tuple a rule carries. The enums are CLOSED sets shared by the
// SQLite CHECKs, the service validation and the SQL builder — a rule's
// free text (name/description) never reaches SQL.

import (
	"errors"
	"time"
)

// FeeRiskRule is one configurable rule row.
type FeeRiskRule struct {
	ID           string
	Name         string
	FeeType      FeeType
	Metric       string // amount | rows
	Aggregation  string // sum | avg | max | count
	Dimension    string // site_month
	Comparator   string // gt | gte | lt | lte | mom_ratio
	Threshold    float64
	WindowMonths int
	Severity     string // high | medium | low
	Description  string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// FeeRiskRuleAudit is one action ledger entry (created/enabled/disabled —
// the 阈值变更留痕 red line: phase 1 has no update channel, so every
// state change is one of these three actions).
type FeeRiskRuleAudit struct {
	ID      int64
	RuleID  string
	Action  string
	Actor   string
	Changes string // JSON
	At      time.Time
}

// The closed enums (mirrored by the v37 CHECK constraints).
var (
	feeRuleMetrics      = map[string]bool{"amount": true, "rows": true}
	feeRuleAggregations = map[string]bool{"sum": true, "avg": true, "max": true, "count": true}
	feeRuleDimensions   = map[string]bool{"site_month": true}
	feeRuleComparators  = map[string]bool{"gt": true, "gte": true, "lt": true, "lte": true, "mom_ratio": true}
	feeRuleSeverities   = map[string]bool{"high": true, "medium": true, "low": true}
)

// ValidFeeRuleEnum answers whether every enum field of the rule is inside
// its closed set (the service re-validation — the SQL builder trusts the
// database CHECKs AND this gate, belt and braces).
func ValidFeeRuleEnum(metric, aggregation, dimension, comparator, severity string) bool {
	return feeRuleMetrics[metric] && feeRuleAggregations[aggregation] &&
		feeRuleDimensions[dimension] && feeRuleComparators[comparator] && feeRuleSeverities[severity]
}

// ErrFeeRuleValidation reports a rule that violates the closed sets or
// the numeric bounds (the caller's text is the operator-facing message).
var ErrFeeRuleValidation = errors.New("domain: 费用风险规则校验失败")
