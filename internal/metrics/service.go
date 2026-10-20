package metrics

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/workbook"
)

// DefaultAnomalyThreshold is the |delta| threshold of the anomaly scan when
// the caller passes none (§5.5 Q2 uses -1.0 on optical power; the generic
// default per the task spec is 1.0).
const DefaultAnomalyThreshold = 1.0

// The aggregation kinds of GET /api/resources/{id}/metrics (§7.3), aliased
// from the repo layer so the service surface stays self-describing.
type AggKind = repo.AggKind

const (
	// AggRaw serves the raw points (default).
	AggRaw = repo.AggRaw
	// AggDailyAvg / AggDailyMin / AggDailyMax aggregate per calendar day.
	AggDailyAvg = repo.AggDailyAvg
	AggDailyMin = repo.AggDailyMin
	AggDailyMax = repo.AggDailyMax
)

// Service is the metric ingest/query service (W20). All persistence goes
// through the repo layer; multi-statement atomicity uses DB.Do. The Now
// hook exists for deterministic batch bookkeeping timestamps — it never
// feeds a data value (ts comes from the source workbook, design D2).
type Service struct {
	db  *repo.DB
	Now func() time.Time
}

// New returns a Service bound to db.
func New(db *repo.DB) *Service {
	return &Service{db: db, Now: time.Now}
}

