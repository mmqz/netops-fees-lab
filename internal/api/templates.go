package api

// templates.go — the W24 unattended import HTTP surface (§12-B7 方案 A,
// issue #28): the template capture of a completed batch, the template
// listing/deletion, and the auto=1 flag of the import upload (implemented
// in imports.go next to the upload handler it extends). The handlers are
// thin projections over the SAME importer service the CLI uses (services
// composition root) and the repo layer — no second template path exists.
// The MCP tool surface stays frozen at the five read-only tools
// (ADR-0011 D5): templates are import automation, an HTTP/CLI human+ops
// channel, never an AI-controlled one.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/importer"
)

// templateItem is one mapping template of the list/capture responses. The
// stored single-sheet Mapping document is echoed as a JSON OBJECT
// (json.RawMessage) — the exact §8 shape POST /api/imports/{id}/mapping
// accepts, so a captured template can be replayed by hand.
type templateItem struct {
	TemplateID        string          `json:"templateId"`
	SourceName        string          `json:"sourceName"`
	HeaderFingerprint string          `json:"headerFingerprint"`
	ResourceType      string          `json:"resourceType"`
	Mapping           json.RawMessage `json:"mapping"`
	SheetName         string          `json:"sheetName"`
	ConfirmedAt       time.Time       `json:"confirmedAt"`
	ConfirmedBatch    string          `json:"confirmedBatch"`
}

func newTemplateItem(t domain.MappingTemplate) templateItem {
	mappingJSON := json.RawMessage(t.MappingJSON)
	if len(mappingJSON) == 0 || !json.Valid(mappingJSON) {
		// Defensive only: capture stores marshaled documents; a hand-edited
		// row must not turn the whole listing into invalid JSON — echo the
		// raw text as a JSON string instead (honest, visible, parseable).
		b, _ := json.Marshal(t.MappingJSON)
		mappingJSON = b
	}
	return templateItem{
		TemplateID:        t.TemplateID,
		SourceName:        t.SourceName,
		HeaderFingerprint: t.HeaderFingerprint,
		ResourceType:      string(t.ResourceType),
		Mapping:           mappingJSON,
		SheetName:         t.SheetName,
		ConfirmedAt:       t.ConfirmedAt,
		ConfirmedBatch:    t.ConfirmedBatch,
	}
}

// templatesPage is the list envelope of GET /api/mapping-templates.
type templatesPage struct {
	Items    []templateItem `json:"items"`
	Total    int            `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"pageSize"`
}

// handleMappingTemplatesList implements GET /api/mapping-templates
// ?page=&pageSize= (the shared pagination contract): items ordered
// newest-confirmed first, deterministic. The stored mapping of each item is
// the single-sheet §8 document.
func (s *Server) handleMappingTemplatesList(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	templates, total, err := s.db.Templates.ListTemplates(r.Context(), pageSize, (page-1)*pageSize)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	items := make([]templateItem, 0, len(templates))
	for _, t := range templates {
		items = append(items, newTemplateItem(t))
	}
	respondJSON(w, http.StatusOK, templatesPage{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// handleMappingTemplateDelete implements DELETE /api/mapping-templates/{id}:
// removes one template (the unattended channel's opt-out — a deleted
// workbook shape falls back to the human confirmation flow). 404 unknown
// id. Deleting a template NEVER touches the batch that produced it (the
// asymmetry is the other way round: a batch rollback does not delete the
// template either — a template is a confirmed artifact, not batch data).
func (s *Server) handleMappingTemplateDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.db.Templates.DeleteTemplate(r.Context(), id); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, struct {
		Deleted bool   `json:"deleted"`
		ID      string `json:"id"`
	}{Deleted: true, ID: id})
}

// templateCaptureRequest is the OPTIONAL body of POST /api/imports/{id}/
// template: {"sourceName": "…"} overrides the template's human label (the
// default is the batch's workbook name). An empty body is legal (the W23
// no-argument-action precedent).
type templateCaptureRequest struct {
	SourceName string `json:"sourceName"`
}

// handleImportTemplateCapture implements POST /api/imports/{id}/template —
// the one-click save of §12-B7: copies the CONFIRMED mapping of the
// COMPLETED batch into per-sheet mapping templates (fingerprint recomputed
// from the batch's recorded workbook — the analysis JSON is not persisted,
// so the workbook is re-read). 201 + the stored templates; 404 unknown
// batch; 409 batch not completed (or its mapping absent / a mapping sheet
// missing from the recorded workbook).
func (s *Server) handleImportTemplateCapture(w http.ResponseWriter, r *http.Request) {
	batchID := r.PathValue("id")

	// Optional body (empty is legal — the W23 no-argument-action precedent).
	sourceName := ""
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		respondErrorf(w, http.StatusBadRequest, "bad_request", "读取请求体失败：%v", err)
		return
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		var req templateCaptureRequest
		if err := json.Unmarshal(body, &req); err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "请求体不是有效的 JSON 对象：%v", err)
			return
		}
		sourceName = req.SourceName
	}

	captured, err := s.importer.CaptureTemplate(r.Context(), batchID, sourceName)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	items := make([]templateItem, 0, len(captured))
	for _, t := range captured {
		items = append(items, newTemplateItem(t))
	}
	respondJSON(w, http.StatusCreated, struct {
		Items []templateItem `json:"items"`
		Total int            `json:"total"`
	}{Items: items, Total: len(items)})
}

// isAutoRequest reports whether the import upload carries the unattended
// flag: the query parameter auto=1/true OR a multipart form field auto
// (either interface, documented in api.md — the multipart style of the
// upload route makes a form field the natural in-body variant).
func isAutoRequest(r *http.Request, parsedForm bool) bool {
	probe := func(v string) bool {
		switch v {
		case "1", "true", "TRUE", "True":
			return true
		}
		return false
	}
	if probe(r.URL.Query().Get("auto")) {
		return true
	}
	if parsedForm {
		if v := r.FormValue("auto"); probe(v) {
			return true
		}
	}
	return false
}

// writeImportSummaryEnvelope runs the post-import validation and writes the
// 202 summary envelope of a completed import — the SHARED body of POST
// /api/imports/{id}/mapping and the auto=1 executed path (identical
// contract: row counts + row-phase issues of this batch + the set-level
// issues the validation recompute produced).
func (s *Server) writeImportSummaryEnvelope(w http.ResponseWriter, r *http.Request, sum importer.Summary) {
	vr, err := s.validation.Run(r.Context())
	if err != nil {
		s.log.Error("post-import validation failed", "error", err.Error(), "batch", sum.BatchID)
		respondError(w, http.StatusInternalServerError, "internal",
			"导入成功但校验引擎运行失败，请查看服务端日志")
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
