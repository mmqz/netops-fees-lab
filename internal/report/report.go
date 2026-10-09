// Package report builds the data-quality report of NetOps Lab from the
// repository state: a typed Report struct (serialized as JSON by the API and
// the CLI) and a standalone HTML rendering (html/template, auto-escaped,
// self-contained CSS).
//
// Scope semantics:
//
//   - global (batchID ""): every issue (row-phase + set-level), every batch
//     in summary.batches, quality score over all issues.
//   - batch (batchID != ""): the batch's row-phase issues PLUS ALL set-level
//     issues — set-level rules (E005/E006/I001) are database-global
//     findings, so they always appear — and only that batch in
//     summary.batches. The quality score is computed over exactly that
//     included issue set (100 − 5·#error − 1·#warning, floor 0), which for
//     the seeded devices batch yields 100 − 5·(3 row errors + 2 set errors)
//     − 1·(1 warning) = 74.
//
// The resource and relation inventory (totalResources/byType,
// totalRelations/byRelationType) always describes the WHOLE database: the
// set-level part of a batch report validates the global state, so the
// inventory it refers to is the global one.
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// Report is one rendered data-quality report. The JSON shape is the
// contract consumed by the HTTP API (GET /api/report?format=json) and the
// CLI (netopslab report --format json); the HTML rendering derives from the
// same struct.
type Report struct {
	// GeneratedAt is the report creation time (UTC).
	GeneratedAt time.Time `json:"generatedAt"`
	// RuleVersion is the validation rule catalog version.
	RuleVersion string `json:"ruleVersion"`
	// Scope is "global" or "batch:<batchId>".
	Scope string `json:"scope"`
	// Summary aggregates the validated dataset.
	Summary Summary `json:"summary"`
	// Issues lists the included issues ordered by severity
	// (error → warning → info), then rule_code, then entity key.
	Issues []Issue `json:"issues"`
}

// Summary is the aggregate block of the report.
type Summary struct {
	// TotalResources counts all resources of the database.
	TotalResources int `json:"totalResources"`
	// ByType counts resources per resource type.
	ByType map[string]int `json:"byType"`
	// TotalRelations counts all relations of the database.
	TotalRelations int `json:"totalRelations"`
	// ByRelationType counts relations per relation type.
	ByRelationType map[string]int `json:"byRelationType"`
	// IssuesBySeverity counts the INCLUDED issues per severity; the three
	// keys (error, warning, info) are always present.
	IssuesBySeverity map[string]int `json:"issuesBySeverity"`
	// QualityScore = 100 − 5·#error − 1·#warning over the included issue
	// set, floored at 0 (info issues do not deduct; PROJECT_BRIEF §7).
	QualityScore int `json:"qualityScore"`
	// Batches lists the batches in scope, in (started_at, workbook name)
	// order — chronological with a deterministic same-second tie-breaker
	// (started_at has second granularity).
	Batches []BatchSummary `json:"batches"`
}

// BatchSummary is the row-accounting snapshot of one import batch.
type BatchSummary struct {
	// BatchID is the import batch id.
	BatchID string `json:"batchId"`
	// Workbook is the workbook file name of the batch.
	Workbook string `json:"workbook"`
	// Status is the batch status (completed, failed, rolled_back, …).
	Status string `json:"status"`
	// RowsTotal counts the data rows of the batch's import sheets.
	RowsTotal int `json:"rowsTotal"`
	// RowsImported counts the rows that created a resource.
	RowsImported int `json:"rowsImported"`
	// RowsSkipped counts the rows skipped as duplicates.
	RowsSkipped int `json:"rowsSkipped"`
	// RowsFailed counts the rows rejected by row-phase error rules.
	RowsFailed int `json:"rowsFailed"`
	// CompletenessPct = round(100·imported/total); a batch with no data
	// rows is vacuously complete (100).
	CompletenessPct int `json:"completenessPct"`
}

// Issue is one report issue with resolved source provenance.
type Issue struct {
	// ID is the issue id.
	ID string `json:"id"`
	// RuleCode is the validation rule code (E001…, W001…, I001).
	RuleCode string `json:"ruleCode"`
	// Severity is error, warning or info.
	Severity string `json:"severity"`
	// Message is the human-readable (Chinese) finding.
	Message string `json:"message"`
	// EntityKind is the referenced entity kind; empty when the issue has
	// no entity (failed rows).
	EntityKind string `json:"entityKind,omitempty"`
	// EntityID is the referenced entity id; empty when there is no entity.
	EntityID string `json:"entityId,omitempty"`
	// EntityKey is the entity's natural key, resolved when possible.
	EntityKey string `json:"entityKey,omitempty"`
	// Source locates the workbook/sheet/row that produced the issue; nil
	// for application-generated issues.
	Source *Source `json:"source,omitempty"`
	// ApplicationGenerated marks issues computed by the set-level engine
	// (no workbook row of their own). It is false whenever Source is set.
	ApplicationGenerated bool `json:"applicationGenerated"`
	// Evidence carries the rule-specific evidence key/value pairs.
	Evidence map[string]string `json:"evidence,omitempty"`
}

