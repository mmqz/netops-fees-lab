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

// withoutV28 temporarily removes migration v28 (and any later) from the
// registry so the next Open builds a genuine v27 database (the v0.12-R18
// head: run-delivery audit, no lifecycle audit). Restored via t.Cleanup —
// the withoutV27 pattern.
func withoutV28(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 27 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

// seedAlertForV28 stores one firing alert + its rule + series + resource
// chain (the minimal join context ListAlerts and the lifecycle audit
// link need). The timestamps are FIXED seeds — no clock in the data
// path. The rule comes from the lazily-seeded default catalog; the
// series exists before the alert references it.
func seedAlertForV28(t *testing.T, db *DB, alertID string) {
	t.Helper()
	ctx := context.Background()
	ts := time.Date(2026, 10, 18, 8, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_"+alertID, time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC))); err != nil {
			return err
		}
		res := newResource("res_"+alertID, "DEV-001|"+alertID, time.Date(2026, 10, 18, 7, 0, 0, 0, time.UTC))
		res.Type = domain.ResourceTypePort
		res.FirstBatchID = "bat_" + alertID
		if err := tx.Resources.Insert(ctx, res); err != nil {
			return err
		}
		if err := tx.DetectionRules.SeedDefaultDetectionRules(ctx); err != nil {
			return err
		}
		scope := "eth0"
		seriesID, _, err := tx.Metrics.FindOrCreateSeries(ctx, "res_"+alertID, "optical_power_rx_avg_dbm", &scope, ts, ts)
		if err != nil {
			return err
		}
		_, err = tx.Alerts.InsertFiringAlert(ctx, domain.Alert{
			AlertID: alertID, RuleID: "dr_opt_rx_low", SeriesID: seriesID,
			FiredAt: ts, LastValue: -18.5, FirstSeen: ts,
		})
		return err
	}); err != nil {
		t.Fatalf("seed alert %s: %v", alertID, err)
	}
}

