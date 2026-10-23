package repo

// Timeseries metric storage of migration v14 (W20): the metric catalog
// (metric_keys), series metadata (metric_series) and the narrow measurement
// points (metrics). All SQL of the metric channel lives here; the service
// (internal/metrics) works over these typed methods.
//
// Upsert semantics (ADR-0014 decision 2 — value semantics):
//   - series rows are INSERT OR IGNORE keyed by the identity index
//     (resource_id, metric_key, COALESCE(scope,'')) — a re-import finds the
//     existing series instead of duplicating it;
//   - points are INSERT OR REPLACE keyed by (series_id, ts) — the latest
//     value of the same (series, ts) wins, and metrics.batch_id always
//     names the LAST writer (the lineage of the current value).
//
// extend via new methods; do not rewrite.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// MetricRepo persists and queries the timeseries metric tables.
type MetricRepo struct{ q DBTX }

// ListMetricKeys returns the catalog rows in REGISTRY order. The v14 seed
// inserts them in registry order and nothing else ever inserts (closed
// registry), so rowid order IS registry order; the sync test in
// internal/metrics pins the equality.
func (r *MetricRepo) ListMetricKeys(ctx context.Context) ([]domain.MetricKey, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT metric_key, label, unit, value_kind, min_value, max_value, direction, created_at
FROM metric_keys ORDER BY rowid`)
	if err != nil {
		return nil, fmt.Errorf("repo: list metric keys: %w", err)
	}
	defer rows.Close()
	keys := make([]domain.MetricKey, 0, 16)
	for rows.Next() {
		var (
			k                    domain.MetricKey
			valueKind, direction string
			minV, maxV           sql.NullFloat64
		)
		if err := rows.Scan(&k.Key, &k.Label, &k.Unit, &valueKind, &minV, &maxV, &direction, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("repo: scan metric key: %w", err)
		}
		k.ValueKind = domain.MetricValueKind(valueKind)
		k.Direction = domain.MetricDirection(direction)
		k.MinValue = floatPtrFromNull(minV)
		k.MaxValue = floatPtrFromNull(maxV)
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list metric keys: %w", err)
	}
	return keys, nil
}

// FindOrCreateSeries returns the series_id of (resourceID, metricKey,
// scope), creating the row when missing (INSERT OR IGNORE + SELECT — the
// identity unique index of migration v14 makes the ignore fire even for
// scope IS NULL). firstTS/lastTS seed the metadata of a NEW series; an
// existing series keeps its values (UpdateSeriesBounds maintains them).
// The returned created flag distinguishes the two paths for accounting.
func (r *MetricRepo) FindOrCreateSeries(ctx context.Context, resourceID, metricKey string, scope *string, firstTS, lastTS string) (seriesID string, created bool, err error) {
	// deterministic series id for the create path
	newID := domain.NewID("ser")
	if _, err := r.q.ExecContext(ctx,
		`INSERT OR IGNORE INTO metric_series (series_id, resource_id, metric_key, scope, first_seen_at, last_seen_at)
VALUES (?,?,?,?,?,?)`,
		newID, resourceID, metricKey, nullablePtr(scope), firstTS, lastTS); err != nil {
		return "", false, fmt.Errorf("repo: upsert metric series %s/%s: %w", resourceID, metricKey, err)
	}
	if err := r.q.QueryRowContext(ctx,
		`SELECT series_id FROM metric_series WHERE resource_id = ? AND metric_key = ? AND scope IS ?`,
		resourceID, metricKey, nullablePtr(scope)).Scan(&seriesID); err != nil {
		return "", false, fmt.Errorf("repo: load metric series %s/%s: %w", resourceID, metricKey, err)
	}
	return seriesID, seriesID == newID, nil
}

// UpdateSeriesBounds widens first_seen_at/last_seen_at to cover [minTS,
// maxTS] (MIN/MAX semantics — both are data-derived, never the clock).
// Re-importing older data moves first_seen_at back; nothing else changes.
func (r *MetricRepo) UpdateSeriesBounds(ctx context.Context, seriesID, minTS, maxTS string) error {
	res, err := r.q.ExecContext(ctx,
		`UPDATE metric_series
SET first_seen_at = MIN(first_seen_at, ?), last_seen_at = MAX(last_seen_at, ?)
WHERE series_id = ?`, minTS, maxTS, seriesID)
	if err != nil {
		return fmt.Errorf("repo: update series bounds %s: %w", seriesID, err)
	}
	return requireUpdated(res, "metric series", seriesID)
}

// insertChunk caps the multi-row INSERT OR REPLACE of InsertPoints: SQLite
// bound-parameter limits are generous but chunking keeps statement
// preparation bounded and gives the volume run predictable memory.
const insertChunk = 400

// InsertPoints upserts measurement points (INSERT OR REPLACE on
// (series_id, ts) — the ADR-0014 value semantics). Points are written in
// chunks of insertChunk rows per statement; the whole call joins the
// caller's transaction.
func (r *MetricRepo) InsertPoints(ctx context.Context, points []domain.MetricPoint) error {
	const prefix = `INSERT OR REPLACE INTO metrics (series_id, ts, value, quality, batch_id) VALUES `
	for start := 0; start < len(points); start += insertChunk {
		end := start + insertChunk
		if end > len(points) {
			end = len(points)
		}
		chunk := points[start:end]
		var sb strings.Builder
		sb.WriteString(prefix)
		args := make([]any, 0, len(chunk)*5)
		for i, p := range chunk {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,?,?,?)")
			args = append(args, p.SeriesID, p.TS, p.Value, p.Quality.String(), p.BatchID)
		}
		if _, err := r.q.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("repo: insert %d metric points (chunk at %d): %w", len(chunk), start, err)
		}
	}
	return nil
}

// SeriesWithUnit is one series of a resource plus the catalog unit (the
// join metric_keys.unit — the AI contract "每个指标带 unit", §7.5).
type SeriesWithUnit struct {
	Series domain.MetricSeries
	Unit   string
}

// SeriesOfResource lists the resource's series (optionally filtered to the
// given metric keys — nil/empty = all), ordered by (metric_key, series_id)
// for deterministic output. The unit comes from the catalog join.
func (r *MetricRepo) SeriesOfResource(ctx context.Context, resourceID string, keys []string) ([]SeriesWithUnit, error) {
	query := `SELECT s.series_id, s.resource_id, s.metric_key, s.scope, s.first_seen_at, s.last_seen_at, k.unit
FROM metric_series s JOIN metric_keys k ON k.metric_key = s.metric_key
WHERE s.resource_id = ?`
	args := []any{resourceID}
	if len(keys) > 0 {
		query += ` AND s.metric_key IN (` + placeholders(len(keys)) + `)`
		for _, k := range keys {
			args = append(args, k)
		}
	}
	query += ` ORDER BY s.metric_key, s.series_id`
	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: list series of resource %s: %w", resourceID, err)
	}
	defer rows.Close()
	out := make([]SeriesWithUnit, 0, 8)
	for rows.Next() {
		var (
			s     domain.MetricSeries
			scope sql.NullString
			unit  string
		)
		if err := rows.Scan(&s.SeriesID, &s.ResourceID, &s.MetricKey, &scope, &s.FirstSeenAt, &s.LastSeenAt, &unit); err != nil {
			return nil, fmt.Errorf("repo: scan series: %w", err)
		}
		s.Scope = stringPtrFromNull(scope)
		out = append(out, SeriesWithUnit{Series: s, Unit: unit})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list series of resource %s: %w", resourceID, err)
	}
	return out, nil
}

// SeriesOfKey lists EVERY series of one metric key (the detection engine's
// daily-inspection sweep input — all series of the rule's metric_key),
// ordered by series_id for deterministic findings order. Each row carries
// the resource the series hangs on.
func (r *MetricRepo) SeriesOfKey(ctx context.Context, metricKey string) ([]domain.MetricSeries, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT series_id, resource_id, metric_key, scope, first_seen_at, last_seen_at
FROM metric_series WHERE metric_key = ? ORDER BY series_id`, metricKey)
	if err != nil {
		return nil, fmt.Errorf("repo: list series of key %s: %w", metricKey, err)
	}
	defer rows.Close()
	out := make([]domain.MetricSeries, 0, 16)
	for rows.Next() {
		var (
			s     domain.MetricSeries
			scope sql.NullString
		)
		if err := rows.Scan(&s.SeriesID, &s.ResourceID, &s.MetricKey, &scope, &s.FirstSeenAt, &s.LastSeenAt); err != nil {
			return nil, fmt.Errorf("repo: scan series of key: %w", err)
		}
		s.Scope = stringPtrFromNull(scope)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list series of key %s: %w", metricKey, err)
	}
	return out, nil
}

