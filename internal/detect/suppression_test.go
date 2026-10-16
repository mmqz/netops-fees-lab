package detect

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/testsupport"
)

// TestSuppressedWindowVerdict pins the pure suppression predicate: the
// half-open [starts, ends) interval on the DATA timeline, the resource
// scope (nil = global), and the never-suppress-on-parse-miss fallback.
func TestSuppressedWindowVerdict(t *testing.T) {
	start := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	global := SuppressedWindow{StartsAfter: start, EndsBefore: end}
	scoped := SuppressedWindow{StartsAfter: start, EndsBefore: end,
		ResourceIDs: map[string]bool{"res_a": true}}

	cases := []struct {
		name string
		w    SuppressedWindow
		res  string
		ts   string
		want bool
	}{
		{"global in window", global, "res_x", "2026-03-06T00:00:00Z", true},
		{"ts at starts boundary", global, "res_x", "2026-03-05T00:00:00Z", true},
		{"ts at ends boundary", global, "res_x", "2026-03-10T00:00:00Z", false},
		{"ts before window", global, "res_x", "2026-03-04T23:59:59Z", false},
		{"ts after window", global, "res_x", "2026-03-10T00:00:01Z", false},
		{"scoped in scope", scoped, "res_a", "2026-03-06T00:00:00Z", true},
		{"scoped out of scope", scoped, "res_b", "2026-03-06T00:00:00Z", false},
		{"non-RFC3339 never suppresses", global, "res_x", "not-a-timestamp", false},
		{"offset ts still in window", global, "res_x", "2026-03-06T08:00:00+08:00", true},
	}
	for _, tc := range cases {
		if got := tc.w.Suppresses(tc.res, tc.ts); got != tc.want {
			t.Errorf("%s: Suppresses(%s, %s) = %v, want %v", tc.name, tc.res, tc.ts, got, tc.want)
		}
	}
}

// seedWindow inserts one maintenance window row through the repo (the
// engine's own read path).
func seedWindow(t *testing.T, db *repo.DB, id string, scope []string) {
	t.Helper()
	win := domain.MaintenanceWindow{
		WindowID: id, Label: "测试窗口 " + id,
		StartsAt: "2026-03-05T00:00:00Z", EndsAt: "2026-03-10T00:00:00Z",
		ResourceIDs: scope, CreatedAt: "2026-01-01T00:00:00Z",
	}
	if err := db.Maintenance.InsertWindow(context.Background(), win); err != nil {
		t.Fatalf("seed window %s: %v", id, err)
	}
}

// appendPoints adds more points to an EXISTING series (seedPoints' one
// batch per call shape — the per-call batch id keeps re-seeding a series
// collision-free).
func appendPoints(t *testing.T, db *repo.DB, seriesID, day0 string, values []float64) {
	t.Helper()
	base, err := time.Parse(time.RFC3339, day0)
	if err != nil {
		t.Fatalf("bad day0 %q: %v", day0, err)
	}
	batchID := "mbat_" + seriesID + "_" + base.Format("20060102T150405")
	if err := db.MetricBatches.Create(context.Background(), domain.MetricBatch{
		ID: batchID, WorkbookName: "detect.xlsx",
		WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Status:         domain.BatchStatusCompleted, RowsTotal: len(values), RowsImported: len(values),
		StartedAt: detectTS,
	}); err != nil {
		t.Fatalf("seed metric batch: %v", err)
	}
	pts := make([]domain.MetricPoint, 0, len(values))
	for i, v := range values {
		pts = append(pts, domain.MetricPoint{
			SeriesID: seriesID, TS: base.AddDate(0, 0, i).Format(time.RFC3339),
			Value: v, Quality: domain.PointQualityOk, BatchID: batchID,
		})
	}
	if err := db.Metrics.InsertPoints(context.Background(), pts); err != nil {
		t.Fatalf("seed points: %v", err)
	}
}

