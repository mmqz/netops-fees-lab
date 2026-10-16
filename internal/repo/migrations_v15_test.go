package repo

import (
	"context"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// withoutV15 temporarily removes migration v15 AND every later migration
// (v16, …) from the registry so the next Open builds a GENUINE v14
// database (resources CHECK = 8 pre-W21 types, relations CHECK = 4
// relations). The registry is restored via t.Cleanup; the removed tail is
// returned for Phase 2's full-registry reopen (the withoutV13 pattern).
func withoutV15(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 14 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

// withoutV16 temporarily removes migration v16 AND every later migration
// from the registry so the next Open builds a GENUINE v15 database
// (resources CHECK = 33 types, relations CHECK = 4 relations — the
// intermediate state between the two W21 rebuilds). The registry is
// restored via t.Cleanup; the removed tail is returned for Phase 2's
// full-registry reopen.
func withoutV16(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 15 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

// w21SampleTypes are representative new resource types of every W21 layer:
// L0 rack, L1 board, L2 optical_cable + odf_terminal, L3 otn_och, L5 ring,
// L6 customer, L4 pon_port. The full 33-type set equality is pinned by the
// four-point consistency test (TestTypeRegistryFourPointConsistency); this
// migration test pins the upgrade mechanics.
var w21SampleTypes = []domain.ResourceType{
	domain.ResourceTypeRack, domain.ResourceTypeBoard, domain.ResourceTypeOpticalCable,
	domain.ResourceTypeODFTerminal, domain.ResourceTypeOTNOch, domain.ResourceTypeRing,
	domain.ResourceTypeCustomer, domain.ResourceTypePONPort,
}

// seedW21BatchResources inserts one legacy device + machine_room pair
// joined by a belongs_to relation — the pre-v15 data both rebuilds must
// preserve byte-for-byte (B4 guard) — plus the batches they hang from.
func seedW21BatchResources(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_w21_legacy", simTS)); err != nil {
			return err
		}
		for _, r := range []domain.Resource{
			{ID: "res_w21_dev", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-001",
				Name: "dev-1", Attributes: map[string]string{"model": "NE40E"},
				FirstBatchID: "bat_w21_legacy", CreatedAt: simTS, UpdatedAt: simTS},
			{ID: "res_w21_mr", Type: domain.ResourceTypeMachineRoom, NaturalKey: "23100120000018049",
				Name: "呼", Attributes: map[string]string{"site_id": "231001824048"},
				FirstBatchID: "bat_w21_legacy", CreatedAt: simTS, UpdatedAt: simTS},
		} {
			if err := tx.Resources.Insert(ctx, r); err != nil {
				return err
			}
		}
		return tx.Relations.Insert(ctx, domain.Relation{
			ID: "rel_w21_bt", Type: domain.RelationTypeBelongsTo,
			FromResourceID: "res_w21_mr", ToResourceID: "res_w21_dev",
			FirstBatchID: "bat_w21_legacy", CreatedAt: simTS,
		})
	})
	if err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}
}

