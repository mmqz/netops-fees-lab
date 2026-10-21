package fees

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// newService opens a fresh DB + service rooted at a temp data dir.
func newService(t *testing.T) (*Service, *repo.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := repo.Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewService(db, dir)
	stamp := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return stamp }
	return svc, db, dir
}

// writeCSV writes a fee CSV for the tests.
func writeCSV(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const csvMay = `站址编码,账期,电费(元),电量(kWh)
0230101ABCDEFG01,2024-05,1,234.56,3200
0230101ABCDEFG02,2024-05,5,678.00,4100
0230101ABCDEFG03,2024-05,9,876.50,5300
`

const csvJune = `站址编码,账期,电费(元),电量(kWh)
0230101ABCDEFG01,2024-06,2,000.00,3400
`

// TestImportPipelineAndIdempotency walks the full vertical slice:
// import → completed batch + snapshot + partitioned parquet with lineage →
// same file re-import = duplicate skip (zero new data) → listed state →
// row counts verifiable.
func TestImportPipelineAndIdempotency(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	src := writeCSV(t, dir, "may-bills.csv", csvMay)

	sum, err := svc.Import(ctx, ImportOptions{
		Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if sum.Status != "completed" || sum.RowsImported != 3 || sum.RowsTotal != 3 || sum.RowsFailed != 0 {
		t.Fatalf("summary = %+v, want 3/3/0 completed", sum)
	}
	if len(sum.Files) != 1 {
		t.Fatalf("files = %v, want the single month=05 partition file", sum.Files)
	}
	// batch row: completed, canonical mapping recorded.
	b, err := db.FeeBatches.GetByID(ctx, sum.BatchID)
	if err != nil || b.Status != FeeStatusCompleted || b.FeeType != FeeTypeElectricity ||
		b.FeeYear != 2024 || b.FeeMonth != 5 || b.RowsImported != 3 || b.CompletedAt == nil {
		t.Fatalf("batch = %+v (err %v)", b, err)
	}
	// snapshot exists and is the evidence anchor. SnapshotPath is the
	// logical ('/') contract (#75): never carries the platform separator,
	// and the disk join converts once via FromSlash.
	if strings.Contains(b.SnapshotPath, `\`) {
		t.Fatalf("snapshotPath %q contains a backslash — logical paths are always '/' (#75)", b.SnapshotPath)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(b.SnapshotPath))); err != nil {
		t.Fatalf("snapshot missing: %v", err)
	}
	// the parquet file carries the lineage (source_row = the CSV data row
	// numbers 2..4, NOT the import order). sum.Files is the logical
	// contract too (#75).
	f, err := os.Open(filepath.Join(dir, "analysis", filepath.FromSlash(sum.Files[0])))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	// read via the duckdb engine in the tagged build; the pure-Go path is
	// covered by parquet_test.go — here the line check is structural
	// (file exists, one partition). Full lineage round-trip: engine tests.

	// idempotent re-import: same file, same key → duplicate skip, zero
	// new files.
	sum2, err := svc.Import(ctx, ImportOptions{
		Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5,
	})
	if err == nil {
		t.Fatalf("re-import must answer ErrDuplicateImport, got %+v", sum2)
	}
	var dup *ErrDuplicateImport
	if !errors.As(err, &dup) || dup.BatchID != sum.BatchID {
		t.Fatalf("re-import err = %v, want ErrDuplicateImport(%s)", err, sum.BatchID)
	}
	if sum2.BatchID != sum.BatchID || !sum2.SkippedDuplicate {
		t.Fatalf("duplicate summary = %+v, want the existing batch surfaced", sum2)
	}
	// still exactly one live parquet file.
	files, err := BatchParquetFiles(filepath.Join(dir, "analysis"), sum.BatchID)
	if err != nil || len(files) != 1 {
		t.Fatalf("live files after re-import = %v (err %v), want 1", files, err)
	}

	// the same CONTENT under a different name is the same hash → still a
	// duplicate (content addressing, not name addressing).
	same := writeCSV(t, dir, "may-bills-copy.csv", csvMay)
	if _, err := svc.Import(ctx, ImportOptions{Path: same, FeeType: FeeTypeElectricity, Year: 2024, Month: 5}); !errors.As(err, &dup) {
		t.Fatalf("same-content different-name import must be a duplicate, got %v", err)
	}

	// listing shows the one completed batch.
	list, err := svc.List(ctx, "")
	if err != nil || len(list) != 1 || list[0].ID != sum.BatchID {
		t.Fatalf("list = %v (err %v), want the single completed batch", list, err)
	}
}

// TestCrossBatchCoexistenceAndDuplicates proves the #60 ruling end to
// end: two different files covering the SAME site+period land as two
// coexisting batches (no merge, no dedup) — the raw material of the
// duplicate-settlement risk signal.
func TestCrossBatchCoexistenceAndDuplicates(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	f1 := writeCSV(t, dir, "biller-a-may.csv", csvMay)
	// same sites, different amounts, a different workbook (content-wise).
	f2 := writeCSV(t, dir, "biller-b-may.csv", `站址编码,账期,电费(元),电量(kWh)
0230101ABCDEFG01,2024-05,1,234.56,3200
`)

	s1, err := svc.Import(ctx, ImportOptions{Path: f1, FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("import f1: %v", err)
	}
	s2, err := svc.Import(ctx, ImportOptions{Path: f2, FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("import f2: %v", err)
	}
	if s1.BatchID == s2.BatchID {
		t.Fatal("different files must land as different batches")
	}
	// both files live in the SAME partition dir — coexistence, not merge.
	part := filepath.Join(dir, "analysis", "fees", "fee_type=electricity", "year=2024", "month=05")
	for _, id := range []string{s1.BatchID, s2.BatchID} {
		if _, err := os.Stat(filepath.Join(part, "batch-"+id+".parquet")); err != nil {
			t.Errorf("batch %s file must coexist in the partition: %v", id, err)
		}
	}
	// both batches completed, listed.
	if list, _ := svc.List(ctx, FeeStatusCompleted); len(list) != 2 {
		t.Fatalf("completed batches = %d, want 2", len(list))
	}
	_ = db
}

// TestRowPeriodOverridesDeclared pins the period semantics: each ROW's
// own period column wins over the declared batch period (a file spanning
// months splits into its partitions).
func TestRowPeriodOverridesDeclared(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	src := writeCSV(t, dir, "spans.csv", `站址编码,账期,电费(元)
S1,2024-05,100.00
S2,2024-06,200.00
S3,2024-06,300.00
`)
	sum, err := svc.Import(ctx, ImportOptions{Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(sum.Files) != 2 {
		t.Fatalf("files = %v, want month=05 + month=06 partitions", sum.Files)
	}
}

// TestImportRowFailuresAreCountedNotFatal: bad rows skip with counted
// errors; good rows land; the snapshot keeps the evidence.
func TestImportRowFailuresAreCountedNotFatal(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	src := writeCSV(t, dir, "dirty.csv", `站址编码,账期,电费(元)
S1,2024-05,100.00
,2024-05,50.00
S2,2024-05,garbage
S3,2024-XX,50.00
S4,2024-05,70.00
`)
	sum, err := svc.Import(ctx, ImportOptions{Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("import must succeed with row failures counted: %v", err)
	}
	if sum.RowsTotal != 5 || sum.RowsImported != 2 || sum.RowsFailed != 3 {
		t.Fatalf("summary = %+v, want 5 total / 2 imported / 3 failed", sum)
	}
	if len(sum.Errors) != 3 {
		t.Fatalf("errors = %v, want 3 row errors", sum.Errors)
	}
}

// TestImportAllRowsFailedIsAFailedBatch: a wholly unparseable file leaves
// a failed batch (retry channel open), no parquet, snapshot intact.
func TestImportAllRowsFailedIsAFailedBatch(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	src := writeCSV(t, dir, "broken.csv", "站址编码,账期,电费(元)\nS1,2024-05,nope\nS2,2024-05,also-nope\n")
	sum, err := svc.Import(ctx, ImportOptions{Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err == nil {
		t.Fatalf("all-failed import must error, got %+v", sum)
	}
	b, gerr := db.FeeBatches.GetByID(ctx, sum.BatchID)
	if gerr != nil || b.Status != FeeStatusFailed || b.RowsFailed != 2 || b.Error == "" {
		t.Fatalf("batch = %+v (err %v), want failed with 2 rows and an error text", b, gerr)
	}
	// retry channel open: fixing the file and re-importing creates a NEW
	// batch that completes (the failed row never blocked the key).
	fixed := writeCSV(t, dir, "fixed.csv", "站址编码,账期,电费(元)\nS1,2024-05,10.00\nS2,2024-05,20.00\n")
	sum2, err2 := svc.Import(ctx, ImportOptions{Path: fixed, FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err2 != nil || sum2.Status != "completed" || sum2.RowsImported != 2 {
		t.Fatalf("retry after failure must succeed: %+v (err %v)", sum2, err2)
	}
}

// TestMissingMappingRefused: no siteCode/amount mapping and no matching
// headers → refuse BEFORE any batch/snapshot lands.
func TestMissingMappingRefused(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	src := writeCSV(t, dir, "unknown.csv", "A,B,C\n1,2,3\n")
	_, err := svc.Import(ctx, ImportOptions{Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err == nil {
		t.Fatal("unmappable file must be refused")
	}
	if list, _ := svc.List(ctx, ""); len(list) != 0 {
		t.Fatalf("no batch rows must exist, got %d", len(list))
	}
	_ = db
}

// TestExplicitMappingWinsOverRecognizer: a header using non-synonym
// names maps via the explicit mapping.
func TestExplicitMappingWinsOverRecognizer(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	src := writeCSV(t, dir, "custom.csv", "资产编号,期间,应付\nX1,2024-05,88.00\n")
	sum, err := svc.Import(ctx, ImportOptions{
		Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5,
		Mapping: map[string]string{"siteCode": "资产编号", "amount": "应付", "period": "期间"},
	})
	if err != nil || sum.RowsImported != 1 {
		t.Fatalf("explicit mapping import = %+v (err %v), want 1 imported", sum, err)
	}
}

// TestRollbackAndGC: rollback deletes the batch's parquet (analysis rows
// gone), keeps the snapshot (evidence), flips the row to rolled_back;
// re-import after rollback completes (the key is free again); the GC
// sweeps orphans (a fake batch file).
func TestRollbackAndGC(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	src := writeCSV(t, dir, "rb.csv", "站址编码,账期,电费(元)\nS1,2024-05,10.00\n")
	sum, err := svc.Import(ctx, ImportOptions{Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	b, err := svc.Rollback(ctx, sum.BatchID)
	if err != nil || b.Status != FeeStatusRolledBack {
		t.Fatalf("rollback = %+v (err %v)", b, err)
	}
	// parquet gone, snapshot kept.
	if files, _ := BatchParquetFiles(filepath.Join(dir, "analysis"), sum.BatchID); len(files) != 0 {
		t.Fatalf("parquet must be gone after rollback: %v", files)
	}
	full, err := db.FeeBatches.GetByID(ctx, sum.BatchID)
	if err != nil || full.SnapshotPath == "" {
		t.Fatalf("snapshot path must survive rollback: %+v (err %v)", full, err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(full.SnapshotPath))); err != nil {
		t.Fatalf("snapshot file must survive rollback: %v", err)
	}
	// re-rollback → conflict.
	if _, err := svc.Rollback(ctx, sum.BatchID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("re-rollback err = %v, want ErrConflict", err)
	}
	// re-import of the same file: the key is free again → completes.
	sum2, err := svc.Import(ctx, ImportOptions{Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil || sum2.Status != "completed" {
		t.Fatalf("re-import after rollback = %+v (err %v), want completed", sum2, err)
	}
	// GC: plant a fake orphan file (batch id that has no completed row)
	// and verify it is swept while the live batch's file survives.
	part := filepath.Join(dir, "analysis", "fees", "fee_type=electricity", "year=2024", "month=05")
	orphan := filepath.Join(part, "batch-fee_nope.parquet")
	if err := os.WriteFile(orphan, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	deleted, _, err := svc.GCAnalysis(ctx)
	if err != nil || deleted != 1 {
		t.Fatalf("GC = %d deleted (err %v), want 1 (the planted orphan)", deleted, err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan must be swept (err %v)", err)
	}
	if live, _ := BatchParquetFiles(filepath.Join(dir, "analysis"), sum2.BatchID); len(live) != 1 {
		t.Fatalf("live batch file must survive the GC: %v", live)
	}
}

// TestEngineAvailabilityDefaultBuild: without the duckdb tag the std stub
// answers ErrEngineNotBuilt (the import path is unaffected — the whole
// file above proves it).
func TestEngineAvailabilityDefaultBuild(t *testing.T) {
	e := Query(context.Background())
	if e.Available() {
		t.Skip("this binary was built with -tags duckdb; the stub check belongs to the default build")
	}
	if _, err := e.Totals(context.Background(), t.TempDir(), FeeTypeElectricity, 2024, 5, 0); !errors.Is(err, ErrEngineNotBuilt) {
		t.Fatalf("Totals err = %v, want ErrEngineNotBuilt", err)
	}
	if _, err := e.Duplicates(context.Background(), t.TempDir(), FeeTypeElectricity, 2024, 0, 20); !errors.Is(err, ErrEngineNotBuilt) {
		t.Fatalf("Duplicates err = %v, want ErrEngineNotBuilt", err)
	}
}

// TestImportXLSX (#70): the xlsx channel through the FULL vertical slice —
// the recognize tests above pin the reader; this pins that the service's
// batch/parquet/evidence layers treat an xlsx source identically to a CSV
// one (rows imported, partition file on disk, the evidence anchor carrying
// the workbook's own name).
func TestImportXLSX(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()

	f := excelize.NewFile()
	for i, row := range [][]string{
		{"站址编码", "账期", "电费(元)"},
		{"SITE-X1", "2024-05", "123.45"},
		{"SITE-X2", "2024-05", "67.89"},
	} {
		for j, cell := range row {
			ref, err := excelize.CoordinatesToCellName(j+1, i+1)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellValue("Sheet1", ref, cell); err != nil {
				t.Fatal(err)
			}
		}
	}
	src := filepath.Join(dir, "tower-may.xlsx")
	if err := f.SaveAs(src); err != nil {
		t.Fatal(err)
	}

	sum, err := svc.Import(ctx, ImportOptions{
		Path: src, FeeType: FeeTypeTowerLease, Year: 2024, Month: 5,
	})
	if err != nil {
		t.Fatalf("import xlsx: %v", err)
	}
	if sum.Status != "completed" || sum.RowsImported != 2 || sum.RowsFailed != 0 {
		t.Fatalf("summary = %+v, want completed/2/0", sum)
	}
	if sum.WorkbookName != "tower-may.xlsx" {
		t.Fatalf("workbookName = %q, want the original file name in the evidence anchor", sum.WorkbookName)
	}
	if _, err := db.FeeBatches.GetByID(ctx, sum.BatchID); err != nil {
		t.Fatalf("batch row: %v", err)
	}
	files, err := BatchParquetFiles(filepath.Join(dir, "analysis"), sum.BatchID)
	if err != nil || len(files) != 1 {
		t.Fatalf("parquet files = %v (err %v), want the one partition file", files, err)
	}
}

// TestImportPreflightPropagatesDBErrors pins #104-B: the idempotency
// preflight used to read ANY error as "no duplicate" and proceed — a real
// DB failure silently voided the guarantee (or crashed later with a less
// honest message). Only the not-found verdict may proceed; everything else
// propagates as a preflight error.
func TestImportPreflightPropagatesDBErrors(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	src := writeCSV(t, dir, "healthy.csv", "站址编码,账期,电费(元)\nS1,2024-05,10.00\n")

	// Close the DB underneath the service: HashFile/ReadFeeFile still work
	// (filesystem), so the FIRST database touch — the preflight point
	// query — is what fails. The OLD shape skipped it silently and died at
	// the batch Create with a misleading error; the NEW shape fails the
	// preflight LOUDLY.
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	_, err := svc.Import(ctx, ImportOptions{
		Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5,
	})
	if err == nil {
		t.Fatal("Import with a dead database must fail")
	}
	if !strings.Contains(err.Error(), "幂等性预检") {
		t.Fatalf("Import error = %v, want the honest preflight classification (the DB failed, not the file)", err)
	}
	if errors.Is(err, ErrImportRefused) {
		t.Fatalf("a dead database is a SERVER fault, not an operator refusal: %v", err)
	}
}
