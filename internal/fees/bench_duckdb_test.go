//go:build duckdb

package fees

// bench_duckdb_test.go — the #33 B3–B6 query benchmarks on the REAL
// engine: the monthly series, a single risk scenario, the full ten-
// scenario pass, and the cross-dataset reconcile. The corpus is the
// 12-month × 200-site ledger (both fee types — the reconcile needs both
// sides), seeded ONCE per benchmark outside the timed window; every
// iteration then re-queries the same library, which is exactly what the
// report/trend/cross faces do in production.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// seedQueryBenchLibrary seeds the query-benchmark corpus: 12 months × 200
// sites of electricity + the same shape as tower_lease (amounts offset so
// the reconcile finds a spread, not a zero delta), one import per (type,
// month) — 24 imports, all outside the timed window.
func seedQueryBenchLibrary(b *testing.B, dir string) (*Service, string) {
	b.Helper()
	dbPath := filepath.Join(dir, "netops.db")
	if _, err := os.Stat(dbPath); err == nil {
		b.Fatal("seedQueryBenchLibrary expects a fresh dir")
	}
	svc := newBenchServiceAt(b, dir)
	ctx := context.Background()
	for m := 1; m <= 12; m++ {
		period := fmt.Sprintf("2026-%02d", m)
		var elec, tower strings.Builder
		for s := 1; s <= 200; s++ {
			fmt.Fprintf(&elec, "S-%05d,%s,%d.00\n", s, period, s%97+1)
			fmt.Fprintf(&tower, "S-%05d,%s,%d.00\n", s, period, s%89+2)
		}
		for _, side := range []struct {
			name, body string
			ft         FeeType
		}{
			{"elec", "站址编码,账期,电费(元)\n" + elec.String(), FeeTypeElectricity},
			{"tower", "站址编码,账期,金额\n" + tower.String(), FeeTypeTowerLease},
		} {
			p := filepath.Join(dir, fmt.Sprintf("m%02d-%s.csv", m, side.name))
			if err := os.WriteFile(p, []byte(side.body), 0o644); err != nil {
				b.Fatal(err)
			}
			if _, err := svc.Import(ctx, ImportOptions{
				Path: p, FeeType: side.ft, Year: 2026, Month: m,
			}); err != nil {
				b.Fatalf("seed %s: %v", p, err)
			}
		}
	}
	return svc, filepath.Join(dir, "analysis")
}

// newBenchServiceAt is freshBenchService's duckdb-file twin (kept local so
// the untagged file stays buildable on its own).
func newBenchServiceAt(b *testing.B, dir string) *Service {
	b.Helper()
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	return NewService(db, dir)
}

// BenchmarkQueryMonthly is B3: the whole-year monthly trend series over
// the 2400-row electricity ledger — the trend chart's own query.
func BenchmarkQueryMonthly(b *testing.B) {
	dir := b.TempDir()
	_, analysis := seedQueryBenchLibrary(b, dir)
	ctx := context.Background()
	engine := Query(ctx)
	if !engine.Available() {
		b.Fatal("the duckdb build must report Available")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.MonthlyTotals(ctx, analysis, FeeTypeElectricity, 2026); err != nil {
			b.Fatal(err)
		}
	}
}

// benchmarkRiskScenario times one scenario over the seeded library (B4's
// shape, shared by the single-scenario and full-pass benchmarks).
func benchmarkRiskScenario(b *testing.B, svc *Service, analysis string, sc RiskScenario) {
	ctx := context.Background()
	engine := Query(ctx)
	if !engine.Available() {
		b.Fatal("the duckdb build must report Available")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.Risks(ctx, engine, sc, RiskQuery{FeeType: FeeTypeElectricity, Year: 2026}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRiskSingleScenario is B4: the spike scenario (the heaviest
// single pass — per-site month-over-month aggregation) over the seeded
// library.
func BenchmarkRiskSingleScenario(b *testing.B) {
	dir := b.TempDir()
	svc, _ := seedQueryBenchLibrary(b, dir)
	benchmarkRiskScenario(b, svc, dir, RiskSpike)
}

// BenchmarkRiskAllTen is B5: the full ten-scenario pass (the overview's
// "run all" face) over the seeded library.
func BenchmarkRiskAllTen(b *testing.B) {
	dir := b.TempDir()
	svc, _ := seedQueryBenchLibrary(b, dir)
	ctx := context.Background()
	engine := Query(ctx)
	if !engine.Available() {
		b.Fatal("the duckdb build must report Available")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, sc := range riskScenarios {
			if _, err := svc.Risks(ctx, engine, sc, RiskQuery{FeeType: FeeTypeElectricity, Year: 2026}); err != nil {
				b.Fatal(err)
			}
		}
	}
}

// BenchmarkCrossReconcile is B6: the 200×200 cross-dataset reconcile
// (the canonical-site merge across both fee types) over the seeded
// library.
func BenchmarkCrossReconcile(b *testing.B) {
	dir := b.TempDir()
	svc, _ := seedQueryBenchLibrary(b, dir)
	ctx := context.Background()
	engine := Query(ctx)
	if !engine.Available() {
		b.Fatal("the duckdb build must report Available")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.CrossReconcile(ctx, engine, 2026, 9); err != nil {
			b.Fatal(err)
		}
	}
}
