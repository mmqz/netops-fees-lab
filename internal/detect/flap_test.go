package detect

// flap_test.go — pins the v0.12-R17 re-fire hold-down (告警抖动治理):
// the churn contract of a threshold-hovering series under the patrol
// scheduler. The journey: three complete fire→clear cycles (the honest
// allow-budget), the 4th fire held (no row, counted, the deterministic
// would-be id promised), the hold ages out as the pair's cleared rows
// exit the trailing evidence span, and the next sustained fire lands the
// PROMISED identity. The narrow semantics are pinned separately: only
// ENGINE clears count (a human clear is a decision, not oscillation),
// the hold is per-(rule, series) pair, recovery is never held, and an
// active maintenance window outranks the flap verdict.

import (
	"context"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// flapRule is the canonical flap test rule (the rxRule shape).
func flapRule() domain.DetectionRule {
	return domain.DetectionRule{
		RuleID: "dr_flap", MetricKey: "optical_power_rx_avg_dbm",
		Comparator: domain.ComparatorLte, Threshold: -14.0, Window: 3,
		Severity: domain.SeverityWarning, Enabled: true, Label: "接收光功率持续偏低",
	}
}

// TestFlapConstantsPinned pins the damping envelope: three completed
// cycles within a 14-day evidence span hold the next fire. The values are
// engine tuning shared by every rule (code constants, not per-rule
// columns — the domain doc); this test exists so a casual edit cannot
// silently change the whole fleet's noise behavior.
func TestFlapConstantsPinned(t *testing.T) {
	if domain.FlapThreshold != 3 {
		t.Errorf("FlapThreshold = %d, want 3 (allow three complete cycles)", domain.FlapThreshold)
	}
	if domain.FlapSpan != 14*24*time.Hour {
		t.Errorf("FlapSpan = %v, want 14d (the aging horizon)", domain.FlapSpan)
	}
}

// TestFlapCutoffMatrix pins the pure cutoff math: the trailing span is
// counted BACK from the would-be fire's own evidence ts on the data
// timeline, RFC3339 both ends, and an unparseable anchor is a hard error
// (the corrupted-timeline doctrine of loadSuppressionWindows).
func TestFlapCutoffMatrix(t *testing.T) {
	cases := []struct {
		anchor string
		want   string
	}{
		{"2026-03-15T00:00:00Z", "2026-03-01T00:00:00Z"},
		{"2026-03-01T00:00:00Z", "2026-02-15T00:00:00Z"},
		{"2026-03-15T12:34:56Z", "2026-03-01T12:34:56Z"},
	}
	for _, tc := range cases {
		got, err := flapCutoff(tc.anchor)
		if err != nil {
			t.Fatalf("flapCutoff(%s): %v", tc.anchor, err)
		}
		if got != tc.want {
			t.Errorf("flapCutoff(%s) = %s, want %s", tc.anchor, got, tc.want)
		}
	}
	if _, err := flapCutoff("not-a-timestamp"); err == nil {
		t.Error("flapCutoff(unparseable) = nil error, want error (honest failure)")
	}
}

// seedClearedCycle appends one complete fire→clear cycle to the pair's
// history through the REAL repo paths (InsertFiringAlert + ClearAlert
// with a data-timeline ts — the engine's own recovery-edge shape).
func seedClearedCycle(t *testing.T, db *repo.DB, ruleID, seriesID, firstSeen, firedAt, clearedAt string) {
	t.Helper()
	_, err := db.Alerts.InsertFiringAlert(context.Background(), domain.Alert{
		AlertID: NewAlertID(ruleID, seriesID, firstSeen), RuleID: ruleID, SeriesID: seriesID,
		FiredAt: firedAt, State: domain.AlertStateFiring, LastValue: -16.0, FirstSeen: firstSeen,
	})
	if err != nil {
		t.Fatalf("seed firing alert %s: %v", firstSeen, err)
	}
	if err := db.Alerts.ClearAlert(context.Background(), NewAlertID(ruleID, seriesID, firstSeen), clearedAt); err != nil {
		t.Fatalf("seed cleared alert %s: %v", firstSeen, err)
	}
}

// TestCountClearedInSpanBoundaries pins the repo boundary semantics the
// flap verdict derives from: state='cleared' rows only, pair-filtered,
// the span is (from, to] — strict lower, inclusive upper — and a
// wall-clock (human) clear lands outside a data-timeline span.
func TestCountClearedInSpanBoundaries(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_flap_q", "DEV-FLQ|GE-0/0/1")
	sid := seedSeries(t, db, "res_flap_q", "optical_power_rx_avg_dbm")
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_flap_q", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
	})

	// Three engine-shaped cycles: cleared at Mar 4 / Mar 8 / Mar 12.
	seedClearedCycle(t, db, "dr_flap_q", sid, "2026-03-01T00:00:00Z", "2026-03-03T00:00:00Z", "2026-03-04T00:00:00Z")
	seedClearedCycle(t, db, "dr_flap_q", sid, "2026-03-05T00:00:00Z", "2026-03-07T00:00:00Z", "2026-03-08T00:00:00Z")
	seedClearedCycle(t, db, "dr_flap_q", sid, "2026-03-09T00:00:00Z", "2026-03-11T00:00:00Z", "2026-03-12T00:00:00Z")
	// A fourth pair (rule/series isolation) and a firing row (state filter).
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_other", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
	})
	seedResource(t, db, "res_other_q", "DEV-OTQ|GE-0/0/1")
	serOther := seedSeries(t, db, "res_other_q", "optical_power_rx_avg_dbm")
	seedClearedCycle(t, db, "dr_other", sid, "2026-03-01T00:00:00Z", "2026-03-03T00:00:00Z", "2026-03-04T00:00:00Z")
	seedClearedCycle(t, db, "dr_flap_q", serOther, "2026-03-01T00:00:00Z", "2026-03-03T00:00:00Z", "2026-03-04T00:00:00Z")
	if _, err := db.Alerts.InsertFiringAlert(ctx, domain.Alert{
		AlertID: NewAlertID("dr_flap_q", sid, "2026-03-13T00:00:00Z"), RuleID: "dr_flap_q", SeriesID: sid,
		FiredAt: "2026-03-15T00:00:00Z", State: domain.AlertStateFiring, LastValue: -16.0, FirstSeen: "2026-03-13T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed firing row: %v", err)
	}

	count := func(from, to string) int {
		t.Helper()
		n, err := db.Alerts.CountClearedInSpan(ctx, "dr_flap_q", sid, from, to)
		if err != nil {
			t.Fatalf("CountClearedInSpan(%s, %s): %v", from, to, err)
		}
		return n
	}

	if got := count("2026-03-01T00:00:00Z", "2026-03-15T00:00:00Z"); got != 3 {
		t.Errorf("count (Mar1, Mar15] = %d, want 3 (all three cycles)", got)
	}
	if got := count("2026-03-04T00:00:00Z", "2026-03-15T00:00:00Z"); got != 2 {
		t.Errorf("count (Mar4, Mar15] = %d, want 2 (strict lower bound drops Mar 4)", got)
	}
	if got := count("2026-03-01T00:00:00Z", "2026-03-11T00:00:00Z"); got != 2 {
		t.Errorf("count (Mar1, Mar11] = %d, want 2 (inclusive upper drops Mar 12)", got)
	}
	if got := count("2026-03-01T00:00:00Z", "2026-03-12T00:00:00Z"); got != 3 {
		t.Errorf("count (Mar1, Mar12] = %d, want 3 (inclusive upper keeps Mar 12)", got)
	}
	if got := count("2026-03-20T00:00:00Z", "2026-03-30T00:00:00Z"); got != 0 {
		t.Errorf("count (Mar20, Mar30] = %d, want 0 (aged out)", got)
	}

	// A HUMAN clear stamps the wall clock (2026-10-17) — far ahead of the
	// data timeline (March 2026), so it never counts inside a data span.
	seedClearedCycle(t, db, "dr_flap_q", sid, "2026-03-16T00:00:00Z", "2026-03-18T00:00:00Z", "2026-10-17T09:00:00Z")
	if got := count("2026-03-01T00:00:00Z", "2026-03-30T00:00:00Z"); got != 3 {
		t.Errorf("count after human clear = %d, want 3 (wall-clock clear outside the data span)", got)
	}
}

