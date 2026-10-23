// Uploads garbage-collection tests (W5-D, v0.4.0): orphan uploads are
// deleted; uploads referenced by ANY batch status (completed, failed,
// rolled_back) survive; a missing uploads directory is a clean no-op; the
// CLI `gc` subcommand keeps the single-JSON stdout contract.
package app

import (
	"context"
	"encoding/json"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// gcTestBatch inserts one batch row with the given workbook_path and status.
func gcTestBatch(t *testing.T, db *repo.DB, id, status, path string) {
	t.Helper()
	b := domain.ImportBatch{
		ID:             id,
		WorkbookName:   id + ".xlsx",
		WorkbookSHA256: "sha256-" + id,
		Status:         domain.BatchStatusAnalyzing,
		RuleVersion:    domain.RuleVersion,
		StartedAt:      time.Now().UTC(),
	}
	if err := db.Batches.Create(context.Background(), b); err != nil {
		t.Fatalf("create batch %s: %v", id, err)
	}
	if err := db.Batches.SetWorkbookPath(context.Background(), id, path); err != nil {
		t.Fatalf("set workbook path %s: %v", id, err)
	}
	if err := db.Batches.UpdateStatus(context.Background(), id, domain.BatchStatus(status)); err != nil {
		t.Fatalf("set status %s: %v", id, err)
	}
}

// writeFile creates a small file at path (parent must exist).
func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("workbook-bytes-"+filepath.Base(path)), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// gcFeeTestBatch inserts one fee batch row whose staging keep-set entry is
// the (sha256, workbook_name) pair the sweep derives names from.
func gcFeeTestBatch(t *testing.T, db *repo.DB, id string, status domain.FeeStatus, sha, name string) {
	t.Helper()
	b := domain.FeeBatch{
		ID: id, WorkbookName: name, SHA256: sha, FeeType: domain.FeeTypeElectricity,
		FeeYear: 2024, FeeMonth: 5, SnapshotPath: "fee-imports/electricity/2024/05/" + sha + "_" + name,
		Mapping: `{"columns":{"siteCode":"站址编码","amount":"电费(元)"}}`,
		Status:  status, CreatedAt: time.Now().UTC(),
	}
	if err := db.FeeBatches.Create(context.Background(), b); err != nil {
		t.Fatalf("create fee batch %s: %v", id, err)
	}
}

// backdateGC pushes an entry's mtime past the fee GC milestone window
// (real orphans are crash debris — old by the time anyone sweeps).
func backdateGC(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func TestGCOrphanUploads(t *testing.T) {
	dir := testsupport.TempDir(t)
	dbPath := filepath.Join(dir, "netops.db")
	a, err := New(Config{DBPath: dbPath, DataDir: dir})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	defer func() { _ = a.Close() }()

	uploads := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatalf("mkdir uploads: %v", err)
	}
	// Two orphans: never referenced by any batch.
	orphan1 := filepath.Join(uploads, "aaa111_deadbeef_orphan1.xlsx")
	orphan2 := filepath.Join(uploads, "bbb222_deadbeef_orphan2.xlsx")
	writeFile(t, orphan1)
	writeFile(t, orphan2)
	// Three referenced uploads, one per batch status. The failed batch's
	// reference is a RELATIVE path shape — the base-name identity must keep
	// the file regardless of how the recording process saw the path.
	keptCompleted := filepath.Join(uploads, "ccc333_deadbeef_kept-completed.xlsx")
	keptFailed := "uploads/ddd444_deadbeef_kept-failed.xlsx"
	keptRolled := filepath.Join(uploads, "eee555_deadbeef_kept-rolled.xlsx")
	writeFile(t, keptCompleted)
	writeFile(t, filepath.Join(dir, keptFailed))
	writeFile(t, keptRolled)

	gcTestBatch(t, a.DB, "bat_gc_completed", "completed", keptCompleted)
	gcTestBatch(t, a.DB, "bat_gc_failed", "failed", keptFailed)
	gcTestBatch(t, a.DB, "bat_gc_rolled", "rolled_back", keptRolled)

	res, err := a.GCOrphanUploads(context.Background())
	if err != nil {
		t.Fatalf("GCOrphanUploads: %v", err)
	}
	if res.Scanned != 5 || res.Deleted != 2 || res.Referenced != 3 || res.Errors != 0 {
		t.Fatalf("gc result = %+v, want scanned=5 deleted=2 referenced=3 errors=0", res)
	}
	for _, p := range []string{orphan1, orphan2} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("orphan %s still exists (stat err = %v)", p, err)
		}
	}
	for _, p := range []string{keptCompleted, filepath.Join(dir, keptFailed), keptRolled} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("referenced upload %s missing: %v", p, err)
		}
	}

	// Idempotent: a second sweep finds nothing to delete.
	res, err = a.GCOrphanUploads(context.Background())
	if err != nil {
		t.Fatalf("second GCOrphanUploads: %v", err)
	}
	if res.Deleted != 0 || res.Scanned != 3 {
		t.Fatalf("second gc result = %+v, want deleted=0 scanned=3", res)
	}

	// A missing uploads directory is a clean no-op (fresh install).
	if err := os.RemoveAll(uploads); err != nil {
		t.Fatalf("remove uploads: %v", err)
	}
	res, err = a.GCOrphanUploads(context.Background())
	if err != nil {
		t.Fatalf("gc without uploads dir: %v", err)
	}
	if res != (GCResult{}) {
		t.Fatalf("gc without uploads dir = %+v, want zero result", res)
	}

	// An empty dataDir fails fast (mirrors the upload route guard).
	emptyApp := &App{DB: a.DB, Log: a.Log, Config: Config{}}
	if _, err := emptyApp.GCOrphanUploads(context.Background()); err == nil {
		t.Fatal("gc with empty dataDir must fail fast")
	}
}

