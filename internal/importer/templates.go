package importer

// templates.go — the W24 unattended import channel (§12-B7 方案 A, issue
// #28): the header-fingerprint template match on the analyze path, the
// one-shot auto import (analyze → all import sheets matched → execute with
// the stored mappings, skipping awaiting_mapping), and the one-click
// template capture of a completed batch's confirmed mapping.
//
// Design anchors:
//
//   - the fingerprint is mapping.Fingerprint (pure): sheet name + trimmed/
//     fullwidth-folded/lower-cased headers IN SHEET ORDER. Any structural
//     change (rename/add/remove/permute column) is a different fingerprint
//     → no template match → the auto path REFUSES and the batch stays
//     awaiting_mapping for the ordinary human confirmation (表头变更感知 —
//     never a silent mismatched import).
//
//   - the template's stored mapping is re-validated by the ordinary
//     Execute pipeline (mapping.Validate against the ACTUAL headers) before
//     any write — the template is a convenience, never a trust boundary.
//
//   - a template SURVIVES the rollback of the batch that produced it (it is
//     a confirmed human artifact, not batch data — the W005/W006 decision
//     lifecycle precedent).

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/mapping"
	"github.com/mmqz/netops-fees-lab/internal/workbook"
)

// TemplateMatch describes the stored mapping template that matched one
// analyzed sheet's header fingerprint.
type TemplateMatch struct {
	// TemplateID is the matched mapping_templates row id.
	TemplateID string `json:"templateId"`
	// ResourceType is the template's target type (the validation anchor).
	ResourceType domain.ResourceType `json:"resourceType"`
	// ConfirmedAt is the template's (re-)confirmation time (RFC3339).
	ConfirmedAt string `json:"confirmedAt"`
}

// AutoWarning is the refusal report of an unattended (auto=1) import: the
// import sheets whose header fingerprints found no stored template. The
// batch is NOT executed and NOT failed — it stays awaiting_mapping, and the
// human confirmation flow (the wizard / POST /api/imports/{id}/mapping) is
// the fallback. CLI consumers surface this as the no_matching_template
// error envelope (exit 1); HTTP answers 202 with the analysis + this block.
type AutoWarning struct {
	// Code is the refusal reason code; "no_matching_template" when import
	// sheets missed their templates, "no_import_sheets" when the workbook
	// has no importable sheet at all (nothing an unattended run could do).
	Code string `json:"code"`
	// UnmatchedSheets lists the import sheets that found no template, in
	// sheet order (empty for no_import_sheets).
	UnmatchedSheets []string `json:"unmatchedSheets"`
	// Message is the human-readable reason (names the sheets — 表头变更感知).
	Message string `json:"message"`
}

// Refusal code of AutoWarning / NoMatchingTemplateError.
const (
	// AutoRefusalNoTemplate: at least one import sheet's fingerprint has no
	// stored template.
	AutoRefusalNoTemplate = "no_matching_template"
	// AutoRefusalNoImportSheets: the workbook's suggested mapping contains
	// no import sheet — an unattended import would import nothing, which is
	// an anomalous daily workbook and must be surfaced, not "completed".
	AutoRefusalNoImportSheets = "no_import_sheets"
)

// NoMatchingTemplateError is the typed refusal of the unattended path: at
// least one import sheet found no template (or none was importable). The
// CLI maps it to the no_matching_template error code; the HTTP auto path
// does NOT answer 4xx — the refusal is the designed fallback to the human
// flow, so the analysis (with the AutoWarning block) is the 202 body.
type NoMatchingTemplateError struct {
	// Code mirrors AutoWarning.Code.
	Code string
	// UnmatchedSheets mirrors AutoWarning.UnmatchedSheets.
	UnmatchedSheets []string
}

// Error implements the error interface with the sheet list embedded.
func (e *NoMatchingTemplateError) Error() string {
	switch e.Code {
	case AutoRefusalNoImportSheets:
		return "importer: unattended import refused: workbook has no importable sheet (no_import_sheets)"
	default:
		return fmt.Sprintf("importer: unattended import refused: no mapping template matches the sheet fingerprint(s) %v (no_matching_template)",
			e.UnmatchedSheets)
	}
}

