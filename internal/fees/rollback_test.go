package fees

// The #107 regression suite: the fee rollback is a TRANSACTION over the
// filesystem — before the fix it looped os.Remove over the batch's
// partition files and flipped the batch row only afterwards, so an IO
// fault mid-loop left earlier files already deleted under a still-
// completed row: neither fully rolled back nor fully intact, the audit
// surface silently corrupt. The fix quarantines every file with
// reversible renames first and flips the row last (one commit point, the
// import's step-7 shape); a fault before the flip undoes the moves.
//
// Fault injection is the testMoveFault seam (export_test.go): it places
// failures at exact dance steps deterministically — between two renames
// of the same call, where no chmod could reach.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
)

// csvTwoMonths imports as TWO partition files (month=05: 1 row,
// month=06: 2 rows) — the multi-file delete set every atomicity
// assertion below needs.
const csvTwoMonths = `站址编码,账期,电费(元)
S1,2024-05,10.00
S2,2024-06,20.00
S3,2024-06,30.00
`

// importTwoMonths runs the two-partition import and returns its summary.
func importTwoMonths(t *testing.T, svc *Service, dir string) ImportSummary {
	t.Helper()
	src := writeCSV(t, dir, "two-months.csv", csvTwoMonths)
	sum, err := svc.Import(context.Background(), ImportOptions{
		Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5,
	})
	if err != nil {
		t.Fatalf("import two-months: %v", err)
	}
	if sum.Status != "completed" || sum.RowsImported != 3 {
		t.Fatalf("summary = %+v, want a completed 3-row import", sum)
	}
	return sum
}

// liveBatchFiles asserts the batch has exactly n live partition files and
// returns them (walk order: month=05 before month=06).
func liveBatchFiles(t *testing.T, analysisDir, batchID string, n int) []string {
	t.Helper()
	files, err := BatchParquetFiles(analysisDir, batchID)
	if err != nil {
		t.Fatalf("BatchParquetFiles: %v", err)
	}
	if len(files) != n {
		t.Fatalf("live files of %s = %v, want exactly %d", batchID, files, n)
	}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("live file %s missing: %v", f, err)
		}
	}
	return files
}

// countParquetRows reads a partition file back with the pure-Go reader and
// returns its row count (the write path must not need the duckdb engine
// to be verifiable — parquet_test.go's discipline).
func countParquetRows(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	r := parquet.NewGenericReader[feeParquetRow](f)
	defer func() { _ = r.Close() }()
	total := 0
	for {
		buf := make([]feeParquetRow, 8)
		n, rerr := r.Read(buf)
		total += n
		if rerr == io.EOF {
			return total
		}
		if rerr != nil {
			t.Fatalf("read rows of %s: %v", path, rerr)
		}
		if n == 0 {
			return total
		}
	}
}

// TestRollbackAtomicUnderIOFault is the #107 acceptance case: the IO fault
// hits the SECOND quarantine move (the first file is already quarantined
// when it fires). The pre-fix shape left that first file deleted under a
// completed row; the fixed shape UNDOES the move — after the failed
// rollback the batch keeps completed with every row intact, and a plain
// retry (no fault) completes.
func TestRollbackAtomicUnderIOFault(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	analysisDir := filepath.Join(dir, "analysis")
	sum := importTwoMonths(t, svc, dir)
	files := liveBatchFiles(t, analysisDir, sum.BatchID, 2) // [month=05, month=06]

	reset := setMoveFault(svc, func(op, path string) error {
		if op == moveQuarantine && path == files[1] {
			return fmt.Errorf("injected io fault")
		}
		return nil
	})
	_, err := svc.Rollback(ctx, sum.BatchID)
	reset()
	if err == nil {
		t.Fatal("rollback under the injected IO fault must fail")
	}
	if !strings.Contains(err.Error(), "已恢复全部分析行") {
		t.Fatalf("rollback error = %v, want the honest undo report (rows restored, batch untouched)", err)
	}

	// The #107 invariant: NOTHING stays deleted.
	b, err := db.FeeBatches.GetByID(ctx, sum.BatchID)
	if err != nil || b.Status != FeeStatusCompleted {
		t.Fatalf("batch = %+v (err %v), want completed — a failed rollback must not have happened", b, err)
	}
	live := liveBatchFiles(t, analysisDir, sum.BatchID, 2)
	// row counts unchanged — the files carry their full payload.
	if got := countParquetRows(t, live[0]); got != 1 {
		t.Errorf("month=05 rows = %d, want 1 (unchanged)", got)
	}
	if got := countParquetRows(t, live[1]); got != 2 {
		t.Errorf("month=06 rows = %d, want 2 (unchanged)", got)
	}
	// the quarantine tree is gone after a successful undo.
	if _, err := os.Stat(rollbackQuarantineRoot(analysisDir, sum.BatchID)); !os.IsNotExist(err) {
		t.Errorf("quarantine tree must be removed after the undo (stat err = %v)", err)
	}

	// the failure left a fully retryable state — the plain retry rolls back.
	rb, err := svc.Rollback(ctx, sum.BatchID)
	if err != nil || rb.Status != FeeStatusRolledBack {
		t.Fatalf("retry rollback = %+v (err %v), want rolled_back", rb, err)
	}
	liveBatchFiles(t, analysisDir, sum.BatchID, 0)
	if _, err := os.Stat(rollbackQuarantineRoot(analysisDir, sum.BatchID)); !os.IsNotExist(err) {
		// #17: name WHAT is left behind — an empty-dir residue points at
		// the platform's RemoveAll semantics, a file residue at the
		// cleanup order. The failure stays a failure; the message makes
		// the next diagnosis one read instead of one session.
		entries, _ := os.ReadDir(rollbackQuarantineRoot(analysisDir, sum.BatchID))
		t.Errorf("quarantine tree must be dropped after the commit (stat err = %v, residue = %d entries: %v)",
			err, len(entries), dirEntryNames(entries))
	}
}

