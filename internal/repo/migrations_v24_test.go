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

// withoutV24 temporarily removes migration v24 (and any later) from the
// registry so the next Open builds a genuine v23 database (the v0.11.1
// head: layer overlay present, no maintenance windows). Restored via
// t.Cleanup — the withoutV21 pattern.
func withoutV24(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 23 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

var v24TS = time.Date(2026, 4, 2, 9, 0, 0, 0, time.UTC)

// TestMigrationV24AddsMaintenanceWindows pins the window store and the
// detection_runs suppression column (v0.12-R15):
//
//   - on a genuine v23 database (v24 temporarily unregistered) a window
//     insert fails with "no such table" — the regression the migration
//     fixes; a receipt insert WITHOUT the suppressed column still works
//     (the v21 shape) and backfills to suppressed=0 after the upgrade;
//   - re-registering v24 and reopening the SAME file applies it: the
//     window repo round-trips (insert / newest-started listing / scope
//     JSON encode-decode / honest 404 delete), MAX(version) == 24;
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV24AddsMaintenanceWindows(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v23 database.
	tail := withoutV24(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v23: %v", err)
	}
	if _, err := db1.sql.ExecContext(ctx,
		`INSERT INTO maintenance_windows (id, label, starts_at, ends_at) VALUES ('mw_t','w','2026-04-01T00:00:00Z','2026-04-02T00:00:00Z')`); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Errorf("insert maintenance_windows at v23 = %v, want \"no such table\" error", err)
	}
	// A v21-shaped receipt (no suppressed column) inserts fine pre-v24…
	if _, err := db1.sql.ExecContext(ctx,
		`INSERT INTO detection_runs (id, source, started_at, finished_at, duration_ms)
VALUES ('run_v24_pre','api','2026-04-01T08:00:00Z','2026-04-01T08:00:01Z',1000)`); err != nil {
		t.Fatalf("insert v21-shaped receipt at v23: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v23 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v24 applies.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v23 → v24): %v", err)
	}
	defer db2.Close()

	// The pre-upgrade receipt backfilled to suppressed=0 — the additive
	// ALTER's DEFAULT 0 discipline (v21 rows never lie about the past).
	var sup int
	if err := db2.sql.QueryRowContext(ctx,
		`SELECT suppressed FROM detection_runs WHERE id = 'run_v24_pre'`).Scan(&sup); err != nil {
		t.Fatalf("read backfilled suppressed: %v", err)
	}
	if sup != 0 {
		t.Errorf("backfilled suppressed = %d, want 0", sup)
	}

	// Window round-trip: global scope, scoped scope, ordering, 404 delete.
	wins := []domain.MaintenanceWindow{
		{WindowID: "mw_v24_a", Label: "全网升级窗口", StartsAt: "2026-04-01T00:00:00Z", EndsAt: "2026-04-02T00:00:00Z", CreatedAt: v24TS.Format(time.RFC3339)},
		{WindowID: "mw_v24_b", Label: "哈大齐割接", StartsAt: "2026-04-05T10:00:00Z", EndsAt: "2026-04-05T14:00:00Z",
			ResourceIDs: []string{"res_2", "res_1", "res_2"}, CreatedAt: v24TS.Format(time.RFC3339)},
	}
	for i, win := range wins {
		if err := db2.Maintenance.InsertWindow(ctx, win); err != nil {
			t.Fatalf("insert window %d: %v", i, err)
		}
	}
	list, err := db2.Maintenance.ListWindows(ctx)
	if err != nil {
		t.Fatalf("list windows: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("windows = %d, want 2", len(list))
	}
	// Newest-started first: the 割接 (Apr 5) leads the 升级 (Apr 1).
	if list[0].WindowID != "mw_v24_b" || list[1].WindowID != "mw_v24_a" {
		t.Errorf("window order = %s, %s; want newest-started first", list[0].WindowID, list[1].WindowID)
	}
	// The scope round-trips DEDUPLICATED AND SORTED (stable encoding).
	if got := list[0].ResourceIDs; len(got) != 2 || got[0] != "res_1" || got[1] != "res_2" {
		t.Errorf("scoped window ids = %v, want [res_1 res_2] (dedup + sort)", got)
	}
	// The global scope decodes to nil (no scope), not an empty slice.
	if list[1].ResourceIDs != nil {
		t.Errorf("global window ids = %v, want nil", list[1].ResourceIDs)
	}

	// Delete: the honest 404 (domain.ErrNotFound), then the real delete.
	if err := db2.Maintenance.DeleteWindow(ctx, "mw_missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("delete unknown window = %v, want not-found error", err)
	}
	if err := db2.Maintenance.DeleteWindow(ctx, "mw_v24_b"); err != nil {
		t.Fatalf("delete window: %v", err)
	}
	list, err = db2.Maintenance.ListWindows(ctx)
	if err != nil {
		t.Fatalf("relist windows: %v", err)
	}
	if len(list) != 1 || list[0].WindowID != "mw_v24_a" {
		t.Errorf("after delete windows = %+v, want only mw_v24_a", list)
	}

	// The receipt round-trips the suppressed counter end to end.
	if err := db2.DetectionRuns.InsertRun(ctx, domain.DetectionRun{
		RunID: "run_v24_post", Source: domain.RunSourceAPI,
		StartedAt: v24TS.Format(time.RFC3339), FinishedAt: v24TS.Add(time.Second).Format(time.RFC3339),
		DurationMS: 1000, Suppressed: 4,
	}); err != nil {
		t.Fatalf("insert suppressed receipt: %v", err)
	}
	runs, _, err := db2.DetectionRuns.ListRuns(ctx, 1, 0)
	if err != nil {
		t.Fatalf("list receipts: %v", err)
	}
	if runs[0].RunID != "run_v24_post" || runs[0].Suppressed != 4 {
		t.Errorf("receipt = %+v, want run_v24_post with suppressed=4", runs[0])
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 32 {
		t.Errorf("max schema version after upgrade = %d, want 32", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op.
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if list, err := db3.Maintenance.ListWindows(ctx); err != nil || len(list) != 1 {
		t.Errorf("windows after reopen: len=%d err=%v, want 1 / nil", len(list), err)
	}
}

// TestMaintenanceScopeEncoding pins the domain scope codec: the stable
// (sorted, deduplicated) JSON representation of the window scope — equal
// scopes encode byte-identically, "" is the global scope both ways.
func TestMaintenanceScopeEncoding(t *testing.T) {
	if got := domain.EncodeMaintenanceScope(nil); got != "" {
		t.Errorf("encode nil = %q, want \"\"", got)
	}
	if got := domain.EncodeMaintenanceScope([]string{}); got != "" {
		t.Errorf("encode empty = %q, want \"\"", got)
	}
	// Duplicates compact, order normalizes — the same logical scope always
	// encodes to the same bytes.
	a := domain.EncodeMaintenanceScope([]string{"res_9", "res_1", "res_9", "res_4"})
	b := domain.EncodeMaintenanceScope([]string{"res_4", "res_1", "res_9"})
	if a != b {
		t.Errorf("scope encoding not stable: %q vs %q", a, b)
	}
	if a != `["res_1","res_4","res_9"]` {
		t.Errorf("encode = %q, want the sorted deduped JSON array", a)
	}
	ids, err := domain.DecodeMaintenanceScope(a)
	if err != nil || len(ids) != 3 || ids[0] != "res_1" || ids[2] != "res_9" {
		t.Errorf("decode = %v err=%v, want [res_1 res_4 res_9]", ids, err)
	}
	if ids, err := domain.DecodeMaintenanceScope(""); err != nil || ids != nil {
		t.Errorf("decode \"\" = %v err=%v, want nil/nil (global scope)", ids, err)
	}
	if _, err := domain.DecodeMaintenanceScope("{bad json"); err == nil {
		t.Error("decode malformed = nil error, want error")
	}
}