// TestDetectMaintenanceSuppression pins the engine's suppression contract
// end to end against a real database:
//
//   - a NEW fire whose evidence ts (LastTS, from the data) falls inside a
//     window covering the resource → NO alert row, counted in Suppressed,
//     listed with action="suppressed" carrying the DETERMINISTIC would-be
//     id, and NO run-state write (a resource under planned maintenance is
//     not "down");
//   - an out-of-scope resource fires normally (the window discriminates);
//   - after the window is deleted, the still-violating series fires with
//     the SAME alert id the suppressed line promised — correlatable;
//   - an alert that already fired keeps its lifecycle inside the window:
//     evidence refresh continues, no second alert;
//   - recovery continues during a window: an active alert whose series
//     stopped violating is cleared even on a scoped resource.
func TestDetectMaintenanceSuppression(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	eng := New()

	seedResource(t, db, "res_sup_a", "DEV-SUP|GE-0/0/1")
	seedResource(t, db, "res_sup_b", "DEV-SUP|GE-0/0/2")
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_sup", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
	})
	sidA := seedSeries(t, db, "res_sup_a", "optical_power_rx_avg_dbm")
	sidB := seedSeries(t, db, "res_sup_b", "optical_power_rx_avg_dbm")
	// 7 violating points (Mar 1..7): the trailing run's first_seen stays
	// Mar 1 across the whole test — the would-be id is stable.
	seedPoints(t, db, sidA, "2026-03-01T00:00:00Z", []float64{-16, -16.5, -17, -17.5, -18, -18.5, -19}, domain.PointQualityOk)
	seedPoints(t, db, sidB, "2026-03-01T00:00:00Z", []float64{-16, -16.5, -17, -17.5, -18, -18.5, -19}, domain.PointQualityOk)

	// Window covers [Mar 5, Mar 10) scoped to resource A only — A's
	// newest evidence (Mar 7) is inside, B is out of scope.
	seedWindow(t, db, "mw_sup", []string{"res_sup_a"})

	res, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect with window: %v", err)
	}
	// Both series violate; only A's fire is suppressed.
	if res.Findings != 2 {
		t.Errorf("findings = %d, want 2", res.Findings)
	}
	if res.AlertsCreated != 1 || res.Suppressed != 1 {
		t.Errorf("created=%d suppressed=%d, want 1/1", res.AlertsCreated, res.Suppressed)
	}
	alerts := activeAlertsOf(t, db, "dr_sup")
	if len(alerts) != 1 || alerts[0].SeriesID != sidB {
		t.Fatalf("active alerts = %+v, want only series B firing", alerts)
	}
	// The suppressed line: action, empty state, the would-be id.
	var supLine *AlertSummary
	for i := range res.Alerts {
		if res.Alerts[i].Action == "suppressed" {
			supLine = &res.Alerts[i]
		}
	}
	if supLine == nil {
		t.Fatalf("no suppressed line in envelope: %+v", res.Alerts)
	}
	if supLine.SeriesID != sidA || supLine.State != "" || supLine.FirstSeen != "2026-03-01T00:00:00Z" {
		t.Errorf("suppressed line = %+v, want series A / empty state / firstSeen Mar 1", supLine)
	}
	wantID := NewAlertID("dr_sup", sidA, "2026-03-01T00:00:00Z")
	if supLine.AlertID != wantID {
		t.Errorf("suppressed would-be id = %s, want %s (deterministic identity)", supLine.AlertID, wantID)
	}
	// Suppressed findings write no run-state: resource A stays unwritten.
	states, err := db.ResourceStatus.ListHistory(ctx, "res_sup_a", "", "")
	if err != nil {
		t.Fatalf("list run-states of A: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("run-states of suppressed resource = %d, want 0", len(states))
	}

	// Delete the window → same data, next sweep fires A with the PROMISED id.
	if err := db.Maintenance.DeleteWindow(ctx, "mw_sup"); err != nil {
		t.Fatalf("delete window: %v", err)
	}
	res2, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect after window delete: %v", err)
	}
	if res2.AlertsCreated != 1 || res2.Suppressed != 0 {
		t.Errorf("second sweep created=%d suppressed=%d, want 1/0", res2.AlertsCreated, res2.Suppressed)
	}
	alerts = activeAlertsOf(t, db, "dr_sup")
	if len(alerts) != 2 {
		t.Fatalf("active alerts after window delete = %d, want 2", len(alerts))
	}
	var alertA *domain.Alert
	for i := range alerts {
		if alerts[i].SeriesID == sidA {
			alertA = &alerts[i]
		}
	}
	if alertA == nil || alertA.AlertID != wantID {
		t.Errorf("series A alert id = %+v, want the suppressed line's id %s", alertA, wantID)
	}

	// Idempotent re-run over unchanged data: 0/0 mutations, 0 suppressed.
	res3, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("idempotent re-run: %v", err)
	}
	if res3.AlertsCreated != 0 || res3.AlertsCleared != 0 || res3.Suppressed != 0 {
		t.Errorf("re-run = created %d / cleared %d / suppressed %d, want 0/0/0", res3.AlertsCreated, res3.AlertsCleared, res3.Suppressed)
	}

	// A GLOBAL window covering the newest evidence: existing firing alerts
	// keep their lifecycle (no delete, no second row), the evidence refresh
	// continues — suppression only gates NEW fires.
	seedWindow(t, db, "mw_global", nil)
	appendPoints(t, db, sidA, "2026-03-08T00:00:00Z", []float64{-20, -21})
	res4, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect with global window: %v", err)
	}
	if res4.Suppressed != 0 {
		t.Errorf("global window suppressed = %d, want 0 (no NEW fires: both alerts exist)", res4.Suppressed)
	}
	alerts = activeAlertsOf(t, db, "dr_sup")
	if len(alerts) != 2 {
		t.Fatalf("alerts during global window = %d, want 2 (lifecycle untouched)", len(alerts))
	}
	var refreshed *domain.Alert
	for i := range alerts {
		if alerts[i].SeriesID == sidA {
			refreshed = &alerts[i]
		}
	}
	if refreshed == nil || refreshed.FiredAt != "2026-03-09T00:00:00Z" {
		t.Errorf("evidence refresh during window = %+v, want firedAt Mar 9", refreshed)
	}

	// Recovery continues during the window: series B stops violating —
	// its alert clears even though the global window covers the resource.
	appendPoints(t, db, sidB, "2026-03-10T12:00:00Z", []float64{-1, -1})
	res5, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect recovery during window: %v", err)
	}
	if res5.AlertsCleared != 1 {
		t.Errorf("recovery during window cleared = %d, want 1 (good news is never suppressed)", res5.AlertsCleared)
	}
}

