package repo

import (
	"context"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// withoutV17 temporarily removes migration v17 AND every later migration
// from the registry so the next Open builds a GENUINE v16 database (the W21
// head: 33 types / 29 relations, no run-state columns). The registry is
// restored via t.Cleanup; the removed tail is returned for Phase 2's
// full-registry reopen (the withoutV13 pattern).
func withoutV17(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 16 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

// v17TS is the fixed timestamp of this file's seeds (no clock in the data
// path — the simrepo_test.go convention; simTS there is 2026-11-01).
var v17TS = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// preV17Resource reads one resources row's PRE-v17 columns by id via raw
// SQL. The surgery tests (v9/v12/v13/v14) deliberately hold their database
// below the registry head, and since v17 the repo read path (whose SELECT
// includes the run-state snapshot columns) cannot prepare against those
// older schemas — raw SQL keeps each surgery's semantics ("the upgrade
// under test stops at N") while still pinning the stored bytes.
func preV17Resource(ctx context.Context, db *DB, id string) (key, name, attrs, status, updatedAt string, err error) {
	err = db.sql.QueryRowContext(ctx,
		`SELECT natural_key, name, attributes, COALESCE(status,''), updated_at FROM resources WHERE id = ?`, id).
		Scan(&key, &name, &attrs, &status, &updatedAt)
	return
}

// TestMigrationV17AddsRunState pins the run-state model (§12-B1 方案 C):
//
//   - on a genuine v16 database (v17 temporarily unregistered) the
//     resource_status insert fails with "no such table" and resources has
//     NO run_state column — the regression the migration exists to fix;
//   - re-registering v17 and reopening the SAME file applies it: the table
//     exists, the resources columns read as NULL for legacy rows (no
//     backfill — NULL = no observation), the legacy row is otherwise
//     byte-identical, both CHECK enums reject out-of-set values, the
//     resource FK is enforced and ON DELETE CASCADE removes the history
//     with its resource, and MAX(version) == 17;
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV17AddsRunState(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v16 database. Seed one device with a ledger status +
	// attributes — the row the snapshot columns must leave byte-identical.
	tail := withoutV17(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v16: %v", err)
	}
	seedErr := db1.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_v17", v17TS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_v17_dev", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-V17",
			Name: "v17设备", Attributes: map[string]string{"vendor": "合成厂商"}, Status: "在网",
			FirstBatchID: "bat_v17", CreatedAt: v17TS, UpdatedAt: v17TS,
		})
	})
	if seedErr != nil {
		t.Fatalf("seed v16 rows: %v", seedErr)
	}
	// Pre-migration regression: no resource_status table.
	if _, err := db1.sql.ExecContext(ctx,
		`INSERT INTO resource_status (resource_id, ts, run_state, source) VALUES ('res_v17_dev','2026-01-02T00:00:00Z','down','manual')`); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Errorf("insert resource_status at v16 = %v, want \"no such table\" error", err)
	}
	// Pre-migration regression: no run_state column on resources.
	if _, err := db1.sql.ExecContext(ctx, `SELECT run_state FROM resources WHERE id = 'res_v17_dev'`); err == nil || !strings.Contains(err.Error(), "no such column") {
		t.Errorf("select run_state at v16 = %v, want \"no such column\" error", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v16 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v17 applies.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v16 → v17): %v", err)
	}
	defer db2.Close()

	// The closed run_state enum is enforced by the CHECK constraint.
	for _, bad := range []string{"bogus", "UP", ""} {
		if _, err := db2.sql.ExecContext(ctx,
			`INSERT INTO resource_status (resource_id, ts, run_state, source) VALUES ('res_v17_dev','2026-01-02T00:00:00Z',?,'manual')`, bad); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("insert run_state %q = %v, want CHECK constraint error", bad, err)
		}
	}
	// The closed source enum too ('alert' passes the CHECK — reserving it for
	// W23 is an HTTP-layer rule, not a storage rule).
	for _, bad := range []string{"bogus", "web", ""} {
		if _, err := db2.sql.ExecContext(ctx,
			`INSERT INTO resource_status (resource_id, ts, run_state, source) VALUES ('res_v17_dev','2026-01-02T00:00:00Z','up',?)`, bad); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("insert source %q = %v, want CHECK constraint error", bad, err)
		}
	}

	// Upsert round-trip through the repo: history row + snapshot in one tx.
	ts1 := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.ResourceStatus.UpsertStatus(ctx, domain.ResourceStatus{
			ResourceID: "res_v17_dev", TS: ts1, RunState: domain.RunStateDown, Source: domain.StatusSourceManual,
		})
	}); err != nil {
		t.Fatalf("UpsertStatus: %v", err)
	}
	got, err := db2.Resources.GetByID(ctx, "res_v17_dev")
	if err != nil {
		t.Fatalf("GetByID after upsert: %v", err)
	}
	if got.RunState != "down" || !got.RunStateAt.Equal(ts1) {
		t.Errorf("snapshot after upsert = %q@%v, want down@%v", got.RunState, got.RunStateAt, ts1)
	}
	// Ledger separation (§12-B1): status/attributes/updated_at untouched.
	if got.Status != "在网" || got.Attributes["vendor"] != "合成厂商" || !got.UpdatedAt.Equal(v17TS) {
		t.Errorf("ledger fields after run-state write = status %q attrs %v updated %v, want byte-identical", got.Status, got.Attributes, got.UpdatedAt)
	}

	// History accumulates; the same (resource, ts) upserts (value semantics).
	ts2 := ts1.Add(time.Hour)
	if err := db2.Do(ctx, func(tx *Tx) error {
		for _, st := range []domain.ResourceStatus{
			{ResourceID: "res_v17_dev", TS: ts1, RunState: domain.RunStateUp, Source: domain.StatusSourceManual},                  // same ts → replace
			{ResourceID: "res_v17_dev", TS: ts2, RunState: domain.RunStateDown, Source: domain.StatusSourceManual},                // append
			{ResourceID: "res_v17_dev", TS: ts2.Add(time.Minute), RunState: domain.RunStateUp, Source: domain.StatusSourceManual}, // append
		} {
			if err := tx.ResourceStatus.UpsertStatus(ctx, st); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("history upserts: %v", err)
	}
	hist, err := db2.ResourceStatus.ListHistory(ctx, "res_v17_dev", "", "")
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(hist) != 3 { // ts1(replaced), ts2, ts2+1m
		t.Errorf("history rows = %d, want 3 (same-ts upsert replaced): %+v", len(hist), hist)
	}
	if len(hist) == 3 {
		if hist[0].RunState != domain.RunStateUp || hist[0].TS != ts1 ||
			hist[1].RunState != domain.RunStateDown || hist[1].TS != ts2 ||
			hist[2].RunState != domain.RunStateUp {
			t.Errorf("history order/states = %+v, want [up@ts1, down@ts2, up@ts2+1m] ascending", hist)
		}
	}

	// The resource FK is enforced; ON DELETE CASCADE removes the history.
	if _, err := db2.sql.ExecContext(ctx,
		`INSERT INTO resource_status (resource_id, ts, run_state, source) VALUES ('res_nope','2026-01-02T00:00:00Z','up','manual')`); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("insert with unknown resource = %v, want FOREIGN KEY constraint error", err)
	}
	if _, err := db2.ResourceStatus.ListHistory(ctx, "res_v17_dev", "", ""); err != nil {
		t.Fatalf("ListHistory before delete: %v", err)
	}
	if err := db2.Do(ctx, func(tx *Tx) error {
		_, err := tx.Resources.DeleteByID(ctx, "res_v17_dev")
		return err
	}); err != nil {
		t.Fatalf("delete resource: %v", err)
	}
	histAfter, err := db2.ResourceStatus.ListHistory(ctx, "res_v17_dev", "", "")
	if err != nil {
		t.Fatalf("ListHistory after delete: %v", err)
	}
	if len(histAfter) != 0 {
		t.Errorf("history after resource delete = %d rows, want 0 (ON DELETE CASCADE)", len(histAfter))
	}

	// MAX(version) follows the registry head.
	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 32 {
		t.Errorf("max schema version after upgrade = %d, want 32", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op (a second ADD COLUMN would
	// fail on the duplicate column, so a clean reopen proves the bookkeeping).
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if err := db3.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version after reopen: %v", err)
	}
	if maxV != 32 {
		t.Errorf("max schema version after reopen = %d, want 32", maxV)
	}
}

// TestMigrationV17LegacyRowsStayNULL pins the no-backfill rule on a seeded
// multi-row v16 database: after v17 every legacy resource reads
// RunState == "" (NULL — no observation) while its ledger fields survive
// byte-identically, and CountByRunState reports every row in the null bucket.
func TestMigrationV17LegacyRowsStayNULL(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	tail := withoutV17(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v16: %v", err)
	}
	if err := db1.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_v17b", v17TS)); err != nil {
			return err
		}
		for _, r := range []domain.Resource{
			{ID: "res_v17b_dev", Type: domain.ResourceTypeDevice, NaturalKey: "DEV-1", Name: "d1",
				Status: "在网", FirstBatchID: "bat_v17b", CreatedAt: v17TS, UpdatedAt: v17TS},
			{ID: "res_v17b_cust", Type: domain.ResourceTypeCustomer, NaturalKey: "CUST-1", Name: "c1",
				FirstBatchID: "bat_v17b", CreatedAt: v17TS, UpdatedAt: v17TS},
		} {
			if err := tx.Resources.Insert(ctx, r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen (v16 → v17): %v", err)
	}
	defer db2.Close()

	for _, id := range []string{"res_v17b_dev", "res_v17b_cust"} {
		r, err := db2.Resources.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID(%s) after v17: %v", id, err)
		}
		if r.RunState != "" || !r.RunStateAt.IsZero() {
			t.Errorf("%s after v17: runState = %q@%v, want NULL (no backfill)", id, r.RunState, r.RunStateAt)
		}
		if !r.UpdatedAt.Equal(v17TS) {
			t.Errorf("%s after v17: updated_at = %v, want byte-identical %v", id, r.UpdatedAt, v17TS)
		}
	}
	counts, nulls, err := db2.ResourceStatus.CountByRunState(ctx)
	if err != nil {
		t.Fatalf("CountByRunState: %v", err)
	}
	if nulls != 2 || len(counts) != 0 {
		t.Errorf("CountByRunState after v17 = %v/nulls %d, want empty map with 2 NULL rows", counts, nulls)
	}
}
