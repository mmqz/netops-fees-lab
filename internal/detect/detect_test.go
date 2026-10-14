package detect

import (
	"context"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// newDetectDB opens a fresh database for the engine tests.
func newDetectDB(t *testing.T) *repo.DB {
	t.Helper()
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	return db
}

// detectTS is the fixed bookkeeping timestamp of the seeds.
var detectTS = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// seedResource inserts one port resource hanging from one import batch
// (one batch per resource — the helper is called repeatedly per test).
func seedResource(t *testing.T, db *repo.DB, id, naturalKey string) {
	t.Helper()
	ctx := context.Background()
	batID := "bat_" + id
	err := db.Do(ctx, func(tx *repo.Tx) error {
		if err := tx.Batches.Create(ctx, newDetectBatch(batID)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: id, Type: domain.ResourceTypePort, NaturalKey: naturalKey,
			Name: naturalKey, Attributes: map[string]string{},
			FirstBatchID: batID, CreatedAt: detectTS, UpdatedAt: detectTS,
		})
	})
	if err != nil {
		t.Fatalf("seed resource %s: %v", id, err)
	}
}

// newDetectBatch is the import batch the seeds hang from.
func newDetectBatch(id string) domain.ImportBatch {
	return domain.ImportBatch{
		ID: id, WorkbookName: "detect.xlsx",
		WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Status:         domain.BatchStatusCompleted, RuleVersion: "seed", StartedAt: detectTS,
	}
}

// seedSeries creates one series of metricKey on resourceID (scope NULL) and
// returns its series id.
func seedSeries(t *testing.T, db *repo.DB, resourceID, metricKey string) string {
	t.Helper()
	sid, _, err := db.Metrics.FindOrCreateSeries(context.Background(), resourceID, metricKey, nil, "", "")
	if err != nil {
		t.Fatalf("seed series %s/%s: %v", resourceID, metricKey, err)
	}
	return sid
}

// seedPoints inserts daily points (value+quality per day index) into the
// series — the test data path (value semantics, ts from data).
func seedPoints(t *testing.T, db *repo.DB, seriesID string, day0 string, values []float64, quality domain.PointQuality) {
	t.Helper()
	base, err := time.Parse(time.RFC3339, day0)
	if err != nil {
		t.Fatalf("bad day0 %q: %v", day0, err)
	}
	if err := db.MetricBatches.Create(context.Background(), domain.MetricBatch{
		ID: "mbat_" + seriesID, WorkbookName: "detect.xlsx",
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
			Value: v, Quality: quality, BatchID: "mbat_" + seriesID,
		})
	}
	if err := db.Metrics.InsertPoints(context.Background(), pts); err != nil {
		t.Fatalf("seed points: %v", err)
	}
}

// insertRule stores one rule with the FIXED seed timestamps.
func insertRule(t *testing.T, db *repo.DB, rule domain.DetectionRule) {
	t.Helper()
	if rule.CreatedAt == "" {
		rule.CreatedAt = "2026-01-01T00:00:00Z"
		rule.UpdatedAt = "2026-01-01T00:00:00Z"
	}
	if err := db.DetectionRules.InsertRule(context.Background(), rule); err != nil {
		t.Fatalf("insert rule %s: %v", rule.RuleID, err)
	}
}

