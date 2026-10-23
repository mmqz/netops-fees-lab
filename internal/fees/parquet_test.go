package fees

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"

	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// TestWriteParquetPartitionsLayout pins the analysis layer's physical
// layout (#60): hive partition dirs (fee_type/year/month), one file per
// batch×period, rows readable back with the pure-Go reader (the write
// path must not depend on the duckdb engine to be verifiable), staging
// removed after commit.
func TestWriteParquetPartitionsLayout(t *testing.T) {
	dir := testsupport.TempDir(t)
	rows := []NormalizedRow{
		{SiteCode: "S1", AmountCents: 10050, Year: 2024, Month: 5, SourceFile: "h_f.csv", SourceRow: 2, ImportBatch: "fee_b1"},
		{SiteCode: "S2", AmountCents: 20000, Year: 2024, Month: 5, SourceFile: "h_f.csv", SourceRow: 3, ImportBatch: "fee_b1"},
		{SiteCode: "S1", AmountCents: 30025, Year: 2024, Month: 6, SourceFile: "h_f.csv", SourceRow: 4, ImportBatch: "fee_b1"},
		{SiteCode: "S3", AmountCents: 5000, Year: 2023, Month: 12, SourceFile: "h_f.csv", SourceRow: 5, ImportBatch: "fee_b1"},
	}
	files, err := writeParquetPartitions(dir, FeeTypeElectricity, "fee_b1", rows)
	if err != nil {
		t.Fatalf("writeParquetPartitions: %v", err)
	}
	// The manifest paths are the LOGICAL ('/') contract (#75): path.Join
	// constants, NOT filepath.Join — the API/CLI echo them verbatim, so
	// the expectation must not flip with the test platform's separator.
	want := []string{
		"fees/fee_type=electricity/year=2023/month=12/batch-fee_b1.parquet",
		"fees/fee_type=electricity/year=2024/month=05/batch-fee_b1.parquet",
		"fees/fee_type=electricity/year=2024/month=06/batch-fee_b1.parquet",
	}
	if len(files) != len(want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	for i, f := range files {
		if f.relPath != want[i] {
			t.Errorf("file[%d] = %q, want %q", i, f.relPath, want[i])
		}
		// The contract never carries the platform separator (#75).
		if strings.Contains(f.relPath, `\`) {
			t.Errorf("file[%d] relPath %q contains a backslash — logical paths are always '/'", i, f.relPath)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f.relPath))); err != nil {
			t.Errorf("partition file missing: %v", err)
		}
	}
	// row counts add up to the input.
	total := 0
	for _, f := range files {
		total += f.rows
	}
	if total != len(rows) {
		t.Errorf("manifest rows total = %d, want %d", total, len(rows))
	}
	// the batch's staging dir is cleaned up (the .staging parent may
	// remain for concurrent batches — it must at least be EMPTY).
	batchStaging := filepath.Join(dir, "fees", ".staging", "fee_b1")
	if _, err := os.Stat(batchStaging); !os.IsNotExist(err) {
		t.Errorf("batch staging dir must be removed after commit (err %v)", err)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "fees", ".staging")); err == nil && len(entries) != 0 {
		t.Errorf(".staging must be empty after the commit, has %d entries", len(entries))
	}
	// read one partition back with the pure-Go reader and verify the
	// payload + lineage columns (hive columns are NOT in the file).
	p := filepath.Join(dir, want[1])
	f, err := os.Open(p)
	if err != nil {
		t.Fatalf("open parquet: %v", err)
	}
	defer func() { _ = f.Close() }()
	r := parquet.NewGenericReader[feeParquetRow](f)
	got := make([]feeParquetRow, 4)
	n, err := r.Read(got)
	if err != nil && n <= 0 { // (n>0, io.EOF) is the normal last-chunk shape
		t.Fatalf("read parquet: %v", err)
	}
	_ = r.Close()
	if n != 2 {
		t.Fatalf("read %d rows, want 2", n)
	}
	if got[0].SiteCode != "S1" || got[0].AmountCents != 10050 || got[0].SourceRow != 2 ||
		got[0].SourceFile != "h_f.csv" || got[0].ImportBatch != "fee_b1" {
		t.Errorf("row[0] = %+v, want S1 100.5 with full lineage", got[0])
	}
	if got[1].SiteCode != "S2" || got[1].SourceRow != 3 {
		t.Errorf("row[1] = %+v, want S2 row 3", got[1])
	}
}

// TestBatchParquetFilesAndRemove pins the rollback delete set: a second
// batch's file in the SAME partition coexists (never merged — the #60
// coexistence ruling), and BatchParquetFiles isolates exactly one batch's
// files.
func TestBatchParquetFilesAndRemove(t *testing.T) {
	dir := testsupport.TempDir(t)
	rowsB1 := []NormalizedRow{{SiteCode: "S1", AmountCents: 100, Year: 2024, Month: 5, SourceRow: 2, ImportBatch: "fee_b1"}}
	rowsB2 := []NormalizedRow{{SiteCode: "S1", AmountCents: 200, Year: 2024, Month: 5, SourceRow: 2, ImportBatch: "fee_b2"}}
	if _, err := writeParquetPartitions(dir, FeeTypeRepair, "fee_b1", rowsB1); err != nil {
		t.Fatalf("write b1: %v", err)
	}
	if _, err := writeParquetPartitions(dir, FeeTypeRepair, "fee_b2", rowsB2); err != nil {
		t.Fatalf("write b2: %v", err)
	}
	files1, err := BatchParquetFiles(dir, "fee_b1")
	if err != nil || len(files1) != 1 || filepath.Base(files1[0]) != "batch-fee_b1.parquet" {
		t.Fatalf("BatchParquetFiles(b1) = %v (err %v), want the single b1 file", files1, err)
	}
	// rolling back b1 removes exactly its file; b2 coexists untouched.
	if err := RemoveParquetFiles(files1); err != nil {
		t.Fatalf("RemoveParquetFiles: %v", err)
	}
	if _, err := os.Stat(files1[0]); !os.IsNotExist(err) {
		t.Errorf("b1 file must be gone (err %v)", err)
	}
	files2, err := BatchParquetFiles(dir, "fee_b2")
	if err != nil || len(files2) != 1 {
		t.Fatalf("b2 must survive b1's rollback: %v (err %v)", files2, err)
	}
	// idempotent remove.
	if err := RemoveParquetFiles(files1); err != nil {
		t.Errorf("re-remove must tolerate missing files: %v", err)
	}
}

// TestSnapshotLayer pins the snapshot layer: content-addressed path
// shape, idempotent same-content writes, hash correctness.
func TestSnapshotLayer(t *testing.T) {
	dir := testsupport.TempDir(t)
	src := filepath.Join(dir, "bill.csv")
	content := []byte("站址编码,电费\nS1,100\n")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sha, err := HashFile(src)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := WriteSnapshot(dir, src, FeeTypeElectricity, 2024, 5, sha, "bill.csv")
	if err != nil {
		t.Fatal(err)
	}
	// The snapshot manifest path is the LOGICAL ('/') contract (#75,
	// #79): a string constant, NOT filepath.Join — the expectation
	// must not flip with the test platform's separator. 3377762 (#75)
	// converted every other fees test expectation; this line was the
	// one missed spot, stable-failing TestSnapshotLayer on Windows.
	want := "fee-imports/electricity/2024/05/" + sha + "_bill.csv"
	if rel != want {
		t.Fatalf("snapshot path = %q, want %q", rel, want)
	}
	if strings.Contains(rel, `\`) {
		t.Fatalf("snapshot path %q contains a backslash — logical paths are always '/'", rel)
	}
	// reading back crosses the FromSlash boundary: disk paths are
	// native, the persisted contract is not.
	got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(want)))
	if err != nil || string(got) != string(content) {
		t.Fatalf("snapshot bytes = %q (err %v), want verbatim copy", got, err)
	}
	// second snapshot of the same content: same path, no error, no dup.
	rel2, err := WriteSnapshot(dir, src, FeeTypeElectricity, 2024, 5, sha, "bill.csv")
	if err != nil || rel2 != rel {
		t.Fatalf("re-snapshot = %q (err %v), want same path %q", rel2, err, rel)
	}
	// no tmp litter.
	entries, _ := os.ReadDir(filepath.Join(dir, "fee-imports", "electricity", "2024", "05"))
	if len(entries) != 1 {
		t.Errorf("snapshot dir entries = %d, want 1 (no .tmp litter)", len(entries))
	}
}
