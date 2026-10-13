package repo

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// withoutV9 temporarily removes migration v9 AND every later migration
// (v10, …) from the registry so the next Open builds a GENUINE v8 database
// (migrations v1–v8 applied, resources CHECK without 'base_station' — and
// without the v10 types). The registry is restored via t.Cleanup so later
// tests in this package are unaffected even on failure. The package never
// runs these migrations concurrently (no t.Parallel in the repo package), so
// mutating the package-level list between Opens is safe. Phase 2 of the test
// then re-appends ONLY migrationV9, so the upgrade under test stops at v9.
func withoutV9(t *testing.T) {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	for _, m := range migrations {
		if m.Version <= 8 {
			filtered = append(filtered, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
}

// seedBaseStation inserts one base_station resource (bypassing the importer,
// exactly like the port seeding of migrations_v8_test.go). first_batch
// references a freshly created batch.
func seedBaseStation(t *testing.T, db *DB, batchID, id, key string, attrs map[string]string) {
	t.Helper()
	ctx := context.Background()
	err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch(batchID, simTS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: id, Type: domain.ResourceTypeBaseStation, NaturalKey: key,
			Name: "bs-" + id, Attributes: attrs,
			FirstBatchID: batchID, CreatedAt: simTS, UpdatedAt: simTS,
		})
	})
	if err != nil {
		t.Fatalf("seed base_station resource %s: %v", id, err)
	}
}

// TestMigrationV9AddsBaseStation pins the resources CHECK extension
// ('base_station' joins device/ip_pool/circuit/port):
//
//   - on a genuine v8 database (v9 temporarily unregistered) a
//     base_station INSERT fails with a CHECK constraint error — the
//     regression the migration exists to fix;
//   - re-registering v9 and reopening the SAME file applies the v9 rebuild:
//     the base_station insert succeeds, the port row seeded at v8 survives
//     byte-for-byte (key, attrs, updated_at), the first_batch_id FK is still
//     enforced, and MAX(version) == 9;
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV9AddsBaseStation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v8 database (no v9 in the registry).
	withoutV9(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v8: %v", err)
	}
	// A port row proves pre-existing data rides the rebuild untouched.
	if err := db1.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_v9_port", simTS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_v9_port", Type: domain.ResourceTypePort, NaturalKey: "B2|bvi2.1008",
			Name: "bvi2.1008", Attributes: map[string]string{"device_ip": "12.1.53.24"},
			FirstBatchID: "bat_v9_port", CreatedAt: simTS, UpdatedAt: simTS,
		})
	}); err != nil {
		t.Fatalf("seed port at v8: %v", err)
	}
	// The v8 CHECK rejects base_station — the pre-migration regression.
	seedErr := func() error {
		return db1.Do(ctx, func(tx *Tx) error {
			return tx.Resources.Insert(ctx, domain.Resource{
				ID: "res_v9_bs", Type: domain.ResourceTypeBaseStation, NaturalKey: "动力机房2",
				Name: "动力机房2", Attributes: map[string]string{"base_station_ip": "172.7.10.125/30"},
				FirstBatchID: "bat_v9_port", CreatedAt: simTS, UpdatedAt: simTS,
			})
		})
	}()
	if seedErr == nil || !strings.Contains(seedErr.Error(), "CHECK") {
		t.Fatalf("base_station insert at v8 = %v, want CHECK constraint error", seedErr)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v8 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v9 applies.
	migrations = append(append([]Migration(nil), migrations...), migrationV9)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v8 → v9): %v", err)
	}
	defer db2.Close()

	seedBaseStation(t, db2, "bat_v9_bs", "res_v9_bs", "动力机房2",
		map[string]string{"base_station_ip": "172.7.10.125/30"})
	// W22 adaptation: the database is held at v9 (< v17) so the repo read
	// path (whose SELECT now carries the run-state columns) cannot
	// prepare — raw SQL pins the stored row directly.
	bsKey, bsName, bsAttrs, _, _, err := preV17Resource(ctx, db2, "res_v9_bs")
	if err != nil || bsKey != "动力机房2" || bsName != "bs-res_v9_bs" ||
		!strings.Contains(bsAttrs, "172.7.10.125/30") {
		t.Errorf("base_station after v9 = %q/%q/%q (err %v), want round-trip with attrs", bsKey, bsName, bsAttrs, err)
	}
	// The port row survived the rebuild untouched.
	pKey, pName, pAttrs, _, pUpdated, err := preV17Resource(ctx, db2, "res_v9_port")
	if err != nil || pKey != "B2|bvi2.1008" || pName != "bvi2.1008" ||
		!strings.Contains(pAttrs, "12.1.53.24") || pUpdated != formatTime(simTS) {
		t.Errorf("port row after v9 rebuild = %q/%q/%q@%q (err %v), want byte-identical survival", pKey, pName, pAttrs, pUpdated, err)
	}
	// The rebuild preserved the FK graph: an unknown first_batch is rejected.
	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_v9_fk", Type: domain.ResourceTypeBaseStation, NaturalKey: "无批次基站",
			Name: "无批次基站", FirstBatchID: "bat_does_not_exist", CreatedAt: simTS, UpdatedAt: simTS,
		})
	}); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("insert with unknown first_batch_id = %v, want FOREIGN KEY constraint error", err)
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 9 {
		t.Errorf("max schema version after upgrade = %d, want 9", maxV)
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
	if maxV != 9 {
		t.Errorf("max schema version after reopen = %d, want 9", maxV)
	}
	// W22 adaptation: raw read (repo read path needs >= v17).
	if _, _, _, _, _, err := preV17Resource(ctx, db3, "res_v9_bs"); err != nil {
		t.Errorf("base_station row after reopen: %v", err)
	}
}
