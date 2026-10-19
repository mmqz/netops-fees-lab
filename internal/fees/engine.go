package fees

import (
	"context"
	"fmt"
)

// The QUERY surface (#58's stage-5 preview, the vertical slice's proof):
// FIXED, parameterized analysis presets over the Parquet table — callers
// can never pass SQL (the same red line that keeps the MCP tools read-only
// and parameterized; DuckDB is an engine, not an open door).
//
// Two presets cover the #60/#61 acceptance surface:
//
//   - Totals: per-site fee totals of one fee type in one period (the
//     sanity/aggregation read — proves partition pruning + lineage).
//   - Duplicates: the same (site, period) paid in MORE THAN ONE batch —
//     THE cross-batch duplicate-settlement risk signal (#59's finding:
//     duplicates must COEXIST as batch lineage, never merge; the query
//     surfaces them, the human investigates with the row-level evidence).
//
// Engine availability: the DuckDB engine (go-duckdb, CGO) compiles behind
// the `duckdb` build tag. Default builds carry the std stub answering
// ErrEngineNotBuilt — the import/write path is unaffected.

// TotalsRow is one site's aggregate of a period.
type TotalsRow struct {
	SiteCode string  `json:"siteCode"`
	Rows     int     `json:"rows"`
	Total    float64 `json:"total"`
}

// DuplicateRow is one duplicate-settlement candidate: the same site billed
// in the same period by DIFFERENT import batches, with the per-batch
// amounts and the evidence anchors (batch ids + row numbers).
type DuplicateRow struct {
	SiteCode string  `json:"siteCode"`
	Year     int     `json:"year"`
	Month    int     `json:"month"`
	Batches  int     `json:"batches"`
	Rows     int     `json:"rows"`
	Total    float64 `json:"total"`
}

// QueryEngine answers the fee analysis presets over the Parquet table at
// analysisDir. Implementations MUST be safe for concurrent use (the HTTP
// surface shares one instance).
type QueryEngine interface {
	// Available reports whether the real engine is compiled in.
	Available() bool
	// Totals returns the per-site totals of one fee type and period
	// (month 0 = the whole year), largest total first, capped at limit
	// (<= 0 → 100).
	Totals(ctx context.Context, analysisDir string, feeType FeeType, year, month, limit int) ([]TotalsRow, error)
	// Duplicates returns the cross-batch duplicate-settlement candidates
	// of one fee type (month 0 = the whole year): every (site, period)
	// whose rows span more than one import batch.
	Duplicates(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]DuplicateRow, error)
}

// Query is the package-level engine entry: NewQueryEngine returns the
// duckdb-tagged implementation when compiled in, else the std stub. Wired
// once at startup (the CLI command / the HTTP handler), shared everywhere.
func Query(ctx context.Context) QueryEngine {
	return newQueryEngine()
}

// normalizeLimit clamps the preset limits.
func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}

// feeTableGlob is the read_parquet glob of the whole fees table (hive
// partitioning on). The path is built from the analysis dir — NEVER from
// caller strings.
func feeTableGlob(analysisDir string) string {
	return fmt.Sprintf("%s/fees/*/*/*/*.parquet", analysisDir)
}
