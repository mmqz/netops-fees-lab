package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/repo"
)

// The timeseries metric channel endpoints (W20, design §7.3 first three +
// the ADR-0011 D1 ingest channel). HTTP + CLI only — the MCP tool surface
// stays frozen at the five read-only tools (ADR-0011 D5: analysis data
// flows through the open HTTP face, never through the AI control channel).

// metricKeyItem is one catalog entry of GET /api/metrics/keys.
type metricKeyItem struct {
	MetricKey string   `json:"metricKey"`
	Label     string   `json:"label"`
	Unit      string   `json:"unit"`
	ValueKind string   `json:"valueKind"`
	MinValue  *float64 `json:"minValue"`
	MaxValue  *float64 `json:"maxValue"`
	Direction string   `json:"direction"`
	CreatedAt string   `json:"createdAt"`
}

// handleMetricKeys implements GET /api/metrics/keys: the closed metric
// catalog FROM THE TABLE (the serving source of truth), in registry order
// (the v14 seed order — pinned by internal/metrics.TestCatalogSyncs).
func (s *Server) handleMetricKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.db.Metrics.ListMetricKeys(r.Context())
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	items := make([]metricKeyItem, 0, len(keys))
	for _, k := range keys {
		items = append(items, metricKeyItem{
			MetricKey: k.Key,
			Label:     k.Label,
			Unit:      k.Unit,
			ValueKind: k.ValueKind.String(),
			MinValue:  k.MinValue,
			MaxValue:  k.MaxValue,
			Direction: k.Direction.String(),
			CreatedAt: k.CreatedAt,
		})
	}
	respondJSON(w, http.StatusOK, struct {
		Items []metricKeyItem `json:"items"`
		Total int             `json:"total"`
	}{Items: items, Total: len(items)})
}

// parseRFC3339 parses one RFC3339 timestamp (the from/to query params of
// the metric endpoints).
func parseRFC3339(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q 不是 RFC3339 时间戳（示例 2026-01-15T00:00:00Z）", raw)
	}
	return t.UTC(), nil
}

// parseRFC3339Query validates one optional RFC3339 query parameter and
// returns its normalized storage form ("" when absent).
func parseRFC3339Query(w http.ResponseWriter, name, raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	t, err := parseRFC3339(raw)
	if err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request",
			"%s 参数 %s", name, err.Error())
		return "", false
	}
	return t.Format(time.RFC3339), true
}

// parseAgg validates the agg query parameter (raw|daily_avg|daily_min|
// daily_max, default raw).
func parseAgg(w http.ResponseWriter, raw string) (repo.AggKind, bool) {
	switch raw {
	case "", "raw":
		return repo.AggRaw, true
	case "daily_avg":
		return repo.AggDailyAvg, true
	case "daily_min":
		return repo.AggDailyMin, true
	case "daily_max":
		return repo.AggDailyMax, true
	default:
		respondErrorf(w, http.StatusBadRequest, "bad_request",
			"agg 参数 %q 不是 raw/daily_avg/daily_min/daily_max", raw)
		return "", false
	}
}

// handleResourceMetrics implements GET /api/resources/{id}/metrics
// ?keys=a,b&from=&to=&agg= (design §7.3): the resource's series, each with
// its points (raw or daily-aggregated), ordered by (metric_key, ts) —
// deterministic. keys is optional (all series of the resource); from/to
// are optional RFC3339 bounds; unknown resource → 404; unknown metric key
// in keys → 400.
func (s *Server) handleResourceMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.db.Resources.GetByID(r.Context(), id); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	q := r.URL.Query()

	var keys []string
	if raw := q.Get("keys"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, known := metrics.SpecForKey(part); !known {
				respondErrorf(w, http.StatusBadRequest, "bad_request",
					"keys 参数包含未知指标键 %q（封闭目录：%s）", part, strings.Join(metrics.KeySet(), ", "))
				return
			}
			keys = append(keys, part)
		}
	}
	from, ok := parseRFC3339Query(w, "from", q.Get("from"))
	if !ok {
		return
	}
	to, ok := parseRFC3339Query(w, "to", q.Get("to"))
	if !ok {
		return
	}
	agg, ok := parseAgg(w, q.Get("agg"))
	if !ok {
		return
	}

	series, err := s.metrics.ResourceSeries(r.Context(), id, keys, from, to, agg)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, struct {
		ResourceID string                 `json:"resourceId"`
		Series     []metrics.SeriesResult `json:"series"`
	}{ResourceID: id, Series: series})
}

