package repo

// facts_gate_test.go — the #110 drift gate for the migration chain: the
// REAL length of the ordered migration list must equal the number
// declared in docs/facts.md (the machine-readable fact source), and the
// versions must stay CONTIGUOUS 1..N — a version gap would silently skip
// every intermediate schema on a fresh database while a wrong count in
// docs/architecture.md / data-model.md would keep lying about the schema
// head. The per-version upgrade tests pin each migration's behavior; this
// gate pins the chain's SHAPE.

import (
	"testing"

	"github.com/mmqz/netops-lab/internal/testsupport"
)

func TestMigrationCountMatchesFactsFile(t *testing.T) {
	want, ok := testsupport.FactsFileInt(t, "repo-migrations")
	if !ok {
		t.Skipf("docs/facts.md not readable (package tested outside the repo tree)")
	}
	if len(migrations) != want {
		t.Fatalf("migration chain holds %d versions, docs/facts.md declares repo-migrations = %d — a migration added must update the facts file in the same change (see docs/facts.md §4)", len(migrations), want)
	}
	for i, m := range migrations {
		if m.Version != i+1 {
			t.Fatalf("migrations[%d].Version = %d, want %d — the chain must stay contiguous 1..N (a gap silently skips schema on fresh databases)", i, m.Version, i+1)
		}
	}
}