// attachTemplateMatches computes every sheet's header fingerprint and
// attaches the matching template (templateMatched) to the analysis sheets.
// Matching is fingerprint-only (the sheet name is part of the fingerprint);
// sheets with no template keep templateMatched = null. The suggested action
// is NOT part of the match — a skip-suggested sheet that matches a template
// still reports the match (the wizard banner is informational), but the
// auto path only consumes matches of import-suggested sheets.
func (s *Service) attachTemplateMatches(ctx context.Context, a *Analysis) error {
	for i := range a.Sheets {
		sa := &a.Sheets[i]
		fp := mapping.Fingerprint(sa.Sheet, headersOf(sa))
		tpl, err := s.db.Templates.FindByFingerprint(ctx, fp)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue // no template for this sheet shape — the ordinary flow
			}
			return fmt.Errorf("importer: template lookup of sheet %q: %w", sa.Sheet, err)
		}
		sa.TemplateMatched = &TemplateMatch{
			TemplateID:   tpl.TemplateID,
			ResourceType: tpl.ResourceType,
			ConfirmedAt:  tpl.ConfirmedAt.UTC().Format(time.RFC3339),
		}
	}
	return nil
}

// headersOf extracts the header row of one analyzed sheet (column order).
func headersOf(sa *SheetAnalysis) []string {
	headers := make([]string, len(sa.Columns))
	for i := range sa.Columns {
		headers[i] = sa.Columns[i].Header
	}
	return headers
}

// ImportAutoResult is the outcome of an unattended (auto=1) import attempt.
type ImportAutoResult struct {
	// Executed reports whether the import ran with the stored template
	// mappings (zero human interaction).
	Executed bool
	// Summary is the executed import's accounting (Executed only; the same
	// shape the mapping-confirmation endpoint returns).
	Summary Summary
	// Analysis is the full analysis of the uploaded workbook, with the
	// per-sheet templateMatched attached (and AutoWarning set on refusal).
	Analysis Analysis
	// UnmatchedSheets lists the import sheets that blocked the auto path
	// (refusal only; empty on success and on no_import_sheets).
	UnmatchedSheets []string
}

