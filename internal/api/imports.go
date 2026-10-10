package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/repo"
)

// handleHealth implements GET /api/health.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, struct {
		Status      string `json:"status"`
		Version     string `json:"version"`
		RuleVersion string `json:"ruleVersion"`
	}{
		Status:      "ok",
		Version:     domain.AppVersion,
		RuleVersion: domain.RuleVersion,
	})
}

// handleImportUpload implements POST /api/imports: multipart field "file",
// .xlsx only, ≤10MB. The upload is stored at
// dataDir/uploads/<sha256>_<name>.xlsx (content-addressed, overwriting an
// identical prior upload), then analyzed via the importer with the user's
// original file name as the batch's display name (202 + Analysis JSON).
// An unconfigured (empty) dataDir fails fast with 500: the analyze step
// reads the workbook back from the stored path, so a relative "uploads/…"
// path could never work (AUD1-10 residue, now guarded at the entry).
func (s *Server) handleImportUpload(w http.ResponseWriter, r *http.Request) {
	if s.dataDir == "" {
		s.log.Error("import upload rejected: dataDir is not configured",
			"method", r.Method, "path", r.URL.Path)
		respondError(w, http.StatusInternalServerError, "internal",
			"服务器未配置上传存储目录（dataDir），无法处理导入上传")
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
	if ext := strings.ToLower(filepath.Ext(name)); ext != ".xlsx" {
		respondError(w, http.StatusUnprocessableEntity, "not_xlsx",
			fmt.Sprintf("仅支持 .xlsx 工作簿，收到 %q", filepath.Ext(name)))
		return
	}

	data, err := io.ReadAll(file)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			respondError(w, http.StatusRequestEntityTooLarge, "too_large", "上传文件超过 10MB 上限")
			return
		}
		respondError(w, http.StatusBadRequest, "bad_request", "读取上传文件失败："+err.Error())
		return
	}
	if len(data) == 0 {
		respondError(w, http.StatusUnprocessableEntity, "empty_workbook", "上传文件为空")
		return
	}

	// Content-addressed storage: <sha256>_<original name>.xlsx.
	sum := sha256.Sum256(data)
	stored := filepath.Join(s.dataDir, "uploads", hex.EncodeToString(sum[:])+"_"+name)
	if err := os.MkdirAll(filepath.Join(s.dataDir, "uploads"), 0o755); err != nil {
		s.log.Error("create uploads dir failed", "error", err.Error())
		respondError(w, http.StatusInternalServerError, "internal", "服务器内部错误，无法保存上传文件")
		return
	}
	if err := os.WriteFile(stored, data, 0o644); err != nil {
		s.log.Error("store upload failed", "error", err.Error(), "path", stored)
		respondError(w, http.StatusInternalServerError, "internal", "服务器内部错误，无法保存上传文件")
		return
	}

	a, err := s.importer.AnalyzeNamed(r.Context(), stored, name)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusAccepted, a)
}

// mappingSummary is the row-accounting block of the mapping response (§9).
type mappingSummary struct {
	RowsTotal    int `json:"rowsTotal"`
	RowsImported int `json:"rowsImported"`
	RowsSkipped  int `json:"rowsSkipped"`
	RowsFailed   int `json:"rowsFailed"`
}

// mappingResponse is the 202 body of POST /api/imports/{id}/mapping.
type mappingResponse struct {
	BatchID string         `json:"batchId"`
	Status  string         `json:"status"`
	Summary mappingSummary `json:"summary"`
	Issues  map[string]int `json:"issues"`
}