// TestMigrationV28AddsAlertEventDeliveryAudit pins the lifecycle
// notification audit store and its alerts-listing join (v0.13-R19):
//
//   - on a genuine v27 database (v28 temporarily unregistered) a
//     lifecycle delivery insert fails with "no such table" — the
//     regression the migration fixes;
//   - re-registering v28 and reopening the SAME file applies it: the
//     delivery row round-trips (LatestByAlert: status/event/attempts/
//     redacted URL/httpStatus/error), an alert WITHOUT a row LEFT
//     JOINs to the zero verdict (never notified — the honest "" read),
//     an alert WITH a delivered row joins to delivered + its event,
//     the LATEST of two rows (ack then clear) wins, and both CHECK
//     columns reject out-of-enum values (event, status); the
//     alert_id index exists; MAX(version) == 28;
//   - a third Open is a bookkeeping no-op (idempotency).
func TestMigrationV28AddsAlertEventDeliveryAudit(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v27 database.
	tail := withoutV28(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v27: %v", err)
	}
	seedAlertForV28(t, db1, "al_v28_a")
	if _, err := db1.sql.ExecContext(ctx,
		`INSERT INTO alert_event_deliveries (id, alert_id, event, url, status, attempts, delivered_at)
VALUES ('ad_pre','al_v28_a','ack','http://h/p','delivered',1,'2026-10-18T08:00:00Z')`); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Errorf("insert lifecycle delivery at v27 = %v, want \"no such table\" error", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v27 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v28 applies.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v27 → v28): %v", err)
	}
	defer db2.Close()
	seedAlertForV28(t, db2, "al_v28_b")

	v28TS := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	if err := db2.AlertEventDeliveries.InsertDelivery(ctx, domain.AlertEventDelivery{
		ID: "ad_v28_ack", AlertID: "al_v28_a", Event: domain.AlertLifecycleAck,
		URL: "http://receiver.example/hook", Status: domain.WebhookDelivered,
		Attempts: 3, HTTPStatus: 200,
		DeliveredAt: v28TS.Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("insert ack verdict row: %v", err)
	}
	if err := db2.AlertEventDeliveries.InsertDelivery(ctx, domain.AlertEventDelivery{
		ID: "ad_v28_clear", AlertID: "al_v28_a", Event: domain.AlertLifecycleClear,
		URL: "http://receiver.example/hook", Status: domain.WebhookFailed,
		Attempts: 3, HTTPStatus: 503, Error: "receiver status 503",
		DeliveredAt: v28TS.Add(time.Hour).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("insert clear verdict row: %v", err)
	}

	// The CHECKs are the backstop of the domain parse: event and status
	// are both closed sets.
	for _, bad := range []struct {
		event  string
		status string
	}{
		{event: "escalate", status: "delivered"}, // not a lifecycle edge
		{event: "ack", status: "pending"},        // no pending state, ever
	} {
		if _, err := db2.sql.ExecContext(ctx,
			`INSERT INTO alert_event_deliveries (id, alert_id, event, url, status, attempts, delivered_at)
VALUES ('ad_bad','al_v28_b',?,'http://h/p',?,1,'2026-10-18T08:00:00Z')`, bad.event, bad.status); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("insert event=%q status=%q = %v, want CHECK constraint failure", bad.event, bad.status, err)
		}
	}

	// LatestByAlert round-trips the LATEST verdict (clear wins over the
	// earlier ack — the newest human decision the channel learned).
	got, err := db2.AlertEventDeliveries.LatestByAlert(ctx, "al_v28_a")
	if err != nil {
		t.Fatalf("LatestByAlert: %v", err)
	}
	if got.Status != domain.WebhookFailed || got.Event != domain.AlertLifecycleClear {
		t.Errorf("LatestByAlert = %s/%s, want failed/clear (the latest row)", got.Status, got.Event)
	}
	if got.Attempts != 3 || got.HTTPStatus != 503 || got.Error != "receiver status 503" {
		t.Errorf("LatestByAlert = %+v, want attempts 3 / 503 / the error text", got)
	}
	if got.URL != "http://receiver.example/hook" {
		t.Errorf("LatestByAlert url = %q, want the stored (already-redacted) form", got.URL)
	}
	if _, err := db2.AlertEventDeliveries.LatestByAlert(ctx, "al_v28_b"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("LatestByAlert(never notified) = %v, want not-found", err)
	}

	// Edge-order pin: the ack row carries the lexicographically LARGER
	// delivered_at (".41Z" > ".4125Z" — 'Z' > '2', the RFC3339Nano
	// variable-width trap) and is the one an ORDER BY delivered_at DESC
	// would wrongly return. The read must answer by the EVENT's edge
	// order instead: ack ≺ clear by the state machine (ack requires
	// firing, clear requires firing|acked — the guards reject repeats
	// with 409, so an alert has at most one row per edge and clear is
	// the later decision whenever both exist). The same reasoning makes
	// the read immune to the ack/clear audit inserts taking the SQLite
	// write lock in either order (they are concurrent goroutines —
	// insertion order is NOT verdict order).
	if err := db2.AlertEventDeliveries.InsertDelivery(ctx, domain.AlertEventDelivery{
		ID: "ad_v28_inv_ack", AlertID: "al_v28_b", Event: domain.AlertLifecycleAck,
		URL: "http://receiver.example/hook", Status: domain.WebhookDelivered,
		Attempts: 1, HTTPStatus: 200, DeliveredAt: "2026-10-18T09:30:00.41Z",
	}); err != nil {
		t.Fatalf("insert inversion ack row: %v", err)
	}
	if err := db2.AlertEventDeliveries.InsertDelivery(ctx, domain.AlertEventDelivery{
		ID: "ad_v28_inv_clear", AlertID: "al_v28_b", Event: domain.AlertLifecycleClear,
		URL: "http://receiver.example/hook", Status: domain.WebhookDelivered,
		Attempts: 1, HTTPStatus: 200, DeliveredAt: "2026-10-18T09:30:00.4125Z",
	}); err != nil {
		t.Fatalf("insert inversion clear row: %v", err)
	}
	if got, err := db2.AlertEventDeliveries.LatestByAlert(ctx, "al_v28_b"); err != nil || got.Event != domain.AlertLifecycleClear {
		t.Errorf("LatestByAlert(clock inversion) = %+v (%v), want clear (the edge order, not the string sort)", got, err)
	}

	// The alert_id index exists (the alerts-listing join + LatestByAlert
	// lookup path).
	var name string
	if err := db2.sql.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='index' AND name='idx_alert_event_deliveries_alert'`).Scan(&name); err != nil {
		t.Errorf("idx_alert_event_deliveries_alert: %v (index missing)", err)
	}

	// The alerts-listing LEFT JOIN: alert without a row → the zero join;
	// alert with rows → the LATEST verdict + its event. al_v28_b carries
	// the clock-inversion pair — the JOIN must agree with LatestByAlert
	// (edge order, clear); al_v28_c is the never-notified zero read.
	seedAlertForV28(t, db2, "al_v28_c")
	rows, _, err := db2.Alerts.ListAlerts(ctx, AlertFilter{})
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	verdicts := make(map[string]LifecycleJoin, len(rows))
	for _, row := range rows {
		verdicts[row.AlertID] = row.Lifecycle
	}
	if lc := verdicts["al_v28_a"]; lc.Status != domain.WebhookFailed || lc.Event != domain.AlertLifecycleClear || lc.Attempts != 3 || lc.TS == "" {
		t.Errorf("join of notified alert = %+v, want failed/clear/3/ts", lc)
	}
	if lc := verdicts["al_v28_b"]; lc.Status != domain.WebhookDelivered || lc.Event != domain.AlertLifecycleClear || lc.Attempts != 1 {
		t.Errorf("join of inversion pair = %+v, want delivered/clear/1 (the edge order — same verdict as LatestByAlert)", lc)
	}
	if lc := verdicts["al_v28_c"]; lc.Status != "" || lc.Event != "" || lc.Attempts != 0 || lc.TS != "" {
		t.Errorf("join of never-notified alert = %+v, want the zero verdict (Status \"\")", lc)
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != len(migrations) {
		t.Errorf("max schema version after upgrade = %d, want the registry head", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op.
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if _, err := db3.AlertEventDeliveries.LatestByAlert(ctx, "al_v28_a"); err != nil {
		t.Errorf("LatestByAlert after reopen: %v", err)
	}
}

// TestMigrationV28DeliberatelyNotFK pins the audit-line discipline: the
// delivery row SURVIVES its alert being gone (no FK, no cascade) — the
// v27 run_id precedent, where the audit trail may outlive the thing it
// describes rather than dangle-reference it.
func TestMigrationV28DeliberatelyNotFK(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedAlertForV28(t, db, "al_v28_fk")
	if err := db.AlertEventDeliveries.InsertDelivery(ctx, domain.AlertEventDelivery{
		ID: "ad_v28_fk", AlertID: "al_v28_fk", Event: domain.AlertLifecycleAck,
		URL: "http://receiver.example/hook", Status: domain.WebhookDelivered,
		Attempts: 1, HTTPStatus: 200, DeliveredAt: "2026-10-18T09:30:00Z",
	}); err != nil {
		t.Fatalf("insert verdict row: %v", err)
	}
	// Remove the alert directly (no API path deletes alerts — the
	// audit-trail doctrine — so the raw delete is the pathological
	// probe, exactly the case the missing FK must tolerate).
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM alerts WHERE alert_id = 'al_v28_fk'`); err != nil {
		t.Fatalf("delete alert: %v", err)
	}
	if _, err := db.AlertEventDeliveries.LatestByAlert(ctx, "al_v28_fk"); err != nil {
		t.Errorf("LatestByAlert after its alert is gone: %v, want the surviving audit line", err)
	}
}
