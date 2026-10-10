package repo

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// newChangeDB opens a fresh database seeded with one batch + one device
// resource and returns it (closed at test cleanup).
func newChangeDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	ctx := context.Background()
	err = db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_chg", simTS)); err != nil {
			return err
		}
		if err := tx.Batches.Create(ctx, newBatch("bat_chg2", simTS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, newDeviceResource("res_chg_dev", "bat_chg"))
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// chg1..chg3 are three pending proposals used by the filter tests.
var (
	tsA = simTS
	tsB = simTS.Add(1 * time.Minute)
	tsC = simTS.Add(2 * time.Minute)
)

func chg(id, field, oldV, newV string, ts time.Time) domain.AttributeChange {
	return domain.AttributeChange{
		ID: id, ResourceID: "res_chg_dev", BatchID: "bat_chg",
		Field: field, OldValue: oldV, NewValue: newV,
		Status: domain.ChangeStatusPending, Sheet: "设备清单", RowNumber: 2, CreatedAt: ts,
	}
}

// TestAttributeChangeInsertListAndLifecycle pins the repo CRUD of
// resource_attribute_changes: InsertBatch, List filters (status / resource
// / batch) with pagination and totals, the created_at ordering, the
// pending-flip methods and the pending-scope delete/count helpers.
func TestAttributeChangeInsertListAndLifecycle(t *testing.T) {
	db := newChangeDB(t)
	ctx := context.Background()

	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.AttributeChanges.InsertBatch(ctx, []domain.AttributeChange{
			chg("chg_a", "mgmt_ip", "10.0.0.1", "10.0.0.9", tsA),
			chg("chg_b", "vendor", "华为", "中兴", tsB),
		})
	}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	// List all: chronological order, total 2.
	all, total, err := db.AttributeChanges.List(ctx, ChangeFilter{})
	if err != nil || total != 2 || len(all) != 2 {
		t.Fatalf("List all = %d items (total %d, err %v), want 2/2", len(all), total, err)
	}
	if all[0].ID != "chg_a" || all[1].ID != "chg_b" {
		t.Errorf("List order = %s, %s, want created_at order chg_a, chg_b", all[0].ID, all[1].ID)
	}

	// Pagination: first of two.
	page1, total, err := db.AttributeChanges.List(ctx, ChangeFilter{Limit: 1, Offset: 0})
	if err != nil || total != 2 || len(page1) != 1 || page1[0].ID != "chg_a" {
		t.Errorf("List limit=1 = %+v (total %d, err %v), want only chg_a", page1, total, err)
	}
	page2, total, err := db.AttributeChanges.List(ctx, ChangeFilter{Limit: 1, Offset: 1})
	if err != nil || total != 2 || len(page2) != 1 || page2[0].ID != "chg_b" {
		t.Errorf("List limit=1 offset=1 = %+v (total %d, err %v), want only chg_b", page2, total, err)
	}

	// Get an unknown id.
	if _, err := db.AttributeChanges.Get(ctx, "chg_nope"); err == nil || !isNotFoundErr(err) {
		t.Errorf("Get unknown = %v, want domain.ErrNotFound", err)
	}

	// MarkApplied flips chg_a and stamps applied_at.
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.AttributeChanges.MarkApplied(ctx, "chg_a", tsC)
	}); err != nil {
		t.Fatalf("MarkApplied: %v", err)
	}
	applied, err := db.AttributeChanges.Get(ctx, "chg_a")
	if err != nil || applied.Status != domain.ChangeStatusApplied ||
		applied.AppliedAt == nil || !applied.AppliedAt.Equal(tsC) {
		t.Errorf("chg_a after MarkApplied = %+v (err %v), want applied at tsC", applied, err)
	}
	// A second MarkApplied refuses (not pending → ErrConflict).
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.AttributeChanges.MarkApplied(ctx, "chg_a", tsC)
	}); err == nil || !isConflictErr(err) {
		t.Errorf("re-MarkApplied = %v, want domain.ErrConflict", err)
	}
	// MarkApplied of an unknown id also conflicts (0 rows affected).
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.AttributeChanges.MarkApplied(ctx, "chg_nope", tsC)
	}); err == nil || !isConflictErr(err) {
		t.Errorf("MarkApplied unknown = %v, want domain.ErrConflict", err)
	}

	// MarkDismissed flips chg_b (applied_at stays NULL).
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.AttributeChanges.MarkDismissed(ctx, "chg_b")
	}); err != nil {
		t.Fatalf("MarkDismissed: %v", err)
	}
	dismissed, err := db.AttributeChanges.Get(ctx, "chg_b")
	if err != nil || dismissed.Status != domain.ChangeStatusDismissed || dismissed.AppliedAt != nil {
		t.Errorf("chg_b after MarkDismissed = %+v (err %v), want dismissed with NULL applied_at", dismissed, err)
	}

	// Status filter: pending only → empty; applied only → chg_a.
	pend, total, err := db.AttributeChanges.List(ctx, ChangeFilter{Status: domain.ChangeStatusPending})
	if err != nil || total != 0 || len(pend) != 0 {
		t.Errorf("List pending = %d (total %d, err %v), want empty", len(pend), total, err)
	}
	app, total, err := db.AttributeChanges.List(ctx, ChangeFilter{Status: domain.ChangeStatusApplied})
	if err != nil || total != 1 || len(app) != 1 || app[0].ID != "chg_a" {
		t.Errorf("List applied = %+v (total %d, err %v), want chg_a", app, total, err)
	}
	// Invalid status filter is an error.
	if _, _, err := db.AttributeChanges.List(ctx, ChangeFilter{Status: domain.ChangeStatus("bogus")}); err == nil {
		t.Errorf("List bogus status = nil error, want error")
	}

	// Resource filter and a change on another batch.
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.AttributeChanges.InsertBatch(ctx, []domain.AttributeChange{{
			ID: "chg_c", ResourceID: "res_chg_dev", BatchID: "bat_chg2",
			Field: "mgmt_ip", OldValue: "10.0.0.1", NewValue: "10.0.0.9",
			Status: domain.ChangeStatusPending, Sheet: "设备清单", RowNumber: 2, CreatedAt: tsC,
		}})
	}); err != nil {
		t.Fatalf("InsertBatch chg_c: %v", err)
	}
	byRes, total, err := db.AttributeChanges.List(ctx, ChangeFilter{ResourceID: "res_chg_dev"})
	if err != nil || total != 3 || len(byRes) != 3 {
		t.Errorf("List by resource = %d (total %d, err %v), want 3", len(byRes), total, err)
	}
	byBatch, total, err := db.AttributeChanges.List(ctx, ChangeFilter{BatchID: "bat_chg2"})
	if err != nil || total != 1 || len(byBatch) != 1 || byBatch[0].ID != "chg_c" {
		t.Errorf("List by batch = %+v (total %d, err %v), want only chg_c", byBatch, total, err)
	}

	// ListPendingByResource: only the pending rows of the resource.
	pending, err := db.AttributeChanges.ListPendingByResource(ctx, "res_chg_dev")
	if err != nil || len(pending) != 1 || pending[0].ID != "chg_c" {
		t.Errorf("ListPendingByResource = %+v (err %v), want only chg_c", pending, err)
	}

	// DeletePendingByBatch removes only the batch's pending rows.
	n, err := db.AttributeChanges.DeletePendingByBatch(ctx, "bat_chg2")
	if err != nil || n != 1 {
		t.Fatalf("DeletePendingByBatch = %d rows (err %v), want 1", n, err)
	}
	if _, err := db.AttributeChanges.Get(ctx, "chg_c"); err == nil || !isNotFoundErr(err) {
		t.Errorf("chg_c after delete = %v, want not found", err)
	}
	// The applied/dismissed rows of bat_chg survive.
	if _, err := db.AttributeChanges.Get(ctx, "chg_a"); err != nil {
		t.Errorf("applied chg_a after pending delete: %v", err)
	}
	if _, err := db.AttributeChanges.Get(ctx, "chg_b"); err != nil {
		t.Errorf("dismissed chg_b after pending delete: %v", err)
	}

	// CountPendingByBatch counts only pending rows.
	count, err := db.AttributeChanges.CountPendingByBatch(ctx, "bat_chg")
	if err != nil || count != 0 {
		t.Errorf("CountPendingByBatch(bat_chg) = %d (err %v), want 0 (all terminal)", count, err)
	}
}

// TestAttributeChangeInsertBatchTransaction pins the transactional
// behavior of InsertBatch inside DB.Do: an error after the inserts rolls
// every change row back (zero residue).
func TestAttributeChangeInsertBatchTransaction(t *testing.T) {
	db := newChangeDB(t)
	ctx := context.Background()

	err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.AttributeChanges.InsertBatch(ctx, []domain.AttributeChange{
			chg("chg_tx1", "mgmt_ip", "10.0.0.1", "10.0.0.9", tsA),
			chg("chg_tx2", "vendor", "华为", "中兴", tsB),
		}); err != nil {
			return err
		}
		return errBoom // abort AFTER the inserts
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("Do = %v, want boom", err)
	}
	_, total, err := db.AttributeChanges.List(ctx, ChangeFilter{})
	if err != nil || total != 0 {
		t.Errorf("changes after rollback = %d (err %v), want 0", total, err)
	}
}

// errBoom aborts a test transaction after some writes.
var errBoom = errors.New("boom")

func isNotFoundErr(err error) bool { return errors.Is(err, domain.ErrNotFound) }

func isConflictErr(err error) bool { return errors.Is(err, domain.ErrConflict) }
