package repo

// metrics_scale_probe_test.go — the #27 stage-2 measurement harness
// (时序量级实测), living in the REPO package because it measures the
// STORAGE layer: batched InsertPoints, the §5.5 query shapes as raw SQL
// (the package's own reads), and the database file's bytes.
//
// The issue's acceptance boxes demand NUMBERS before the retention/
// aggregation decision: ingest time, query time, DB size — measured on
// synthetic data, then the per-row numbers extrapolate to the §3.2
// scale (20 万 ports × 10 metrics × 365 days ≈ 7.3e8 rows).
//
// The probe is env-guarded: it skips in every normal test run and only
// executes when the operator asks for it, e.g.
//
//	METRICS_SCALE_PROBE=10000,90 go test ./internal/repo/ -run TestMetricsScaleProbe -count=1 -timeout 30m
//	METRICS_SCALE_PROBE=40000,90 go test ./internal/repo/ -run TestMetricsScaleProbe -count=1 -timeout 60m
//
// (two scales prove the linearity the extrapolation relies on — two
// points on the line). Output: ONE JSON line on stdout, the shape the
// measurement script parses and archives:
//
//	{"series":10000,"days":90,"points":900000,"ingestMsP50":...,
//	 "ingestMsP95":...,"ingestMsPerDayMax":...,"q1Ms":...,"q2Ms":...,
//	 "q3Ms":...,"dbBytes":...,"bytesPerRow":...,"retentionDeleteMs":...,
//	 "dbBytesAfterDelete":...}
//
// What is measured and why:
//   - ingest: per-DAY batched InsertPoints (the storage-layer upsert the
//     issue asks about — "每日导入这批数据耗时多少（批量 upsert）";
//     the xlsx parse overhead belongs to the workbook layer and is
//     quoted separately from the small-scale ingest tests);
//   - Q1 (single series, whole-window trend): the degradation-trend
//     shape of §5.5 — must stay index-served as the table grows;
//   - Q2 (full scan over the window, grouped per series): the daily-
//     inspection worst case — the query the issue fears will time out;
//   - Q3 (latest value of one series): the AI context shape — a single
//     seek after #95's LatestPointOfSeries fix; the N-series resource
//     shape scales by N (noted in the analysis, not re-measured);
//   - size: the file bytes after the run → bytes/row (WITHOUT ROWID
//     clustering keeps this flat);
//   - retention: one DELETE of the oldest window slice (plan A's
//     90-day-retention shape) — the operator-command cost, and the size
//     delta (SQLite reuses freed pages; the file shrinks on VACUUM,
//     which the probe deliberately does NOT run — the cost line quotes
//     the honest no-vacuum shape and the analysis says so).
//
// Values drift downward on exactly 1/10 of the series so Q2 has real
// work; the rest stay flat — the realistic mix, not a worst-case set.

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