// dayTS renders the i-th day of March 2026 as RFC3339.
func dayTS(i int) string {
	return time.Date(2026, 3, i+1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
}

// activeAlertsOf reads the firing|acked alerts of a rule through the repo
// read path (ordered by series_id).
func activeAlertsOf(t *testing.T, db *repo.DB, ruleID string) []domain.Alert {
	t.Helper()
	alerts, err := db.Alerts.ListActiveAlertsByRule(context.Background(), ruleID)
	if err != nil {
		t.Fatalf("ListActiveAlertsByRule(%s): %v", ruleID, err)
	}
	return alerts
}

// TestDetectFiresClearsAndRefires pins the alert lifecycle against a real
// database: fire on violation, engine-clear on recovery (firing→cleared at
// the last evaluated data ts), then a LATER re-violation creates a NEW
// alert row (fresh first_seen — never a zombie re-open), and a manual
// clear of a still-violating series is not resurrected by a same-window
// re-run (the honest UNIQUE suppression).
func TestDetectFiresClearsAndRefires(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_p1", "DEV-1|GE-0/0/1")
	sid := seedSeries(t, db, "res_p1", "optical_power_rx_avg_dbm")
	// Days 1-10: healthy first, violating from day 5 on (trailing run
	// days 5-10 = 6 consecutive ≥ window 3).
	vals := []float64{-8.0, -8.1, -8.0, -8.2, -14.5, -15.0, -15.4, -15.2, -15.1, -15.3}
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z", vals, domain.PointQualityOk)
	insertRule(t, db, rxRule())
	engine := New()

	// Run 1: fires exactly one firing alert with the evidence.
	res, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if res.Findings != 1 || res.AlertsCreated != 1 || res.AlertsCleared != 0 {
		t.Fatalf("run 1 = %+v, want 1 finding / 1 created / 0 cleared", res)
	}
	if res.EvaluatedAt != dayTS(9) {
		t.Errorf("run 1 EvaluatedAt = %q, want %q (newest evaluated point)", res.EvaluatedAt, dayTS(9))
	}
	alerts := activeAlertsOf(t, db, "dr_test_rx")
	if len(alerts) != 1 {
		t.Fatalf("active alerts after run 1 = %d, want 1", len(alerts))
	}
	a1 := alerts[0]
	if a1.State != domain.AlertStateFiring || a1.FirstSeen != dayTS(4) || a1.FiredAt != dayTS(9) || a1.LastValue != -15.3 {
		t.Errorf("alert after run 1 = %+v, want firing firstSeen=%s firedAt=%s lastValue=-15.3", a1, dayTS(4), dayTS(9))
	}
	if a1.AlertID != NewAlertID("dr_test_rx", sid, dayTS(4)) {
		t.Errorf("alert id %q is not the derived identity", a1.AlertID)
	}

	// Run 1 again (idempotent per run): no alert mutations, the run-state
	// write repeats at the SAME evaluation ts — value semantics keeps
	// the history at ONE row (the write is the idempotency unit).
	res2, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if res2.Findings != 1 || res2.AlertsCreated != 0 || res2.AlertsCleared != 0 {
		t.Errorf("re-run = %+v, want 1 finding / 0 created / 0 cleared (idempotent)", res2)
	}
	if res2.RunStatesWritten != 1 {
		t.Errorf("re-run run-states = %d, want 1 (the qualifying degraded write repeats)", res2.RunStatesWritten)
	}
	if got := activeAlertsOf(t, db, "dr_test_rx"); len(got) != 1 || got[0].AlertID != a1.AlertID {
		t.Errorf("re-run changed the alert rows: %+v", got)
	}
	hist, err := db.ResourceStatus.ListHistory(ctx, "res_p1", "", "")
	if err != nil || len(hist) != 1 {
		t.Errorf("history after re-run = %d rows (err %v), want 1 (same-ts write replaced in place)", len(hist), err)
	}

	// Recovery: days 11-12 back in bounds (the value-semantics upsert of
	// the metric channel re-importing the same series) → run 2 clears.
	if err := db.Metrics.InsertPoints(ctx, []domain.MetricPoint{
		{SeriesID: sid, TS: dayTS(10), Value: -9.0, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid},
		{SeriesID: sid, TS: dayTS(11), Value: -9.1, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid},
	}); err != nil {
		t.Fatalf("seed recovery points: %v", err)
	}
	res3, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("run 2 (recovery): %v", err)
	}
	if res3.Findings != 0 || res3.AlertsCleared != 1 {
		t.Fatalf("run 2 = %+v, want 0 findings / 1 cleared", res3)
	}
	cleared, err := db.Alerts.GetAlertByID(ctx, a1.AlertID)
	if err != nil {
		t.Fatalf("GetAlertByID after clear: %v", err)
	}
	if cleared.State != domain.AlertStateCleared || cleared.ClearedAt != dayTS(11) {
		t.Errorf("cleared alert = %+v, want cleared at %q (last evaluated ts from data)", cleared, dayTS(11))
	}
	if got := activeAlertsOf(t, db, "dr_test_rx"); len(got) != 0 {
		t.Errorf("active alerts after recovery = %d, want 0", len(got))
	}

	// Re-violation days 13-15: a NEW alert row (fresh first_seen), the
	// old cleared row untouched.
	if err := db.Metrics.InsertPoints(ctx, []domain.MetricPoint{
		{SeriesID: sid, TS: dayTS(12), Value: -16.0, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid},
		{SeriesID: sid, TS: dayTS(13), Value: -16.1, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid},
		{SeriesID: sid, TS: dayTS(14), Value: -16.2, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid},
	}); err != nil {
		t.Fatalf("seed re-violation points: %v", err)
	}
	res4, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("run 3 (re-fire): %v", err)
	}
	if res4.AlertsCreated != 1 {
		t.Fatalf("run 3 created %d alerts, want 1 (fresh first_seen)", res4.AlertsCreated)
	}
	alerts = activeAlertsOf(t, db, "dr_test_rx")
	if len(alerts) != 1 || alerts[0].FirstSeen != dayTS(12) {
		t.Fatalf("alerts after re-fire = %+v, want one row with firstSeen %s", alerts, dayTS(12))
	}
	if alerts[0].AlertID == a1.AlertID {
		t.Errorf("re-fire reused the cleared alert id — zombie re-open")
	}

	// Manual clear of a STILL-violating series, then a same-window run:
	// no resurrection (UNIQUE (rule, series, first_seen) suppression).
	if err := db.Alerts.ClearAlert(ctx, alerts[0].AlertID, "2026-03-20T00:00:00Z"); err != nil {
		t.Fatalf("manual clear: %v", err)
	}
	res5, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("run 4 (manual clear holds): %v", err)
	}
	if res5.AlertsCreated != 0 || len(activeAlertsOf(t, db, "dr_test_rx")) != 0 {
		t.Errorf("run 4 = %+v, want 0 created and 0 active (manual clear holds until a later violation window)", res5)
	}
}

