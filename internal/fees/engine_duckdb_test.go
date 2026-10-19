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
	for _, f := range []struct{ name, body string }{
		{"a.csv", "站址编码,账期,电费(元)\nS1,2024-05,100.00\nS2,2024-05,50.00\n"},
		{"b.csv", "站址编码,账期,电费(元)\nS1,2024-05,300.00\n"},
		{"c.csv", "站址编码,账期,电费(元)\nS1,2024-06,70.00\n"},
	} {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Import(ctx, ImportOptions{Path: p, FeeType: FeeTypeElectricity, Year: 2024, Month: 5}); err != nil {
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
	if totals[0].SiteCode != "S1" || totals[0].Rows != 2 || totals[0].Total != 400 {
		t.Errorf("S1 totals = %+v, want 2 rows / 400.00", totals[0])
	}
	if totals[1].SiteCode != "S2" || totals[1].Total != 50 {
		t.Errorf("S2 totals = %+v, want 50.00", totals[1])
	}
	// year scope (month 0): includes June.
	totals, err = e.Totals(ctx, analysis, FeeTypeElectricity, 2024, 0, 0)
	if err != nil || len(totals) != 2 || totals[0].Total != 470 {
		t.Fatalf("year totals = %+v (err %v), want S1 = 470.00", totals, err)
	}

	// Duplicates: exactly S1@2024-05 (2 batches, 2 rows, 400 total);
	// S2 and June are single-batch, no signal.
	dups, err := e.Duplicates(ctx, analysis, FeeTypeElectricity, 2024, 0)
	if err != nil {
		t.Fatalf("Duplicates: %v", err)
	}
	if len(dups) != 1 {
		t.Fatalf("duplicates = %+v, want exactly the S1@2024-05 pair", dups)
	}
	d := dups[0]
	if d.SiteCode != "S1" || d.Year != 2024 || d.Month != 5 || d.Batches != 2 || d.Rows != 2 || d.Total != 400 {
		t.Errorf("duplicate row = %+v, want S1 2024-05 / 2 batches / 2 rows / 400.00", d)
	}
}
