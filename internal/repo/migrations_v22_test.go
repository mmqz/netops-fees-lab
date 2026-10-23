package repo

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

// TestMigrationV22HotPathIndexes pins the R14 audit outcome:
//   - the alerts state filter + fired_at ordering rides the new
//     idx_alerts_state_fired (EXPLAIN QUERY PLAN names it);
//   - the metrics series window read is ALREADY optimal — the v14 table
//     is WITHOUT ROWID with PRIMARY KEY (series_id, ts) and the plan
//     SEARCHes the primary key (the redundant index is deliberately not
//     created; this assertion is the pin);
//   - the listing still returns the seeded alert through the repo path
//     (a new index must not change results, only plans).
func TestMigrationV22HotPathIndexes(t *testing.T) {
	dir := testsupport.TempDir(t)
	path := filepath.Join(dir, "netops.db")
	ctx := context.Background()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// Seed one series + alert so the plans are meaningful (the planner
	// still names indexes on empty tables, but the regression is clearer
	// with data).
	if err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_v22", v21TS)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_v22", Type: domain.ResourceTypePort, NaturalKey: "DEV-V22|GE-0/0/1",
			Name: "v22端口", Attributes: map[string]string{}, FirstBatchID: "bat_v22",
			CreatedAt: v21TS, UpdatedAt: v21TS,
		})
	}); err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	sid, _, err := db.Metrics.FindOrCreateSeries(ctx, "res_v22", "optical_power_rx_avg_dbm",
		nil, "2026-03-01T00:00:00Z", "2026-03-05T00:00:00Z")
	if err != nil {
		t.Fatalf("seed series: %v", err)
	}
	if err := db.DetectionRules.InsertRule(ctx, domain.DetectionRule{
		RuleID: "dr_v22", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	if _, err := db.Alerts.InsertFiringAlert(ctx, domain.Alert{
		AlertID: "al_v22", RuleID: "dr_v22", SeriesID: sid,
		FiredAt: "2026-03-04T00:00:00Z", State: domain.AlertStateFiring,
		LastValue: -15.2, FirstSeen: "2026-03-02T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed alert: %v", err)
	}

	// The index exists (sqlite_master); the NOT-created one is absent.
	for name, want := range map[string]int{
		"idx_alerts_state_fired": 1,
		// The audit verdict: metrics needs NO extra index.
		"idx_metrics_series_ts": 0,
	} {
		var n int
		if err := db.sql.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&n); err != nil {
			t.Fatalf("probe %s: %v", name, err)
		}
		if n != want {
			t.Errorf("index %s rows = %d, want %d", name, n, want)
		}
	}

	// EXPLAIN QUERY PLAN helper.
	plan := func(t *testing.T, query string, args ...any) string {
		t.Helper()
		rows, err := db.sql.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatalf("explain: %v", err)
		}
		defer rows.Close()
		var sb strings.Builder
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				t.Fatalf("scan plan: %v", err)
			}
			sb.WriteString(detail + "\n")
		}
		return sb.String()
	}

	// The alerts state queue rides the NEW index.
	alertsPlan := plan(t, `SELECT alert_id FROM alerts WHERE state = ? ORDER BY fired_at DESC`, "firing")
	if !strings.Contains(alertsPlan, "idx_alerts_state_fired") {
		t.Errorf("alerts state plan = %q, want idx_alerts_state_fired", alertsPlan)
	}

	// The metrics window read is already optimal on the v14 PK — the
	// audit's "add (series_id, ts)" instinct was wrong, and this pin
	// keeps it wrong on purpose (no redundant write tax).
	metricsPlan := plan(t, `SELECT ts, value FROM metrics WHERE series_id = ? AND ts >= ? ORDER BY ts`, sid, "2026-03-01T00:00:00Z")
	if !strings.Contains(metricsPlan, "PRIMARY KEY") {
		t.Errorf("metrics series plan = %q, want the WITHOUT ROWID primary key scan", metricsPlan)
	}

	// The listing still returns the seeded alert through the repo path.
	alerts, total, err := db.Alerts.ListAlerts(ctx, AlertFilter{State: "firing", Limit: 10})
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if total != 1 || len(alerts) != 1 || alerts[0].AlertID != "al_v22" {
		t.Errorf("ListAlerts after v22 = %d rows (total %d), want the seeded firing alert", len(alerts), total)
	}
}
