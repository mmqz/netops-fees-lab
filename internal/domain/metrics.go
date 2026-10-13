package domain

import "time"

// MetricValueKind is the closed value-kind enum of the metric catalog
// (design data-model-v1.md §5.3 metric_keys.value_kind): gauge (instantaneous
// reading), counter (monotonic cumulative count) or ratio (share in 0..100%).
type MetricValueKind string

// The closed set of metric value kinds.
const (
	MetricValueKindGauge   MetricValueKind = "gauge"
	MetricValueKindCounter MetricValueKind = "counter"
	MetricValueKindRatio   MetricValueKind = "ratio"
)

// IsValid reports whether k is one of the closed value kinds.
func (k MetricValueKind) IsValid() bool {
	switch k {
	case MetricValueKindGauge, MetricValueKindCounter, MetricValueKindRatio:
		return true
	}
	return false
}

// String returns the string form of the value kind.
func (k MetricValueKind) String() string { return string(k) }

// MetricDirection is the closed direction enum of the metric catalog
// (design §5.3 metric_keys.direction): which way is "better" for this
// metric. The anomaly scan (§5.5 Q2) reads it to decide whether an
// observed delta is degrading — see internal/metrics for the sign logic.
type MetricDirection string

// The closed set of metric directions.
const (
	MetricDirectionHigherIsBetter MetricDirection = "higher_is_better"
	MetricDirectionLowerIsBetter  MetricDirection = "lower_is_better"
	MetricDirectionNeutral        MetricDirection = "neutral"
)

// IsValid reports whether d is one of the closed directions.
func (d MetricDirection) IsValid() bool {
	switch d {
	case MetricDirectionHigherIsBetter, MetricDirectionLowerIsBetter, MetricDirectionNeutral:
		return true
	}
	return false
}

// String returns the string form of the direction.
func (d MetricDirection) String() string { return string(d) }

// PointQuality is the closed quality enum of a stored measurement point
// (design §5.3 metrics.quality): ok (passed the plausibility bounds),
// suspect (value recorded but outside the metric's advisory min/max — the
// point is kept, never rejected, per the disposition that bounds are
// advisory) or missing (reserved marker of a known gap; the ingest channel
// does not write missing points — empty source cells simply produce no
// point — W23 detection may use the marker later).
type PointQuality string

// The closed set of point qualities.
const (
	PointQualityOk      PointQuality = "ok"
	PointQualityMissing PointQuality = "missing"
	PointQualitySuspect PointQuality = "suspect"
)

// IsValid reports whether q is one of the closed qualities.
func (q PointQuality) IsValid() bool {
	switch q {
	case PointQualityOk, PointQualityMissing, PointQualitySuspect:
		return true
	}
	return false
}

// String returns the string form of the point quality.
func (q PointQuality) String() string { return string(q) }

// MetricKey is one row of the metric_keys catalog table (design §5.3 ①).
// The rows are seeded by migration v14 from the closed registry
// (internal/metrics.MetricKeys — the Go fact source) and the sync is pinned
// by a consistency test; MinValue/MaxValue are the advisory plausibility
// bounds (NULL = unbounded on that side).
type MetricKey struct {
	Key       string
	Label     string
	Unit      string
	ValueKind MetricValueKind
	MinValue  *float64
	MaxValue  *float64
	Direction MetricDirection
	CreatedAt string // RFC3339, fixed seed timestamp (catalog rows are static)
}

// MetricSeries is one row of the metric_series table (design §5.3 ②): one
// resource × one metric (scope NULL = resource-level; port performance
// series hang on the port resource itself per the §12-A3 disposition, so
// scope stays NULL and is reserved for finer dimensions that cannot become
// graph nodes). First/LastSeenAt are data-derived (min/max ts of the
// series's points), never server clock.
type MetricSeries struct {
	SeriesID    string
	ResourceID  string
	MetricKey   string
	Scope       *string
	FirstSeenAt string // RFC3339 UTC, = MIN(ts) of the series's points
	LastSeenAt  string // RFC3339 UTC, = MAX(ts) of the series's points
}

// MetricPoint is one row of the metrics table (design §5.3 ③): the narrow
// measurement point. TS is RFC3339 UTC normalized from the source data (no
// server clock in the data path). BatchID is the metric batch that last
// wrote this (series, ts) — INSERT OR REPLACE value semantics (ADR-0014
// decision 2).
type MetricPoint struct {
	SeriesID string
	TS       string // RFC3339 UTC, from the source data
	Value    float64
	Quality  PointQuality
	BatchID  string
}

// MetricBatch is one ingest run of a performance workbook through the
// metric channel (ADR-0011 D1: separate table family — deliberately NOT
// import_batches, "不与资源批次混表"). It reuses the ImportBatch status
// state machine (analyzing → awaiting_mapping → importing → completed |
// failed, rolled_back post-terminal) but carries no rule_version (the
// E/W/I catalog does not cross domains) and no per-row results table (a
// metric row is value-semantic: the row accounting IS the counters).
type MetricBatch struct {
	ID             string
	WorkbookName   string
	WorkbookSHA256 string
	Status         BatchStatus
	Mapping        string // confirmed ingest mapping JSON (internal/metrics)
	RowsTotal      int
	RowsImported   int
	RowsFailed     int
	Error          string
	StartedAt      time.Time // bookkeeping timestamp (clock hook), not data
	FinishedAt     *time.Time
}
