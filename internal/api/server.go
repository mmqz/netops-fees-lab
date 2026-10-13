// Package api implements the HTTP API of NetOps Lab (PROJECT_BRIEF §9) on
// the Go 1.27 net/http ServeMux with method patterns. Every handler:
//
//   - takes a context from the request,
//   - answers JSON (UTF-8) via respondJSON,
//   - reports failures through the uniform error envelope
//     {"error":{"code","message"}} with proper status codes
//     (400 bad_request/validation, 404 not_found, 409 batch_in_use/conflict,
//     413 too_large, 422 not_xlsx/empty_workbook/workbook_too_large/
//     unsupported_script/script_parse_error, 500 internal).
//
// Cross-cutting concerns live in the middleware stack installed by Routes:
// panic recovery (500 + logged stack, no leak to the client) and request
// logging (slog JSON: method, path, status, duration_ms, bytes). The import
// upload route enforces the 10MB body limit itself via http.MaxBytesReader.
//
// Unknown /api/ paths answer the JSON 404 envelope; every other path falls
// through to the embedded SPA (internal/webui) with an index.html fallback.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/changes"
	"github.com/mmqz/netops-lab/internal/detect"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/governance"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/ptpconfig"
	"github.com/mmqz/netops-lab/internal/query"
	"github.com/mmqz/netops-lab/internal/renames"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/report"
	"github.com/mmqz/netops-lab/internal/services"
	"github.com/mmqz/netops-lab/internal/sim"
	"github.com/mmqz/netops-lab/internal/validation"
	"github.com/mmqz/netops-lab/internal/webui"
	"github.com/mmqz/netops-lab/internal/workbook"
)

// maxUploadBytes is the request body limit of the import upload route
// (PROJECT_BRIEF §9: ≤10MB).
const maxUploadBytes = 10 << 20

// Server is the HTTP API server. It is wired from ONE *services.Services
// composition root (W9-D): the importer, validation engine, query service,
// report generator, attribute change and rename confirmation services and
// the simulation engine are the SAME instances the App (and the MCP server)
// use — New no longer assembles a second set from the bare db. Handlers with
// thin repo-passthrough read paths keep direct db access via svc.DB().
type Server struct {
	log        *slog.Logger
	db         *repo.DB
	dataDir    string
	svc        *services.Services
	importer   *importer.Service
	metrics    *metrics.Service
	validation *validation.Engine
	query      *query.Service
	report     *report.Generator
	sim        *sim.Service
	lab        *report.LabGenerator
	changes    *changes.Service
	renames    *renames.Service
	governance *governance.Service
	// detect is the W23 alert detection engine (the SAME instance the
	// CLI's `netopslab detect` uses — the composition root hands it to
	// both, no bypass).
	detect *detect.Engine
}

// New returns a Server wired from svc — the single composition root the
// application assembled once (app.New builds it and passes the same instance
// here, so the App's service fields and the HTTP handlers share every
// service instance; the wiring test asserts the pointer identity). Uploads
// are stored under <dataDir>/uploads (created lazily on the first upload). An
// empty dataDir leaves POST /api/imports failing FAST with a 500 "dataDir
// 未配置" error at the handler entry: the analyze step reads the workbook
// back from the stored path, so without a data root the upload route can
// never succeed (AUD1-10). All call sites (app.New with its "./data"
// default, the tests with t.TempDir()) pass a real directory.
func New(log *slog.Logger, svc *services.Services, dataDir string) *Server {
	return &Server{
		log:        log,
		db:         svc.DB(),
		dataDir:    dataDir,
		svc:        svc,
		importer:   svc.Importer,
		metrics:    svc.Metrics,
		validation: svc.Validation,
		query:      svc.Query,
		report:     svc.Report,
		sim:        svc.Lab,
		lab:        svc.LabReport,
		changes:    svc.Changes,
		renames:    svc.Renames,
		governance: svc.Governance,
		detect:     svc.Detect,
	}
}

// Services returns the composition root the server was wired from — the
// W9-D single-assembly handle. app.New passes its one instance through New;
// the wiring test (internal/services) asserts a.API.Services() and the App's
// own handle are the SAME pointer, which is what "no dead second assembly"
// means in code.
func (s *Server) Services() *services.Services { return s.svc }