// TestDetectAckFloor pins the human floor: an acked alert never re-fires
// while acked (no second row, state stays acked) even as the violation
// continues; the engine only clears it when the recovery condition holds
// (acked→cleared, the third edge).
func TestDetectAckFloor(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_p1", "DEV-1|GE-0/0/1")
	sid := seedSeries(t, db, "res_p1", "optical_power_rx_avg_dbm")
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z",
		[]float64{-8.0, -14.5, -15.0, -15.4, -15.2}, domain.PointQualityOk)
	insertRule(t, db, rxRule())
	engine := New()

	if _, err := engine.Detect(ctx, db, Options{}); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	alerts := activeAlertsOf(t, db, "dr_test_rx")
	if len(alerts) != 1 {
		t.Fatalf("alerts after run 1 = %d, want 1", len(alerts))
	}
	id := alerts[0].AlertID
	if err := db.Alerts.AckAlert(ctx, id, "2026-03-06T00:00:00Z", "值班员A"); err != nil {
		t.Fatalf("ack: %v", err)
	}

	// The violation continues (days 6-8 violating): no re-fire, the
	// acked alert stays acked, its evidence is FROZEN (no update).
	if err := db.Metrics.InsertPoints(ctx, []domain.MetricPoint{
		{SeriesID: sid, TS: dayTS(5), Value: -15.9, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid},
	}); err != nil {
		t.Fatalf("seed continued violation: %v", err)
	}
	res, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("run 2 (ack floor): %v", err)
	}
	if res.Findings != 1 || res.AlertsCreated != 0 {
		t.Errorf("run 2 = %+v, want 1 finding / 0 created (ack floor)", res)
	}
	alerts = activeAlertsOf(t, db, "dr_test_rx")
	if len(alerts) != 1 || alerts[0].AlertID != id || alerts[0].State != domain.AlertStateAcked {
		t.Fatalf("alerts during ack = %+v, want the single acked row", alerts)
	}
	if alerts[0].LastValue != -15.2 {
		t.Errorf("acked alert evidence moved to %v, want frozen at -15.2 (the value at ack time)", alerts[0].LastValue)
	}

	// Recovery: the acked alert clears via the engine (acked→cleared).
	if err := db.Metrics.InsertPoints(ctx, []domain.MetricPoint{
		{SeriesID: sid, TS: dayTS(6), Value: -9.0, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid},
	}); err != nil {
		t.Fatalf("seed recovery: %v", err)
	}
	res, err = engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("run 3 (acked recovery): %v", err)
	}
	if res.AlertsCleared != 1 {
		t.Fatalf("run 3 cleared %d, want 1 (acked→cleared)", res.AlertsCleared)
	}
	got, err := db.Alerts.GetAlertByID(ctx, id)
	if err != nil || got.State != domain.AlertStateCleared || got.AckedBy != "值班员A" {
		t.Errorf("alert after acked recovery = %+v (err %v), want cleared with the ack audit kept", got, err)
	}
}

