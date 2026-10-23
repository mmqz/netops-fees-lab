package repo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// v34TS is the deterministic clock of this test file.
var v34TS = time.Date(2026, 10, 23, 8, 0, 0, 0, time.UTC)

// withoutV34 temporarily unregisters v34 from the migration registry so a
// test can build a genuine v33 database first (the withoutV31 pattern).
func withoutV34(t *testing.T) {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	for _, m := range migrations {
		if m.Version <= 33 {
			filtered = append(filtered, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
}

// TestMigrationV34AddsFeeBatchSheet pins the #125 schema move: a genuine
// v33 database upgrades in place to v34 (sheet column exists, defaults to
// ”), the idempotency index is rebuilt WITH the sheet coordinate, and the
// historical single-sheet key semantics survive —
//
//  1. a completed pre-v34 batch keeps blocking a same-key re-import
//     (sheet ” == the new default ”);
//  2. two completed batches of the same file hash + fee type + period +
//     mapping but DIFFERENT sheets now coexist (the same-type-twice
//     workbook, impossible to import before v34);
//  3. the same (sha, sheet, type, period, mapping) still refuses a second
//     completed batch — the one-completed-per-key doctrine is unchanged.
func TestMigrationV34AddsFeeBatchSheet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: a genuine v33 database with one completed legacy batch
	// (the sheet column does not exist yet — raw SQL).
	withoutV34(t)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open v33 database: %v", err)
	}
	const legacy = `INSERT INTO fee_batches
(id, workbook_name, sha256, fee_type, fee_year, fee_month, snapshot_path, mapping, status, created_at)
VALUES ('fee_legacy', '账本.xlsx', 'sha125', 'electricity', 2024, 5, 'fee-imports/electricity/2024/05/sha125_账本.xlsx',
        '{"columns":{}}', 'completed', '2026-10-23T08:00:00Z')`
	if _, err := db.sql.ExecContext(ctx, legacy); err != nil {
		t.Fatalf("seed legacy batch: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close v33 database: %v", err)
	}

	// Phase 2: restore the registry and reopen — v34 applies. The
	// legacy row gains sheet=''.
	migrations = append(append([]Migration(nil), migrations...), migrationV34)
	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen with v34: %v", err)
	}
	defer func() { _ = db.Close() }()
	var version int
	if err := db.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if version != 34 {
		t.Fatalf("MAX(version) = %d, want 34", version)
	}
	got, err := db.FeeBatches.GetByID(ctx, "fee_legacy")
	if err != nil || got.Sheet != "" {
		t.Fatalf("legacy batch after v34 = %+v (err %v), want sheet %q", got, err, "")
	}

	// 1. Historical idempotency survives: the same key (sheet '') still
	// finds the legacy completed batch.
	dup, err := db.FeeBatches.CompletedByIdempotencyKey(ctx, "sha125", "electricity", 2024, 5, `{"columns":{}}`, "")
	if err != nil || dup.ID != "fee_legacy" {
		t.Fatalf("legacy key lookup = %+v (err %v), want fee_legacy", dup, err)
	}

	// 2. Same file hash + type + period + mapping, two DIFFERENT sheets —
	// both complete (the collision the migration exists to fix).
	for _, sheet := range []string{"电费A", "电费B"} {
		if err := db.Do(ctx, func(tx *Tx) error {
			return tx.FeeBatches.Create(ctx, newV34Batch("fee_"+sheet, "shaB", sheet))
		}); err != nil {
			t.Fatalf("create %s: %v", sheet, err)
		}
		if err := db.FeeBatches.MarkCompleted(ctx, "fee_"+sheet, 2, 2, 0, v34TS); err != nil {
			t.Fatalf("complete %s: %v", sheet, err)
		}
	}

	// 3. The same (sha, sheet, …) key still refuses a second completion.
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.FeeBatches.Create(ctx, newV34Batch("fee_second", "shaB", "电费A"))
	}); err != nil {
		t.Fatalf("create second: %v", err)
	}
	if err := db.FeeBatches.MarkCompleted(ctx, "fee_second", 1, 1, 0, v34TS); err == nil {
		t.Fatalf("second completed batch of the same (sha, sheet, type, period, mapping) must answer ErrFeeIdempotentDuplicate")
	}

	// The pre-flight duplicate check finds the per-sheet completed batch.
	perSheet, err := db.FeeBatches.CompletedByIdempotencyKey(ctx, "shaB", domain.FeeTypeTowerLease, 2024, 5, `{"columns":{}}`, "电费A")
	if err != nil || perSheet.ID != "fee_电费A" {
		t.Fatalf("per-sheet key lookup = %+v (err %v), want fee_电费A", perSheet, err)
	}
}

// newV34Batch builds the Create payload of one v34 batch with a fixed key
// shape (shaB + the given sheet + tower_lease + 2024-05 + the empty
// mapping), sharing the canonical mapping literal across cases so the
// idempotency comparisons are exact.
func newV34Batch(id, sha, sheet string) domain.FeeBatch {
	return domain.FeeBatch{
		ID: id, WorkbookName: "账本.xlsx", SHA256: sha, Sheet: sheet,
		FeeType: domain.FeeTypeTowerLease, FeeYear: 2024, FeeMonth: 5,
		SnapshotPath: "fee-imports/tower_lease/2024/05/" + sha + "_账本.xlsx",
		Mapping:      `{"columns":{}}`, Status: domain.FeeStatusFailed, CreatedAt: v34TS,
	}
}
