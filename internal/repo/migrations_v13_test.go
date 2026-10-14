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

// withoutV13 temporarily removes migration v13 from the registry so the
// next Open builds a GENUINE v12 database (migrations v1–v12 applied,
// resources CHECK without 'ptp_port'). The registry is restored via
// t.Cleanup so later tests in this package are unaffected even on failure.
// The package never runs these migrations concurrently (no t.Parallel in
// the repo package), so mutating the package-level list between Opens is
// safe — the withoutV9/withoutV10 precedent. Phase 2 re-appends ONLY
// migrationV13, so the upgrade under test stops at v13.
func withoutV13(t *testing.T) {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	for _, m := range migrations {
		if m.Version <= 12 {
			filtered = append(filtered, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
}

// TestMigrationV13AddsPTPPort pins the resources CHECK extension
// ('ptp_port' joins the seven prior types):
//
//   - on a genuine v12 database (v13 temporarily unregistered) a ptp_port
//     INSERT fails with a CHECK constraint error — the regression the
//     migration exists to fix;
//   - re-registering v13 and reopening the SAME file applies the rebuild:
//     the ptp_port insert succeeds, a machine_room row seeded at v12
//     survives byte-for-byte (key, attrs, updated_at), the first_batch_id
//     FK is still enforced, and MAX(version) == 13;
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV13AddsPTPPort(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v12 database (no v13 in the registry).
	withoutV13(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v12: %v", err)
	}
	// A machine_room row proves pre-existing data rides the rebuild
	// untouched (seeded via the same direct-insert path as the v9/v10
	// precedents).
	if err := db1.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_v13_room", simTS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_v13_room", Type: domain.ResourceTypeMachineRoom, NaturalKey: "23100120000018049",
			Name: "源", Attributes: map[string]string{"machine_room_name": "源"},
			FirstBatchID: "bat_v13_room", CreatedAt: simTS, UpdatedAt: simTS,
		})
	}); err != nil {
		t.Fatalf("seed machine_room at v12: %v", err)
	}
	// The v12 CHECK rejects ptp_port — the pre-migration regression.
	seedErr := db1.Do(ctx, func(tx *Tx) error {
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_v13_ptp", Type: domain.ResourceTypePTPPort, NaturalKey: "6150|gei-1/1/0/7",
			Name: "gei-1/1/0/7", Attributes: map[string]string{"portnum": "6"},
			FirstBatchID: "bat_v13_room", CreatedAt: simTS, UpdatedAt: simTS,
		})
	})
	if seedErr == nil || !strings.Contains(seedErr.Error(), "CHECK") {
		t.Fatalf("ptp_port insert at v12 = %v, want CHECK constraint error", seedErr)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v12 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v13 applies.
	migrations = append(append([]Migration(nil), migrations...), migrationV13)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v12 → v13): %v", err)
	}
	defer db2.Close()

	if err := db2.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_v13_ptp", simTS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_v13_ptp", Type: domain.ResourceTypePTPPort, NaturalKey: "6150|gei-1/1/0/7",
			Name: "gei-1/1/0/7", Attributes: map[string]string{"portnum": "6", "port_state": "master"},
			FirstBatchID: "bat_v13_ptp", CreatedAt: simTS, UpdatedAt: simTS,
		})
	}); err != nil {
		t.Fatalf("seed ptp_port after v13: %v", err)
	}
	// W22 adaptation: the database is held at v13 (< v17) so the repo
	// read path (whose SELECT now carries the run-state columns) cannot
	// prepare — raw SQL pins the stored rows directly.
	tKey, tName, tAttrs, _, _, err := preV17Resource(ctx, db2, "res_v13_ptp")
	if err != nil || tName != "gei-1/1/0/7" ||
		!strings.Contains(tAttrs, "\"portnum\":\"6\"") || !strings.Contains(tAttrs, "\"port_state\":\"master\"") {
		t.Errorf("ptp_port after v13 = %q/%q/%q (err %v), want round-trip with attrs", tKey, tName, tAttrs, err)
	}
	// The machine_room row survived the rebuild untouched.
	rKey, rName, rAttrs, _, rUpdated, err := preV17Resource(ctx, db2, "res_v13_room")
	if err != nil || rName != "源" || !strings.Contains(rAttrs, "machine_room_name") || rUpdated != formatTime(simTS) {
		t.Errorf("machine_room row after v13 rebuild = %q/%q/%q@%q (err %v), want byte-identical survival", rKey, rName, rAttrs, rUpdated, err)
	}
	// The rebuild preserved the FK graph: an unknown first_batch is rejected.
	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_v13_fk", Type: domain.ResourceTypePTPPort, NaturalKey: "6150|gei-1/1/0/8",
			Name: "gei-1/1/0/8", FirstBatchID: "bat_does_not_exist", CreatedAt: simTS, UpdatedAt: simTS,
		})
	}); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("insert with unknown first_batch_id = %v, want FOREIGN KEY constraint error", err)
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
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
	// W22 adaptation: raw read (repo read path needs >= v17).
	if _, _, _, _, _, err := preV17Resource(ctx, db3, "res_v13_ptp"); err != nil {
		t.Errorf("ptp_port row after reopen: %v", err)
	}
}