// TestRollbackDoubleFaultMarksFailed covers the corner where even the undo
// faults (a second IO fault on the already-quarantined file): the batch
// can honestly keep neither completed (rows are half out of the query
// surface) nor rolled_back (the flip never ran). It is marked failed —
// the existing honest marker — with the quarantine whereabouts in the
// error text; the snapshot survives and the freed idempotency key makes
// the re-import the recovery channel; GC reclaims the stranded remains
// past the milestone.
func TestRollbackDoubleFaultMarksFailed(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	analysisDir := filepath.Join(dir, "analysis")
	sum := importTwoMonths(t, svc, dir)
	files := liveBatchFiles(t, analysisDir, sum.BatchID, 2)

	reset := setMoveFault(svc, func(op, path string) error {
		if op == moveQuarantine && path == files[1] {
			return fmt.Errorf("injected io fault")
		}
		if op == moveRestore && path == files[0] {
			return fmt.Errorf("injected undo fault")
		}
		return nil
	})
	_, err := svc.Rollback(ctx, sum.BatchID)
	reset()
	if err == nil {
		t.Fatal("rollback with both faults injected must fail")
	}
	if !strings.Contains(err.Error(), "批次已标记 failed") {
		t.Fatalf("rollback error = %v, want the torn-state report", err)
	}

	// The honest marker — never a fake completed/rolled_back.
	b, err := db.FeeBatches.GetByID(ctx, sum.BatchID)
	if err != nil || b.Status != FeeStatusFailed {
		t.Fatalf("batch = %+v (err %v), want failed — the torn state must be recorded", b, err)
	}
	if !strings.Contains(b.Error, "回滚中断") {
		t.Errorf("batch.Error = %q, want the 回滚中断 audit text", b.Error)
	}
	if !strings.Contains(b.Error, "month=05") {
		t.Errorf("batch.Error = %q, want the stranded file's whereabouts", b.Error)
	}
	// the import accounting passes through unchanged.
	if b.RowsTotal != 3 || b.RowsImported != 3 || b.RowsFailed != 0 {
		t.Errorf("counts = %d/%d/%d, want the untouched 3/3/0 import accounting",
			b.RowsTotal, b.RowsImported, b.RowsFailed)
	}
	// evidence on disk: file 1 stranded in the quarantine, file 2 still live.
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Errorf("files[0] must be quarantined (stat err = %v)", err)
	}
	if _, err := os.Stat(files[1]); err != nil {
		t.Errorf("files[1] must still be live (stat err = %v)", err)
	}
	stranded, err := quarantinedFiles(analysisDir, sum.BatchID)
	if err != nil || len(stranded) != 1 || stranded[0].live != files[0] {
		t.Fatalf("stranded = %+v (err %v), want exactly files[0]", stranded, err)
	}
	// the snapshot survives — the recovery source.
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(b.SnapshotPath))); err != nil {
		t.Fatalf("snapshot must survive the torn rollback: %v", err)
	}

	// 'failed' freed the idempotency key: re-importing the same file
	// completes and restores the analysis rows.
	src := filepath.Join(dir, "two-months.csv")
	sum2, err := svc.Import(ctx, ImportOptions{
		Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5,
	})
	if err != nil || sum2.Status != "completed" || sum2.RowsImported != 3 {
		t.Fatalf("re-import after the torn rollback = %+v (err %v), want the recovery import", sum2, err)
	}
	liveBatchFiles(t, analysisDir, sum2.BatchID, 2)

	// GC reclaims the stranded remains once past the milestone: the live
	// orphan file (the failed batch is not keep-set) and the quarantine dir.
	backdate(t, files[1])
	backdate(t, rollbackQuarantineRoot(analysisDir, sum.BatchID))
	deleted, deletedStaging, err := svc.GCAnalysis(ctx)
	if err != nil {
		t.Fatalf("GCAnalysis: %v", err)
	}
	if deleted != 1 || deletedStaging != 1 {
		t.Fatalf("GCAnalysis = files %d, dirs %d; want 1 (the failed batch's live file) and 1 (its quarantine)", deleted, deletedStaging)
	}
	if _, err := os.Stat(files[1]); !os.IsNotExist(err) {
		t.Errorf("the failed batch's live file must be swept (stat err = %v)", err)
	}
	if _, err := os.Stat(rollbackQuarantineRoot(analysisDir, sum.BatchID)); !os.IsNotExist(err) {
		t.Errorf("the failed batch's quarantine must be swept (stat err = %v)", err)
	}
	liveBatchFiles(t, analysisDir, sum2.BatchID, 2) // the recovery import survives
}