// PointsOfSeries returns the raw points of one series in ts order
// (optional RFC3339 from/to bounds, inclusive — lexicographic order of the
// normalized UTC RFC3339 strings is chronological).
func (r *MetricRepo) PointsOfSeries(ctx context.Context, seriesID, from, to string) ([]domain.MetricPoint, error) {
	query := `SELECT series_id, ts, value, quality, batch_id FROM metrics WHERE series_id = ?`
	args := []any{seriesID}
	if from != "" {
		query += ` AND ts >= ?`
		args = append(args, from)
	}
	if to != "" {
		query += ` AND ts <= ?`
		args = append(args, to)
	}
	query += ` ORDER BY ts`
	return r.scanPoints(ctx, query, args...)
}

// CountPointsOfResource counts the metric points a resource carries
// within [from, to] — across ALL of its series (the idle-billing
// cross-layer composition, #114: the fee face says "money flowing" while
// the measurement face says "no records at all"; the zero count is the
// deterministic signal, no magic threshold). from/to are the same
// normalized-RFC3339 lexicographic bounds PointsOfSeries uses ("" =
// unbounded on that side); a resource with no series counts as zero
// points. One indexed (series_id → resource_id) join per call.
func (r *MetricRepo) CountPointsOfResource(ctx context.Context, resourceID, from, to string) (int, error) {
	query := `SELECT COUNT(*) FROM metrics m JOIN metric_series s ON s.series_id = m.series_id WHERE s.resource_id = ?`
	args := []any{resourceID}
	if from != "" {
		query += ` AND m.ts >= ?`
		args = append(args, from)
	}
	if to != "" {
		query += ` AND m.ts <= ?`
		args = append(args, to)
	}
	var n int
	if err := r.q.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count points of resource %s: %w", resourceID, err)
	}
	return n, nil
}

