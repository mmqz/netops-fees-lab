package api

// fees.go — the HTTP surface of the fee analysis layer (#58/#60/#61,
// stage-2.5 of the route map): one import route (multipart upload + the
// declared type/period), the batch list/rollback, and the two query
// presets. All POST routes ride the opt-in write gate (authMiddleware
// gates every non-GET); the query presets answer 501 engine_not_built on
// default builds (the DuckDB engine compiles behind the `duckdb` build
// tag) — the import path itself works everywhere.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/fees"
	"github.com/mmqz/netops-lab/internal/repo"
)

// handleFeesImport implements POST /api/fees/imports: multipart fields
// "file" (the CSV/xlsx fee file, ≤10MB) + "feeType" + "year" + "month"
// (the declared period) + optional "map" (the column-mapping JSON, see
// the CLI's --map). 201 with the import summary on a fresh import; 200
// with the SAME summary shape (skippedDuplicate=true, existingBatch) on
// the idempotent duplicate — informational per the CLI's severity
// doctrine, never a failure. Row-level normalization failures land in
// summary.errors (counted, not fatal). A wholly unparseable file answers
// 422 fee_rows_failed; missing/invalid parameters answer 400.
func (s *Server) handleFeesImport(w http.ResponseWriter, r *http.Request) {
	if s.dataDir == "" {
		s.log.Error("fees import rejected: dataDir is not configured",
			"method", r.Method, "path", r.URL.Path)
		respondError(w, http.StatusInternalServerError, "internal",
			"服务器未配置数据目录（dataDir），无法处理费用导入")
		return
	}
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
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
	file, header, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", "缺少 multipart 字段 \"file\"")
		return
	}
	defer func() { _ = file.Close() }()

	name := filepath.Base(header.Filename)
	if name == "" || name == "." || name == "/" {
		respondError(w, http.StatusBadRequest, "bad_request", "上传文件名无效")
		return
	}
	feeType, err := fees.ParseFeeType(r.FormValue("feeType"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request",
			"feeType 必须是 electricity/tower_lease/repair/maintenance，收到 "+strconv.Quote(r.FormValue("feeType")))
		return
	}
	year, err := strconv.Atoi(strings.TrimSpace(r.FormValue("year")))
	if err != nil || year < 2000 || year > 2100 {
		respondError(w, http.StatusBadRequest, "bad_request", "year 必须是 2000–2100 的整数")
		return
	}
	month, err := strconv.Atoi(strings.TrimSpace(r.FormValue("month")))
	if err != nil || month < 1 || month > 12 {
		respondError(w, http.StatusBadRequest, "bad_request", "month 必须是 1–12 的整数")
		return
	}

	// Stage the upload under the data dir (the fee import takes a path;
	// the snapshot layer then content-addresses the copy it keeps — the
	// staging copy is transient and reused by content hash).
	staged, err := stageFeeUpload(s.dataDir, file, name)
	if err != nil {
		if errors.Is(err, errUnsupportedFeeExt) {
			respondError(w, http.StatusUnprocessableEntity, "unsupported_fee_file", err.Error())
			return
		}
		s.log.Error("store fee upload failed", "error", err.Error())
		respondError(w, http.StatusInternalServerError, "internal", "服务器内部错误，无法保存上传文件")
		return
	}

	var mapping map[string]string
	if raw := r.FormValue("map"); raw != "" {
		var m struct {
			Columns map[string]string `json:"columns"`
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			respondError(w, http.StatusBadRequest, "bad_request", "map 字段不是合法的列映射 JSON："+err.Error())
			return
		}
		mapping = m.Columns
	}

	summary, err := s.fees.Import(r.Context(), fees.ImportOptions{
		Path: staged, FeeType: feeType, Year: year, Month: month, Mapping: mapping,
	})
	if err != nil {
		var dup *fees.ErrDuplicateImport
		if errors.As(err, &dup) {
			// informational: the existing completed batch IS the answer
			respondJSON(w, http.StatusOK, summary)
			return
		}
		respondError(w, http.StatusUnprocessableEntity, "fee_import_failed", err.Error())
		return
	}
	if summary.Status == "failed" {
		// every row failed — the summary carries the errors; the caller
		// fixes the file and retries (the retry channel is open).
		respondError(w, http.StatusUnprocessableEntity, "fee_rows_failed", "全部行规范化失败："+
			firstString(summary.Errors, "无行错误详情（见批次记录 error 字段）"))
		return
	}
	respondJSON(w, http.StatusCreated, summary)
}

// errUnsupportedFeeExt answers a fee upload whose extension is outside
// csv/xlsx/xls.
var errUnsupportedFeeExt = errors.New("仅支持 .csv / .xlsx / .xls 费用文件")

