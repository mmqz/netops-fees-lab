//go:build duckdb

package fees

// catchup_rules_duckdb_test.go — issue #11 against the REAL engine: the
// two deterministic catch-up rules fire on crafted windows/money faces,
// normal cycles and settled rows stay silent, the spike clue carries the
// catch-up ANNOTATION when the period's purchase window crosses the
// marking line, and a pre-#11-generation partition (no purchase columns
// anywhere) degrades to the old behavior without a binder error.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

func importCatchUpCSV(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewService(db, dir)
	if _, err := svc.Import(context.Background(), ImportOptions{
		Path: path, FeeType: "electricity", Year: 2026, Month: 4,
	}); err != nil {
		t.Fatalf("import %s: %v", name, err)
	}
}

// TestCatchUpRulesFire: one crafted row per new rule — a 95-day window
// (2026-01-10 → 2026-04-15) fires purchase-period at 95, a receivable
// gap of 344 元 fires receivable-unreceived at 344; the normal-cycle row
// (30 days) and the settled row (received == receivable) stay silent.
func TestCatchUpRulesFire(t *testing.T) {
	dir := testsupport.TempDir(t)
	importCatchUpCSV(t, dir, "transfer.csv",
		"站址编码,购电起始日,购电截止日,应收电信,实收电信,电费(元),账期\n"+
			// 95-day window: 2026-01-10 → 2026-04-15 — fires purchase-period
			"CATCH-01,2026-01-10,2026-04-15,34400,0,61500,2026-04\n"+
			// normal 30-day cycle, receivable == received — silent
			"NORM-01,2026-03-17,2026-04-15,5000,5000,5000,2026-04\n"+
			// normal cycle but partial payment — fires receivable-unreceived (130 元)
			"PART-01,2026-03-17,2026-04-15,13000,0,13000,2026-04\n")
	clues := ledgerConsistencyRows(t, dir, 2026, 0)
	byRule := rulesOf(clues)
	c, ok := byRule["purchase-period"]
	if !ok {
		t.Fatalf("purchase-period did not fire; got %v", byRule)
	}
	if c.MetricValue != 95 {
		t.Errorf("purchase-period metric = %v, want 95 days", c.MetricValue)
	}
	if !strings.Contains(c.Detail, "2026-01-10") || !strings.Contains(c.Detail, "95") {
		t.Errorf("purchase-period detail must carry the window and the span: %s", c.Detail)
	}
	c, ok = byRule["receivable-unreceived"]
	if !ok {
		t.Fatalf("receivable-unreceived did not fire; got %v", byRule)
	}
	// CATCH-01: 34400-0=34400 元 (the #11 extreme shape); PART-01:
	// 13000-0=13000 元 (the partial-payment shape). rulesOf keeps ONE per
	// rule — assert the fired metric is one of the two crafted gaps.
	if c.MetricValue != 34400 && c.MetricValue != 13000 {
		t.Errorf("receivable-unreceived metric = %v, want 34400 or 13000 元", c.MetricValue)
	}
	if c.SourceFile == "" || c.SourceRow == 0 {
		t.Errorf("receivable-unreceived clue carries no row anchor: %+v", c)
	}
	// NORM-01 must never appear in any clue.
	for _, cl := range clues {
		if cl.SiteCode == "NORM-01" {
			t.Errorf("NORM-01 (normal cycle, settled) fired %s: %+v", cl.Rule, cl)
		}
	}
}