// LatestPointOfSeries returns the newest point of one series — a single
// seek on the (series_id, ts) WITHOUT ROWID key (#95: graph_context used
// to materialize EVERY raw point of EVERY series just to keep the last
// one; months of points per series per call). ErrNotFound when the
// series has no points at all (an initialized-but-empty series).
func (r *MetricRepo) LatestPointOfSeries(ctx context.Context, seriesID string) (domain.MetricPoint, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT series_id, ts, value, quality, batch_id FROM metrics WHERE series_id = ? ORDER BY ts DESC LIMIT 1`,
		seriesID)
	if err != nil {
		return domain.MetricPoint{}, fmt.Errorf("repo: latest point of series %s: %w", seriesID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return domain.MetricPoint{}, fmt.Errorf("%w: repo: series %s has no points", domain.ErrNotFound, seriesID)
	}
	var p domain.MetricPoint
	if err := rows.Scan(&p.SeriesID, &p.TS, &p.Value, &p.Quality, &p.BatchID); err != nil {
		return domain.MetricPoint{}, fmt.Errorf("repo: scan latest point of series %s: %w", seriesID, err)
	}
	return p, rows.Err()
}

// AggKind selects the daily aggregation of DailyPointsOfSeries.
type AggKind string

// The closed aggregation set of GET /api/resources/{id}/metrics (§7.3).
const (
	AggRaw      AggKind = "raw"
	AggDailyAvg AggKind = "daily_avg"
	AggDailyMin AggKind = "daily_min"
	AggDailyMax AggKind = "daily_max"
)

// AggPoint is one aggregated daily point: Day is the local date string
// (YYYY-MM-DD); Quality is 'suspect' when ANY contributing point was
// suspect (honest aggregation — a poisoned day does not hide behind clean
// averages), else 'ok'.
type AggPoint struct {
	Day     string
	Value   float64
	Quality domain.PointQuality
}

// DailyPointsOfSeries aggregates the series's points per calendar day
// (GROUP BY date(ts)). Only the three daily kinds are valid — raw is served
// by PointsOfSeries.
func (r *MetricRepo) DailyPointsOfSeries(ctx context.Context, seriesID, from, to string, agg AggKind) ([]AggPoint, error) {
	var aggFn string
	switch agg {
	case AggDailyAvg:
		aggFn = "AVG"
	case AggDailyMin:
		aggFn = "MIN"
	case AggDailyMax:
		aggFn = "MAX"
	default:
		return nil, fmt.Errorf("repo: daily aggregation %q is not one of daily_avg/daily_min/daily_max", string(agg))
	}
	query := fmt.Sprintf(`SELECT date(ts) AS day, %s(value) AS value,
CASE WHEN SUM(CASE WHEN quality = 'suspect' THEN 1 ELSE 0 END) > 0 THEN 'suspect' ELSE 'ok' END AS quality
FROM metrics WHERE series_id = ?`, aggFn)
	args := []any{seriesID}
	if from != "" {
		query += ` AND ts >= ?`
		args = append(args, from)
	}
	if to != "" {
		query += ` AND ts <= ?`
		args = append(args, to)
	}
	query += ` GROUP BY date(ts) ORDER BY day`
	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: daily aggregate of series %s: %w", seriesID, err)
	}
	defer rows.Close()
	out := make([]AggPoint, 0, 32)
	for rows.Next() {
		var (
			p       AggPoint
			quality string
		)
		if err := rows.Scan(&p.Day, &p.Value, &quality); err != nil {
			return nil, fmt.Errorf("repo: scan aggregate point: %w", err)
		}
		p.Quality = domain.PointQuality(quality)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: daily aggregate of series %s: %w", seriesID, err)
	}
	return out, nil
}

// AnomalyRow is one series-level trend observation of the §5.5 Q2 scan:
// the point count inside the window plus the FIRST and LAST points (ts and
// value) — delta = last - first is computed by the caller together with the
// degrading flag (the metric direction lives in the catalog registry, not
// in SQL).
type AnomalyRow struct {
	ResourceID   string
	ResourceName string
	ResourceType string
	MetricKey    string
	N            int
	FirstTS      string
	FirstValue   float64
	LastTS       string
	LastValue    float64
}

// DeltaTrend filters the anomaly scan by the sign of the value change:
// TrendAny keeps both, TrendDeclining keeps delta < 0, TrendRising keeps
// delta > 0.
type DeltaTrend int

// The closed trend filter set.
const (
	TrendAny DeltaTrend = iota
	TrendDeclining
	TrendRising
)

// Anomalies scans every series (optionally restricted to metricKey) over
// the window [from, to] (either bound empty = unbounded on that side) and
// returns the series whose |last - first| >= threshold, ordered by
// (resource_id, metric_key) — deterministic. First/last are the window's
// extremes (MIN(ts)/MAX(ts)); series with a single point have delta 0 and
// only match threshold <= 0. Resource name/type come from the join.
func (r *MetricRepo) Anomalies(ctx context.Context, resourceID, metricKey, from, to string, threshold float64, trend DeltaTrend) ([]AnomalyRow, error) {
	query := `SELECT s.resource_id, r.name, r.resource_type, s.metric_key, c.n, f.ts, f.value, l.ts, l.value
FROM metric_series s
JOIN (
  SELECT series_id, COUNT(*) AS n, MIN(ts) AS mn, MAX(ts) AS mx
  FROM metrics WHERE 1=1`
	args := make([]any, 0, 9)
	if from != "" {
		query += ` AND ts >= ?`
		args = append(args, from)
	}
	if to != "" {
		query += ` AND ts <= ?`
		args = append(args, to)
	}
	query += ` GROUP BY series_id
) c ON c.series_id = s.series_id
JOIN metrics f ON f.series_id = s.series_id AND f.ts = c.mn
JOIN metrics l ON l.series_id = s.series_id AND l.ts = c.mx
JOIN resources r ON r.id = s.resource_id
WHERE ABS(l.value - f.value) >= ?`
	args = append(args, threshold)
	switch trend {
	case TrendDeclining:
		query += ` AND l.value < f.value`
	case TrendRising:
		query += ` AND l.value > f.value`
	}
	if metricKey != "" {
		query += ` AND s.metric_key = ?`
		args = append(args, metricKey)
	}
	// #95: the single-resource pushdown — graph_context used to scan the
	// WHOLE anomaly universe (every series of every resource) and filter
	// to one resource in Go; the predicate now rides the same indexed
	// series_id/resource_id column the metric_series table carries.
	if resourceID != "" {
		query += ` AND s.resource_id = ?`
		args = append(args, resourceID)
	}
	query += ` ORDER BY s.resource_id, s.metric_key`

	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: anomaly scan: %w", err)
	}
	defer rows.Close()
	out := make([]AnomalyRow, 0, 16)
	for rows.Next() {
		var a AnomalyRow
		if err := rows.Scan(&a.ResourceID, &a.ResourceName, &a.ResourceType, &a.MetricKey,
			&a.N, &a.FirstTS, &a.FirstValue, &a.LastTS, &a.LastValue); err != nil {
			return nil, fmt.Errorf("repo: scan anomaly row: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: anomaly scan: %w", err)
	}
	return out, nil
}

// FindPortIDByNaturalKey resolves a port resource by its composite natural
// key "device_name|port_name" — the resolution path (a) of the ingest
// channel (exact value match on the key; never name-similarity inference).
func (r *MetricRepo) FindPortIDByNaturalKey(ctx context.Context, deviceName, portName string) (string, error) {
	key := deviceName + "|" + portName
	var id string
	err := r.q.QueryRowContext(ctx,
		`SELECT id FROM resources WHERE resource_type = 'port' AND natural_key = ?`, key).Scan(&id)
	if err != nil {
		return "", notFound("port", key, err)
	}
	return id, nil
}

// FindDeviceNameByMgmtIP resolves the device NAME of the device whose
// mgmt_ip attribute equals ip exactly (resolution path (b): 设备IP+端口名 →
// device by mgmt_ip → its name → the port natural key). Exact
// json_extract equality — no LIKE, no fuzzy match.
func (r *MetricRepo) FindDeviceNameByMgmtIP(ctx context.Context, ip string) (string, error) {
	var name string
	err := r.q.QueryRowContext(ctx,
		`SELECT name FROM resources
WHERE resource_type = 'device' AND json_extract(attributes, '$.mgmt_ip') = ?`, ip).Scan(&name)
	if err != nil {
		return "", notFound("device by mgmt_ip", ip, err)
	}
	return name, nil
}

// DeletePointsByBatch removes every point currently owned by the batch
// (metrics.batch_id = the LAST writer) and returns the deleted count — the
// core of the metric rollback. Points the batch once wrote but another
// batch later overwrote are NOT deleted (they belong to the later writer).
func (r *MetricRepo) DeletePointsByBatch(ctx context.Context, batchID string) (int64, error) {
	res, err := r.q.ExecContext(ctx, `DELETE FROM metrics WHERE batch_id = ?`, batchID)
	if err != nil {
		return 0, fmt.Errorf("repo: delete points of metric batch %s: %w", batchID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: delete points of metric batch %s: rows affected: %w", batchID, err)
	}
	return n, nil
}

// DeleteEmptySeries prunes series rows that hold zero points and returns
// the pruned count. Called after DeletePointsByBatch: a series whose every
// point vanished with its batch has no meaning left; a series still
// holding OTHER batches' points survives (the rollback asymmetry —
// completed decisions stay auditable, shared data stays).
func (r *MetricRepo) DeleteEmptySeries(ctx context.Context) (int64, error) {
	res, err := r.q.ExecContext(ctx,
		`DELETE FROM metric_series WHERE NOT EXISTS (SELECT 1 FROM metrics WHERE metrics.series_id = metric_series.series_id)`)
	if err != nil {
		return 0, fmt.Errorf("repo: prune empty metric series: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: prune empty metric series: rows affected: %w", err)
	}
	return n, nil
}

// CountPointsByBatch counts the points currently owned by a metric batch
// (the rollback response's deleted-points figure).
func (r *MetricRepo) CountPointsByBatch(ctx context.Context, batchID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM metrics WHERE batch_id = ?`, batchID).
		Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count points of metric batch %s: %w", batchID, err)
	}
	return n, nil
}

// CountMetricPoints counts every stored point (the volume measurement's
// ground truth).
func (r *MetricRepo) CountMetricPoints(ctx context.Context) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM metrics`).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count metric points: %w", err)
	}
	return n, nil
}

// CountMetricSeries counts every series row.
func (r *MetricRepo) CountMetricSeries(ctx context.Context) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_series`).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count metric series: %w", err)
	}
	return n, nil
}

// scanPoints runs query and scans the metric-point rows.
func (r *MetricRepo) scanPoints(ctx context.Context, query string, args ...any) ([]domain.MetricPoint, error) {
	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: query points: %w", err)
	}
	defer rows.Close()
	out := make([]domain.MetricPoint, 0, 64)
	for rows.Next() {
		var (
			p       domain.MetricPoint
			quality string
		)
		if err := rows.Scan(&p.SeriesID, &p.TS, &p.Value, &quality, &p.BatchID); err != nil {
			return nil, fmt.Errorf("repo: scan point: %w", err)
		}
		p.Quality = domain.PointQuality(quality)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: query points: %w", err)
	}
	return out, nil
}

// placeholders builds a "?,?,?" list of n bind markers.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
