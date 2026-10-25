//go:build duckdb

package fees

// engine_risks_duckdb.go — the four pure-Parquet risk presets and the
// evidence primitives of the risk-scenario surface (#64, #58 stage 4;
// #114's scenario 7 reuses SitePeriodTotals — a cross-layer Go composition).
//
// SQL shape discipline (unchanged from the two existing presets): every
// statement is a FIXED template; the only interpolations are the analysis
// dir (server-side path), the fee type (closed enum), integer periods and
// the query's explicit numeric thresholds — no caller string ever reaches
// the SQL text. The scenario ids themselves never interpolate.
//
// Determinism rulings baked into the SQL:
//
//   - Scenarios 4/5 compute only over SINGLE-BATCH (site, period) pairs
//     (#59's finding: duplicate rows make window functions and
//     distributions ambiguous). The multi-batch pairs are EXCLUDED with a
//     reported count — they are scenarios 2/3's clues, and the exclusion
//     is what makes spike/outlier results reproducible on dirty data.
//   - z-score uses stddev_samp and requires the period's peer count >=
//     MinSites (default 8) — tiny samples never produce z-clues. The
//     reported z is ROUNDED to 10 decimals in SQL: DuckDB's vectorized
//     multi-term aggregates (avg/stddev) sum in chunk order, so the raw
//     double wobbles at the last ULP across runs — the clue SET is
//     deterministic, and the rounded metric makes the OUTPUT stable too.
//   - The spike lag walks (year*12+month) months with data for the site;
//     the previous month WITH DATA is the comparison base (a skipped
//     month compares against the last billed month — the operator's
//     mental model of "vs last bill").

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// riskPeriodFilter is periodFilter with the risk query's shape.
func riskPeriodFilter(year, month int) string { return periodFilter(year, month) }

// perPair is the shared CTE head of the four presets: every (site,
// period) pair with its batch count, row count and total.
const riskPerPairCTE = `
WITH per AS (
  SELECT site_code, year, month,
         count(DISTINCT import_batch) AS batches,
         count(*) AS rows,
         sum(amount_cents) AS total_cents
  FROM read_parquet('%s', hive_partitioning = true, union_by_name = true)
  WHERE fee_type = '%s' %s
  GROUP BY site_code, year, month
)`

// RiskDuplicatesPreset implements scenario 2: multi-batch (site, period)
// pairs; metric = the batch count, threshold = >1 batches.
func (duckQueryEngine) RiskDuplicatesPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	if analysisEmpty(analysisDir) {
		return nil, 0, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = db.Close() }()
	query := fmt.Sprintf(riskPerPairCTE+`
SELECT site_code, year, month, batches, rows, total_cents
FROM per
WHERE batches > 1
ORDER BY total_cents DESC, site_code, year, month`,
		feeTableGlob(analysisDir), string(q.FeeType), riskPeriodFilter(q.Year, q.Month))
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("fees: 风险场景 duplicate-settlement 查询: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var clues []RiskClue
	for rows.Next() {
		var (
			site              string
			year, month, b, r int
			total             int64
		)
		if err := rows.Scan(&site, &year, &month, &b, &r, &total); err != nil {
			return nil, 0, fmt.Errorf("fees: 读取重复结算线索: %w", err)
		}
		th := float64(1)
		clues = append(clues, RiskClue{
			SiteCode: site, Year: year, Month: month,
			MetricValue: float64(b), MetricLabel: "batches",
			Threshold: &th, ThresholdLabel: ">1 批次",
			EvidenceTotal: r,
		})
	}
	return clues, 0, rows.Err()
}

// The deterministic tolerances of the ledger-consistency family (#5/#8)
// — every value documented, none buried in SQL:
const (
	// ReadingGapTolerance: 读数差 vs 填报用量的容差（度）。台账读数为整数或
	// 一位小数，0.5 容差吸收抄表进位；超出即线索。
	ReadingGapTolerance = 0.5
	// ShareSumEpsilon: 四方分摊比例之和与 1 的容差。千分位的四舍五入
	// (0.333×3) 是真实业务形态，5‰ 吸收它。
	ShareSumEpsilon = 0.005
	// ExclusiveShareFloor: 分摊合计 >= 0.999 视为独表（本方 100%，
	// #8 §0 的 reading-gap 前置）。
	ExclusiveShareFloor = 0.999
)