// handleImportMapping implements POST /api/imports/{id}/mapping: body = the
// confirmed mapping model (§8). The importer executes it (all writes of all
// import sheets in one transaction); on success the set-level validation
// engine recomputes E005/E006/I001 OUTSIDE the import transaction
// (§8 "set-phase validation runs automatically after successful import"),
// and the response counts the row-phase issues of THIS batch plus the
// set-level issues this validation run produced.
func (s *Server) handleImportMapping(w http.ResponseWriter, r *http.Request) {
	batchID := r.PathValue("id")

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", "读取请求体失败："+err.Error())
		return
	}
	m, err := mapping.Parse(body)
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", "映射 JSON 解析失败："+err.Error())
		return
	}

	sum, err := s.importer.Execute(r.Context(), batchID, m)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}

	// Auto validation after a successful import (outside the import tx).
	vr, err := s.validation.Run(r.Context())
	if err != nil {
		s.log.Error("post-import validation failed", "error", err.Error(), "batch", batchID)
		respondError(w, http.StatusInternalServerError, "internal", "导入成功但校验引擎运行失败，请查看服务端日志")
		return
	}

	issues := map[string]int{
		domain.SeverityError.String():   0,
		domain.SeverityWarning.String(): 0,
		domain.SeverityInfo.String():    0,
	}
	for sev, n := range sum.IssuesBySeverity {
		issues[sev.String()] += n
	}
	for rule, n := range vr.InsertedByRule {
		if sev, known := domain.SeverityForCode(rule); known {
			issues[sev.String()] += n
		}
	}

	respondJSON(w, http.StatusAccepted, mappingResponse{
		BatchID: sum.BatchID,
		Status:  sum.Status.String(),
		Summary: mappingSummary{
			RowsTotal:    sum.RowsTotal,
			RowsImported: sum.RowsImported,
			RowsSkipped:  sum.RowsSkipped,
			RowsFailed:   sum.RowsFailed,
		},
		Issues: issues,
	})
}

// validateResponse is the POST /api/validate body (§9).
type validateResponse struct {
	Issues map[string]int `json:"issues"`
	Score  int            `json:"score"`
}

// handleValidate implements POST /api/validate: recomputes the set-level
// issues (one transaction: delete + insert), then answers the issue counts
// and the quality score 100 − 5·#error − 1·#warning (floor 0).
//
// With the optional body {"batchId": …} the counts and score cover the
// batch's row-phase issues plus ALL set-level issues (the same inclusion
// rule as the batch-scope report); without a body they cover the whole
// database. A body referencing an unknown batch answers 404.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		respondError(w, http.StatusBadRequest, "bad_request", "读取请求体失败："+err.Error())
		return
	}
	batchID := ""
	if len(strings.TrimSpace(string(body))) > 0 {
		var req struct {
			BatchID string `json:"batchId"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			respondError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON 对象："+err.Error())
			return
		}
		batchID = req.BatchID
	}

	if _, err := s.validation.Run(r.Context()); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}

	issues := map[string]int{
		domain.SeverityError.String():   0,
		domain.SeverityWarning.String(): 0,
		domain.SeverityInfo.String():    0,
	}
	if batchID == "" {
		counts, err := s.db.Issues.CountBySeverity(r.Context(), nil)
		if err != nil {
			s.respondErrorFor(w, r, err, "")
			return
		}
		for sev, n := range counts {
			issues[sev.String()] += n
		}
	} else {
		if _, err := s.db.Batches.Get(r.Context(), batchID); err != nil {
			s.respondErrorFor(w, r, err, "")
			return
		}
		rowPhase, err := s.db.Issues.CountBySeverity(r.Context(), &batchID)
		if err != nil {
			s.respondErrorFor(w, r, err, "")
			return
		}
		setLevel, err := s.setLevelIssueCounts(r.Context())
		if err != nil {
			s.respondErrorFor(w, r, err, "")
			return
		}
		for sev, n := range rowPhase {
			issues[sev.String()] += n
		}
		for sev, n := range setLevel {
			issues[sev.String()] += n
		}
	}

	score := 100 - 5*issues[domain.SeverityError.String()] - issues[domain.SeverityWarning.String()]
	if score < 0 {
		score = 0
	}
	respondJSON(w, http.StatusOK, validateResponse{Issues: issues, Score: score})
}

// setLevelIssueCounts counts the set-level issues (batch_id NULL) per
// severity — the database-global half of a batch-scope count.
func (s *Server) setLevelIssueCounts(ctx context.Context) (map[domain.Severity]int, error) {
	all, _, err := s.db.Issues.List(ctx, repo.IssueFilter{})
	if err != nil {
		return nil, err
	}
	counts := map[domain.Severity]int{}
	for _, iss := range all {
		if iss.BatchID == nil {
			counts[iss.Severity]++
		}
	}
	return counts, nil
}
