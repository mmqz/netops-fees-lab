package fees

// bench_test.go — the #33 B1/B2/B7 import-and-write benchmarks (both
// builds: the import pipeline and the details ledger are pure Go — only
// the QUERY benchmarks need the engine, see bench_duckdb_test.go).
//
// Method discipline (the issue's §5.3):
//   - fixed synthetic data, deterministic content (the same corpus on
//     every run — committed baseline comparisons stay meaningful);
//   - b.ReportAllocs() everywhere (memory is half the story);
//   - b.SetBytes carries the row count so the report reads rows/sec;
//   - fresh DB per import iteration (a first import is what a benchmark
//     of the import must measure — a same-file re-import answers the
//     idempotent duplicate sentinel in microseconds and measures
//     nothing); the per-iteration DB open/close sits OUTSIDE the timed
//     window.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/xuri/excelize/v2"
)

// benchCSV writes one synthetic electricity CSV of n data rows (site
// S-00001..n, period 2026-01, amount i mod 1000 yuan) to dir and returns
// its path — deterministic content, the same corpus on every run.
func benchCSV(b *testing.B, dir string, n int) string {
	b.Helper()
	var body strings.Builder
	body.WriteString("站址编码,账期,电费(元)\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&body, "S-%05d,2026-01,%d.00\n", i, i%1000+1)
	}
	p := filepath.Join(dir, fmt.Sprintf("bench-%d.csv", n))
	if err := os.WriteFile(p, []byte(body.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	return p
}

// freshBenchService opens a throwaway DB+service (untimed); the caller
// closes the DB when the iteration ends.
func freshBenchService(b *testing.B, dir string) (*Service, *repo.DB) {
	b.Helper()
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		b.Fatal(err)
	}
	return NewService(db, dir), db
}

// benchImportOnce times ONE full first-import into a fresh DB (B1's core
// shape — used by all three scale points).
func benchImportOnce(b *testing.B, rows int) {
	dir := b.TempDir()
	p := benchCSV(b, dir, rows)
	b.ReportAllocs()
	b.SetBytes(int64(rows))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		itDir := filepath.Join(dir, fmt.Sprintf("it-%d", i))
		if err := os.MkdirAll(itDir, 0o755); err != nil {
			b.Fatal(err)
		}
		svc, db := freshBenchService(b, itDir)
		b.StartTimer()

		if _, err := svc.Import(context.Background(), ImportOptions{
			Path: p, FeeType: FeeTypeElectricity, Year: 2026, Month: 1,
		}); err != nil {
			b.Fatal(err)
		}

		b.StopTimer()
		_ = db.Close()
		b.StartTimer()
	}
}

// BenchmarkImportCSV1k is B1 at the 1k-row scale point.
func BenchmarkImportCSV1k(b *testing.B) { benchImportOnce(b, 1000) }

// BenchmarkImportCSV10k is B1 at the 10k-row scale point.
func BenchmarkImportCSV10k(b *testing.B) { benchImportOnce(b, 10000) }

// BenchmarkImportCSV100k is B1 at the 100k-row scale point (the real
// monthly-ledger scale the wild fixtures approach).
func BenchmarkImportCSV100k(b *testing.B) { benchImportOnce(b, 100000) }

// benchXLSXSettlement builds ONCE the 157-column tower settlement shape
// (the #29 real sheet: 站址编码 + the 产品服务费合计 family with the
// all-zero decoy variants + 罚责赠费合计 + filler columns to 157) with 454
// data rows — the issue's B2 corpus. The workbook build sits in setup so
// the timed window is the IMPORT, not the file generation.
func benchXLSXSettlement(b *testing.B, dir string, rows int) string {
	b.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	headers := []string{"站址编码", "账期月份"}
	// the scoped family (口径 priority order) + the decoy siblings.
	headers = append(headers,
		"产品服务费合计\n（出账费用）（不含税）", // the Alt+Enter wrap, the #29 shape
		"产品服务费合计(出账费用)(历史调增)",
		"产品服务费合计(历史待退回)",
		"罚责赠费合计",
	)
	for len(headers) < 157 {
		headers = append(headers, fmt.Sprintf("填充列%d", len(headers)+1))
	}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellValue(sheet, cell, h); err != nil {
			b.Fatal(err)
		}
	}
	for r := 1; r <= rows; r++ {
		cell, _ := excelize.CoordinatesToCellName(1, r+1)
		if err := f.SetCellValue(sheet, cell, fmt.Sprintf("B-%05d", r)); err != nil {
			b.Fatal(err)
		}
		per, _ := excelize.CoordinatesToCellName(2, r+1)
		if err := f.SetCellValue(sheet, per, fmt.Sprintf("2026-%02d", r%12+1)); err != nil {
			b.Fatal(err)
		}
		amt, _ := excelize.CoordinatesToCellName(3, r+1)
		if err := f.SetCellValue(sheet, amt, float64(r%1000+1)); err != nil {
			b.Fatal(err)
		}
	}
	p := filepath.Join(dir, "bench-settlement.xlsx")
	if err := f.SaveAs(p); err != nil {
		b.Fatal(err)
	}
	return p
}

// BenchmarkImportXLSXSettlement is B2: the 454-row × 157-column tower
// settlement import (read + recognize through the scoped 口径 table +
// normalize + snapshot + parquet) — fresh first-import per iteration,
// workbook built once in setup.
func BenchmarkImportXLSXSettlement(b *testing.B) {
	dir := b.TempDir()
	p := benchXLSXSettlement(b, dir, 454)
	b.ReportAllocs()
	b.SetBytes(454)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		itDir := filepath.Join(dir, fmt.Sprintf("it-%d", i))
		if err := os.MkdirAll(itDir, 0o755); err != nil {
			b.Fatal(err)
		}
		svc, db := freshBenchService(b, itDir)
		b.StartTimer()

		if _, err := svc.Import(context.Background(), ImportOptions{
			Path: p, FeeType: FeeTypeTowerLease, Year: 2026, Month: 1,
		}); err != nil {
			b.Fatal(err)
		}

		b.StopTimer()
		_ = db.Close()
		b.StartTimer()
	}
}

// BenchmarkDetailWrite is B7: the details ledger's write path (validate
// the three mandates + insert one pending row) — one AddDetail per
// iteration, distinct linkKey per row so the uniqueness face never
// short-circuits the insert.
func BenchmarkDetailWrite(b *testing.B) {
	dir := b.TempDir()
	svc, db := freshBenchService(b, dir)
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	cents := int64(12345)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.AddDetail(ctx, DetailInput{
			LinkKey:     fmt.Sprintf("BENCH-%06d", i),
			Period:      "202601",
			FeeType:     "electricity",
			Category:    "电费",
			AmountCents: &cents,
			ItemName:    "基准明细",
			Reason:      "bench",
		}, "bench-actor"); err != nil {
			b.Fatal(err)
		}
	}
}
