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
