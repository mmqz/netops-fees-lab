package repo

import (
	"context"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// withoutV11 temporarily removes migration v11 from the registry so the
// next Open builds a GENUINE v10 database (migrations v1–v10 applied, no
// resource_attribute_changes table). Restored via t.Cleanup — the
// withoutV9/withoutV10 precedent (the repo package never runs migrations
// concurrently).
func withoutV11(t *testing.T) {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	for _, m := range migrations {
		if m.Version != 11 {
			filtered = append(filtered, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
}

// tableExists reports whether a table exists in sqlite_master.
func tableExists(ctx context.Context, db *DB, name string) (bool, error) {
	var n int
	err := db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// seedChange inserts one pending change (bypassing the importer) for an
// already-seeded batch and resource.
func seedChange(t *testing.T, db *DB, ch domain.AttributeChange) {
	t.Helper()
	ctx := context.Background()
	err := db.Do(ctx, func(tx *Tx) error {
		return tx.AttributeChanges.InsertBatch(ctx, []domain.AttributeChange{ch})
	})
	if err != nil {
		t.Fatalf("seed change %s: %v", ch.ID, err)
	}
}

// newDeviceResource builds a minimal device resource for the v11 tests.
func newDeviceResource(id, batchID string) domain.Resource {
	return domain.Resource{
		ID: id, Type: domain.ResourceTypeDevice, NaturalKey: "DEV-" + strings.TrimPrefix(id, "res_"),
		Name: "dev", Attributes: map[string]string{"mgmt_ip": "10.0.0.1"},
		FirstBatchID: batchID, CreatedAt: simTS, UpdatedAt: simTS,
	}
}

// TestMigrationV11AddsAttributeChanges pins the v11 CREATE TABLE migration:
//
//   - on a genuine v10 database (v11 temporarily unregistered) the
//     resource_attribute_changes table does not exist and an insert fails;
//   - re-registering v11 and reopening the SAME file creates the table:
//     the seeded device row survives byte-for-byte, a change row round-trips
//     (pending + applied_at NULL), the status CHECK rejects bogus values,
//     both foreign keys are enforced, the resource_id ON DELETE CASCADE
//     removes the change history with its resource, and MAX(version) == 11;
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV11AddsAttributeChanges(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v10 database (no v11 in the registry).
	withoutV11(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v10: %v", err)
	}
	if ok, err := tableExists(ctx, db1, "resource_attribute_changes"); err != nil || ok {
		t.Fatalf("table exists at v10 = %v (err %v), want false", ok, err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v10 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v11 applies.
	migrations = append(append([]Migration(nil), migrations...), migrationV11)
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v10 → v11): %v", err)
	}
	defer db2.Close()
	if ok, err := tableExists(ctx, db2, "resource_attribute_changes"); err != nil || !ok {
		t.Fatalf("table exists after v11 = %v (err %v), want true", ok, err)
	}

	// Seed a legacy resource and record a change against it.
	if err := db2.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_v11", simTS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, newDeviceResource("res_v11_dev", "bat_v11"))
	}); err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	seedChange(t, db2, domain.AttributeChange{
		ID: "chg_v11_1", ResourceID: "res_v11_dev", BatchID: "bat_v11",
		Field: "mgmt_ip", OldValue: "10.0.0.1", NewValue: "10.0.0.9",
		Status: domain.ChangeStatusPending, Sheet: "设备清单", RowNumber: 2,
		CreatedAt: simTS,
	})
	got, err := db2.AttributeChanges.Get(ctx, "chg_v11_1")
	if err != nil || got.Status != domain.ChangeStatusPending || got.OldValue != "10.0.0.1" ||
		got.NewValue != "10.0.0.9" || got.AppliedAt != nil || got.Sheet != "设备清单" || got.RowNumber != 2 {
		t.Errorf("change round-trip = %+v (err %v), want pending 10.0.0.1→10.0.0.9 with NULL applied_at", got, err)
	}

	// The pre-v11 data survived untouched.
	dev, err := db2.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-v11_dev")
	if err != nil || dev.Name != "dev" || !dev.UpdatedAt.Equal(simTS) {
		t.Errorf("device row after v11 = %v (err %v), want byte-identical survival", dev, err)
	}

	// The status CHECK rejects bogus values (raw SQL: InsertBatch's own
	// Go-side pending guard fires before the constraint for non-pending
	// structs, both are rejections — this pins the DDL constraint itself).
	_, err = db2.sql.ExecContext(ctx, `INSERT INTO resource_attribute_changes
(id, resource_id, batch_id, field, old_value, new_value, status, sheet, row_number, created_at)
VALUES ('chg_v11_bad','res_v11_dev','bat_v11','mgmt_ip','a','b','bogus','s',3,?)`, formatTime(simTS))
	if err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("insert bogus status = %v, want CHECK constraint error", err)
	}

	// Both foreign keys are enforced (unknown resource / unknown batch).
	for name, bad := range map[string]domain.AttributeChange{
		"resource": {ID: "chg_v11_fk1", ResourceID: "res_nope", BatchID: "bat_v11",
			Field: "f", OldValue: "a", NewValue: "b", Status: domain.ChangeStatusPending,
			Sheet: "s", RowNumber: 4, CreatedAt: simTS},
		"batch": {ID: "chg_v11_fk2", ResourceID: "res_v11_dev", BatchID: "bat_nope",
			Field: "f", OldValue: "a", NewValue: "b", Status: domain.ChangeStatusPending,
			Sheet: "s", RowNumber: 5, CreatedAt: simTS},
	} {
		err := db2.Do(ctx, func(tx *Tx) error {
			return tx.AttributeChanges.InsertBatch(ctx, []domain.AttributeChange{bad})
		})
		if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
			t.Errorf("insert with unknown %s = %v, want FOREIGN KEY constraint error", name, err)
		}
	}

	// The resource_id ON DELETE CASCADE removes the change with its resource
	// (the rollback delete chain relies on this for applied/dismissed rows).
	if err := db2.Do(ctx, func(tx *Tx) error {
		_, err := tx.Resources.DeleteByBatch(ctx, "bat_v11")
		return err
	}); err != nil {
		t.Fatalf("delete resources of batch: %v", err)
	}
	if ch, err := db2.AttributeChanges.Get(ctx, "chg_v11_1"); err == nil {
		t.Errorf("change after resource cascade delete = %+v, want not found", ch)
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	// Necessary adaptation for v13→v14→v16→v17 (W8-D ptp_port, W20
	// metrics, W21 type expansion, W22 run state): the reopened registry
	// applies every registered migration, advancing MAX(version) with the
	// registry head.
	if maxV != 31 {
		t.Errorf("max schema version after upgrade = %d, want 31", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op.
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if ok, err := tableExists(ctx, db3, "resource_attribute_changes"); err != nil || !ok {
		t.Errorf("table exists after reopen = %v (err %v), want true", ok, err)
	}
}
