package domain

// facts_gate_test.go — the #110 drift gate for the resource-type
// registry: the REAL count of AllResourceTypes() must equal the number
// declared in docs/facts.md (the machine-readable fact source). The
// closed SET is already pinned four ways (domain enum ↔ SQL CHECK ↔
// /api/schema ↔ web mirror, the data-model v1.0 four-point consistency
// tests) and every consumer derives its type list from the registry
// (#91/#108 doctrine) — this gate only keeps the COUNT quoted in prose
// honest, the same way TestMCPToolCountMatchesFactsFile does for tools.
//
// The facts-row parsing is INLINED here instead of shared through
// internal/testsupport because domain is the zero-internal-dependency
// layer (architecture.md §1) and its test files keep that property too —
// the helper is nine lines; the boundary invariant is worth more.

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestResourceTypeCountMatchesFactsFile(t *testing.T) {
	raw, err := os.ReadFile("../../docs/facts.md")
	if err != nil {
		t.Skipf("docs/facts.md not readable (package tested outside the repo tree): %v", err)
	}
	want := -1
	for _, line := range strings.Split(string(raw), "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "| `resource-types` |") {
			cells := strings.Split(trimmed, "|")
			if len(cells) < 3 {
				t.Fatalf("docs/facts.md 的 resource-types 行形态不合法：%s", line)
			}
			v, err := strconv.Atoi(strings.TrimSpace(cells[2]))
			if err != nil {
				t.Fatalf("docs/facts.md 键 resource-types 的当前值单元格 %q 不是裸整数：%v", strings.TrimSpace(cells[2]), err)
			}
			want = v
			break
		}
	}
	if want < 0 {
		t.Fatalf("docs/facts.md 中找不到键 \"resource-types\" 的行——事实源缺行即漂移（门禁测试无法对照）")
	}
	if got := len(AllResourceTypes()); got != want {
		t.Fatalf("AllResourceTypes() holds %d types, docs/facts.md declares resource-types = %d — a type added or removed must update the facts file in the same change (see docs/facts.md §4)", got, want)
	}
}
