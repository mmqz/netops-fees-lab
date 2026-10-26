package repo

// migrations_v36_test.go — issue #15: the period-provenance migration is a
// pure additive ALTER (the v31/v34/v35 non-rebuild shape), legacy rows
// default to 'manual' with '' basis (the pre-#15 reality: human-declared,
// never verified), new rows round-trip both fields, and the chain reaches
// v36 from scratch.
import (
	"context"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

func TestMigrationV36PeriodProvenance(t *testing.T) {
	db := newTestDB(t) // migrates to head (v36) from an empty file
	ctx := context.Background()

	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	// A pre-v36-style row (zero-value provenance fields) inserts with the
	// 'manual' default — the honest label for a period that was never
	// content-verified.
	if err := db.FeeBatches.Create(ctx, domain.FeeBatch{
		ID: "fee_v36_legacy", WorkbookName: "legacy.csv", SHA256: "aaaa",
		FeeType: domain.FeeTypeElectricity, FeeYear: 2026, FeeMonth: 1,
		SnapshotPath: "fee-imports/x", Mapping: "{}",
		Status: domain.FeeStatusCompleted, CreatedAt: now,
	}); err != nil {
		t.Fatalf("create legacy-style batch: %v", err)
	}
	got, err := db.FeeBatches.GetByID(ctx, "fee_v36_legacy")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PeriodSource != "manual" || got.PeriodBasis != "" {
		t.Fatalf("legacy provenance = %q / %q, want manual / empty", got.PeriodSource, got.PeriodBasis)
	}

	// A v36-style row round-trips the three-tier verdict verbatim.
	if err := db.FeeBatches.Create(ctx, domain.FeeBatch{
		ID: "fee_v36_auto", WorkbookName: "payroll.xlsx", SHA256: "bbbb",
		FeeType: domain.FeeTypeMaintenance, FeeYear: 2026, FeeMonth: 9,
		SnapshotPath: "fee-imports/y", Mapping: "{}",
		PeriodSource: "auto", PeriodBasis: "账期内容核验一致（2026-09，依据：「业务账期」列（30行一致））",
		Status: domain.FeeStatusCompleted, CreatedAt: now,
	}); err != nil {
		t.Fatalf("create v36 batch: %v", err)
	}
	got, err = db.FeeBatches.GetByID(ctx, "fee_v36_auto")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.PeriodSource != "auto" || got.PeriodBasis != "账期内容核验一致（2026-09，依据：「业务账期」列（30行一致））" {
		t.Fatalf("v36 roundtrip = %q / %q", got.PeriodSource, got.PeriodBasis)
	}
}
