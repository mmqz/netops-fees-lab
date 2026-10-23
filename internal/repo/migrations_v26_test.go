package repo

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/testsupport"
)

// withoutV26 temporarily removes migration v26 (and any later) from the
// registry so the next Open builds a genuine v25 database (the v0.12-R16
// head: alert correlation present, no flap_held column). Restored via
// t.Cleanup — the withoutV24 pattern.
func withoutV26(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 25 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

// TestMigrationV26AddsFlapHeldReceipt pins the flap-damped receipt column
// (v0.12-R17):
//
//   - on a genuine v25 database (v26 temporarily unregistered) a receipt
//     insert naming flap_held fails with "no such column" — the
//     regression the migration fixes; a receipt insert WITHOUT the
//     column (the v24/v25 shape) still works and backfills to
//     flap_held=0 after the upgrade (v21 rows never lie about the past);
//   - re-registering v26 and reopening the SAME file applies it: the
//     receipt round-trips the flap_held counter, MAX(version) == 26;
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV26AddsFlapHeldReceipt(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v25 database.
	tail := withoutV26(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v25: %v", err)
	}
	if _, err := db1.sql.ExecContext(ctx,
		`INSERT INTO detection_runs (id, source, started_at, finished_at, duration_ms, flap_held)
VALUES ('run_v26_pre','api','2026-10-17T08:00:00Z','2026-10-17T08:00:01Z',1000,2)`); err == nil || !strings.Contains(err.Error(), "flap_held") {
		t.Errorf("insert flap_held receipt at v25 = %v, want the missing-column error", err)
	}
	// A v24/v25-shaped receipt (no flap_held) inserts fine pre-v26…
	if _, err := db1.sql.ExecContext(ctx,
		`INSERT INTO detection_runs (id, source, started_at, finished_at, duration_ms)
VALUES ('run_v26_shape','api','2026-10-17T08:00:00Z','2026-10-17T08:00:01Z',1000)`); err != nil {
		t.Fatalf("insert v25-shaped receipt at v25: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v25 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v26 applies.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v25 → v26): %v", err)
	}
	defer db2.Close()

	// The pre-upgrade receipt backfilled to flap_held=0 — the additive
	// ALTER's DEFAULT 0 discipline (pre-R17 runs never held anything).
	var held int
	if err := db2.sql.QueryRowContext(ctx,
		`SELECT flap_held FROM detection_runs WHERE id = 'run_v26_shape'`).Scan(&held); err != nil {
		t.Fatalf("read backfilled flap_held: %v", err)
	}
	if held != 0 {
		t.Errorf("backfilled flap_held = %d, want 0", held)
	}

	// The receipt round-trips the flap_held counter end to end.
	v26TS := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	if err := db2.DetectionRuns.InsertRun(ctx, domain.DetectionRun{
		RunID: "run_v26_post", Source: domain.RunSourceAPI,
		StartedAt: v26TS.Format(time.RFC3339), FinishedAt: v26TS.Add(time.Second).Format(time.RFC3339),
		DurationMS: 1000, Suppressed: 4, FlapHeld: 2,
	}); err != nil {
		t.Fatalf("insert flap-held receipt: %v", err)
	}
	runs, _, err := db2.DetectionRuns.ListRuns(ctx, 1, 0)
	if err != nil {
		t.Fatalf("list receipts: %v", err)
	}
	if runs[0].RunID != "run_v26_post" || runs[0].FlapHeld != 2 || runs[0].Suppressed != 4 {
		t.Errorf("receipt = %+v, want run_v26_post with flapHeld=2 suppressed=4", runs[0])
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 33 {
		t.Errorf("max schema version after upgrade = %d, want 33", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op.
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	runs, _, err = db3.DetectionRuns.ListRuns(ctx, 0, 0)
	if err != nil || len(runs) != 2 {
		t.Errorf("receipts after reopen: len=%d err=%v, want 2 / nil", len(runs), err)
	}
}
