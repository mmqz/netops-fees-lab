//go:build duckdb

package fees

// engine_concurrency_duckdb_test.go — the fee-domain concurrency stress the
// v0.13.0 unverified table promised "阶段 4 风险场景上线前压测" (phases 4-5
// are live since v0.14/v0.15, so the promise is due). The engine's
// concurrency contract is "ONE in-memory DuckDB handle per query call, no
// shared engine state" (engine_duckdb.go header) — safe by construction,
// with #59's general-engine concurrent-read benchmark as the only witness.
// This test is the fee-domain-specific witness: the full read surface
// (Totals / Duplicates / all seven risk scenarios) under sustained
// concurrency against one imported dataset, every worker's result checked
// byte-for-byte against the single-threaded golden pass.
//
// What it proves (and what a failure would mean):
//   - a query result drifting from the golden ⇒ shared mutable engine
//     state or result-object reuse between queries (the contract broken);
//   - an error under load ⇒ handle lifetime or driver registration races;
//   - SQLite-side reads (the link/metric joins of scenarios 1/6/7) are
//     covered too — those run through the repo's WAL-safe read pool, and a
//     BUSY escaping the retry budget would surface as an error here.
//
// The golden comparison is sound because every scenario result is
// deterministic by ruling (#59/#64 determinism ruling: single-batch pairs
// only, fixed anchor order, z rounded to 10 decimals) — a re-run returns
// byte-identical output, and so must every concurrent re-run.

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// TestDuckEngineConcurrentFeeQueries: N workers run the full fee-domain
// query mix repeatedly and concurrently; every result must equal the
// single-threaded golden. Skipped in -short mode (it is a stress test,
// not a correctness unit — the correctness surfaces have their own
// per-scenario tests in engine_risks_duckdb_test.go).
func TestDuckEngineConcurrentFeeQueries(t *testing.T) {
	if testing.Short() {
		t.Skip("fee-domain concurrency stress skipped in -short mode")
	}
	svc, db, dir := newService(t)
	ctx := context.Background()
	importRiskFixture(t, svc)

	// The cross-layer setup of TestRiskPresetsDuck, replicated: two
	// confirmed links (retired/active) + one pending proposal + June
	// metric points for the active site — scenarios 1/6/7 then exercise
	// their SQLite-side joins, not just the pure-Parquet paths.
	seedLinkResource(t, db, "res_retired_site", "RETIRED-SITE", "停用")
	seedLinkResource(t, db, "res_active_site", "ACTIVE-SITE", "在网")
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.FeeSiteLinks.InsertProposal(ctx, domain.FeeSiteLink{
			ID: "fsl_pending_site", FeeSiteCode: "PENDING-SITE", ResourceID: "res_active_site",
			ResourceType: domain.ResourceTypeSite, ResourceKey: "ACTIVE-SITE",
			Grade: domain.LinkGradeFuzzy, Status: domain.LinkStatusPending,
			Source: domain.LinkSourceAuto, CreatedAt: simTSLink,
		})
	}); err != nil {
		t.Fatal(err)
	}
	seedMetricPoints(t, db, "mbat_active_site", "res_active_site",
		"2024-06-10T00:00:00Z", "2024-06-20T00:00:00Z")

	engine := Query(ctx)
	if !engine.Available() {
		t.Fatal("the duckdb build must report Available")
	}
	analysis := filepath.Join(dir, "analysis")
	base := RiskQuery{FeeType: FeeTypeElectricity, Year: 2024}

	// The fee domain's whole read surface, one closure per entry. Results
	// are compared with reflect.DeepEqual — it dereferences the pointer
	// fields (Threshold *float64 etc.) and compares the pointed-to values,
	// while every slice order is pinned by the determinism ruling, so any
	// drift is a real concurrency defect, not serialization noise.
	queries := []struct {
		name string
		run  func() (any, error)
	}{
		{"totals-may", func() (any, error) {
			return engine.Totals(ctx, analysis, FeeTypeElectricity, 2024, 5, 0)
		}},
		{"totals-year", func() (any, error) {
			return engine.Totals(ctx, analysis, FeeTypeElectricity, 2024, 0, 0)
		}},
		{"duplicates", func() (any, error) {
			return engine.Duplicates(ctx, analysis, FeeTypeElectricity, 2024, 0, 20)
		}},
	}
	for _, sc := range riskScenarios {
		sc := sc
		q := base
		if sc == RiskOutlier {
			q.Month = 5 // the same scope TestRiskPresetsDuck pins
		}
		queries = append(queries, struct {
			name string
			run  func() (any, error)
		}{string(sc), func() (any, error) {
			return svc.Risks(ctx, engine, sc, q)
		}})
	}

	// Golden single-threaded pass (also proves the fixture once, so a
	// broken setup fails BEFORE the stress runs, not inside a worker).
	golden := make([]any, len(queries))
	for i, q := range queries {
		got, err := q.run()
		if err != nil {
			t.Fatalf("golden %s: %v", q.name, err)
		}
		golden[i] = got
	}

	// Stress: workers × passes × the full mix. Workers capped at 8 (the
	// point is sustained concurrency, not a core-count stress); passes
	// tuned so the whole test stays a few seconds on a laptop while still
	// giving each worker multiple sequential full-surface sweeps.
	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	if workers < 2 {
		workers = 2
	}
	const passes = 3

	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		firstErr string
	)
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := 0; p < passes; p++ {
				for i, q := range queries {
					got, err := q.run()
					if err != nil {
						errMu.Lock()
						if firstErr == "" {
							firstErr = fmt.Sprintf("%s: %v", q.name, err)
						}
						errMu.Unlock()
						return
					}
					if !reflect.DeepEqual(got, golden[i]) {
						errMu.Lock()
						if firstErr == "" {
							firstErr = fmt.Sprintf("%s drifted under concurrency:\n got: %+v\nwant: %+v", q.name, got, golden[i])
						}
						errMu.Unlock()
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)

	if firstErr != "" {
		t.Fatalf("concurrent fee-domain stress: %s", firstErr)
	}
	n := workers * passes * len(queries)
	t.Logf("fee-domain concurrency stress: %d workers × %d passes × %d queries = %d in %v (%.0f/s), every result byte-identical to the single-threaded golden",
		workers, passes, len(queries), n, elapsed, float64(n)/elapsed.Seconds())
}
