package fees

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRenameWithRetryPolicy pins the quarantine-rename retry policy
// (#124's class, rollback edition) without needing a Windows box: the
// seam is the rename function itself, so the three policy questions —
// transient failure rides the bounded retries, persistent failure
// passes through after the last attempt, ENOENT fails fast as a
// semantic answer — are all observable on Linux.
func TestRenameWithRetryPolicy(t *testing.T) {
	sharedErr := errors.New("simulated sharing violation")

	t.Run("transient failure succeeds within the retry budget", func(t *testing.T) {
		calls := 0
		rename := func(_, _ string) error {
			calls++
			if calls < 3 { // fails twice — one retry more than the
				// #82-style single retry would survive — then lands
				return sharedErr
			}
			return nil
		}
		if err := renameWithRetry(rename, []time.Duration{0, 0}, "src", "dst"); err != nil {
			t.Fatalf("renameWithRetry: %v, want nil (transient failures ride the retries)", err)
		}
		if calls != 3 {
			t.Fatalf("calls = %d, want 3 (two failures + the successful attempt)", calls)
		}
	})

	t.Run("persistent failure passes through after the last attempt", func(t *testing.T) {
		calls := 0
		rename := func(_, _ string) error {
			calls++
			return sharedErr
		}
		backoff := []time.Duration{0, 0} // zero sleep: the policy, not the clock, is under test
		err := renameWithRetry(rename, backoff, "src", "dst")
		if !errors.Is(err, sharedErr) {
			t.Fatalf("renameWithRetry err = %v, want the caller's own error object passed through", err)
		}
		if calls != len(backoff)+1 {
			t.Fatalf("calls = %d, want %d (every attempt spends its backoff slot, then stops)", calls, len(backoff)+1)
		}
	})

	t.Run("ENOENT fails fast — a vanished source is an answer, not a transient", func(t *testing.T) {
		calls := 0
		enoent := &os.PathError{Op: "rename", Path: "src", Err: os.ErrNotExist}
		rename := func(_, _ string) error {
			calls++
			return enoent
		}
		err := renameWithRetry(rename, []time.Duration{time.Hour, time.Hour}, "src", "dst")
		if !os.IsNotExist(err) {
			t.Fatalf("renameWithRetry err = %v, want ENOENT surfaced untouched", err)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1 (no retry burn on the semantic answer)", calls)
		}
	})
}

// TestQuarantineFileRidesTransientRename exercises the real quarantineFile
// (mkdir + rename + ENOENT tolerance) against a seam-injected transient
// failure: the plan step must land the file in its quarantine slot once
// the simulated sharing window clears.
func TestQuarantineFileRidesTransientRename(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "fees", "fee_type=electricity", "year=2026", "month=10", "batch-x.parquet")
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte("rows"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := rollbackPlan{
		live:       live,
		quarantine: filepath.Join(dir, "fees", quarantineDirName, "batch-x", "fee_type=electricity", "year=2026", "month=10", "batch-x.parquet"),
	}

	realRename := os.Rename
	failures := 1 // one sharing-violation-shaped blip, then the real rename
	renameWithRetrySeam = func(src, dst string) error {
		if failures > 0 {
			failures--
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: errors.New("access is denied")}
		}
		return realRename(src, dst)
	}
	defer func() { renameWithRetrySeam = os.Rename }()

	if err := quarantineFile(plan); err != nil {
		t.Fatalf("quarantineFile: %v, want the transient window ridden out", err)
	}
	if _, err := os.Stat(plan.quarantine); err != nil {
		t.Fatalf("quarantine slot: %v, want the file landed there", err)
	}
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatalf("live slot still present (err = %v), want it vacated", err)
	}
}

// TestQuarantineFileMissingSourceIsSuccess pins the pre-existing ENOENT
// idempotence (still intact under the retry wrapper): quarantining a
// batch whose file is already gone answers success, not an error.
func TestQuarantineFileMissingSourceIsSuccess(t *testing.T) {
	plan := rollbackPlan{
		live:       filepath.Join(t.TempDir(), "nope", "batch-gone.parquet"),
		quarantine: filepath.Join(t.TempDir(), "q", "batch-gone.parquet"),
	}
	if err := quarantineFile(plan); err != nil {
		t.Fatalf("quarantineFile of a vanished file: %v, want nil (idempotent no-op)", err)
	}
}