// TestRollbackAdoptsTornQuarantine pins the crash-window adoption: a
// previous attempt torn mid-dance (file already quarantined, row still
// completed — the shape a process crash leaves) is restored first, so a
// FAULTED retry still ends with every row restored (the pre-#107 shape
// would have left the quarantined file behind while reporting success),
// and a clean retry then rolls the WHOLE batch back.
func TestRollbackAdoptsTornQuarantine(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	analysisDir := filepath.Join(dir, "analysis")
	sum := importTwoMonths(t, svc, dir)
	files := liveBatchFiles(t, analysisDir, sum.BatchID, 2)

	// simulate the crash mid-dance: the first move already happened.
	for _, p := range buildRollbackPlan(analysisDir, sum.BatchID, files[:1]) {
		if err := quarantineFile(p); err != nil {
			t.Fatalf("setup quarantine: %v", err)
		}
	}
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Fatalf("setup: files[0] must be quarantined (stat err = %v)", err)
	}
	// the delete set is the LIVE files only — a quarantined file must not
	// leak back in through BatchParquetFiles (its own quarantine slot is
	// derived from the same name).
	liveBatchFiles(t, analysisDir, sum.BatchID, 1)

	// a FAULTED retry (the still-live file's quarantine faults) must end
	// with EVERY row restored — the adopted one included.
	reset := setMoveFault(svc, func(op, path string) error {
		if op == moveQuarantine && path == files[1] {
			return fmt.Errorf("injected io fault")
		}
		return nil
	})
	_, err := svc.Rollback(ctx, sum.BatchID)
	reset()
	if err == nil {
		t.Fatal("faulted retry must fail")
	}
	live := liveBatchFiles(t, analysisDir, sum.BatchID, 2)
	if got := countParquetRows(t, live[0]) + countParquetRows(t, live[1]); got != 3 {
		t.Errorf("restored rows = %d, want all 3 (the torn file adopted back too)", got)
	}
	b, err := db.FeeBatches.GetByID(ctx, sum.BatchID)
	if err != nil || b.Status != FeeStatusCompleted {
		t.Fatalf("batch = %+v (err %v), want completed after the faulted retry's full undo", b, err)
	}
	if stranded, serr := quarantinedFiles(analysisDir, sum.BatchID); serr != nil || len(stranded) != 0 {
		t.Errorf("stranded = %+v (err %v), want an empty quarantine after the undo", stranded, serr)
	}

	// the clean retry rolls the whole batch back.
	rb, err := svc.Rollback(ctx, sum.BatchID)
	if err != nil || rb.Status != FeeStatusRolledBack {
		t.Fatalf("rollback after a torn attempt = %+v (err %v), want rolled_back", rb, err)
	}
	// the adopted file went back live and then away with the whole dance.
	liveBatchFiles(t, analysisDir, sum.BatchID, 0)
	if _, err := os.Stat(rollbackQuarantineRoot(analysisDir, sum.BatchID)); !os.IsNotExist(err) {
		t.Errorf("quarantine tree must be dropped after the commit (stat err = %v)", err)
	}
	b, err = db.FeeBatches.GetByID(ctx, sum.BatchID)
	if err != nil || b.Status != FeeStatusRolledBack {
		t.Fatalf("batch = %+v (err %v), want rolled_back", b, err)
	}
}

