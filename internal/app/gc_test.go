// Uploads garbage-collection tests (W5-D, v0.4.0): orphan uploads are
// deleted; uploads referenced by ANY batch status (completed, failed,
// rolled_back) survive; a missing uploads directory is a clean no-op; the
// CLI `gc` subcommand keeps the single-JSON stdout contract.
package app

import (
	"context"
	"encoding/json"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
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
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan still exists after CLI gc (stat err = %v)", err)
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
