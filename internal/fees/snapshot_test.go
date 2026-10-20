package fees

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSnapshotPathIsLogicalContract pins the #75 cross-platform contract:
// SnapshotPath is a LOGICAL path — always '/'-separated on every OS. The
// value is persisted to fee_batches.snapshot_path and echoed verbatim by
// the HTTP API and the CLI, so it must not flip with the server's
// platform (a Windows-written database copied to Linux must resolve
// identically). WriteSnapshot must conversely resolve that logical path
// on the LOCAL filesystem (FromSlash at the disk boundary) and land the
// file at the content-addressed destination.
func TestSnapshotPathIsLogicalContract(t *testing.T) {
	got := SnapshotPath(FeeTypeElectricity, 2024, 5, "deadbeef", "bills.csv")
	want := "fee-imports/electricity/2024/05/deadbeef_bills.csv"
	if got != want {
		t.Fatalf("SnapshotPath = %q, want %q", got, want)
	}
	if strings.Contains(got, `\`) {
		t.Errorf("SnapshotPath %q contains a backslash — the contract is always '/'", got)
	}
	// the path is content-addressed: same inputs → same destination, the
	// dedup anchor of the snapshot layer.
	if again := SnapshotPath(FeeTypeElectricity, 2024, 5, "deadbeef", "bills.csv"); again != got {
		t.Errorf("SnapshotPath is not deterministic: %q vs %q", got, again)
	}

	// WriteSnapshot resolves the logical path onto the local filesystem
	// (this test runs on the host OS; on Windows the FromSlash boundary
	// is what makes it pass).
	dir := t.TempDir()
	src := filepath.Join(dir, "source.csv")
	if err := os.WriteFile(src, []byte("站址编码,电费(元),账期\nS1,1.00,2024-05\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	rel, err := WriteSnapshot(dir, src, FeeTypeTowerLease, 2023, 12, "cafe01", "source.csv")
	if err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	if rel != "fee-imports/tower_lease/2023/12/cafe01_source.csv" {
		t.Fatalf("WriteSnapshot rel = %q, want the logical fee-imports/tower_lease/2023/12/cafe01_source.csv", rel)
	}
	if strings.Contains(rel, `\`) {
		t.Errorf("WriteSnapshot rel %q contains a backslash — the persisted contract is always '/'", rel)
	}
	// the file physically exists at the logical path's local resolution.
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("snapshot file missing at the FromSlash resolution: %v", err)
	}
	// idempotent re-write hits the same destination and skips the copy.
	if rel2, err := WriteSnapshot(dir, src, FeeTypeTowerLease, 2023, 12, "cafe01", "source.csv"); err != nil || rel2 != rel {
		t.Errorf("WriteSnapshot re-run = %q (err %v), want the same logical path", rel2, err)
	}
}