// ImportAuto runs the unattended import of the workbook at path: analyze
// (a batch is created), then — when EVERY import-suggested sheet of the
// workbook matches a stored template — execute the merged template mapping
// in one shot, skipping the awaiting_mapping stop. Refusal semantics (the
// batch STAYS awaiting_mapping — the human confirmation flow is the
// fallback, never an error state):
//
//   - an import sheet without a template match (renamed/added/removed/
//     permuted columns → different fingerprint) refuses with
//     no_matching_template, naming the sheets;
//   - a workbook with no import-suggested sheet at all refuses with
//     no_import_sheets (an unattended "import nothing" must not report
//     success);
//   - skip-suggested sheets do NOT block the auto path (they carry no
//     mapping to confirm — the same sheets the human flow skips); their
//     data is simply not imported, exactly as in the confirmed flow.
//
// The merged mapping re-validates through the ordinary Execute pipeline,
// so a template whose stored mapping no longer validates leaves the batch
// awaiting_mapping with the typed mapping error (honest failure, no
// partial writes).
func (s *Service) ImportAuto(ctx context.Context, path, workbookName string) (ImportAutoResult, error) {
	a, err := s.AnalyzeNamed(ctx, path, workbookName)
	if err != nil {
		return ImportAutoResult{}, err
	}
	if err := s.attachTemplateMatches(ctx, &a); err != nil {
		return ImportAutoResult{}, err
	}

	// Build the merged mapping: every import-suggested sheet must be
	// covered by its matched template; skip-suggested sheets pass through.
	merged := mapping.Mapping{}
	var unmatched []string
	haveImport := false
	for i := range a.SuggestedMapping.Sheets {
		sm := a.SuggestedMapping.Sheets[i]
		if sm.Action != mapping.ActionImport {
			merged.Sheets = append(merged.Sheets, sm)
			continue
		}
		haveImport = true
		var match *TemplateMatch
		for j := range a.Sheets {
			if a.Sheets[j].Sheet == sm.Sheet {
				match = a.Sheets[j].TemplateMatched
			}
		}
		if match == nil {
			unmatched = append(unmatched, sm.Sheet)
			continue
		}
		tpl, err := s.db.Templates.GetTemplate(ctx, match.TemplateID)
		if err != nil {
			return ImportAutoResult{Analysis: a}, fmt.Errorf("importer: load template %s: %w", match.TemplateID, err)
		}
		tplMapping, err := mapping.Parse([]byte(tpl.MappingJSON))
		if err != nil {
			return ImportAutoResult{Analysis: a}, fmt.Errorf("importer: template %s: %w", tpl.TemplateID, err)
		}
		if len(tplMapping.Sheets) != 1 || tplMapping.Sheets[0].Sheet != sm.Sheet {
			return ImportAutoResult{Analysis: a}, fmt.Errorf(
				"importer: template %s: stored mapping is not a single-sheet document for sheet %q", tpl.TemplateID, sm.Sheet)
		}
		// Cosmetic header drift (case/fullwidth) is folded by the fingerprint;
		// re-anchor the stored columnMapping onto the CURRENT sheet's exact
		// header strings so mapping.Validate sees the real headers.
		resolved, resErr := resolveTemplateHeaders(tplMapping.Sheets[0], a, sm.Sheet)
		if resErr != nil {
			return ImportAutoResult{Analysis: a}, resErr
		}
		merged.Sheets = append(merged.Sheets, resolved)
	}

	if !haveImport {
		a.AutoWarning = &AutoWarning{
			Code:    AutoRefusalNoImportSheets,
			Message: "工作簿没有任何可导入的工作表（建议映射全部为 skip），无人值守导入拒绝执行",
		}
		return ImportAutoResult{Analysis: a}, &NoMatchingTemplateError{Code: AutoRefusalNoImportSheets}
	}
	if len(unmatched) > 0 {
		a.AutoWarning = &AutoWarning{
			Code:            AutoRefusalNoTemplate,
			UnmatchedSheets: unmatched,
			Message: fmt.Sprintf(
				"以下导入工作表的表头指纹未命中任何映射模板（表头可能已变更，需人工重新确认）：%s",
				strings.Join(unmatched, "、")),
		}
		return ImportAutoResult{Analysis: a, UnmatchedSheets: unmatched}, &NoMatchingTemplateError{
			Code: AutoRefusalNoTemplate, UnmatchedSheets: unmatched,
		}
	}

	sum, err := s.Execute(ctx, a.BatchID, merged)
	if err != nil {
		// A mapping validation failure leaves the batch awaiting_mapping and
		// carries the typed *mapping.FieldError up (the same contract as the
		// human path). Other failures mark the batch failed (Execute's own
		// semantics).
		return ImportAutoResult{Analysis: a}, err
	}
	return ImportAutoResult{Executed: true, Summary: sum, Analysis: a}, nil
}

// resolveTemplateHeaders re-anchors a template's stored columnMapping (field
// → confirmed-time header) onto the CURRENT sheet's exact header strings:
// for each stored header, the current sheet's headers are matched under the
// fingerprint's normalization (mapping.NormalizeHeader: trim + fullwidth
// fold + lower); a unique hit rewrites the mapping to the current string,
// zero hits keep the stored string (mapping.Validate will honestly reject
// it), and an ambiguous hit (two current headers normalize identically —
// the workbook itself is ambiguous) fails the auto attempt. The sheet's
// keyColumns and action pass through untouched.
func resolveTemplateHeaders(sm mapping.SheetMapping, a Analysis, sheet string) (mapping.SheetMapping, error) {
	resolved := sm
	var current []string
	for i := range a.Sheets {
		if a.Sheets[i].Sheet == sheet {
			current = headersOf(&a.Sheets[i])
		}
	}
	if current == nil {
		return resolved, nil // sheet absent from the analysis — Validate reports it
	}
	resolved.ColumnMapping = make(map[string]string, len(sm.ColumnMapping))
	for field, stored := range sm.ColumnMapping {
		hits := 0
		replacement := stored
		for _, cur := range current {
			if mapping.NormalizeHeader(cur) == mapping.NormalizeHeader(stored) {
				hits++
				replacement = cur
			}
		}
		if hits > 1 {
			return resolved, fmt.Errorf(
				"importer: sheet %q: header %q matches %d current columns ambiguously — workbook is ambiguous, refusing unattended import",
				sheet, stored, hits)
		}
		resolved.ColumnMapping[field] = replacement
	}
	return resolved, nil
}

