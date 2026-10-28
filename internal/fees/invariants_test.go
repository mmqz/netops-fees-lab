package fees

// invariants_test.go — the #33 property face, part 1: the invariants that
// live on the DB + Parquet inventory + snapshot faces (assertable in the
// DEFAULT build, no analysis engine needed — the money/query faces have
// their duckdb twins in invariants_duckdb_test.go).
//
//   I1  import idempotency    — same file, n imports (1/2/5/10): one
//                               completed batch, one snapshot, one live
//                               partition file; the ledger never
//                               double-counts
//   I1b import order          — A then B vs B then A: identical batch
//                               inventories (no hidden order dependence)
//   I3  rollback thoroughness — analysis rows gone, sibling data intact,
//                               snapshot KEPT (the evidence chain), the
//                               rolled_back status recorded
//   I6  period consistency    — declared period == snapshot path == Parquet
//                               partition == the row the ledger sees, and
//                               verify-batches' own period face agrees

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// importFixture writes body to <dir>/<name> and imports it as an
// electricity fee file for the declared period; fails loudly — the
// invariant tests never tolerate a refused import.
func importFixture(t *testing.T, svc *Service, dir, name, body string, year, month int) ImportSummary {
	t.Helper()
	sum, err := importFixtureErr(svc, dir, name, body, year, month)
	if err != nil {
		t.Fatalf("import %s: %v", name, err)
	}
	return sum
}

// importFixtureErr is importFixture's twin that surfaces the duplicate
// sentinel: the idempotency face asserts on the ErrDuplicateImport
// answer (the service layer's honest "zero new data" — the CLI/HTTP
// channels translate it to informational, the property asserts the
// sentinel itself).
func importFixtureErr(svc *Service, dir, name, body string, year, month int) (ImportSummary, error) {
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		return ImportSummary{}, err
	}
	return svc.Import(context.Background(), ImportOptions{
		Path: p, FeeType: FeeTypeElectricity, Year: year, Month: month,
	})
}

// completedBatches lists the completed batches (helper so every invariant
// shares one accessor).
func completedBatches(t *testing.T, svc *Service) []FeeBatch {
	t.Helper()
	batches, err := svc.List(context.Background(), FeeStatusCompleted)
	if err != nil {
		t.Fatal(err)
	}
	return batches
}

