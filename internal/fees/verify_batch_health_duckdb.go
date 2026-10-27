//go:build duckdb

package fees

// verify_batch_health_duckdb.go — the #32 data-validity face's real
// engine: one money-health aggregate per completed batch over its live
// Parquet. The issue's own verdict drove this file: `fees verify-batches`
// checked only period consistency and answered `problems: []` on the
// real-world ledger where 9 tower batches (every month of the type)
// carried amount 0 in every row — the #29 wrong-column import. 「检查
// 通过」比「没检查」更危险: a clean audit on garbage data hands the
// operator a false all-clear.
//
// The shape checks (issue §5.2, trimmed to what the data can honestly
// support):
//
//   - zero-amount: every row amount_cents 0 (min = max = 0, rows > 0) —
//     the #29 fingerprint. sum==0 alone would false-positive on a
//     positive/negative wash; min=max=0 is the real all-zero shape.
//   - empty-site-code: every row's site_code blank. The import guard
//     refuses blank codes row-by-row, so a live hit means a writer
//     regressed — the check is the regression fingerprint.
//   - all-negative-amount: max < 0 — a whole-batch refund or a parse/口径
//     anomaly; either way it deserves eyes, not silence.
//
// Row-count cliffs (issue §5.2's 骤降 item) deliberately stay OUT: the
// import-time guard (#12) already warns on <20% of last month with an
// honest baseline, and re-deriving a weaker version here would duplicate
// the channel without adding evidence.

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// verifyBatchDataHealth runs the money-health aggregate over every queued
// target: one in-memory DuckDB query per batch (ids whitelisted and paths
// byte-checked before they reach the query text — queueDataTarget). The
// batch order is the sorted id order: the report is a ledger, not a map
// iteration.
func (s *Service) verifyBatchDataHealth(ctx context.Context, targets []batchDataTarget) ([]VerifyBatchProblem, error) {
	if len(targets) == 0 {
		return nil, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, fmt.Errorf("fees: 批次数据体检: %w", err)
	}
	defer func() { _ = db.Close() }()
	sorted := make([]batchDataTarget, len(targets))
	copy(sorted, targets)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].id < sorted[j].id })

	var problems []VerifyBatchProblem
	for _, t := range sorted {
		if len(t.files) == 0 {
			continue // analysis-missing already named this on the period face
		}
		list := make([]string, 0, len(t.files))
		for _, f := range t.files {
			list = append(list, "'"+filepath.ToSlash(f)+"'")
		}
		query := fmt.Sprintf(`SELECT count(*),
min(amount_cents), max(amount_cents),
count(*) FILTER (WHERE trim(coalesce(site_code, '')) = '')
FROM read_parquet([%s], union_by_name = true)`,
			strings.Join(list, ", "))
		var rows, blankSites int
		var mn, mx int64
		if err := db.QueryRowContext(ctx, query).Scan(&rows, &mn, &mx, &blankSites); err != nil {
			return nil, fmt.Errorf("fees: 批次 %s 数据体检查询: %w", t.id, err)
		}
		if rows == 0 {
			continue
		}
		if mn == 0 && mx == 0 {
			problems = append(problems, VerifyBatchProblem{
				Kind:    "zero-amount",
				BatchID: t.id,
				Detail:  fmt.Sprintf("该批次 %d 行金额全部为 0——极可能是金额列未正确识别（#29 形态：长表头/多金额列变体，导入应带同款警告）。该批数据无法参与任何金额分析，建议核对映射后回滚重导", rows),
			})
		} else if mx < 0 {
			problems = append(problems, VerifyBatchProblem{
				Kind:    "all-negative-amount",
				BatchID: t.id,
				Detail:  fmt.Sprintf("该批次 %d 行金额全部为负（min=%d max=%d）——口径或解析异常（整批退费/负数标错），请人工核查", rows, mn, mx),
			})
		}
		if blankSites == rows {
			problems = append(problems, VerifyBatchProblem{
				Kind:    "empty-site-code",
				BatchID: t.id,
				Detail:  fmt.Sprintf("该批次 %d 行的站址编码全部为空——导入守卫逐行拒绝空编码，命中此检查意味着写入通道回归", rows),
			})
		}
	}
	return problems, nil
}