// stageFeeUpload writes the uploaded fee file under
// <dataDir>/uploads/fees/<name> and returns its path (the caller's name
// is kept — the snapshot layer content-addresses its own copy, this
// staging copy only bridges the multipart stream to the path-based
// importer; same name re-upload overwrites, which is safe: the
// idempotency key is the CONTENT hash).
func stageFeeUpload(dataDir string, file io.Reader, name string) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "csv", "xlsx", "xls":
	default:
		return "", fmt.Errorf("%w（收到 .%s）", errUnsupportedFeeExt, ext)
	}
	dir := filepath.Join(dataDir, "uploads", "fees")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建费用上传目录: %w", err)
	}
	path := filepath.Join(dir, name)
	out, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("保存上传文件: %w", err)
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, file); err != nil {
		return "", fmt.Errorf("写入上传文件: %w", err)
	}
	return path, out.Close()
}

// handleFeesBatchesList implements GET /api/fees/batches?status= — the
// fee import batch list (newest first; empty status = all).
func (s *Server) handleFeesBatchesList(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	var status domain.FeeStatus
	if raw := r.URL.Query().Get("status"); raw != "" {
		switch domain.FeeStatus(raw) {
		case domain.FeeStatusCompleted, domain.FeeStatusFailed, domain.FeeStatusRolledBack:
			status = domain.FeeStatus(raw)
		default:
			respondError(w, http.StatusBadRequest, "bad_request",
				"status 必须是 completed/failed/rolled_back，收到 "+strconv.Quote(raw))
			return
		}
	}
	batches, err := s.fees.List(r.Context(), status)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	items := make([]feeBatchItem, 0, len(batches))
	for _, b := range batches {
		items = append(items, feeBatchItem{
			ID:           b.ID,
			WorkbookName: b.WorkbookName,
			FeeType:      string(b.FeeType),
			FeeTypeLabel: fees.FeeTypeLabel(b.FeeType),
			Period:       fees.PeriodLabel(b.FeeYear, b.FeeMonth),
			Status:       string(b.Status),
			RowsTotal:    b.RowsTotal,
			RowsImported: b.RowsImported,
			RowsFailed:   b.RowsFailed,
			SnapshotPath: b.SnapshotPath,
			CreatedAt:    b.CreatedAt,
			CompletedAt:  b.CompletedAt,
		})
	}
	respondJSON(w, http.StatusOK, feeBatchList{Items: items, Total: len(items)})
}

// feeBatchItem is the REST shape of one fee batch (camelCase, the API
// convention).
type feeBatchItem struct {
	ID           string     `json:"id"`
	WorkbookName string     `json:"workbookName"`
	FeeType      string     `json:"feeType"`
	FeeTypeLabel string     `json:"feeTypeLabel"`
	Period       string     `json:"period"`
	Status       string     `json:"status"`
	RowsTotal    int        `json:"rowsTotal"`
	RowsImported int        `json:"rowsImported"`
	RowsFailed   int        `json:"rowsFailed"`
	SnapshotPath string     `json:"snapshotPath"`
	CreatedAt    time.Time  `json:"createdAt"`
	CompletedAt  *time.Time `json:"completedAt"`
}

type feeBatchList struct {
	Items []feeBatchItem `json:"items"`
	Total int            `json:"total"`
}

// handleFeesRollback implements POST /api/fees/batches/{id}/rollback:
// delete the batch's Parquet files (the analysis rows disappear), keep
// the snapshot (evidence), flip the row to rolled_back. Only completed
// batches roll back (409 otherwise); unknown id 404.
func (s *Server) handleFeesRollback(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	b, err := s.fees.Rollback(r.Context(), r.PathValue("id"))
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, feeBatchItem{
		ID: b.ID, WorkbookName: b.WorkbookName, FeeType: string(b.FeeType),
		FeeTypeLabel: fees.FeeTypeLabel(b.FeeType), Period: fees.PeriodLabel(b.FeeYear, b.FeeMonth),
		Status: string(b.Status), RowsTotal: b.RowsTotal, RowsImported: b.RowsImported,
		RowsFailed: b.RowsFailed, SnapshotPath: b.SnapshotPath, CreatedAt: b.CreatedAt,
	})
}

// handleFeesQueryTotals implements GET /api/fees/query/totals?
// feeType=&year=&month=&limit= — the per-site totals preset (month 0 =
// whole year). 501 engine_not_built on default builds.
func (s *Server) handleFeesQueryTotals(w http.ResponseWriter, r *http.Request) {
	s.feesQuery(w, r, "totals")
}

