package repo

import (
	"context"
	"errors"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// rewindV8 emulates a database left at schema v7: the v8 bookkeeping row is
// dropped while the data stays, so the next Open re-applies migration v8.
func rewindV8(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.sql.ExecContext(context.Background(),
		`DELETE FROM schema_migrations WHERE version = 8`); err != nil {
		t.Fatalf("rewind v8: %v", err)
	}
}

// seedPortResource inserts one port resource with the given legacy natural
// key and attributes (bypassing the importer, exactly like a pre-v8 database
// would hold it). first_batch references a freshly created batch.
func seedPortResource(t *testing.T, db *DB, batchID, id, key string, attrs map[string]string) {
	t.Helper()
	ctx := context.Background()
	err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch(batchID, simTS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: id, Type: domain.ResourceTypePort, NaturalKey: key,
			Name: "port-" + id, Attributes: attrs,
			FirstBatchID: batchID, CreatedAt: simTS, UpdatedAt: simTS,
		})
	})
	if err != nil {
		t.Fatalf("seed port resource %s: %v", id, err)
	}
}

// portKey reads the natural key of one port row by id (ErrNotFound-free:
// the test wants the raw outcome, including "row gone" surprises).
func portKey(t *testing.T, db *DB, id string) string {
	t.Helper()
	var key string
	if err := db.sql.QueryRowContext(context.Background(),
		`SELECT natural_key FROM resources WHERE id = ?`, id).Scan(&key); err != nil {
		t.Fatalf("read natural_key of %s: %v", id, err)
	}
	return key
}

// TestMigrationV8RecomputesPortNaturalKeys pins the port natural-key rebasing
// (device_ip|port_name → device_name|port_name) on a database left at v7:
//
//   - fallback identity: a legacy row WITHOUT attrs.device_name (the only
//     realistic shape — the pre-v8 spec had no device_name field, and key
//     fields never enter attrs) keeps its old key byte-for-byte: honest data
//     preservation, no invented device names;
//   - mover: a row carrying attrs.device_name moves to the new key format
//     and is then findable under the new key only;
//   - conflict (two rows claiming one new key): BOTH keep their original
//     unique keys — the conservative keep-original-key strategy;
//   - conflict (new key occupied by a fallback row): the mover keeps its old
//     key instead of colliding with the row that permanently holds the
//     target;
//   - non-port rows and rows whose key has no '|' are untouched;
//   - re-running the migration (rewound bookkeeping) is a data no-op.
func TestMigrationV8RecomputesPortNaturalKeys(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	// A device row proves non-port types are untouched by v8 (newResource
	// anchors first_batch_id "bat_1", so that batch is created alongside).
	if err := db1.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_1", simTS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, newResource("res_dev8", "DEV-001", simTS))
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	// Legacy v7-shaped port rows (old key format device_ip|port_name).
	seedPortResource(t, db1, "bat_p1", "res_p1", "12.1.53.24|bvi2.1008", nil) // fallback identity
	seedPortResource(t, db1, "bat_p2", "res_p2", "4.8.2.5|xgei-1/1/0/2.186",
		map[string]string{"device_name": "道西30米1"}) // mover
	seedPortResource(t, db1, "bat_p3", "res_p3", "5.6.7.8|gei-1/1/0/7",
		map[string]string{"device_name": "动力机房2"}) // conflict pair...
	seedPortResource(t, db1, "bat_p4", "res_p4", "5.6.7.9|gei-1/1/0/7",
		map[string]string{"device_name": "动力机房2"}) // ...same target key
	seedPortResource(t, db1, "bat_p5", "res_p5", "9.9.9.9|gei-1/1/0/8",
		map[string]string{"device_name": "B2"}) // mover whose target is occupied
	seedPortResource(t, db1, "bat_p6", "res_p6", "B2|gei-1/1/0/8", nil)       // fallback row holding that key
	seedPortResource(t, db1, "bat_p7", "res_p7", "odd-key-no-separator", nil) // not old-format
	rewindV8(t, db1)
	if err := db1.Close(); err != nil {
		t.Fatalf("close after rewind: %v", err)
	}

	db2, err := Open(path) // applies v8
	if err != nil {
		t.Fatalf("second Open (v7 → v8): %v", err)
	}
	defer db2.Close()

	for _, tc := range []struct{ id, want string }{
		{"res_p1", "12.1.53.24|bvi2.1008"},    // fallback: no device_name attr → old key kept
		{"res_p2", "道西30米1|xgei-1/1/0/2.186"}, // mover: attrs.device_name wins
		{"res_p3", "5.6.7.8|gei-1/1/0/7"},     // contested target → original key kept
		{"res_p4", "5.6.7.9|gei-1/1/0/7"},     // contested target → original key kept
		{"res_p5", "9.9.9.9|gei-1/1/0/8"},     // occupied target → original key kept
		{"res_p6", "B2|gei-1/1/0/8"},          // fallback row stays put
		{"res_p7", "odd-key-no-separator"},    // no '|' → untouched
	} {
		if got := portKey(t, db2, tc.id); got != tc.want {
			t.Errorf("port %s natural_key after v8 = %q, want %q", tc.id, got, tc.want)
		}
	}
	// The mover is reachable under the NEW key and gone under the old one;
	// the contested rows keep their old lookup paths.
	if _, err := db2.Resources.GetByTypeKey(ctx, domain.ResourceTypePort, "道西30米1|xgei-1/1/0/2.186"); err != nil {
		t.Errorf("GetByTypeKey(new key of res_p2): %v", err)
	}
	if _, err := db2.Resources.GetByTypeKey(ctx, domain.ResourceTypePort, "4.8.2.5|xgei-1/1/0/2.186"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByTypeKey(old key of res_p2) = %v, want ErrNotFound", err)
	}
	if _, err := db2.Resources.GetByTypeKey(ctx, domain.ResourceTypePort, "5.6.7.8|gei-1/1/0/7"); err != nil {
		t.Errorf("GetByTypeKey(kept key of res_p3): %v", err)
	}
	// Non-port rows are untouched.
	if res, err := db2.Resources.GetByID(ctx, "res_dev8"); err != nil || res.NaturalKey != "DEV-001" {
		t.Errorf("device row after v8 = %v/%v, want DEV-001 untouched", res, err)
	}
	// attrs and timestamps survive unchanged (identity repair, no business
	// mutation): the mover still carries its device_name attribute.
	res2, err := db2.Resources.GetByTypeKey(ctx, domain.ResourceTypePort, "道西30米1|xgei-1/1/0/2.186")
	if err != nil || res2.Attributes["device_name"] != "道西30米1" || !res2.UpdatedAt.Equal(simTS) {
		t.Errorf("mover attrs/updated_at must be untouched, got attrs %v at %v", res2.Attributes, res2.UpdatedAt)
	}

	// Idempotency: a rewound bookkeeping row re-running v8 changes no data.
	rewindV8(t, db2)
	if err := db2.Close(); err != nil {
		t.Fatalf("close before rewind re-run: %v", err)
	}
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (re-run v8): %v", err)
	}
	defer db3.Close()
	for _, tc := range []struct{ id, want string }{
		{"res_p1", "12.1.53.24|bvi2.1008"},
		{"res_p2", "道西30米1|xgei-1/1/0/2.186"},
		{"res_p3", "5.6.7.8|gei-1/1/0/7"},
		{"res_p4", "5.6.7.9|gei-1/1/0/7"},
		{"res_p5", "9.9.9.9|gei-1/1/0/8"},
	} {
		if got := portKey(t, db3, tc.id); got != tc.want {
			t.Errorf("port %s natural_key after v8 re-run = %q, want stable %q", tc.id, got, tc.want)
		}
	}
}

