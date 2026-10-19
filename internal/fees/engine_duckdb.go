//go:build duckdb

package fees

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/marcboeker/go-duckdb/v2" // the sql driver registers itself
)

// duckQueryEngine is the real analysis engine (release builds: -tags
// duckdb, CGO_ENABLED=1 + gcc — the #59-validated integration: prebuilt
// platform bindings, 17s build, static 68MB binary).
//
// The engine opens ONE in-memory DuckDB handle per query call and reads
// the Parquet table via read_parquet(hive_partitioning=true) — the files
// ARE the store (no .duckdb database file, no write locks, per-batch
// files append immutably; the state layer stays in SQLite). Concurrent
// queries are independent handles: safe by construction.
//
// SQL shape: every statement is a FIXED template. The only interpolation
// is the analysis dir (a server-side path), the fee type (closed enum) and
// integer year/month — no caller string ever reaches the SQL text (the
// same parameterized-tool red line the MCP surface enforces).
type duckQueryEngine struct{}

func newQueryEngine() QueryEngine { return duckQueryEngine{} }

func (duckQueryEngine) Available() bool { return true }

func openAnalysis() (*sql.DB, error) {
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("fees: 打开 DuckDB 引擎: %w", err)
	}
	return db, nil
}

// periodFilter renders the WHERE fragment of a period scope: a single
// month, or the whole year. The values are ints — injection-free by type.
func periodFilter(year, month int) string {
	if month > 0 {
		return fmt.Sprintf("AND year = %d AND month = %d", year, month)
	}
	return fmt.Sprintf("AND year = %d", year)
}

func (duckQueryEngine) Totals(ctx context.Context, analysisDir string, feeType FeeType, year, month, limit int) ([]TotalsRow, error) {
	db, err := openAnalysis()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	query := fmt.Sprintf(`
SELECT site_code, count(*) AS rows, sum(amount_cents) AS total_cents
FROM read_parquet('%s', hive_partitioning = true)
WHERE fee_type = '%s' %s
GROUP BY site_code
ORDER BY total_cents DESC, site_code
LIMIT %d`,
		feeTableGlob(analysisDir), string(feeType), periodFilter(year, month), normalizeLimit(limit))
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("fees: 查询费用汇总: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TotalsRow
	for rows.Next() {
		var r TotalsRow
		if err := rows.Scan(&r.SiteCode, &r.Rows, &r.TotalCents); err != nil {
			return nil, fmt.Errorf("fees: 读取汇总行: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (duckQueryEngine) Duplicates(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]DuplicateRow, error) {
	db, err := openAnalysis()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	query := fmt.Sprintf(`
SELECT site_code, year, month, count(DISTINCT import_batch) AS batches, count(*) AS rows, sum(amount_cents) AS total_cents
FROM read_parquet('%s', hive_partitioning = true)
WHERE fee_type = '%s' %s
GROUP BY site_code, year, month
HAVING count(DISTINCT import_batch) > 1
ORDER BY total_cents DESC, site_code, year, month`,
		feeTableGlob(analysisDir), string(feeType), periodFilter(year, month))
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("fees: 查询跨批次重复结算候选: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DuplicateRow
	for rows.Next() {
		var r DuplicateRow
		if err := rows.Scan(&r.SiteCode, &r.Year, &r.Month, &r.Batches, &r.Rows, &r.TotalCents); err != nil {
			return nil, fmt.Errorf("fees: 读取重复结算行: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
