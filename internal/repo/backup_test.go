package repo

// Online Backup API tests (PROJECT_BRIEF_P2 §1.6/§4): Backup writes a
// byte-consistent snapshot through the SQLite Online Backup API (never a raw
// file copy), OpenReadOnly reads such a snapshot, read-only handles refuse
// writes at the SQLite level, and the error paths are wrapped with the
// "repo: backup to <path>" / "repo: open read-only <path>" prefixes.

import (
	"context"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// seedBackupSource creates a database with one workspace + one device (data
// a byte-consistent snapshot must carry) and returns the opened handle.
func seedBackupSource(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(testsupport.TempDir(t), "netops.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	w := seedWorkspace(t, db, "ws-1", "backup source", "{}")
	ctx := context.Background()
	err = db.Do(ctx, func(tx *Tx) error {
		return tx.VirtualDevices.Insert(ctx, domain.VirtualDevice{
			ID: "vd-1", WorkspaceID: w.ID, Code: "DEV-001", Name: "源设备",
			Status: domain.EntityStatusUp, CreatedAt: simTS, UpdatedAt: simTS,
		})
	})
	if err != nil {
		t.Fatalf("seed device: %v", err)
	}
	return db
}

// TestBackupSnapshotRoundTrip: Backup creates the destination file (parent
// directories included), the snapshot opens read-only, carries the exact
// rows written before the backup, and the LIVE database keeps working after
// the backup ran (that is what "online" means).
func TestBackupSnapshotRoundTrip(t *testing.T) {
	db := seedBackupSource(t)
	ctx := context.Background()
	dst := filepath.Join(testsupport.TempDir(t), "nested", "checkpoints", "ws-1", "cp-1.db")

	if err := db.Backup(ctx, dst); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if st, err := os.Stat(dst); err != nil || st.Size() == 0 {
		t.Fatalf("snapshot file %s missing or empty (stat %v, err %v)", dst, st, err)
	}

	ro, err := OpenReadOnly(dst)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	if got := ro.Path(); got != dst {
		t.Fatalf("Path() = %q, want %q", got, dst)
	}
	w, err := ro.Workspaces.GetByName(ctx, "backup source")
	if err != nil {
		t.Fatalf("snapshot read workspace: %v", err)
	}
	if w.ID != "ws-1" || w.Status != domain.WorkspaceStatusActive {
		t.Fatalf("snapshot workspace = %+v", w)
	}
	dev, err := ro.VirtualDevices.GetByID(ctx, "vd-1")
	if err != nil {
		t.Fatalf("snapshot read device: %v", err)
	}
	if dev.Code != "DEV-001" || dev.Status != domain.EntityStatusUp {
		t.Fatalf("snapshot device = %+v", dev)
	}

	// The live database still reads and writes after the backup.
	if ws, total, err := db.Workspaces.List(ctx, 1, 10); err != nil || total != 1 || len(ws) != 1 {
		t.Fatalf("live db after backup: %d workspaces (err %v), want 1", len(ws), err)
	}
	seedWorkspace(t, db, "ws-2", "post-backup write", "{}")
	if _, err := db.Workspaces.GetByName(ctx, "post-backup write"); err != nil {
		t.Fatalf("live write after backup: %v", err)
	}
	// … and the snapshot did not follow it.
	if _, err := ro.Workspaces.GetByName(ctx, "post-backup write"); err == nil {
		t.Fatalf("snapshot must not contain rows written after the backup")
	}
}

// TestBackupOverwritesStaleDestination: a second Backup into the SAME
// destination path replaces the old content (the backup API rewrites the
// destination page by page).
func TestBackupOverwritesStaleDestination(t *testing.T) {
	db := seedBackupSource(t)
	ctx := context.Background()
	dst := filepath.Join(testsupport.TempDir(t), "cp.db")

	if err := db.Backup(ctx, dst); err != nil {
		t.Fatalf("first Backup: %v", err)
	}
	// Mutate the source, back up again — the snapshot must carry the new row.
	seedWorkspace(t, db, "ws-9", "second generation", "{}")
	if err := db.Backup(ctx, dst); err != nil {
		t.Fatalf("second Backup: %v", err)
	}
	ro, err := OpenReadOnly(dst)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	if _, err := ro.Workspaces.GetByName(ctx, "second generation"); err != nil {
		t.Fatalf("second backup must overwrite the stale snapshot: %v", err)
	}
}

// TestOpenReadOnlyRefusesWrites: attempted writes on a read-only handle fail
// at the SQLite level (documented behavior of OpenReadOnly).
func TestOpenReadOnlyRefusesWrites(t *testing.T) {
	db := seedBackupSource(t)
	ctx := context.Background()
	dst := filepath.Join(testsupport.TempDir(t), "cp.db")
	if err := db.Backup(ctx, dst); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	ro, err := OpenReadOnly(dst)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close() })

	err = ro.Workspaces.Insert(ctx, domain.Workspace{
		ID: "ws-x", Name: "mutation", Status: domain.WorkspaceStatusActive,
		SourceJSON: "{}", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatalf("insert on a read-only handle must fail")
	}
	// Read-only ≠ broken: reads still work after the refused write.
	if _, err := ro.Workspaces.GetByName(ctx, "backup source"); err != nil {
		t.Fatalf("reads after a refused write: %v", err)
	}
}

// TestBackupErrorWrapping: a destination that cannot be opened as a database
// (a directory) yields an error wrapped with the repo: backup to <path>
// prefix; OpenReadOnly on a missing file fails eagerly at open time.
func TestBackupErrorWrapping(t *testing.T) {
	db := seedBackupSource(t)
	ctx := context.Background()

	dirDst := testsupport.TempDir(t) // a directory, not a file
	err := db.Backup(ctx, dirDst)
	if err == nil {
		t.Fatalf("Backup into a directory path must fail")
	}
	if !strings.Contains(err.Error(), "repo: backup to "+dirDst) {
		t.Fatalf("error %q must carry the repo: backup to %s wrap", err.Error(), dirDst)
	}

	_, err = OpenReadOnly(filepath.Join(testsupport.TempDir(t), "missing.db"))
	if err == nil {
		t.Fatalf("OpenReadOnly on a missing file must fail")
	}
	if !strings.Contains(err.Error(), "repo: open read-only") {
		t.Fatalf("error %q must carry the repo: open read-only wrap", err.Error())
	}
}
