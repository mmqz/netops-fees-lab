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

// withoutV18 temporarily removes migration v18 AND every later migration
// from the registry so the next Open builds a GENUINE v17 database (the
// W22 head: run-state model live, no detection tables). The registry is
// restored via t.Cleanup; the removed tail is returned for Phase 2's
// full-registry reopen (the withoutV13 pattern).
func withoutV18(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 17 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

// v18TS is the fixed timestamp of this file's seeds (no clock in the data
// path — the simrepo_test.go convention).
var v18TS = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// seedV18PortSeries seeds ONE port resource with one metric series of
// optical_power_rx_avg_dbm and five daily points — the populated-DB data
// the v18 upgrade must preserve byte-identically. Returns (portID,
// seriesID).
func seedV18PortSeries(t *testing.T, db *DB) (string, string) {
	t.Helper()
	ctx := context.Background()
	portID, seriesID := "res_v18_port", ""
	err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_v18", v18TS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: portID, Type: domain.ResourceTypePort, NaturalKey: "DEV-V18|GE-0/0/1",
			Name: "v18端口", Attributes: map[string]string{}, FirstBatchID: "bat_v18",
			CreatedAt: v18TS, UpdatedAt: v18TS,
		})
	})
	if err != nil {
		t.Fatalf("seed v17 resource: %v", err)
	}
	seriesID, _, err = db.Metrics.FindOrCreateSeries(ctx, portID, "optical_power_rx_avg_dbm",
		nil, "2026-03-01T00:00:00Z", "2026-03-05T00:00:00Z")
	if err != nil {
		t.Fatalf("seed v17 series: %v", err)
	}
	// Metric points reference the SEPARATE metric_batches family
	// (migration v14, ADR-0011 D1 — 不与资源批次混表).
	if err := db.MetricBatches.Create(ctx, domain.MetricBatch{
		ID: "mbat_v18", WorkbookName: "v18.xlsx",
		WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Status:         domain.BatchStatusCompleted, RowsTotal: 5, RowsImported: 5,
		StartedAt: v18TS,
	}); err != nil {
		t.Fatalf("seed v17 metric batch: %v", err)
	}
	if err := db.Metrics.InsertPoints(ctx, []domain.MetricPoint{
		{SeriesID: seriesID, TS: "2026-03-01T00:00:00Z", Value: -8.1, Quality: domain.PointQualityOk, BatchID: "mbat_v18"},
		{SeriesID: seriesID, TS: "2026-03-02T00:00:00Z", Value: -14.5, Quality: domain.PointQualityOk, BatchID: "mbat_v18"},
		{SeriesID: seriesID, TS: "2026-03-03T00:00:00Z", Value: -15.0, Quality: domain.PointQualityOk, BatchID: "mbat_v18"},
		{SeriesID: seriesID, TS: "2026-03-04T00:00:00Z", Value: -15.4, Quality: domain.PointQualityOk, BatchID: "mbat_v18"},
		{SeriesID: seriesID, TS: "2026-03-05T00:00:00Z", Value: -15.2, Quality: domain.PointQualityOk, BatchID: "mbat_v18"},
	}); err != nil {
		t.Fatalf("seed v17 points: %v", err)
	}
	return portID, seriesID
}

