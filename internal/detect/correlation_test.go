package detect

import (
	"context"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// seedTypedResource inserts one resource of an arbitrary type (the file's
// seedResource pins port; the correlation tests need cross-layer types).
func seedTypedResource(t *testing.T, db *repo.DB, id string, typ domain.ResourceType, naturalKey string) {
	t.Helper()
	ctx := context.Background()
	batID := "bat_" + id
	err := db.Do(ctx, func(tx *repo.Tx) error {
		if err := tx.Batches.Create(ctx, newDetectBatch(batID)); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: id, Type: typ, NaturalKey: naturalKey,
			Name: naturalKey, Attributes: map[string]string{},
			FirstBatchID: batID, CreatedAt: detectTS, UpdatedAt: detectTS,
		})
	})
	if err != nil {
		t.Fatalf("seed resource %s: %v", id, err)
	}
}

// seedRelation inserts one relation (the graph edge the correlation pass
// walks).
func seedRelation(t *testing.T, db *repo.DB, id string, typ domain.RelationType, from, to string) {
	t.Helper()
	if err := db.Relations.Insert(context.Background(), domain.Relation{
		ID: id, Type: typ, FromResourceID: from, ToResourceID: to,
		FirstBatchID: "bat_" + from, CreatedAt: detectTS,
	}); err != nil {
		t.Fatalf("seed relation %s: %v", id, err)
	}
}