// Routes builds the full HTTP handler: the §9 API routes with logging and
// recovery middleware, the JSON 404 envelope for unknown /api/ paths, and
// the embedded SPA for everything else. The returned handler is what
// cmd/netopslab serve and the httptest-based tests mount.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)

	// Dynamic schema discovery (W5-D, v0.4.0): the faithful projection
	// of mapping.Specs + the domain rule catalog. HTTP-only — the MCP
	// tool surface stays frozen at the five read-only tools.
	mux.HandleFunc("GET /api/schema", s.handleSchema)

	mux.HandleFunc("POST /api/imports", s.handleImportUpload)
	mux.HandleFunc("POST /api/imports/{id}/mapping", s.handleImportMapping)

	// W24 unattended import channel (§12-B7 方案 A, issue #28): the one-click
	// template capture of a completed batch's confirmed mapping + the
	// template listing/deletion. POST /api/imports?auto=1 (the auto path) is
	// the SAME upload route above with the flag — see handleImportUpload.
	// The MCP tool surface stays frozen at the five read-only tools: the
	// template lifecycle is an HTTP/CLI human+ops channel, never an
	// AI-controlled one.
	mux.HandleFunc("POST /api/imports/{id}/template", s.handleImportTemplateCapture)
	mux.HandleFunc("GET /api/mapping-templates", s.handleMappingTemplatesList)
	mux.HandleFunc("DELETE /api/mapping-templates/{id}", s.handleMappingTemplateDelete)

	mux.HandleFunc("GET /api/batches", s.handleBatchesList)
	mux.HandleFunc("GET /api/batches/{id}", s.handleBatchDetail)
	mux.HandleFunc("GET /api/batches/{id}/rows", s.handleBatchRows)
	mux.HandleFunc("DELETE /api/batches/{id}", s.handleBatchDelete)

	mux.HandleFunc("GET /api/resources", s.handleResourcesList)
	mux.HandleFunc("GET /api/resources/{id}", s.handleResourceDetail)
	mux.HandleFunc("GET /api/resources/{id}/relations", s.handleResourceRelations)

	// W22 run-state model (§12-B1 方案 C, issue #22): the MANUAL observation
	// channel of the operational state + the flap-analysis history. The MCP
	// tool surface stays frozen at the five read-only tools — run-state
	// writes are HTTP/CLI-side human (W22) / engine (W23, source=alert)
	// bookkeeping, never an AI-controlled channel.
	mux.HandleFunc("POST /api/resources/{id}/run-state", s.handleRunStateSet)
	mux.HandleFunc("GET /api/resources/{id}/run-state/history", s.handleRunStateHistory)

	// W22 graph query endpoints (§7.3 后两个 + §10): the layer-delta vertical
	// traverse, the impact (root-cause candidates / impacted surface) and the
	// one-shot AI context assembly. The legacy /relations endpoint above
	// stays untouched (deprecated in docs — §10.10 并行提供). MCP stays
	// frozen at five tools: the engines are exposed over HTTP only.
	mux.HandleFunc("GET /api/resources/{id}/traverse", s.handleResourceTraverse)
	mux.HandleFunc("GET /api/resources/{id}/impact", s.handleResourceImpact)
	mux.HandleFunc("GET /api/resources/{id}/context", s.handleResourceContext)

	// Resource export channel (W12-D, v0.6.0): GET the current resource
	// inventory as an import-isomorphic xlsx download. HTTP + CLI + web
	// button only — the MCP tool surface stays frozen at the five
	// read-only tools (an export is a bulk read, but the freeze is the
	// doctrine; nothing new is exposed to the AI channel).
	mux.HandleFunc("GET /api/exports/resources", s.handleExportResources)

	// Governance overview (W14-D, v0.6.0): the read-only aggregate of the
	// pending confirmation queues, the decision audit counters, the issue
	// distribution and the inventory shapes. No parameters, always 200
	// (all-zero structure on an empty database). MCP stays frozen at the
	// five read-only tools — nothing new is exposed to the AI channel.
	mux.HandleFunc("GET /api/governance/summary", s.handleGovernanceSummary)

	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/issues", s.handleIssues)
	mux.HandleFunc("POST /api/validate", s.handleValidate)
	mux.HandleFunc("GET /api/report", s.handleReport)
	mux.HandleFunc("GET /api/topology/check", s.handleTopologyCheck)

	// Attribute change confirmation (W4-D, migration v11): the HUMAN
	// channel of the update-on-diff import mode. The MCP tool surface
	// stays frozen at the five read-only tools — an AI must never apply
	// attribute changes (resource-domain writes are HTTP/CLI only).
	mux.HandleFunc("GET /api/attribute-changes", s.handleAttributeChangesList)
	mux.HandleFunc("POST /api/attribute-changes/{id}/apply", s.handleAttributeChangeApply)
	mux.HandleFunc("POST /api/attribute-changes/{id}/dismiss", s.handleAttributeChangeDismiss)

	// Rename confirmation (W6-D, migration v12): the HUMAN channel of the
	// "name change = identity change event" semantics. The MCP tool surface
	// stays frozen at the five read-only tools — an AI must never merge
	// resource identities (resource-domain writes are HTTP/CLI only).
	mux.HandleFunc("GET /api/renames", s.handleRenamesList)
	mux.HandleFunc("POST /api/renames/{id}/apply", s.handleRenameApply)
	mux.HandleFunc("POST /api/renames/{id}/reject", s.handleRenameReject)

	// Simulation workbench (PROJECT_BRIEF_P2 §7). The sim engine owns ALL
	// writes to the sim tables; the handlers only decode, call and project.
	mux.HandleFunc("GET /api/workspaces", s.handleWorkspacesList)
	mux.HandleFunc("POST /api/workspaces", s.handleWorkspacesCreate)
	mux.HandleFunc("GET /api/workspaces/{id}", s.handleWorkspaceDetail)
	mux.HandleFunc("DELETE /api/workspaces/{id}", s.handleWorkspaceDelete)
	mux.HandleFunc("GET /api/workspaces/{id}/topology", s.handleWorkspaceTopology)
	mux.HandleFunc("POST /api/workspaces/{id}/topology/path", s.handleWorkspacePath)
	mux.HandleFunc("POST /api/workspaces/{id}/ops", s.handleWorkspaceOps)
	mux.HandleFunc("POST /api/workspaces/{id}/checks/run", s.handleWorkspaceChecksRun)
	mux.HandleFunc("GET /api/workspaces/{id}/checks", s.handleWorkspaceChecksList)
	mux.HandleFunc("GET /api/workspaces/{id}/checks/{runId}", s.handleWorkspaceCheckRun)
	mux.HandleFunc("GET /api/workspaces/{id}/checks/{runId}/diff", s.handleWorkspaceRunDiff)
	mux.HandleFunc("GET /api/workspaces/{id}/changesets", s.handleWorkspaceChangeSets)
	mux.HandleFunc("POST /api/workspaces/{id}/undo", s.handleWorkspaceUndo)
	mux.HandleFunc("POST /api/workspaces/{id}/checkpoints", s.handleCheckpointCreate)
	mux.HandleFunc("GET /api/workspaces/{id}/checkpoints", s.handleCheckpointsList)
	mux.HandleFunc("POST /api/workspaces/{id}/checkpoints/{cpId}/restore", s.handleCheckpointRestore)
	mux.HandleFunc("GET /api/lab/report", s.handleLabReport)

	// Timeseries metric channel (W20, migration v14 — design §7.3 first
	// three query endpoints + the ADR-0011 D1 ingest channel). The MCP
	// tool surface stays frozen at the five read-only tools: metric data
	// is exposed over HTTP only (ADR-0011 D5).
	mux.HandleFunc("GET /api/metrics/keys", s.handleMetricKeys)
	mux.HandleFunc("GET /api/metrics/anomalies", s.handleMetricAnomalies)
	mux.HandleFunc("GET /api/resources/{id}/metrics", s.handleResourceMetrics)
	mux.HandleFunc("POST /api/metrics/analyze", s.handleMetricsAnalyze)
	mux.HandleFunc("POST /api/metrics/imports", s.handleMetricsImport)
	mux.HandleFunc("GET /api/metrics/batches", s.handleMetricBatchesList)
	mux.HandleFunc("POST /api/metrics/batches/{id}/rollback", s.handleMetricBatchRollback)

	// W23 alert detection endpoints (ADR-0011 D2/D3, §12-B2, issue
	// #23): the detection rule CRUD, the engine run (the
	// daily-inspection sweep — one call, one transaction, deterministic)
	// and the alert lifecycle (list / ack / clear). The MCP tool surface
	// stays frozen at the five read-only tools (ADR-0011 D5): AI reads
	// alerts via HTTP.
	mux.HandleFunc("GET /api/detection/rules", s.handleDetectionRulesList)
	mux.HandleFunc("POST /api/detection/rules", s.handleDetectionRulesCreate)
	mux.HandleFunc("PATCH /api/detection/rules/{id}", s.handleDetectionRulesPatch)
	mux.HandleFunc("DELETE /api/detection/rules/{id}", s.handleDetectionRulesDelete)
	mux.HandleFunc("POST /api/detection/run", s.handleDetectionRun)
	mux.HandleFunc("GET /api/alerts", s.handleAlertsList)
	mux.HandleFunc("POST /api/alerts/{id}/ack", s.handleAlertAck)
	mux.HandleFunc("POST /api/alerts/{id}/clear", s.handleAlertClear)

	// Unknown /api/ paths: JSON 404 envelope (never the SPA fallback).
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		respondError(w, http.StatusNotFound, "not_found",
			fmt.Sprintf("未知的 API 路径：%s %s", r.Method, r.URL.Path))
	})

	// Everything else: the embedded single-page application.
	mux.Handle("/", webui.Handler())

	return s.middleware(mux)
}

