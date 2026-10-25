package fees_test

// verify_sources_test.go — issue #6: the three honest verdicts (match /
// changed / missing) plus the unknown skip, and the import-time
// writable-source warning.
import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// importOne runs one real single-sheet CSV import through the service and
// returns the path it imported from.
func importOne(t *testing.T, dir string, name, site string, amount string) (string, fees.ImportSummary, error) {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "站址编码,电费(元),账期\n" + site + "," + amount + ",2026-01\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := fees.NewService(db, dir)
	sum, err := svc.Import(context.Background(), fees.ImportOptions{
		Path: path, FeeType: "electricity", Year: 2026, Month: 1,
	})
	return path, sum, err
}

// TestSourcePathRecordedAndVerified pins the full loop: import records the
// absolute source path; verify-sources answers match; editing the file
// flips it to changed; deleting it flips to missing.
func TestSourcePathRecordedAndVerified(t *testing.T) {
	dir := testsupport.TempDir(t)
	path, sum, err := importOne(t, dir, "jan.csv", "HLJ-0001", "100.00")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if sum.Status != "completed" {
		t.Fatalf("import status = %q, want completed", sum.Status)
	}

	dbPath := filepath.Join(dir, "netops.db")
	db, err := repo.Open(dbPath)
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := fees.NewService(db, dir)

	// The recorded path is absolute and points at the source file.
	b, err := db.FeeBatches.GetByID(context.Background(), sum.BatchID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	abs, _ := filepath.Abs(path)
	if b.SourcePath != abs {
		t.Fatalf("recorded source path = %q, want %q", b.SourcePath, abs)
	}

	// Verdict 1: match.
	rep, err := svc.VerifySources(context.Background())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Checked != 1 || rep.Match != 1 || rep.Changed != 0 || rep.Missing != 0 {
		t.Fatalf("fresh verify = %+v, want 1 match", rep)
	}
	if !rep.Results[0].SnapshotIntact {
		t.Fatal("snapshot must hash back to the recorded sha (intact evidence)")
	}

	// Verdict 2: changed — the source file was edited after import.
	if err := os.WriteFile(path, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err = svc.VerifySources(context.Background())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Changed != 1 || rep.Match != 0 {
		t.Fatalf("post-edit verify = %+v, want 1 changed", rep)
	}
	if rep.Results[0].CurrentSHA == "" || rep.Results[0].CurrentSHA == rep.Results[0].RecordedSHA {
		t.Fatal("changed verdict must carry the differing current hash")
	}
	if !rep.Results[0].SnapshotIntact {
		t.Fatal("the snapshot stays intact — that is the whole point")
	}

	// Verdict 3: missing — the source file is gone.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	rep, err = svc.VerifySources(context.Background())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if rep.Missing != 1 {
		t.Fatalf("post-delete verify = %+v, want 1 missing", rep)
	}
}

// TestWritableSourceWarning: a writable source file produces exactly the
// issue #6 advice in Warnings; a read-only source produces none. (POSIX
// write bit — skipped on Windows by design.)
func TestWritableSourceWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX write-bit semantics — Windows skips the check by design")
	}
	dir := testsupport.TempDir(t)
	path, sum, err := importOne(t, dir, "jan.csv", "HLJ-0002", "200.00")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(sum.Warnings) == 0 {
		t.Fatal("a writable source (0o644) must carry the read-only advice")
	}
	// Second import of a read-only copy stays silent about it.
	ro := filepath.Join(dir, "readonly.csv")
	body := "站址编码,电费(元),账期\nHLJ-0003,50.00,2026-01\n"
	if err := os.WriteFile(ro, []byte(body), 0o444); err != nil {
		t.Fatal(err)
	}
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := fees.NewService(db, dir)
	sum2, err := svc.Import(context.Background(), fees.ImportOptions{
		Path: ro, FeeType: "electricity", Year: 2026, Month: 2,
	})
	if err != nil {
		t.Fatalf("import ro: %v", err)
	}
	if len(sum2.Warnings) != 0 {
		t.Fatalf("read-only source must not warn, got %v", sum2.Warnings)
	}
	_ = path
}