// TestMigrationV8NoPortRowsIsNoOp pins the empty-database path: with no port
// rows the migration applies (bookkeeping row recorded), touches nothing and
// leaves a usable schema — the snapshot table is created and dropped clean.
func TestMigrationV8NoPortRowsIsNoOp(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")

	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	rewindV8(t, db1)
	if err := db1.Close(); err != nil {
		t.Fatalf("close after rewind: %v", err)
	}

	db2, err := Open(path) // v8 on an empty resources table
	if err != nil {
		t.Fatalf("second Open (empty v7 → v8): %v", err)
	}
	defer db2.Close()

	var count int
	if err := db2.sql.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM resources`).Scan(&count); err != nil {
		t.Fatalf("count resources: %v", err)
	}
	if count != 0 {
		t.Errorf("resources count = %d, want 0", count)
	}
	// The snapshot table must NOT survive the migration.
	var leftover int
	if err := db2.sql.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='port_key_targets_v8'`).Scan(&leftover); err != nil {
		t.Fatalf("count snapshot table: %v", err)
	}
	if leftover != 0 {
		t.Errorf("port_key_targets_v8 left behind: %d table(s)", leftover)
	}
	// Post-v8 inserts still work (schema unchanged apart from the keys).
	seedPortResource(t, db2, "bat_new", "res_new", "B2|bvi2.1008",
		map[string]string{"device_ip": "12.1.53.24"})
	if got := portKey(t, db2, "res_new"); got != "B2|bvi2.1008" {
		t.Errorf("post-v8 insert key = %q, want B2|bvi2.1008", got)
	}
}