// countResources counts the rows of the resources table (raw SQL — the
// helpers filter by type).
func countResources(t *testing.T, db *DB) int {
	t.Helper()
	var n int
	if err := db.sql.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM resources`).Scan(&n); err != nil {
		t.Fatalf("count resources: %v", err)
	}
	return n
}

// TestMigrationV15AddsW21Types pins the resources CHECK extension (the
// 33-type closed set of data-model v1.0 §3.3):
//
//   - on a genuine v14 database (v15 temporarily unregistered) an INSERT
//     of any new type fails with a CHECK constraint error — the
//     regression the migration exists to fix;
//   - re-registering the tail and reopening the SAME file applies the
//     rebuild: the new-type inserts succeed and round-trip, the LEGACY
//     rows survive byte-identically (B4: counts unchanged, attributes
//     and timestamps untouched), the first_batch_id FK is still enforced,
//     and MAX(version) == 17 (W22 adaptation: the restored tail includes
//     v17, so the head follows the registry);
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV15AddsW21Types(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v14 database; tail keeps v15/v16 for Phase 2.
	tail := withoutV15(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v14: %v", err)
	}
	seedW21BatchResources(t, db1)
	before := countResources(t, db1)

	// The v14 resources CHECK rejects every new type.
	for _, rt := range w21SampleTypes {
		err := db1.Do(ctx, func(tx *Tx) error {
			return tx.Resources.Insert(ctx, domain.Resource{
				ID: "res_w21_" + rt.String(), Type: rt, NaturalKey: "W21-" + rt.String(),
				Name: rt.String(), FirstBatchID: "bat_w21_legacy", CreatedAt: simTS, UpdatedAt: simTS,
			})
		})
		if err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("insert %s at v14 = %v, want CHECK constraint error", rt, err)
		}
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v14 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v15 (and v16) apply.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v14 → v15/v16): %v", err)
	}
	defer db2.Close()

	// Every new type inserts cleanly and round-trips (KeyNormalize types
	// keep zero-prefixed string keys as identity).
	for _, rt := range w21SampleTypes {
		res := domain.Resource{
			ID: "res_w21_" + rt.String(), Type: rt, NaturalKey: "W21-" + rt.String(),
			Name: rt.String(), Attributes: map[string]string{"probe": rt.String()},
			FirstBatchID: "bat_w21_legacy", CreatedAt: simTS, UpdatedAt: simTS,
		}
		if err := db2.Do(ctx, func(tx *Tx) error {
			return tx.Resources.Insert(ctx, res)
		}); err != nil {
			t.Fatalf("insert %s after v15: %v", rt, err)
		}
		got, err := db2.Resources.GetByTypeKey(ctx, rt, "W21-"+rt.String())
		if err != nil || got.Attributes["probe"] != rt.String() {
			t.Errorf("%s after v15 = %v (err %v), want round-trip with attrs", rt, got, err)
		}
	}

	// B4 guard: the legacy rows survived the rebuild byte-identically and
	// the count only grew by the rows this test itself inserted.
	if after := countResources(t, db2); after != before+len(w21SampleTypes) {
		t.Errorf("resources count after v15 = %d, want %d (legacy %d + %d new)", after, before+len(w21SampleTypes), before, len(w21SampleTypes))
	}
	dev, err := db2.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-001")
	if err != nil || dev.Name != "dev-1" || dev.Attributes["model"] != "NE40E" || !dev.UpdatedAt.Equal(simTS) {
		t.Errorf("device row after v15 rebuild = %v (err %v), want byte-identical survival", dev, err)
	}
	mr, err := db2.Resources.GetByTypeKey(ctx, domain.ResourceTypeMachineRoom, "23100120000018049")
	if err != nil || mr.Name != "呼" || mr.Attributes["site_id"] != "231001824048" || !mr.UpdatedAt.Equal(simTS) {
		t.Errorf("machine_room row after v15 rebuild = %v (err %v), want byte-identical survival", mr, err)
	}

	// The rebuild preserved the FK graphs.
	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_w21_fk", Type: domain.ResourceTypeRack, NaturalKey: "RACK-NO-BATCH",
			Name: "rack-no-batch", FirstBatchID: "bat_does_not_exist", CreatedAt: simTS, UpdatedAt: simTS,
		})
	}); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("insert with unknown first_batch_id = %v, want FOREIGN KEY constraint error", err)
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 25 {
		t.Errorf("max schema version after upgrade = %d, want 25 (restored tail includes v17-v19)", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op.
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if _, err := db3.Resources.GetByTypeKey(ctx, domain.ResourceTypeOpticalCable, "W21-optical_cable"); err != nil {
		t.Errorf("GetByTypeKey(optical_cable) after reopen: %v", err)
	}
	if maxV != 25 {
		t.Errorf("max schema version after reopen = %d, want 25", maxV)
	}
}

// TestMigrationV16AddsW21Relations pins the relations CHECK extension (the
// 29-value closed set of data-model v1.0 §4.3, with the carries_fiber →
// fiber_of direction correction):
//
//   - on a genuine v15 database (v16 temporarily unregistered) an INSERT
//     of a new relation type fails with a CHECK constraint error;
//   - re-registering v16 and reopening the SAME file applies the rebuild:
//     the new relation inserts succeed, the legacy relation row survives
//     byte-identically (B4: relation count unchanged, NO new relations
//     derived), the v14 §10.7 indexes are re-created verbatim, the
//     relations→resources FK is still enforced, and MAX(version) == 17
//     (W22 adaptation: the restored tail includes v17);
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV16AddsW21Relations(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v15 database (resources already 33 types, relations
	// still 4). Seed two resources of NEW types joined by a LEGACY
	// relation so the seed itself passes the v15 CHECKs.
	tail := withoutV16(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v15: %v", err)
	}
	seedErr := db1.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_w16", simTS)); err != nil {
			return err
		}
		for _, r := range []domain.Resource{
			{ID: "res_w16_fiber", Type: domain.ResourceTypeFiber, NaturalKey: "GY-001|1",
				Name: "纤芯1", FirstBatchID: "bat_w16", CreatedAt: simTS, UpdatedAt: simTS},
			{ID: "res_w16_cable", Type: domain.ResourceTypeOpticalCable, NaturalKey: "GY-001",
				Name: "光缆1", FirstBatchID: "bat_w16", CreatedAt: simTS, UpdatedAt: simTS},
		} {
			if err := tx.Resources.Insert(ctx, r); err != nil {
				return err
			}
		}
		// A legacy-typed relation between two NEW-type resources: legal at
		// v15 (relations CHECK is 4 values) and must survive v16 untouched.
		return tx.Relations.Insert(ctx, domain.Relation{
			ID: "rel_w16_legacy", Type: domain.RelationTypeBelongsTo,
			FromResourceID: "res_w16_fiber", ToResourceID: "res_w16_cable",
			FirstBatchID: "bat_w16", CreatedAt: simTS,
		})
	})
	if seedErr != nil {
		t.Fatalf("seed v15 rows: %v", seedErr)
	}
	var relBefore int
	if err := db1.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM relations`).Scan(&relBefore); err != nil {
		t.Fatalf("count relations at v15: %v", err)
	}

	// The v15 relations CHECK rejects every new relation type.
	for _, rt := range []domain.RelationType{
		domain.RelationTypeFiberOf, domain.RelationTypeCarriedBy, domain.RelationTypeUsesFiber,
		domain.RelationTypeServes, domain.RelationTypeConnectsONU,
	} {
		err := db1.Do(ctx, func(tx *Tx) error {
			return tx.Relations.Insert(ctx, domain.Relation{
				ID: "rel_w16_" + rt.String(), Type: rt,
				FromResourceID: "res_w16_fiber", ToResourceID: "res_w16_cable",
				FirstBatchID: "bat_w16", CreatedAt: simTS,
			})
		})
		if err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("insert relation %s at v15 = %v, want CHECK constraint error", rt, err)
		}
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v15 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v16 applies.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v15 → v16): %v", err)
	}
	defer db2.Close()

	// The fiber_of relation (the corrected carries_fiber) inserts cleanly
	// between the fiber (member) and the cable (container).
	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.Relations.Insert(ctx, domain.Relation{
			ID: "rel_w16_fiber_of", Type: domain.RelationTypeFiberOf,
			FromResourceID: "res_w16_fiber", ToResourceID: "res_w16_cable",
			FirstBatchID: "bat_w16", CreatedAt: simTS,
		})
	}); err != nil {
		t.Fatalf("insert fiber_of after v16: %v", err)
	}
	rels, err := db2.Relations.ListByResource(ctx, "res_w16_fiber")
	if err != nil {
		t.Fatalf("list relations after v16: %v", err)
	}
	fiberOf, belongsTo := 0, 0
	for _, r := range rels {
		switch r.Type {
		case domain.RelationTypeFiberOf:
			fiberOf++
		case domain.RelationTypeBelongsTo:
			belongsTo++
		}
	}
	if fiberOf != 1 || belongsTo != 1 {
		t.Errorf("relations of the fiber after v16 = fiber_of %d / belongs_to %d, want 1/1", fiberOf, belongsTo)
	}

	// B4 guard: the legacy relation row survived byte-identically and the
	// count only grew by this test's own insert — zero auto-derivation.
	var relAfter int
	if err := db2.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM relations`).Scan(&relAfter); err != nil {
		t.Fatalf("count relations after v16: %v", err)
	}
	if relAfter != relBefore+1 {
		t.Errorf("relations count after v16 = %d, want %d (legacy %d + 1 inserted by the test — no auto-derivation)", relAfter, relBefore+1, relBefore)
	}
	legacy, err := db2.Relations.ListByResource(ctx, "res_w16_cable")
	if err != nil || len(legacy) != 2 {
		t.Fatalf("legacy relation listing after v16 = %v (err %v), want 2", legacy, err)
	}
	for _, r := range legacy {
		if r.ID == "rel_w16_legacy" && !r.CreatedAt.Equal(simTS) {
			t.Errorf("legacy relation after v16 rebuild = %+v, want byte-identical survival", r)
		}
	}

	// The v14 §10.7 indexes are re-created verbatim by the rebuild.
	for _, idx := range []string{"idx_relations_to", "idx_relations_type"} {
		if ok, err := indexExists(ctx, db2, idx); err != nil || !ok {
			t.Errorf("index %s exists after v16 = %v (err %v), want true (re-created by the rebuild)", idx, ok, err)
		}
	}

	// The rebuild preserved the FK graph: unknown endpoints are rejected.
	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.Relations.Insert(ctx, domain.Relation{
			ID: "rel_w16_fk", Type: domain.RelationTypeFiberOf,
			FromResourceID: "res_w16_fiber", ToResourceID: "res_does_not_exist",
			FirstBatchID: "bat_w16", CreatedAt: simTS,
		})
	}); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("relation with unknown endpoint = %v, want FOREIGN KEY constraint error", err)
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 25 {
		t.Errorf("max schema version after upgrade = %d, want 25 (restored tail includes v17-v19)", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op.
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if err := db3.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version after reopen: %v", err)
	}
	if maxV != 25 {
		t.Errorf("max schema version after reopen = %d, want 25", maxV)
	}
	if _, err := db3.Resources.GetByTypeKey(ctx, domain.ResourceTypeFiber, "GY-001|1"); err != nil {
		t.Errorf("GetByTypeKey(fiber) after reopen: %v", err)
	}
}