// TestGCOrphanUploadsRemovalFailureNotFatal pins the "delete failure is
// logged, not fatal" semantics: a write-protected uploads directory yields
// errors>0 and a nil error.
func TestGCOrphanUploadsRemovalFailureNotFatal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("issue #38: Windows ignores the read-only directory attribute for file removal, so the chmod-based failure injection cannot reach the error path here; the POSIX branch covers it (audit-v0.8.0 §2.1.3)")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: read-only directories do not block root removal")
	}
	dir := testsupport.TempDir(t)
	dbPath := filepath.Join(dir, "netops.db")
	a, err := New(Config{DBPath: dbPath, DataDir: dir})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	defer func() { _ = a.Close() }()

	uploads := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatalf("mkdir uploads: %v", err)
	}
	orphan := filepath.Join(uploads, "aaa111_deadbeef_orphan.xlsx")
	writeFile(t, orphan)
	if err := os.Chmod(uploads, 0o500); err != nil {
		t.Fatalf("chmod uploads: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(uploads, 0o755) })

	res, err := a.GCOrphanUploads(context.Background())
	if err != nil {
		t.Fatalf("gc must not fail on removal errors: %v", err)
	}
	if res.Deleted != 0 || res.Errors != 1 {
		t.Fatalf("gc result = %+v, want deleted=0 errors=1", res)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Errorf("orphan must survive a failed removal: %v", err)
	}
}

// TestCLIGCSubcommand drives the real CLI dispatch (`gc --db --data`) on a
// fresh data dir: exit code 0, exactly one JSON object on stdout with the
// GC counts, nothing required on stderr.
func TestCLIGCSubcommand(t *testing.T) {
	dir := testsupport.TempDir(t)
	dbPath := filepath.Join(dir, "netops.db")

	// Seed one orphan via the app wiring (same DB the CLI re-opens).
	a, err := New(Config{DBPath: dbPath, DataDir: dir})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	uploads := filepath.Join(a.Config.DataDir, "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatalf("mkdir uploads: %v", err)
	}
	orphan := filepath.Join(uploads, "zzz999_deadbeef_orphan.xlsx")
	writeFile(t, orphan)
	// #106-C: the manual gc now ALSO sweeps the fee analysis tree — seed
	// one orphan Parquet file (backdated past the milestone) and one stale
	// staging dir, and the single-JSON contract carries their counts.
	analysisOrphan := filepath.Join(dir, "analysis", "fees",
		"fee_type=electricity", "year=2024", "month=05", "batch-fee_nope.parquet")
	if err := os.MkdirAll(filepath.Dir(analysisOrphan), 0o755); err != nil {
		t.Fatalf("mkdir analysis partition: %v", err)
	}
	writeFile(t, analysisOrphan)
	backdateGC(t, analysisOrphan)
	staleStaging := filepath.Join(dir, "analysis", "fees", ".staging", "fee_nope2")
	if err := os.MkdirAll(staleStaging, 0o755); err != nil {
		t.Fatalf("mkdir stale staging: %v", err)
	}
	backdateGC(t, staleStaging)
	if err := a.Close(); err != nil {
		t.Fatalf("app.Close: %v", err)
	}

	var stdout, stderr strings.Builder
	code := CLI([]string{"gc", "--db", dbPath, "--data", dir}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("gc exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	var got gcResponse
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("gc stdout is not one JSON object: %v\nstdout: %s", err, stdout.String())
	}
	if got.Scanned != 1 || got.Deleted != 1 || got.Referenced != 0 || got.Errors != 0 {
		t.Errorf("gc response = %+v, want scanned=1 deleted=1", got)
	}
	if got.AnalysisFiles != 1 || got.AnalysisStaging != 1 {
		t.Errorf("gc response analysis counts = %+v, want analysisFiles=1 analysisStaging=1 (the seeded fee orphans)", got)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan still exists after CLI gc (stat err = %v)", err)
	}
	if _, err := os.Stat(analysisOrphan); !os.IsNotExist(err) {
		t.Errorf("analysis orphan must be swept by CLI gc (stat err = %v)", err)
	}
	if _, err := os.Stat(staleStaging); !os.IsNotExist(err) {
		t.Errorf("stale staging dir must be swept by CLI gc (stat err = %v)", err)
	}

	// Usage error keeps exit code 2 and the JSON error envelope.
	stdout.Reset()
	code = CLI([]string{"gc", "--db", dbPath, "--data", dir, "stray-arg"}, &stdout, &stderr)
	if code != exitUsage {
		t.Errorf("gc stray-arg exit code = %d, want 2", code)
	}
	if !strings.Contains(stdout.String(), "usage_error") {
		t.Errorf("gc stray-arg stdout = %q, want usage_error envelope", stdout.String())
	}
}

// TestGCOrphanFeeStagingUploads pins the #106-B sweep: uploads/fees shares
// the uploads doctrine — a fee batch of ANY status derives a staging
// file's name and keeps it; never-derived files (pre-batch crash debris,
// refused uploads, interrupted staging temps) are orphans. The counts
// aggregate with the flat uploads dir in one GCResult.
func TestGCOrphanFeeStagingUploads(t *testing.T) {
	dir := testsupport.TempDir(t)
	dbPath := filepath.Join(dir, "netops.db")
	a, err := New(Config{DBPath: dbPath, DataDir: dir})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	defer func() { _ = a.Close() }()

	uploads := filepath.Join(dir, "uploads")
	feesDir := filepath.Join(uploads, "fees")
	if err := os.MkdirAll(feesDir, 0o755); err != nil {
		t.Fatalf("mkdir uploads/fees: %v", err)
	}
	// One flat orphan (the legacy sweep must keep working through the
	// merged result) + the fees entries.
	flatOrphan := filepath.Join(uploads, "flat_deadbeef_orphan.xlsx")
	writeFile(t, flatOrphan)
	feeOrphan := filepath.Join(feesDir, "aaa111deadbeef_orphan.csv") // no fee batch derives it
	writeFile(t, feeOrphan)
	// Referenced staging files, one per batch status — the name is exactly
	// what stageFeeUpload derives (<sha256>_<multipart base name>) and
	// what the keep-set rebuilds from the batch row.
	keptCompleted := filepath.Join(feesDir, "bbb222deadbeef_kept-completed.csv")
	keptFailed := filepath.Join(feesDir, "ccc333deadbeef_kept-failed.csv")
	keptRolled := filepath.Join(feesDir, "ddd444deadbeef_kept-rolled.csv")
	writeFile(t, keptCompleted)
	writeFile(t, keptFailed)
	writeFile(t, keptRolled)
	gcFeeTestBatch(t, a.DB, "fee_gc_completed", domain.FeeStatusCompleted, "bbb222deadbeef", "kept-completed.csv")
	gcFeeTestBatch(t, a.DB, "fee_gc_failed", domain.FeeStatusFailed, "ccc333deadbeef", "kept-failed.csv")
	gcFeeTestBatch(t, a.DB, "fee_gc_rolled", domain.FeeStatusRolledBack, "ddd444deadbeef", "kept-rolled.csv")
	// An interrupted staging temp (pre-rename crash debris) is an orphan.
	writeFile(t, filepath.Join(feesDir, ".tmp-1234567"))
	// A stray directory inside uploads/fees is kept (conservative).
	if err := os.MkdirAll(filepath.Join(feesDir, "stray-dir"), 0o755); err != nil {
		t.Fatalf("mkdir stray-dir: %v", err)
	}

	res, err := a.GCOrphanUploads(context.Background())
	if err != nil {
		t.Fatalf("GCOrphanUploads: %v", err)
	}
	// Scanned = 1 flat + 6 fees entries; deleted = flat + fee orphans +
	// temp; referenced = 3 kept + stray dir.
	if res.Scanned != 7 || res.Deleted != 3 || res.Referenced != 4 || res.Errors != 0 {
		t.Fatalf("gc result = %+v, want scanned=7 deleted=3 referenced=4 errors=0", res)
	}
	for _, p := range []string{flatOrphan, feeOrphan, filepath.Join(feesDir, ".tmp-1234567")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("orphan %s still exists (stat err = %v)", p, err)
		}
	}
	for _, p := range []string{keptCompleted, keptFailed, keptRolled} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("referenced fee staging %s missing: %v", p, err)
		}
	}

	// Idempotent second sweep: nothing left to collect, only the kept
	// entries remain (3 referenced staging files + the conservative dir).
	res, err = a.GCOrphanUploads(context.Background())
	if err != nil {
		t.Fatalf("second GCOrphanUploads: %v", err)
	}
	if res.Deleted != 0 || res.Scanned != 4 {
		t.Fatalf("second gc result = %+v, want deleted=0 scanned=4", res)
	}
}