// CaptureTemplate copies the CONFIRMED mapping of the COMPLETED batch
// batchID into per-sheet mapping templates (the one-click save of §12-B7):
// for every import sheet of the stored mapping, the workbook is re-read
// from the batch's recorded path, the sheet's header fingerprint is
// recomputed, and a template row is upserted under the identity
// (fingerprint, resource type) — re-confirmation REPLACES the stored
// mapping and stamps a fresh confirmed_at, never a second row.
//
// sourceName is the template's human label; empty falls back to the batch's
// workbook name. The returned templates are the stored rows (ids included;
// on an upsert of an existing identity the ORIGINAL template id survives).
//
// Precondition: batch completed (the mapping worth saving is the one that
// actually imported); other statuses answer domain.ErrConflict. A missing
// batch answers domain.ErrNotFound; a missing workbook path or an unreadable
// workbook answers a plain error (the capture honestly cannot run).
func (s *Service) CaptureTemplate(ctx context.Context, batchID, sourceName string) ([]domain.MappingTemplate, error) {
	batch, err := s.db.Batches.Get(ctx, batchID)
	if err != nil {
		return nil, err
	}
	if batch.Status != domain.BatchStatusCompleted {
		return nil, fmt.Errorf("importer: capture template of batch %s: status %s, want completed (只有已完成批次的确认映射才值得保存为模板): %w",
			batchID, batch.Status, domain.ErrConflict)
	}
	if strings.TrimSpace(batch.Mapping) == "" || batch.Mapping == "{}" {
		return nil, fmt.Errorf("importer: capture template of batch %s: batch has no confirmed mapping: %w", batchID, domain.ErrConflict)
	}
	m, err := mapping.Parse([]byte(batch.Mapping))
	if err != nil {
		return nil, fmt.Errorf("importer: parse stored mapping of batch %s: %w", batchID, err)
	}

	path, err := s.db.Batches.WorkbookPath(ctx, batchID)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("importer: batch %s has no recorded workbook path — cannot recompute the header fingerprint", batchID)
	}
	wb, err := workbook.Read(path)
	if err != nil {
		return nil, fmt.Errorf("importer: re-read workbook of batch %s: %w", batchID, err)
	}
	byName := wb.SheetsByName()

	if sourceName == "" {
		sourceName = batch.WorkbookName
	}
	now := s.clock()

	captured := make([]domain.MappingTemplate, 0, len(m.Sheets))
	// Deterministic order: the stored mapping's sheet order (the confirmation
	// order), so the returned list and the upsert sequence are stable.
	for i := range m.Sheets {
		sm := m.Sheets[i]
		if sm.Action != mapping.ActionImport {
			continue
		}
		sheet, ok := byName[sm.Sheet]
		if !ok {
			return nil, fmt.Errorf("importer: capture template of batch %s: sheet %q of the confirmed mapping is absent from the recorded workbook: %w",
				batchID, sm.Sheet, domain.ErrConflict)
		}
		oneSheet := mapping.Mapping{Sheets: []mapping.SheetMapping{sm}}
		oneJSON, err := mapping.Marshal(oneSheet)
		if err != nil {
			return nil, fmt.Errorf("importer: encode template mapping of sheet %q: %w", sm.Sheet, err)
		}
		tpl := domain.MappingTemplate{
			TemplateID:        domain.NewID("tpl"),
			SourceName:        sourceName,
			HeaderFingerprint: mapping.Fingerprint(sm.Sheet, sheet.Headers),
			ResourceType:      sm.ResourceType,
			MappingJSON:       string(oneJSON),
			SheetName:         sm.Sheet,
			ConfirmedAt:       now,
			ConfirmedBatch:    batchID,
		}
		if _, err := s.db.Templates.UpsertTemplate(ctx, tpl); err != nil {
			return nil, fmt.Errorf("importer: store template of sheet %q: %w", sm.Sheet, err)
		}
		// Re-read the stored row: an upsert of an existing identity keeps the
		// ORIGINAL template_id, and the response must report the truth.
		fp := tpl.HeaderFingerprint
		stored, err := s.db.Templates.FindByFingerprint(ctx, fp)
		if err != nil {
			return nil, fmt.Errorf("importer: reload template of sheet %q: %w", sm.Sheet, err)
		}
		captured = append(captured, stored)
	}
	// Stable sort by sheet name for the response (the upsert order stays the
	// confirmation order above; the LISTING contract is name-ordered).
	sort.Slice(captured, func(i, j int) bool { return captured[i].SheetName < captured[j].SheetName })
	return captured, nil
}