// handleFeesQueryDuplicates implements GET /api/fees/query/duplicates?
// feeType=&year=&month= — the cross-batch duplicate-settlement preset
// (risk leads with evidence anchors, never payment-duplicate verdicts).
func (s *Server) handleFeesQueryDuplicates(w http.ResponseWriter, r *http.Request) {
	s.feesQuery(w, r, "duplicates")
}

// feesQuery is the shared query-preset entry: parse/validate the closed
// parameter set, then dispatch to the engine (fixed SQL templates — no
// caller SQL ever reaches the engine).
func (s *Server) feesQuery(w http.ResponseWriter, r *http.Request, preset string) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	q := r.URL.Query()
	feeType, err := fees.ParseFeeType(q.Get("feeType"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", "feeType 参数必须是 "+
			strings.Join(fees.FeeTypes(), "/")+"，收到 "+strconv.Quote(q.Get("feeType")))
		return
	}
	year, month, limit, err := parseFeesQueryPeriod(q)
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if preset == "totals" {
		if raw := q.Get("limit"); raw != "" {
			limit, err = strconv.Atoi(raw)
			if err != nil || limit < 1 {
				respondError(w, http.StatusBadRequest, "bad_request", "limit 必须是正整数")
				return
			}
		}
	}

	engine := fees.Query(r.Context())
	if !engine.Available() {
		respondError(w, http.StatusNotImplemented, "engine_not_built", fees.ErrEngineNotBuilt.Error())
		return
	}
	analysis := filepath.Join(s.dataDir, "analysis")
	switch preset {
	case "totals":
		rows, err := engine.Totals(r.Context(), analysis, feeType, year, month, limit)
		if err != nil {
			s.respondErrorFor(w, r, err, "")
			return
		}
		respondJSON(w, http.StatusOK, feesTotalsResult{
			Preset: "totals", FeeType: string(feeType), Year: year, Month: month, Items: rows, Total: len(rows),
		})
	case "duplicates":
		rows, err := engine.Duplicates(r.Context(), analysis, feeType, year, month)
		if err != nil {
			s.respondErrorFor(w, r, err, "")
			return
		}
		respondJSON(w, http.StatusOK, feesDuplicatesResult{
			Preset: "duplicates", FeeType: string(feeType), Year: year, Month: month, Items: rows, Total: len(rows),
		})
	}
}

// parseFeesQueryPeriod validates year (mandatory) and month (optional,
// 0 = whole year).
func parseFeesQueryPeriod(q map[string][]string) (year, month, limit int, err error) {
	year, err = strconv.Atoi(single(q, "year"))
	if err != nil || year < 2000 || year > 2100 {
		return 0, 0, 0, fmt.Errorf("year 必须是 2000–2100 的整数，收到 %q", single(q, "year"))
	}
	if raw := single(q, "month"); raw != "" {
		month, err = strconv.Atoi(raw)
		if err != nil || month < 1 || month > 12 {
			return 0, 0, 0, fmt.Errorf("month 必须是 1–12 的整数，收到 %q", raw)
		}
	}
	return year, month, 20, nil
}

// single reads one query value ("" when absent).
func single(q map[string][]string, key string) string {
	vs := q[key]
	if len(vs) == 0 {
		return ""
	}
	return vs[0]
}

type feesTotalsResult struct {
	Preset  string           `json:"preset"`
	FeeType string           `json:"feeType"`
	Year    int              `json:"year"`
	Month   int              `json:"month"`
	Items   []fees.TotalsRow `json:"items"`
	Total   int              `json:"total"`
}

type feesDuplicatesResult struct {
	Preset  string              `json:"preset"`
	FeeType string              `json:"feeType"`
	Year    int                 `json:"year"`
	Month   int                 `json:"month"`
	Items   []fees.DuplicateRow `json:"items"`
	Total   int                 `json:"total"`
}

// firstString returns the first non-empty string (fallback def).
func firstString(ss []string, def string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return def
}