// middleware wraps the mux with panic recovery and request logging. All /api
// responses carry Cache-Control: no-store: they are dynamic database views
// and must never be served from a browser's HTTP cache (heuristic caching of
// unvalidated 200s once produced stale-statistics illusions during UI
// testing; see docs/adr/0006-api-no-store.md).
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if p := recover(); p != nil {
				s.log.Error("panic recovered",
					"method", r.Method,
					"path", r.URL.Path,
					"panic", fmt.Sprint(p),
					"stack", string(debug.Stack()),
				)
				// Never leak the panic value to the client.
				if !rec.wroteHeader {
					respondError(rec, http.StatusInternalServerError, "internal",
						"服务器内部错误，请查看服务端日志")
				}
			}
			s.log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", float64(time.Since(start).Microseconds())/1000.0,
				"bytes", rec.bytes,
			)
		}()
		next.ServeHTTP(rec, r)
	})
}

// statusRecorder captures the response status code and byte count for the
// request log. It forwards Flush and Unwrap so streaming responses keep
// working (http.ResponseController support).
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush implements http.Flusher when the wrapped writer does.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer so http.ResponseController reaches its
// capabilities (the doc comment above claims it — now it is real).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// respondJSON writes v as an indented UTF-8 JSON body with the given status.
func respondJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		// The header is already written; nothing more we can do.
		_ = err
	}
}

