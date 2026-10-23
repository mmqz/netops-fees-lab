package repo

import (
	"context"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// withoutV12 temporarily removes migration v12 AND every later migration
// from the registry so the next Open builds a GENUINE v11 database
// (migrations v1–v11 applied, no rename_proposals table). Restored via
// t.Cleanup — the withoutV9 precedent (m.Version <= 8), which survives any
// later migration being registered.
func withoutV12(t *testing.T) {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	for _, m := range migrations {
		if m.Version <= 11 {
			filtered = append(filtered, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
}

// newBaseStationResource builds a minimal base_station resource for the v12
// tests (a name-keyed type — the natural carrier of a rename proposal).
func newBaseStationResource(id, batchID string) domain.Resource {
	return domain.Resource{
		ID: id, Type: domain.ResourceTypeBaseStation, NaturalKey: "动力机房2",
		Name: "动力机房2", Attributes: map[string]string{"base_station_ip": "172.7.10.125/30"},
		FirstBatchID: batchID, CreatedAt: simTS, UpdatedAt: simTS,
	}
}

// TestMigrationV12AddsRenameProposals pins the v12 CREATE TABLE migration:
//
//   - on a genuine v11 database (v12+ temporarily unregistered) the
//     rename_proposals table does not exist and an insert fails;
//   - re-registering v12 and reopening the SAME file creates the table: the
//     seeded base_station row survives byte-for-byte, a pending proposal
//     round-trips (matched_fields JSON array included), the status CHECK
//     rejects bogus values, both foreign keys are enforced, the resource_id
//     ON DELETE CASCADE removes the proposal with its resource, and
//     MAX(version) == 12 (withoutV12 removed v12 AND every later migration,
//     so the upgrade under test stops at v12 — v13 stays unapplied here);
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV12AddsRenameProposals(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v11 database (no v12 in the registry).
	withoutV12(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v11: %v", err)
	}
	if ok, err := tableExists(ctx, db1, "rename_proposals"); err != nil || ok {
		t.Fatalf("table exists at v11 = %v (err %v), want false", ok, err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v11 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v12 applies.
	migrations = append(append([]Migration(nil), migrations...), migrationV12)
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v11 → v12): %v", err)
	}
	defer db2.Close()
	if ok, err := tableExists(ctx, db2, "rename_proposals"); err != nil || !ok {
		t.Fatalf("table exists after v12 = %v (err %v), want true", ok, err)
	}

	// Seed the old resource plus its twin (the new key's resource) and one
	// pending rename proposal linking them.
	if err := db2.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_v12", simTS)); err != nil {
			return err
		}
		if err := tx.Resources.Insert(ctx, newBaseStationResource("res_v12_old", "bat_v12")); err != nil {
			return err
		}
		twin := newBaseStationResource("res_v12_new", "bat_v12")
		twin.NaturalKey = "动力机房2新"
		twin.Name = "动力机房2新"
		return tx.Resources.Insert(ctx, twin)
	}); err != nil {
		t.Fatalf("seed resources: %v", err)
	}
	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.RenameProposals.Insert(ctx, domain.RenameProposal{
			ID: "rp_v12_1", ResourceID: "res_v12_old", NewResourceID: "res_v12_new", BatchID: "bat_v12",
			ResourceType: domain.ResourceTypeBaseStation, OldKey: "动力机房2", NewKey: "动力机房2新",
			MatchedFields: []string{"base_station_ip"},
			Status:        domain.RenameStatusPending, Sheet: "无线基站表", RowNumber: 2, CreatedAt: simTS,
		})
	}); err != nil {
		t.Fatalf("seed proposal: %v", err)
	}
	got, err := db2.RenameProposals.Get(ctx, "rp_v12_1")
	if err != nil || got.Status != domain.RenameStatusPending || got.OldKey != "动力机房2" ||
		got.NewKey != "动力机房2新" || got.NewResourceID != "res_v12_new" ||
		len(got.MatchedFields) != 1 || got.MatchedFields[0] != "base_station_ip" ||
		got.DecidedAt != nil || got.Sheet != "无线基站表" || got.RowNumber != 2 ||
		got.ResourceType != domain.ResourceTypeBaseStation {
		t.Errorf("proposal round-trip = %+v (err %v), want pending 动力机房2→动力机房2新 with one matched field", got, err)
	}

	// The pre-v12 data survived untouched. W22 adaptation: the database is
	// held at v12 (< v17) so the repo read path (whose SELECT now carries
	// the run-state columns) cannot prepare — raw SQL pins the stored row.
	oKey, oName, _, _, oUpdated, err := preV17Resource(ctx, db2, "res_v12_old")
	if err != nil || oKey != "动力机房2" || oName != "动力机房2" || oUpdated != formatTime(simTS) {
		t.Errorf("base_station row after v12 = %q/%q@%q (err %v), want byte-identical survival", oKey, oName, oUpdated, err)
	}

	// The status CHECK rejects bogus values (raw SQL: Insert's own Go-side
	// pending guard fires before the constraint for non-pending structs,
	// both are rejections — this pins the DDL constraint itself).
	_, err = db2.sql.ExecContext(ctx, `INSERT INTO rename_proposals
(id, resource_id, new_resource_id, batch_id, resource_type, old_key, new_key, matched_fields, status, sheet, row_number, created_at)
VALUES ('rp_v12_bad','res_v12_old','res_v12_new','bat_v12','base_station','a','b','[]','bogus','s',3,?)`, formatTime(simTS))
	if err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("insert bogus status = %v, want CHECK constraint error", err)
	}

	// Both foreign keys are enforced (unknown resource / unknown batch).
	for name, bad := range map[string]domain.RenameProposal{
		"resource": {ID: "rp_v12_fk1", ResourceID: "res_nope", NewResourceID: "res_v12_new", BatchID: "bat_v12",
			ResourceType: domain.ResourceTypeBaseStation, OldKey: "a", NewKey: "b",
			Status: domain.RenameStatusPending, Sheet: "s", RowNumber: 4, CreatedAt: simTS},
		"batch": {ID: "rp_v12_fk2", ResourceID: "res_v12_old", NewResourceID: "res_v12_new", BatchID: "bat_nope",
			ResourceType: domain.ResourceTypeBaseStation, OldKey: "a", NewKey: "b",
			Status: domain.RenameStatusPending, Sheet: "s", RowNumber: 5, CreatedAt: simTS},
	} {
		err := db2.Do(ctx, func(tx *Tx) error {
			return tx.RenameProposals.Insert(ctx, bad)
		})
		if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
			t.Errorf("insert with unknown %s = %v, want FOREIGN KEY constraint error", name, err)
		}
	}

	// The resource_id ON DELETE CASCADE removes the proposal with its old
	// resource (the rollback delete chain relies on this for terminal rows).
	if err := db2.Do(ctx, func(tx *Tx) error {
		_, err := tx.Resources.DeleteByID(ctx, "res_v12_old")
		return err
	}); err != nil {
		t.Fatalf("delete old resource: %v", err)
	}
	if rp, err := db2.RenameProposals.Get(ctx, "rp_v12_1"); err == nil {
		t.Errorf("proposal after resource cascade delete = %+v, want not found", rp)
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 12 {
		t.Errorf("max schema version after upgrade = %d, want 12", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op.
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if ok, err := tableExists(ctx, db3, "rename_proposals"); err != nil || !ok {
		t.Errorf("table exists after reopen = %v (err %v), want true", ok, err)
	}
}
