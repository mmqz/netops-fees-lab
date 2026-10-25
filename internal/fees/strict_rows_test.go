package fees_test

// strict_rows_test.go — issue #8's --strict-rows: a PRESENT but
// unparseable optional ledger cell fails the row (with its reason) in
// strict mode; lenient keeps importing it with the cell degraded to nil
// (the pre-#8 behavior, byte-compatible) — scenario 9 stays the on-demand
// warning surface.
import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

func writeStrictCSV(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "站址编码,电表编号,实际电量,电费(元),账期\n" +
		"HLJ-0001,M-01,130,11050,2026-01\n" + // good row
		"HLJ-0002,M-02,约一百,20000,2026-01\n" // hand-mangled quantity
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	return path
}

func TestStrictRowsFailsMangledCellsLenientImports(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := writeStrictCSV(t, dir, "mixed.csv")

	// Lenient (default): both rows import; the mangled quantity degrades
	// to nil — the money record never lies about being verified.
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := fees.NewService(db, dir)
	sum, err := svc.Import(context.Background(), fees.ImportOptions{
		Path: path, FeeType: "electricity", Year: 2026, Month: 1,
	})
	if err != nil || sum.Status != "completed" {
		t.Fatalf("lenient import: %v (%+v)", err, sum)
	}
	if sum.RowsImported != 2 {
		t.Fatalf("lenient: rowsImported = %d, want 2", sum.RowsImported)
	}

	// Strict: THE ROW refuses with its reason; the good row lands — the
	// issue's exact semantics ("该行导入失败并列出原因", not a file refusal).
	db2, err := repo.Open(filepath.Join(dir, "strict.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })
	svc2 := fees.NewService(db2, dir)
	sum2, err := svc2.Import(context.Background(), fees.ImportOptions{
		Path: path, FeeType: "electricity", Year: 2026, Month: 1, StrictRows: true,
	})
	if err != nil {
		t.Fatalf("strict import completes with the row refused: %v (%+v)", err, sum2)
	}
	if sum2.Status != "completed" || sum2.RowsImported != 1 || sum2.RowsFailed != 1 {
		t.Fatalf("strict outcome = %+v, want completed 1 imported 1 refused", sum2)
	}
	if len(sum2.Errors) == 0 {
		t.Fatal("the refused row must list its reason")
	}
	found := false
	for _, e := range sum2.Errors {
		if contains(e, "quantity") && contains(e, "约一百") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the error must name the field and the raw value, got %v", sum2.Errors)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestStrictRowsAcceptsCleanFiles: strict mode on a clean file behaves
// exactly like lenient — the switch adds no false refusals.
func TestStrictRowsAcceptsCleanFiles(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "clean.csv")
	body := "站址编码,电表编号,实际电量,单价,电费(元),账期\n" +
		"HLJ-0001,M-01,130,0.85,11050,2026-01\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := fees.NewService(db, dir)
	sum, err := svc.Import(context.Background(), fees.ImportOptions{
		Path: path, FeeType: "electricity", Year: 2026, Month: 1, StrictRows: true,
	})
	if err != nil || sum.Status != "completed" || sum.RowsImported != 1 {
		t.Fatalf("strict on clean file = %+v (%v), want completed 1/1", sum, err)
	}
	_ = testsupport.TempDir
}