// handleMetricAnomalies implements GET /api/metrics/anomalies
// ?metricKey=&from=&to=&threshold=&direction= (design §5.5 Q2 / §7.3 —
// the 每日巡检 core call): per series, the first/last points of the window
// and their delta; series with |delta| >= threshold are returned, grouped
// per resource and ordered (resource, metricKey) — deterministic.
//
// threshold defaults to 1.0; direction filters the delta sign
// (declining|rising|any, default any); the degrading flag is
// direction-aware per the metric catalog (higher_is_better & delta<0, or
// lower_is_better & delta>0 → degrading; neutral metrics never degrade by
// trend alone).
func (s *Server) handleMetricAnomalies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	metricKey := strings.TrimSpace(q.Get("metricKey"))
	if metricKey != "" {
		if _, known := metrics.SpecForKey(metricKey); !known {
			respondErrorf(w, http.StatusBadRequest, "bad_request",
				"metricKey 参数 %q 不是封闭目录中的指标键", metricKey)
			return
		}
	}
	from, ok := parseRFC3339Query(w, "from", q.Get("from"))
	if !ok {
		return
	}
	to, ok := parseRFC3339Query(w, "to", q.Get("to"))
	if !ok {
		return
	}
	threshold := 0.0
	if raw := q.Get("threshold"); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v <= 0 {
			respondErrorf(w, http.StatusBadRequest, "bad_request",
				"threshold 参数 %q 不是正数", raw)
			return
		}
		threshold = v
	}
	direction := strings.TrimSpace(q.Get("direction"))

	items, err := s.metrics.Anomalies(r.Context(), metricKey, from, to, threshold, direction)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, struct {
		Items     []metrics.AnomalyItem `json:"items"`
		Total     int                   `json:"total"`
		MetricKey string                `json:"metricKey,omitempty"`
	}{Items: items, Total: len(items), MetricKey: metricKey})
}

// handleMetricsAnalyze implements POST /api/metrics/analyze: multipart
// field "file" (.xlsx performance workbook, ≤10MB) → 200 with the offline
// analysis {suggestedMapping, columns, rowCount, warnings}. Stateless: no
// batch row is created (the metric channel's import is one-shot — the
// mapping and the file travel together in POST /api/metrics/imports).
func (s *Server) handleMetricsAnalyze(w http.ResponseWriter, r *http.Request) {
	if s.dataDir == "" {
		s.log.Error("metrics analyze rejected: dataDir is not configured",
			"method", r.Method, "path", r.URL.Path)
		respondError(w, http.StatusInternalServerError, "internal",
			"服务器未配置上传存储目录（dataDir），无法处理指标分析上传")
		return
	}
	path, name, ok := s.readMetricsUpload(w, r)
	if !ok {
		return
	}
	defer func() { _ = os.Remove(path) }() // analyze is stateless: scratch only

	a, err := metrics.AnalyzeOffline(path, name)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, a)
}

// metricsImportResponse is the 202 body of POST /api/metrics/imports.
type metricsImportResponse struct {
	BatchID       string `json:"batchId"`
	Status        string `json:"status"`
	RowsTotal     int    `json:"rowsTotal"`
	RowsImported  int    `json:"rowsImported"`
	RowsFailed    int    `json:"rowsFailed"`
	PointsWritten int    `json:"pointsWritten"`
	SeriesTouched int    `json:"seriesTouched"`
}

// handleMetricsImport implements POST /api/metrics/imports: multipart
// fields "file" (.xlsx, ≤10MB) + "mapping" (the confirmed ingest mapping
// JSON, field-mapped so the upload stays multipart). The upload is stored
// content-addressed like the resource channel (dataDir/uploads), the
// ingest runs in one transaction (all points or nothing, the failed-batch
// mark on error) and the response is the batch result envelope.
//
// Error taxonomy: a bad multipart/extension → 400/422 (same as the
// resource channel); an invalid mapping → 400 validation (the typed
// *metrics.FieldError is a mapping.FieldError alias, so the shared
// mapServiceError branch catches it); an unreadable workbook → 422
// not_xlsx / empty_workbook.
func (s *Server) handleMetricsImport(w http.ResponseWriter, r *http.Request) {
	if s.dataDir == "" {
		s.log.Error("metrics import rejected: dataDir is not configured",
			"method", r.Method, "path", r.URL.Path)
		respondError(w, http.StatusInternalServerError, "internal",
			"服务器未配置上传存储目录（dataDir），无法处理指标导入上传")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			respondError(w, http.StatusRequestEntityTooLarge, "too_large",
				fmt.Sprintf("上传文件超过 10MB 上限（实际 %d 字节）", maxErr.Limit))
			return
		}
		respondError(w, http.StatusBadRequest, "bad_request", "无法解析 multipart 请求："+err.Error())
		return
	}
	mappingRaw := r.FormValue("mapping")
	if strings.TrimSpace(mappingRaw) == "" {
		respondError(w, http.StatusBadRequest, "bad_request",
			"缺少 multipart 字段 \"mapping\"（指标导入映射 JSON）")
		return
	}
	m, err := metrics.ParseMapping([]byte(mappingRaw))
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request",
			"映射 JSON 解析失败："+err.Error())
		return
	}

	path, name, ok := s.readMetricsUpload(w, r)
	if !ok {
		return
	}

	sum, err := s.metrics.Ingest(r.Context(), path, m, name)
	if err != nil {
		// A mapping FieldError leaves no batch (nothing was imported); the
		// stored upload file stays content-addressed and is collected by the
		// next `netopslab gc` orphan sweep, exactly like the resource
		// channel's failed analyses.
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusAccepted, metricsImportResponse{
		BatchID:       sum.BatchID,
		Status:        sum.Status.String(),
		RowsTotal:     sum.RowsTotal,
		RowsImported:  sum.RowsImported,
		RowsFailed:    sum.RowsFailed,
		PointsWritten: sum.PointsWritten,
		SeriesTouched: sum.SeriesTouched,
	})
}