func TestMetricsScaleProbe(t *testing.T) {
	raw := os.Getenv("METRICS_SCALE_PROBE")
	if raw == "" {
		t.Skip("set METRICS_SCALE_PROBE=<series>,<days> to run the #27 stage-2 measurement (e.g. 10000,90)")
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		t.Fatalf("METRICS_SCALE_PROBE must be <series>,<days>, got %q", raw)
	}
	seriesCount, err1 := strconv.Atoi(parts[0])
	days, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || seriesCount < 100 || days < 7 {
		t.Fatalf("METRICS_SCALE_PROBE = %q: series >= 100 and days >= 7 required", raw)
	}
	ctx := context.Background()
	dir := testsupport.TempDir(t)
	db, err := Open(filepath.Join(dir, "netops.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	// ---- seed: one resource per series (the series FK demands it), one
	// synthetic import batch as the lineage anchor. The seed cost is NOT
	// part of the ingest measurement.
	if err := db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, domain.ImportBatch{
			ID: "bat_probe", WorkbookName: "probe.xlsx", WorkbookSHA256: "probe",
			Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: domain.RuleVersion,
		}); err != nil {
			return err
		}
		// the metric key catalog row (metric_series carries a real FK to
		// metric_keys — the ingest channel normally seeds it via the
		// mapping; the probe seeds the one key it writes).
		if _, err := tx.Resources.q.ExecContext(ctx, `INSERT INTO metric_keys
(metric_key, label, unit, value_kind, direction, created_at)
VALUES ('optical_power_rx_dbm', '接收光功率', 'dBm', 'gauge', 'higher_is_better', '2026-01-01T00:00:00Z')`); err != nil {
			return err
		}
		const chunk = 1000
		for start := 0; start < seriesCount; start += chunk {
			end := start + chunk
			if end > seriesCount {
				end = seriesCount
			}
			var sb strings.Builder
			sb.WriteString(`INSERT INTO resources (id, resource_type, natural_key, name, status, attributes, first_batch_id, created_at, updated_at) VALUES `)
			args := make([]any, 0, (end-start)*9)
			for i := start; i < end; i++ {
				if i > start {
					sb.WriteByte(',')
				}
				sb.WriteString("(?, 'device', ?, ?, '在网', '{}', 'bat_probe', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')")
				args = append(args, fmt.Sprintf("res_probe_%06d", i), fmt.Sprintf("DEV-P%06d", i), fmt.Sprintf("DEV-P%06d", i))
			}
			if _, err := tx.Resources.q.ExecContext(ctx, sb.String(), args...); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	seriesIDs := make([]string, seriesCount)
	for i := 0; i < seriesCount; i++ {
		sid, _, err := db.Metrics.FindOrCreateSeries(ctx, fmt.Sprintf("res_probe_%06d", i),
			"optical_power_rx_dbm", nil, "2026-01-01T00:00:00Z", "2026-12-31T00:00:00Z")
		if err != nil {
			t.Fatalf("series %d: %v", i, err)
		}
		seriesIDs[i] = sid
	}

	// ---- ingest: one INSERT batch per day (the daily workbook's shape).
	perDayMs := make([]int, 0, days)
	totalPoints := 0
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for d := 0; d < days; d++ {
		day := start.AddDate(0, 0, d)
		ts := day.Format(time.RFC3339)
		batchID := fmt.Sprintf("mbat_probe_%03d", d)
		if err := db.MetricBatches.Create(ctx, domain.MetricBatch{
			ID: batchID, WorkbookName: fmt.Sprintf("perf-day-%02d.xlsx", d+1),
			WorkbookSHA256: fmt.Sprintf("probe%03d", d), Status: domain.BatchStatusCompleted,
			Mapping: "{}", StartedAt: day,
		}); err != nil {
			t.Fatalf("metric batch %d: %v", d, err)
		}
		points := make([]domain.MetricPoint, 0, seriesCount)
		for i, sid := range seriesIDs {
			v := -18.5
			if i%10 == 0 {
				v = -18.5 - float64(d)*0.05 // the degrading tenth (crosses the 1.0 decline bar by day 21)
			}
			points = append(points, domain.MetricPoint{
				SeriesID: sid, TS: ts, Value: v, Quality: domain.PointQualityOk, BatchID: batchID,
			})
		}
		t0 := time.Now()
		if err := db.Metrics.InsertPoints(ctx, points); err != nil {
			t.Fatalf("insert day %d: %v", d, err)
		}
		perDayMs = append(perDayMs, int(time.Since(t0).Milliseconds()))
		totalPoints += len(points)
	}

	// ---- queries, 3 runs each (the median is the quoted number; the
	// warm cache is the honest steady-state after the first scan).
	median := func(xs []int) int {
		sort.Ints(xs)
		return xs[len(xs)/2]
	}
	runMS := func(fn func() error) int {
		var runs []int
		for i := 0; i < 3; i++ {
			t0 := time.Now()
			if err := fn(); err != nil {
				t.Fatalf("query: %v", err)
			}
			runs = append(runs, int(time.Since(t0).Milliseconds()))
		}
		return median(runs)
	}
	probeSeries := seriesIDs[0]
	q1 := runMS(func() error {
		_, err := db.Metrics.PointsOfSeries(ctx, probeSeries, "2026-01-01T00:00:00Z", "2026-12-31T23:59:59Z")
		return err
	})
	// Q2 comes in TWO shapes — the design doc's fear ("the daily
	// inspection full scan will time out") is answerable only against
	// both. The doc's printed SQL (HAVING MAX-MIN < -1.0) is a latent
	// defect: MAX-MIN is always >= 0, the predicate can NEVER fire —
	// the writeback of this round fixes the printed form to the
	// first/last difference.
	//
	// Q2a (the naive shape the doc feared): window functions over the
	// window slice — a genuine full scan of the in-window rows. NOTE the
	// SQL subtlety the probe itself stepped on: window functions
	// evaluate AFTER grouping, so mixing COUNT(*) into the same SELECT
	// collapses every partition to one post-aggregate row and
	// first/last silently degenerate — the window-only form below is the
	// correct one (DISTINCT dedupes the per-row projections).
	windowEnd := start.AddDate(0, 0, days-1).Format("2006-01-02") + "T23:59:59Z"
	wantFlagged := seriesCount / 10
	q2a := runMS(func() error {
		rows, err := db.sql.QueryContext(ctx, `
SELECT DISTINCT series_id, decline FROM (
  SELECT series_id,
         FIRST_VALUE(value) OVER w - LAST_VALUE(value) OVER w AS decline
  FROM metrics
  WHERE ts >= ? AND ts <= ?
  WINDOW w AS (PARTITION BY series_id ORDER BY ts
               ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)
) WHERE decline > 1.0`,
			"2026-01-01T00:00:00Z", windowEnd)
		if err != nil {
			return err
		}
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		if n != wantFlagged {
			return fmt.Errorf("Q2a flagged %d series, want the degrading tenth (%d)", n, wantFlagged)
		}
		return rows.Err()
	})
	// Q2b (the series-driven shape the writeback recommends): two
	// ordered seeks per series on the (series_id, ts) WITHOUT ROWID key
	// — O(series × log points), never touching the rows between the
	// endpoints. This is the shape the daily inspection should run.
	q2b := runMS(func() error {
		rows, err := db.sql.QueryContext(ctx, `
SELECT s.series_id FROM metric_series s
WHERE (
  (SELECT m1.value FROM metrics m1
    WHERE m1.series_id = s.series_id AND m1.ts >= ? AND m1.ts <= ?
    ORDER BY m1.ts ASC LIMIT 1)
  -
  (SELECT m2.value FROM metrics m2
    WHERE m2.series_id = s.series_id AND m2.ts >= ? AND m2.ts <= ?
    ORDER BY m2.ts DESC LIMIT 1)
) > 1.0`,
			"2026-01-01T00:00:00Z", windowEnd, "2026-01-01T00:00:00Z", windowEnd)
		if err != nil {
			return err
		}
		n := 0
		for rows.Next() {
			n++
		}
		rows.Close()
		if n != wantFlagged {
			return fmt.Errorf("Q2b flagged %d series, want the degrading tenth (%d)", n, wantFlagged)
		}
		return rows.Err()
	})
	// Q3: the latest value of one series — a single seek (the N-series
	// resource shape scales by N; noted in the analysis, not re-measured).
	q3 := runMS(func() error {
		_, err := db.Metrics.LatestPointOfSeries(ctx, probeSeries)
		return err
	})

	// ---- size + the retention delete (plan A's operator command).
	sizeOf := func() int64 {
		st, err := os.Stat(db.path)
		if err != nil {
			t.Fatalf("stat db: %v", err)
		}
		return st.Size()
	}
	dbBytes := sizeOf()

	t0 := time.Now()
	cutoff := start.AddDate(0, 0, days-30).Format("2006-01-02") + "T00:00:00Z"
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM metrics WHERE ts < ?`, cutoff); err != nil {
		t.Fatalf("retention delete: %v", err)
	}
	retentionMs := int(time.Since(t0).Milliseconds())
	dbBytesAfter := sizeOf()

	sort.Ints(perDayMs)
	out := map[string]any{
		"series": seriesCount, "days": days, "points": totalPoints,
		"ingestMsP50": perDayMs[len(perDayMs)/2],
		"ingestMsP95": perDayMs[(len(perDayMs)*95)/100],
		"ingestMsMax": perDayMs[len(perDayMs)-1],
		"q1Ms":        q1, "q2aFullScanMs": q2a, "q2bSeeksMs": q2b, "q3Ms": q3,
		"dbBytes": dbBytes, "bytesPerRow": dbBytes / int64(totalPoints),
		"retentionDeleteMs": retentionMs, "dbBytesAfterDelete": dbBytesAfter,
	}
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(out); err != nil {
		t.Fatalf("encode: %v", err)
	}
}