// TestDetectCorrelationPrimarySecondary pins the 根因/次生 verdict end to
// end against a real graph: a circuit (L3) hanging off a device (L1, the
// cause direction is DOWN the layer ladder). When BOTH alert:
//
//   - the circuit's alert is SECONDARY, linked to the device's alert
//     (the nearest alerting cause — depth, then layer, then id);
//   - the device's alert is PRIMARY (an alerting dependent sits above);
//   - the run envelope's created lines carry the same verdicts;
//   - re-running over unchanged data re-writes the same verdicts
//     (idempotent content-wise, 0 mutations);
//   - when the device's alert clears (its series recovers), the next run
//     re-verdicts the circuit to UNMARKED — observation state follows the
//     live alert set, and the cleared device alert KEEPS its primary mark
//     (the audit truth of what it looked like while it lived).
func TestDetectCorrelationPrimarySecondary(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	eng := New()

	seedTypedResource(t, db, "res_corr_dev", domain.ResourceTypeDevice, "DEV-CORR")
	seedTypedResource(t, db, "res_corr_cir", domain.ResourceTypeCircuit, "CIR-CORR")
	// The circuit hangs off the device (a_end) — the classic L3 → L1 edge.
	seedRelation(t, db, "rel_corr_1", domain.RelationTypeAEnd, "res_corr_cir", "res_corr_dev")
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_corr", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 1, Severity: domain.SeverityWarning, Enabled: true,
	})
	sidDev := seedSeries(t, db, "res_corr_dev", "optical_power_rx_avg_dbm")
	sidCir := seedSeries(t, db, "res_corr_cir", "optical_power_rx_avg_dbm")
	seedPoints(t, db, sidDev, "2026-03-01T00:00:00Z", []float64{-16, -17, -18}, domain.PointQualityOk)
	seedPoints(t, db, sidCir, "2026-03-01T00:00:00Z", []float64{-16, -17, -18}, domain.PointQualityOk)

	// First sweep: both series violate → both fire → correlation runs.
	res, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if res.AlertsCreated != 2 {
		t.Fatalf("created = %d, want 2 (device + circuit)", res.AlertsCreated)
	}
	var devLine, cirLine *AlertSummary
	for i := range res.Alerts {
		switch res.Alerts[i].SeriesID {
		case sidDev:
			devLine = &res.Alerts[i]
		case sidCir:
			cirLine = &res.Alerts[i]
		}
	}
	if devLine == nil || cirLine == nil {
		t.Fatalf("envelope lines missing: %+v", res.Alerts)
	}
	if cirLine.CorrelationRole != "secondary" {
		t.Errorf("circuit created line correlationRole = %q, want secondary", cirLine.CorrelationRole)
	}
	if devLine.CorrelationRole != "primary" {
		t.Errorf("device created line correlationRole = %q, want primary", devLine.CorrelationRole)
	}

	// The rows carry the same verdicts; the secondary links the device alert.
	devAlert, err := db.Alerts.GetAlertByID(ctx, devLine.AlertID)
	if err != nil {
		t.Fatalf("get device alert: %v", err)
	}
	cirAlert, err := db.Alerts.GetAlertByID(ctx, cirLine.AlertID)
	if err != nil {
		t.Fatalf("get circuit alert: %v", err)
	}
	if devAlert.CorrelationRole != "primary" || devAlert.CorrelatedAlertID != "" {
		t.Errorf("device alert row = role %q link %q, want primary / empty", devAlert.CorrelationRole, devAlert.CorrelatedAlertID)
	}
	if cirAlert.CorrelationRole != "secondary" || cirAlert.CorrelatedAlertID != devAlert.AlertID ||
		cirAlert.CorrelatedResourceID != "res_corr_dev" {
		t.Errorf("circuit alert row = %+v %+v %+v, want secondary linked to the device alert",
			cirAlert.CorrelationRole, cirAlert.CorrelatedAlertID, cirAlert.CorrelatedResourceID)
	}

	// Idempotent re-run: 0 mutations, the verdicts hold.
	res2, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if res2.AlertsCreated != 0 || res2.AlertsCleared != 0 {
		t.Errorf("re-run mutations = %d/%d, want 0/0", res2.AlertsCreated, res2.AlertsCleared)
	}
	cirAlert2, err := db.Alerts.GetAlertByID(ctx, cirLine.AlertID)
	if err != nil {
		t.Fatalf("re-get circuit alert: %v", err)
	}
	if cirAlert2.CorrelationRole != "secondary" || cirAlert2.CorrelatedAlertID != devAlert.AlertID {
		t.Errorf("re-run circuit verdict = %+v → %+v, want stable secondary", cirAlert.CorrelationRole, cirAlert2.CorrelationRole)
	}

	// The device recovers: its alert clears, the circuit's verdict
	// re-computes to unmarked, and the CLEARED device alert keeps its
	// primary mark (audit truth, never rewritten after clear).
	appendPoints(t, db, sidDev, "2026-03-04T00:00:00Z", []float64{-1, -1})
	res3, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("recovery detect: %v", err)
	}
	if res3.AlertsCleared != 1 {
		t.Errorf("recovery cleared = %d, want 1 (the device alert)", res3.AlertsCleared)
	}
	cirAlert3, err := db.Alerts.GetAlertByID(ctx, cirLine.AlertID)
	if err != nil {
		t.Fatalf("re-get circuit alert 3: %v", err)
	}
	if cirAlert3.CorrelationRole != "" || cirAlert3.CorrelatedAlertID != "" {
		t.Errorf("circuit verdict after cause cleared = %q/%q, want unmarked", cirAlert3.CorrelationRole, cirAlert3.CorrelatedAlertID)
	}
	devAlert3, err := db.Alerts.GetAlertByID(ctx, devLine.AlertID)
	if err != nil {
		t.Fatalf("re-get device alert 3: %v", err)
	}
	if devAlert3.State != domain.AlertStateCleared || devAlert3.CorrelationRole != "primary" {
		t.Errorf("cleared device alert = state %s role %q, want cleared / primary (kept)", devAlert3.State, devAlert3.CorrelationRole)
	}
}

