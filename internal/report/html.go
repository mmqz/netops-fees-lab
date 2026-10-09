package report

import (
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"sort"
	"strconv"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
)

// reportTemplateHTML is the standalone HTML template of the quality report;
// html/template auto-escapes every dynamic value it interpolates.
//
//go:embed template.html
var reportTemplateHTML string

// htmlTmpl parses the template once at package initialization; a malformed
// template is a programming error and panics at startup (template.Must).
var htmlTmpl = template.Must(template.New("report").Parse(reportTemplateHTML))

// htmlView is the template data model of the standalone HTML report. It is
// derived from Report; all values are pre-rendered display strings or plain
// numbers, so the template stays free of logic.
type htmlView struct {
	Scope           string
	ScopeLabel      string
	GeneratedAt     string
	RuleVersion     string
	TotalResources  int
	TotalRelations  int
	TotalIssues     int
	ErrorCount      int
	WarningCount    int
	InfoCount       int
	QualityScore    int
	ScoreClass      string
	CompletenessPct int
	ByType          []countRow
	ByRelationType  []countRow
	Batches         []batchView
	Groups          []severityGroup
}

// countRow is one "name → count" inventory line.
type countRow struct {
	Key   string
	Value int
}

// batchView is one row of the batch accounting table.
type batchView struct {
	ID           string
	Workbook     string
	Status       string
	StatusLabel  string
	Total        int
	Imported     int
	Skipped      int
	Failed       int
	Completeness int
}

// severityGroup is one severity section of the issue list.
type severityGroup struct {
	Severity string // error | warning | info
	Label    string // 错误 | 警告 | 提示
	Class    string // CSS chip class
	Count    int
	Issues   []issueView
}

// issueView is one row of a severity section.
type issueView struct {
	RuleCode    string
	Message     string
	EntityLabel string
	HasSource   bool
	SourceLabel string
	Evidence    []evidenceRow
}

// evidenceRow is one key/value pair of an issue's evidence.
type evidenceRow struct {
	Key   string
	Value string
}

// statusLabels maps batch statuses to Chinese display labels.
var statusLabels = map[string]string{
	domain.BatchStatusCompleted.String():       "已完成",
	domain.BatchStatusFailed.String():          "失败",
	domain.BatchStatusRolledBack.String():      "已回滚",
	domain.BatchStatusAnalyzing.String():       "解析中",
	domain.BatchStatusAwaitingMapping.String(): "待确认映射",
	domain.BatchStatusImporting.String():       "导入中",
}

// severityMeta describes the three severity sections in report order.
var severityMeta = []struct {
	severity string
	label    string
	class    string
}{
	{domain.SeverityError.String(), "错误", "chip-error"},
	{domain.SeverityWarning.String(), "警告", "chip-warning"},
	{domain.SeverityInfo.String(), "提示", "chip-info"},
}

// RenderHTML builds the report of the given scope ("" = global) and renders
// it as a single self-contained HTML document (inline CSS, no external
// resources, all dynamic values auto-escaped by html/template).
func (g *Generator) RenderHTML(ctx context.Context, batchID string) (string, error) {
	r, err := g.JSON(ctx, batchID)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	if err := htmlTmpl.Execute(&sb, buildHTMLView(r)); err != nil {
		return "", fmt.Errorf("report: render html: %w", err)
	}
	return sb.String(), nil
}

// buildHTMLView derives the template data model from a built report.
func buildHTMLView(r *Report) htmlView {
	v := htmlView{
		Scope:          r.Scope,
		GeneratedAt:    r.GeneratedAt.Format("2006-01-02 15:04:05 UTC"),
		RuleVersion:    r.RuleVersion,
		TotalResources: r.Summary.TotalResources,
		TotalRelations: r.Summary.TotalRelations,
		TotalIssues:    len(r.Issues),
		ErrorCount:     r.Summary.IssuesBySeverity[domain.SeverityError.String()],
		WarningCount:   r.Summary.IssuesBySeverity[domain.SeverityWarning.String()],
		InfoCount:      r.Summary.IssuesBySeverity[domain.SeverityInfo.String()],
		QualityScore:   r.Summary.QualityScore,
		ByType:         sortedCounts(r.Summary.ByType),
		ByRelationType: sortedCounts(r.Summary.ByRelationType),
	}

	// Scope label: 全局 or the batch id with its workbook name.
	v.ScopeLabel = "全局（全部批次）"
	if strings.HasPrefix(r.Scope, "batch:") {
		id := strings.TrimPrefix(r.Scope, "batch:")
		workbook := ""
		if len(r.Summary.Batches) > 0 {
			workbook = r.Summary.Batches[0].Workbook
		}
		v.ScopeLabel = "批次 " + id
		if workbook != "" {
			v.ScopeLabel += "（" + workbook + "）"
		}
	}

	// Score color band.
	switch {
	case v.QualityScore >= 80:
		v.ScoreClass = "score-good"
	case v.QualityScore >= 50:
		v.ScoreClass = "score-mid"
	default:
		v.ScoreClass = "score-bad"
	}

	// Overall completeness: imported/total weighted over the batches in scope.
	totalRows, totalImported := 0, 0
	for _, b := range r.Summary.Batches {
		totalRows += b.RowsTotal
		totalImported += b.RowsImported
	}
	v.CompletenessPct = completenessPct(totalImported, totalRows)

	for _, b := range r.Summary.Batches {
		v.Batches = append(v.Batches, batchView{
			ID:           b.BatchID,
			Workbook:     b.Workbook,
			Status:       b.Status,
			StatusLabel:  statusLabels[b.Status],
			Total:        b.RowsTotal,
			Imported:     b.RowsImported,
			Skipped:      b.RowsSkipped,
			Failed:       b.RowsFailed,
			Completeness: b.CompletenessPct,
		})
	}

	for _, meta := range severityMeta {
		group := severityGroup{Severity: meta.severity, Label: meta.label, Class: meta.class}
		for _, iss := range r.Issues {
			if iss.Severity != meta.severity {
				continue
			}
			entity := "—"
			if iss.EntityKey != "" {
				entity = iss.EntityKey
				if iss.EntityKind != "" && iss.EntityKind != domain.EntityKindResource.String() {
					entity = iss.EntityKind + " · " + iss.EntityKey
				}
			}
			source, hasSource := "系统生成", false
			if iss.Source != nil {
				hasSource = true
				source = iss.Source.Workbook + " · " + iss.Source.Sheet
				if iss.Source.RowNumber != 0 {
					source += " · 第 " + strconv.Itoa(iss.Source.RowNumber) + " 行"
				}
			}
			ev := make([]evidenceRow, 0, len(iss.Evidence))
			for _, k := range sortedStringKeys(iss.Evidence) {
				ev = append(ev, evidenceRow{Key: k, Value: iss.Evidence[k]})
			}
			group.Issues = append(group.Issues, issueView{
				RuleCode:    iss.RuleCode,
				Message:     iss.Message,
				EntityLabel: entity,
				HasSource:   hasSource,
				SourceLabel: source,
				Evidence:    ev,
			})
		}
		group.Count = len(group.Issues)
		v.Groups = append(v.Groups, group)
	}
	return v
}

// sortedCounts turns a count map into key-sorted rows.
func sortedCounts(m map[string]int) []countRow {
	rows := make([]countRow, 0, len(m))
	for _, k := range sortedStringKeys(m) {
		rows = append(rows, countRow{Key: k, Value: m[k]})
	}
	return rows
}

// sortedStringKeys returns the keys of m in sorted order.
func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
