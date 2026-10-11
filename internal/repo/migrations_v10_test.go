package repo

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// withoutV10 temporarily removes migration v10 AND every later migration
// (v11, v12, v13, …) from the registry so the next Open builds a GENUINE
// v9 database (migrations v1–v9 applied, resources CHECK without
// site/machine_room, relations CHECK without belongs_to). Necessary
// adaptation for v13 (W8-D): v13's cumulative resources CHECK also carries
// site/machine_room, so leaving later migrations registered would mask the
// v10 regression the test pins — the same adaptation withoutV9 received
// when v10 landed. The registry is restored via t.Cleanup; the removed
// tail (v10+) is returned for Phase 2's full-registry reopen. The package
// never runs these migrations concurrently (no t.Parallel in the repo
// package), so mutating the package-level list between Opens is safe.
func withoutV10(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 9 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

// seedRelation inserts one relation (bypassing the importer) between two
// seeded resources of the given batch — the pre-v10 data the v10 relations
// rebuild must preserve byte-for-byte.
func seedRelation(t *testing.T, db *DB, batchID string, from, to domain.Resource, rt domain.RelationType) {
	t.Helper()
	ctx := context.Background()
	err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch(batchID, simTS)); err != nil {
			return err
		}
		for _, r := range []domain.Resource{from, to} {
			if err := tx.Resources.Insert(ctx, r); err != nil {
				return err
			}
		}
		return tx.Relations.Insert(ctx, domain.Relation{
			ID: "rel_v10_" + rt.String(), Type: rt,
			FromResourceID: from.ID, ToResourceID: to.ID,
			FirstBatchID: batchID, CreatedAt: simTS,
		})
	})
	if err != nil {
		t.Fatalf("seed relation %s: %v", rt, err)
	}
}