// readMetricsUpload extracts and persists the multipart "file" field the
// way the resource channel does (content-addressed
// dataDir/uploads/<sha256>_<name>) and returns its path + display name.
func (s *Server) readMetricsUpload(w http.ResponseWriter, r *http.Request) (path, name string, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			respondError(w, http.StatusRequestEntityTooLarge, "too_large",
				fmt.Sprintf("上传文件超过 10MB 上限（实际 %d 字节）", maxErr.Limit))
			return "", "", false
		}
		respondError(w, http.StatusBadRequest, "bad_request", "无法解析 multipart 请求："+err.Error())
		return "", "", false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", "缺少 multipart 字段 \"file\"")
		return "", "", false
	}
	defer func() { _ = file.Close() }()

	name = filepath.Base(header.Filename)
	if name == "" || name == "." || name == "/" {
		respondError(w, http.StatusBadRequest, "bad_request", "上传文件名无效")
		return "", "", false
	}
	if ext := strings.ToLower(filepath.Ext(name)); ext != ".xlsx" {
		respondError(w, http.StatusUnprocessableEntity, "not_xlsx",
			fmt.Sprintf("指标导入仅支持 .xlsx 工作簿，收到 %q", filepath.Ext(name)))
		return "", "", false
	}
	data, err := io.ReadAll(file)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			respondError(w, http.StatusRequestEntityTooLarge, "too_large", "上传文件超过 10MB 上限")
			return "", "", false
		}
		respondError(w, http.StatusBadRequest, "bad_request", "读取上传文件失败："+err.Error())
		return "", "", false
	}
	if len(data) == 0 {
		respondError(w, http.StatusUnprocessableEntity, "empty_workbook", "上传文件为空")
		return "", "", false
	}
	sum := sha256.Sum256(data)
	stored := filepath.Join(s.dataDir, "uploads", hex.EncodeToString(sum[:])+"_"+name)
	if err := os.MkdirAll(filepath.Join(s.dataDir, "uploads"), 0o755); err != nil {
		s.log.Error("create uploads dir failed", "error", err.Error())
		respondError(w, http.StatusInternalServerError, "internal", "服务器内部错误，无法保存上传文件")
		return "", "", false
	}
	if err := os.WriteFile(stored, data, 0o644); err != nil {
		s.log.Error("store upload failed", "error", err.Error(), "path", stored)
		respondError(w, http.StatusInternalServerError, "internal", "服务器内部错误，无法保存上传文件")
		return "", "", false
	}
	return stored, name, true
}

// metricBatchItem is one entry of GET /api/metrics/batches.
type metricBatchItem struct {
	ID           string     `json:"id"`
	WorkbookName string     `json:"workbookName"`
	SHA256       string     `json:"sha256"`
	Status       string     `json:"status"`
	RowsTotal    int        `json:"rowsTotal"`
	RowsImported int        `json:"rowsImported"`
	RowsFailed   int        `json:"rowsFailed"`
	Error        string     `json:"error"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt"`
}

// handleMetricBatchesList implements GET /api/metrics/batches
// ?page=&pageSize= (newest first — the /api/batches contract).
func (s *Server) handleMetricBatchesList(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	batches, total, err := s.metrics.ListBatches(r.Context(), pageSize, (page-1)*pageSize)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	items := make([]metricBatchItem, 0, len(batches))
	for _, b := range batches {
		items = append(items, metricBatchItem{
			ID:           b.ID,
			WorkbookName: b.WorkbookName,
			SHA256:       b.WorkbookSHA256,
			Status:       b.Status.String(),
			RowsTotal:    b.RowsTotal,
			RowsImported: b.RowsImported,
			RowsFailed:   b.RowsFailed,
			Error:        b.Error,
			StartedAt:    b.StartedAt,
			FinishedAt:   b.FinishedAt,
		})
	}
	respondJSON(w, http.StatusOK, struct {
		Items    []metricBatchItem `json:"items"`
		Total    int               `json:"total"`
		Page     int               `json:"page"`
		PageSize int               `json:"pageSize"`
	}{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// handleMetricBatchRollback implements POST /api/metrics/batches/{id}/
// rollback: reverse a completed metric batch (delete the points it
// currently owns, prune now-empty series, flip the status to
// rolled_back). Non-completed status → 409 conflict (the resource
// rollback's refusal semantics); unknown batch → 404.
func (s *Server) handleMetricBatchRollback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := s.metrics.Rollback(r.Context(), id)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, res)
}