// TestDetectRunStateIntegration pins the §12-B2 衔接点 ③ mapping: firing
// error alert → down, firing warning alert → degraded (error wins), all
// writes go through the SAME double-write path as the manual channel
// (resource_status history row + resources snapshot, source=alert), and
// recovery writes up for resources whose alert-sourced history exists.
func TestDetectRunStateIntegration(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_p1", "DEV-1|GE-0/0/1")
	seedResource(t, db, "res_p2", "DEV-1|GE-0/0/2")
	seedResource(t, db, "res_p3", "DEV-1|GE-0/0/3")
	sid1 := seedSeries(t, db, "res_p1", "optical_power_rx_avg_dbm") // floor rule (error)
	sid2 := seedSeries(t, db, "res_p2", "optical_power_rx_avg_dbm") // low rule (warning)
	sid3 := seedSeries(t, db, "res_p3", "module_temp_avg_c")        // no rule hit
	_ = sid3
	// p1: single point below -25 → dr_opt_rx_floor error.
	seedPoints(t, db, sid1, "2026-03-01T00:00:00Z", []float64{-26.0}, domain.PointQualityOk)
	// p2: three consecutive below -14 → dr_test_rx warning.
	seedPoints(t, db, sid2, "2026-03-01T00:00:00Z", []float64{-15.0, -15.1, -15.2}, domain.PointQualityOk)
	insertRule(t, db, rxRule())
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_test_floor", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -25.0, Window: 1, Severity: domain.SeverityError, Enabled: true,
	})
	engine := New()

	res, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if res.AlertsCreated != 2 {
		t.Fatalf("run 1 created %d alerts, want 2 (p1 error + p2 warning)", res.AlertsCreated)
	}
	// p1 error (and p1's -26 also fires dr_test_rx? -26 ≤ -14 → yes: p1
	// fires BOTH rules; the error wins the run-state), p2 warning.
	if res.RunStatesWritten != 2 {
		t.Fatalf("run 1 wrote %d run-states, want 2 (p1 down + p2 degraded)", res.RunStatesWritten)
	}

	// Double-write assertion: history row AND snapshot, source=alert.
	for _, tc := range []struct {
		id   string
		want domain.RunState
	}{
		{"res_p1", domain.RunStateDown},
		{"res_p2", domain.RunStateDegraded},
	} {
		r, err := db.Resources.GetByID(ctx, tc.id)
		if err != nil {
			t.Fatalf("GetByID(%s): %v", tc.id, err)
		}
		if r.RunState != tc.want.String() {
			t.Errorf("%s snapshot run_state = %q, want %q", tc.id, r.RunState, tc.want)
		}
		hist, err := db.ResourceStatus.ListHistory(ctx, tc.id, "", "")
		if err != nil {
			t.Fatalf("ListHistory(%s): %v", tc.id, err)
		}
		if len(hist) != 1 || hist[0].RunState != tc.want || hist[0].Source != domain.StatusSourceAlert {
			t.Errorf("%s history = %+v, want one %q observation from source=alert", tc.id, hist, tc.want)
		}
		if hist[0].TS.Format(time.RFC3339) != res.EvaluatedAt {
			t.Errorf("%s history ts = %v, want the run's evaluation ts %q", tc.id, hist[0].TS, res.EvaluatedAt)
		}
	}
	// p3: no violation, no alert-sourced write (no data-no-verdict).
	r3, err := db.Resources.GetByID(ctx, "res_p3")
	if err != nil {
		t.Fatalf("GetByID(res_p3): %v", err)
	}
	if r3.RunState != "" {
		t.Errorf("res_p3 run_state = %q, want empty (no observation)", r3.RunState)
	}

	// Recovery: both series back in bounds → alerts cleared + up writes.
	if err := db.Metrics.InsertPoints(ctx, []domain.MetricPoint{
		{SeriesID: sid1, TS: "2026-03-02T00:00:00Z", Value: -8.0, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid1},
		{SeriesID: sid2, TS: "2026-03-04T00:00:00Z", Value: -8.0, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid2},
	}); err != nil {
		t.Fatalf("seed recovery: %v", err)
	}
	res2, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("run 2 (recovery): %v", err)
	}
	if res2.AlertsCleared != 2 { // p1 floor + p2 low
		t.Errorf("run 2 cleared %d, want 2", res2.AlertsCleared)
	}
	for _, id := range []string{"res_p1", "res_p2"} {
		r, err := db.Resources.GetByID(ctx, id)
		if err != nil {
			t.Fatalf("GetByID(%s) after recovery: %v", id, err)
		}
		if r.RunState != domain.RunStateUp.String() {
			t.Errorf("%s run_state after recovery = %q, want up", id, r.RunState)
		}
		hist, _ := db.ResourceStatus.ListHistory(ctx, id, "", "")
		if len(hist) != 2 || hist[1].RunState != domain.RunStateUp || hist[1].Source != domain.StatusSourceAlert {
			t.Errorf("%s history after recovery = %+v, want [non-up, up] both alert-sourced", id, hist)
		}
	}

	// Manual-then-engine co-existence: a manual 'up' written AFTER the
	// alert-sourced 'down' is not overridden by a recovery write at an
	// OLDER evaluation ts (UpsertStatus moves the snapshot only for
	// ts >= projected) — the honest last-write-wins semantics.
	manualTS := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		return tx.ResourceStatus.UpsertStatus(ctx, domain.ResourceStatus{
			ResourceID: "res_p2", TS: manualTS, RunState: domain.RunStateUp, Source: domain.StatusSourceManual,
		})
	}); err != nil {
		t.Fatalf("manual up write: %v", err)
	}
	// A NEW detection run (newer evaluation ts than the manual write)
	// legitimately moves the snapshot back to the alert verdict.
	if err := db.Metrics.InsertPoints(ctx, []domain.MetricPoint{
		{SeriesID: sid2, TS: "2026-03-11T00:00:00Z", Value: -15.5, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid2},
		{SeriesID: sid2, TS: "2026-03-12T00:00:00Z", Value: -15.6, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid2},
		{SeriesID: sid2, TS: "2026-03-13T00:00:00Z", Value: -15.7, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid2},
	}); err != nil {
		t.Fatalf("seed re-violation: %v", err)
	}
	res3, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("run 3: %v", err)
	}
	if res3.AlertsCreated != 1 || res3.RunStatesWritten != 2 { // p2 degraded + p1 up (recovery pass)
		t.Fatalf("run 3 = %+v, want 1 created / 2 run-states", res3)
	}
	r2, _ := db.Resources.GetByID(ctx, "res_p2")
	if r2.RunState != domain.RunStateDegraded.String() {
		t.Errorf("res_p2 run_state after new alert run = %q, want degraded (newer run overrides older manual up)", r2.RunState)
	}
}

