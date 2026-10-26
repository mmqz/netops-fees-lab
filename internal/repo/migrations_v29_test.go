package repo

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// feeTS is the deterministic clock of this test file.
var feeTS = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// newFeeBatch builds one completed-shaped fee batch for the tests.
func newFeeBatch(id string, sha string, status domain.FeeStatus) domain.FeeBatch {
	return domain.FeeBatch{
		ID: id, WorkbookName: "bills.csv", SHA256: sha, FeeType: domain.FeeTypeElectricity,
		FeeYear: 2024, FeeMonth: 5, SnapshotPath: "fee-imports/electricity/2024/05/" + sha + "_bills.csv",
		Mapping: `{"feeType":"electricity","columns":{"amount":"电费(元)","period":"账期","siteCode":"站址编码"}}`,
		Status:  status, RowsTotal: 3, RowsImported: 2, RowsFailed: 1,
		CreatedAt: feeTS,
	}
}

// TestMigrationV29AddsFeeBatches pins the v29 CREATE TABLE migration: the
// fee_batches table appears on reopen, the CHECK constraints (fee type /
// status / period bounds) reject bogus values, the partial idempotency
// index enforces "one completed batch per key" while failed/rolled_back
// rows never occupy the key, and MAX(version) advances to 29.
func TestMigrationV29AddsFeeBatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "netops.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	if ok, err := tableExists(ctx, db, "fee_batches"); err != nil || !ok {
		t.Fatalf("fee_batches exists = %v (err %v), want true", ok, err)
	}

	// A completed batch round-trips with every field.
	b := newFeeBatch("fee_1", "cafe01", domain.FeeStatusCompleted)
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.FeeBatches.Create(ctx, b)
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := db.FeeBatches.GetByID(ctx, "fee_1")
	if err != nil || got.WorkbookName != "bills.csv" || got.FeeType != domain.FeeTypeElectricity ||
		got.FeeYear != 2024 || got.FeeMonth != 5 || got.Status != domain.FeeStatusCompleted ||
		got.RowsTotal != 3 || got.RowsImported != 2 || got.RowsFailed != 1 || got.CompletedAt != nil {
		t.Fatalf("round-trip = %+v (err %v)", got, err)
	}
	if !strings.Contains(got.Mapping, "siteCode") {
		t.Errorf("canonical mapping = %q, want the column mapping JSON", got.Mapping)
	}

	// CHECK constraints: bogus fee type / status / month / year.
	for name, bad := range map[string]domain.FeeBatch{
		"fee_type": {ID: "fee_bad1", WorkbookName: "b", SHA256: "s1", FeeType: "groceries", FeeYear: 2024, FeeMonth: 5, Status: domain.FeeStatusCompleted, CreatedAt: feeTS},
		"status":   {ID: "fee_bad2", WorkbookName: "b", SHA256: "s2", FeeType: domain.FeeTypeElectricity, FeeYear: 2024, FeeMonth: 5, Status: "awaiting", CreatedAt: feeTS},
		"month":    {ID: "fee_bad3", WorkbookName: "b", SHA256: "s3", FeeType: domain.FeeTypeElectricity, FeeYear: 2024, FeeMonth: 13, Status: domain.FeeStatusCompleted, CreatedAt: feeTS},
		"year":     {ID: "fee_bad4", WorkbookName: "b", SHA256: "s4", FeeType: domain.FeeTypeElectricity, FeeYear: 1999, FeeMonth: 5, Status: domain.FeeStatusCompleted, CreatedAt: feeTS},
	} {
		err := db.Do(ctx, func(tx *Tx) error { return tx.FeeBatches.Create(ctx, bad) })
		if err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("insert bogus %s = %v, want CHECK constraint error", name, err)
		}
	}

	// Idempotency: the same key CAN be failed, and CAN be completed only
	// once. The partial index covers completed rows only.
	failed := newFeeBatch("fee_f1", "cafe01", domain.FeeStatusFailed)
	if err := db.Do(ctx, func(tx *Tx) error { return tx.FeeBatches.Create(ctx, failed) }); err != nil {
		// a failed row with the same key as the completed fee_1 is legal
		t.Errorf("failed row with an occupied completed key must be insertable: %v", err)
	}
	second := newFeeBatch("fee_2", "cafe01", domain.FeeStatusCompleted)
	err = db.Do(ctx, func(tx *Tx) error { return tx.FeeBatches.Create(ctx, second) })
	if err == nil || !isUniqueViolation(err) {
		t.Errorf("second completed batch with the same key must violate the partial unique index, got %v", err)
	}
	// same key, DIFFERENT period → free (coexistence across periods).
	otherPeriod := newFeeBatch("fee_3", "cafe01", domain.FeeStatusCompleted)
	otherPeriod.FeeMonth = 6
	if err := db.Do(ctx, func(tx *Tx) error { return tx.FeeBatches.Create(ctx, otherPeriod) }); err != nil {
		t.Errorf("same key in a different period must be insertable: %v", err)
	}

	// CompletedByIdempotencyKey finds fee_1, not the failed twin.
	hit, err := db.FeeBatches.CompletedByIdempotencyKey(ctx, "cafe01", domain.FeeTypeElectricity, 2024, 5, b.Mapping, "")
	if err != nil || hit.ID != "fee_1" {
		t.Fatalf("idempotency lookup = %+v (err %v), want fee_1", hit, err)
	}
	if _, err := db.FeeBatches.CompletedByIdempotencyKey(ctx, "cafe01", domain.FeeTypeRepair, 2024, 5, b.Mapping, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("lookup with a different type must be not found, got %v", err)
	}

	// MarkCompleted is the commit point: the failed twin flips to
	// completed and NOW the race loser answers the sentinel.
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.FeeBatches.MarkFailed(ctx, "fee_1", 3, 1, "race")
	}); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.FeeBatches.MarkCompleted(ctx, "fee_f1", 3, 2, 1, feeTS.Add(time.Hour))
	}); err != nil {
		t.Fatalf("complete the failed twin: %v", err)
	}
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.FeeBatches.MarkCompleted(ctx, "fee_1", 3, 2, 1, feeTS.Add(time.Hour))
	}); err != ErrFeeIdempotentDuplicate {
		t.Fatalf("completing the second holder of the key must answer ErrFeeIdempotentDuplicate, got %v", err)
	}

	// List filters by status; newest first. fee_1's second completion was
	// refused (its transaction rolled back) so it STAYS failed; the
	// completed set is the race winner fee_f1 + the other period fee_3.
	if list, err := db.FeeBatches.List(ctx, domain.FeeStatusCompleted); err != nil || len(list) != 2 {
		t.Fatalf("completed list = %d (err %v), want 2", len(list), err)
	}
	if list, err := db.FeeBatches.List(ctx, ""); err != nil || len(list) != 3 {
		t.Fatalf("all list = %d (err %v), want 3", len(list), err)
	}

	// Rollback: completed → rolled_back; re-rollback answers not found
	// (only completed rows are rollable).
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.FeeBatches.MarkRolledBack(ctx, "fee_f1")
	}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.FeeBatches.MarkRolledBack(ctx, "fee_f1")
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("re-rollback = %v, want ErrNotFound", err)
	}

	// Live batch ids (the GC keep-set) = the completed rows only.
	live, err := db.FeeBatches.ListParquetLiveBatchIDs(ctx)
	if err != nil || len(live) != 1 {
		t.Fatalf("live ids = %v (err %v), want only fee_3", live, err)
	}
	if _, ok := live["fee_f1"]; ok {
		t.Error("rolled_back fee_f1 must not be live")
	}
	if _, ok := live["fee_3"]; !ok {
		t.Error("completed fee_3 must be live")
	}

	var maxV int
	if err := db.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 39 {
		t.Errorf("max schema version = %d, want 39", maxV)
	}
}
