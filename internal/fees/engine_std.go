//go:build !duckdb

package fees

import "context"

// stdQueryEngine is the default-build stub: the DuckDB engine (go-duckdb,
// CGO) compiles only with the `duckdb` build tag (release builds for
// linux/docker); every preset answers ErrEngineNotBuilt so the import
// pipeline and the write-side tests run on every platform the main binary
// builds on — Windows and CGO-free environments included (the CI windows
// gate stays meaningful; a dedicated linux job exercises the duckdb tag).
type stdQueryEngine struct{}

func newQueryEngine() QueryEngine { return stdQueryEngine{} }

func (stdQueryEngine) Available() bool { return false }

func (stdQueryEngine) Totals(ctx context.Context, analysisDir string, feeType FeeType, year, month, limit int) ([]TotalsRow, error) {
	return nil, ErrEngineNotBuilt
}

func (stdQueryEngine) Duplicates(ctx context.Context, analysisDir string, feeType FeeType, year, month, limit int) ([]DuplicateRow, error) {
	return nil, ErrEngineNotBuilt
}

func (stdQueryEngine) LedgerRows(ctx context.Context, analysisDir string, feeType FeeType, year, month int, site string, limit int) ([]LedgerRow, error) {
	return nil, ErrEngineNotBuilt
}

func (stdQueryEngine) RiskLedgerConsistencyPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]LedgerConsistencyClue, int, error) {
	return nil, 0, ErrEngineNotBuilt
}

// ---- the risk-scenario presets + cross-layer primitives on the std stub:
// every method answers the honest 501 contract (#64 acceptance: default
// builds refuse, duckdb builds answer). ----

func (stdQueryEngine) RiskDuplicatesPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, ErrEngineNotBuilt
}

func (stdQueryEngine) RiskLedgerMismatchPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, ErrEngineNotBuilt
}

func (stdQueryEngine) RiskSpikePreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, ErrEngineNotBuilt
}

func (stdQueryEngine) RiskOutlierPreset(ctx context.Context, analysisDir string, q RiskQuery) ([]RiskClue, int, error) {
	return nil, 0, ErrEngineNotBuilt
}

func (stdQueryEngine) SitePeriodTotals(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]SitePeriodTotal, error) {
	return nil, ErrEngineNotBuilt
}

func (stdQueryEngine) SiteTotalsAll(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]SiteTotal, error) {
	return nil, ErrEngineNotBuilt
}

func (stdQueryEngine) EvidenceFor(ctx context.Context, analysisDir string, feeType FeeType, keys []EvidenceKey, maxPer int) (map[EvidenceKey][]EvidenceAnchor, error) {
	return nil, ErrEngineNotBuilt
}
