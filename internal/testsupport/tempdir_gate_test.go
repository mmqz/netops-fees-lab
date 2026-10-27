package testsupport

// tempdir_gate_test.go — issue #28's root fix: bare t.TempDir() must not
// re-enter the repository. The standard library's cleanup fails a test
// outright when Windows has not released a handle yet (unlinkat: The
// directory is not empty) — the flake factory behind the reported 3-of-4
// random Windows failures; testsupport.TempDir retries ~1s and never
// fails the suite on a lingering handle. The gate scans every *_test.go
// under internal/ and cmd/ for the bare pattern; an escape hatch exists
// for a test that genuinely needs the stdlib semantics — the exact
// exemption marker on the same line:
//
//      dir := t.TempDir() // stdlib-tempdir-ok: <reason>
//
// (issue 方案 B: the mechanical sweep of 方案 A already landed — this gate
// is what keeps it landed.)

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoStdlibTempDir is the static gate. A hit names the file:line so the
// fix is a one-liner, not an archaeology dig.
func TestNoStdlibTempDir(t *testing.T) {
	// this test binary runs with CWD = the testsupport package dir; the
	// repository root sits two levels up (internal/testsupport).
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
			// the gate itself names the pattern in prose — its scanning
			// logic is the ONLY legitimate occurrence, and it lives in
			// this file.
			if filepath.Base(path) == "tempdir_gate_test.go" {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			for i, line := range strings.Split(string(data), "\n") {
				if !bareTempDirHit(line) {
					continue
				}
				if strings.Contains(line, "stdlib-tempdir-ok") {
					continue // the documented escape hatch
				}
				offenders = append(offenders, filepath.ToSlash(path)+":"+itoa(i+1)+"  "+strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			// the gate MUST NOT skip silently when it cannot read its
			// own tree (the #21 discipline, applied to the tooling).
			t.Fatalf("扫描 %s 失败: %v", root, err)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("发现 %d 处裸 t.TempDir(（Windows 清理不可重试，句柄延迟会随机挂测试；改用 testsupport.TempDir(t)，如确需标准库语义加同款行内豁免标记）:\n%s",
			len(offenders), strings.Join(offenders, "\n"))
	}
}

// bareTempDirHit reports whether the line calls the BARE stdlib
// t.TempDir( — the receiver must be a standalone t, not the tail of
// testsupport.TempDir( or any other qualified selector (the gate's own
// replacement text embeds the pattern as a substring, so a naive
// strings.Contains would flag all 216 of the very calls the sweep
// landed).
func bareTempDirHit(line string) bool {
	for off := 0; ; {
		i := strings.Index(line[off:], "t.TempDir(")
		if i < 0 {
			return false
		}
		i += off
		prev := byte('.')
		if i > 0 {
			prev = line[i-1]
		}
		switch prev {
		case 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm',
			'n', 'o', 'p', 'q', 'r', 's', 'u', 'v', 'w', 'x', 'y', 'z',
			'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M',
			'N', 'O', 'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z',
			'_', '.', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			off = i + 1 // qualified selector — not the bare call
			continue
		}
		return true
	}
}

// itoa is the tiny local int renderer (strconv stays out — nothing in the
// gate needs formatting beyond a line number).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