// TestDetectFlapHoldDownJourney pins the whole damping contract end to
// end against a real database — the operator-visible story:
//
//	cycle 1..3 fire/clear honestly (the allow-budget: 3 rows + 3 webhooks);
//	cycle 4's fire is HELD — no 4th row, FlapHeld=1, the would-be id
//	  promised in the envelope line;
//	the hold AGES OUT as cleared rows exit the trailing 14-day span —
//	  and when the still-violating series finally fires, it lands the
//	  PROMISED identity (the r15 correlatability doctrine);
//	an idempotent re-run over unchanged data repeats the hold verbatim.
func TestDetectFlapHoldDownJourney(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_flap", "DEV-FLAP|GE-0/0/1")
	sid := seedSeries(t, db, "res_flap", "optical_power_rx_avg_dbm")
	insertRule(t, db, flapRule())
	eng := New()

	// cycle helper: 3 violating days then 1 healthy day, then sweep.
	// dayN renders Mar N as RFC3339.
	dayN := func(n int) string {
		return time.Date(2026, 3, n, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	}
	cycle := func(firstDay int) {
		t.Helper()
		appendPoints(t, db, sid, dayN(firstDay), []float64{-15.0, -15.5, -16.0})
		res, err := eng.Detect(ctx, db, Options{})
		if err != nil {
			t.Fatalf("cycle fire at day %d: %v", firstDay, err)
		}
		if res.AlertsCreated != 1 || res.AlertsCleared != 0 || res.FlapHeld != 0 {
			t.Fatalf("cycle fire at day %d = created %d / cleared %d / flapHeld %d, want 1/0/0", firstDay, res.AlertsCreated, res.AlertsCleared, res.FlapHeld)
		}
		appendPoints(t, db, sid, dayN(firstDay+3), []float64{-9.0})
		res, err = eng.Detect(ctx, db, Options{})
		if err != nil {
			t.Fatalf("cycle clear at day %d: %v", firstDay+3, err)
		}
		if res.AlertsCleared != 1 || res.FlapHeld != 0 {
			t.Fatalf("cycle clear at day %d = cleared %d / flapHeld %d, want 1/0", firstDay+3, res.AlertsCleared, res.FlapHeld)
		}
	}

	// Cycles 1-3: days 1-4, 5-8, 9-12 — three cleared rows (cleared_at
	// Mar 4 / 8 / 12, all data-timeline ts).
	cycle(1)
	cycle(5)
	cycle(9)

	// Cycle 4's fire (days 13-15): the pair's own history now shows 3
	// completed cycles within (Mar 1, Mar 15] — the fire is HELD.
	appendPoints(t, db, sid, dayN(13), []float64{-15.2, -15.6, -16.1})
	res, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("flap-held sweep: %v", err)
	}
	if res.FlapHeld != 1 || res.AlertsCreated != 0 || res.AlertsCleared != 0 {
		t.Fatalf("held sweep = flapHeld %d / created %d / cleared %d, want 1/0/0", res.FlapHeld, res.AlertsCreated, res.AlertsCleared)
	}
	if res.Findings != 1 {
		t.Errorf("held sweep findings = %d, want 1 (the violation is real, only the fire is held)", res.Findings)
	}
	// The held line: action, empty state, the would-be id.
	var heldLine *AlertSummary
	for i := range res.Alerts {
		if res.Alerts[i].Action == "flap_held" {
			heldLine = &res.Alerts[i]
		}
	}
	if heldLine == nil {
		t.Fatalf("no flap_held line in envelope: %+v", res.Alerts)
	}
	wantID := NewAlertID("dr_flap", sid, dayN(13))
	if heldLine.AlertID != wantID || heldLine.State != "" || heldLine.FirstSeen != dayN(13) {
		t.Errorf("held line = %+v, want the deterministic would-be id %s / empty state / firstSeen Mar 13", heldLine, wantID)
	}
	// No 4th alert row: exactly the three cleared cycles remain.
	alerts := flapPairRows(t, db, "dr_flap", sid)
	if len(alerts) != 3 {
		t.Errorf("alert rows after hold = %d, want 3 (no 4th row)", len(alerts))
	}
	// A held fire writes no DOWN run-state: the audit history may keep the
	// three cycles' degraded→up transitions (past truth), but NOTHING at
	// or after the held window's first_seen (Mar 13) is degraded — a
	// series under flap hold is not "down".
	states, err := db.ResourceStatus.ListHistory(ctx, "res_flap", "", "")
	if err != nil {
		t.Fatalf("list run-states: %v", err)
	}
	for _, st := range states {
		if (st.RunState == domain.RunStateDown || st.RunState == domain.RunStateDegraded) && !st.TS.Before(time.Date(2026, 3, 13, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("degraded run-state during the hold: %+v", st)
		}
	}
	if n := len(states); n == 0 || states[n-1].RunState != domain.RunStateUp {
		t.Errorf("newest run-state = %+v, want up (the held sweep wrote no down)", states[n-1])
	}

	// Idempotent re-run: the hold repeats verbatim (no rows, no counters drift).
	res2, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("held re-run: %v", err)
	}
	if res2.FlapHeld != 1 || res2.AlertsCreated != 0 {
		t.Fatalf("held re-run = flapHeld %d / created %d, want 1/0 (deterministic)", res2.FlapHeld, res2.AlertsCreated)
	}

	// The violation PERSISTS (days 16-17): still held — the trailing run
	// extends back to Mar 13, the would-be id is unchanged.
	appendPoints(t, db, sid, dayN(16), []float64{-16.2, -16.3})
	res3, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("persist sweep: %v", err)
	}
	if res3.FlapHeld != 1 || res3.AlertsCreated != 0 {
		t.Fatalf("persist sweep = flapHeld %d / created %d, want 1/0", res3.FlapHeld, res3.AlertsCreated)
	}
	var heldLine2 *AlertSummary
	for i := range res3.Alerts {
		if res3.Alerts[i].Action == "flap_held" {
			heldLine2 = &res3.Alerts[i]
		}
	}
	if heldLine2 == nil || heldLine2.AlertID != wantID {
		t.Errorf("persist held line = %+v, want the SAME would-be id %s", heldLine2, wantID)
	}

	// Day 18: the oldest clear (Mar 4) has aged out of (Mar 4, Mar 18] —
	// only Mar 8 / Mar 12 remain (2 < 3) — the STILL-VIOLATING series
	// finally fires, landing the PROMISED identity (first_seen is still
	// Mar 13: the trailing violating run never broke).
	appendPoints(t, db, sid, dayN(18), []float64{-16.4})
	res4, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("release sweep: %v", err)
	}
	if res4.AlertsCreated != 1 || res4.FlapHeld != 0 {
		t.Fatalf("release sweep = created %d / flapHeld %d, want 1/0", res4.AlertsCreated, res4.FlapHeld)
	}
	var createdLine *AlertSummary
	for i := range res4.Alerts {
		if res4.Alerts[i].Action == "created" {
			createdLine = &res4.Alerts[i]
		}
	}
	if createdLine == nil || createdLine.AlertID != wantID {
		t.Errorf("released fire = %+v, want the held line's promised id %s", createdLine, wantID)
	}
	// The 4th row exists now, firing.
	alerts = flapPairRows(t, db, "dr_flap", sid)
	if len(alerts) != 4 {
		t.Errorf("alert rows after release = %d, want 4", len(alerts))
	}
}

