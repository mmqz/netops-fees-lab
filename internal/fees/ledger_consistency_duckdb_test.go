//go:build duckdb

package fees

// ledger_consistency_duckdb_test.go — issue #5 scenario 9 against the
// REAL engine: each of the four deterministic rules fires on crafted
// data, the honest exclusions hold (multi-party rows never judged for
// reading-gap; swap-flagged chains never clues), absent columns skip
// their rules, and the rows query returns the optional values.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

func importLedgerCSV(t *testing.T, dir, name, body string) {
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
		Path: path, FeeType: "electricity", Year: 2026, Month: 1,
	}); err != nil {
		t.Fatalf("import %s: %v", name, err)
	}
}

// ledgerConsistencyRows runs scenario 9 on the given period.
func ledgerConsistencyRows(t *testing.T, dir string, year, month int) []LedgerConsistencyClue {
	t.Helper()
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clues, _, err := newQueryEngine().RiskLedgerConsistencyPreset(context.Background(),
		filepath.Join(dir, "analysis"), RiskQuery{FeeType: "electricity", Year: year, Month: month})
	if err != nil {
		t.Fatalf("preset: %v", err)
	}
	return clues
}

func rulesOf(clues []LedgerConsistencyClue) map[string]LedgerConsistencyClue {
	out := make(map[string]LedgerConsistencyClue, len(clues))
	for _, c := range clues {
		out[c.Rule] = c // one crafted row per rule
	}
	return out
}

// TestLedgerConsistencyFourRules: one crafted row per rule in one batch —
// all four fire with their metrics and row anchors.
func TestLedgerConsistencyFourRules(t *testing.T) {
	dir := testsupport.TempDir(t)
	importLedgerCSV(t, dir, "rules.csv",
		"站址编码,电表编号,起始读数,截止读数,实际电量,移动分摊比例,联通分摊比例,电信分摊比例,铁塔分摊比例,电费(元),账期\n"+
			// reading-gap: 1410-1280=130 != 100 (gap 30 > 0.5), no shares
			"GAP-01,M-01,1280,1410,100,,,,,5000,2026-01\n"+
			// ratio-sum: 0.4+0.3+0.2+0.05 = 0.95 (delta 0.05 > 0.005)
			"SUM-01,,,,,0.4,0.3,0.2,0.05,3000,2026-01\n"+
			// zero-quantity-billing: quantity 0, amount 8000
			"ZERO-01,,,,0,,0.5,0.5,,8000,2026-01\n")
	// chain-break needs two periods for the same meter.
	importTwoPeriod := func(name string) {
		db, err := repo.Open(filepath.Join(dir, "netops.db"))
		if err != nil {
			t.Fatalf("repo.Open: %v", err)
		}
		defer func() { _ = db.Close() }()
		svc := NewService(db, dir)
		path := filepath.Join(dir, name)
		body := "站址编码,电表编号,起始读数,截止读数,实际电量,电费(元),账期\n" +
			"CHAIN-01,M-02,100,200,100,5000,2026-01\n" +
			"CHAIN-01,M-02,500,600,100,5000,2026-02\n" // reading_start 500 != prev 200, no swap flag
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Import(context.Background(), ImportOptions{
			Path: path, FeeType: "electricity", Year: 2026, Month: 1,
		}); err != nil {
			t.Fatalf("import chain: %v", err)
		}
	}
	importTwoPeriod("chain.csv")

	clues := ledgerConsistencyRows(t, dir, 2026, 0)
	byRule := rulesOf(clues)
	for _, rule := range []string{"reading-gap", "ratio-sum", "chain-break", "zero-quantity-billing"} {
		if _, ok := byRule[rule]; !ok {
			t.Errorf("rule %s did not fire; got %v", rule, byRule)
		}
	}
	if c, ok := byRule["reading-gap"]; !ok || c.MetricValue != 30 {
		t.Errorf("reading-gap metric = %+v, want 30", c)
	}
	if c, ok := byRule["ratio-sum"]; !ok || c.MetricValue < 0.049 || c.MetricValue > 0.051 {
		t.Errorf("ratio-sum metric = %+v, want ~0.05", c)
	}
	if c := byRule["zero-quantity-billing"]; c.MetricValue != 8000 {
		t.Errorf("zero-quantity metric = %v, want 8000 元", c.MetricValue)
	}
	if c := byRule["chain-break"]; c.MetricValue != 300 {
		t.Errorf("chain-break metric = %v, want 300", c.MetricValue)
	}
	// Row-level evidence rides every clue.
	for rule, c := range byRule {
		if c.SourceFile == "" || c.SourceRow == 0 {
			t.Errorf("rule %s clue carries no row anchor: %+v", rule, c)
		}
	}
}