// errorEnvelope is the uniform failure body (§9).
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// respondError writes the error envelope with the given status/code.
func respondError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

// respondErrorf is respondError with a formatted message.
func respondErrorf(w http.ResponseWriter, status int, code, format string, args ...any) {
	respondError(w, status, code, fmt.Sprintf(format, args...))
}

// mapServiceError converts a service/repo error into (status, code,
// message): *mapping.FieldError → 400 validation (the message carries the
// sheet/field detail the UI points at); workbook sentinels → 422
// (not_xlsx / empty_workbook / workbook_too_large — the decompressed-size
// guard of the workbook parser); PTP script errors → 422
// (script_parse_error, the message carries the script line number;
// unsupported_script — a .txt/.cfg without the script fingerprint);
// domain.ErrNotFound → 404; domain.ErrConflict → 409. Anything else is a
// 500 (the caller logs it).
func mapServiceError(err error) (status int, code string, message string) {
	var fieldErr *mapping.FieldError
	if errors.As(err, &fieldErr) {
		return http.StatusBadRequest, "validation", fieldErr.Error()
	}
	switch {
	case errors.Is(err, workbook.ErrNotXLSX):
		return http.StatusUnprocessableEntity, "not_xlsx", "文件不是有效的 xlsx 工作簿"
	case errors.Is(err, workbook.ErrUnsupportedScript):
		return http.StatusUnprocessableEntity, "unsupported_script",
			"文件不是可识别的 PTP 配置脚本（.txt/.cfg 仅支持中兴 VRP 风格 PTP 配置脚本）"
	case errors.Is(err, ptpconfig.ErrSyntax):
		return http.StatusUnprocessableEntity, "script_parse_error", err.Error()
	case errors.Is(err, workbook.ErrEmptyWorkbook):
		return http.StatusUnprocessableEntity, "empty_workbook", "工作簿不包含任何数据行"
	case errors.Is(err, workbook.ErrWorkbookTooLarge):
		return http.StatusUnprocessableEntity, "workbook_too_large",
			"工作簿超出大小上限（单表行数或全簿单元格数量过多），请拆分后重试"
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not_found", "请求的对象不存在"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "conflict", "操作与当前状态冲突"
	}
	return http.StatusInternalServerError, "internal", "服务器内部错误，请查看服务端日志"
}

