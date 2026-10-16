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

// withoutV25 temporarily removes migration v25 (and any later) from the
// registry so the next Open builds a genuine v24 database (the v0.12-R15
// head: maintenance windows present, alerts without correlation columns).
func withoutV25(t *testing.T) []Migration {
	t.Helper()
	restored := append([]Migration(nil), migrations...)
	filtered := make([]Migration, 0, len(migrations))
	var removed []Migration
	for _, m := range migrations {
		if m.Version <= 24 {
			filtered = append(filtered, m)
		} else {
			removed = append(removed, m)
		}
	}
	migrations = filtered
	t.Cleanup(func() { migrations = restored })
	return removed
}

// v25Setup seeds the FK anchors (rule, resource, series) once and
// returns the real series id; v25InsertAlert pins one alert row on it.
func v25Setup(t *testing.T, db *DB) string {
	t.Helper()
	ctx := context.Background()
	if err := db.DetectionRules.InsertRule(ctx, domain.DetectionRule{
		RuleID: "dr_v25", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14, Window: 1, Severity: domain.SeverityWarning, Enabled: true,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	if err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, domain.ImportBatch{
			ID: "bat_v25", WorkbookName: "v25.xlsx",
			WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
			Status:         domain.BatchStatusCompleted, RuleVersion: "seed", StartedAt: v25TS,
		}); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_v25", Type: domain.ResourceTypePort, NaturalKey: "DEV-V25|GE-0/0/1",
			Name: "v25 端口", Attributes: map[string]string{}, FirstBatchID: "bat_v25",
			CreatedAt: v25TS, UpdatedAt: v25TS,
		})
	}); err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	sid, _, err := db.Metrics.FindOrCreateSeries(ctx, "res_v25", "optical_power_rx_avg_dbm",
		nil, "2026-03-01T00:00:00Z", "2026-03-05T00:00:00Z")
	if err != nil {
		t.Fatalf("seed series: %v", err)
	}
	return sid
}

// v25InsertAlert pins one alert row on the seeded series (the lifecycle
// engine tests live in internal/detect; here we pin the COLUMNS — the
// UNIQUE (rule, series, first_seen) identity forces distinct first_seen
// per row).
func v25InsertAlert(t *testing.T, db *DB, id, seriesID, firstSeen string) {
	t.Helper()
	if _, err := db.sql.ExecContext(context.Background(),
		`INSERT INTO alerts (alert_id, rule_id, series_id, fired_at, state, last_value, first_seen)
VALUES (?, 'dr_v25', ?, ?, 'firing', -20.0, ?)`, id, seriesID, firstSeen, firstSeen); err != nil {
		t.Fatalf("seed alert %s: %v", id, err)
	}
}

// v25TS is the fixed clock of this file's seeds.
var v25TS = time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)

// TestMigrationV25AddsAlertCorrelation pins the correlation columns
// (v0.12-R16): pre-migration the verdict write fails with "no such
// column" — the regression the migration fixes; after the upgrade the
// columns round-trip, the CHECK rejects out-of-set roles, existing rows
// backfill to unmarked, and MAX(version) == 25.
func TestMigrationV25AddsAlertCorrelation(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	// Phase 1: genuine v24 database.
	tail := withoutV25(t)
	db1, err := Open(path)
	if err != nil {
		t.Fatalf("open at v24: %v", err)
	}
	// Pre-migration regression: the correlation write has no column.
	if _, err := db1.sql.ExecContext(ctx,
		`UPDATE alerts SET correlation_role = 'primary' WHERE alert_id = 'x'`); err == nil || !strings.Contains(err.Error(), "no such column") {
		t.Errorf("set correlation_role at v24 = %v, want \"no such column\" error", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close v24 db: %v", err)
	}

	// Phase 2: restore the full registry and reopen — v25 applies.
	migrations = append(append([]Migration(nil), migrations...), tail...)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (v24 → v25): %v", err)
	}
	defer db2.Close()

	seedV25 := v25Setup(t, db2)
	v25InsertAlert(t, db2, "al_v25_a", seedV25, "2026-04-01T00:00:00Z")
	v25InsertAlert(t, db2, "al_v25_b", seedV25, "2026-04-02T00:00:00Z")

	// Existing rows backfill to unmarked (the verdict arrives with the
	// next detect run — history never rewrites itself).
	a, err := db2.Alerts.GetAlertByID(ctx, "al_v25_a")
	if err != nil {
		t.Fatalf("get alert: %v", err)
	}
	if a.CorrelationRole != "" || a.CorrelatedAlertID != "" || a.CorrelatedResourceID != "" {
		t.Errorf("backfilled alert = %+v, want unmarked correlation", a)
	}

	// The repo round-trip: secondary with the cause link, primary, reset.
	if err := db2.Alerts.UpdateAlertCorrelation(ctx, "al_v25_a", "secondary", "al_v25_b", "res_v25_cause"); err != nil {
		t.Fatalf("mark secondary: %v", err)
	}
	if err := db2.Alerts.UpdateAlertCorrelation(ctx, "al_v25_b", "primary", "", ""); err != nil {
		t.Fatalf("mark primary: %v", err)
	}
	a, err = db2.Alerts.GetAlertByID(ctx, "al_v25_a")
	if err != nil {
		t.Fatalf("re-get alert: %v", err)
	}
	if a.CorrelationRole != "secondary" || a.CorrelatedAlertID != "al_v25_b" || a.CorrelatedResourceID != "res_v25_cause" {
		t.Errorf("secondary mark = %+v, want role/cause round-tripped", a)
	}
	b, err := db2.Alerts.GetAlertByID(ctx, "al_v25_b")
	if err != nil {
		t.Fatalf("re-get alert b: %v", err)
	}
	if b.CorrelationRole != "primary" || b.CorrelatedAlertID != "" {
		t.Errorf("primary mark = %+v, want role without link", b)
	}

	// The listing filter selects by role (the closed-enum equality).
	rows, total, err := db2.Alerts.ListAlerts(ctx, AlertFilter{Role: "secondary", Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list by role: %v", err)
	}
	if total != 1 || len(rows) != 1 || rows[0].AlertID != "al_v25_a" {
		t.Errorf("role filter = total %d rows %+v, want only al_v25_a", total, rows)
	}

	// The CHECK rejects out-of-set roles at the storage level (the domain
	// parse is the front gate, the constraint the backstop).
	if _, err := db2.sql.ExecContext(ctx,
		`UPDATE alerts SET correlation_role = 'bogus' WHERE alert_id = 'al_v25_a'`); err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Errorf("bogus correlation_role = %v, want CHECK constraint error", err)
	}

	var maxV int
	if err := db2.sql.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&maxV); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if maxV != 27 {
		t.Errorf("max schema version after upgrade = %d, want 27", maxV)
	}
}

// TestParseCorrelationRole pins the closed role set at the domain gate.
func TestParseCorrelationRole(t *testing.T) {
	for _, ok := range []string{"", "primary", "secondary"} {
		if _, err := domain.ParseCorrelationRole(ok); err != nil {
			t.Errorf("ParseCorrelationRole(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"root", "cause", "Primary", "info"} {
		if _, err := domain.ParseCorrelationRole(bad); err == nil {
			t.Errorf("ParseCorrelationRole(%q) = nil error, want rejection", bad)
		}
	}
}
