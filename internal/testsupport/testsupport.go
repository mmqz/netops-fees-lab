// Package testsupport provides cross-platform test scaffolding shared by
// every suite in this repository.
//
// Issue #38 (audit-v0.8.0 §2.1.1): modernc.org/sqlite may release the
// database file handle a few milliseconds AFTER db.Close returns, so on
// Windows the RemoveAll cleanup of a plain t.TempDir fails with
// "being used by another process" and turns a green suite into a
// platform-only failure. v0.6.2 (issue #5) fixed this with per-package
// copies (sim + repo); v0.7.0's new tests then re-introduced bare
// t.TempDir and the failure came back in six more packages. TempDir is the
// single shared replacement: any test whose directory hosts a SQLite
// database — directly via repo.Open or indirectly via app.New / runCLI --
// must call testsupport.TempDir(t) instead of t.TempDir().
package testsupport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TempDir is t.TempDir with a Windows-tolerant cleanup: os.MkdirTemp plus a
// cleanup that retries RemoveAll for ~1s before giving up. POSIX removes on
// the first try and never sleeps; a residual failure is logged, never
// failed. Tests that open SQLite databases in the directory must use this
// helper so the platform workaround never has to be re-discovered per
// package.
func TempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "netops-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		var last error
		for i := 0; i < 25; i++ {
			if last = os.RemoveAll(dir); last == nil {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
		t.Logf("tempdir cleanup: RemoveAll(%s) failed after ~1s of retries: %v (Windows 句柄释放延迟，忽略)", dir, last)
	})
	return dir
}

// FactsFileInt reads the integer declared for one key in the machine-
// readable fact source (docs/facts.md, issue #110). The drift-gate tests
// assert registry / route-table / migration-list REALITY against the
// numbers declared there, so prose counts and code can never disagree
// silently — the TestAppVersionMatchesChangelogHead discipline
// generalized from one version string to every count that prose tends to
// freeze and lie about ("恰好 5 个" survived three tool additions).
//
// The row format is a markdown table line whose first cell is the
// backticked key and whose second cell is a BARE integer; description
// prose lives in the third cell (the contract is documented in
// docs/facts.md §4). ok=false means the facts file is not readable (the
// package is being tested outside the repo tree) and the caller must
// SKIP, exactly like TestAppVersionMatchesChangelogHead; a readable file
// with a missing key row or a non-integer value cell is a hard failure —
// a broken fact source is the one bug this helper must never swallow.
//
// The relative path assumes the caller's test binary runs with the CWD of
// an internal/<pkg> package directory (go test's contract), which every
// gate test satisfies.
func FactsFileInt(t *testing.T, key string) (n int, ok bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "facts.md"))
	if err != nil {
		return 0, false
	}
	prefix := "| `" + key + "` |"
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), prefix) {
			continue
		}
		cells := strings.Split(strings.TrimSpace(line), "|")
		// cells[0] is the empty text before the leading pipe, cells[1] the
		// key cell, cells[2] the bare-integer value cell.
		if len(cells) < 3 {
			t.Fatalf("docs/facts.md 的 %q 行形态不合法（应为三单元格表格行）：%s", key, line)
		}
		v, err := strconv.Atoi(strings.TrimSpace(cells[2]))
		if err != nil {
			t.Fatalf("docs/facts.md 键 %q 的当前值单元格 %q 不是裸整数（契约见 facts.md §4）：%v", key, strings.TrimSpace(cells[2]), err)
		}
		return v, true
	}
	t.Fatalf("docs/facts.md 中找不到键 %q 的行——事实源缺行即漂移（门禁测试无法对照）", key)
	return 0, true
}
