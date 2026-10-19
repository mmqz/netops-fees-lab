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

// withoutV21 temporarily removes migration v21 (and any later) from the
// registry so the next Open builds a GENUINE v20 database (the v0.10.0
// head: detection tables live, no run history). Restored via t.Cleanup;
// the removed tail is returned for the full-registry reopen (the
// withoutV18/withoutV19 pattern).
func withoutV21(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 20 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

// v21TS is the fixed clock of this file's seeds (no clock in the data
// path — the repo test convention).
var v21TS = time.Date(2026, 3, 10, 8, 0, 0, 0, time.UTC)

// TestMigrationV21AddsDetectionRuns pins the run-history store (v0.11-R11):
//
//   - on a genuine v20 database (v21 temporarily unregistered) any receipt
//     insert fails with "no such table" — the regression the migration
//     exists to fix;
//   - re-registering v21 and reopening the SAME file applies it: the table
//     exists, the closed source CHECK rejects out-of-set origins (the
//     storage-side twin of the MCP forbidden-name pin), the repo
//     round-trips (insert / newest-first listing / pagination / total),
//     and MAX(version) == 21;
//   - a third Open is a bookkeeping no-op (idempotency);
//   - the migration seeds NOTHING: run receipts are born from real sweeps,
//     not from schema replay.
func TestMigrationV21AddsDetectionRuns(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v20 database.
	tail := withoutV21(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v20: %v", err)
	}
	// Pre-migration regression: the table does not exist.
	if _, err := db1.sql.ExecContext(ctx,
		`INSERT INTO detection_runs (id, source, started_at, finished_at, duration_ms)
VALUES ('run_t','api','2026-03-10T08:00:00Z','2026-03-10T08:00:01Z',1000)`); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Errorf("insert detection_runs at v20 = %v, want \"no such table\" error", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v20 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v21 applies.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v20 → v21): %v", err)
	}
	defer db2.Close()

	// The closed source enum is enforced by CHECK — the MCP face cannot
	// even be persisted as an origin.
	for _, bad := range []string{"mcp", "cron", "http", ""} {
		if _, err := db2.sql.ExecContext(ctx,
			`INSERT INTO detection_runs (id, source, started_at, finished_at, duration_ms)
VALUES ('run_bad','?','2026-03-10T08:00:00Z','2026-03-10T08:00:01Z',1)`, bad); err == nil || !strings.Contains(err.Error(), "CHECK") {
			t.Errorf("insert source %q = %v, want CHECK constraint error", bad, err)
		}
	}

	// The repo round-trip: three receipts, newest started first.
	for i, src := range []domain.RunSource{domain.RunSourceAPI, domain.RunSourceScheduler, domain.RunSourceCLI} {
		run := domain.DetectionRun{
			RunID: "run_v21_" + src.String(), Source: src,
			StartedAt:  v21TS.Add(time.Duration(2-i) * time.Hour).UTC().Format(time.RFC3339),
			FinishedAt: v21TS.Add(time.Duration(2-i)*time.Hour + 1500*time.Millisecond).UTC().Format(time.RFC3339),
			DurationMS: 1500, EvaluatedAt: "2026-03-09T00:00:00Z",
			Findings: 3, AlertsCreated: 2, AlertsCleared: 1, RunStatesWritten: 2,
		}
		if err := db2.DetectionRuns.InsertRun(ctx, run); err != nil {
			t.Fatalf("insert run %d: %v", i, err)
		}
	}
	runs, total, err := db2.DetectionRuns.ListRuns(ctx, 2, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if total != 3 {
		t.Errorf("runs total = %d, want 3", total)
	}
	if len(runs) != 2 {
		t.Fatalf("page size = %d runs, want 2", len(runs))
	}
	// Newest started first: the API run (started +2h) leads.
	if runs[0].Source != domain.RunSourceAPI || runs[1].Source != domain.RunSourceScheduler {
		t.Errorf("run order = %s, %s; want api, scheduler (newest first)", runs[0].Source, runs[1].Source)
	}
	if runs[0].DurationMS != 1500 || runs[0].EvaluatedAt != "2026-03-09T00:00:00Z" || runs[0].Error != "" {
		t.Errorf("run projection = %+v, want duration/evaluatedAt/error round-tripped", runs[0])
	}
	// Page 2 carries the CLI run.
	runs2, _, err := db2.DetectionRuns.ListRuns(ctx, 2, 2)
	if err != nil {
		t.Fatalf("list runs page 2: %v", err)
	}
	if len(runs2) != 1 || runs2[0].Source != domain.RunSourceCLI {
		t.Errorf("page 2 = %+v, want the single cli run", runs2)
	}

	// A failed run round-trips with its error text (failure auditing).
	failRun := domain.DetectionRun{
		RunID: "run_v21_failed", Source: domain.RunSourceScheduler,
		StartedAt:  v21TS.Add(6 * time.Hour).UTC().Format(time.RFC3339),
		FinishedAt: v21TS.Add(6*time.Hour + 40*time.Millisecond).UTC().Format(time.RFC3339),
		DurationMS: 40, Error: "detect: engine boom",
	}
	if err := db2.DetectionRuns.InsertRun(ctx, failRun); err != nil {
		t.Fatalf("insert failed run: %v", err)
	}
	all, total, err := db2.DetectionRuns.ListRuns(ctx, 0, 0)
	if err != nil {
		t.Fatalf("list all runs: %v", err)
	}
	if total != 4 || len(all) != 4 {
		t.Fatalf("all runs = %d (total %d), want 4", len(all), total)
	}
	if all[0].RunID != "run_v21_failed" || all[0].Error != "detect: engine boom" {
		t.Errorf("failed run = %+v, want error text round-tripped and newest-first", all[0])
	}

	// MAX(version) follows the registry head.
	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 29 {
		t.Errorf("max schema version after upgrade = %d, want 29", maxV)
	}

	// Phase 3: reopening is a bookkeeping no-op — the receipt rows
	// survive byte-identically.
	db3, err := Open(path)
	if err != nil {
		t.Fatalf("third Open (idempotency): %v", err)
	}
	defer db3.Close()
	if _, total, err := db3.DetectionRuns.ListRuns(ctx, 0, 0); err != nil || total != 4 {
		t.Errorf("runs after reopen: total=%d err=%v, want 4 / nil", total, err)
	}
}

// TestAlertStatsMatrix pins the AlertRepo.AlertStats GROUP BY: the state ×
// severity matrix over ALL alerts — the backend of GET /api/alerts/stats.
func TestAlertStatsMatrix(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Empty database: zero rows, not an error.
	rows, err := db.Alerts.AlertStats(ctx)
	if err != nil {
		t.Fatalf("stats on empty db: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("stats on empty db = %d rows, want 0", len(rows))
	}

	// Seed: one rule + one series + three alerts (firing/warning,
	// acked/error, cleared/warning).
	portID := "res_stats_port"
	if err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_stats", v21TS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: portID, Type: domain.ResourceTypePort, NaturalKey: "DEV-ST|GE-0/0/1",
			Name: "统计端口", Attributes: map[string]string{}, FirstBatchID: "bat_stats",
			CreatedAt: v21TS, UpdatedAt: v21TS,
		})
	}); err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	seriesID, _, err := db.Metrics.FindOrCreateSeries(ctx, portID, "optical_power_rx_avg_dbm",
		nil, "2026-03-01T00:00:00Z", "2026-03-05T00:00:00Z")
	if err != nil {
		t.Fatalf("seed series: %v", err)
	}
	if err := db.DetectionRules.InsertRule(ctx, domain.DetectionRule{
		RuleID: "dr_stats_w", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed warning rule: %v", err)
	}
	if err := db.DetectionRules.InsertRule(ctx, domain.DetectionRule{
		RuleID: "dr_stats_e", MetricKey: "packet_loss_rx_pct", Comparator: domain.ComparatorGte,
		Threshold: 1.0, Window: 5, Severity: domain.SeverityError, Enabled: true,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed error rule: %v", err)
	}
	// A packet-loss series for the error rule's alert.
	plSeriesID, _, err := db.Metrics.FindOrCreateSeries(ctx, portID, "packet_loss_rx_pct",
		nil, "2026-03-01T00:00:00Z", "2026-03-05T00:00:00Z")
	if err != nil {
		t.Fatalf("seed packet-loss series: %v", err)
	}
	if err := db.MetricBatches.Create(ctx, domain.MetricBatch{
		ID: "mbat_stats", WorkbookName: "stats.xlsx",
		WorkbookSHA256: "1111111111111111111111111111111111111111111111111111111111111111",
		Status:         domain.BatchStatusCompleted, RowsTotal: 10, RowsImported: 10,
		StartedAt: v21TS,
	}); err != nil {
		t.Fatalf("seed metric batch: %v", err)
	}
	if err := db.Metrics.InsertPoints(ctx, []domain.MetricPoint{
		{SeriesID: seriesID, TS: "2026-03-02T00:00:00Z", Value: -15.0, Quality: domain.PointQualityOk, BatchID: "mbat_stats"},
		{SeriesID: seriesID, TS: "2026-03-03T00:00:00Z", Value: -15.4, Quality: domain.PointQualityOk, BatchID: "mbat_stats"},
		{SeriesID: seriesID, TS: "2026-03-04T00:00:00Z", Value: -15.8, Quality: domain.PointQualityOk, BatchID: "mbat_stats"},
	}); err != nil {
		t.Fatalf("seed points: %v", err)
	}

	// Three lifecycle states across two severities.
	if created, err := db.Alerts.InsertFiringAlert(ctx, domain.Alert{
		AlertID: "al_st_1", RuleID: "dr_stats_w", SeriesID: seriesID,
		FiredAt: "2026-03-04T00:00:00Z", State: domain.AlertStateFiring,
		LastValue: -15.8, FirstSeen: "2026-03-02T00:00:00Z",
	}); err != nil || !created {
		t.Fatalf("insert firing alert: created=%v err=%v", created, err)
	}
	if err := db.Alerts.AckAlert(ctx, "al_st_2_ackee", "2026-03-05T00:00:00Z", "op"); err == nil {
		t.Fatal("ack unknown alert should fail")
	}
	// Seed acked + cleared alerts directly (the lifecycle edges are the
	// engine's business; the stats matrix only reads).
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO alerts (alert_id, rule_id, series_id, fired_at, state, last_value, first_seen, acked_at, acked_by)
VALUES ('al_st_2', 'dr_stats_e', ?, '2026-03-04T00:00:00Z', 'acked', 2.5, '2026-03-01T00:00:00Z', '2026-03-05T00:00:00Z', 'op')`, plSeriesID); err != nil {
		t.Fatalf("seed acked alert: %v", err)
	}
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO alerts (alert_id, rule_id, series_id, fired_at, state, last_value, first_seen, cleared_at)
VALUES ('al_st_3', 'dr_stats_w', ?, '2026-03-03T00:00:00Z', 'cleared', -15.4, '2026-03-01T00:00:00Z', '2026-03-04T00:00:00Z')`, seriesID); err != nil {
		t.Fatalf("seed cleared alert: %v", err)
	}

	rows, err = db.Alerts.AlertStats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	want := map[domain.AlertState]map[domain.Severity]int{
		domain.AlertStateFiring:  {domain.SeverityWarning: 1},
		domain.AlertStateAcked:   {domain.SeverityError: 1},
		domain.AlertStateCleared: {domain.SeverityWarning: 1},
	}
	if len(rows) != 3 {
		t.Fatalf("stats rows = %d (%+v), want 3", len(rows), rows)
	}
	for _, row := range rows {
		if want[row.State] == nil || want[row.State][row.Severity] != row.Count {
			t.Errorf("stats cell (%s, %s) = %d, want %d", row.State, row.Severity, row.Count, want[row.State][row.Severity])
		}
	}
}