// TestDetectSuppressedDeterminism pins the replay doctrine: the same
// database state + same windows → the same envelope, twice, byte-for-byte
// in every field the envelope carries (the suppression path adds no
// hidden ordering).
func TestDetectSuppressedDeterminism(t *testing.T) {
	run := func() RunResult {
		db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
		if err != nil {
			t.Fatalf("repo.Open: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		seedResource(t, db, "res_det", "DEV-DET|GE-0/0/9")
		insertRule(t, db, domain.DetectionRule{
			RuleID: "dr_det", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
			Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
		})
		sid := seedSeries(t, db, "res_det", "optical_power_rx_avg_dbm")
		seedPoints(t, db, sid, "2026-03-01T00:00:00Z", []float64{-16, -17, -18, -19}, domain.PointQualityOk)
		seedWindow(t, db, "mw_det", nil)
		res, err := New().Detect(context.Background(), db, Options{})
		if err != nil {
			t.Fatalf("detect: %v", err)
		}
		return res
	}
	a, b := run(), run()
	if a.Findings != b.Findings || a.AlertsCreated != b.AlertsCreated || a.Suppressed != b.Suppressed || a.EvaluatedAt != b.EvaluatedAt {
		t.Errorf("suppression determinism broken: %+v vs %+v", a, b)
	}
	if len(a.Alerts) != len(b.Alerts) || len(a.Alerts) != 1 {
		t.Fatalf("envelope lines = %d / %d, want 1 each", len(a.Alerts), len(b.Alerts))
	}
	// Series ids (and therefore the ids derived from them) are generated
	// per database; the determinism contract pins every DATA-derived
	// field of the line — the same normalization the lifecycle
	// determinism test applies.
	norm := func(s AlertSummary) AlertSummary {
		s.SeriesID, s.AlertID, s.ResourceID = "", "", ""
		return s
	}
	if norm(a.Alerts[0]) != norm(b.Alerts[0]) {
		t.Errorf("suppressed line differs across replays: %+v vs %+v", norm(a.Alerts[0]), norm(b.Alerts[0]))
	}
}