// Source is the resolved provenance of an issue: the workbook, sheet and
// 1-based Excel row that produced it.
type Source struct {
	Workbook  string `json:"workbook"`
	Sheet     string `json:"sheet"`
	RowNumber int    `json:"rowNumber"`
}

// Generator builds reports from the repository state.
type Generator struct {
	db *repo.DB

	// Now is the clock hook stamping GeneratedAt; tests may replace it. A
	// nil hook falls back to time.Now.
	Now func() time.Time
}

// New returns a Generator bound to db.
func New(db *repo.DB) *Generator {
	return &Generator{db: db}
}

// clock returns the current UTC time, honoring the Now hook.
func (g *Generator) clock() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

// JSON builds the Report struct of the given scope. An empty batchID means
// the global scope; a non-empty batchID selects the batch scope and must
// reference an existing batch (domain.ErrNotFound otherwise).
func (g *Generator) JSON(ctx context.Context, batchID string) (*Report, error) {
	now := g.clock()

	var batchList []domain.ImportBatch
	scope := "global"
	if batchID == "" {
		list, _, err := g.db.Batches.List(ctx, 0, 0)
		if err != nil {
			return nil, fmt.Errorf("report: list batches: %w", err)
		}
		// Chronological (import) order with a deterministic tie-breaker;
		// Batches.List is newest-first, which suits the API list endpoint
		// but not a report's reading order.
		sort.Slice(list, func(i, j int) bool {
			if !list[i].StartedAt.Equal(list[j].StartedAt) {
				return list[i].StartedAt.Before(list[j].StartedAt)
			}
			if list[i].WorkbookName != list[j].WorkbookName {
				return list[i].WorkbookName < list[j].WorkbookName
			}
			return list[i].ID < list[j].ID
		})
		batchList = list
	} else {
		b, err := g.db.Batches.Get(ctx, batchID)
		if err != nil {
			return nil, fmt.Errorf("report: batch %s: %w", batchID, err)
		}
		batchList = []domain.ImportBatch{b}
		scope = "batch:" + batchID
	}

	byType, err := g.db.Resources.CountByType(ctx)
	if err != nil {
		return nil, fmt.Errorf("report: count resources: %w", err)
	}
	totalResources := 0
	byTypeJSON := map[string]int{}
	for t, n := range byType {
		totalResources += n
		byTypeJSON[t.String()] = n
	}
	byRelation, err := g.db.Relations.CountByType(ctx)
	if err != nil {
		return nil, fmt.Errorf("report: count relations: %w", err)
	}
	totalRelations := 0
	byRelationJSON := map[string]int{}
	for t, n := range byRelation {
		totalRelations += n
		byRelationJSON[t.String()] = n
	}

	// Issues: one query, then the scope filter (batch scope = the batch's
	// row-phase issues + ALL set-level issues with batch_id NULL).
	dbIssues, _, err := g.db.Issues.List(ctx, repo.IssueFilter{})
	if err != nil {
		return nil, fmt.Errorf("report: list issues: %w", err)
	}
	included := make([]domain.Issue, 0, len(dbIssues))
	for _, iss := range dbIssues {
		// Batch scope keeps the batch's row-phase issues and ALL set-level
		// issues (batch_id NULL — database-global findings).
		inScope := batchID == "" || iss.BatchID == nil || *iss.BatchID == batchID
		if inScope {
			included = append(included, iss)
		}
	}

	// Entity key resolution: one resource scan builds the id → natural key
	// map (also reused for the HTML view).
	resources, _, err := g.db.Resources.Search(ctx, repo.ResourceSearchFilter{})
	if err != nil {
		return nil, fmt.Errorf("report: search resources: %w", err)
	}
	entityKeys := make(map[string]string, len(resources))
	for _, res := range resources {
		entityKeys[res.ID] = res.NaturalKey
	}

	// Workbook names for the row-phase issues' source resolution.
	workbooks := make(map[string]string, len(batchList))
	for _, b := range batchList {
		workbooks[b.ID] = b.WorkbookName
	}

	issues := make([]Issue, 0, len(included))
	sevCount := map[string]int{domain.SeverityError.String(): 0, domain.SeverityWarning.String(): 0, domain.SeverityInfo.String(): 0}
	for _, iss := range included {
		sevCount[iss.Severity.String()]++
		src := g.resolveSource(ctx, iss, workbooks)
		issues = append(issues, Issue{
			ID:                   iss.ID,
			RuleCode:             iss.RuleCode,
			Severity:             iss.Severity.String(),
			Message:              iss.Message,
			EntityKind:           iss.EntityKind.String(),
			EntityID:             iss.EntityID,
			EntityKey:            entityKeys[iss.EntityID],
			Source:               src,
			ApplicationGenerated: src == nil,
			Evidence:             iss.Evidence,
		})
	}
	sortIssues(issues)

	batches := make([]BatchSummary, 0, len(batchList))
	totalRows, totalImported := 0, 0
	for _, b := range batchList {
		totalRows += b.RowsTotal
		totalImported += b.RowsImported
		batches = append(batches, BatchSummary{
			BatchID:         b.ID,
			Workbook:        b.WorkbookName,
			Status:          b.Status.String(),
			RowsTotal:       b.RowsTotal,
			RowsImported:    b.RowsImported,
			RowsSkipped:     b.RowsSkipped,
			RowsFailed:      b.RowsFailed,
			CompletenessPct: completenessPct(b.RowsImported, b.RowsTotal),
		})
	}

	return &Report{
		GeneratedAt: now,
		RuleVersion: domain.RuleVersion,
		Scope:       scope,
		Summary: Summary{
			TotalResources:   totalResources,
			ByType:           byTypeJSON,
			TotalRelations:   totalRelations,
			ByRelationType:   byRelationJSON,
			IssuesBySeverity: sevCount,
			QualityScore:     qualityScore(sevCount),
			Batches:          batches,
		},
		Issues: issues,
	}, nil
}

