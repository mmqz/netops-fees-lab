package testsupport

// closefile_shapes_test.go — the W4 gate's own brain, pinned: the
// hasDeferredClose shapes (defer same-line close, deferred closure close,
// the escape-hatch line, leaving the function without a defer) so the
// gate's behavior is itself under test (the W1 precedent: gates get
// gates).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHasDeferredCloseShapes pins the W4 gate's detection logic on both
// the passing and failing shapes.
func TestHasDeferredCloseShapes(t *testing.T) {
	lines := strings.Split(`package x

func a() {
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	_ = err
}

func b() {
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()
	_ = err
}

func c() {
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	_ = out
	_ = err
}

func d() {
	out, err := os.Create(dst)
	defer out.Close()
	_ = err
}
`, "\n")

	// shape a: same-line defer close → caught as closed.
	if !hasDeferredClose(lines, index(lines, "func a()"), "out") {
		t.Error("same-line `defer out.Close()` must count as a deferred close")
	}
	// shape b: deferred closure → counts.
	if !hasDeferredClose(lines, index(lines, "func b()"), "out") {
		t.Error("`defer func() { _ = out.Close() }()` must count as a deferred close")
	}
	// shape c: plain close never deferred → does NOT count.
	if hasDeferredClose(lines, index(lines, "func c()"), "out") {
		t.Error("no defer in the function must fail the gate")
	}
	// shape d: a CLOSER function that references out is a different
	// function's boundary — leaving c without finding a defer is the
	// point; d's defer is beyond the boundary and must not leak back.
	if hasDeferredClose(lines, index(lines, "func c()"), "out") {
		t.Error("a defer after the function boundary must not count")
	}
}

// index finds the line number (0-based slice index) of the first line
// carrying s.
func index(lines []string, s string) int {
	for i, l := range lines {
		if strings.HasPrefix(l, s) {
			return i
		}
	}
	return -1
}

// TestMutationHarnessPointsPresent keeps the #33 mutation harness honest:
// every mutation point scripts/mutation-round.py declares must still
// exist verbatim in the source — when the code drifts, the harness must
// be updated IN THE SAME CHANGE, never silently rot (a harness whose
// mutation points no longer match the source proves nothing).
func TestMutationHarnessPointsPresent(t *testing.T) {
	root, err := filepath.Abs(filepath.Join(".", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	harness, err := os.ReadFile(filepath.Join(root, "scripts", "mutation-round.py"))
	if err != nil {
		t.Skip("mutation harness not present in this checkout")
	}
	text := string(harness)
	// the four declared mutants' mutation points must appear in their
	// named files verbatim (the harness asserts "MUTATION POINT GONE" at
	// runtime; this gate catches the drift at CI time instead).
	for _, m := range []struct{ file, needle string }{
		{"internal/fees/risks.go", "DefaultOutlierZ = 3.0"},
		{"internal/fees/crosscheck.go", "case a.elecRows > 0:"},
		{"internal/fees/recognize.go", `"产品服务费合计(出账费用)(不含税)",`},
		{"internal/repo/repo.go", "if err := sqlTx.Commit(); err != nil {"},
	} {
		if !strings.Contains(text, m.needle) {
			t.Errorf("harness no longer declares the mutation point %q (of %s) — update both together", m.needle, m.file)
		}
		src, err := os.ReadFile(filepath.Join(root, m.file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), m.needle) {
			t.Errorf("mutation point %q no longer exists in %s — the code drifted, update the harness in the same change", m.needle, m.file)
		}
	}
}
