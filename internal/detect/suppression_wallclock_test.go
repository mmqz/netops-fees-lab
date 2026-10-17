package detect

// suppression_wallclock_test.go — issue #55 contract tests: the DUAL time
// basis of maintenance-window suppression. Windows are human plans written
// against the WALL clock; the sweep evaluates on the DATA timeline. The
// three controlled experiments of the issue map to:
//
//   - A (no window)         → created=1, suppressed=0           [engine_test baseline]
//   - B (wall-clock window, → BEFORE v0.12.1: silently created;  THIS FILE:
//     historical data)        AFTER: suppressed=1, basis=wall_clock
//   - C (data-time window)  → suppressed=1, basis=data_time      (below + legacy test)
//
// Plus the silent-failure eliminator (RunResult.SuppressionNote): a run
// with wall-ACTIVE windows that suppressed nothing while creating alerts
// names both clocks, so an operator can see the mismatch instead of
// believing a plan is protecting them. And the backward-compat pin: a
// zero Options.WallClock keeps pure data-time matching (the pre-v0.12.1
// engine shape the deterministic tests rely on).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// seedWallWindow inserts one maintenance window whose [starts, ends) sits
// on the OPERATIONAL timeline (October) while the test data lives in
// March — the exact replay shape of issue #55.
func seedWallWindow(t *testing.T, db *repo.DB, id string, scope []string) {
	t.Helper()
	win := domain.MaintenanceWindow{
		WindowID: id, Label: "今晚割接 " + id,
		StartsAt: "2026-10-17T00:00:00Z", EndsAt: "2026-10-17T06:00:00Z",
		ResourceIDs: scope, CreatedAt: "2026-10-16T00:00:00Z",
	}
	if err := db.Maintenance.InsertWindow(context.Background(), win); err != nil {
		t.Fatalf("seed wall window %s: %v", id, err)
	}
}

// TestDetectSuppressionWallClockBasis is issue #55 experiment B: the
// window covers the sweep's OPERATIONAL moment (wall clock) but the data
// timeline (March evidence ts) is far outside it. Pre-fix this silently
// created the alert (suppression never triggered); post-fix the fire is
// held back with basis="wall_clock" and the window's label on the line.
func TestDetectSuppressionWallClockBasis(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	eng := New()

	seedResource(t, db, "res_wc_a", "DEV-WC|GE-0/0/1")
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_wc", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
	})
	sid := seedSeries(t, db, "res_wc_a", "optical_power_rx_avg_dbm")
	// Data timeline: March — days away from the October window.
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z", []float64{-16, -16.5, -17, -17.5, -18}, domain.PointQualityOk)

	// Window covers [Oct 17 00:00, Oct 17 06:00) scoped to the resource;
	// the sweep runs at Oct 17 02:00 — inside on the WALL clock only.
	seedWallWindow(t, db, "mw_wc", []string{"res_wc_a"})
	wall := time.Date(2026, 10, 17, 2, 0, 0, 0, time.UTC)

	res, err := eng.Detect(ctx, db, Options{WallClock: wall})
	if err != nil {
		t.Fatalf("detect with wall-clock window: %v", err)
	}
	if res.Findings != 1 || res.AlertsCreated != 0 || res.Suppressed != 1 {
		t.Fatalf("created=%d suppressed=%d findings=%d, want 0/1/1 (experiment B: wall-clock plan protects the replay)", res.AlertsCreated, res.Suppressed, res.Findings)
	}
	if alerts := activeAlertsOf(t, db, "dr_wc"); len(alerts) != 0 {
		t.Fatalf("active alerts = %d, want 0 (no row for a suppressed fire)", len(alerts))
	}
	var line *AlertSummary
	for i := range res.Alerts {
		if res.Alerts[i].Action == "suppressed" {
			line = &res.Alerts[i]
		}
	}
	if line == nil {
		t.Fatalf("no suppressed line in envelope: %+v", res.Alerts)
	}
	if line.SuppressionBasis != BasisWallClock {
		t.Errorf("suppressionBasis = %q, want %q", line.SuppressionBasis, BasisWallClock)
	}
	if line.WindowLabel != "今晚割接 mw_wc" {
		t.Errorf("windowLabel = %q, want the window's label", line.WindowLabel)
	}
	// Suppression DID happen — the silent-failure note stays empty.
	if res.SuppressionNote != "" {
		t.Errorf("suppressionNote = %q, want empty when the window actually held a fire", res.SuppressionNote)
	}
	// The would-be id promise: delete the window, same data, the NEXT sweep
	// fires with the SAME identity the suppressed line carried.
	if err := db.Maintenance.DeleteWindow(ctx, "mw_wc"); err != nil {
		t.Fatalf("delete window: %v", err)
	}
	res2, err := eng.Detect(ctx, db, Options{WallClock: wall})
	if err != nil {
		t.Fatalf("detect after window delete: %v", err)
	}
	if res2.AlertsCreated != 1 {
		t.Fatalf("post-window created = %d, want 1 (the plan ended, the fire lands)", res2.AlertsCreated)
	}
	if a := activeAlertsOf(t, db, "dr_wc"); len(a) != 1 || a[0].AlertID != line.AlertID {
		t.Fatalf("fired alert = %+v, want the suppressed line's promised id %s", a, line.AlertID)
	}
}

