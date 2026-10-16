package api_test

// flap_held_test.go — pins the v0.12-R17 flap-damping read surfaces
// through the REAL HTTP channel: the POST /api/detection/run envelope
// carries flapHeld and the flap_held mutation lines (would-be identity),
// and GET /api/detection/runs echoes the counter on the receipt rows.
// The engine semantics themselves are pinned in internal/detect/flap_test.go
// — this file only proves the wire contract.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/detect"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// seedFlapPair populates one flapping (rule, series) pair directly through
// the repository: three completed fire→clear cycles with DATA-timeline
// clears (the engine recovery shape), plus a fresh violating window whose
// 4th fire the next sweep must hold.
func seedFlapPair(t *testing.T, db *repo.DB) string {
	t.Helper()
	ctx := context.Background()
	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	day := func(n int) string {
		return time.Date(2026, 3, n, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	}
	if err := db.Do(ctx, func(tx *repo.Tx) error {
		if err := tx.Batches.Create(ctx, domain.ImportBatch{
			ID: "bat_flap", WorkbookName: "flap.xlsx",
			WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
			Status:         domain.BatchStatusCompleted, RuleVersion: "seed", StartedAt: ts,
		}); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_flap", Type: domain.ResourceTypePort, NaturalKey: "DEV-FLAP|GE-0/0/1",
			Name: "DEV-FLAP|GE-0/0/1", Attributes: map[string]string{},
			FirstBatchID: "bat_flap", CreatedAt: ts, UpdatedAt: ts,
		})
	}); err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	sid, _, err := db.Metrics.FindOrCreateSeries(ctx, "res_flap", "optical_power_rx_avg_dbm", nil, "", "")
	if err != nil {
		t.Fatalf("seed series: %v", err)
	}
	if err := db.DetectionRules.InsertRule(ctx, domain.DetectionRule{
		RuleID: "dr_flap_api", MetricKey: "optical_power_rx_avg_dbm", Comparator: domain.ComparatorLte,
		Threshold: -14.0, Window: 3, Severity: domain.SeverityWarning, Enabled: true,
		Label: "接收光功率持续偏低", CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	// Three completed cycles: fires at days 3/7/11, clears at days 4/8/12
	// (data-timeline ts — the engine's recovery edge shape).
	for _, d := range []int{1, 5, 9} {
		first := day(d)
		_, err := db.Alerts.InsertFiringAlert(ctx, domain.Alert{
			AlertID: detect.NewAlertID("dr_flap_api", sid, first), RuleID: "dr_flap_api", SeriesID: sid,
			FiredAt: day(d + 2), State: domain.AlertStateFiring, LastValue: -16.0, FirstSeen: first,
		})
		if err != nil {
			t.Fatalf("seed firing alert day %d: %v", d, err)
		}
		if err := db.Alerts.ClearAlert(ctx, detect.NewAlertID("dr_flap_api", sid, first), day(d+3)); err != nil {
			t.Fatalf("seed cleared alert day %d: %v", d, err)
		}
	}
	// The 4th violating window (days 13-15): 6 points, one batch.
	if err := db.MetricBatches.Create(ctx, domain.MetricBatch{
		ID: "mbat_flap", WorkbookName: "flap.xlsx",
		WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Status:         domain.BatchStatusCompleted, RowsTotal: 3, RowsImported: 3, StartedAt: ts,
	}); err != nil {
		t.Fatalf("seed metric batch: %v", err)
	}
	pts := make([]domain.MetricPoint, 0, 3)
	for i, v := range []float64{-15.0, -15.5, -16.0} {
		pts = append(pts, domain.MetricPoint{
			SeriesID: sid, TS: day(13 + i), Value: v, Quality: domain.PointQualityOk, BatchID: "mbat_flap",
		})
	}
	if err := db.Metrics.InsertPoints(ctx, pts); err != nil {
		t.Fatalf("seed points: %v", err)
	}
	return sid
}

// TestDetectionRunFlapHeldContract pins the wire shape: the run envelope
// carries flapHeld + the flap_held line (would-be alertId, empty state),
// and the persisted receipt row echoes the counter in the runs listing.
func TestDetectionRunFlapHeldContract(t *testing.T) {
	srv, a := newDetectTestServerWithApp(t)
	_ = seedFlapPair(t, a.DB)

	// The held sweep through the real HTTP channel.
	resp, body := doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detection run: %d\n%s", resp.StatusCode, body)
	}
	var env struct {
		runEnvelope
		FlapHeld      int `json:"flapHeld"`
		AlertsCreated int `json:"alertsCreated"`
		Alerts        []struct {
			AlertID string `json:"alertId"`
			Action  string `json:"action"`
			State   string `json:"state"`
		} `json:"alerts"`
	}
	decodeJSON(t, "detection run", body, &env)
	if env.FlapHeld != 1 || env.AlertsCreated != 0 {
		t.Fatalf("envelope flapHeld=%d alertsCreated=%d, want 1/0 (the 4th fire is held)", env.FlapHeld, env.AlertsCreated)
	}
	held := false
	for _, line := range env.Alerts {
		if line.Action == "flap_held" {
			held = true
			if line.State != "" {
				t.Errorf("flap_held line state = %q, want empty (no alert row)", line.State)
			}
			if line.AlertID == "" {
				t.Error("flap_held line alertId empty, want the deterministic would-be id")
			}
		}
	}
	if !held {
		t.Errorf("no flap_held line in the envelope: %+v", env.Alerts)
	}

	// The receipt row echoes the counter.
	resp, body = doRequest(t, srv, http.MethodGet, "/api/detection/runs?page=1&pageSize=5", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("runs list: %d\n%s", resp.StatusCode, body)
	}
	var listed struct {
		Items []struct {
			RunID    string `json:"runId"`
			FlapHeld int    `json:"flapHeld"`
			Error    string `json:"error"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeJSON(t, "runs list", body, &listed)
	if listed.Total == 0 || len(listed.Items) == 0 {
		t.Fatalf("runs list empty, want the receipt row")
	}
	if listed.Items[0].RunID != env.RunID {
		t.Errorf("newest run = %s, want %s", listed.Items[0].RunID, env.RunID)
	}
	if listed.Items[0].FlapHeld != 1 {
		t.Errorf("listed flapHeld = %d, want 1 (the counter is durable)", listed.Items[0].FlapHeld)
	}
	if listed.Items[0].Error != "" {
		t.Errorf("listed error = %q, want empty", listed.Items[0].Error)
	}
}

// TestDetectionRunFlapHeldZeroShape pins the additive contract: an
// ordinary sweep (no oscillation) reports flapHeld=0 — the field is
// ALWAYS present, never omitted (the strict-client doctrine).
func TestDetectionRunFlapHeldZeroShape(t *testing.T) {
	srv := newDetectTestServer(t)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detection run: %d\n%s", resp.StatusCode, body)
	}
	var raw map[string]json.RawMessage
	decodeJSON(t, "detection run", body, &raw)
	if _, ok := raw["flapHeld"]; !ok {
		t.Fatal("flapHeld missing from the run envelope, want always present")
	}
	var held int
	decodeJSON(t, "flapHeld", raw["flapHeld"], &held)
	if held != 0 {
		t.Errorf("flapHeld = %d, want 0 (nothing flapping)", held)
	}
}