// TestImportIdempotentRepeat pins I1: importing the same file 1, 2, 5 and
// 10 times ends in the SAME library state — one completed batch, the same
// row counts, one live partition file, one snapshot. The repeated answers
// are skippedDuplicate (informational), never re-writes; anything else is
// a double-counting defect with the #20/#12 shapes.
func TestImportIdempotentRepeat(t *testing.T) {
	const body = "站址编码,账期,电费(元)\n" +
		"S-INV-1,2024-05,100.00\n" +
		"S-INV-1,2024-05,39.47\n" +
		"S-INV-2,2024-05,200.00\n"
	for _, n := range []int{1, 2, 5, 10} {
		svc, db, dir := newService(t)
		ctx := context.Background()

		var first ImportSummary
		dupes := 0
		for i := 0; i < n; i++ {
			sum, err := importFixtureErr(svc, dir, "inv.csv", body, 2024, 5)
			if err != nil {
				var dup *ErrDuplicateImport
				if !errors.As(err, &dup) {
					t.Fatalf("n=%d: repeat import failed with a non-duplicate error: %v", n, err)
				}
				dupes++
				// the duplicate answer still carries the batch-derived summary
				// (#24: the complete field set, skippedDuplicate included).
				if !sum.SkippedDuplicate || sum.ExistingBatch == "" {
					t.Fatalf("n=%d: duplicate answer missing its library record: %+v", n, sum)
				}
				continue
			}
			first = sum
		}
		if dupes != n-1 {
			t.Fatalf("n=%d: %d skippedDuplicate answers, want %d", n, dupes, n-1)
		}
		if first.RowsImported != 3 || first.RowsFailed != 0 {
			t.Fatalf("n=%d: first summary rows %d/%d, want 3/0", n, first.RowsImported, first.RowsFailed)
		}

		batches, err := db.FeeBatches.List(ctx, FeeStatusCompleted)
		if err != nil {
			t.Fatal(err)
		}
		if len(batches) != 1 {
			t.Fatalf("n=%d: %d completed batches, want exactly 1", n, len(batches))
		}
		b := batches[0]
		if b.RowsImported != 3 || b.FeeYear != 2024 || b.FeeMonth != 5 {
			t.Fatalf("n=%d: batch row = %+v", n, b)
		}
		files, err := BatchParquetFiles(svc.analysisDir(), b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 {
			t.Fatalf("n=%d: %d live parquet files, want exactly 1", n, len(files))
		}
		// snapshot face: the content-addressed copy of the one import is
		// on disk (re-imports never re-wrote it — same sha, same name).
		if _, err := os.Stat(filepath.Join(dir, b.SnapshotPath)); err != nil {
			t.Fatalf("n=%d: snapshot missing after %d imports: %v", n, n, err)
		}
	}
}

// fixtureFile is one importable fixture of the order-independence face.
type fixtureFile struct {
	name        string
	body        string
	year, month int
}

// TestImportOrderIndependence pins I1's order face: importing A then B
// ends in the same library as B then A — same batch inventory (SHA ×
// period × rows) and the same live partition counts. Idempotency keys are
// content-addressed, never position-addressed.
func TestImportOrderIndependence(t *testing.T) {
	const bodyA = "站址编码,账期,电费(元)\nS-A,2024-05,100.00\n"
	const bodyB = "站址编码,账期,电费(元)\nS-B,2024-06,200.00\n"

	run := func(order []fixtureFile) []string {
		svc, _, dir := newService(t)
		for _, f := range order {
			importFixture(t, svc, dir, f.name, f.body, f.year, f.month)
		}
		var digest []string
		for _, b := range completedBatches(t, svc) {
			files, err := BatchParquetFiles(svc.analysisDir(), b.ID)
			if err != nil {
				t.Fatal(err)
			}
			digest = append(digest, fmt.Sprintf("%s|%04d-%02d|rows=%d|files=%d",
				b.SHA256[:12], b.FeeYear, b.FeeMonth, b.RowsImported, len(files)))
		}
		sort.Strings(digest)
		return digest
	}

	aThenB := run([]fixtureFile{{"a.csv", bodyA, 2024, 5}, {"b.csv", bodyB, 2024, 6}})
	bThenA := run([]fixtureFile{{"b.csv", bodyB, 2024, 6}, {"a.csv", bodyA, 2024, 5}})

	if len(aThenB) != 2 {
		t.Fatalf("A→B: %d completed batches, want 2", len(aThenB))
	}
	for i := range aThenB {
		if aThenB[i] != bThenA[i] {
			t.Fatalf("order changed the library:\n A→B %v\n B→A %v", aThenB, bThenA)
		}
	}
}

// TestRollbackThoroughFourFaces pins I3: after a rollback the batch's
// analysis rows are GONE (live partition face), the sibling batch's data
// is UNTOUCHED, the snapshot STAYS on disk (the evidence chain outlives
// the data), and the ledger records the rolled_back status — all four
// faces the issue lists, asserted in one coherent scenario.
func TestRollbackThoroughFourFaces(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()

	sumA := importFixture(t, svc, dir, "a.csv", "站址编码,账期,电费(元)\nS-A,2024-05,100.00\n", 2024, 5)
	sumB := importFixture(t, svc, dir, "b.csv", "站址编码,账期,电费(元)\nS-B,2024-05,200.00\n", 2024, 5)
	if len(completedBatches(t, svc)) != 2 {
		t.Fatal("want 2 completed batches before the rollback")
	}

	rb, err := svc.Rollback(ctx, sumA.BatchID)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.Status != FeeStatusRolledBack {
		t.Fatalf("rollback returned status %q", rb.Status)
	}

	// face 1: the rolled-back batch's live analysis rows are gone.
	filesA, err := BatchParquetFiles(svc.analysisDir(), sumA.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filesA) != 0 {
		t.Fatalf("rolled-back batch still carries %d live parquet files", len(filesA))
	}
	// face 2: the sibling batch is untouched — still completed, one live
	// partition file, same rows.
	if got := len(completedBatches(t, svc)); got != 1 {
		t.Fatalf("completed batches after rollback = %d, want 1 (the sibling)", got)
	}
	filesB, err := BatchParquetFiles(svc.analysisDir(), sumB.BatchID)
	if err != nil {
		t.Fatal(err)
	}
	if len(filesB) != 1 {
		t.Fatalf("sibling batch carries %d live files, want 1", len(filesB))
	}
	// face 3: the snapshot STAYS — the evidence chain outlives the data.
	if _, err := os.Stat(filepath.Join(dir, rb.SnapshotPath)); err != nil {
		t.Fatalf("snapshot vanished on rollback (the evidence must stay): %v", err)
	}
	// face 4: the ledger records rolled_back (the record never vanishes
	// — a rollback is an event, not a delete).
	rolled, err := db.FeeBatches.List(ctx, FeeStatusRolledBack)
	if err != nil {
		t.Fatal(err)
	}
	if len(rolled) != 1 || rolled[0].ID != sumA.BatchID {
		t.Fatalf("rolled_back ledger = %+v", rolled)
	}
}

// TestPeriodConsistencyThreeFaces pins I6: for every completed batch the
// declared period, the snapshot's content-addressed path, the Parquet
// partition layout and verify-batches' own period face all agree — three
// independent recordings of one fact, re-derived here (verify-batches
// could itself be wrong; the property re-derives from the raw faces).
func TestPeriodConsistencyThreeFaces(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()

	importFixture(t, svc, dir, "may.csv", "站址编码,账期,电费(元)\nS-A,2024-05,100.00\n", 2024, 5)
	importFixture(t, svc, dir, "jun.csv", "站址编码,账期,电费(元)\nS-B,2024-06,200.00\n", 2024, 6)

	batches := completedBatches(t, svc)
	if len(batches) != 2 {
		t.Fatalf("want 2 completed batches, got %d", len(batches))
	}
	for _, b := range batches {
		wantY := fmt.Sprintf("%04d", b.FeeYear)
		wantM := fmt.Sprintf("%02d", b.FeeMonth)

		// face 1: snapshot path fee-imports/<type>/<year>/<mm>/<sha>_<name>
		parts := strings.Split(b.SnapshotPath, "/")
		if len(parts) < 4 || parts[2] != wantY || parts[3] != wantM {
			t.Fatalf("batch %s period %s-%s vs snapshot path %q", b.ID, wantY, wantM, b.SnapshotPath)
		}
		// face 2: every live parquet file sits under the same period's
		// hive partition.
		files, err := BatchParquetFiles(svc.analysisDir(), b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 {
			t.Fatalf("batch %s: %d live files, want 1", b.ID, len(files))
		}
		rel := filepath.ToSlash(files[0])
		if !strings.Contains(rel, "year="+wantY) || !strings.Contains(rel, "month="+wantM) {
			t.Fatalf("batch %s: partition %q does not carry year=%s/month=%s", b.ID, rel, wantY, wantM)
		}
	}

	// face 3: verify-batches' own period face agrees (zero findings) and
	// the default build's engine-less data face says so honestly.
	report, err := svc.VerifyBatches(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.BatchesChecked != 2 {
		t.Fatalf("verify-batches checked %d, want 2", report.BatchesChecked)
	}
	if len(report.PeriodProblems) != 0 || len(report.DataProblems) != 0 {
		t.Fatalf("verify-batches problems on a consistent library: %+v", report.Problems)
	}
	if !report.DataChecksSkipped {
		t.Fatal("default build must set dataChecksSkipped (nil engine)")
	}
}