// TestGCOrphanUploadsSweepStagingTemps pins the #112 flat-dir addition:
// the resource and metrics channels stage their uploads through
// per-request ".tmp-*" temp names inside the FLAT uploads dir
// (internal/api storeUpload) before renaming into the content-addressed
// name — an interrupted temp is never recorded in any workbook_path, so
// it is orphan crash debris exactly like the fee staging temps (#106-B).
// The temps here are FRESH (not backdated) on purpose: a manual
// `netopsfees gc` overlapping a live serve may legitimately collect an
// in-flight request's temp — the upload then fails loudly (rename ENOENT
// → 500, operator retries) and can never yield a torn file or a lying
// hash, the same accepted window the fee staging sweep already pins
// (TestGCOrphanFeeStagingUploads). The serve-startup sweep — the primary
// trigger — runs before the listener, where a temp is unambiguous debris.
// This is deliberately NOT the Parquet tree's mtime milestone: deleting
// a fresh orphan there silently loses rows of a completed batch; deleting
// a fresh temp here only makes one request fail loudly.
func TestGCOrphanUploadsSweepStagingTemps(t *testing.T) {
	dir := testsupport.TempDir(t)
	dbPath := filepath.Join(dir, "netops.db")
	a, err := New(Config{DBPath: dbPath, DataDir: dir})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	defer func() { _ = a.Close() }()

	uploads := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatalf("mkdir uploads: %v", err)
	}
	// A referenced upload (completed batch) must survive around the temps.
	kept := filepath.Join(uploads, "aaa111deadbeef_kept.xlsx")
	writeFile(t, kept)
	gcTestBatch(t, a.DB, "bat_gc_temps", "completed", kept)
	// Interrupted staging temps of the resource/metrics channels — fresh,
	// zero age (see the comment above for why freshness must not keep
	// them).
	temp1 := filepath.Join(uploads, ".tmp-112233445")
	temp2 := filepath.Join(uploads, ".tmp-998877665")
	writeFile(t, temp1)
	writeFile(t, temp2)

	res, err := a.GCOrphanUploads(context.Background())
	if err != nil {
		t.Fatalf("GCOrphanUploads: %v", err)
	}
	if res.Scanned != 3 || res.Deleted != 2 || res.Referenced != 1 || res.Errors != 0 {
		t.Fatalf("gc result = %+v, want scanned=3 deleted=2 referenced=1 errors=0", res)
	}
	for _, p := range []string{temp1, temp2} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("staging temp %s still exists (stat err = %v)", p, err)
		}
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("referenced upload %s missing: %v", kept, err)
	}

	// Idempotent second sweep: only the referenced upload remains.
	res, err = a.GCOrphanUploads(context.Background())
	if err != nil {
		t.Fatalf("second GCOrphanUploads: %v", err)
	}
	if res.Deleted != 0 || res.Scanned != 1 || res.Referenced != 1 {
		t.Fatalf("second gc result = %+v, want deleted=0 scanned=1 referenced=1", res)
	}
}