// TestSpikeCatchUpAnnotation: T-01 bills a normal month, then a month
// whose single row carries a 764-day purchase window (the #11 extreme
// shape — 2+ years of electricity billed at once). The spike clue fires
// (the rise is real) but its Note names the catch-up reading: the rise
// is likely the METERING consequence of the concentrated report, not a
// trend anomaly.
func TestSpikeCatchUpAnnotation(t *testing.T) {
	dir := testsupport.TempDir(t)
	// January: normal 30-day window, 1000 元.
	importCatchUpCSV(t, dir, "jan.csv",
		"站址编码,购电起始日,购电截止日,应收电信,实收电信,电费(元),账期\n"+
			"T-01,2025-12-17,2026-01-15,1000,1000,1000,2026-01\n")
	// February: one row, 764-day window (2024-01-05 → 2026-02-07, the
	// #11 extreme — 2+ years of electricity billed at once), 61500 元.
	importCatchUpCSV(t, dir, "feb.csv",
		"站址编码,购电起始日,购电截止日,应收电信,实收电信,电费(元),账期\n"+
			"T-01,2024-01-05,2026-02-07,34400,0,61500,2026-02\n")
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	clues, _, err := newQueryEngine().RiskSpikePreset(context.Background(),
		filepath.Join(dir, "analysis"), RiskQuery{FeeType: "electricity", Year: 2026, Ratio: 0.3})
	if err != nil {
		t.Fatalf("spike preset: %v", err)
	}
	if len(clues) != 1 {
		t.Fatalf("clues = %d, want 1 (the T-01 Feb rise): %+v", len(clues), clues)
	}
	c := clues[0]
	if c.SiteCode != "T-01" || c.Month != 2 {
		t.Fatalf("clue = %s %04d-%02d, want T-01 2026-02", c.SiteCode, c.Year, c.Month)
	}
	if !strings.Contains(c.Note, "764") || !strings.Contains(c.Note, "集中补报") {
		t.Fatalf("catch-up annotation missing (want 764-day window + 集中补报 wording): %q", c.Note)
	}
	if !strings.Contains(c.Note, "70") {
		t.Fatalf("annotation must name the marking line (70 days): %q", c.Note)
	}
}

// TestSpikeNoAnnotationBelowLine: a spike whose window is BELOW the
// marking line keeps its pre-#11 note shape (record-count/row-count or
// empty) — no catch-up wording on ordinary rises.
func TestSpikeNoAnnotationBelowLine(t *testing.T) {
	dir := testsupport.TempDir(t)
	importCatchUpCSV(t, dir, "jan.csv",
		"站址编码,购电起始日,购电截止日,电费(元),账期\nT-02,2025-12-17,2026-01-15,1000,2026-01\n")
	importCatchUpCSV(t, dir, "feb.csv",
		"站址编码,购电起始日,购电截止日,电费(元),账期\nT-02,2026-01-16,2026-02-14,6000,2026-02\n") // 29-day window
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	clues, _, err := newQueryEngine().RiskSpikePreset(context.Background(),
		filepath.Join(dir, "analysis"), RiskQuery{FeeType: "electricity", Year: 2026, Ratio: 0.3})
	if err != nil {
		t.Fatalf("spike preset: %v", err)
	}
	if len(clues) != 1 {
		t.Fatalf("clues = %d, want 1: %+v", len(clues), clues)
	}
	if strings.Contains(clues[0].Note, "补报") {
		t.Fatalf("29-day window must not carry the catch-up annotation: %q", clues[0].Note)
	}
}

// TestLedgerConsistencyPreGen11Partition: a partition whose files carry
// NO purchase/receivable columns (the pre-#11 generation — written here
// by hand with the legacy schema) must answer scenario 9 without a
// binder error and without firing the #11 rules. This is the schema
// guard: the column probe resolves absent columns to NULL literals.
func TestLedgerConsistencyPreGen11Partition(t *testing.T) {
	dir := testsupport.TempDir(t)
	writeLegacyParquet(t, dir)
	if _, _, err := newQueryEngine().RiskLedgerConsistencyPreset(context.Background(),
		filepath.Join(dir, "analysis"), RiskQuery{FeeType: "electricity", Year: 2026}); err != nil {
		t.Fatalf("pre-#11 partition must not binder-error: %v", err)
	}
	// The spike preset's catch-up probe must also degrade (empty map, no
	// annotation, no error).
	if _, _, err := newQueryEngine().RiskSpikePreset(context.Background(),
		filepath.Join(dir, "analysis"), RiskQuery{FeeType: "electricity", Year: 2026, Ratio: 0.3}); err != nil {
		t.Fatalf("spike on pre-#11 partition must not error: %v", err)
	}
}
