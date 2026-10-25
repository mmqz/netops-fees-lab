//go:build duckdb

package fees

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

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

// LedgerRows is the #5 rows query: raw analysis rows with the optional
// ledger columns (union_by_name keeps pre-#5 files readable — their
// optional columns come back NULL). Site filter optional, limit capped
// like the sibling presets.
func (duckQueryEngine) LedgerRows(ctx context.Context, analysisDir string, feeType FeeType, year, month int, site string, recordKey string, limit int) ([]LedgerRow, error) {
	if analysisEmpty(analysisDir) {
		return nil, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	siteFilter := ""
	if site != "" {
		siteFilter = fmt.Sprintf("AND site_code = '%s'", escapeSQLString(site))
	}
	if recordKey != "" {
		siteFilter += fmt.Sprintf(" AND COALESCE(record_key, '') = '%s'", escapeSQLString(recordKey))
	}
	query := fmt.Sprintf(`
SELECT site_code, year, month, amount_cents, record_key,
       meter_no, reading_start, reading_end, meter_prev_end, meter_swapped,
       quantity, unit_price, share_mobile, share_unicom, share_telecom, share_tower,
       source_file, source_row, import_batch
FROM read_parquet('%s', hive_partitioning = true, union_by_name = true)
WHERE fee_type = '%s' %s %s
ORDER BY year, month, site_code, source_file, source_row
LIMIT %d`,
		feeTableGlob(analysisDir), string(feeType), periodFilter(year, month), siteFilter, normalizeLimit(limit))
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("fees: 查询台账行: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []LedgerRow
	for rows.Next() {
		var (
			r        LedgerRow
			recordNo sql.NullString
			meterNo  sql.NullString
			f        [9]sql.NullFloat64
			swapped  sql.NullBool
			_        = f
		)
		if err := rows.Scan(&r.SiteCode, &r.Year, &r.Month, &r.AmountCents,
			&recordNo,
			&meterNo, &f[0], &f[1], &f[2], &swapped, &f[3], &f[4],
			&f[5], &f[6], &f[7], &f[8],
			&r.SourceFile, &r.SourceRow, &r.ImportBatch); err != nil {
			return nil, fmt.Errorf("fees: 读取台账行: %w", err)
		}
		if recordNo.Valid {
			v := recordNo.String
			r.RecordKey = &v
		}
		if meterNo.Valid {
			v := meterNo.String
			r.MeterNo = &v
		}
		if f[0].Valid {
			r.ReadingStart = &f[0].Float64
		}
		if f[1].Valid {
			r.ReadingEnd = &f[1].Float64
		}
		if f[2].Valid {
			r.MeterPrevEnd = &f[2].Float64
		}
		if swapped.Valid {
			r.MeterSwapped = &swapped.Bool
		}
		if f[3].Valid {
			r.Quantity = &f[3].Float64
		}
		if f[4].Valid {
			r.UnitPrice = &f[4].Float64
		}
		if f[5].Valid {
			r.ShareMobile = &f[5].Float64
		}
		if f[6].Valid {
			r.ShareUnicom = &f[6].Float64
		}
		if f[7].Valid {
			r.ShareTelecom = &f[7].Float64
		}
		if f[8].Valid {
			r.ShareTower = &f[8].Float64
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// escapeSQLString doubles single quotes (site codes come from user input;
// the sibling queries type-constrain their params, the site filter is the
// first string in — escape it, keep the injection surface closed).
func escapeSQLString(s string) string { return strings.ReplaceAll(s, "'", "''") }

func (duckQueryEngine) Duplicates(ctx context.Context, analysisDir string, feeType FeeType, year, month, limit int) ([]DuplicateRow, error) {
	if analysisEmpty(analysisDir) {
		return nil, nil
	}
	db, err := openAnalysis()
	if err != nil {
		return nil, err
	}
	defer func() { _ = db.Close() }()
	// #90: bounded like the sibling presets — an unbounded duplicate scan on
	// a dirty 10k-code universe could materialize the whole pair list; the
	// risk presets have always capped their clue sets, this preset now does
	// too. limit <= 0 falls back to the CLI-visible default (the 20-line
	// preview the docs describe).
	if limit <= 0 {
		limit = 20
	}
	query := fmt.Sprintf(`
SELECT site_code, year, month, count(DISTINCT import_batch) AS batches, count(*) AS rows, sum(amount_cents) AS total_cents
FROM read_parquet('%s', hive_partitioning = true, union_by_name = true)
WHERE fee_type = '%s' %s
GROUP BY site_code, year, month
HAVING count(DISTINCT import_batch) > 1
ORDER BY total_cents DESC, site_code, year, month
LIMIT %d`,
		feeTableGlob(analysisDir), string(feeType), periodFilter(year, month), limit)
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