// TestMigrationV10AddsSiteMachineRoom pins the double rebuild of v10
// (resources CHECK + 'site'/'machine_room', relations CHECK + 'belongs_to'):
//
//   - on a genuine v9 database (v10 temporarily unregistered) both a
//     machine_room INSERT and a belongs_to relation INSERT fail with CHECK
//     constraint errors — the regressions the migration exists to fix;
//   - re-registering v10 and reopening the SAME file applies the rebuild:
//     both inserts succeed, the device/circuit/a_end rows seeded at v9
//     survive byte-for-byte, first_batch_id and the relations→resources FK
//     are still enforced, and MAX(version) == 10;
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV10AddsSiteMachineRoom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v9 database (no v10 — and nothing later — in the
	// registry); tail keeps the removed v10+ migrations for Phase 2.
	tail := withoutV10(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v9: %v", err)
	}
	// A device + circuit pair joined by an a_end relation proves pre-existing
	// data (both rebuilt tables) rides the rebuild untouched.
	seedRelation(t, db1, "bat_v10_legacy",
		domain.Resource{ID: "res_v10_dev", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-001",
			Name: "dev-1", FirstBatchID: "bat_v10_legacy", CreatedAt: simTS, UpdatedAt: simTS},
		domain.Resource{ID: "res_v10_ckt", Type: domain.ResourceTypeCircuit, NaturalKey: "CKT-001",
			Name: "ckt-1", FirstBatchID: "bat_v10_legacy", CreatedAt: simTS, UpdatedAt: simTS},
		domain.RelationTypeAEnd)

	// The v9 resources CHECK rejects the new resource types.
	insertSite := func() error {
		return db1.Do(ctx, func(tx *Tx) error {
			if err := tx.Batches.Create(ctx, newBatch("bat_v10_site", simTS)); err != nil {
				return err
			}
			return tx.Resources.Insert(ctx, domain.Resource{
				ID: "res_v10_site", Type: domain.ResourceTypeSite, NaturalKey: "231001824048",
				Name: "源", FirstBatchID: "bat_v10_site", CreatedAt: simTS, UpdatedAt: simTS,
			})
		})
	}
	if err := insertSite(); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("site insert at v9 = %v, want CHECK constraint error", err)
	}
	// The v9 relations CHECK rejects belongs_to.
	insertBelongsTo := func() error {
		return db1.Do(ctx, func(tx *Tx) error {
			if err := tx.Batches.Create(ctx, newBatch("bat_v10_rel", simTS)); err != nil {
				return err
			}
			return tx.Relations.Insert(ctx, domain.Relation{
				ID: "rel_v10_bt", Type: domain.RelationTypeBelongsTo,
				FromResourceID: "res_v10_dev", ToResourceID: "res_v10_ckt",
				FirstBatchID: "bat_v10_rel", CreatedAt: simTS,
			})
		})
	}
	if err := insertBelongsTo(); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("belongs_to insert at v9 = %v, want CHECK constraint error", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v9 db: %v", err)
	}

	// Phase 2: restore the full registry (the tail Phase 1 removed) and
	// reopen — v10 (and v11+) apply, so the upgrade path matches production.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v9 → v10): %v", err)
	}
	defer db2.Close()

	// Both new resource types insert cleanly and round-trip (the leading-zero
	// form is the identity semantics the KindString key protects).
	seed := func(res domain.Resource) {
		t.Helper()
		if err := db2.Do(ctx, func(tx *Tx) error {
			return tx.Resources.Insert(ctx, res)
		}); err != nil {
			t.Fatalf("insert %s after v10: %v", res.Type, err)
		}
	}
	seed(domain.Resource{ID: "res_v10_site", Type: domain.ResourceTypeSite, NaturalKey: "231001824048",
		Name: "源", Attributes: map[string]string{"city": "大兴"}, FirstBatchID: "bat_v10_legacy", CreatedAt: simTS, UpdatedAt: simTS})
	seed(domain.Resource{ID: "res_v10_mr", Type: domain.ResourceTypeMachineRoom, NaturalKey: "23100120000018049",
		Name: "呼", Attributes: map[string]string{"site_id": "231001824048"}, FirstBatchID: "bat_v10_legacy", CreatedAt: simTS, UpdatedAt: simTS})
	gotSite, err := db2.Resources.GetByTypeKey(ctx, domain.ResourceTypeSite, "231001824048")
	if err != nil || gotSite.Name != "源" || gotSite.Attributes["city"] != "大兴" {
		t.Errorf("site after v10 = %v (err %v), want round-trip with attrs", gotSite, err)
	}
	gotMR, err := db2.Resources.GetByTypeKey(ctx, domain.ResourceTypeMachineRoom, "23100120000018049")
	if err != nil || gotMR.Name != "呼" || gotMR.Attributes["site_id"] != "231001824048" {
		t.Errorf("machine_room after v10 = %v (err %v), want round-trip with attrs", gotMR, err)
	}

	// The belongs_to relation inserts cleanly between the new types.
	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.Relations.Insert(ctx, domain.Relation{
			ID: "rel_v10_bt", Type: domain.RelationTypeBelongsTo,
			FromResourceID: "res_v10_mr", ToResourceID: "res_v10_site",
			FirstBatchID: "bat_v10_legacy", CreatedAt: simTS,
		})
	}); err != nil {
		t.Fatalf("insert belongs_to after v10: %v", err)
	}
	btRels, err := db2.Relations.ListByResource(ctx, "res_v10_mr")
	if err != nil || len(btRels) != 1 || btRels[0].Type != domain.RelationTypeBelongsTo ||
		btRels[0].ToResourceID != "res_v10_site" {
		t.Errorf("belongs_to after v10 = %+v (err %v), want one edge to the site", btRels, err)
	}

	// The pre-v10 data survived BOTH rebuilds byte-for-byte.
	dev, err := db2.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-001")
	if err != nil || dev.Name != "dev-1" || !dev.UpdatedAt.Equal(simTS) {
		t.Errorf("device row after v10 rebuild = %v (err %v), want byte-identical survival", dev, err)
	}
	cktRels, err := db2.Relations.ListByResource(ctx, "res_v10_ckt")
	if err != nil || len(cktRels) != 1 || cktRels[0].Type != domain.RelationTypeAEnd ||
		cktRels[0].ID != "rel_v10_a_end" || !cktRels[0].CreatedAt.Equal(simTS) {
		t.Errorf("a_end relation after v10 rebuild = %+v (err %v), want byte-identical survival", cktRels, err)
	}

	// The rebuilds preserved the FK graphs: an unknown first_batch is
	// rejected (resources), and the relations→resources FK still enforces
	// real endpoints.
	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_v10_fk", Type: domain.ResourceTypeSite, NaturalKey: "无批次局站",
			Name: "无批次局站", FirstBatchID: "bat_does_not_exist", CreatedAt: simTS, UpdatedAt: simTS,
		})
	}); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("insert with unknown first_batch_id = %v, want FOREIGN KEY constraint error", err)
	}
	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.Relations.Insert(ctx, domain.Relation{
			ID: "rel_v10_fk", Type: domain.RelationTypeBelongsTo,
			FromResourceID: "res_v10_mr", ToResourceID: "res_does_not_exist",
			FirstBatchID: "bat_v10_legacy", CreatedAt: simTS,
		})
	}); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("relation with unknown endpoint = %v, want FOREIGN KEY constraint error", err)
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	// v11 (and now v13) also applies on this Open (the registry holds it),
	// so the max version advances with the schema; the v10 assertions of
	// THIS test were 10 before v11 existed and 12 before v13 (W8-D).
	if maxV != 13 {
		t.Errorf("max schema version after upgrade = %d, want 13", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op.
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if err := db3.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 13 {
		t.Errorf("max schema version after reopen = %d, want 13", maxV)
	}
	if _, err := db3.Resources.GetByTypeKey(ctx, domain.ResourceTypeMachineRoom, "23100120000018049"); err != nil {
		t.Errorf("GetByTypeKey(machine_room) after reopen: %v", err)
	}
}
