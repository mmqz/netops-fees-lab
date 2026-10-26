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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/repo"
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
	// #15: the UI's confirmation path — after the period-probe panel
	// showed a disagreement, the operator chose the DECLARED period;
	// the override rides the batch (period_source stays unverified).
	trustPeriod := parseBoolForm(r.FormValue("periodTrustDeclared"))

	summary, err := s.fees.Import(r.Context(), fees.ImportOptions{
		Path: staged, OriginalName: name, FeeType: feeType, Year: year, Month: month, Mapping: mapping,
		TrustDeclaredPeriod: trustPeriod,
	})
	if err != nil {
		var dup *fees.ErrDuplicateImport
		if errors.As(err, &dup) {
			// informational: the existing completed batch IS the answer
			respondJSON(w, http.StatusOK, summary)
			return
		}
		if errors.Is(err, fees.ErrImportRefused) {
			// #104: the operator's file is the problem — 422 with the honest
			// refusal text (bad format / unreadable / missing mapping). The
			// all-rows-failed shape carries a summary; it keeps its dedicated
			// code so the client can tell "fix the file, retry" from "wrong
			// file entirely".
			if summary.BatchID != "" && summary.Status == "failed" {
				respondError(w, http.StatusUnprocessableEntity, "fee_rows_failed", "全部行规范化失败："+
					firstString(summary.Errors, "无行错误详情（见批次记录 error 字段）"))
				return
			}
			respondError(w, http.StatusUnprocessableEntity, "fee_import_refused", err.Error())
			return
		}
		// #104: everything else (snapshot IO / DB / Parquet) is a SERVER
		// fault — it used to answer 422 fee_import_failed, steering the
		// operator toward fixing their file. respondErrorFor logs the detail
		// and answers the generic 500.
		s.respondErrorFor(w, r, err, "")
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

// parseBoolForm reads a multipart form boolean the tolerant way: the
// HTML checkbox family (1/true/on/yes) and the empty default (false).
// Anything else is false — the flag is an explicit confirmation, never a
// tri-state.
func parseBoolForm(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// handleFeesPeriodProbe implements POST /api/fees/period-probe (#15): the
// read-only content probe — upload the fee file, get back the table-kind
// classification and the three-tier period detection WITHOUT declaring
// anything and WITHOUT writing anything (no snapshot, no batch). This is
// the pre-import confirmation panel's data face: the UI shows the
// detected kind/period/basis, the operator picks or confirms the
// declaration, then calls POST /api/fees/imports.
func (s *Server) handleFeesPeriodProbe(w http.ResponseWriter, r *http.Request) {
	if s.dataDir == "" {
		s.log.Error("fees period probe rejected: dataDir is not configured",
			"method", r.Method, "path", r.URL.Path)
		respondError(w, http.StatusInternalServerError, "internal",
			"服务器未配置数据目录（dataDir），无法处理费用文件探测")
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
	// Stage under the temp probe area (content-addressed like the import
	// staging; the probe itself never snapshots — nothing persists).
	staged, err := stageFeeUpload(s.dataDir, file, name)
	if err != nil {
		if errors.Is(err, errUnsupportedFeeExt) || errors.Is(err, errXlsBinaryFormat) {
			respondError(w, http.StatusUnprocessableEntity, "unsupported_fee_file", err.Error())
			return
		}
		s.log.Error("store fee upload failed", "error", err.Error())
		respondError(w, http.StatusInternalServerError, "internal", "服务器内部错误，无法保存上传文件")
		return
	}
	sheet, err := fees.ReadFeeFileFirst(staged)
	if err != nil {
		respondError(w, http.StatusUnprocessableEntity, "fee_import_refused", err.Error())
		return
	}
	respondJSON(w, http.StatusOK, fees.DetectPeriod(sheet.Headers, sheet.Rows))
}

// errUnsupportedFeeExt answers a fee upload whose extension is outside
// csv/xlsx (.xls gets its own dedicated message — #92).
var errUnsupportedFeeExt = errors.New("仅支持 .csv / .xlsx 费用文件")

var errXlsBinaryFormat = errors.New(".xls 是 Excel 2003 二进制旧格式，不受支持——请先转换后重试：netopsfees convert --in <file.xls> --out <dir> [--format xlsx|csv]（批量保真转换，issue #7）")

// handleFeesImportSheets implements POST /api/fees/imports/sheets (#125):
// the multi-sheet channel — the REAL monthly ledger shape where one
// workbook holds one sheet per fee type (电费/铁塔租费/修理费). No feeType
// field exists here on purpose: each sheet's own evidence (name + header
// markers) declares its type, which is exactly what the single-sheet
// channel cannot see. 201 with the per-sheet batch array on a fresh
// import; 200 with the same shape (every entry skippedDuplicate) on the
// idempotent re-import — informational, never a failure. Classifiable-
// impossible files (CSV, an unclassifiable sheet, a sheet whose mandatory
// columns won't map, a file already imported through the single-sheet
// channel) answer 422 fee_import_refused BEFORE any sheet lands; a
// server fault mid-file answers 500 fee_import_partial naming both the
// batches that DID land (queryable, rollback-able) and the failing
// sheet — "completed with missing data" stays impossible.
func (s *Server) handleFeesImportSheets(w http.ResponseWriter, r *http.Request) {
	if s.dataDir == "" {
		s.log.Error("fees sheets import rejected: dataDir is not configured",
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
	if v := r.FormValue("feeType"); v != "" {
		// The channel's whole premise is sheet-declared types; an explicit
		// feeType here means the caller is on the wrong endpoint — send
		// them to the single-sheet channel instead of silently accepting.
		respondError(w, http.StatusBadRequest, "bad_request",
			"多 sheet 导入不接受 feeType——每张 sheet 的类型由表名/表头自动判定；需固定单一类型请用 POST /api/fees/imports")
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
	staged, err := stageFeeUpload(s.dataDir, file, name)
	if err != nil {
		if errors.Is(err, errUnsupportedFeeExt) || errors.Is(err, errXlsBinaryFormat) {
			respondError(w, http.StatusUnprocessableEntity, "unsupported_fee_file", err.Error())
			return
		}
		s.log.Error("store fee upload failed", "error", err.Error())
		respondError(w, http.StatusInternalServerError, "internal", "服务器内部错误，无法保存上传文件")
		return
	}
	summary, err := s.fees.ImportSheets(r.Context(), fees.SheetsImportOptions{
		Path: staged, OriginalName: name, Year: year, Month: month,
		TrustDeclaredPeriod: parseBoolForm(r.FormValue("periodTrustDeclared")),
	})
	if err != nil {
		if errors.Is(err, fees.ErrImportRefused) {
			// The all-or-nothing refusal: NOTHING landed — 422 with every
			// offending sheet named (the service text already lists them).
			respondError(w, http.StatusUnprocessableEntity, "fee_import_refused", err.Error())
			return
		}
		// A server fault AFTER some sheets landed: the summary is the
		// honest accounting — name the landed batches (each queryable,
		// rollback-able) inside the message so the operator can verify
		// or undo without guessing.
		landed := make([]string, 0, len(summary.Batches))
		for _, b := range summary.Batches {
			if b.Status == "completed" || b.SkippedDuplicate {
				landed = append(landed, b.BatchID)
			}
		}
		if len(landed) > 0 {
			s.log.Error("fees sheets import partially failed", "error", err.Error(),
				"landed", strings.Join(landed, ","))
			respondError(w, http.StatusInternalServerError, "fee_import_partial",
				fmt.Sprintf("多 sheet 导入部分失败——已落地批次：%s；失败原因：%s（已落地批次可查询/可回滚）",
					strings.Join(landed, ", "), err.Error()))
			return
		}
		s.respondErrorFor(w, r, err, "")
		return
	}
	// Pure-duplicate re-import is informational (200); anything that
	// landed new data answers 201 — the single-channel doctrine. The
	// service contract has only two nil-error shapes: every data sheet
	// completed, or every entry already a known duplicate.
	allSkipped := len(summary.Batches) > 0
	for _, b := range summary.Batches {
		if !b.SkippedDuplicate {
			allSkipped = false
			break
		}
	}
	if allSkipped {
		respondJSON(w, http.StatusOK, summary)
		return
	}
	respondJSON(w, http.StatusCreated, summary)
}

// stageFeeUpload writes the uploaded fee file under
// <dataDir>/uploads/fees/<sha256>_<name> and returns its path —
// content-addressed, the same pattern as the resource and metrics
// channels (#88). The importer later opens this staging path three
// times (HashFile → ReadFeeFile → WriteSnapshot); the name alone must
// therefore never be visible with partial bytes. #88 fixed the
// name-collision half (identical content shares one path, different
// content never collides) but kept the in-place O_TRUNC write: two
// concurrent same-content submissions truncated and rewrote the SAME
// file while the other request was reading it, so a reader could
// hash/parse a torn file and record a SHA256 that does not match the
// bytes actually imported — the evidence hash must never be able to
// lie (#106-A). #112 deduplicated the fix into storeUpload (uploads.go):
// the bytes land under a per-request ".tmp-*" temp name and rename into
// the content-addressed name only once complete — rename is atomic per
// file, so concurrent same-content uploads replace each other with
// whole identical files and no reader ever observes a partial one (this
// also self-heals a torn file a pre-#106 crash may have left at the
// target name). The extension doctrine stays channel-local: only
// .csv/.xlsx stage (.xls gets its dedicated refusal, #92).
func stageFeeUpload(dataDir string, file io.Reader, name string) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "csv", "xlsx":
	case "xls":
		return "", errXlsBinaryFormat
	default:
		return "", fmt.Errorf("%w（收到 .%s）", errUnsupportedFeeExt, ext)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("读取上传文件: %w", err)
	}
	return storeUpload(filepath.Join(dataDir, "uploads", "fees"), data, name)
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
			PeriodSource: b.PeriodSource,
			PeriodBasis:  b.PeriodBasis,
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
	ID           string `json:"id"`
	WorkbookName string `json:"workbookName"`
	FeeType      string `json:"feeType"`
	FeeTypeLabel string `json:"feeTypeLabel"`
	Period       string `json:"period"`
	// PeriodSource/PeriodBasis (#15): how the batch's period was
	// established (auto/unverified/manual) and the 识别依据 — the
	// provenance the three-tier detection records on every import.
	PeriodSource string     `json:"periodSource"`
	PeriodBasis  string     `json:"periodBasis,omitempty"`
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

// handleFeesEvidence implements GET /api/fees/batches/{id}/evidence?row=N
// (#86): reads one row of the batch's immutable snapshot back — the third
// tier of the evidence chain (conclusion → source_file+source_row anchor →
// the original text). row is the source file's own numbering (header = 1),
// the exact value every clue anchor and import-error detail carries. Works
// for rolled-back batches too (the snapshot survives rollback by design).
func (s *Server) handleFeesEvidence(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	row, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("row")))
	if err != nil || row < 1 {
		respondError(w, http.StatusBadRequest, "bad_request",
			"row 参数必须是 ≥ 1 的整数（源文件自身编号，表头 = 1）")
		return
	}
	ev, err := s.fees.EvidenceRow(r.Context(), r.PathValue("id"), row)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, ev)
}

// handleFeesQueryTotals implements GET /api/fees/query/totals?
// feeType=&year=&month=&limit= — the per-site totals preset (month 0 =
// whole year). 501 engine_not_built on default builds.
func (s *Server) handleFeesQueryTotals(w http.ResponseWriter, r *http.Request) {
	s.feesQuery(w, r, "totals")
}

// handleFeesQueryMonthly implements GET /api/fees/query/monthly?
// feeType=&year= — the #3 trend series (one row per month WITH data).
// A month parameter is refused: the series covers the whole year.
func (s *Server) handleFeesQueryMonthly(w http.ResponseWriter, r *http.Request) {
	s.feesQuery(w, r, "monthly")
}

// handleFeesQueryRows answers GET /api/fees/query/rows (#5): the raw
// analysis rows with the optional ledger columns.
func (s *Server) handleFeesQueryRows(w http.ResponseWriter, r *http.Request) {
	s.feesQuery(w, r, "rows")
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
	if raw := q.Get("limit"); raw != "" {
		// #90: the limit applies to both presets now (duplicates used to be
		// unbounded); 1..500 keeps the payload honest.
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 500 {
			respondError(w, http.StatusBadRequest, "bad_request", "limit 必须是 1–500 的整数")
			return
		}
	}

	if preset == "monthly" && month > 0 {
		respondError(w, http.StatusBadRequest, "bad_request", "monthly 不接受 month 参数——序列覆盖所选年份全部有数据的月份")
		return
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
	case "monthly":
		// #3: the trend series — the whole-year monthly breakdown. The
		// rows engine-direct discipline of the sibling presets applies.
		rows, err := engine.MonthlyTotals(r.Context(), analysis, feeType, year)
		if err != nil {
			s.respondErrorFor(w, r, err, "")
			return
		}
		respondJSON(w, http.StatusOK, feesMonthlyResult{
			Preset: "monthly", FeeType: string(feeType), Year: year, Items: rows, Total: len(rows),
		})
	case "duplicates":
		rows, err := engine.Duplicates(r.Context(), analysis, feeType, year, month, limit)
		if err != nil {
			s.respondErrorFor(w, r, err, "")
			return
		}
		respondJSON(w, http.StatusOK, feesDuplicatesResult{
			Preset: "duplicates", FeeType: string(feeType), Year: year, Month: month, Items: rows, Total: len(rows),
		})
	case "rows":
		// #5: the optional ledger columns' query surface — site and record
		// key (#4) filters optional, the same period/limit discipline.
		site := strings.TrimSpace(q.Get("site"))
		recordKey := strings.TrimSpace(q.Get("recordKey"))
		rows, err := engine.LedgerRows(r.Context(), analysis, feeType, year, month, site, recordKey, limit)
		if err != nil {
			s.respondErrorFor(w, r, err, "")
			return
		}
		respondJSON(w, http.StatusOK, feesRowsResult{
			Preset: "rows", FeeType: string(feeType), Year: year, Month: month, Site: site,
			RecordKey: recordKey, Items: rows, Total: len(rows),
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

// feesMonthlyResult is the #3 display-layer series answer: the twelve-
// month trend of one fee type's year. No month field — the series IS the
// whole year; months without data are absent from Items.
type feesMonthlyResult struct {
	Preset  string                  `json:"preset"`
	FeeType string                  `json:"feeType"`
	Year    int                     `json:"year"`
	Items   []fees.MonthlyTotalsRow `json:"items"`
	Total   int                     `json:"total"`
}

type feesRowsResult struct {
	Preset    string           `json:"preset"`
	FeeType   string           `json:"feeType"`
	Year      int              `json:"year"`
	Month     int              `json:"month"`
	Site      string           `json:"site"`
	RecordKey string           `json:"recordKey"`
	Items     []fees.LedgerRow `json:"items"`
	Total     int              `json:"total"`
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

// handleFeesLinksList implements GET /api/fees/links?status=&grade=&code=&resource_id=&page=&pageSize=
// — newest first, PAGED (#90: the list used to return every row unbounded
// and join the resource side with one GetByID per unique id). The resource
// join rides ONE GetByIDs query per page now; total is the full COUNT so
// the pager shows how far the list goes.
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
	// #90: paged fetch — page/pageSize via the shared pagination contract
	// (limit/offset also accepted); total is the full COUNT for the pager.
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	total, err := s.db.FeeSiteLinks.Count(r.Context(), filter)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	filter.Limit = pageSize
	filter.Offset = (page - 1) * pageSize
	links, err := s.fees.ListLinks(r.Context(), filter)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	// join the resource side for display in ONE query (a vanished resource
	// keeps the recorded key — the link's own fields still tell the story).
	ids := make([]string, 0, len(links))
	seen := map[string]struct{}{}
	for _, l := range links {
		if _, dup := seen[l.ResourceID]; !dup {
			seen[l.ResourceID] = struct{}{}
			ids = append(ids, l.ResourceID)
		}
	}
	byID, err := s.db.Resources.GetByIDs(r.Context(), ids)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
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
	respondJSON(w, http.StatusOK, feeLinksList{Items: items, Total: total, Page: page, PageSize: pageSize})
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
	Items    []feeLinkItem `json:"items"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"pageSize"`
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

// handleFeesRisks implements GET /api/fees/risks/{scenario}?feeType=&year=
// [&month=&status=&diffCents=&ratio=&z=&minSites=&limit=&maxEvidence=] —
// the risk-scenario presets (#64, #58 stage 4 + #114's scenario 7). Closed parameter set
// (every threshold explicit, defaults documented in fees-layer.md §7);
// default builds answer 501 engine_not_built like every engine preset.
func (s *Server) handleFeesRisks(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	scenario, err := fees.ParseRiskScenario(r.PathValue("scenario"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	q := r.URL.Query()
	feeType, err := fees.ParseFeeType(q.Get("feeType"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", "feeType 参数必须是 "+
			strings.Join(fees.FeeTypes(), "/")+"，收到 "+strconv.Quote(q.Get("feeType")))
		return
	}
	year, month, _, err := parseFeesQueryPeriod(q)
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	rq := fees.RiskQuery{FeeType: feeType, Year: year, Month: month}
	if raw := q.Get("status"); raw != "" {
		rq.Statuses = fees.SplitComma(raw)
	}
	if raw := q.Get("diffCents"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			respondError(w, http.StatusBadRequest, "bad_request", "diffCents 必须是非负整数（分）")
			return
		}
		rq.DiffCents = v
	}
	for _, p := range []struct {
		name string
		ptr  *float64
	}{
		{"ratio", &rq.Ratio},
		{"z", &rq.ZScore},
	} {
		if raw := q.Get(p.name); raw != "" {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil || v <= 0 {
				respondError(w, http.StatusBadRequest, "bad_request", p.name+" 必须是正数")
				return
			}
			*p.ptr = v
		}
	}
	for _, p := range []struct {
		name string
		ptr  *int
	}{
		{"minSites", &rq.MinSites},
		{"limit", &rq.Limit},
		{"maxEvidence", &rq.MaxEvidence},
	} {
		if raw := q.Get(p.name); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 {
				respondError(w, http.StatusBadRequest, "bad_request", p.name+" 必须是正整数")
				return
			}
			*p.ptr = v
		}
	}
	// #119: the audit view — dismissed clues too (marked). Default
	// false: dismissed are suppressed and counted in the envelope.
	switch q.Get("includeDecided") {
	case "", "0", "false":
	case "1", "true":
		rq.IncludeDecided = true
	default:
		respondError(w, http.StatusBadRequest, "bad_request", "includeDecided 必须是 0/1/true/false")
		return
	}
	engine := fees.Query(r.Context())
	if !engine.Available() {
		respondError(w, http.StatusNotImplemented, "engine_not_built", fees.ErrEngineNotBuilt.Error())
		return
	}
	result, err := s.fees.Risks(r.Context(), engine, scenario, rq)
	if err != nil {
		// #103: engine failures carry DuckDB/parquet error texts with
		// absolute filesystem paths — the raw text leaked to the client and
		// to the AI face. respondErrorFor LOGS the detail server-side and
		// answers the generic 500 message, the same vocabulary every other
		// route uses.
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// clueDecisionItem is the JSON shape of one ledger row.
type clueDecisionItem struct {
	ID         string `json:"id"`
	Scenario   string `json:"scenario"`
	ScenarioCn string `json:"scenarioCn"`
	FeeType    string `json:"feeType"`
	SiteCode   string `json:"siteCode"`
	Year       int    `json:"year"`
	Month      int    `json:"month"`
	Status     string `json:"status"`
	Note       string `json:"note"`
	DecidedAt  string `json:"decidedAt"`
}

// handleFeesClueDecisionPost implements POST /api/fees/risk-clue-decisions
// (#119): the HUMAN channel of the risk-clue verification loop — verify
// (confirmed real, stays visible as the known-issues ledger) or dismiss
// (ruled out, suppressed from the default output, counted in the
// envelope). The MCP face gains no write tool: an AI never verifies or
// dismisses a clue on a human's behalf. Re-deciding flips the status
// (UPSERT). NOT gated on the DuckDB engine: decisions persist on default
// builds too — the suppression applies the next time the engine runs.
func (s *Server) handleFeesClueDecisionPost(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	var body struct {
		Scenario string `json:"scenario"`
		FeeType  string `json:"feeType"`
		SiteCode string `json:"siteCode"`
		Year     int    `json:"year"`
		Month    int    `json:"month"`
		Status   string `json:"status"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", "请求体必须是合法 JSON："+err.Error())
		return
	}
	// validation precedes the write (the #64 acceptance shape): the
	// closed sets and the period bounds answer 400 with their own texts;
	// the service re-validates as defense in depth.
	if _, err := fees.ParseRiskScenario(body.Scenario); err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if _, err := fees.ParseFeeType(body.FeeType); err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if _, err := domain.ParseClueDecisionStatus(body.Status); err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if body.SiteCode == "" {
		respondError(w, http.StatusBadRequest, "bad_request", "siteCode 必填（线索的站址编码）")
		return
	}
	if body.Year < 2000 || body.Year > 2100 || body.Month < 0 || body.Month > 12 {
		respondError(w, http.StatusBadRequest, "bad_request",
			fmt.Sprintf("账期 %04d-%02d 不在合法范围（year 2000-2100，month 0-12）", body.Year, body.Month))
		return
	}
	d := domain.RiskClueDecision{
		Scenario: body.Scenario, FeeType: domain.FeeType(body.FeeType),
		SiteCode: body.SiteCode, Year: body.Year, Month: body.Month,
		Status: domain.ClueDecisionStatus(body.Status), Note: body.Note,
	}
	saved, err := s.fees.DecideClue(r.Context(), d)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, clueDecisionItem{
		ID: saved.ID, Scenario: saved.Scenario, ScenarioCn: fees.RiskScenarioLabels[fees.RiskScenario(saved.Scenario)],
		FeeType: string(saved.FeeType), SiteCode: saved.SiteCode,
		Year: saved.Year, Month: saved.Month, Status: string(saved.Status),
		Note: saved.Note, DecidedAt: saved.DecidedAt.Format(time.RFC3339),
	})
}

// handleFeesClueDecisionsList implements GET /api/fees/risk-clue-decisions
// — the decision ledger (newest first): the audit view of the human
// verification loop, with the same filter set as the links list.
func (s *Server) handleFeesClueDecisionsList(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	q := r.URL.Query()
	f := repo.ClueDecisionFilter{
		Scenario: q.Get("scenario"), FeeType: q.Get("feeType"),
		Status: q.Get("status"), SiteCode: q.Get("siteCode"),
	}
	// the closed-set filters refuse with 400 (the same shape as the POST
	// and every other fees route).
	if f.Scenario != "" {
		if _, err := fees.ParseRiskScenario(f.Scenario); err != nil {
			respondError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	if f.FeeType != "" {
		if _, err := fees.ParseFeeType(f.FeeType); err != nil {
			respondError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	if f.Status != "" {
		if _, err := domain.ParseClueDecisionStatus(f.Status); err != nil {
			respondError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	if raw := q.Get("year"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 2000 || v > 2100 {
			respondError(w, http.StatusBadRequest, "bad_request", "year 必须是 2000-2100")
			return
		}
		f.Year = v
	}
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	f.Limit, f.Offset = pageSize, (page-1)*pageSize
	items, total, err := s.fees.ListClueDecisions(r.Context(), f)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	out := make([]clueDecisionItem, 0, len(items))
	for _, d := range items {
		out = append(out, clueDecisionItem{
			ID: d.ID, Scenario: d.Scenario, ScenarioCn: fees.RiskScenarioLabels[fees.RiskScenario(d.Scenario)],
			FeeType: string(d.FeeType), SiteCode: d.SiteCode,
			Year: d.Year, Month: d.Month, Status: string(d.Status),
			Note: d.Note, DecidedAt: d.DecidedAt.Format(time.RFC3339),
		})
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"items": out, "total": total, "limit": f.Limit, "offset": f.Offset,
	})
}

// ---- the configurable risk-rule management of issue #2 (phase 1): the
// L1 six-tuple rules. Channel doctrine on the HTTP face: create is the
// DECLARER tier (the AI/operator channel — a fresh rule lands DISABLED,
// the conservative observation state); enable/disable is the ADMIN tier
// (the human channel — the same 403 doctrine as clue decisions and link
// confirms); the audit read is open. Every mutation's audit row carries
// the token-tier actor (the shared-token world cannot attribute
// per-person — the tier is the honest identity). ----

// feeRuleItem is the HTTP rule shape (the six-tuple travels whole).
type feeRuleItem struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	FeeType      string  `json:"feeType"`
	Metric       string  `json:"metric"`
	Aggregation  string  `json:"aggregation"`
	Dimension    string  `json:"dimension"`
	Comparator   string  `json:"comparator"`
	Threshold    float64 `json:"threshold"`
	WindowMonths int     `json:"windowMonths"`
	Severity     string  `json:"severity"`
	Description  string  `json:"description,omitempty"`
	Enabled      bool    `json:"enabled"`
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

func feeRuleItemOf(r domain.FeeRiskRule) feeRuleItem {
	return feeRuleItem{
		ID: r.ID, Name: r.Name, FeeType: string(r.FeeType), Metric: r.Metric,
		Aggregation: r.Aggregation, Dimension: r.Dimension, Comparator: r.Comparator,
		Threshold: r.Threshold, WindowMonths: r.WindowMonths, Severity: r.Severity,
		Description: r.Description, Enabled: r.Enabled,
		CreatedAt: r.CreatedAt.Format(time.RFC3339), UpdatedAt: r.UpdatedAt.Format(time.RFC3339),
	}
}

// feeRuleActor derives the audit actor from the request's token tier: the
// two-tier shared-token world cannot attribute per-person, so the tier is
// the honest identity (admin / declarer). With the gate off the actor
// records the channel itself (http — the single-operator compat row).
func (s *Server) feeRuleActor(r *http.Request) string {
	if !s.gateOn() {
		return "http"
	}
	if s.tokenIsAdmin(bearerToken(r)) {
		return "admin"
	}
	return "declarer"
}

// handleFeesRulesList implements GET /api/fees/rules — every rule
// (default) or only the enabled ones (?enabled=1). Read-open: the rule
// set is the evidence of WHAT fires, not a secret.
func (s *Server) handleFeesRulesList(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	onlyEnabled := r.URL.Query().Get("enabled") == "1"
	rules, err := s.fees.ListRules(r.Context(), onlyEnabled)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	out := make([]feeRuleItem, 0, len(rules))
	for _, rule := range rules {
		out = append(out, feeRuleItemOf(rule))
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"items": out, "total": len(out), "enabledOnly": onlyEnabled,
	})
}

// handleFeesRulesCreate implements POST /api/fees/rules — the AI/operator
// creation channel (declarer tier suffices; the rule lands DISABLED — a
// fresh rule fires nothing until a human enables it). 422 rule_refused
// carries the validation text; a duplicate name answers the same 422
// (the honest operator-facing vocabulary of the import refusals).
func (s *Server) handleFeesRulesCreate(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	var body fees.RuleInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", "请求体必须是合法 JSON："+err.Error())
		return
	}
	rule, err := s.fees.CreateRule(r.Context(), body, s.feeRuleActor(r))
	if err != nil {
		if errors.Is(err, fees.ErrImportRefused) {
			respondError(w, http.StatusUnprocessableEntity, "rule_refused", err.Error())
			return
		}
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusCreated, feeRuleItemOf(rule))
}

// handleFeesRuleSetEnabled implements POST /api/fees/rules/{id}/enable
// and .../disable — the HUMAN channel (Admin routes, the same 403
// doctrine as clue decisions; the MCP face has no such tool). The op
// rides the path tail; the audit row records the admin tier.
func (s *Server) handleFeesRuleSetEnabled(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	id := r.PathValue("id")
	enable := strings.HasSuffix(r.URL.Path, "/enable")
	if err := s.fees.SetRuleEnabled(r.Context(), id, enable, s.feeRuleActor(r)); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	rule, err := s.fees.RuleByID(r.Context(), id)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, feeRuleItemOf(rule))
}

// handleFeesRuleAudit implements GET /api/fees/rules/{id}/audit — the
// action ledger of one rule (created/enabled/disabled, newest first):
// the 阈值变更留痕 red line made visible (read-open like the list).
func (s *Server) handleFeesRuleAudit(w http.ResponseWriter, r *http.Request) {
	if s.fees == nil {
		respondError(w, http.StatusInternalServerError, "internal", "费用分析服务未装配")
		return
	}
	// The 404 contract: an unknown rule answers not_found, never an empty
	// ledger that reads like "no actions ever taken".
	if _, err := s.fees.RuleByID(r.Context(), r.PathValue("id")); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	entries, err := s.fees.RuleAudit(r.Context(), r.PathValue("id"))
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	out := make([]map[string]any, 0, len(entries))
	for _, a := range entries {
		out = append(out, map[string]any{
			"id": a.ID, "ruleId": a.RuleID, "action": a.Action, "actor": a.Actor,
			"changes": a.Changes, "at": a.At.Format(time.RFC3339),
		})
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"ruleId": r.PathValue("id"), "items": out, "total": len(out),
	})
}