// clock returns the current UTC time, honoring the Now hook.
func (s *Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// ColumnStat describes one column of the analyzed performance sheet.
type ColumnStat struct {
	// Header is the trimmed header cell text.
	Header string `json:"header"`
	// Index is the 1-based Excel column position (A = 1).
	Index int `json:"index"`
	// Samples holds up to 3 non-empty values of the column, in row order.
	Samples []string `json:"samples"`
	// MetricKey is the matched catalog key; "" when the column matched no
	// metric synonym.
	MetricKey string `json:"metricKey,omitempty"`
}

// IngestAnalysis is the result of the offline analyze of a performance
// workbook: the column stats, the suggested mapping and the warnings. No
// database is touched, no batch is created.
type IngestAnalysis struct {
	WorkbookName     string        `json:"workbookName"`
	SHA256           string        `json:"sha256"`
	Sheet            string        `json:"sheet"`
	RowCount         int           `json:"rowCount"`
	Columns          []ColumnStat  `json:"columns"`
	SuggestedMapping IngestMapping `json:"suggestedMapping"`
	Warnings         []string      `json:"warnings"`
}

// maxSamples caps the per-column sample values of the analysis.
const maxSamples = 3

// AnalyzeOffline parses the performance workbook at path and builds the
// IngestAnalysis — suggested mapping via the catalog synonym engine plus
// per-column stats — WITHOUT touching the database. The CLI's
// `netopslab ingest --suggest` and the HTTP POST /api/metrics/analyze use
// it (the metric channel's analyze is stateless: the confirmed mapping and
// the file travel together in the one-shot import call, so no
// awaiting_mapping batch is parked in the database).
//
// The sheet of interest is picked deterministically: the first sheet whose
// headers match at least one catalog synonym; when nothing matches, the
// first sheet with data rows (the suggestion then degenerates to warnings,
// which is the honest answer — the mapping must be written by hand).
func AnalyzeOffline(path, workbookName string) (IngestAnalysis, error) {
	wb, err := workbook.Read(path)
	if err != nil {
		return IngestAnalysis{}, fmt.Errorf("metrics: analyze %s: %w", workbookName, err)
	}
	sheet := pickSheet(wb)
	if sheet == nil {
		return IngestAnalysis{}, fmt.Errorf("metrics: analyze %s: %w", workbookName, workbook.ErrEmptyWorkbook)
	}
	return analysisOf(sheet, wb.SHA256, workbookName), nil
}

// pickSheet returns the performance sheet of wb (or nil when the workbook
// has no data rows at all): the first sheet with a catalog synonym match,
// else the first sheet with data rows.
func pickSheet(wb *workbook.Workbook) *workbook.Sheet {
	var firstWithRows *workbook.Sheet
	for i := range wb.Sheets {
		sh := &wb.Sheets[i]
		if sh.DataRows == 0 {
			continue
		}
		if firstWithRows == nil {
			firstWithRows = sh
		}
		for _, h := range sh.Headers {
			if _, ok := SpecForHeader(h); ok {
				return sh
			}
		}
	}
	return firstWithRows
}

// analysisOf builds the IngestAnalysis view of one sheet.
func analysisOf(sheet *workbook.Sheet, sha256, workbookName string) IngestAnalysis {
	m, warnings := Suggest(sheet.Name, sheet.Headers)
	a := IngestAnalysis{
		WorkbookName:     workbookName,
		SHA256:           sha256,
		Sheet:            sheet.Name,
		RowCount:         sheet.DataRows,
		SuggestedMapping: m,
		Warnings:         warnings,
	}
	for ci, h := range sheet.Headers {
		key := ""
		if spec, ok := SpecForHeader(h); ok {
			key = spec.Key
		}
		a.Columns = append(a.Columns, ColumnStat{
			Header:    h,
			Index:     ci + 1,
			Samples:   columnSamples(sheet, ci, maxSamples),
			MetricKey: key,
		})
	}
	return a
}

// columnSamples collects up to max non-empty values of the headerIdx-th
// column, in row order (the importer's analyze helper, in miniature).
func columnSamples(sheet *workbook.Sheet, headerIdx, max int) []string {
	samples := make([]string, 0, max)
	if headerIdx >= len(sheet.Headers) {
		return samples
	}
	header := sheet.Headers[headerIdx]
	for _, row := range sheet.Rows {
		if v := row.Values[header]; v != "" {
			samples = append(samples, v)
			if len(samples) == max {
				break
			}
		}
	}
	return samples
}

// IngestSummary is the outcome of an executed metric ingest.
type IngestSummary struct {
	// BatchID is the executed metric batch.
	BatchID string `json:"batchId"`
	// Status is the batch status after execution (completed).
	Status domain.BatchStatus `json:"status"`
	// RowsTotal counts the data rows of the performance sheet.
	RowsTotal int `json:"rowsTotal"`
	// RowsImported counts rows whose points landed.
	RowsImported int `json:"rowsImported"`
	// RowsFailed counts rows rejected by row-phase checks (unresolvable
	// port, unparseable date, non-numeric metric value) — counted, never
	// fatal (the resource importer's row-failure philosophy).
	RowsFailed int `json:"rowsFailed"`
	// PointsWritten counts the upserted measurement points (INSERT OR
	// REPLACE — a re-import of the same data rewrites the same points).
	PointsWritten int `json:"pointsWritten"`
	// SeriesTouched counts the distinct series written.
	SeriesTouched int `json:"seriesTouched"`
}

// Ingest runs the confirmed mapping m against the performance workbook at
// path (one-shot: analyze + execute, the metric channel's batch runs in
// the same call). The pipeline:
//
//   - the workbook is read and the mapping validated against the actual
//     headers — a validation failure returns a typed *FieldError (HTTP
//     400) WITHOUT creating a batch (nothing was imported, there is
//     nothing to account);
//   - a metric batch is created (status importing, the mapping JSON, the
//     workbook hash; started_at is bookkeeping — the ONLY clock read in
//     the channel, ts values themselves come from the data, design D2);
//   - ALL writes (points, series, counts, terminal status) run inside ONE
//     transaction; an internal error rolls everything back and the batch
//     is marked failed with zero partial writes;
//   - row-level failures (unresolvable port / bad date / non-numeric
//     value) are counted per row and never abort the ingest.
//
// Upsert semantics (ADR-0014 decision 2, value semantics): series rows are
// INSERT OR IGNORE by (resource_id, metric_key, scope NULL); points are
// INSERT OR REPLACE by (series_id, ts) — re-importing the same workbook
// overwrites the same points (idempotent point count, last value wins).
// Values outside the catalog's advisory bounds are recorded with
// quality='suspect' (never rejected). workbookName is the display name of
// the batch (the HTTP upload path passes the user's original file name).
// interruptedMetricReason is the metric channel's twin of the importer's
// contractual heal marker (recover.go): the leading "interrupted" token
// makes a healed batch recognizable as "the pipeline never reached a
// terminal state", not as an ordinary ingest failure.
const interruptedMetricReason = "interrupted: metric ingest stopped before reaching a terminal state (process crash or restart)"

// RecoverInterrupted heals the metric channel's crash-window batches (#87):
// Ingest writes its terminal status inside the ingest transaction, so a
// process death mid-ingest leaves the batch row stuck at importing forever
// — the metric channel was the only one of the three import channels
// without a sweeper (the resource channel heals via
// importer.RecoverInterrupted, the fee channel's batches are created in
// the retry-open style with a failed row as the fallback). This resets
// every transient metric batch to failed(interrupted) and returns the
// number healed. Same placement discipline as the importer twin: only at
// serve startup / CLI entry, never in repo.Open or the MCP path.
func (s *Service) RecoverInterrupted(ctx context.Context) (int64, error) {
	n, err := s.db.MetricBatches.MarkInterruptedImportsFailed(ctx, interruptedMetricReason)
	if err != nil {
		return 0, fmt.Errorf("metrics: recover interrupted batches: %w", err)
	}
	return n, nil
}

func (s *Service) Ingest(ctx context.Context, path string, m IngestMapping, workbookName string) (IngestSummary, error) {
	wb, err := workbook.Read(path)
	if err != nil {
		return IngestSummary{}, fmt.Errorf("metrics: ingest %s: %w", workbookName, err)
	}
	sheet := pickSheet(wb)
	if sheet == nil {
		return IngestSummary{}, fmt.Errorf("metrics: ingest %s: %w", workbookName, workbook.ErrEmptyWorkbook)
	}
	if err := m.Validate(sheet.Headers); err != nil {
		return IngestSummary{}, err
	}

	// The catalog from the TABLE (the serving source of truth; the registry
	// sync is pinned by TestCatalogSyncs). Also validates the metric keys
	// against the database, not just the Go registry.
	catalogRows, err := s.db.Metrics.ListMetricKeys(ctx)
	if err != nil {
		return IngestSummary{}, fmt.Errorf("metrics: load catalog: %w", err)
	}
	catalog := make(map[string]domain.MetricKey, len(catalogRows))
	for _, k := range catalogRows {
		catalog[k.Key] = k
	}
	metricKeys := make([]string, 0, len(m.Metrics))
	for k := range m.Metrics {
		metricKeys = append(metricKeys, k)
	}
	sort.Strings(metricKeys) // deterministic series/point write order
	for _, k := range metricKeys {
		if _, known := catalog[k]; !known {
			return IngestSummary{}, &FieldError{Field: "metrics",
				Msg: fmt.Sprintf("指标键 %q 不在数据库目录中（封闭目录：%s）", k, strings.Join(KeySet(), ", "))}
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return IngestSummary{}, fmt.Errorf("metrics: ingest: read %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	mappingJSON, err := MarshalMapping(m)
	if err != nil {
		return IngestSummary{}, fmt.Errorf("metrics: encode mapping: %w", err)
	}
	batch := domain.MetricBatch{
		ID:             domain.NewID("mbat"),
		WorkbookName:   workbookName,
		WorkbookSHA256: fmt.Sprintf("%x", sum[:]),
		Status:         domain.BatchStatusImporting,
		Mapping:        string(mappingJSON),
		StartedAt:      s.clock(),
	}
	if err := s.db.MetricBatches.Create(ctx, batch); err != nil {
		return IngestSummary{}, fmt.Errorf("metrics: create batch %s: %w", batch.ID, err)
	}

	summary := IngestSummary{BatchID: batch.ID}
	err = s.db.Do(ctx, func(tx *repo.Tx) error {
		sum, err := s.executeSheet(ctx, tx, batch, sheet, m, metricKeys, catalog)
		if err != nil {
			return err
		}
		summary = sum
		return nil
	})
	if err != nil {
		if mErr := s.db.MetricBatches.MarkFailed(ctx, batch.ID, err.Error()); mErr != nil {
			return IngestSummary{}, fmt.Errorf("metrics: mark batch %s failed: %w (import error: %v)", batch.ID, mErr, err)
		}
		return IngestSummary{}, fmt.Errorf("metrics: ingest batch %s: %w", batch.ID, err)
	}
	return summary, nil
}

// executeSheet processes every row of the performance sheet inside the
// surrounding transaction and finalizes the batch. Rows are parsed fully
// BEFORE any write of that row (a row with one non-numeric metric value
// fails as a unit — no partial rows), then its points are upserted.
func (s *Service) executeSheet(ctx context.Context, tx *repo.Tx, batch domain.MetricBatch,
	sheet *workbook.Sheet, m IngestMapping, metricKeys []string,
	catalog map[string]domain.MetricKey) (IngestSummary, error) {

	cols := ResolveColumns(sheet.Headers)
	// The concrete resolver columns (Validate guaranteed they exist).
	var deviceCol, portCol string
	if m.ResolveBy[0] == "device_ip" {
		if c, ok := cols["device_ip"]; ok {
			deviceCol = c
		}
	} else if c, ok := cols["device_name"]; ok {
		deviceCol = c
	}
	if c, ok := cols["port_name"]; ok {
		portCol = c
	}
	resolveByIP := m.ResolveBy[0] == "device_ip"

	summary := IngestSummary{BatchID: batch.ID, RowsTotal: sheet.DataRows}
	// resolution caches (per-batch, in-transaction): exact-value lookups
	portIDs := map[string]string{}         // "device|port" → resource id
	deviceNames := map[string]string{}     // mgmt_ip → device name (path b)
	seriesIDs := map[string]string{}       // "resource\x00metric" → series id
	seriesBounds := map[string][2]string{} // series id → {minTS, maxTS}

	for _, row := range sheet.Rows {
		// Data time (D2): from the sheet, never the clock.
		tsRaw := row.Values[m.TSField]
		ts, tsErr := ParseTS(tsRaw)
		if tsErr != nil {
			summary.RowsFailed++
			continue
		}
		// Port resolution: exact value matches only.
		deviceName := row.Values[deviceCol]
		if resolveByIP {
			name, ok := deviceNames[deviceName]
			if !ok {
				name, err := tx.Metrics.FindDeviceNameByMgmtIP(ctx, deviceName)
				if err != nil {
					summary.RowsFailed++
					continue
				}
				deviceNames[row.Values[deviceCol]] = name
				deviceName = name
			} else {
				deviceName = name
			}
		}
		portName := row.Values[portCol]
		lookup := deviceName + "|" + portName
		resourceID, ok := portIDs[lookup]
		if !ok {
			id, err := tx.Metrics.FindPortIDByNaturalKey(ctx, deviceName, portName)
			if err != nil {
				summary.RowsFailed++
				continue
			}
			portIDs[lookup] = id
			resourceID = id
		}

		// Parse every mapped metric value BEFORE writing anything of the
		// row: one non-numeric value fails the row as a unit.
		type pending struct {
			key    string
			value  float64
			bounds domain.MetricKey
		}
		rowMetrics := make([]pending, 0, len(metricKeys))
		rowFailed := false
		for _, key := range metricKeys {
			raw := strings.TrimSpace(row.Values[m.Metrics[key]])
			if raw == "" {
				continue // empty cell → no point (real exports carry blanks)
			}
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				rowFailed = true
				break
			}
			rowMetrics = append(rowMetrics, pending{key: key, value: v, bounds: catalog[key]})
		}
		if rowFailed {
			summary.RowsFailed++
			continue
		}

		// Upsert the series rows and collect the points.
		points := make([]domain.MetricPoint, 0, len(rowMetrics))
		for _, pm := range rowMetrics {
			cacheKey := resourceID + "\x00" + pm.key
			seriesID, ok := seriesIDs[cacheKey]
			if !ok {
				id, _, err := tx.Metrics.FindOrCreateSeries(ctx, resourceID, pm.key, nil, ts, ts)
				if err != nil {
					return summary, fmt.Errorf("metrics: batch %s: upsert series %s/%s: %w", batch.ID, resourceID, pm.key, err)
				}
				seriesIDs[cacheKey] = id
				seriesID = id
			}
			quality := domain.PointQualityOk
			if !inBounds(pm.bounds, pm.value) {
				quality = domain.PointQualitySuspect // bounds are advisory — record, never reject
			}
			points = append(points, domain.MetricPoint{
				SeriesID: seriesID, TS: ts, Value: pm.value, Quality: quality, BatchID: batch.ID,
			})
			// widen the series bounds (first_seen_at/last_seen_at = MIN/MAX ts)
			bounds, ok := seriesBounds[seriesID]
			if !ok || ts < bounds[0] {
				bounds[0] = ts
			}
			if !ok || ts > bounds[1] {
				bounds[1] = ts
			}
			seriesBounds[seriesID] = bounds
		}
		if len(points) == 0 {
			// The row resolved and parsed but carries no metric values —
			// nothing to store; count it imported (the row was processed,
			// its port and date were valid).
			summary.RowsImported++
			continue
		}
		if err := tx.Metrics.InsertPoints(ctx, points); err != nil {
			return summary, fmt.Errorf("metrics: batch %s: %w", batch.ID, err)
		}
		summary.RowsImported++
		summary.PointsWritten += len(points)
	}

	// Series metadata bounds: one UPDATE per touched series.
	for seriesID, bounds := range seriesBounds {
		if err := tx.Metrics.UpdateSeriesBounds(ctx, seriesID, bounds[0], bounds[1]); err != nil {
			return summary, fmt.Errorf("metrics: batch %s: %w", batch.ID, err)
		}
	}
	summary.SeriesTouched = len(seriesBounds)

	if err := tx.MetricBatches.UpdateCounts(ctx, batch.ID, repo.MetricBatchCounts{
		Total:    summary.RowsTotal,
		Imported: summary.RowsImported,
		Failed:   summary.RowsFailed,
	}); err != nil {
		return summary, fmt.Errorf("metrics: batch %s: %w", batch.ID, err)
	}
	if err := tx.MetricBatches.UpdateStatus(ctx, batch.ID, domain.BatchStatusCompleted); err != nil {
		return summary, fmt.Errorf("metrics: batch %s: %w", batch.ID, err)
	}
	summary.Status = domain.BatchStatusCompleted
	return summary, nil
}

// inBounds checks the advisory bounds of a catalog row (nil side = open).
func inBounds(k domain.MetricKey, v float64) bool {
	if k.MinValue != nil && v < *k.MinValue {
		return false
	}
	if k.MaxValue != nil && v > *k.MaxValue {
		return false
	}
	return true
}

// ParseTS normalizes a source date cell to RFC3339 UTC midnight
// (daily granularity, design D2: ts from data). Accepted forms:
//
//   - Excel serial date (integer days; the 1900 leap-bug convention —
//     serial 61 = 1900-03-01, serials 1..60 map linearly onto
//     1900-01-01..1900-02-28, serial 60 is the fake 1900-02-29 and maps to
//     1900-02-28);
//   - "YYYY-MM-DD", "YYYY-MM-DD HH:MM[:SS]", "YYYY-MM-DDTHH:MM:SS[Z/+off]"
//     (the time part is dropped — a daily point is pinned to midnight);
//   - "YYYY/M/D" (single or double digits), optionally followed by a time
//     part (also dropped).
//
// Anything else (including the empty cell) is an error; the row fails.
func ParseTS(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("metrics: empty date cell")
	}
	// Excel serial: pure digits (a bare integer year like "2026" would be
	// serial 2026 = 1905-07-18 — real sheets never carry bare years; the
	// forms below catch real dates first, digits-only is the serial case).
	if isDigits(s) {
		serial, err := strconv.Atoi(s)
		if err != nil || serial <= 0 {
			return "", fmt.Errorf("metrics: date %q is not a positive excel serial", raw)
		}
		var d time.Time
		switch {
		case serial >= 61:
			d = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC).AddDate(0, 0, serial)
		case serial == 60:
			d = time.Date(1900, 2, 28, 0, 0, 0, 0, time.UTC) // the fake 1900-02-29 has no real date
		default:
			d = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC).AddDate(0, 0, serial)
		}
		return d.Format(time.RFC3339), nil
	}
	// Date part = everything before the first space or 'T'.
	datePart := s
	if idx := strings.IndexAny(s, " T"); idx > 0 {
		datePart = s[:idx]
	}
	if strings.Contains(datePart, "-") {
		d, err := time.Parse("2006-01-02", datePart)
		if err != nil {
			return "", fmt.Errorf("metrics: date %q is not YYYY-MM-DD", raw)
		}
		return d.Format(time.RFC3339), nil
	}
	if strings.Contains(datePart, "/") {
		d, err := time.Parse("2006/1/2", datePart)
		if err != nil {
			return "", fmt.Errorf("metrics: date %q is not YYYY/M/D", raw)
		}
		return d.Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("metrics: date %q matches no accepted form (excel serial / YYYY-MM-DD / YYYY/M/D)", raw)
}

// isDigits reports whether s consists only of ASCII digits.
func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// ListBatches returns metric batches newest-first with the total count
// (page/pageSize of the API contract).
func (s *Service) ListBatches(ctx context.Context, limit, offset int) ([]domain.MetricBatch, int, error) {
	return s.db.MetricBatches.List(ctx, limit, offset)
}

// RollbackResult reports what a metric batch rollback deleted.
type RollbackResult struct {
	BatchID       string `json:"batchId"`
	Status        string `json:"status"`
	DeletedPoints int    `json:"deletedPoints"`
	PrunedSeries  int    `json:"prunedSeries"`
}

// Rollback reverses a completed metric batch: every point the batch
// currently owns (metrics.batch_id = it — the LAST writer of that
// (series, ts)) is deleted, series rows that become empty are pruned, and
// the batch becomes rolled_back.
//
// The asymmetry against the resource rollback is deliberate and honest:
// a point this batch once wrote but a LATER batch overwrote (INSERT OR
// REPLACE moved batch_id to the later writer) is NOT deleted — the
// current value belongs to the later import, and undoing the earlier
// batch must not silently revert data a later run superseded. The audit
// trail stays complete: the rolled-back batch row keeps its counters and
// mapping, and surviving points name their actual writer. Series rows
// still holding OTHER batches' points survive; only genuinely empty
// series are pruned (a series row without points has no meaning).
//
// Only completed batches roll back (any other status → domain.ErrConflict,
// the 409 of the HTTP contract). The status check, deletions and the
// status flip run inside ONE transaction (BEGIN IMMEDIATE — serialized
// with every other write, the importer.Rollback discipline).
func (s *Service) Rollback(ctx context.Context, batchID string) (RollbackResult, error) {
	result := RollbackResult{BatchID: batchID}
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		batch, err := tx.MetricBatches.Get(ctx, batchID)
		if err != nil {
			return fmt.Errorf("metrics: load batch %s: %w", batchID, err)
		}
		if batch.Status != domain.BatchStatusCompleted {
			return fmt.Errorf("metrics: rollback batch %s: status %s, want %s: %w",
				batchID, batch.Status, domain.BatchStatusCompleted, domain.ErrConflict)
		}
		deleted, err := tx.Metrics.DeletePointsByBatch(ctx, batchID)
		if err != nil {
			return fmt.Errorf("metrics: rollback batch %s: %w", batchID, err)
		}
		result.DeletedPoints = int(deleted)
		pruned, err := tx.Metrics.DeleteEmptySeries(ctx)
		if err != nil {
			return fmt.Errorf("metrics: rollback batch %s: %w", batchID, err)
		}
		result.PrunedSeries = int(pruned)
		if err := tx.MetricBatches.UpdateStatus(ctx, batchID, domain.BatchStatusRolledBack); err != nil {
			return fmt.Errorf("metrics: rollback batch %s: %w", batchID, err)
		}
		result.Status = domain.BatchStatusRolledBack.String()
		return nil
	})
	if err != nil {
		return RollbackResult{}, err
	}
	return result, nil
}