// TestDetectDeterminism pins the run-level determinism on two levels:
//
//   - SAME database: a second Detect over unchanged data mutates nothing
//     (the alert rows are byte-identical, the run reports zero mutations) —
//     same state + rules + window → same outcome, the strong claim;
//   - two INDEPENDENTLY seeded databases with the same logical data: the
//     verdict content (rule, metric key, severity, state, first_seen,
//     fired_at, last_value) is identical. The only divergence allowed is
//     the ids themselves: series ids are ser_<uuid> random (the W20 ingest
//     convention) and alert ids are DERIVED from them, so byte-identity
//     holds within a database, logical identity across databases.
func TestDetectDeterminism(t *testing.T) {
	seed := func() (*repo.DB, RunResult, []repo.AlertRow) {
		db := newDetectDB(t)
		ctx := context.Background()
		seedResource(t, db, "res_p1", "DEV-1|GE-0/0/1")
		seedResource(t, db, "res_p2", "DEV-2|GE-0/0/1")
		s1 := seedSeries(t, db, "res_p1", "optical_power_rx_avg_dbm")
		s2 := seedSeries(t, db, "res_p2", "packet_loss_rx_pct")
		seedPoints(t, db, s1, "2026-03-01T00:00:00Z", []float64{-15.0, -15.1, -15.2, -15.3}, domain.PointQualityOk)
		seedPoints(t, db, s2, "2026-03-01T00:00:00Z", []float64{0.0, 1.2, 1.5, 1.8}, domain.PointQualityOk)
		insertRule(t, db, rxRule())
		insertRule(t, db, domain.DetectionRule{
			RuleID: "dr_test_loss", MetricKey: "packet_loss_rx_pct", Comparator: domain.ComparatorGte,
			Threshold: 1.0, Window: 5, Severity: domain.SeverityWarning, Enabled: true,
		})
		res, err := New().Detect(ctx, db, Options{})
		if err != nil {
			t.Fatalf("detect: %v", err)
		}
		rows, _, err := db.Alerts.ListAlerts(ctx, repo.AlertFilter{})
		if err != nil {
			t.Fatalf("ListAlerts: %v", err)
		}
		return db, res, rows
	}
	dbA, resA, rowsA := seed()
	_, resB, rowsB := seed()

	// dr_test_loss needs 5 consecutive ≥1.0: only 3 → the loss rule does
	// NOT fire; exactly the RX alert exists in both databases.
	if resA.Findings != 1 || resA.AlertsCreated != 1 {
		t.Errorf("findings/created = %d/%d, want 1/1 (window 5 needs 5 points)", resA.Findings, resA.AlertsCreated)
	}

	// Logical identity across the two databases (ids excepted).
	if resA.Findings != resB.Findings || resA.AlertsCreated != resB.AlertsCreated ||
		resA.AlertsCleared != resB.AlertsCleared || resA.RunStatesWritten != resB.RunStatesWritten ||
		resA.EvaluatedAt != resB.EvaluatedAt {
		t.Errorf("same logical seeds → different run counters:\n%+v\nvs\n%+v", resA, resB)
	}
	if len(rowsA) != len(rowsB) {
		t.Fatalf("same logical seeds → different alert counts %d vs %d", len(rowsA), len(rowsB))
	}
	for i := range rowsA {
		a, b := rowsA[i], rowsB[i]
		if a.RuleID != b.RuleID || a.MetricKey != b.MetricKey || a.Severity != b.Severity ||
			a.State != b.State || a.FirstSeen != b.FirstSeen || a.FiredAt != b.FiredAt ||
			a.LastValue != b.LastValue || a.ResourceID != b.ResourceID {
			t.Errorf("alert %d verdict diverged: %+v vs %+v", i, a, b)
		}
	}

	// Same-database re-run: zero mutations, byte-identical rows.
	ctx := context.Background()
	resAgain, err := New().Detect(ctx, dbA, Options{})
	if err != nil {
		t.Fatalf("re-detect: %v", err)
	}
	if resAgain.AlertsCreated != 0 || resAgain.AlertsCleared != 0 || resAgain.Findings != 1 {
		t.Errorf("re-detect = %+v, want 1 finding / 0 created / 0 cleared (state deterministic)", resAgain)
	}
	rowsAgain, _, err := dbA.Alerts.ListAlerts(ctx, repo.AlertFilter{})
	if err != nil {
		t.Fatalf("ListAlerts after re-detect: %v", err)
	}
	if len(rowsAgain) != len(rowsA) {
		t.Fatalf("alert count changed on re-detect: %d vs %d", len(rowsAgain), len(rowsA))
	}
	for i := range rowsA {
		if rowsAgain[i].AlertID != rowsA[i].AlertID || rowsAgain[i].LastValue != rowsA[i].LastValue ||
			rowsAgain[i].State != rowsA[i].State || rowsAgain[i].FirstSeen != rowsA[i].FirstSeen {
			t.Errorf("alert %d mutated on re-detect: %+v vs %+v", i, rowsAgain[i], rowsA[i])
		}
	}
}

