package metrics

// The D4 v1 math pins: exact linear series reproduce slope/intercept to
// floating-point tolerance; the four direction semantics answer the
// honest blanks; <14 daily points refuse; a series already beyond its
// bound answers 0 days. These run against a REAL database-backed
// service (newTestService seeds series through the ingest path), so the
// pins cover the fit AND the daily aggregation it rides.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// seedDailyPoints inserts n daily-average-equivalent raw points for one
// resource × metric: one point per day, value = f(dayIndex). The daily
// AVG aggregation of one point/day reproduces f exactly.
func seedDailyPoints(t *testing.T, db *repo.DB, resourceID, metricKey string, n int, f func(i int) float64) {
	t.Helper()
	ctx := context.Background()
	seriesID, _, err := db.Metrics.FindOrCreateSeries(ctx, resourceID, metricKey, nil,
		"2026-08-01T00:00:00Z", "2026-08-01T00:00:00Z")
	if err != nil {
		t.Fatalf("FindOrCreateSeries: %v", err)
	}
	// The points' batch FK needs its metric batch row first (one
	// synthetic completed batch per seeded series).
	batchID := "mb_fc_" + resourceID
	if err := db.MetricBatches.Create(ctx, domain.MetricBatch{
		ID: batchID, WorkbookName: "forecast-seed.csv",
		WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Status:         domain.BatchStatusCompleted, RowsTotal: n, RowsImported: n,
		StartedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed metric batch: %v", err)
	}
	pts := make([]domain.MetricPoint, 0, n)
	for i := 0; i < n; i++ {
		ts := fmt.Sprintf("2026-08-%02dT00:00:00Z", 1+i)
		pts = append(pts, domain.MetricPoint{
			SeriesID: seriesID, TS: ts, Value: f(i), Quality: domain.PointQualityOk, BatchID: batchID,
		})
	}
	if err := db.Metrics.InsertPoints(ctx, pts); err != nil {
		t.Fatalf("InsertPoints: %v", err)
	}
}

// TestForecastExactLinearTrend pins the fit itself: a perfectly linear
// daily series (value = 100 + 2·i over 30 days) reproduces slope 2 and
// intercept 100 to tolerance, zero residual SE, and the projections
// follow the same line with a symmetric band.
func TestForecastExactLinearTrend(t *testing.T) {
	svc, db := newMetricService(t)
	portID := seedPortInventory(t, db, "DEV-FC1", "10.0.0.1", "G1/0/1")
	seedDailyPoints(t, db, portID, "optical_power_rx_avg_dbm", 30, func(i int) float64 { return 100 + 2*float64(i) })

	out, err := svc.Forecast(context.Background(), portID, "optical_power_rx_avg_dbm", 7)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if out.ResourceID != portID || out.Points != 30 || out.FirstDay != "2026-08-01" || out.LastDay != "2026-08-30" {
		t.Fatalf("window = %d %s..%s, want 30 days 08-01..08-30", out.Points, out.FirstDay, out.LastDay)
	}
	if math.Abs(out.Slope-2) > 1e-9 || math.Abs(out.Intercept-100) > 1e-9 {
		t.Fatalf("fit slope=%v intercept=%v, want 2/100", out.Slope, out.Intercept)
	}
	if out.ResidualSE > 1e-9 || out.SlopeSE > 1e-9 {
		t.Fatalf("exact line must have zero residual/slope SE, got %v/%v", out.ResidualSE, out.SlopeSE)
	}
	if len(out.Projections) != 8 { // day 0 (last observed) + 7 ahead
		t.Fatalf("projections = %d entries, want 8", len(out.Projections))
	}
	if out.Projections[0].Day != "2026-08-30" || math.Abs(out.Projections[0].Value-158) > 1e-9 {
		t.Fatalf("projection[0] = %+v, want 2026-08-30 value 158", out.Projections[0])
	}
	if out.Projections[7].Day != "2026-09-06" || math.Abs(out.Projections[7].Value-172) > 1e-9 {
		t.Fatalf("projection[7] = %+v, want 2026-09-06 value 172", out.Projections[7])
	}
	if out.Note != ForecastNote {
		t.Fatalf("note = %q, want the D4 disclaimer", out.Note)
	}
}

// TestForecastDaysToThresholdHigherIsBetter: rx power (higher is better,
// min bound) falling 0.5/day from -10 crosses the advisory min at a
// computable whole-day count — the degradation warning D4 asks for.
func TestForecastDaysToThresholdHigherIsBetter(t *testing.T) {
	svc, db := newMetricService(t)
	spec, _ := SpecForKey("optical_power_rx_avg_dbm")
	if spec.Direction != domain.MetricDirectionHigherIsBetter || spec.MinValue == nil {
		t.Fatal("test premise: optical_power_rx_avg_dbm must be higher_is_better with a min bound")
	}
	// from -10 down to -16.5 over 14 days; min bound -30:
	// crossing at x where -10-0.5x = -30 → x = 40 → 40-13 = 27 days out.
	portID := seedPortInventory(t, db, "DEV-HB", "10.0.0.2", "G1/0/1")
	seedDailyPoints(t, db, portID, "optical_power_rx_avg_dbm", 14, func(i int) float64 { return -10 - 0.5*float64(i) })

	out, err := svc.Forecast(context.Background(), portID, "optical_power_rx_avg_dbm", 5)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if out.DaysToThreshold == nil || *out.DaysToThreshold != 27 {
		t.Fatalf("daysToThreshold = %v, want 27 (crossing x=40, last x=13)", out.DaysToThreshold)
	}
	if out.Threshold == nil || out.Threshold.Bound != ThresholdAtMin || out.Threshold.Value != *spec.MinValue {
		t.Fatalf("threshold = %+v, want the min bound", out.Threshold)
	}
}

// TestForecastDaysToThresholdLowerIsBetter: utilization (lower is
// better, max bound) RISING toward the max is the mirror semantics.
func TestForecastDaysToThresholdLowerIsBetter(t *testing.T) {
	svc, db := newMetricService(t)
	spec, ok := SpecForKey("packet_loss_rx_pct")
	if !ok || spec.Direction != domain.MetricDirectionLowerIsBetter || spec.MaxValue == nil {
		t.Skip("no lower_is_better key with a max bound in the catalog — premise unmet")
	}
	// 2..28% over 14 days, rising 2/day (fit: intercept 2, slope 2);
	// max bound 100: crossing at x = (100-2)/2 = 49 → 49-13 = 36 days out.
	portID := seedPortInventory(t, db, "DEV-LB", "10.0.0.3", "G1/0/1")
	seedDailyPoints(t, db, portID, "packet_loss_rx_pct", 14, func(i int) float64 { return 2 + 2*float64(i) })
	out, err := svc.Forecast(context.Background(), portID, "packet_loss_rx_pct", 5)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if out.DaysToThreshold == nil || *out.DaysToThreshold != 36 {
		t.Fatalf("daysToThreshold = %d, want 36", *out.DaysToThreshold)
	}
	if out.Threshold.Bound != ThresholdAtMax {
		t.Fatalf("threshold bound = %q, want max", out.Threshold.Bound)
	}
}

// TestForecastRefusesUnder14Points: D4's own refusal — 13 daily points
// answer ErrForecastNotEnoughData, never a shrunk fit.
func TestForecastRefusesUnder14Points(t *testing.T) {
	svc, db := newMetricService(t)
	portID := seedPortInventory(t, db, "DEV-THIN", "10.0.0.4", "G1/0/1")
	seedDailyPoints(t, db, portID, "optical_power_rx_avg_dbm", 13, func(i int) float64 { return float64(i) })

	_, err := svc.Forecast(context.Background(), portID, "optical_power_rx_avg_dbm", 7)
	if !errors.Is(err, ErrForecastNotEnoughData) {
		t.Fatalf("13-point forecast err = %v, want ErrForecastNotEnoughData", err)
	}
	// and no series at all answers the same honest class.
	_, err = svc.Forecast(context.Background(), "res_nosuch", "optical_power_rx_avg_dbm", 7)
	if !errors.Is(err, ErrForecastNotEnoughData) {
		t.Fatalf("no-series forecast err = %v, want ErrForecastNotEnoughData", err)
	}
}

// TestForecastAlreadyBeyondBoundIsZeroDays: the last observed value
// already under the min bound answers 0 days — detection's business
// today; the forecast refuses to "predict" the present.
func TestForecastAlreadyBeyondBoundIsZeroDays(t *testing.T) {
	svc, db := newMetricService(t)
	spec, _ := SpecForKey("optical_power_rx_avg_dbm")
	portID := seedPortInventory(t, db, "DEV-OVER", "10.0.0.5", "G1/0/1")
	seedDailyPoints(t, db, portID, "optical_power_rx_avg_dbm", 14, func(i int) float64 {
		return *spec.MinValue - 10 - float64(i) // deep below the bound, falling
	})
	out, err := svc.Forecast(context.Background(), portID, "optical_power_rx_avg_dbm", 3)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if out.DaysToThreshold == nil || *out.DaysToThreshold != 0 {
		t.Fatalf("daysToThreshold = %v, want 0 (already beyond)", out.DaysToThreshold)
	}
}

// TestForecastMovingAwayFromBoundIsHonestNil: a series IMPROVING (moving
// away from the degradation bound) must answer nil, not a negative day
// count — no fabricated warnings on healthy trends.
func TestForecastMovingAwayFromBoundIsHonestNil(t *testing.T) {
	svc, db := newMetricService(t)
	portID := seedPortInventory(t, db, "DEV-AWAY", "10.0.0.6", "G1/0/1")
	seedDailyPoints(t, db, portID, "optical_power_rx_avg_dbm", 14, func(i int) float64 { return -10 + 0.5*float64(i) })
	out, err := svc.Forecast(context.Background(), portID, "optical_power_rx_avg_dbm", 3)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if out.DaysToThreshold != nil {
		t.Fatalf("improving trend daysToThreshold = %v, want nil", *out.DaysToThreshold)
	}
}
