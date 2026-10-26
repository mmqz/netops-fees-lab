//go:build duckdb

package fees

// engine_custom_rules_duckdb.go — issue #2 phase 1: the L1 configurable
// rules against the REAL engine. Every SQL fragment comes from a CLOSED
// enum re-validated here (the 不可注入 red line: a rule's name and
// description NEVER reach SQL — only whitelisted tokens and NUMERIC
// thresholds formatted via %d/%.6f, never the rule text).

import (
	"context"
	"fmt"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// The closed SQL fragment tables (mirrored by the v37 CHECKs and
// domain.ValidFeeRuleEnum — validated again here, belt and braces: the
// builder is a public seam).
var (
	ruleAggSQL = map[string]string{
		"sum":   "sum(amount_cents)",
		"avg":   "avg(amount_cents)",
		"max":   "max(amount_cents)",
		"count": "count(*)",
	}
	ruleComparatorSQL = map[string]string{
		"gt":  ">",
		"gte": ">=",
		"lt":  "<",
		"lte": "<=",
	}
)

// RiskCustomRulesPreset runs every given rule over the analysis table.
// Each rule is its own small query (the whitelist is per-fragment);
// findings merge in Go with the rule provenance riding the result.
// month_over_month rules use the window as the lag distance and compare
// the RISE RATIO against the threshold (the spike semantics: 0.30 = +30%).
func (duckQueryEngine) RiskCustomRulesPreset(ctx context.Context, analysisDir string, q RiskQuery, rules []domain.FeeRiskRule) ([]CustomRuleClue, error) {
	if analysisEmpty(analysisDir) || len(rules) == 0 {
		return nil, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	var out []CustomRuleClue
	for _, rule := range rules {
		// The injection guard: re-validate every enum before ANY SQL is
		// built — a rule row that somehow carries a foreign token answers
		// an error, never a query.
		if !domain.ValidFeeRuleEnum(rule.Metric, rule.Aggregation, rule.Dimension, rule.Comparator, rule.Severity) {
			return nil, fmt.Errorf("fees: 规则 %s 的枚举字段越界（metric=%q aggregation=%q dimension=%q comparator=%q severity=%q）——拒绝执行",
				rule.ID, rule.Metric, rule.Aggregation, rule.Dimension, rule.Comparator, rule.Severity)
		}
		agg, ok := ruleAggSQL[rule.Aggregation]
		if rule.Metric == "rows" {
			agg = "count(*)" // the rows metric ignores the aggregation — count IS the metric
		}
		if !ok {
			return nil, fmt.Errorf("fees: 规则 %s 的聚合 %q 不在白名单", rule.ID, rule.Aggregation)
		}
		glob := feeTableGlob(analysisDir)
		period := riskPeriodFilter(q.Year, 0)
		var query string
		if rule.Comparator == "mom_ratio" {
			// The lag chain sees the whole year (the base month must stay
			// in the sequence); the clue window narrows like scenario 4.
			monthFilter := ""
			if q.Month > 0 {
				monthFilter = fmt.Sprintf(" AND seq.month::BIGINT = %d", q.Month)
			}
			query = fmt.Sprintf(`
WITH per AS (
  SELECT site_code, year, month, %[1]s AS v, count(*) AS rows
  FROM read_parquet('%[2]s', hive_partitioning = true, union_by_name = true)
  WHERE fee_type = '%[3]s' %[4]s
  GROUP BY site_code, year, month
), seq AS (
  SELECT site_code, year, month, v, rows,
         lag(v, %[5]d) OVER (PARTITION BY site_code ORDER BY (year::BIGINT)*12 + (month::BIGINT)) AS prev
  FROM per
)
SELECT site_code, year, month, (v - prev)::DOUBLE / prev AS ratio, rows
FROM seq
WHERE prev IS NOT NULL AND prev > 0
  AND (v - prev)::DOUBLE / prev >= %.17[6]f %[7]s
ORDER BY ratio DESC, site_code, year, month
LIMIT %[8]d`,
				agg, glob, string(rule.FeeType), period, rule.WindowMonths, rule.Threshold, monthFilter, q.Limit)
		} else {
			comp, ok := ruleComparatorSQL[rule.Comparator]
			if !ok {
				return nil, fmt.Errorf("fees: 规则 %s 的算子 %q 不在白名单", rule.ID, rule.Comparator)
			}
			query = fmt.Sprintf(`
SELECT site_code, year, month, %[1]s AS v, count(*) AS rows
FROM read_parquet('%[2]s', hive_partitioning = true, union_by_name = true)
WHERE fee_type = '%[3]s' %[4]s
GROUP BY site_code, year, month
HAVING %[1]s %[5]s %.17[6]f
ORDER BY v DESC, site_code, year, month
LIMIT %[7]d`,
				agg, glob, string(rule.FeeType), period, comp, rule.Threshold, q.Limit)
		}
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("fees: 可配置规则 %s 查询: %w", rule.ID, err)
		}
		for rows.Next() {
			var c CustomRuleClue
			var v float64
			var nrows int
			if err := rows.Scan(&c.SiteCode, &c.Year, &c.Month, &v, &nrows); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("fees: 读取可配置规则 %s 线索: %w", rule.ID, err)
			}
			c.RuleID = rule.ID
			c.RuleName = rule.Name
			c.Severity = rule.Severity
			c.MetricValue = v
			c.Rows = nrows
			out = append(out, c)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("fees: 可配置规则 %s 遍历: %w", rule.ID, err)
		}
		_ = rows.Close()
	}
	return out, nil
}