// TestNewAlertIDStable pins the derived-id contract: a pure function of
// (rule, series, first_seen) — same inputs, same id; any input change, a
// different id (the re-fire identity).
func TestNewAlertIDStable(t *testing.T) {
	a := NewAlertID("dr_r", "ser_s", "2026-03-01T00:00:00Z")
	b := NewAlertID("dr_r", "ser_s", "2026-03-01T00:00:00Z")
	if a != b {
		t.Errorf("same identity → different ids %q vs %q", a, b)
	}
	if a == NewAlertID("dr_r2", "ser_s", "2026-03-01T00:00:00Z") ||
		a == NewAlertID("dr_r", "ser_s2", "2026-03-01T00:00:00Z") ||
		a == NewAlertID("dr_r", "ser_s", "2026-03-04T00:00:00Z") {
		t.Errorf("different identities collided on %q", a)
	}
}

// TestDetectDisabledRuleHolds pins the disabled-rule semantics: the engine
// loads ONLY enabled rules — a disabled rule produces no findings and its
// existing firing alert is neither cleared (no evaluation) nor re-fired;
// the alert still HOLDS the resource's run-state (ListActiveAlertRefs is
// rule-blind) until cleared.
func TestDetectDisabledRuleHolds(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_p1", "DEV-1|GE-0/0/1")
	sid := seedSeries(t, db, "res_p1", "optical_power_rx_avg_dbm")
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z", []float64{-15.0, -15.1, -15.2}, domain.PointQualityOk)
	insertRule(t, db, rxRule())
	engine := New()

	if _, err := engine.Detect(ctx, db, Options{}); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	alert := activeAlertsOf(t, db, "dr_test_rx")[0]

	// Disable the rule (the mutable field of PATCH).
	rule, err := db.DetectionRules.GetRuleByID(ctx, "dr_test_rx")
	if err != nil {
		t.Fatalf("GetRuleByID: %v", err)
	}
	rule.Enabled = false
	rule.UpdatedAt = "2026-03-06T00:00:00Z"
	if err := db.DetectionRules.UpdateRule(ctx, rule); err != nil {
		t.Fatalf("UpdateRule (disable): %v", err)
	}

	// Recovery data arrives; the engine (enabled rules only) neither
	// fires nor clears — the alert stays firing, the state holds.
	if err := db.Metrics.InsertPoints(ctx, []domain.MetricPoint{
		{SeriesID: sid, TS: dayTS(3), Value: -8.0, Quality: domain.PointQualityOk, BatchID: "mbat_" + sid},
	}); err != nil {
		t.Fatalf("seed recovery: %v", err)
	}
	res, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("run 2 (disabled): %v", err)
	}
	if res.Findings != 0 || res.AlertsCleared != 0 || res.AlertsCreated != 0 {
		t.Errorf("run 2 = %+v, want all zero (disabled rule is not evaluated)", res)
	}
	got, err := db.Alerts.GetAlertByID(ctx, alert.AlertID)
	if err != nil || got.State != domain.AlertStateFiring {
		t.Errorf("alert of a disabled rule = %+v (err %v), want still firing (held)", got, err)
	}
	// The alert still holds the run-state down (or degraded here) even
	// though no run-state write happened this run.
	r, _ := db.Resources.GetByID(ctx, "res_p1")
	if r.RunState != domain.RunStateDegraded.String() {
		t.Errorf("res_p1 run_state with a held alert = %q, want degraded (held from run 1)", r.RunState)
	}
}