// RiskLedgerConsistencyPreset implements scenario 9 (#5, refined by #8):
// four deterministic in-ledger rules over the optional columns. Every
// clue is row-level evidence; the Excluded return counts the rows a rule
// intentionally did NOT judge: multi-party meters for reading-gap (the
// 一表多户 shape of #8 §0 — 读数差 is the WHOLE meter's consumption while
// 实际电量 is this party's share; comparing them is a false-positive
// machine) and swap-flagged rows for chain-break (a marked meter change
// is a legal break).
func (duckQueryEngine) RiskLedgerConsistencyPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]LedgerConsistencyClue, int, error) {
	if analysisEmpty(analysisDir) {
		return nil, 0, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = db.Close() }()
	if q.Limit <= 0 {
		q.Limit = 100
	}
	base := `
SELECT site_code, year, month, amount_cents, meter_no, reading_start, reading_end,
       meter_prev_end, meter_swapped, quantity, unit_price,
       share_mobile, share_unicom, share_telecom, share_tower,
       (share_mobile IS NOT NULL OR share_unicom IS NOT NULL OR share_telecom IS NOT NULL OR share_tower IS NOT NULL) AS has_share,
       COALESCE(share_mobile,0) + COALESCE(share_unicom,0) + COALESCE(share_telecom,0) + COALESCE(share_tower,0) AS share_sum,
       -- exclusive_meter: the reading-gap precondition of #8 §0 —
       -- judgeable ONLY when the party bears the FULL meter: no share
       -- columns at all, or one single party's share is the whole. A
       -- share_sum of 1 is NOT exclusivity (a 40/60 split also sums to
       -- 1 — the trap the first SQL fell into).
       (NOT (share_mobile IS NOT NULL OR share_unicom IS NOT NULL OR share_telecom IS NOT NULL OR share_tower IS NOT NULL)
        OR greatest(COALESCE(share_mobile,0), COALESCE(share_unicom,0), COALESCE(share_telecom,0), COALESCE(share_tower,0)) >= %f) AS exclusive_meter,
       source_file, source_row, import_batch
FROM read_parquet('%s', hive_partitioning = true, union_by_name = true)
WHERE fee_type = '%s' %s
`
	// NOTE: the UNION ALL shape keeps every rule's WHERE explicit below —
	// one query, one round trip, the limit applied to the merged set.
	query := fmt.Sprintf(`
WITH ledger AS (%s),
rule_rows AS (SELECT * FROM ledger),
gap AS (
  SELECT site_code, year, month, amount_cents, source_file, source_row, import_batch,
         abs((reading_end - reading_start) - quantity) AS metric,
         '读数差与用量之差（度）' AS label,
         '读数差=' || (reading_end - reading_start) || '，填报用量=' || quantity AS detail
  FROM rule_rows
  WHERE reading_start IS NOT NULL AND reading_end IS NOT NULL AND quantity IS NOT NULL
    AND exclusive_meter
    AND abs((reading_end - reading_start) - quantity) > %f
),
sums AS (
  SELECT site_code, year, month, amount_cents, source_file, source_row, import_batch,
         abs(share_sum - 1) AS metric,
         '四方分摊比例之和与 1 的偏差' AS label,
         '分摊合计=' || share_sum AS detail
  FROM rule_rows
  WHERE has_share AND abs(share_sum - 1) > %f
),
chain AS (
  SELECT site_code, year, month, amount_cents, source_file, source_row, import_batch,
         abs(reading_start - prev_end) AS metric,
         '跨账期读数断差（上月止数≠本月起数，度）' AS label,
         '上月止数=' || prev_end || '，本月起数=' || reading_start AS detail
  FROM (
    SELECT *, LAG(reading_end) OVER (PARTITION BY meter_no ORDER BY year, month) AS prev_end
    FROM rule_rows
    WHERE meter_no IS NOT NULL AND reading_start IS NOT NULL
  ) t
  WHERE prev_end IS NOT NULL
    AND reading_start IS NOT NULL AND reading_start <> prev_end
    AND NOT COALESCE(meter_swapped, false)
),
zero AS (
  SELECT site_code, year, month, amount_cents, source_file, source_row, import_batch,
         amount_cents / 100.0 AS metric,
         '零用量计费金额（元）' AS label,
         '用量=0 但应付金额>0——疑似上期漏抄表后用量顺延到本期计费' AS detail
  FROM rule_rows
  WHERE quantity IS NOT NULL AND quantity = 0 AND amount_cents > 0
)
SELECT 'reading-gap' AS rule, site_code, year, month, metric, label, detail, source_file, source_row, import_batch FROM gap
UNION ALL
SELECT 'ratio-sum', site_code, year, month, metric, label, detail, source_file, source_row, import_batch FROM sums
UNION ALL
SELECT 'chain-break', site_code, year, month, metric, label, detail, source_file, source_row, import_batch FROM chain
UNION ALL
SELECT 'zero-quantity-billing', site_code, year, month, metric, label, detail, source_file, source_row, import_batch FROM zero
ORDER BY year, month, site_code, rule
LIMIT %d`,
		fmt.Sprintf(base, ExclusiveShareFloor, feeTableGlob(analysisDir), string(q.FeeType), riskPeriodFilter(q.Year, q.Month)),
		ReadingGapTolerance, ShareSumEpsilon, q.Limit)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("fees: 风险场景 ledger-consistency 查询: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var clues []LedgerConsistencyClue
	for rows.Next() {
		var c LedgerConsistencyClue
		if err := rows.Scan(&c.Rule, &c.SiteCode, &c.Year, &c.Month, &c.MetricValue,
			&c.MetricLabel, &c.Detail, &c.SourceFile, &c.SourceRow, &c.ImportBatch); err != nil {
			return nil, 0, fmt.Errorf("fees: 读取台账一致性线索: %w", err)
		}
		clues = append(clues, c)
	}
	// The intentionally-unjudged counts: multi-party rows (reading-gap's
	// honest exclusion — the 一表多户 shape of #8 §0) and swap-flagged
	// chain rows (a MARKED meter change is a legal break, never a clue).
	var excluded int
	if len(clues) > 0 || true {
		if err := db.QueryRowContext(ctx, fmt.Sprintf(`
WITH ledger AS (%s),
chains AS (
  SELECT reading_start, prev_end, meter_swapped FROM (
    SELECT *, LAG(reading_end) OVER (PARTITION BY meter_no ORDER BY year, month) AS prev_end
    FROM ledger
    WHERE meter_no IS NOT NULL AND reading_start IS NOT NULL
  ) t
)
SELECT
  (SELECT count(*) FROM ledger
     WHERE reading_start IS NOT NULL AND reading_end IS NOT NULL AND quantity IS NOT NULL
       AND NOT exclusive_meter)
+ (SELECT count(*) FROM chains
     WHERE prev_end IS NOT NULL AND reading_start IS NOT NULL AND reading_start <> prev_end
       AND COALESCE(meter_swapped, false))`,
			fmt.Sprintf(base, ExclusiveShareFloor, feeTableGlob(analysisDir), string(q.FeeType), riskPeriodFilter(q.Year, q.Month)))).Scan(&excluded); err != nil {
			return nil, 0, fmt.Errorf("fees: 台账一致性排除数: %w", err)
		}
	}
	return clues, excluded, rows.Err()
}

// RiskLedgerMismatchPreset implements scenario 3: (site, period) pairs
// whose PER-BATCH totals disagree by more than DiffCents (default 0 =
// any delta). The batch-level aggregation first, then min/max across
// batches — split bills inside one batch never false-fire.
func (duckQueryEngine) RiskLedgerMismatchPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	if analysisEmpty(analysisDir) {
		return nil, 0, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = db.Close() }()
	query := fmt.Sprintf(`
WITH batched AS (
  SELECT site_code, year, month, import_batch, sum(amount_cents) AS batch_total, count(*) AS rows
  FROM read_parquet('%s', hive_partitioning = true, union_by_name = true)
  WHERE fee_type = '%s' %s
  GROUP BY site_code, year, month, import_batch
),
ledger AS (
  SELECT site_code, year, month, count(*) AS batches, sum(rows) AS rows,
         min(batch_total) AS mn, max(batch_total) AS mx
  FROM batched
  GROUP BY site_code, year, month
)
SELECT site_code, year, month, batches, rows, mx - mn AS delta
FROM ledger
WHERE batches > 1 AND (mx - mn) > %d
ORDER BY delta DESC, site_code, year, month`,
		feeTableGlob(analysisDir), string(q.FeeType), riskPeriodFilter(q.Year, q.Month), q.DiffCents)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("fees: 风险场景 ledger-mismatch 查询: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var clues []RiskClue
	for rows.Next() {
		var (
			site              string
			year, month, b, r int
			delta             int64
		)
		if err := rows.Scan(&site, &year, &month, &b, &r, &delta); err != nil {
			return nil, 0, fmt.Errorf("fees: 读取金额差异线索: %w", err)
		}
		th := float64(q.DiffCents)
		clues = append(clues, RiskClue{
			SiteCode: site, Year: year, Month: month,
			MetricValue: float64(delta), MetricLabel: "deltaCents",
			Threshold: &th, ThresholdLabel: fmt.Sprintf("> %d 分（各批次合计之差）", q.DiffCents),
			EvidenceTotal: r,
		})
	}
	return clues, 0, rows.Err()
}

// excludedPairCount runs the honest-envelope count for the risk presets:
// multi-batch (site, period) pairs are excluded from the single-batch
// scenarios 4/5, and the number must stay truthful EVEN WHEN the clue
// WHERE filters yield zero rows (#83 — the old first-row capture fell
// back to 0 exactly then, misreporting "nothing was excluded" on an
// empty clue set). The period scope matches the preset's per CTE: spike
// counts the whole YEAR (the lag chain spans the year), outlier the
// queried month.
func excludedPairCount(ctx context.Context, db *sql.DB, glob, feeType, period string) (int, error) {
	query := fmt.Sprintf(riskPerPairCTE+`
SELECT count(*) FROM per WHERE batches > 1`, glob, feeType, period)
	var n int
	if err := db.QueryRowContext(ctx, query).Scan(&n); err != nil {
		return 0, fmt.Errorf("fees: 风险场景排除计数查询: %w", err)
	}
	return n, nil
}

// RiskSpikePreset implements scenario 4: single-batch (site, period)
// totals whose rise against the previous billed month crosses Ratio.
// The excluded count reports the multi-batch pairs left out (#59's
// determinism ruling — they are scenarios 2/3's clues).
func (duckQueryEngine) RiskSpikePreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	if analysisEmpty(analysisDir) {
		return nil, 0, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = db.Close() }()
	// A single-month scope narrows the CLUE window, not the lag base:
	// the per CTE always sees the WHOLE YEAR (month 0 in the period
	// filter) so the previous billed month stays in the sequence — the
	// operator's "vs last bill" comparison never loses its base.
	monthClueFilter := ""
	if q.Month > 0 {
		monthClueFilter = fmt.Sprintf("AND seq.month::BIGINT = %d", q.Month)
	}
	query := fmt.Sprintf(riskPerPairCTE+`
, clean AS (
  SELECT * FROM per WHERE batches = 1
), seq AS (
  SELECT site_code, year, month, total_cents, rows,
         lag(total_cents) OVER (PARTITION BY site_code ORDER BY (year::BIGINT)*12 + (month::BIGINT)) AS prev_total
  FROM clean
)
SELECT seq.site_code, seq.year, seq.month, seq.total_cents, seq.rows, seq.prev_total,
       (seq.total_cents - seq.prev_total)::DOUBLE / seq.prev_total AS ratio
FROM seq
WHERE seq.prev_total IS NOT NULL AND seq.prev_total > 0
  AND (seq.total_cents - seq.prev_total)::DOUBLE / seq.prev_total >= %.6f
  %s
ORDER BY ratio DESC, site_code, year, month`,
		feeTableGlob(analysisDir), string(q.FeeType), riskPeriodFilter(q.Year, 0), q.Ratio, monthClueFilter)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("fees: 风险场景 spike 查询: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var clues []RiskClue
	for rows.Next() {
		var (
			site           string
			year, month, r int
			total, prev    int64
			ratio          float64
		)
		if err := rows.Scan(&site, &year, &month, &total, &r, &prev, &ratio); err != nil {
			return nil, 0, fmt.Errorf("fees: 读取环比线索: %w", err)
		}
		th := q.Ratio
		clues = append(clues, RiskClue{
			SiteCode: site, Year: year, Month: month,
			MetricValue: ratio, MetricLabel: "riseRatio",
			Threshold: &th, ThresholdLabel: fmt.Sprintf("环比涨幅 ≥ %.2f（上期 %d 分 → 本期 %d 分）", q.Ratio, prev, total),
			EvidenceTotal: r,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// #83: the excluded count is its own query — truthful on empty clue
	// sets and independent of the display filters. Year-wide scope: the
	// lag chain spans the whole year, so a pair excluded anywhere in it
	// affects every ratio in the year.
	excluded, err := excludedPairCount(ctx, db, feeTableGlob(analysisDir), string(q.FeeType), riskPeriodFilter(q.Year, 0))
	if err != nil {
		return nil, 0, err
	}
	// #105: pairs whose PREVIOUS total is <= 0 used to be invisible in
	// BOTH counters — no clue (the ratio is undefined on a zero base) and
	// no excluded entry. A zero-base month followed by charges is the
	// STRONGEST spike shape there is; the envelope must count it (the
	// service note tells the operator what the class means).
	zeroBase, err := zeroBasePairCount(ctx, db, feeTableGlob(analysisDir), string(q.FeeType), riskPeriodFilter(q.Year, 0))
	if err != nil {
		return nil, 0, err
	}
	return clues, excluded + zeroBase, nil
}

// zeroBasePairCount counts the single-batch (site, period) pairs whose
// previous billed month total is <= 0 — the pairs the ratio window cannot
// judge (#105's excluded class; multi-batch pairs are excludedPairCount's
// class).
func zeroBasePairCount(ctx context.Context, db *sql.DB, glob, feeType, period string) (int, error) {
	query := fmt.Sprintf(riskPerPairCTE+`
, clean AS (
  SELECT * FROM per WHERE batches = 1
), seq AS (
  SELECT site_code, year, month, total_cents,
         lag(total_cents) OVER (PARTITION BY site_code ORDER BY (year::BIGINT)*12 + (month::BIGINT)) AS prev_total
  FROM clean
)
SELECT count(*) FROM seq WHERE prev_total IS NOT NULL AND prev_total <= 0`,
		glob, feeType, period)
	var n int
	if err := db.QueryRowContext(ctx, query).Scan(&n); err != nil {
		return 0, fmt.Errorf("fees: 风险场景零基期计数查询: %w", err)
	}
	return n, nil
}

// RiskOutlierPreset implements scenario 5: single-batch (site, period)
// totals whose z-score against the same-period single-batch peers
// crosses |ZScore| (two-sided, the sign stays in the metric).
func (duckQueryEngine) RiskOutlierPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	if analysisEmpty(analysisDir) {
		return nil, 0, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = db.Close() }()
	query := fmt.Sprintf(riskPerPairCTE+`
, clean AS (
  SELECT * FROM per WHERE batches = 1
), stats AS (
  SELECT year, month, count(*) AS n, avg(total_cents) AS m, stddev_samp(total_cents) AS s
  FROM clean
  GROUP BY year, month
)
SELECT c.site_code, c.year, c.month, c.total_cents, c.rows, c.total_cents::DOUBLE - st.m AS dev,
       CASE WHEN st.s > 0 THEN round(((c.total_cents::DOUBLE - st.m) / st.s), 10) ELSE 0 END AS z
FROM clean c JOIN stats st ON c.year = st.year AND c.month = st.month
WHERE st.n >= %d AND st.s > 0
  AND abs(CASE WHEN st.s > 0 THEN (c.total_cents::DOUBLE - st.m) / st.s ELSE 0 END) >= %.6f
ORDER BY abs(z) DESC, c.site_code, c.year, c.month`,
		feeTableGlob(analysisDir), string(q.FeeType), riskPeriodFilter(q.Year, q.Month), q.MinSites, q.ZScore)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("fees: 风险场景 outlier 查询: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var clues []RiskClue
	for rows.Next() {
		var (
			site           string
			year, month, r int
			total          int64
			dev, z         float64
		)
		if err := rows.Scan(&site, &year, &month, &total, &r, &dev, &z); err != nil {
			return nil, 0, fmt.Errorf("fees: 读取离群线索: %w", err)
		}
		th := q.ZScore
		clues = append(clues, RiskClue{
			SiteCode: site, Year: year, Month: month,
			MetricValue: z, MetricLabel: "zScore",
			Threshold: &th, ThresholdLabel: fmt.Sprintf("|z| ≥ %.2f（同期同类单批次站址分布）", q.ZScore),
			EvidenceTotal: r,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// #83: same honest-envelope fix as spike; the outlier scope is the
	// queried month (its per CTE is month-filtered already).
	excluded, err := excludedPairCount(ctx, db, feeTableGlob(analysisDir), string(q.FeeType), riskPeriodFilter(q.Year, q.Month))
	if err != nil {
		return nil, 0, err
	}
	return clues, excluded, nil
}

// SitePeriodTotals — scenario 1's fee half: every (site, period) total
// in the scope, largest first (the composition filters by resource
// status; the order only shapes deterministic iteration).
func (duckQueryEngine) SitePeriodTotals(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]SitePeriodTotal, error) {
	if analysisEmpty(analysisDir) {
		return nil, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	query := fmt.Sprintf(`
SELECT site_code, year, month, count(*) AS rows, sum(amount_cents) AS total_cents
FROM read_parquet('%s', hive_partitioning = true, union_by_name = true)
WHERE fee_type = '%s' %s
GROUP BY site_code, year, month
ORDER BY total_cents DESC, site_code, year, month`,
		feeTableGlob(analysisDir), string(feeType), periodFilter(year, month))
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("fees: 站址-账期汇总查询: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SitePeriodTotal
	for rows.Next() {
		var (
			r     SitePeriodTotal
			rowsN int
		)
		if err := rows.Scan(&r.SiteCode, &r.Year, &r.Month, &rowsN, &r.TotalCents); err != nil {
			return nil, fmt.Errorf("fees: 读取站址-账期汇总: %w", err)
		}
		r.Rows = rowsN
		out = append(out, r)
	}
	return out, rows.Err()
}

// SiteTotalsAll — scenario 6's fee half: every site's total across the
// scope (period-agnostic), largest first.
func (duckQueryEngine) SiteTotalsAll(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]SiteTotal, error) {
	if analysisEmpty(analysisDir) {
		return nil, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	query := fmt.Sprintf(`
SELECT site_code, count(*) AS rows, sum(amount_cents) AS total_cents
FROM read_parquet('%s', hive_partitioning = true, union_by_name = true)
WHERE fee_type = '%s' %s
GROUP BY site_code
ORDER BY total_cents DESC, site_code`,
		feeTableGlob(analysisDir), string(feeType), periodFilter(year, month))
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("fees: 站址汇总查询: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []SiteTotal
	for rows.Next() {
		var (
			r     SiteTotal
			rowsN int
		)
		if err := rows.Scan(&r.SiteCode, &rowsN, &r.TotalCents); err != nil {
			return nil, fmt.Errorf("fees: 读取站址汇总: %w", err)
		}
		r.Rows = rowsN
		out = append(out, r)
	}
	return out, rows.Err()
}

// EvidenceFor fetches up to maxPer row-level anchors per key. The keys
// travel as a VALUES CTE joined against the Parquet table (site + year
// + optional month; key Month 0 = all months of the year); a window
// ranks each key's rows deterministically (import_batch, source_row,
// amount_cents) and the top-N survive. Chunks of 200 keys per statement
// (4 placeholders each) keep every SQLite-era habit irrelevant — DuckDB
// has no such ceiling, the chunk bounds statement size only.
//
// Presentation order = the window's rank (outer ORDER BY kid, rn), NOT
// (kid, source_row): the #60 batch-coexistence doctrine guarantees
// source_row collisions across coexisting batches (each file numbers
// its own rows, so two batches of one (site, period) both carry a row
// 2), and an order without a total-order tiebreak let DuckDB's
// parallel scan plan decide which batch's row 2 came first — the
// evidence anchor order wobbled across queries on identical data (the
// fee-domain concurrency stress caught it live; a re-run must be
// byte-identical, #64's determinism ruling).
func (duckQueryEngine) EvidenceFor(ctx context.Context, analysisDir string, feeType FeeType, keys []EvidenceKey, maxPer int) (map[EvidenceKey][]EvidenceAnchor, error) {
	if analysisEmpty(analysisDir) {
		return map[EvidenceKey][]EvidenceAnchor{}, nil
	}
	out := make(map[EvidenceKey][]EvidenceAnchor, len(keys))
	if len(keys) == 0 || maxPer <= 0 {
		return out, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	const chunk = 200
	for lo := 0; lo < len(keys); lo += chunk {
		hi := lo + chunk
		if hi > len(keys) {
			hi = len(keys)
		}
		var b strings.Builder
		b.WriteString("WITH keys(k, site, yr, mo) AS (VALUES ")
		args := make([]any, 0, (hi-lo)*4)
		for i := lo; i < hi; i++ {
			if i > lo {
				b.WriteByte(',')
			}
			b.WriteString("(?,?,?,?)")
			args = append(args, i-lo, keys[i].SiteCode, keys[i].Year, keys[i].Month)
		}
		b.WriteString(`), matched AS (
  SELECT k.k AS kid, p.source_file, p.source_row, p.import_batch, p.amount_cents,
         k.site, k.yr, k.mo
  FROM read_parquet('` + feeTableGlob(analysisDir) + `', hive_partitioning = true, union_by_name = true) p
  JOIN keys k ON p.site_code = k.site AND p.year = k.yr AND (k.mo = 0 OR p.month = k.mo)
  WHERE fee_type = '` + string(feeType) + `'
), ranked AS (
  SELECT *, row_number() OVER (PARTITION BY kid ORDER BY import_batch, source_row, amount_cents) AS rn
  FROM matched
)
SELECT kid, site, yr, mo, source_file, source_row, import_batch, amount_cents
FROM ranked WHERE rn <= ? ORDER BY kid, rn`)
		args = append(args, maxPer)
		rows, err := db.QueryContext(ctx, b.String(), args...)
		if err != nil {
			return nil, fmt.Errorf("fees: 证据锚点查询: %w", err)
		}
		for rows.Next() {
			var (
				kid    int
				site   string
				yr, mo int
				a      EvidenceAnchor
			)
			if err := rows.Scan(&kid, &site, &yr, &mo, &a.SourceFile, &a.SourceRow, &a.ImportBatch, &a.AmountCents); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("fees: 读取证据锚点: %w", err)
			}
			if idx := lo + kid; idx >= 0 && idx < len(keys) {
				out[keys[idx]] = append(out[keys[idx]], a)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}