// SeriesPoint is one point of a series response: {ts, value, quality}.
type SeriesPoint struct {
	TS      string              `json:"ts"`
	Value   float64             `json:"value"`
	Quality domain.PointQuality `json:"quality"`
}

// SeriesResult is one series of GET /api/resources/{id}/metrics.
type SeriesResult struct {
	SeriesID  string        `json:"seriesId"`
	MetricKey string        `json:"metricKey"`
	Unit      string        `json:"unit"`
	Points    []SeriesPoint `json:"points"`
}

// ResourceSeries returns the resource's series (optional metric-key
// filter; optional RFC3339 from/to bounds; agg = raw | daily_avg |
// daily_min | daily_max), ordered by (metric_key, ts) — deterministic.
// from/to are already-normalized RFC3339 strings ("" = unbounded).
func (s *Service) ResourceSeries(ctx context.Context, resourceID string, keys []string,
	from, to string, agg AggKind) ([]SeriesResult, error) {
	series, err := s.db.Metrics.SeriesOfResource(ctx, resourceID, keys)
	if err != nil {
		return nil, err
	}
	out := make([]SeriesResult, 0, len(series))
	for _, se := range series {
		sr := SeriesResult{SeriesID: se.Series.SeriesID, MetricKey: se.Series.MetricKey, Unit: se.Unit}
		switch agg {
		case AggRaw, "":
			points, err := s.db.Metrics.PointsOfSeries(ctx, se.Series.SeriesID, from, to)
			if err != nil {
				return nil, err
			}
			for _, p := range points {
				sr.Points = append(sr.Points, SeriesPoint{TS: p.TS, Value: p.Value, Quality: p.Quality})
			}
		case AggDailyAvg, AggDailyMin, AggDailyMax:
			daily, err := s.db.Metrics.DailyPointsOfSeries(ctx, se.Series.SeriesID, from, to, agg)
			if err != nil {
				return nil, err
			}
			for _, p := range daily {
				sr.Points = append(sr.Points, SeriesPoint{
					TS:      p.Day + "T00:00:00Z", // normalized RFC3339 of the day
					Value:   p.Value,
					Quality: p.Quality,
				})
			}
		default:
			return nil, fmt.Errorf("metrics: agg %q 不是 raw/daily_avg/daily_min/daily_max", string(agg))
		}
		out = append(out, sr)
	}
	return out, nil
}