// TestRollbackTornRestoreFailureMarksFailed: the torn quarantine cannot be
// restored (the adoption step faults) — the batch must not stay completed
// over quarantined rows; it is marked failed with the evidence kept where
// it lies (the file stays in the quarantine, the live file untouched).
func TestRollbackTornRestoreFailureMarksFailed(t *testing.T) {
	svc, db, dir := newService(t)
	ctx := context.Background()
	analysisDir := filepath.Join(dir, "analysis")
	sum := importTwoMonths(t, svc, dir)
	files := liveBatchFiles(t, analysisDir, sum.BatchID, 2)

	for _, p := range buildRollbackPlan(analysisDir, sum.BatchID, files[:1]) {
		if err := quarantineFile(p); err != nil {
			t.Fatalf("setup quarantine: %v", err)
		}
	}
	reset := setMoveFault(svc, func(op, path string) error {
		if op == moveRestore && path == files[0] {
			return fmt.Errorf("injected restore fault")
		}
		return nil
	})
	_, err := svc.Rollback(ctx, sum.BatchID)
	reset()
	if err == nil {
		t.Fatal("rollback with the torn-adoption fault must fail")
	}
	if !strings.Contains(err.Error(), "恢复中断的隔离文件") {
		t.Fatalf("rollback error = %v, want the adoption failure report", err)
	}
	b, err := db.FeeBatches.GetByID(ctx, sum.BatchID)
	if err != nil || b.Status != FeeStatusFailed {
		t.Fatalf("batch = %+v (err %v), want failed — quarantined rows under a completed row is the #107 lie", b, err)
	}
	if !strings.Contains(b.Error, "回滚中断") {
		t.Errorf("batch.Error = %q, want the 回滚中断 audit text", b.Error)
	}
	// the evidence stays where it lies.
	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Errorf("files[0] must stay quarantined (stat err = %v)", err)
	}
	if _, err := os.Stat(files[1]); err != nil {
		t.Errorf("files[1] must stay live (stat err = %v)", err)
	}
	if stranded, serr := quarantinedFiles(analysisDir, sum.BatchID); serr != nil || len(stranded) != 1 {
		t.Errorf("stranded = %+v (err %v), want the one quarantined file kept as evidence", stranded, serr)
	}
}

// TestRollbackFlipFailureUndoes pins the commit-point failure: the batch
// row's flip (the single commit point) fails after every file is already
// quarantined — the undo still restores ALL rows and the batch keeps
// completed; the retry then completes the rollback. The flip failure is
// induced for real by cancelling the context after the last quarantine
// move (the seam fires before the move; the move itself succeeds and the
// canceled context then fails the flip inside db.Do).
func TestRollbackFlipFailureUndoes(t *testing.T) {
	svc, db, dir := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	analysisDir := filepath.Join(dir, "analysis")
	sum := importTwoMonths(t, svc, dir)
	files := liveBatchFiles(t, analysisDir, sum.BatchID, 2)

	reset := setMoveFault(svc, func(op, path string) error {
		if op == moveQuarantine && path == files[1] {
			cancel() // last move done → the flip below fails for real
		}
		return nil
	})
	_, err := svc.Rollback(ctx, sum.BatchID)
	reset()
	if err == nil {
		t.Fatal("rollback with a failed commit must error")
	}
	if !strings.Contains(err.Error(), "已恢复全部分析行") {
		t.Fatalf("rollback error = %v, want the honest undo report", err)
	}

	// fresh context: the batch honestly completed, every row back.
	fresh := context.Background()
	b, err := db.FeeBatches.GetByID(fresh, sum.BatchID)
	if err != nil || b.Status != FeeStatusCompleted {
		t.Fatalf("batch = %+v (err %v), want completed — the flip never happened, neither did the delete", b, err)
	}
	live := liveBatchFiles(t, analysisDir, sum.BatchID, 2)
	if got := countParquetRows(t, live[0]) + countParquetRows(t, live[1]); got != 3 {
		t.Errorf("restored rows = %d, want all 3", got)
	}
	if stranded, serr := quarantinedFiles(analysisDir, sum.BatchID); serr != nil || len(stranded) != 0 {
		t.Errorf("stranded = %+v (err %v), want an empty quarantine after the undo", stranded, serr)
	}

	// the retry (no fault, live context) completes the rollback.
	rb, err := svc.Rollback(fresh, sum.BatchID)
	if err != nil || rb.Status != FeeStatusRolledBack {
		t.Fatalf("retry rollback = %+v (err %v), want rolled_back", rb, err)
	}
	liveBatchFiles(t, analysisDir, sum.BatchID, 0)
}