// WriteJSON builds the report of the given scope and writes it to w as
// indented UTF-8 JSON.
func (g *Generator) WriteJSON(ctx context.Context, batchID string, w io.Writer) error {
	r, err := g.JSON(ctx, batchID)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("report: encode json: %w", err)
	}
	return nil
}

// resolveSource locates the workbook/sheet/row provenance of an issue:
//
//   - a non-nil source_ref_id resolves via the source_refs row (row-phase
//     warnings on imported rows);
//   - otherwise a row-phase failure (batch_id set) carries its origin in
//     the evidence (sheet + row_number) and the workbook name of its batch;
//   - everything else is application-generated (set-level issues) → nil.
func (g *Generator) resolveSource(ctx context.Context, iss domain.Issue, workbooks map[string]string) *Source {
	if iss.SourceRefID != nil {
		ref, err := g.db.SourceRefs.GetByID(ctx, *iss.SourceRefID)
		if err == nil {
			return &Source{Workbook: ref.Workbook, Sheet: ref.Sheet, RowNumber: ref.RowNumber}
		}
		// A dangling source ref (e.g. the batch was rolled back after the
		// issue was recorded) degrades to application-generated.
		return nil
	}
	if iss.BatchID != nil && iss.Evidence["sheet"] != "" && iss.Evidence["row_number"] != "" {
		rowNumber, err := strconv.Atoi(iss.Evidence["row_number"])
		if err != nil {
			rowNumber = 0
		}
		return &Source{
			Workbook:  workbooks[*iss.BatchID],
			Sheet:     iss.Evidence["sheet"],
			RowNumber: rowNumber,
		}
	}
	return nil
}

// severityRank orders severities error → warning → info.
func severityRank(s string) int {
	switch s {
	case domain.SeverityError.String():
		return 0
	case domain.SeverityWarning.String():
		return 1
	default:
		return 2
	}
}

// sortIssues orders the report issues: severity (error → warning → info),
// then rule_code, then entity key, then the source location (workbook,
// sheet, row number) as the business tie-breaker for entity-less row
// failures, then message and id as the final total-order guards.
func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		a, b := issues[i], issues[j]
		if severityRank(a.Severity) != severityRank(b.Severity) {
			return severityRank(a.Severity) < severityRank(b.Severity)
		}
		if a.RuleCode != b.RuleCode {
			return a.RuleCode < b.RuleCode
		}
		if a.EntityKey != b.EntityKey {
			return a.EntityKey < b.EntityKey
		}
		if sourceCompare(a.Source, b.Source) != 0 {
			return sourceCompare(a.Source, b.Source) < 0
		}
		if a.Message != b.Message {
			return a.Message < b.Message
		}
		return a.ID < b.ID
	})
}

// sourceCompare orders two resolved sources (nil sorts after non-nil).
func sourceCompare(a, b *Source) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	case a.Workbook != b.Workbook:
		if a.Workbook < b.Workbook {
			return -1
		}
		return 1
	case a.Sheet != b.Sheet:
		if a.Sheet < b.Sheet {
			return -1
		}
		return 1
	case a.RowNumber != b.RowNumber:
		if a.RowNumber < b.RowNumber {
			return -1
		}
		return 1
	}
	return 0
}

// qualityScore computes 100 − 5·#error − 1·#warning, floored at 0.
func qualityScore(sevCount map[string]int) int {
	score := 100 - 5*sevCount[domain.SeverityError.String()] - sevCount[domain.SeverityWarning.String()]
	if score < 0 {
		return 0
	}
	return score
}

// completenessPct returns round(100·imported/total); total <= 0 yields 100
// (a batch without data rows is vacuously complete).
func completenessPct(imported, total int) int {
	if total <= 0 {
		return 100
	}
	return int(math.Round(float64(imported) / float64(total) * 100))
}
