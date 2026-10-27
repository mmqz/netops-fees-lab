//go:build !duckdb

package fees

import "context"

// verify_batch_health_std.go — the default build's #32 data-face stub.
// The Go-logic walk (service.go) queues targets and this package's
// duckdb-tagged sibling runs the money-health aggregate; the default
// build cannot open DuckDB at all, so the function answers the honest
// empty result. The caller (VerifyBatches) only reaches it behind
// engine.Available() — the 501 doctrine lives one level up, where the
// report's DataChecksSkipped flag names the skipped face instead of
// pretending it ran clean.

// verifyBatchDataHealth on the default build: no DuckDB, no aggregate —
// the call is unreachable by construction (Available() gate), and an
// empty result keeps the compile green without fabricating checks.
func (s *Service) verifyBatchDataHealth(ctx context.Context, targets []batchDataTarget) ([]VerifyBatchProblem, error) {
	return nil, nil
}
