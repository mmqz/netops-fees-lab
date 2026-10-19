package fees

import (
	"context"
	"fmt"
	"path/filepath"
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

// TotalsRow is one site's aggregate of a period. TotalCents is INTEGER
// CENTS (#65 P1-a): the JSON contract keeps money integral — the display
// layer divides by 100, the data layer never multiplies by a float.
type TotalsRow struct {
	SiteCode   string `json:"siteCode"`
	Rows       int    `json:"rows"`
	TotalCents int64  `json:"totalCents"`
}

// DuplicateRow is one duplicate-settlement candidate: the same site billed
// in the same period by DIFFERENT import batches, with the per-batch
// evidence anchors (batch ids + row numbers). TotalCents: integer-cents
// contract (#65 P1-a) — a duplicate comparison must never depend on the
// summation order.
type DuplicateRow struct {
	SiteCode   string `json:"siteCode"`
	Year       int    `json:"year"`
	Month      int    `json:"month"`
	Batches    int    `json:"batches"`
	Rows       int    `json:"rows"`
	TotalCents int64  `json:"totalCents"`
}

// SitePeriodTotal is one (site, period) aggregate of the fee side (the
// cross-layer scenarios' fee half).
type SitePeriodTotal struct {
	SiteCode   string
	Year       int
	Month      int
	Rows       int
	TotalCents int64
}

// SiteTotal is one site's aggregate across the query scope (scenario 6's
// fee half — period-agnostic).
type SiteTotal struct {
	SiteCode   string
	Rows       int
	TotalCents int64
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

	// ---- the six risk scenarios (#64, #58 stage 4) ----
	// The four pure-Parquet presets return BARE clues (no evidence
	// anchors — the service fetches those uniformly); the second return
	// is the excluded multi-batch (site, period) count (scenarios 4/5;
	// 0 for 2/3).

	// RiskDuplicatesPreset (scenario 2): (site, period) pairs billed
	// by more than one batch. Metric = batch count.
	RiskDuplicatesPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error)
	// RiskLedgerMismatchPreset (scenario 3): (site, period) pairs
	// whose per-batch totals differ by more than DiffCents. Metric =
	// max-min of the per-batch totals (cents).
	RiskLedgerMismatchPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error)
	// RiskSpikePreset (scenario 4): single-batch (site, period)
	// totals whose month-over-period rise ratio reaches Ratio.
	// Metric = the rise ratio.
	RiskSpikePreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error)
	// RiskOutlierPreset (scenario 5): single-batch (site, period)
	// totals whose z-score against the same-period peers reaches
	// ZScore (two-sided; metric = the signed z). Requires at least
	// MinSites peers in the period.
	RiskOutlierPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error)

	// ---- the cross-layer scenarios' fee-side primitives ----

	// SitePeriodTotals returns every (site, period) total of the fee
	// type within the scope (month 0 = the whole year) — scenario 1's
	// fee half.
	SitePeriodTotals(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]SitePeriodTotal, error)
	// SiteTotalsAll returns every site's total across the scope
	// (month 0 = the whole year) — scenario 6's fee half.
	SiteTotalsAll(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]SiteTotal, error)
	// EvidenceFor returns up to maxPer row-level evidence anchors per
	// key (site + period; key Month 0 = all months of the key's
	// year). Deterministic anchor order: (import_batch, source_row,
	// amount_cents).
	EvidenceFor(ctx context.Context, analysisDir string, feeType FeeType, keys []EvidenceKey, maxPer int) (map[EvidenceKey][]EvidenceAnchor, error)
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

// analysisEmpty reports whether the analysis dir carries no Parquet files
// at all (fresh install, nothing imported yet). The engines answer EMPTY
// results for that case — no data is not an engine failure — instead of
// letting DuckDB's glob throw an IO error at the caller (#71 review
// follow-up: the MCP/API engine-state tests run against empty dirs and
// the duckdb build answered 500-IO instead of an honest empty list).
// A glob error reads as NON-empty: the real query then surfaces the
// underlying problem, we never mask it here.
func analysisEmpty(analysisDir string) bool {
	matches, _ := filepath.Glob(feeTableGlob(analysisDir))
	return len(matches) == 0
}
