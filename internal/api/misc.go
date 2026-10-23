package api

import (
	"net/http"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/report"
)

// handleStats implements GET /api/stats.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.query.Stats(r.Context())
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, stats)
}

// issuesPage is the list envelope of GET /api/issues; the items are
// report.Issue values, so their source provenance (workbook/sheet/rowNumber
// or applicationGenerated) is resolved exactly as in GET /api/report.
type issuesPage struct {
	Items    []report.Issue `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
}

// handleIssues implements GET /api/issues?batchId=&severity=&ruleCode=&page=&pageSize=
// (ordered by rule_code then id — the repository pagination order).
func (s *Server) handleIssues(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	filter := repo.IssueFilter{
		RuleCode: r.URL.Query().Get("ruleCode"),
		Limit:    pageSize,
		Offset:   (page - 1) * pageSize,
	}
	if raw := r.URL.Query().Get("batchId"); raw != "" {
		filter.BatchID = &raw
	}
	if raw := r.URL.Query().Get("severity"); raw != "" {
		sev, err := domain.ParseSeverity(raw)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "severity 参数 %q 不是 error/warning/info", raw)
			return
		}
		filter.Severity = sev
	}

	issues, total, err := s.db.Issues.List(r.Context(), filter)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	resolved, err := report.ResolveIssues(r.Context(), s.db, issues)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	if resolved == nil {
		resolved = []report.Issue{}
	}
	respondJSON(w, http.StatusOK, issuesPage{Items: resolved, Total: total, Page: page, PageSize: pageSize})
}

// handleReport implements GET /api/report?batchId=&format=html|json
// (default html). HTML answers Content-Type text/html; charset=utf-8 with
// the standalone report document; JSON answers the report struct.
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "html"
	}
	if format != "html" && format != "json" {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "format 参数 %q 必须是 html 或 json", format)
		return
	}
	batchID := r.URL.Query().Get("batchId")

	if format == "html" {
		html, err := s.report.RenderHTML(r.Context(), batchID)
		if err != nil {
			s.respondErrorFor(w, r, err, "")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(html))
		return
	}

	// Build the report before writing anything so errors (e.g. an unknown
	// batch id) still answer the JSON error envelope, then respond with the
	// API's uniform JSON writer (Content-Type application/json; charset=utf-8).
	rep, err := s.report.JSON(r.Context(), batchID)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, rep)
}