// respondErrorFor logs err (debug stack only for internal errors) and writes
// the mapped error envelope. conflictCode optionally overrides the 409 code
// (the rollback route uses "batch_in_use").
func (s *Server) respondErrorFor(w http.ResponseWriter, r *http.Request, err error, conflictCode string) {
	status, code, message := mapServiceError(err)
	if status == http.StatusConflict && conflictCode != "" {
		code = conflictCode
	}
	if status >= 500 && s.log != nil {
		s.log.Error("request failed",
			"method", r.Method,
			"path", r.URL.Path,
			"error", err.Error(),
			"stack", string(debug.Stack()),
		)
	}
	respondError(w, status, code, message)
}

// parsePagination reads the pagination query parameters. Two equivalent
// interfaces are accepted (issue #7):
//
//   - page (1-based) + pageSize (default 20, clamped to MaxPageSize 100)
//   - limit (1..100, clamped like pageSize) + offset (0-based row offset;
//     the derived page is offset/pageSize + 1)
//
// Non-numeric, negative or zero page/pageSize/limit and negative offset
// answer a 400 bad_request envelope with the accepted range (previously
// limit/offset were unknown names and silently ignored — a negative value
// was indistinguishable from "applied"). Mixing the two interfaces
// (page+offset or pageSize+limit) is also a 400: an explicit conflict beats
// a silent precedence rule.
func parsePagination(w http.ResponseWriter, r *http.Request) (page, pageSize int, ok bool) {
	q := r.URL.Query()
	hasPage := q.Get("page") != ""
	hasPageSize := q.Get("pageSize") != ""
	hasLimit := q.Get("limit") != ""
	hasOffset := q.Get("offset") != ""
	if hasPage && hasOffset {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "page 与 offset 不能同时使用（两种分页接口）")
		return 0, 0, false
	}
	if hasPageSize && hasLimit {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "pageSize 与 limit 不能同时使用（两种分页接口）")
		return 0, 0, false
	}

	page, pageSize = 1, query.DefaultPageSize
	if hasPage {
		raw := q.Get("page")
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "page 参数 %q 不是正整数（1 起）", raw)
			return 0, 0, false
		}
		page = v
	}
	if hasPageSize {
		raw := q.Get("pageSize")
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "pageSize 参数 %q 不是正整数（1–100）", raw)
			return 0, 0, false
		}
		if v > query.MaxPageSize {
			v = query.MaxPageSize // clamp, never fail (§9: max 100)
		}
		pageSize = v
	}
	if hasLimit {
		raw := q.Get("limit")
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "limit 参数 %q 不是正整数（1–100，超出部分钳制为 100）", raw)
			return 0, 0, false
		}
		if v > query.MaxPageSize {
			v = query.MaxPageSize
		}
		pageSize = v
	}
	if hasOffset {
		raw := q.Get("offset")
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "offset 参数 %q 不是非负整数（0 起）", raw)
			return 0, 0, false
		}
		page = v/pageSize + 1
	}
	return page, pageSize, true
}
