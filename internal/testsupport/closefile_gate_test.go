package testsupport

// closefile_gate_test.go — issue #33's W4 static gate: a file a test
// CREATES must be closed on EVERY exit path. The non-deferred close only
// covers the happy path — a t.Fatalf between Create and Close calls
// runtime.Goexit, deferred functions run, and the plain Close never does:
// the handle leaks until process death. On Linux that is invisible; on
// Windows it is the #28 flake factory again (a lingering handle defeats
// directory removal for whoever cleans up next).
//
// The gate: every os.Create( / os.CreateTemp( in a *_test.go under
// internal/ or cmd/ must, within the SAME function, carry a deferred
// close of the created file variable (defer ... NAME.Close()). The
// documented escape hatch for a test that genuinely keeps the handle for
// its whole life is the inline marker:
//
//      out, err := os.Create(...) // create-close-ok: <reason>

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// createHit matches a file-creating call's assignment and captures the
// variable holding the handle.
var createHit = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:,\s*[A-Za-z_][A-Za-z0-9_]*)?\s*:?=\s*os\.(Create|CreateTemp)\(`)

// TestNoUnclosedCreateInTests is the W4 gate: offenders are named
// file:line so the fix is a one-liner (add the deferred close), not an
// archaeology dig.
func TestNoUnclosedCreateInTests(t *testing.T) {
	roots := []string{filepath.Join("..", "..", "internal"), filepath.Join("..", "..", "cmd")}
	var offenders []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if filepath.Base(path) == "closefile_gate_test.go" || filepath.Base(path) == "closefile_shapes_test.go" {
				return nil // the gate + its fixture carrier name the pattern in their own text
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			lines := strings.Split(string(data), "\n")
			for i, line := range lines {
				if strings.Contains(line, "create-close-ok") {
					continue // the documented escape hatch
				}
				m := createHit.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				if hasDeferredClose(lines, i, m[1]) {
					continue
				}
				offenders = append(offenders, filepath.ToSlash(path)+":"+itoa(i+1)+"  "+strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("扫描 %s 失败: %v", root, err)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("发现 %d 处测试内建文件无 defer 关闭（t.Fatalf 中途退出时句柄泄漏到进程退出——Windows 上正是 #28 型句柄残留；补 defer func() { _ = NAME.Close() }() 或加行内豁免 create-close-ok: <理由>）:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// hasDeferredClose reports whether the function containing line idx has a
// deferred close of name. The function body ends at the next top-level
// declaration (`^func ` at column 0) — coarse, but the pattern it guards
// against (a close further out than the function) cannot compile.
func hasDeferredClose(lines []string, idx int, name string) bool {
	want := name + ".Close()"
	for j := idx + 1; j < len(lines); j++ {
		if strings.HasPrefix(lines[j], "func ") {
			return false // left the creating function without a defer
		}
		if strings.Contains(lines[j], "defer") && strings.Contains(lines[j], want) {
			return true
		}
	}
	return false
}
