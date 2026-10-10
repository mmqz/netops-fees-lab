package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/importer"
	"github.com/mmqz/netops-lab/internal/repo"
)

// batchItem is one entry of GET /api/batches (newest first).
type batchItem struct {
	ID           string     `json:"id"`
	WorkbookName string     `json:"workbookName"`
	SHA256       string     `json:"sha256"`
	Status       string     `json:"status"`
	RowsTotal    int        `json:"rowsTotal"`
	RowsImported int        `json:"rowsImported"`
	RowsSkipped  int        `json:"rowsSkipped"`
	RowsFailed   int        `json:"rowsFailed"`
	Error        string     `json:"error"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt"`
}

func newBatchItem(b domain.ImportBatch) batchItem {
	return batchItem{
		ID:           b.ID,
		WorkbookName: b.WorkbookName,
		SHA256:       b.WorkbookSHA256,
		Status:       b.Status.String(),
		RowsTotal:    b.RowsTotal,
		RowsImported: b.RowsImported,
		RowsSkipped:  b.RowsSkipped,
		RowsFailed:   b.RowsFailed,
		Error:        b.Error,
		StartedAt:    b.StartedAt,
		FinishedAt:   b.FinishedAt,
	}
}

// batchesPage is the list envelope of GET /api/batches.
type batchesPage struct {
	Items    []batchItem `json:"items"`
	Total    int         `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
}

// handleBatchesList implements GET /api/batches (page/pageSize supported;
// items ordered newest first, §9 "desc").
func (s *Server) handleBatchesList(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	batches, total, err := s.db.Batches.List(r.Context(), pageSize, (page-1)*pageSize)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	items := make([]batchItem, 0, len(batches))
	for _, b := range batches {
		items = append(items, newBatchItem(b))
	}
	respondJSON(w, http.StatusOK, batchesPage{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// sheetSummary is the per-sheet row accounting of a batch detail.
type sheetSummary struct {
	Sheet        string `json:"sheet"`
	RowsTotal    int    `json:"rowsTotal"`
	RowsImported int    `json:"rowsImported"`
	RowsSkipped  int    `json:"rowsSkipped"`
	RowsFailed   int    `json:"rowsFailed"`
}

// batchDetail is the GET /api/batches/{id} body: the batch plus its stored
// mapping (echoed, §8) and the per-sheet summary.
type batchDetail struct {
	batchItem
	RuleVersion string          `json:"ruleVersion"`
	Mapping     json.RawMessage `json:"mapping"`
	Sheets      []sheetSummary  `json:"sheets"`
}

// handleBatchDetail implements GET /api/batches/{id}.
func (s *Server) handleBatchDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b, err := s.db.Batches.Get(r.Context(), id)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}

	// Per-sheet summary from the row results (ordered by sheet name).
	results, _, err := s.db.RowResults.List(r.Context(), repo.RowResultFilter{BatchID: id})
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	bySheet := map[string]*sheetSummary{}
	names := make([]string, 0)
	for _, rr := range results {
		ss, ok := bySheet[rr.Sheet]
		if !ok {
			ss = &sheetSummary{Sheet: rr.Sheet}
			bySheet[rr.Sheet] = ss
			names = append(names, rr.Sheet)
		}
		ss.RowsTotal++
		switch rr.Status {
		case domain.RowStatusImported:
			ss.RowsImported++
		case domain.RowStatusSkipped:
			ss.RowsSkipped++
		case domain.RowStatusFailed:
			ss.RowsFailed++
		}
	}
	sheets := make([]sheetSummary, 0, len(names))
	sort.Strings(names)
	for _, name := range names {
		sheets = append(sheets, *bySheet[name])
	}

	mappingJSON := json.RawMessage(b.Mapping)
	if len(mappingJSON) == 0 {
		mappingJSON = json.RawMessage("{}")
	}
	respondJSON(w, http.StatusOK, batchDetail{
		batchItem:   newBatchItem(b),
		RuleVersion: b.RuleVersion,
		Mapping:     mappingJSON,
		Sheets:      sheets,
	})
}

// rowResultItem is one row of GET /api/batches/{id}/rows.
type rowResultItem struct {
	ID        int64  `json:"id"`
	BatchID   string `json:"batchId"`
	Sheet     string `json:"sheet"`
	RowNumber int    `json:"rowNumber"`
	Status    string `json:"status"`
	EntityID  string `json:"entityId"`
	RuleCode  string `json:"ruleCode"`
	Message   string `json:"message"`
}

// rowsPage is the list envelope of GET /api/batches/{id}/rows.
type rowsPage struct {
	Items    []rowResultItem `json:"items"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"pageSize"`
}

// handleBatchRows implements GET /api/batches/{id}/rows?status=&page=&pageSize=
// (status: imported|skipped|failed; ordered by sheet, row_number).
func (s *Server) handleBatchRows(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.db.Batches.Get(r.Context(), id); err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	filter := repo.RowResultFilter{BatchID: id}
	if raw := r.URL.Query().Get("status"); raw != "" {
		st, err := domain.ParseRowStatus(raw)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request",
				"status 参数 %q 不是 imported/skipped/failed", raw)
			return
		}
		filter.Status = st
	}
	page, pageSize, ok := parsePagination(w, r)
	if !ok {
		return
	}
	filter.Limit = pageSize
	filter.Offset = (page - 1) * pageSize

	results, total, err := s.db.RowResults.List(r.Context(), filter)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	items := make([]rowResultItem, 0, len(results))
	for _, rr := range results {
		items = append(items, rowResultItem{
			ID:        rr.ID,
			BatchID:   rr.BatchID,
			Sheet:     rr.Sheet,
			RowNumber: rr.RowNumber,
			Status:    rr.Status.String(),
			EntityID:  rr.EntityID,
			RuleCode:  rr.RuleCode,
			Message:   rr.Message,
		})
	}
	respondJSON(w, http.StatusOK, rowsPage{Items: items, Total: total, Page: page, PageSize: pageSize})
}

// rollbackCounts reports what a batch rollback deleted.
type rollbackCounts struct {
	Resources  int `json:"resources"`
	Relations  int `json:"relations"`
	SourceRefs int `json:"sourceRefs"`
	RowResults int `json:"rowResults"`
	Issues     int `json:"issues"`
	// AttributeChanges counts the batch's PENDING attribute change proposals
	// removed by the rollback (W4-D). Applied/dismissed proposals are NOT
	// deleted — the human decision survives its import batch (asymmetric
	// lifecycle, documented in docs/import-and-validation.md).
	AttributeChanges int `json:"attributeChanges"`
}

// rollbackResponse is the DELETE /api/batches/{id} success body.
type rollbackResponse struct {
	BatchID string         `json:"batchId"`
	Status  string         `json:"status"`
	Deleted rollbackCounts `json:"deleted"`
}

// handleBatchDelete implements DELETE /api/batches/{id}: rollback of a
// completed batch. domain.ErrConflict becomes 409 batch_in_use. After a
// successful rollback the set-level validation recomputes so E005/E006/I001
// reflect the post-rollback state (same automatic rule as after an import).
//
// The deleted-counts are COUNT aggregates (repo *CountByBatch), not full
// List + len(): a big workbook's DELETE response must not pay for loading
// every row result and issue (R2-P3-2). The response JSON shape is
// unchanged.
func (s *Server) handleBatchDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx := r.Context()

	resources, err := s.db.Resources.CountByBatch(ctx, id)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	relations, err := s.db.Relations.CountByBatch(ctx, id)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	sourceRefs, err := s.db.SourceRefs.CountByBatch(ctx, id)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	rowResults, err := s.db.RowResults.CountByBatch(ctx, id)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	batchIssues, err := s.db.Issues.CountByBatch(ctx, id)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	pendingChanges, err := s.db.AttributeChanges.CountPendingByBatch(ctx, id)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}

	if err := s.importer.Rollback(ctx, id); err != nil {
		// The in-use refusal (importer.ErrBatchInUse) is the §9 409
		// batch_in_use case; a non-completed batch answers a plain conflict.
		conflictCode := ""
		if errors.Is(err, importer.ErrBatchInUse) {
			conflictCode = "batch_in_use"
		}
		s.respondErrorFor(w, r, err, conflictCode)
		return
	}

	if _, err := s.validation.Run(ctx); err != nil {
		s.log.Error("post-rollback validation failed", "error", err.Error(), "batch", id)
		respondError(w, http.StatusInternalServerError, "internal",
			fmt.Sprintf("批次 %s 已回滚，但校验引擎运行失败，请查看服务端日志", id))
		return
	}

	respondJSON(w, http.StatusOK, rollbackResponse{
		BatchID: id,
		Status:  domain.BatchStatusRolledBack.String(),
		Deleted: rollbackCounts{
			Resources:  resources,
			Relations:  relations,
			SourceRefs: sourceRefs,
			RowResults: rowResults,
			Issues:     batchIssues,
			// The count matches DeletePendingByBatch's scope: pending only.
			AttributeChanges: pendingChanges,
		},
	})
}