// TestDetectWindowBounds pins the [from, to] lookback window: only the
// points inside the window are evaluated (a violation outside the window
// never fires; a window that truncates the trailing run breaks it).
func TestDetectWindowBounds(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_p1", "DEV-1|GE-0/0/1")
	sid := seedSeries(t, db, "res_p1", "optical_power_rx_avg_dbm")
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z", []float64{-15.0, -15.1, -15.2, -8.0}, domain.PointQualityOk)
	insertRule(t, db, rxRule())
	engine := New()

	// Full window: days 1-3 violating then day 4 clean → recovered.
	res, err := engine.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("full-window run: %v", err)
	}
	if res.Findings != 0 {
		t.Errorf("full window findings = %d, want 0 (clean tail)", res.Findings)
	}

	// Bounded to the violating prefix [_, 2026-03-03]: fires.
	res, err = engine.Detect(ctx, db, Options{To: dayTS(2)})
	if err != nil {
		t.Fatalf("bounded run: %v", err)
	}
	if res.Findings != 1 || res.AlertsCreated != 1 {
		t.Errorf("bounded run = %+v, want 1 finding / 1 created", res)
	}
	if res.EvaluatedAt != dayTS(2) {
		t.Errorf("bounded run EvaluatedAt = %q, want %q", res.EvaluatedAt, dayTS(2))
	}

	// From-bound after the violation: no data in scope → no findings,
	// no alert touched, no run-state writes (nothing evaluated).
	res, err = engine.Detect(ctx, db, Options{From: dayTS(5)})
	if err != nil {
		t.Fatalf("empty-window run: %v", err)
	}
	if res.Findings != 0 || res.EvaluatedAt != "" || res.RunStatesWritten != 0 {
		t.Errorf("empty-window run = %+v, want 0 findings / empty evaluatedAt / 0 run-states", res)
	}
	if got := activeAlertsOf(t, db, "dr_test_rx"); len(got) != 1 {
		t.Errorf("empty-window run touched the alert rows: %+v", got)
	}
}