// TestDetectSuppressionDataTimeBasis annotates the LEGACY data-time match
// (issue #55 experiment C, already green before the fix) with the new
// observability fields: basis="data_time", the window's label on the line,
// no note.
func TestDetectSuppressionDataTimeBasis(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	eng := New()

	seedResource(t, db, "res_dt_a", "DEV-DT|GE-0/0/1")
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_dt", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
	})
	sid := seedSeries(t, db, "res_dt_a", "optical_power_rx_avg_dbm")
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z", []float64{-16, -16.5, -17, -17.5, -18}, domain.PointQualityOk)

	// Window covers the DATA timeline (March) — the replayable basis.
	seedWindow(t, db, "mw_dt", []string{"res_dt_a"})

	// Wall clock far outside the window; the data-time pass still matches.
	res, err := eng.Detect(ctx, db, Options{WallClock: time.Date(2026, 10, 17, 2, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("detect with data-time window: %v", err)
	}
	if res.AlertsCreated != 0 || res.Suppressed != 1 {
		t.Fatalf("created=%d suppressed=%d, want 0/1 (experiment C unchanged)", res.AlertsCreated, res.Suppressed)
	}
	var line *AlertSummary
	for i := range res.Alerts {
		if res.Alerts[i].Action == "suppressed" {
			line = &res.Alerts[i]
		}
	}
	if line == nil {
		t.Fatalf("no suppressed line in envelope: %+v", res.Alerts)
	}
	if line.SuppressionBasis != BasisDataTime {
		t.Errorf("suppressionBasis = %q, want %q", line.SuppressionBasis, BasisDataTime)
	}
	if line.WindowLabel != "测试窗口 mw_dt" {
		t.Errorf("windowLabel = %q, want the window's label", line.WindowLabel)
	}
}

// TestDetectSuppressionNoteOnSilentMiss pins the silent-failure
// eliminator: a wall-ACTIVE window exists, the sweep created new alerts,
// but the fires' resources are outside every active window's scope — the
// exact operator illusion of issue #55 (suppressed=0 is indistinguishable
// from "nothing needed gating"). The receipt must name both clocks.
func TestDetectSuppressionNoteOnSilentMiss(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	eng := New()

	seedResource(t, db, "res_out", "DEV-OUT|GE-0/0/1")
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_note", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
	})
	sid := seedSeries(t, db, "res_out", "optical_power_rx_avg_dbm")
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z", []float64{-16, -16.5, -17, -17.5, -18}, domain.PointQualityOk)

	// Wall-active window scoped to ANOTHER resource — nothing to gate here.
	seedWallWindow(t, db, "mw_note", []string{"res_someone_else"})
	wall := time.Date(2026, 10, 17, 2, 0, 0, 0, time.UTC)

	res, err := eng.Detect(ctx, db, Options{WallClock: wall})
	if err != nil {
		t.Fatalf("detect with out-of-scope wall window: %v", err)
	}
	if res.AlertsCreated != 1 || res.Suppressed != 0 {
		t.Fatalf("created=%d suppressed=%d, want 1/0 (out of scope: nothing held)", res.AlertsCreated, res.Suppressed)
	}
	note := res.SuppressionNote
	if note == "" {
		t.Fatalf("suppressionNote empty — the silent failure issue #55 exists to kill")
	}
	for _, want := range []string{"1 个维护窗口", "2026-10-17T02:00:00Z", "今晚割接 mw_note", "2026-03-05T00:00:00Z", "双时基"} {
		if !strings.Contains(note, want) {
			t.Errorf("suppressionNote %q missing %q", note, want)
		}
	}
}

