package metrics

// #95 tail-seek pins: LatestOfResource must answer each series with
// ONLY its newest point (the indexed (series_id, ts) seek replaced the
// materialize-every-point-and-keep-the-last shape), an
// initialized-but-empty series answers Last = nil (not an error, not a
// zero point), and the keys filter selects series the same way
// SeriesOfResource does. The repo-level LatestPointOfSeries is pinned
// order-independent: newest wins regardless of insertion order.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// seedSeriesWithPoints seeds one resource × metric series with one
// point per day (value = f(dayIndex)). Unlike predict_test's
// seedDailyPoints it derives the batch id from resource × metric, so
// several metrics can be seeded onto the SAME resource.
func seedSeriesWithPoints(t *testing.T, db *repo.DB, resourceID, metricKey string, n int, f func(i int) float64) string {
	t.Helper()
	ctx := context.Background()
	seriesID, _, err := db.Metrics.FindOrCreateSeries(ctx, resourceID, metricKey, nil,
		"2026-08-01T00:00:00Z", "2026-08-01T00:00:00Z")
	if err != nil {
		t.Fatalf("FindOrCreateSeries: %v", err)
	}
	batchID := "mb_ts_" + resourceID + "_" + metricKey
	if err := db.MetricBatches.Create(ctx, domain.MetricBatch{
		ID: batchID, WorkbookName: "tailseek-seed.csv",
		WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Status:         domain.BatchStatusCompleted, RowsTotal: n, RowsImported: n,
		StartedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed metric batch: %v", err)
	}
	pts := make([]domain.MetricPoint, 0, n)
	for i := 0; i < n; i++ {
		pts = append(pts, domain.MetricPoint{
			SeriesID: seriesID, TS: fmt.Sprintf("2026-08-%02dT00:00:00Z", 1+i),
			Value: f(i), Quality: domain.PointQualityOk, BatchID: batchID,
		})
	}
	if err := db.Metrics.InsertPoints(ctx, pts); err != nil {
		t.Fatalf("InsertPoints: %v", err)
	}
	return seriesID
}

// TestLatestOfResourceTailSeek pins the #95 contract: a 30-day series
// answers exactly the LAST day's point — the tail seek, not the
// materialized history.
func TestLatestOfResourceTailSeek(t *testing.T) {
	svc, db := newMetricService(t)
	portID := seedPortInventory(t, db, "DEV-TS1", "10.0.0.1", "G1/0/1")
	seedSeriesWithPoints(t, db, portID, "optical_power_rx_avg_dbm", 30,
		func(i int) float64 { return -20 + 0.1*float64(i) })

	out, err := svc.LatestOfResource(context.Background(), portID, nil)
	if err != nil {
		t.Fatalf("LatestOfResource: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("series count = %d, want 1", len(out))
	}
	s := out[0]
	if s.MetricKey != "optical_power_rx_avg_dbm" || s.Unit != "dBm" {
		t.Fatalf("series = %s/%s, want optical_power_rx_avg_dbm/dBm", s.MetricKey, s.Unit)
	}
	if s.Last == nil {
		t.Fatalf("Last = nil, want the newest point")
	}
	if s.Last.TS != "2026-08-30T00:00:00Z" {
		t.Fatalf("Last.TS = %s, want 2026-08-30 (the tail, day 30 of 30)", s.Last.TS)
	}
	if s.Last.Value != -20+0.1*29 {
		t.Fatalf("Last.Value = %v, want %v (day 30's value)", s.Last.Value, -20+0.1*29)
	}
}

// TestLatestOfResourceEmptySeriesAnswersNilPoint pins the honest blank:
// a series that exists but never got a point answers Last = nil — the
// series is listed, no zero-point lie is invented.
func TestLatestOfResourceEmptySeriesAnswersNilPoint(t *testing.T) {
	svc, db := newMetricService(t)
	portID := seedPortInventory(t, db, "DEV-TS2", "10.0.0.2", "G1/0/1")
	if _, _, err := db.Metrics.FindOrCreateSeries(context.Background(), portID,
		"optical_power_rx_avg_dbm", nil, "2026-08-01T00:00:00Z", "2026-08-01T00:00:00Z"); err != nil {
		t.Fatalf("FindOrCreateSeries: %v", err)
	}

	out, err := svc.LatestOfResource(context.Background(), portID, nil)
	if err != nil {
		t.Fatalf("LatestOfResource of a pointless series: %v, want the series listed with Last=nil", err)
	}
	if len(out) != 1 || out[0].Last != nil {
		t.Fatalf("out = %+v, want one series with Last = nil (no points is not an error and not a fabricated point)", out)
	}
}

// TestLatestOfResourceKeysFilter pins that the keys filter selects
// series the same way the full listing enumerates them.
func TestLatestOfResourceKeysFilter(t *testing.T) {
	svc, db := newMetricService(t)
	portID := seedPortInventory(t, db, "DEV-TS3", "10.0.0.3", "G1/0/1")
	seedSeriesWithPoints(t, db, portID, "optical_power_rx_avg_dbm", 5, func(i int) float64 { return float64(i) })
	seedSeriesWithPoints(t, db, portID, "bandwidth_util_rx_pct", 5, func(i int) float64 { return 50 + float64(i) })

	out, err := svc.LatestOfResource(context.Background(), portID, []string{"bandwidth_util_rx_pct"})
	if err != nil {
		t.Fatalf("LatestOfResource(keys): %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("filtered series count = %d, want 1 (only bandwidth_util_rx_pct)", len(out))
	}
	if out[0].MetricKey != "bandwidth_util_rx_pct" || out[0].Last == nil || out[0].Last.Value != 54 {
		t.Fatalf("filtered out = %+v, want bandwidth_util_rx_pct with the day-5 value 54", out[0])
	}
}

// TestLatestPointOfSeriesOrderIndependent pins the repo seek itself:
// newest-by-ts wins regardless of the order the points were inserted.
func TestLatestPointOfSeriesOrderIndependent(t *testing.T) {
	_, db := newMetricService(t)
	portID := seedPortInventory(t, db, "DEV-TS4", "10.0.0.4", "G1/0/1")
	seriesID, _, err := db.Metrics.FindOrCreateSeries(context.Background(), portID,
		"module_temp_avg_c", nil, "2026-08-01T00:00:00Z", "2026-08-01T00:00:00Z")
	if err != nil {
		t.Fatalf("FindOrCreateSeries: %v", err)
	}
	batchID := "mb_ts_ord"
	if err := db.MetricBatches.Create(context.Background(), domain.MetricBatch{
		ID: batchID, WorkbookName: "order-seed.csv",
		WorkbookSHA256: "0000000000000000000000000000000000000000000000000000000000000000",
		Status:         domain.BatchStatusCompleted, RowsTotal: 3, RowsImported: 3,
		StartedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("seed metric batch: %v", err)
	}
	// Deliberately out of chronological order: 10th, 1st, 20th.
	for _, day := range []int{10, 1, 20} {
		if err := db.Metrics.InsertPoints(context.Background(), []domain.MetricPoint{{
			SeriesID: seriesID, TS: fmt.Sprintf("2026-08-%02dT00:00:00Z", day),
			Value: float64(day), Quality: domain.PointQualityOk, BatchID: batchID,
		}}); err != nil {
			t.Fatalf("InsertPoints(day %d): %v", day, err)
		}
	}
	p, err := db.Metrics.LatestPointOfSeries(context.Background(), seriesID)
	if err != nil {
		t.Fatalf("LatestPointOfSeries: %v", err)
	}
	if p.TS != "2026-08-20T00:00:00Z" || p.Value != 20 {
		t.Fatalf("latest = %s/%v, want 2026-08-20/20 (newest by ts, not by insertion)", p.TS, p.Value)
	}

	// The empty-series answer is the typed not-found the service layer
	// tolerates into Last = nil.
	emptyID, _, err := db.Metrics.FindOrCreateSeries(context.Background(), portID,
		"bias_current_ma", nil, "2026-08-01T00:00:00Z", "2026-08-01T00:00:00Z")
	if err != nil {
		t.Fatalf("FindOrCreateSeries(empty): %v", err)
	}
	if _, err := db.Metrics.LatestPointOfSeries(context.Background(), emptyID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("LatestPointOfSeries(empty) err = %v, want domain.ErrNotFound", err)
	}
}