// handleFeesLinksScan implements POST /api/fees/links/scan: run the
// graded matcher (exact → rule → fuzzy proposals) over the live Parquet
// code universe. Returns the scan counters.
func (s *Server) handleFeesLinksScan(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	result, err := s.fees.ScanLinks(r.Context())
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// handleFeesLinksList implements GET /api/fees/links?status=&grade=&code=&resource_id=
// — newest first; the resource join (display name + status) rides the
// resources repo, mirroring the renames list's join.
func (s *Server) handleFeesLinksList(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	q := r.URL.Query()
	filter := repo.FeeLinkFilter{Code: q.Get("code"), Resource: q.Get("resource_id")}
	if raw := q.Get("status"); raw != "" {
		switch domain.LinkStatus(raw) {
		case domain.LinkStatusConfirmed, domain.LinkStatusPending, domain.LinkStatusDismissed:
			filter.Status = domain.LinkStatus(raw)
		default:
			respondError(w, http.StatusBadRequest, "bad_request",
				"status 必须是 confirmed/pending/dismissed，收到 "+strconv.Quote(raw))
			return
		}
	}
	if raw := q.Get("grade"); raw != "" {
		switch domain.LinkGrade(raw) {
		case domain.LinkGradeExact, domain.LinkGradeRule, domain.LinkGradeFuzzy:
			filter.Grade = domain.LinkGrade(raw)
		default:
			respondError(w, http.StatusBadRequest, "bad_request",
				"grade 必须是 exact/rule/fuzzy，收到 "+strconv.Quote(raw))
			return
		}
	}
	links, err := s.fees.ListLinks(r.Context(), filter)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	// join the resource side for display (a vanished resource keeps the
	// recorded key — the link's own fields still tell the story).
	ids := make([]string, 0, len(links))
	seen := map[string]struct{}{}
	for _, l := range links {
		if _, dup := seen[l.ResourceID]; !dup {
			seen[l.ResourceID] = struct{}{}
			ids = append(ids, l.ResourceID)
		}
	}
	byID := map[string]domain.Resource{}
	for _, id := range ids {
		if res, err := s.db.Resources.GetByID(r.Context(), id); err == nil {
			byID[id] = res
		}
	}
	items := make([]feeLinkItem, 0, len(links))
	for _, l := range links {
		item := feeLinkItem{
			ID: l.ID, FeeSiteCode: l.FeeSiteCode, ResourceID: l.ResourceID,
			ResourceType: string(l.ResourceType), ResourceKey: l.ResourceKey,
			Grade: string(l.Grade), Rule: l.Rule, Similarity: l.Similarity,
			Status: string(l.Status), Source: string(l.Source), CreatedAt: l.CreatedAt, DecidedAt: l.DecidedAt,
		}
		if res, ok := byID[l.ResourceID]; ok {
			item.ResourceName = res.Name
			item.ResourceStatus = res.Status
		}
		items = append(items, item)
	}
	respondJSON(w, http.StatusOK, feeLinksList{Items: items, Total: len(items)})
}

// feeLinkItem is the REST shape of one link (joined display fields).
type feeLinkItem struct {
	ID             string     `json:"id"`
	FeeSiteCode    string     `json:"feeSiteCode"`
	ResourceID     string     `json:"resourceId"`
	ResourceType   string     `json:"resourceType"`
	ResourceKey    string     `json:"resourceKey"`
	ResourceName   string     `json:"resourceName,omitempty"`
	ResourceStatus string     `json:"resourceStatus,omitempty"`
	Grade          string     `json:"grade"`
	Rule           string     `json:"rule,omitempty"`
	Similarity     float64    `json:"similarity,omitempty"`
	Status         string     `json:"status"`
	Source         string     `json:"source"`
	CreatedAt      time.Time  `json:"createdAt"`
	DecidedAt      *time.Time `json:"decidedAt"`
}

type feeLinksList struct {
	Items []feeLinkItem `json:"items"`
	Total int           `json:"total"`
}

// handleFeesLinkConfirm implements POST /api/fees/links/{id}/confirm:
// pending → confirmed (source human). A confirmed link already holding
// the code → 409; non-pending or unknown → 409/404.
func (s *Server) handleFeesLinkConfirm(w http.ResponseWriter, r *http.Request) {
	s.feesLinkDecide(w, r, "confirm")
}

// handleFeesLinkDismiss implements POST /api/fees/links/{id}/dismiss:
// pending → dismissed (audit kept).
func (s *Server) handleFeesLinkDismiss(w http.ResponseWriter, r *http.Request) {
	s.feesLinkDecide(w, r, "dismiss")
}

func (s *Server) feesLinkDecide(w http.ResponseWriter, r *http.Request, op string) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	var (
		l   domain.FeeSiteLink
		err error
	)
	if op == "confirm" {
		l, err = s.fees.ConfirmLink(r.Context(), r.PathValue("id"))
	} else {
		l, err = s.fees.DismissLink(r.Context(), r.PathValue("id"))
	}
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, feeLinkItem{
		ID: l.ID, FeeSiteCode: l.FeeSiteCode, ResourceID: l.ResourceID,
		ResourceType: string(l.ResourceType), ResourceKey: l.ResourceKey,
		Grade: string(l.Grade), Rule: l.Rule, Similarity: l.Similarity,
		Status: string(l.Status), Source: string(l.Source), CreatedAt: l.CreatedAt, DecidedAt: l.DecidedAt,
	})
}
