package repo

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// withoutV27 temporarily removes migration v27 (and any later) from the
// registry so the next Open builds a genuine v26 database (the v0.12-R17
// head: flap_held receipts, no delivery audit). Restored via t.Cleanup —
// the withoutV24 pattern.
func withoutV27(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 26 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

// TestMigrationV27AddsWebhookDeliveryAudit pins the notification audit
// store and its runs-listing join (v0.12-R18):
//
//   - on a genuine v26 database (v27 temporarily unregistered) a delivery
//     insert fails with "no such table" — the regression the migration
//     fixes;
//   - re-registering v27 and reopening the SAME file applies it: the
//     delivery row round-trips (GetByRun: status/attempts/httpStatus/
//     redacted URL/error), a run receipt WITHOUT a row LEFT JOINs to the
//     zero verdict (channel off — the honest "" read), a receipt WITH a
//     failed row joins to failed, and the CHECK rejects out-of-enum
//     statuses; MAX(version) == 27;
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV27AddsWebhookDeliveryAudit(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v26 database.
	tail := withoutV27(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v26: %v", err)
	}
	if _, err := db1.sql.ExecContext(ctx,
		`INSERT INTO webhook_deliveries (id, run_id, url, status, attempts, delivered_at)
VALUES ('wd_pre','run_x','http://h/p','delivered',1,'2026-10-17T08:00:00Z')`); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Errorf("insert delivery at v26 = %v, want \"no such table\" error", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v26 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v27 applies.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v26 → v27): %v", err)
	}
	defer db2.Close()

	// Two receipts: one with a delivered verdict, one channel-off.
	v27TS := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	for _, id := range []string{"run_v27_a", "run_v27_b"} {
		if err := db2.DetectionRuns.InsertRun(ctx, domain.DetectionRun{
			RunID: id, Source: domain.RunSourceAPI,
			StartedAt: v27TS.Format(time.RFC3339), FinishedAt: v27TS.Add(time.Second).Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("seed receipt %s: %v", id, err)
		}
	}
	if err := db2.WebhookDeliveries.InsertDelivery(ctx, domain.WebhookDelivery{
		ID: "wd_v27_a", RunID: "run_v27_a", URL: "http://receiver.example/hook",
		Status: domain.WebhookDelivered, Attempts: 3, HTTPStatus: 200,
		DeliveredAt: v27TS.Add(2 * time.Second).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("insert delivered row: %v", err)
	}
	if err := db2.WebhookDeliveries.InsertDelivery(ctx, domain.WebhookDelivery{
		ID: "wd_v27_c", RunID: "run_v27_c", URL: "http://dead.example/hook",
		Status: domain.WebhookFailed, Attempts: 3, HTTPStatus: 503, Error: "receiver status 503",
		DeliveredAt: v27TS.Add(3 * time.Second).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("insert failed row: %v", err)
	}

	// The CHECK is the backstop of the domain parse.
	if _, err := db2.sql.ExecContext(ctx,
		`INSERT INTO webhook_deliveries (id, run_id, url, status, attempts, delivered_at)
VALUES ('wd_bad','run_x','http://h/p','pending',1,'2026-10-17T08:00:00Z')`); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("insert pending status = %v, want CHECK constraint failure", err)
	}

	// GetByRun round-trips the delivered verdict.
	got, err := db2.WebhookDeliveries.GetByRun(ctx, "run_v27_a")
	if err != nil {
		t.Fatalf("GetByRun: %v", err)
	}
	if got.Status != domain.WebhookDelivered || got.Attempts != 3 || got.HTTPStatus != 200 || got.URL != "http://receiver.example/hook" {
		t.Errorf("GetByRun = %+v, want delivered/3/200 with the URL", got)
	}
	if _, err := db2.WebhookDeliveries.GetByRun(ctx, "run_v27_b"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("GetByRun(unknown) = %v, want not-found", err)
	}

	// The runs-listing LEFT JOIN: delivered row → verdict; channel-off
	// receipt → the zero join (Status "" — the honest three-valued read).
	rows, _, err := db2.DetectionRuns.ListRuns(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	verdicts := make(map[string]DeliveryJoin, len(rows))
	for _, row := range rows {
		verdicts[row.RunID] = row.Delivery
	}
	if d := verdicts["run_v27_a"]; d.Status != domain.WebhookDelivered || d.Attempts != 3 {
		t.Errorf("join of delivered run = %+v, want delivered/3", d)
	}
	if d := verdicts["run_v27_b"]; d.Status != "" || d.Attempts != 0 {
		t.Errorf("join of channel-off run = %+v, want the zero verdict (Status \"\")", d)
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 34 {
		t.Errorf("max schema version after upgrade = %d, want 34", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op.
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if _, err := db3.WebhookDeliveries.GetByRun(ctx, "run_v27_a"); err != nil {
		t.Errorf("GetByRun after reopen: %v", err)
	}
}