// TestLedgerConsistencyExclusions: the two honest exclusions — multi-party
// meters are never judged for reading-gap (#8 §0), and swap-flagged chains
// are never clues.
func TestLedgerConsistencyExclusions(t *testing.T) {
	dir := testsupport.TempDir(t)
	importLedgerCSV(t, dir, "multi.csv",
		"站址编码,电表编号,起始读数,截止读数,实际电量,移动分摊比例,电信分摊比例,电费(元),账期\n"+
			// multi-party: readings differ from this party's quantity BY
			// DESIGN (读数差=全表用量, 实际电量=本方份额) — must NOT fire
			"MULTI-01,M-01,1280,1410,100,0.4,0.6,5000,2026-01\n"+
			// zero fires (nothing excludes it)
			"ZERO-02,,,,0,,,6000,2026-01\n")
	clues := ledgerConsistencyRows(t, dir, 2026, 0)
	for _, c := range clues {
		if c.Rule == "reading-gap" {
			t.Fatalf("multi-party meter fired reading-gap (the #8 §0 false-positive machine): %+v", c)
		}
	}
	if len(rulesOf(clues)) == 0 {
		t.Fatal("the zero row must still fire")
	}

	// Swap-flagged chain: same meter, jump, but 是否换表=是 — never a clue.
	dir2 := testsupport.TempDir(t)
	db, err := repo.Open(filepath.Join(dir2, "netops.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	svc := NewService(db, dir2)
	path := filepath.Join(dir2, "swap.csv")
	body := "站址编码,电表编号,起始读数,截止读数,是否换表,电费(元),账期\n" +
		"SWAP-01,M-03,100,200,否,5000,2026-01\n" +
		"SWAP-01,M-03,5,10,是,5000,2026-02\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(context.Background(), ImportOptions{
		Path: path, FeeType: "electricity", Year: 2026, Month: 1,
	}); err != nil {
		t.Fatalf("import swap: %v", err)
	}
	for _, c := range ledgerConsistencyRows(t, dir2, 2026, 0) {
		if c.Rule == "chain-break" {
			t.Fatalf("swap-flagged chain fired chain-break: %+v", c)
		}
	}
}

// TestLedgerRowsQueryReturnOptionalColumns: the rows query surface of
// acceptance #1 — rich rows answer their optional values, plain rows
// answer nil for the same columns (union_by_name over mixed files).
func TestLedgerRowsQueryReturnOptionalColumns(t *testing.T) {
	dir := testsupport.TempDir(t)
	importLedgerCSV(t, dir, "rich.csv",
		"站址编码,电表编号,起始读数,截止读数,实际电量,单价,电费(元),账期\n"+
			"HLJ-0001,M-01,1280,1410,130,0.85,11050,2026-01\n")
	// a plain pre-#5 file in the SAME partition (mixed generations)
	plain := filepath.Join(dir, "plain.csv")
	if err := os.WriteFile(plain, []byte("站址编码,电费(元),账期\nHLJ-0002,20000,2026-01\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	svc := NewService(db, dir)
	if _, err := svc.Import(context.Background(), ImportOptions{
		Path: plain, FeeType: "electricity", Year: 2026, Month: 1,
	}); err != nil {
		t.Fatalf("import plain: %v", err)
	}
	rows, err := newQueryEngine().LedgerRows(context.Background(),
		filepath.Join(dir, "analysis"), "electricity", 2026, 1, "", "", 100)
	if err != nil {
		t.Fatalf("LedgerRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (rich + plain mixed)", len(rows))
	}
	for i := range rows {
		r := rows[i]
		switch r.SiteCode {
		case "HLJ-0001":
			if r.MeterNo == nil || *r.MeterNo != "M-01" ||
				r.ReadingStart == nil || *r.ReadingStart != 1280 ||
				r.ReadingEnd == nil || *r.ReadingEnd != 1410 ||
				r.Quantity == nil || *r.Quantity != 130 ||
				r.UnitPrice == nil || *r.UnitPrice != 0.85 {
				t.Errorf("rich row lost its optional values: %+v", r)
			}
		case "HLJ-0002":
			if r.MeterNo != nil || r.ReadingStart != nil || r.Quantity != nil || r.UnitPrice != nil {
				t.Errorf("plain row must answer nil optionals, got %+v", r)
			}
		}
	}
}
