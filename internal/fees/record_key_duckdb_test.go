//go:build duckdb

package fees

// record_key_duckdb_test.go — issue #4 against the real engine: the
// duplicate-settlement 口径 becomes the (site, recordKey, period) triple
// (multi-key one-batch sites are NATURAL, never clues; nil-keyed rows
// keep the pair semantics), spike clues ANNOTATE record-count changes
// instead of masquerading as price moves, and the rows query drills down
// by record key.
import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

func importRK(t *testing.T, dir, name string, rows string, month int) {
	t.Helper()
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	svc := NewService(db, dir)
	path := filepath.Join(dir, name)
	header := "站址编码,订单号,电费(元),账期\n"
	if err := os.WriteFile(path, []byte(header+rows), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(context.Background(), ImportOptions{
		Path: path, FeeType: "electricity", Year: 2026, Month: month,
	}); err != nil {
		t.Fatalf("import %s: %v", name, err)
	}
}

// TestDuplicateSettlementRecordKeySemantics: the #4 acceptance matrix —
// one site, several orders, ONE batch: NOT a duplicate (the 1429%
// false-positive family); the same ORDER across two batches: IS one,
// and the clue carries the record key.
func TestDuplicateSettlementRecordKeySemantics(t *testing.T) {
	dir := testsupport.TempDir(t)
	importRK(t, dir, "jan.csv",
		"T-01,OD-1,1880,2026-01\nT-01,OD-2,520,2026-01\nT-01,OD-3,175,2026-01\n", 1)
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	clues, _, err := newQueryEngine().RiskDuplicatesPreset(context.Background(),
		filepath.Join(dir, "analysis"), RiskQuery{FeeType: "electricity", Year: 2026})
	if err != nil {
		t.Fatalf("dup preset: %v", err)
	}
	if len(clues) != 0 {
		t.Fatalf("multi-order one-batch site must NOT be a duplicate (the #4 false-positive family), got %+v", clues)
	}

	// The same order re-imported as another batch: IS the duplicate shape.
	dir2 := testsupport.TempDir(t)
	importRK(t, dir2, "jan-a.csv", "T-02,OD-9,1000,2026-01\n", 1)
	// second file, same order number — a distinct batch
	{
		db2, err := repo.Open(filepath.Join(dir2, "netops.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db2.Close() }()
		svc := NewService(db2, dir2)
		path := filepath.Join(dir2, "jan-b.csv")
		body := "站址编码,订单号,电费(元),账期\nT-02,OD-9,1001,2026-01\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Import(context.Background(), ImportOptions{
			Path: path, FeeType: "electricity", Year: 2026, Month: 1,
		}); err != nil {
			t.Fatalf("import b: %v", err)
		}
	}
	clues, _, err = newQueryEngine().RiskDuplicatesPreset(context.Background(),
		filepath.Join(dir2, "analysis"), RiskQuery{FeeType: "electricity", Year: 2026})
	if err != nil {
		t.Fatalf("dup preset 2: %v", err)
	}
	if len(clues) != 1 {
		t.Fatalf("same-order cross-batch must be exactly one clue, got %+v", clues)
	}
	if clues[0].RecordKey != "OD-9" || clues[0].SiteCode != "T-02" {
		t.Fatalf("clue must carry (site, recordKey): %+v", clues[0])
	}

	// Different orders across batches at one site: NOT duplicates.
	dir3 := testsupport.TempDir(t)
	importRK(t, dir3, "x.csv", "T-03,OD-A,500,2026-01\n", 1)
	{
		db3, err := repo.Open(filepath.Join(dir3, "netops.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db3.Close() }()
		svc := NewService(db3, dir3)
		path := filepath.Join(dir3, "y.csv")
		body := "站址编码,订单号,电费(元),账期\nT-03,OD-B,700,2026-01\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Import(context.Background(), ImportOptions{
			Path: path, FeeType: "electricity", Year: 2026, Month: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	clues, _, err = newQueryEngine().RiskDuplicatesPreset(context.Background(),
		filepath.Join(dir3, "analysis"), RiskQuery{FeeType: "electricity", Year: 2026})
	if err != nil {
		t.Fatalf("dup preset 3: %v", err)
	}
	if len(clues) != 0 {
		t.Fatalf("different orders across batches are business-as-usual, got %+v", clues)
	}
}

// TestSpikeAnnotatesRecordCountChange: the #4 canonical case — one order
// in January, THREE in February (a new order, not a price move): the
// clue still fires but its Note names the record-count change instead of
// pretending the rise is a price anomaly.
func TestSpikeAnnotatesRecordCountChange(t *testing.T) {
	dir := testsupport.TempDir(t)
	importRK(t, dir, "jan.csv", "T-01,OD-1,1000,2026-01\n", 1)
	importRK(t, dir, "feb.csv", "T-01,OD-1,1000,2026-02\nT-01,OD-2,4000,2026-02\n", 2)
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
		t.Fatalf("the rise is real and stays a clue, got %+v", clues)
	}
	c := clues[0]
	if c.Note == "" || !containsCJK(c.Note, "记录键数 1→2") {
		t.Fatalf("clue note = %q, want the record-count annotation 1→2", c.Note)
	}
}

// TestRowsQueryRecordKeyDrillDown: the rows query answers the record
// keys and drills by them (#4 acceptance 2/5).
func TestRowsQueryRecordKeyDrillDown(t *testing.T) {
	dir := testsupport.TempDir(t)
	importRK(t, dir, "jan.csv",
		"T-01,OD-1,1880,2026-01\nT-01,OD-2,520,2026-01\n", 1)
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	all, err := newQueryEngine().LedgerRows(context.Background(),
		filepath.Join(dir, "analysis"), "electricity", 2026, 1, "", "", 100)
	if err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("rows = %d, want 2", len(all))
	}
	for _, r := range all {
		if r.RecordKey == nil {
			t.Fatalf("rows must carry recordKey: %+v", r)
		}
	}
	one, err := newQueryEngine().LedgerRows(context.Background(),
		filepath.Join(dir, "analysis"), "electricity", 2026, 1, "T-01", "OD-2", 100)
	if err != nil {
		t.Fatalf("rows drill: %v", err)
	}
	if len(one) != 1 || one[0].RecordKey == nil || *one[0].RecordKey != "OD-2" {
		t.Fatalf("record-key drill-down broken: %+v", one)
	}
}

func containsCJK(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