// TestGCRollbackQuarantineSweep pins the GC side of the quarantine: the
// walk never judges quarantine files at file level (dir grain only — a
// torn attempt's tree is all-or-nothing), the sweep collects the stale
// quarantines of non-completed batches past the milestone, keeps the
// fresh ones, and a completed batch's quarantine is adoption evidence
// the milestone never overrides. Also pins that quarantined files are
// invisible to the query glob (the whole design hangs on the depth
// difference between the mirrored layout and fees/*/*/*/*.parquet).
func TestGCRollbackQuarantineSweep(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	analysisDir := filepath.Join(dir, "analysis")
	feesRoot := filepath.Join(analysisDir, "fees")
	qroot := filepath.Join(feesRoot, quarantineDirName)

	// a live completed batch: its quarantine dir is adoption evidence.
	src := writeCSV(t, dir, "live.csv", "站址编码,账期,电费(元)\nL1,2024-05,10.00\n")
	sum, err := svc.Import(ctx, ImportOptions{Path: src, FeeType: FeeTypeElectricity, Year: 2024, Month: 5})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	mk := func(batchID string) string {
		d := filepath.Join(qroot, batchID)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(d, "batch-"+batchID+".parquet")
		if err := os.WriteFile(f, []byte("fake"), 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}
	keptDir := mk(sum.BatchID)  // completed → keep-set wins, even backdated
	staleDir := mk("fee_stale") // unknown batch, idle → swept
	freshDir := mk("fee_fresh") // unknown batch, fresh → milestone keeps
	backdate(t, keptDir)
	backdate(t, filepath.Join(keptDir, "batch-"+sum.BatchID+".parquet"))
	backdate(t, staleDir)
	backdate(t, filepath.Join(staleDir, "batch-fee_stale.parquet"))

	deleted, deletedStaging, err := svc.GCAnalysis(ctx)
	if err != nil {
		t.Fatalf("GCAnalysis: %v", err)
	}
	// deletedFiles counts only the LIVE tree: the walk must skip the
	// quarantine wholesale (a file-level judgment would have collected
	// the stale and kept quarantine files and littered empty mirror dirs).
	if deleted != 0 {
		t.Errorf("deletedFiles = %d, want 0 — the walk must skip the quarantine tree", deleted)
	}
	if deletedStaging != 1 {
		t.Errorf("deletedStaging = %d, want 1 (the stale quarantine dir)", deletedStaging)
	}
	if _, err := os.Stat(keptDir); err != nil {
		t.Errorf("completed batch's quarantine must be kept as adoption evidence: %v", err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Errorf("fresh quarantine must survive the milestone: %v", err)
	}
	if _, err := os.Stat(staleDir); !os.IsNotExist(err) {
		t.Errorf("stale quarantine must be swept (stat err = %v)", err)
	}

	// the query glob never reaches quarantined rows: the mirrored layout
	// sits deeper than the fixed three-dir pattern.
	matches, err := filepath.Glob(feeTableGlob(analysisDir))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, m := range matches {
		if strings.Contains(filepath.ToSlash(m), quarantineDirName+"/") {
			t.Errorf("query glob matched a quarantined file: %s", m)
		}
	}
	if len(matches) != 1 {
		t.Errorf("glob matches = %v, want exactly the one live partition file", matches)
	}
}

// dirEntryNames renders a residue listing for the #17 diagnostics (nil
// when the read itself failed — the count alone still helps).
func dirEntryNames(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
