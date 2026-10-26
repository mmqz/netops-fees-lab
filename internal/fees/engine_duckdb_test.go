//go:build duckdb

package fees

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestDuckEngineTotalsAndDuplicates is the #60/#61 acceptance proof on the
// REAL engine: rows imported through the pipeline come back through
// DuckDB's read_parquet(hive_partitioning) with the partition columns
// materialized from the directory, the lineage intact, and the
// cross-batch duplicate preset surfacing exactly the coexisting batches
// (the #59 finding — duplicates are the risk signal, never merged).
func TestDuckEngineTotalsAndDuplicates(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	// two batches covering the same site+period (the duplicate-settlement
	// shape) + one batch of another period.
	// #20: c.csv declares June (its rows land under the declaration).
	for _, f := range []struct {
		name string
		m    int
		body string
	}{
		{"a.csv", 5, "站址编码,账期,电费(元)\nS1,2024-05,100.00\nS2,2024-05,50.00\n"},
		{"b.csv", 5, "站址编码,账期,电费(元)\nS1,2024-05,300.00\n"},
		{"c.csv", 6, "站址编码,账期,电费(元)\nS1,2024-06,70.00\n"},
	} {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Import(ctx, ImportOptions{Path: p, FeeType: FeeTypeElectricity, Year: 2024, Month: f.m}); err != nil {
			t.Fatalf("import %s: %v", f.name, err)
		}
	}

	e := newQueryEngine()
	if !e.Available() {
		t.Fatal("the duckdb build must report Available")
	}
	analysis := filepath.Join(dir, "analysis")

	// Totals: month 5 aggregates across BOTH coexisting batches.
	totals, err := e.Totals(ctx, analysis, FeeTypeElectricity, 2024, 5, 0)
	if err != nil {
		t.Fatalf("Totals: %v", err)
	}
	if len(totals) != 2 {
		t.Fatalf("totals = %+v, want 2 sites", totals)
	}
	if totals[0].SiteCode != "S1" || totals[0].Rows != 2 || totals[0].TotalCents != 40000 {
		t.Errorf("S1 totals = %+v, want 2 rows / 400.00", totals[0])
	}
	if totals[1].SiteCode != "S2" || totals[1].TotalCents != 5000 {
		t.Errorf("S2 totals = %+v, want 50.00", totals[1])
	}
	// year scope (month 0): includes June.
	totals, err = e.Totals(ctx, analysis, FeeTypeElectricity, 2024, 0, 0)
	if err != nil || len(totals) != 2 || totals[0].TotalCents != 47000 {
		t.Fatalf("year totals = %+v (err %v), want S1 = 470.00", totals, err)
	}

	// Duplicates: exactly S1@2024-05 (2 batches, 2 rows, 400 total);
	// S2 and June are single-batch, no signal.
	dups, err := e.Duplicates(ctx, analysis, FeeTypeElectricity, 2024, 0, 20)
	if err != nil {
		t.Fatalf("Duplicates: %v", err)
	}
	if len(dups) != 1 {
		t.Fatalf("duplicates = %+v, want exactly the S1@2024-05 pair", dups)
	}
	d := dups[0]
	if d.SiteCode != "S1" || d.Year != 2024 || d.Month != 5 || d.Batches != 2 || d.Rows != 2 || d.TotalCents != 40000 {
		t.Errorf("duplicate row = %+v, want S1 2024-05 / 2 batches / 2 rows / 400.00", d)
	}
}

// TestDuckEngineMonthlyTotals is the #3 trend primitive's acceptance
// proof: the whole-year series aggregates per month (rows, DISTINCT
// sites, integer-cents total), ascending, with the months that carry no
// data simply ABSENT — the display layer pads gaps, the engine never
// invents zeroes.
func TestDuckEngineMonthlyTotals(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	for _, f := range []struct{ name, body string }{
		{"a.csv", "站址编码,账期,电费(元)\nS1,2024-05,100.00\nS2,2024-05,50.00\n"},
		{"b.csv", "站址编码,账期,电费(元)\nS1,2024-05,300.00\n"},
		{"c.csv", "站址编码,账期,电费(元)\nS1,2024-06,70.00\n"},
		{"d.csv", "站址编码,账期,电费(元)\nS2,2024-08,20.00\n"},
	} {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
		month := 5
		if f.name == "c.csv" {
			month = 6
		}
		if f.name == "d.csv" {
			month = 8
		}
		if _, err := svc.Import(ctx, ImportOptions{Path: p, FeeType: FeeTypeElectricity, Year: 2024, Month: month}); err != nil {
			t.Fatalf("import %s: %v", f.name, err)
		}
	}
	e := newQueryEngine()
	analysis := filepath.Join(dir, "analysis")
	rows, err := e.MonthlyTotals(ctx, analysis, FeeTypeElectricity, 2024)
	if err != nil {
		t.Fatalf("MonthlyTotals: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("monthly = %+v, want exactly months 5/6/8 (absent months absent)", rows)
	}
	want := []MonthlyTotalsRow{
		{Month: 5, Rows: 3, Sites: 2, TotalCents: 45000},
		{Month: 6, Rows: 1, Sites: 1, TotalCents: 7000},
		{Month: 8, Rows: 1, Sites: 1, TotalCents: 2000},
	}
	for i, w := range want {
		if rows[i] != w {
			t.Errorf("monthly[%d] = %+v, want %+v", i, rows[i], w)
		}
	}
	// a year with no data answers an empty series (nil), not an error.
	rows, err = e.MonthlyTotals(ctx, analysis, FeeTypeElectricity, 2030)
	if err != nil {
		t.Fatalf("MonthlyTotals(2030): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("MonthlyTotals(2030) = %+v, want empty", rows)
	}
	// another fee type never touches electricity's series.
	rows, err = e.MonthlyTotals(ctx, analysis, FeeTypeTowerLease, 2024)
	if err != nil {
		t.Fatalf("MonthlyTotals(tower): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("MonthlyTotals(tower) = %+v, want empty (type isolation)", rows)
	}
}
