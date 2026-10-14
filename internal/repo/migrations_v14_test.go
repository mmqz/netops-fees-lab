package repo

import (
	"context"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// withoutV14 temporarily removes migration v14 AND every later migration
// (v15, v16, …) from the registry so the next Open builds a GENUINE v13
// database. W21 adaptation: v14 used to be the registry HEAD, so removing
// "v14 and later" and "v14 only" were the same thing; with v15/v16
// registered the tail form is the one that keeps this test's phase-1
// database at v13 (removing only v14 would run the W21 rebuilds first and
// v14's idx_relations_to creation would then collide with v16's — the
// withoutV13 pattern).
func withoutV14(t *testing.T) {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	for _, m := range migrations {
		if m.Version <= 13 {
			filtered = append(filtered, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
}

// indexExists reports whether an index (or table) exists in sqlite_master.
func indexExists(ctx context.Context, db *DB, name string) (bool, error) {
	var n int
	err := db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE name = ? AND type IN ('index','table')`, name).Scan(&n)
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// withoutRowidTable reports whether the named table is declared WITHOUT
// ROWID (sql is the CREATE statement text from sqlite_master).
func withoutRowidTable(ctx context.Context, db *DB, table string) (bool, error) {
	var sql string
	err := db.sql.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&sql)
	if err != nil {
		return false, err
	}
	return strings.Contains(strings.ToUpper(sql), "WITHOUT ROWID"), nil
}

// TestMigrationV14AddsTimeseries pins the v14 migration structurally:
//
//   - on a genuine v13 database (v14 temporarily unregistered) none of the
//     metric tables exists and an insert into metrics fails;
//   - re-registering v14 and reopening the SAME file creates all four
//     tables + every index (§5.3 + §10.7 + the identity index + the
//     metric_batches listing index), seeds the closed catalog (14 rows,
//     PKs unique, fixed created_at), and the pre-v13 data survives
//     byte-for-byte;
//   - the metrics table is WITHOUT ROWID (§5.3 ③) and its CHECK/FK
//     constraints are enforced (bad quality, unknown series FK);
//   - the NULL-scope series identity actually dedupes (INSERT OR IGNORE on
//     the same (resource, key, NULL) leaves ONE row — the reason the
//     identity index exists);
//   - the catalog seed is idempotent (INSERT OR IGNORE re-run is a no-op);
//   - a third Open is a bookkeeping no-op and MAX(version) == 14.
func TestMigrationV14AddsTimeseries(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v13 database (no v14 in the registry).
	withoutV14(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v13: %v", err)
	}
	for _, table := range []string{"metric_keys", "metric_series", "metrics", "metric_batches"} {
		if ok, err := tableExists(ctx, db1, table); err != nil || ok {
			t.Fatalf("table %s exists at v13 = %v (err %v), want false", table, ok, err)
		}
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v13 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v14 applies.
	migrations = append(append([]Migration(nil), migrations...), migrationV14)
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v13 → v14): %v", err)
	}
	defer db2.Close()
	for _, table := range []string{"metric_keys", "metric_series", "metrics", "metric_batches"} {
		if ok, err := tableExists(ctx, db2, table); err != nil || !ok {
			t.Errorf("table %s exists after v14 = %v (err %v), want true", table, ok, err)
		}
	}

	// Every index of the migration (§5.3 + §10.7 + identity + listing).
	for _, idx := range []string{
		"idx_metric_series_resource",
		"idx_metric_series_key",
		"idx_metrics_ts",
		"idx_metrics_ts_key",
		"idx_metric_series_identity",
		"idx_metric_batches_started",
		"idx_relations_to",
		"idx_relations_type",
	} {
		if ok, err := indexExists(ctx, db2, idx); err != nil || !ok {
			t.Errorf("index %s exists after v14 = %v (err %v), want true", idx, ok, err)
		}
	}

	// WITHOUT ROWID shape of the narrow points table.
	if ok, err := withoutRowidTable(ctx, db2, "metrics"); err != nil || !ok {
		t.Errorf("metrics is WITHOUT ROWID = %v (err %v), want true", ok, err)
	}
	if ok, err := withoutRowidTable(ctx, db2, "metric_keys"); err == nil && ok {
		t.Errorf("metric_keys is WITHOUT ROWID (unexpected — catalog reads rowid order)")
	}

	// Catalog seed: closed set, unique PKs, fixed created_at.
	var n int
	if err := db2.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_keys`).Scan(&n); err != nil {
		t.Fatalf("count catalog: %v", err)
	}
	if n != 14 {
		t.Errorf("catalog rows = %d, want 14 (the closed registry size)", n)
	}
	var dups int
	if err := db2.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) - COUNT(DISTINCT metric_key) FROM metric_keys`).Scan(&dups); err != nil {
		t.Fatalf("dup check: %v", err)
	}
	if dups != 0 {
		t.Errorf("duplicate metric_key rows = %d, want 0", dups)
	}
	var createdAt string
	if err := db2.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM metric_keys WHERE created_at != '2026-01-01T00:00:00Z'`).Scan(&n); err != nil {
		t.Fatalf("created_at check: %v", err)
	}
	if n != 0 {
		_ = createdAt
		t.Errorf("catalog rows with non-constant created_at = %d, want 0 (deterministic seed)", n)
	}

	// The value_kind / direction / quality CHECKs are enforced (raw SQL:
	// the DDL constraints themselves, not the Go-side guards).
	if _, err := db2.sql.ExecContext(ctx, `INSERT INTO metric_keys
(metric_key, label, unit, value_kind, created_at) VALUES ('bad_kind', 'x', 'x', 'zigzag', '2026-01-01T00:00:00Z')`); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("insert bogus value_kind = %v, want CHECK constraint error", err)
	}
	if _, err := db2.sql.ExecContext(ctx, `INSERT INTO metric_keys
(metric_key, label, unit, value_kind, direction, created_at) VALUES ('bad_dir', 'x', 'x', 'gauge', 'sideways', '2026-01-01T00:00:00Z')`); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("insert bogus direction = %v, want CHECK constraint error", err)
	}

	// FKs: metrics.series_id → metric_series, metric_series.resource_id →
	// resources, metric_series.metric_key → metric_keys, metrics.batch_id →
	// metric_batches. Seed a batch + a resource + a series and check the
	// point round-trip, then the refusals.
	if err := db2.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_v14", simTS)); err != nil {
			return err
		}
		if err := tx.MetricBatches.Create(ctx, domain.MetricBatch{
			ID: "mbat_v14", WorkbookName: "perf.xlsx", WorkbookSHA256: "abc",
			Status: domain.BatchStatusCompleted, Mapping: "{}",
			StartedAt: simTS,
		}); err != nil {
			return err
		}
		if err := tx.Resources.Insert(ctx, newDeviceResource("res_v14_dev", "bat_v14b")); err != nil {
			return err
		}
		return tx.Metrics.InsertPoints(ctx, []domain.MetricPoint{
			{SeriesID: "ser_v14", TS: "2026-10-01T00:00:00Z", Value: -8.5,
				Quality: domain.PointQualityOk, BatchID: "mbat_v14"},
		})
	}); err == nil {
		// series ser_v14 does not exist yet → the FK must have refused this.
		t.Fatalf("insert point with unknown series succeeded, want FOREIGN KEY error")
	} else if !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Fatalf("insert point with unknown series = %v, want FOREIGN KEY error", err)
	}

	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.Batches.Create(ctx, newBatch("bat_v14b", simTS))
	}); err != nil {
		t.Fatalf("seed second batch: %v", err)
	}
	if err := db2.Do(ctx, func(tx *Tx) error {
		if err := tx.Resources.Insert(ctx, newDeviceResource("res_v14_dev", "bat_v14b")); err != nil {
			return err
		}
		_, _, err := tx.Metrics.FindOrCreateSeries(ctx, "res_v14_dev", "optical_power_rx_avg_dbm", nil,
			"2026-10-01T00:00:00Z", "2026-10-02T00:00:00Z")
		return err
	}); err != nil {
		t.Fatalf("create series: %v", err)
	}
	// A series with an unregistered metric_key must be refused (catalog FK).
	if err := db2.Do(ctx, func(tx *Tx) error {
		_, _, err := tx.Metrics.FindOrCreateSeries(ctx, "res_v14_dev", "no_such_metric", nil,
			"2026-10-01T00:00:00Z", "2026-10-02T00:00:00Z")
		return err
	}); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("series with unknown metric_key = %v, want FOREIGN KEY error", err)
	}

	// Bad quality value refused by the metrics CHECK.
	if err := db2.Do(ctx, func(tx *Tx) error {
		return tx.Metrics.InsertPoints(ctx, []domain.MetricPoint{
			{SeriesID: "ser_v14", TS: "2026-10-03T00:00:00Z", Value: 1,
				Quality: domain.PointQuality("garbage"), BatchID: "mbat_v14"},
		})
	}); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("insert bogus quality = %v, want CHECK constraint error", err)
	}

	// The NULL-scope series identity: a second INSERT of the same
	// (resource, metric, NULL) via FindOrCreateSeries must return the SAME
	// series_id (one row), and a direct raw INSERT OR IGNORE must not add a
	// row either — the §5.3 table-level UNIQUE alone cannot enforce this.
	if err := db2.Do(ctx, func(tx *Tx) error {
		id, created, err := tx.Metrics.FindOrCreateSeries(ctx, "res_v14_dev", "optical_power_rx_avg_dbm", nil,
			"2026-10-01T00:00:00Z", "2026-10-02T00:00:00Z")
		if err != nil {
			return err
		}
		if created {
			t.Errorf("re-find of existing series reported created=true")
		}
		var want string
		if err := tx.Metrics.q.QueryRowContext(ctx,
			`SELECT series_id FROM metric_series WHERE resource_id = 'res_v14_dev' AND metric_key = 'optical_power_rx_avg_dbm'`).
			Scan(&want); err != nil {
			return err
		}
		if id != want {
			t.Errorf("FindOrCreateSeries id %q != stored %q", id, want)
		}
		if _, err := tx.Metrics.q.ExecContext(ctx,
			`INSERT OR IGNORE INTO metric_series (series_id, resource_id, metric_key, scope, first_seen_at, last_seen_at)
VALUES ('ser_raw_dup','res_v14_dev','optical_power_rx_avg_dbm',NULL,'x','x')`); err != nil {
			return err
		}
		var cnt int
		if err := tx.Metrics.q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM metric_series WHERE resource_id = 'res_v14_dev' AND metric_key = 'optical_power_rx_avg_dbm'`).
			Scan(&cnt); err != nil {
			return err
		}
		if cnt != 1 {
			t.Errorf("NULL-scope series rows = %d, want 1 (identity index must dedupe)", cnt)
		}
		return nil
	}); err != nil {
		t.Fatalf("identity dedupe check: %v", err)
	}

	// The pre-v14 data survived untouched. W22 adaptation: the database is
	// held at v14 (< v17) so the repo read path (whose SELECT now carries
	// the run-state columns) cannot prepare — raw SQL pins the stored row.
	dKey, dName, _, _, dUpdated, err := preV17Resource(ctx, db2, "res_v14_dev")
	if err != nil || dKey != "DEV-v14_dev" || dName != "dev" || dUpdated != formatTime(simTS) {
		t.Errorf("device row after v14 = %q/%q@%q (err %v), want byte-identical survival", dKey, dName, dUpdated, err)
	}

	// The re-applied seed is idempotent: INSERT OR IGNORE of an existing
	// key changes nothing (repair path when rows were deleted manually).
	if _, err := db2.sql.ExecContext(ctx,
		`INSERT OR IGNORE INTO metric_keys (metric_key, label, unit, value_kind, created_at)
VALUES ('optical_power_rx_avg_dbm', 'dup', 'dup', 'gauge', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("idempotent seed insert: %v", err)
	}
	var label string
	if err := db2.sql.QueryRowContext(ctx,
		`SELECT label FROM metric_keys WHERE metric_key = 'optical_power_rx_avg_dbm'`).Scan(&label); err != nil {
		t.Fatalf("read seeded label: %v", err)
	}
	if label != "接收光功率均值" {
		t.Errorf("seeded label after duplicate insert = %q, want unchanged 接收光功率均值", label)
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 14 {
		t.Errorf("max schema version after upgrade = %d, want 14", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op.
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if ok, err := tableExists(ctx, db3, "metrics"); err != nil || !ok {
		t.Errorf("metrics exists after reopen = %v (err %v), want true", ok, err)
	}
	var n3 int
	if err := db3.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_keys`).Scan(&n3); err != nil {
		t.Fatalf("reopen catalog count: %v", err)
	}
	if n3 != 14 {
		t.Errorf("reopen catalog rows = %d, want 14", n3)
	}
}
