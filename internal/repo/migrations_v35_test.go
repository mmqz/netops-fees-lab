package repo

// migrations_v35_test.go — issue #6: the source_path migration is a pure
// additive ALTER (the v31/v34 non-rebuild shape), old rows default to ''
// (unknown — verify-sources skips them), and the migration chain reaches
// v35 from scratch.
import (
	"context"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// TestMigrationV35SourcePath pins the migration shape and its semantics.
func TestMigrationV35SourcePath(t *testing.T) {
	db := newTestDB(t) // migrates to head (v35) from an empty file
	ctx := context.Background()

	// A pre-v35-style row (source_path '') inserts via the current column
	// list with the default '' — the "unknown" verification class.
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	if err := db.FeeBatches.Create(ctx, domain.FeeBatch{
		ID: "fee_v35_check", WorkbookName: "legacy.csv", SHA256: "aaaa",
		FeeType: domain.FeeTypeElectricity, FeeYear: 2026, FeeMonth: 1,
		SnapshotPath: "fee-imports/x", Mapping: "{}", SourcePath: "",
		Status: domain.FeeStatusCompleted, CreatedAt: now,
	}); err != nil {
		t.Fatalf("create legacy-style batch: %v", err)
	}
	got, err := db.FeeBatches.GetByID(ctx, "fee_v35_check")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SourcePath != "" {
		t.Fatalf("default source_path = %q, want empty", got.SourcePath)
	}

	// A v35-style row round-trips the absolute path verbatim.
	if err := db.FeeBatches.Create(ctx, domain.FeeBatch{
		ID: "fee_v35_path", WorkbookName: "jan.xls", SHA256: "bbbb",
		FeeType: domain.FeeTypeElectricity, FeeYear: 2026, FeeMonth: 2,
		SnapshotPath: "fee-imports/y", Mapping: "{}",
		SourcePath: "/srv/ledgers/2026/01月台账.xls",
		Status:     domain.FeeStatusCompleted, CreatedAt: now,
	}); err != nil {
		t.Fatalf("create v35 batch: %v", err)
	}
	got, err = db.FeeBatches.GetByID(ctx, "fee_v35_path")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SourcePath != "/srv/ledgers/2026/01月台账.xls" {
		t.Fatalf("source_path roundtrip = %q", got.SourcePath)
	}
}