// TestMigrationV18AddsDetection pins the alert detection model (ADR-0011
// D2/D3, §12-B2):
//
//   - on a genuine v17 database (v18 temporarily unregistered) both rule
//     and alert inserts fail with "no such table" — the regression the
//     migration exists to fix;
//   - re-registering v18 and reopening the SAME file applies it: both
//     tables exist, the closed CHECK enums (comparator / severity / state)
//     reject out-of-set values, the metric_keys and metric_series FKs are
//     enforced, the UNIQUE (rule_id, series_id, first_seen) identity
//     blocks a duplicate firing row, the populated v17 data survived
//     byte-identically (resource + series + points counts and values), a
//     rule+alert pair round-trips through the repos, and
//     MAX(version) == 18;
//   - a third Open is a bookkeeping no-op (idempotency);
//   - the DEFAULT rule set is NOT in the migration: after v18 the
//     detection_rules table is EMPTY until the lazy Go-side seed runs
//     (rules are user data, not schema — pinned here).
func TestMigrationV18AddsDetection(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v17 database with populated metric data.
	tail := withoutV18(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v17: %v", err)
	}
	portID, seriesID := seedV18PortSeries(t, db1)
	// Pre-migration regression: neither table exists.
	if _, err := db1.sql.ExecContext(ctx,
		`INSERT INTO detection_rules (rule_id, metric_key, comparator, threshold, window, severity, enabled, label, created_at, updated_at)
VALUES ('dr_t','optical_power_rx_avg_dbm','lte',-14.0,3,'warning',1,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Errorf("insert detection_rules at v17 = %v, want \"no such table\" error", err)
	}
	if _, err := db1.sql.ExecContext(ctx,
		`INSERT INTO alerts (alert_id, rule_id, series_id, fired_at, state) VALUES ('al_t','dr_t','ser_x','2026-01-01T00:00:00Z','firing')`); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Errorf("insert alerts at v17 = %v, want \"no such table\" error", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v17 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v18 applies.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v17 → v18): %v", err)
	}
	defer db2.Close()

	// The closed comparator enum is enforced by CHECK.
	for _, bad := range []string{"eq", ">", ""} {
		if _, err := db2.sql.ExecContext(ctx,
			`INSERT INTO detection_rules (rule_id, metric_key, comparator, threshold, window, severity, enabled, created_at, updated_at)
VALUES ('dr_bad_c','optical_power_rx_avg_dbm',?,-14.0,3,'warning',1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, bad); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("insert comparator %q = %v, want CHECK constraint error", bad, err)
		}
	}
	// The closed severity enum too.
	for _, bad := range []string{"critical", "warn", ""} {
		if _, err := db2.sql.ExecContext(ctx,
			`INSERT INTO detection_rules (rule_id, metric_key, comparator, threshold, window, severity, enabled, created_at, updated_at)
VALUES ('dr_bad_s','optical_power_rx_avg_dbm','lte',-14.0,3,?,1,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, bad); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("insert severity %q = %v, want CHECK constraint error", bad, err)
		}
	}
	// The closed alert state enum too.
	if err := db2.DetectionRules.InsertRule(ctx, domain.DetectionRule{
		RuleID: "dr_v18_t", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("insert probe rule: %v", err)
	}
	for _, bad := range []string{"resolved", "open", ""} {
		if _, err := db2.sql.ExecContext(ctx,
			`INSERT INTO alerts (alert_id, rule_id, series_id, fired_at, state, last_value, first_seen)
VALUES ('al_bad_s','dr_v18_t',?,'2026-03-05T00:00:00Z',?,-15.2,'2026-03-02T00:00:00Z')`, seriesID, bad); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("insert alert state %q = %v, want CHECK constraint error", bad, err)
		}
	}

	// The metric_keys FK is enforced (unknown metric key rejected).
	if err := db2.DetectionRules.InsertRule(ctx, domain.DetectionRule{
		RuleID: "dr_v18_fk_key", MetricKey: "no_such_metric", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("insert rule with unknown metric_key = %v, want FOREIGN KEY constraint error", err)
	}
	// The rule FK is enforced on alerts.
	if _, err := db2.sql.ExecContext(ctx,
		`INSERT INTO alerts (alert_id, rule_id, series_id, fired_at, state) VALUES ('al_fk','dr_nope',?,'2026-01-01T00:00:00Z','firing')`, seriesID); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("insert alert with unknown rule = %v, want FOREIGN KEY constraint error", err)
	}
	// The series FK is enforced (audit-trail: no ON DELETE action).
	if _, err := db2.sql.ExecContext(ctx,
		`INSERT INTO alerts (alert_id, rule_id, series_id, fired_at, state) VALUES ('al_fk2','dr_v18_t','ser_nope','2026-01-01T00:00:00Z','firing')`); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("insert alert with unknown series = %v, want FOREIGN KEY constraint error", err)
	}

	// The repo round-trip: insert a firing alert on the populated
	// series, read it back through every read path.
	probe := domain.Alert{
		AlertID: "al_v18_probe", RuleID: "dr_v18_t", SeriesID: seriesID,
		FiredAt: "2026-03-05T00:00:00Z", State: domain.AlertStateFiring,
		LastValue: -15.2, FirstSeen: "2026-03-02T00:00:00Z",
	}
	if created, err := db2.Alerts.InsertFiringAlert(ctx, probe); err != nil || !created {
		t.Fatalf("InsertFiringAlert: created=%v err=%v", created, err)
	}
	if created, err := db2.Alerts.InsertFiringAlert(ctx, probe); err != nil || created {
		t.Errorf("re-InsertFiringAlert = created %v err %v, want created=false (idempotent)", created, err)
	}
	got, err := db2.Alerts.GetAlertByID(ctx, probe.AlertID)
	if err != nil || got.State != domain.AlertStateFiring || got.FirstSeen != probe.FirstSeen || got.LastValue != probe.LastValue {
		t.Errorf("GetAlertByID = %+v (err %v), want round-trip of %+v", got, err, probe)
	}
	active, err := db2.Alerts.ListActiveAlertsByRule(ctx, "dr_v18_t")
	if err != nil || len(active) != 1 || active[0].AlertID != probe.AlertID {
		t.Errorf("ListActiveAlertsByRule = %+v (err %v), want the probe alert", active, err)
	}
	// The state machine: ack then double-clear refused.
	if err := db2.Alerts.AckAlert(ctx, probe.AlertID, "2026-03-06T00:00:00Z", "op"); err != nil {
		t.Fatalf("AckAlert: %v", err)
	}
	if err := db2.Alerts.AckAlert(ctx, probe.AlertID, "2026-03-06T00:00:00Z", "op"); err == nil || !strings.Contains(err.Error(), "not firing") {
		t.Errorf("double AckAlert = %v, want not-firing conflict", err)
	}
	if err := db2.Alerts.ClearAlert(ctx, probe.AlertID, "2026-03-07T00:00:00Z"); err != nil {
		t.Fatalf("ClearAlert (acked→cleared): %v", err)
	}
	if err := db2.Alerts.ClearAlert(ctx, probe.AlertID, "2026-03-07T00:00:00Z"); err == nil || !strings.Contains(err.Error(), "already cleared") {
		t.Errorf("double ClearAlert = %v, want already-cleared conflict", err)
	}
	// UNIQUE (rule_id, series_id, first_seen): a second firing row of the
	// same identity is suppressed (INSERT OR IGNORE honors the business
	// key too, not just the primary key).
	reprobe := probe
	reprobe.AlertID = "al_v18_probe2"
	reprobe.State = domain.AlertStateFiring
	reprobe.ClearedAt = ""
	if created, err := db2.Alerts.InsertFiringAlert(ctx, reprobe); err != nil || created {
		t.Errorf("duplicate (rule,series,first_seen) insert = created %v err %v, want suppressed", created, err)
	}

	// The listing join shape: rule + series + resource in one row.
	rows, total, err := db2.Alerts.ListAlerts(ctx, AlertFilter{})
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("ListAlerts = %d rows (total %d), want 1", len(rows), total)
	}
	if rows[0].MetricKey != "optical_power_rx_avg_dbm" || rows[0].Severity != domain.SeverityWarning ||
		rows[0].ResourceID != portID || rows[0].ResourceType != "port" || rows[0].NaturalKey != "DEV-V18|GE-0/0/1" {
		t.Errorf("ListAlerts join projection = %+v, want rule+series+resource fields", rows[0])
	}

	// Blocked-delete: a rule with alerts refuses deletion (audit trail).
	if err := db2.DetectionRules.DeleteRule(ctx, "dr_v18_t"); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Errorf("DeleteRule with alerts = %v, want conflict (audit trail)", err)
	}
	// A rule WITHOUT alerts deletes cleanly.
	if err := db2.DetectionRules.InsertRule(ctx, domain.DetectionRule{
		RuleID: "dr_v18_del", MetricKey: "module_temp_avg_c", Comparator: domain.ComparatorGte,
		Threshold: 70.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("insert delete-candidate rule: %v", err)
	}
	if err := db2.DetectionRules.DeleteRule(ctx, "dr_v18_del"); err != nil {
		t.Errorf("DeleteRule without alerts = %v, want nil", err)
	}

	// Populated v17 data survived byte-identically.
	var nPoints int
	if err := db2.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM metrics`).Scan(&nPoints); err != nil {
		t.Fatalf("count points after v18: %v", err)
	}
	if nPoints != 5 {
		t.Errorf("points after v18 = %d, want 5 (data preserved)", nPoints)
	}
	var rx float64
	if err := db2.sql.QueryRowContext(ctx,
		`SELECT value FROM metrics WHERE series_id = ? AND ts = '2026-03-05T00:00:00Z'`, seriesID).Scan(&rx); err != nil || rx != -15.2 {
		t.Errorf("point value after v18 = %v (err %v), want -15.2", rx, err)
	}
	res, err := db2.Resources.GetByID(ctx, portID)
	if err != nil || res.NaturalKey != "DEV-V18|GE-0/0/1" || !res.UpdatedAt.Equal(v18TS) {
		t.Errorf("port after v18 = %+v (err %v), want byte-identical survival", res, err)
	}

	// The migration seeds NO default rules (data-not-schema policy):
	// after a genuine v17→v18 upgrade the table holds ONLY the rules the
	// test itself inserted — none of the default ids.
	rules, err := db2.DetectionRules.ListRules(ctx, false)
	if err != nil {
		t.Fatalf("ListRules after v18: %v", err)
	}
	var seeded int
	for _, r := range rules {
		for _, d := range DefaultDetectionRules {
			if r.RuleID == d.RuleID {
				seeded++
			}
		}
	}
	if seeded != 0 {
		t.Errorf("default rules found in the migration = %d, want 0 (lazy Go-side seeding only)", seeded)
	}

	// MAX(version) follows the registry head.
	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 34 {
		t.Errorf("max schema version after upgrade = %d, want 34", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op (a second CREATE TABLE
	// would fail on the duplicate table, so a clean reopen proves the
	// bookkeeping).
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if err := db3.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version after reopen: %v", err)
	}
	if maxV != 34 {
		t.Errorf("max schema version after reopen = %d, want 34", maxV)
	}
	if _, err := db3.Alerts.GetAlertByID(ctx, probe.AlertID); err != nil {
		t.Errorf("GetAlertByID after reopen: %v", err)
	}
}

// TestSeedDefaultDetectionRulesLazy pins the lazy seeding contract on a
// FRESH v18 database (empty detection_rules): the seed fills exactly the
// default set, is a no-op when re-called on a non-empty table, and a
// deleted rule does NOT come back (the guard is "table empty", not "row
// missing" — rules are user data, the migration must not resurrect them).
func TestSeedDefaultDetectionRulesLazy(t *testing.T) {
	db, err := Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	if err := db.DetectionRules.SeedDefaultDetectionRules(ctx); err != nil {
		t.Fatalf("SeedDefaultDetectionRules: %v", err)
	}
	seeded, err := db.DetectionRules.ListRules(ctx, false)
	if err != nil || len(seeded) != len(DefaultDetectionRules) {
		t.Fatalf("rules after lazy seed = %d (err %v), want %d", len(seeded), err, len(DefaultDetectionRules))
	}
	// The listing is ordered by rule_id (deterministic contract).
	want := DefaultDetectionRules[0]
	var gotRule *domain.DetectionRule
	for i := range seeded {
		if seeded[i].RuleID == want.RuleID {
			gotRule = &seeded[i]
		}
	}
	if gotRule == nil || gotRule.MetricKey != want.MetricKey || !gotRule.Enabled ||
		gotRule.Comparator != want.Comparator || gotRule.Threshold != want.Threshold ||
		gotRule.Window != want.Window || gotRule.Severity != want.Severity ||
		gotRule.Label != want.Label || gotRule.CreatedAt != want.CreatedAt {
		t.Errorf("seeded rule round-trip = %+v, want %+v", gotRule, want)
	}
	// Delete one, re-seed: the deleted rule stays deleted.
	if err := db.DetectionRules.DeleteRule(ctx, "dr_loss_rx"); err != nil {
		t.Fatalf("delete a seeded rule: %v", err)
	}
	if err := db.DetectionRules.SeedDefaultDetectionRules(ctx); err != nil {
		t.Fatalf("second SeedDefaultDetectionRules: %v", err)
	}
	after, err := db.DetectionRules.ListRules(ctx, false)
	if err != nil || len(after) != len(DefaultDetectionRules)-1 {
		t.Errorf("rules after delete + re-seed = %d (err %v), want %d (deleted rule stays deleted — table non-empty guard)",
			len(after), err, len(DefaultDetectionRules)-1)
	}
}