// flapPairRows counts the (rule, series) pair's alert rows through the
// repo listing read path (unbounded, filtered in Go — the pair is the
// unit under test).
func flapPairRows(t *testing.T, db *repo.DB, ruleID, seriesID string) []repo.AlertRow {
	t.Helper()
	rows, _, err := db.Alerts.ListAlerts(context.Background(), repo.AlertFilter{Limit: 0})
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	out := make([]repo.AlertRow, 0, 4)
	for _, r := range rows {
		if r.RuleID == ruleID && r.SeriesID == seriesID {
			out = append(out, r)
		}
	}
	return out
}

// TestDetectFlapManualClearNotOscillation pins the narrow counting rule
// through the engine: three cycles seeded with WALL-CLOCK clears (the
// human edge — the operator clearing via POST /api/alerts/{id}/clear)
// do NOT damp the next fire. A manual decision is not a property of the
// series; only the engine's own recovery clears are oscillation.
func TestDetectFlapManualClearNotOscillation(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_manual", "DEV-MAN|GE-0/0/1")
	sid := seedSeries(t, db, "res_manual", "optical_power_rx_avg_dbm")
	insertRule(t, db, flapRule())

	// Three "manual" cycles: fires on the data timeline, clears stamped
	// with a far-ahead wall clock (2026-10-17 — the human path).
	for i, day := range []int{1, 5, 9} {
		first := time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
		if _, err := db.Alerts.InsertFiringAlert(ctx, domain.Alert{
			AlertID: NewAlertID("dr_flap", sid, first), RuleID: "dr_flap", SeriesID: sid,
			FiredAt: time.Date(2026, 3, day+2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			State:   domain.AlertStateFiring, LastValue: -16.0, FirstSeen: first,
		}); err != nil {
			t.Fatalf("seed firing %d: %v", i, err)
		}
		if err := db.Alerts.ClearAlert(ctx, NewAlertID("dr_flap", sid, first), "2026-10-17T09:00:00Z"); err != nil {
			t.Fatalf("seed clear %d: %v", i, err)
		}
	}

	// A fresh violating window (days 13-15): the pair has three cleared
	// rows, but none on the data timeline inside the span → fires.
	seedPoints(t, db, sid, "2026-03-13T00:00:00Z", []float64{-15.0, -15.5, -16.0}, domain.PointQualityOk)
	res, err := New().Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if res.AlertsCreated != 1 || res.FlapHeld != 0 {
		t.Errorf("created=%d flapHeld=%d, want 1/0 (manual clears are not oscillation)", res.AlertsCreated, res.FlapHeld)
	}
}

// TestDetectFlapPairIsolation pins that the damping verdict is scoped to
// the (rule, series) pair: a flapping series A holds ITS next fire in the
// same sweep that series B (no history) fires normally.
func TestDetectFlapPairIsolation(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_iso_a", "DEV-ISO|GE-0/0/1")
	seedResource(t, db, "res_iso_b", "DEV-ISO|GE-0/0/2")
	sidA := seedSeries(t, db, "res_iso_a", "optical_power_rx_avg_dbm")
	sidB := seedSeries(t, db, "res_iso_b", "optical_power_rx_avg_dbm")
	insertRule(t, db, flapRule())

	// Three cycles on series A only (days 1-12).
	for _, day := range []int{1, 5, 9} {
		first := time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
		if _, err := db.Alerts.InsertFiringAlert(ctx, domain.Alert{
			AlertID: NewAlertID("dr_flap", sidA, first), RuleID: "dr_flap", SeriesID: sidA,
			FiredAt: time.Date(2026, 3, day+2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			State:   domain.AlertStateFiring, LastValue: -16.0, FirstSeen: first,
		}); err != nil {
			t.Fatalf("seed firing %d: %v", day, err)
		}
		if err := db.Alerts.ClearAlert(ctx, NewAlertID("dr_flap", sidA, first),
			time.Date(2026, 3, day+3, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)); err != nil {
			t.Fatalf("seed clear %d: %v", day, err)
		}
	}

	// Days 13-15: BOTH series violate. A's fire is held (3 cycles), B's
	// fires (no history).
	seedPoints(t, db, sidA, "2026-03-13T00:00:00Z", []float64{-15.0, -15.5, -16.0}, domain.PointQualityOk)
	seedPoints(t, db, sidB, "2026-03-13T00:00:00Z", []float64{-15.0, -15.5, -16.0}, domain.PointQualityOk)
	res, err := New().Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if res.FlapHeld != 1 || res.AlertsCreated != 1 {
		t.Fatalf("created=%d flapHeld=%d, want 1/1 (B fires, A held)", res.AlertsCreated, res.FlapHeld)
	}
	heldA, firedB := false, false
	for _, line := range res.Alerts {
		if line.Action == "flap_held" && line.SeriesID == sidA {
			heldA = true
		}
		if line.Action == "created" && line.SeriesID == sidB {
			firedB = true
		}
	}
	if !heldA || !firedB {
		t.Errorf("envelope lines: heldA=%v firedB=%v, want true/true (%+v)", heldA, firedB, res.Alerts)
	}
}

// TestDetectFlapRecoveryNeverHeld pins the good-news doctrine on the flap
// gate: an ACTIVE alert of a pair with 3+ in-span transitions still
// clears when its series recovers — the hold only gates NEW fires, the
// recovery edge (branch 2 of the sync) has no flap check.
func TestDetectFlapRecoveryNeverHeld(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_rec", "DEV-REC|GE-0/0/1")
	sid := seedSeries(t, db, "res_rec", "optical_power_rx_avg_dbm")
	insertRule(t, db, flapRule())

	// Three cleared cycles (days 1-12) + one ACTIVE firing alert seeded
	// directly (days 13-15), then the series RECOVERS (day 16).
	for _, day := range []int{1, 5, 9} {
		first := time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
		if _, err := db.Alerts.InsertFiringAlert(ctx, domain.Alert{
			AlertID: NewAlertID("dr_flap", sid, first), RuleID: "dr_flap", SeriesID: sid,
			FiredAt: time.Date(2026, 3, day+2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			State:   domain.AlertStateFiring, LastValue: -16.0, FirstSeen: first,
		}); err != nil {
			t.Fatalf("seed firing %d: %v", day, err)
		}
		if err := db.Alerts.ClearAlert(ctx, NewAlertID("dr_flap", sid, first),
			time.Date(2026, 3, day+3, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)); err != nil {
			t.Fatalf("seed clear %d: %v", day, err)
		}
	}
	activeID := NewAlertID("dr_flap", sid, "2026-03-13T00:00:00Z")
	if _, err := db.Alerts.InsertFiringAlert(ctx, domain.Alert{
		AlertID: activeID, RuleID: "dr_flap", SeriesID: sid,
		FiredAt: "2026-03-15T00:00:00Z", State: domain.AlertStateFiring, LastValue: -16.0,
		FirstSeen: "2026-03-13T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed active alert: %v", err)
	}
	// Violating days 13-15 then a healthy tail at day 16: the active alert
	// clears in the SAME sweep whose finding budget is exhausted (3+).
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z",
		[]float64{-15.0, -15.5, -16.0, -9.0, -9.0, -9.0, -9.0, -15.0, -15.5, -16.0, -9.0, -9.0, -9.0, -9.0, -9.0, -9.0}, domain.PointQualityOk)

	res, err := New().Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if res.AlertsCleared != 1 {
		t.Fatalf("cleared = %d, want 1 (recovery is never held — good news doctrine)", res.AlertsCleared)
	}
	if res.FlapHeld != 0 || res.AlertsCreated != 0 {
		t.Errorf("flapHeld=%d created=%d, want 0/0 (no finding: the tail is healthy)", res.FlapHeld, res.AlertsCreated)
	}
}

// TestDetectFlapMaintenanceWins pins the gate ORDER: an active maintenance
// window is the operator's explicit plan — a finding under it is
// "suppressed" even when the pair is also flapping, never "flap_held".
func TestDetectFlapMaintenanceWins(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_mw", "DEV-MW|GE-0/0/1")
	sid := seedSeries(t, db, "res_mw", "optical_power_rx_avg_dbm")
	insertRule(t, db, flapRule())

	// A flapping pair (3 cycles, days 1-12) + a window covering the new
	// evidence (Mar 13-15).
	for _, day := range []int{1, 5, 9} {
		first := time.Date(2026, 3, day, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
		if _, err := db.Alerts.InsertFiringAlert(ctx, domain.Alert{
			AlertID: NewAlertID("dr_flap", sid, first), RuleID: "dr_flap", SeriesID: sid,
			FiredAt: time.Date(2026, 3, day+2, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			State:   domain.AlertStateFiring, LastValue: -16.0, FirstSeen: first,
		}); err != nil {
			t.Fatalf("seed firing %d: %v", day, err)
		}
		if err := db.Alerts.ClearAlert(ctx, NewAlertID("dr_flap", sid, first),
			time.Date(2026, 3, day+3, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)); err != nil {
			t.Fatalf("seed clear %d: %v", day, err)
		}
	}
	seedWindow(t, db, "mw_flap", []string{"res_mw"}) // Mar 5-10 (default)? see seedWindow: [Mar 5, Mar 10)
	// seedWindow's fixed bounds are [Mar 5, Mar 10); build the 4th cycle's
	// evidence INSIDE that window instead: points days 6-8 (the trailing
	// run's first_seen Mar 6, evidence ts Mar 8).
	seedPoints(t, db, sid, "2026-03-06T00:00:00Z", []float64{-15.0, -15.5, -16.0}, domain.PointQualityOk)

	res, err := New().Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if res.Suppressed != 1 || res.FlapHeld != 0 || res.AlertsCreated != 0 {
		t.Fatalf("suppressed=%d flapHeld=%d created=%d, want 1/0/0 (the window wins the verdict)", res.Suppressed, res.FlapHeld, res.AlertsCreated)
	}
	for _, line := range res.Alerts {
		if line.Action == "flap_held" {
			t.Errorf("flap_held line under an active window: %+v", line)
		}
	}
}