// AnomalyItem is one grouped anomaly finding of the §5.5 Q2 scan — the
// "每日巡检" core call.
type AnomalyItem struct {
	ResourceID   string  `json:"resourceId"`
	ResourceName string  `json:"resourceName"`
	ResourceType string  `json:"resourceType"`
	MetricKey    string  `json:"metricKey"`
	Unit         string  `json:"unit"`
	Direction    string  `json:"direction"`
	N            int     `json:"n"`
	FirstTS      string  `json:"firstTs"`
	FirstValue   float64 `json:"firstValue"`
	LastTS       string  `json:"lastTs"`
	LastValue    float64 `json:"lastValue"`
	// Delta = last - first (the first/last difference the design §5.5 Q2
	// sanctions as the slope approximation).
	Delta float64 `json:"delta"`
	// Degrading is the direction-aware verdict: true when the delta moves
	// the metric the WRONG way (higher_is_better & delta < 0, or
	// lower_is_better & delta > 0). neutral metrics never degrade by
	// trend alone.
	Degrading bool `json:"degrading"`
}

// Anomalies scans every series (optionally restricted to metricKey) over
// the window [from, to] (either "" = unbounded; normalized RFC3339) and
// returns the series whose |last - first| >= threshold (default
// DefaultAnomalyThreshold when 0), optionally filtered by trend direction
// ("declining" = delta < 0, "rising" = delta > 0, "" = both). Ordered by
// (resource, metricKey) — deterministic.
func (s *Service) Anomalies(ctx context.Context, metricKey, from, to string,
	threshold float64, direction string) ([]AnomalyItem, error) {
	if threshold <= 0 {
		threshold = DefaultAnomalyThreshold
	}
	trend := repo.TrendAny
	switch direction {
	case "", "any":
		trend = repo.TrendAny
	case "declining", "down":
		trend = repo.TrendDeclining
	case "rising", "up":
		trend = repo.TrendRising
	default:
		return nil, fmt.Errorf("metrics: direction %q 不是 declining/rising/any", direction)
	}
	// The catalog supplies unit + direction per metric key (from the
	// table — the same serving source as /api/metrics/keys).
	catalogRows, err := s.db.Metrics.ListMetricKeys(ctx)
	if err != nil {
		return nil, err
	}
	catalog := make(map[string]domain.MetricKey, len(catalogRows))
	for _, k := range catalogRows {
		catalog[k.Key] = k
	}
	rows, err := s.db.Metrics.Anomalies(ctx, metricKey, from, to, threshold, trend)
	if err != nil {
		return nil, err
	}
	items := make([]AnomalyItem, 0, len(rows))
	for _, row := range rows {
		spec := catalog[row.MetricKey]
		delta := row.LastValue - row.FirstValue
		degrading := false
		switch spec.Direction {
		case domain.MetricDirectionHigherIsBetter:
			degrading = delta < 0
		case domain.MetricDirectionLowerIsBetter:
			degrading = delta > 0
		}
		items = append(items, AnomalyItem{
			ResourceID:   row.ResourceID,
			ResourceName: row.ResourceName,
			ResourceType: row.ResourceType,
			MetricKey:    row.MetricKey,
			Unit:         spec.Unit,
			Direction:    spec.Direction.String(),
			N:            row.N,
			FirstTS:      row.FirstTS,
			FirstValue:   row.FirstValue,
			LastTS:       row.LastTS,
			LastValue:    row.LastValue,
			Delta:        delta,
			Degrading:    degrading,
		})
	}
	return items, nil
}

// Basename is filepath.Base exposed for the CLI's display-name handling.
func Basename(path string) string { return filepath.Base(path) }
