// Package api implements the HTTP API of NetOps Lab (PROJECT_BRIEF §9) on
// the Go 1.27 net/http ServeMux with method patterns. Every handler:
//
//   - takes a context from the request,
//   - answers JSON (UTF-8) via respondJSON,
//   - reports failures through the uniform error envelope
//     {"error":{"code","message"}} with proper status codes
//     (400 bad_request/validation, 404 not_found, 409 batch_in_use/conflict,
//     413 too_large, 422 not_xlsx/empty_workbook, 500 internal).
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
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/query"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/report"
	"github.com/mmqz/netops-lab/internal/validation"
	"github.com/mmqz/netops-lab/internal/webui"
	"github.com/mmqz/netops-lab/internal/workbook"
)

// maxUploadBytes is the request body limit of the import upload route
// (PROJECT_BRIEF §9: ≤10MB).
const maxUploadBytes = 10 << 20

// Server is the HTTP API server. It owns its service instances (importer,
// validation engine, query service, report generator), all bound to the
// shared *repo.DB.
type Server struct {
	log        *slog.Logger
	db         *repo.DB
	dataDir    string
	importer   *importer.Service
	validation *validation.Engine
	query      *query.Service
	report     *report.Generator
}

// New returns a Server bound to db; uploads are stored under
// <dataDir>/uploads (created lazily on the first upload). dataDir may be
// empty when uploads are not served (no directory is created then).
func New(log *slog.Logger, db *repo.DB, dataDir string) *Server {
	return &Server{
		log:        log,
		db:         db,
		dataDir:    dataDir,
		importer:   importer.New(db),
		validation: validation.New(db),
		query:      query.New(db),
		report:     report.New(db),
	}
}

// Routes builds the full HTTP handler: the §9 API routes with logging and
// recovery middleware, the JSON 404 envelope for unknown /api/ paths, and
// the embedded SPA for everything else. The returned handler is what
// cmd/netopslab serve and the httptest-based tests mount.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)

	mux.HandleFunc("POST /api/imports", s.handleImportUpload)
	mux.HandleFunc("POST /api/imports/{id}/mapping", s.handleImportMapping)

	mux.HandleFunc("GET /api/batches", s.handleBatchesList)
	mux.HandleFunc("GET /api/batches/{id}", s.handleBatchDetail)
	mux.HandleFunc("GET /api/batches/{id}/rows", s.handleBatchRows)
	mux.HandleFunc("DELETE /api/batches/{id}", s.handleBatchDelete)

	mux.HandleFunc("GET /api/resources", s.handleResourcesList)
	mux.HandleFunc("GET /api/resources/{id}", s.handleResourceDetail)
	mux.HandleFunc("GET /api/resources/{id}/relations", s.handleResourceRelations)

	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/issues", s.handleIssues)
	mux.HandleFunc("POST /api/validate", s.handleValidate)
	mux.HandleFunc("GET /api/report", s.handleReport)
	mux.HandleFunc("GET /api/topology/check", s.handleTopologyCheck)

	// Unknown /api/ paths: JSON 404 envelope (never the SPA fallback).
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		respondError(w, http.StatusNotFound, "not_found",
			fmt.Sprintf("未知的 API 路径：%s %s", r.Method, r.URL.Path))
	})

	// Everything else: the embedded single-page application.
	mux.Handle("/", webui.Handler())

	return s.middleware(mux)
}

// middleware wraps the mux with panic recovery and request logging.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
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
// sheet/field detail the UI points at); workbook sentinels → 422;
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
	case errors.Is(err, workbook.ErrEmptyWorkbook):
		return http.StatusUnprocessableEntity, "empty_workbook", "工作簿不包含任何数据行"
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

// parsePagination reads the page (1-based) and pageSize (default 20, max
// 100) query parameters. Non-numeric or non-positive values answer a 400
// bad_request envelope and return false.
func parsePagination(w http.ResponseWriter, r *http.Request) (page, pageSize int, ok bool) {
	page, pageSize = 1, query.DefaultPageSize
	if raw := r.URL.Query().Get("page"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "page 参数 %q 不是正整数", raw)
			return 0, 0, false
		}
		page = v
	}
	if raw := r.URL.Query().Get("pageSize"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "pageSize 参数 %q 不是正整数", raw)
			return 0, 0, false
		}
		if v > query.MaxPageSize {
			v = query.MaxPageSize // clamp, never fail (§9: max 100)
		}
		pageSize = v
	}
	return page, pageSize, true
}