// TestDetectCorrelationLateralGuard pins the anti-symmetry guard: two
// circuits on the SAME device (same layer, lateral) alerting together —
// with the device silent — never mark each other; both stay unmarked
// (siblings are symptoms of a common cause, not each other's causes).
func TestDetectCorrelationLateralGuard(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	eng := New()

	seedTypedResource(t, db, "res_lat_dev", domain.ResourceTypeDevice, "DEV-LAT")
	seedTypedResource(t, db, "res_lat_c1", domain.ResourceTypeCircuit, "CIR-LAT-1")
	seedTypedResource(t, db, "res_lat_c2", domain.ResourceTypeCircuit, "CIR-LAT-2")
	seedRelation(t, db, "rel_lat_1", domain.RelationTypeAEnd, "res_lat_c1", "res_lat_dev")
	seedRelation(t, db, "rel_lat_2", domain.RelationTypeAEnd, "res_lat_c2", "res_lat_dev")
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_lat", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 1, Severity: domain.SeverityWarning, Enabled: true,
	})
	sidC1 := seedSeries(t, db, "res_lat_c1", "optical_power_rx_avg_dbm")
	sidC2 := seedSeries(t, db, "res_lat_c2", "optical_power_rx_avg_dbm")
	seedPoints(t, db, sidC1, "2026-03-01T00:00:00Z", []float64{-16, -17, -18}, domain.PointQualityOk)
	seedPoints(t, db, sidC2, "2026-03-01T00:00:00Z", []float64{-16, -17, -18}, domain.PointQualityOk)

	res, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if res.AlertsCreated != 2 {
		t.Fatalf("created = %d, want 2 (the lateral circuits)", res.AlertsCreated)
	}
	for _, line := range res.Alerts {
		if line.CorrelationRole != "" {
			t.Errorf("lateral circuit %s correlationRole = %q, want unmarked (the sibling guard)", line.SeriesID, line.CorrelationRole)
		}
	}
	alerts, err := db.Alerts.ListActiveAlertsByRule(ctx, "dr_lat")
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	for _, a := range alerts {
		if a.CorrelationRole != "" || a.CorrelatedAlertID != "" {
			t.Errorf("lateral alert row %s = %q/%q, want unmarked", a.AlertID, a.CorrelationRole, a.CorrelatedAlertID)
		}
	}
}

// TestDetectCorrelationInfoNeverParticipates pins the severity gate: an
// info alert explains nothing — the warning alert below it stays primary
// only via OTHER evidence, and the info alert itself never becomes
// secondary of the warning (info never enters the alerting set).
func TestDetectCorrelationInfoNeverParticipates(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	eng := New()

	seedTypedResource(t, db, "res_info_dev", domain.ResourceTypeDevice, "DEV-INFO")
	seedTypedResource(t, db, "res_info_cir", domain.ResourceTypeCircuit, "CIR-INFO")
	seedRelation(t, db, "rel_info_1", domain.RelationTypeAEnd, "res_info_cir", "res_info_dev")
	// Two rules, two severities: warning on the device (L1, the cause
	// side), info on the circuit (L3, the symptom side).
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_info_w", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 1, Severity: domain.SeverityWarning, Enabled: true,
	})
	insertRule(t, db, domain.DetectionRule{
		RuleID: "dr_info_i", MetricKey: "packet_loss_rx_pct", Comparator: domain.ComparatorGte,
		Threshold: 0.5, Window: 1, Severity: domain.SeverityInfo, Enabled: true,
	})
	sidDev := seedSeries(t, db, "res_info_dev", "optical_power_rx_avg_dbm")
	sidCirPL := seedSeries(t, db, "res_info_cir", "packet_loss_rx_pct")
	seedPoints(t, db, sidDev, "2026-03-01T00:00:00Z", []float64{-16, -17, -18}, domain.PointQualityOk)
	seedPoints(t, db, sidCirPL, "2026-03-01T00:00:00Z", []float64{2, 3, 4}, domain.PointQualityOk)

	res, err := eng.Detect(ctx, db, Options{})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if res.AlertsCreated != 2 {
		t.Fatalf("created = %d, want 2 (warning + info)", res.AlertsCreated)
	}
	for _, line := range res.Alerts {
		if line.CorrelationRole != "" {
			t.Errorf("line %s (%s) correlationRole = %q, want unmarked — info never participates, and without it the warning has no alerting dependent",
				line.RuleID, line.Severity, line.CorrelationRole)
		}
	}
}