// TestDetectSuppressionWallClockZero pins backward compatibility: a zero
// Options.WallClock is PURE data-time mode (the pre-v0.12.1 shape). A
// wall-clock window suppresses nothing, and the silent-failure note is
// absent (the run never opted into wall-clock matching — direct Detect
// callers and deterministic replays keep their exact old verdicts).
func TestDetectSuppressionWallClockZero(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	eng := New()

	seedResource(t, db, "res_zero", "DEV-ZERO|GE-0/0/1")
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_zero", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
	})
	sid := seedSeries(t, db, "res_zero", "optical_power_rx_avg_dbm")
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z", []float64{-16, -16.5, -17, -17.5, -18}, domain.PointQualityOk)

	seedWallWindow(t, db, "mw_zero", []string{"res_zero"})

	// WallClock zero: the October window cannot match March data — the
	// legacy engine behavior, byte-for-byte.
	res, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect zero wall clock: %v", err)
	}
	if res.AlertsCreated != 1 || res.Suppressed != 0 {
		t.Fatalf("created=%d suppressed=%d, want 1/0 (zero wall clock = pure data-time mode)", res.AlertsCreated, res.Suppressed)
	}
	if res.SuppressionNote != "" {
		t.Errorf("suppressionNote = %q, want empty (wall-clock matching not enabled)", res.SuppressionNote)
	}
}

// TestRunAndRecordInjectsWallClock pins the recorder-side wiring: every
// REAL trigger origin (HTTP / CLI / scheduler) calls RunAndRecord, which
// stamps the sweep's operational moment from its injected clock — the
// operator's wall-clock plan protects the replay without any caller
// wiring it by hand.
func TestRunAndRecordInjectsWallClock(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()

	seedResource(t, db, "res_inj", "DEV-INJ|GE-0/0/1")
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_inj", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
	})
	sid := seedSeries(t, db, "res_inj", "optical_power_rx_avg_dbm")
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z", []float64{-16, -16.5, -17, -17.5, -18}, domain.PointQualityOk)

	// Wall-clock window covering the sweep moment, scoped to the resource.
	seedWallWindow(t, db, "mw_inj", []string{"res_inj"})

	// The recorder's clock ticks INSIDE the October window; the caller
	// passes plain Options{} — no manual wall-clock wiring.
	clock := fixedClock(time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC), 250*time.Millisecond)
	res, _, err := RunAndRecord(ctx, db, New(), Options{}, domain.RunSourceAPI, clock)
	if err != nil {
		t.Fatalf("RunAndRecord: %v", err)
	}
	if res.AlertsCreated != 0 || res.Suppressed != 1 {
		t.Fatalf("created=%d suppressed=%d, want 0/1 (recorder stamps the wall clock, the plan holds)", res.AlertsCreated, res.Suppressed)
	}
}
