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

func (stdQueryEngine) Duplicates(ctx context.Context, analysisDir string, feeType FeeType, year, month int) ([]DuplicateRow, error) {
	return nil, ErrEngineNotBuilt
}
