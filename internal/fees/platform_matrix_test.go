package fees

// platform_matrix_test.go — the #33 §6.4 face: the cross-platform behavior
// contracts that Linux-only CI can never observe directly, asserted on
// BOTH builds for the string-level invariants (GOOS-dependent behavior is
// exercised by the windows job; these tests pin the SHAPES that must hold
// on every platform):
//   - a path that went through ToSlash never carries a double backslash
//     (the #32 CI lesson: the byte-check refused every Windows batch
//     because raw OS paths flowed into it — normalize FIRST, then check);
//   - relPathOf answers the logical ('/') contract, not the OS-native
//     separator (#75's one-conversion-at-the-boundary doctrine);
//   - the snapshot + partition segment families survive any GOOS.

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPathToSlashNeverLeavesDoubleBackslash pins the normalization floor:
// for OS-native joins and for hand-built Windows forms alike, ToSlash
// output never contains a backslash pair — the byte checks downstream
// (queueDataTarget, feeTableGlob) rely on this shape.
func TestPathToSlashNeverLeavesDoubleBackslash(t *testing.T) {
	native := filepath.Join("analysis", "fees", "fee_type=electricity", "year=2024", "month=01", "b.parquet")
	windows := strings.ReplaceAll(native, "/", "\\")
	for _, in := range []string{native, windows, `fee-imports\tower_lease\2026\09\a.xlsx`} {
		got := filepath.ToSlash(in)
		if strings.Contains(got, `\\`) {
			t.Errorf("ToSlash(%q) = %q — a backslash pair survived", in, got)
		}
	}
	if runtime.GOOS == "windows" && !strings.Contains(filepath.ToSlash(windows), "/") {
		t.Error("on Windows ToSlash must convert the native separators to /")
	}
}

// TestRelPathOfAnswersSlashContract pins the logical-path contract: a
// file under the analysis dir answers its slash-separated relative path
// regardless of the OS's own separator; a path OUTSIDE the base falls
// back to the absolute slash form (never an error string, never a leak of
// the OS separator into the report text).
func TestRelPathOfAnswersSlashContract(t *testing.T) {
	base := filepath.Join("data", "analysis")
	abs := filepath.Join(base, "fees", "fee_type=electricity", "year=2024", "month=01", "b.parquet")
	rel := relPathOf(base, abs)
	if strings.ContainsRune(rel, '\\') {
		t.Errorf("relPathOf answered an OS-native separator: %q", rel)
	}
	if !strings.HasPrefix(rel, "fees/") {
		t.Errorf("relative form = %q, want fees/…", rel)
	}
	outside := relPathOf(base, filepath.Join("elsewhere", "x.parquet"))
	if strings.ContainsRune(outside, '\\') {
		t.Errorf("outside-base fallback carried a backslash: %q", outside)
	}
}

// TestSnapshotAndPartitionSegmentFamilies pins the string contracts the
// period faces parse: a snapshot path is fee-imports/<type>/<yyyy>/<mm>/…
// and a partition path carries year=<yyyy>/month=<mm> — both in slash
// form on every GOOS (VerifyBatches' period checks split on "/" and the
// invariants face re-derives from the same contract).
func TestSnapshotAndPartitionSegmentFamilies(t *testing.T) {
	snapshot := filepath.ToSlash(filepath.Join("fee-imports", "electricity", "2024", "05", "a_b.xlsx"))
	parts := strings.Split(snapshot, "/")
	if len(parts) < 4 || parts[0] != "fee-imports" || parts[2] != "2024" || parts[3] != "05" {
		t.Fatalf("snapshot segments drifted: %v", parts)
	}
	partition := filepath.ToSlash(filepath.Join("analysis", "fees", "fee_type=electricity", "year=2024", "month=01", "b.parquet"))
	if !strings.Contains(partition, "year=2024") || !strings.Contains(partition, "month=01") {
		t.Fatalf("partition segments drifted: %q", partition)
	}
}