// TestGCSweepServeStartup pins the serve-startup wiring helper (#106-C):
// both sweeps run through gcSweep — the uploads staging sweep and the fee
// analysis sweep — and a BROKEN uploads sweep must not stop the fees
// sweep (log-and-continue, never fatal, the serve doctrine).
func TestGCSweepServeStartup(t *testing.T) {
	dir := testsupport.TempDir(t)
	dbPath := filepath.Join(dir, "netops.db")

	// Seed orphans of both worlds through the app wiring (same DB/dataDir
	// the helper re-opens): a flat upload orphan, a fee staging orphan and
	// a fee analysis orphan (backdated past the milestone).
	a, err := New(Config{DBPath: dbPath, DataDir: dir})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	flatOrphan := filepath.Join(a.Config.DataDir, "uploads", "eee555deadbeef_flat.xlsx")
	if err := os.MkdirAll(filepath.Dir(flatOrphan), 0o755); err != nil {
		t.Fatalf("mkdir uploads: %v", err)
	}
	writeFile(t, flatOrphan)
	feeStagedOrphan := filepath.Join(a.Config.DataDir, "uploads", "fees", "fff666deadbeef_fee.csv")
	if err := os.MkdirAll(filepath.Dir(feeStagedOrphan), 0o755); err != nil {
		t.Fatalf("mkdir uploads/fees: %v", err)
	}
	writeFile(t, feeStagedOrphan)
	analysisOrphan := filepath.Join(a.Config.DataDir, "analysis", "fees",
		"fee_type=electricity", "year=2024", "month=05", "batch-fee_nope.parquet")
	if err := os.MkdirAll(filepath.Dir(analysisOrphan), 0o755); err != nil {
		t.Fatalf("mkdir analysis partition: %v", err)
	}
	writeFile(t, analysisOrphan)
	backdateGC(t, analysisOrphan)
	staleStaging := filepath.Join(a.Config.DataDir, "analysis", "fees", ".staging", "fee_nope2")
	if err := os.MkdirAll(staleStaging, 0o755); err != nil {
		t.Fatalf("mkdir stale staging: %v", err)
	}
	backdateGC(t, staleStaging)

	a.gcSweep(context.Background())
	for _, p := range []string{flatOrphan, feeStagedOrphan, analysisOrphan, staleStaging} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("gcSweep must sweep %s (stat err = %v)", p, err)
		}
	}
	if err := a.Close(); err != nil {
		t.Fatalf("app.Close: %v", err)
	}

	// Failure doctrine: uploads being unreadable (a regular file in the
	// way) logs an Error but NEVER blocks startup — and the fees sweep
	// still runs.
	dir2 := testsupport.TempDir(t)
	dbPath2 := filepath.Join(dir2, "netops.db")
	a2, err := New(Config{DBPath: dbPath2, DataDir: dir2})
	if err != nil {
		t.Fatalf("app.New 2: %v", err)
	}
	defer func() { _ = a2.Close() }()
	if err := os.WriteFile(filepath.Join(dir2, "uploads"), []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("plant broken uploads: %v", err)
	}
	analysisOrphan2 := filepath.Join(dir2, "analysis", "fees",
		"fee_type=electricity", "year=2024", "month=05", "batch-fee_nope.parquet")
	if err := os.MkdirAll(filepath.Dir(analysisOrphan2), 0o755); err != nil {
		t.Fatalf("mkdir analysis partition 2: %v", err)
	}
	writeFile(t, analysisOrphan2)
	backdateGC(t, analysisOrphan2)

	a2.gcSweep(context.Background()) // must not panic, must still sweep fees
	if _, err := os.Stat(analysisOrphan2); !os.IsNotExist(err) {
		t.Errorf("gcSweep must run the fees sweep even when the uploads sweep fails (stat err = %v)", err)
	}
}
