package report

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/importer"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testfixtures"
	"github.com/mmqz/netops-fees-lab/internal/validation"
)

// testdataPath returns the path of a committed test artifact relative to
// this package's directory.
func testdataPath(name string) string {
	return filepath.Join("..", "..", "testdata", name)
}

// importOrder is the canonical import order of the three workbooks.
var importOrder = []string{"devices.xlsx", "ippools.xlsx", "circuits.xlsx"}

// newGenerator opens a fresh database, imports the three seeded workbooks
// with their suggested mappings, runs the set-level validation engine and
// returns the report generator plus the batch id of each workbook.
func newGenerator(t *testing.T) (*Generator, *repo.DB, map[string]string) {
	t.Helper()
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	ctx := context.Background()
	svc := importer.New(db)
	batches := map[string]string{}
	for _, file := range importOrder {
		a, err := svc.Analyze(ctx, testdataPath(file))
		if err != nil {
			t.Fatalf("Analyze(%s): %v", file, err)
		}
		if _, err := svc.Execute(ctx, a.BatchID, a.SuggestedMapping); err != nil {
			t.Fatalf("Execute(%s): %v", file, err)
		}
		batches[file] = a.BatchID
	}
	if _, err := validation.New(db).Run(ctx); err != nil {
		t.Fatalf("validation Run: %v", err)
	}
	return New(db), db, batches
}

// TestReportJSON checks the global and batch scopes of the JSON report
// against values DERIVED from the imported fixtures (resource, relation and
// issue counts are re-counted from the database, then cross-checked against
// the pinned context facts: 27/16/21 resources, 15/21/21 relations,
// E13/W3/I1 issues, score 32, completeness 90/80/84).
//
// Batch scope definition (asserted below): the devices batch report includes
// exactly the batch's row-phase issues (E001/E002/E004 + W002) plus ALL
// set-level issues (E005/E006/I001 — database-global findings), only the
// devices batch in summary.batches, and the quality score over that included
// set: 100 − 5·(3+2 errors) − 1·(1 warning) = 74. Resource and relation
// inventory counts stay whole-database (the set-level rules validate the
// global state).
func TestReportJSON(t *testing.T) {
	gen, db, batches := newGenerator(t)
	ctx := context.Background()

	// ---- Database-derived expectations (fixture truth, not trust). ----
	resByType, err := db.Resources.CountByType(ctx)
	if err != nil {
		t.Fatalf("count resources: %v", err)
	}
	relByType, err := db.Relations.CountByType(ctx)
	if err != nil {
		t.Fatalf("count relations: %v", err)
	}
	issBySeverity, err := db.Issues.CountBySeverity(ctx, nil)
	if err != nil {
		t.Fatalf("count issues: %v", err)
	}

	// Cross-checks against the context facts (a mismatch here means the
	// fixture reality differs from the plan and must be reported).
	for _, tc := range []struct {
		got  int
		want int
		what string
	}{
		{resByType[domain.ResourceTypeDevice], 27, "device resources"},
		{resByType[domain.ResourceTypeIPPool], 16, "ip_pool resources"},
		{resByType[domain.ResourceTypeCircuit], 21, "circuit resources"},
		{relByType[domain.RelationTypeAssignedTo], 15, "assigned_to relations"},
		{relByType[domain.RelationTypeAEnd], 21, "a_end relations"},
		{relByType[domain.RelationTypeZEnd], 21, "z_end relations"},
		{issBySeverity[domain.SeverityError], 13, "error issues"},
		{issBySeverity[domain.SeverityWarning], 3, "warning issues"},
		{issBySeverity[domain.SeverityInfo], 1, "info issues"},
	} {
		if tc.got != tc.want {
			t.Errorf("DB-derived %s = %d, context fact said %d (reporting the DB-derived truth)",
				tc.what, tc.got, tc.want)
		}
	}

	// ---- Global scope. ----
	r, err := gen.JSON(ctx, "")
	if err != nil {
		t.Fatalf("JSON(global): %v", err)
	}
	if r.Scope != "global" || r.RuleVersion != domain.RuleVersion {
		t.Errorf("scope/ruleVersion = %q/%q, want global/%q", r.Scope, r.RuleVersion, domain.RuleVersion)
	}
	if r.GeneratedAt.IsZero() {
		t.Errorf("generatedAt is zero")
	}
	if r.Summary.TotalResources != resByType[domain.ResourceTypeDevice]+resByType[domain.ResourceTypeIPPool]+resByType[domain.ResourceTypeCircuit] {
		t.Errorf("totalResources = %d, want %d", r.Summary.TotalResources,
			resByType[domain.ResourceTypeDevice]+resByType[domain.ResourceTypeIPPool]+resByType[domain.ResourceTypeCircuit])
	}
	for typ, want := range map[domain.ResourceType]int{
		domain.ResourceTypeDevice:  resByType[domain.ResourceTypeDevice],
		domain.ResourceTypeIPPool:  resByType[domain.ResourceTypeIPPool],
		domain.ResourceTypeCircuit: resByType[domain.ResourceTypeCircuit],
	} {
		if r.Summary.ByType[typ.String()] != want {
			t.Errorf("byType[%s] = %d, want %d", typ, r.Summary.ByType[typ.String()], want)
		}
	}
	if r.Summary.TotalRelations != relByType[domain.RelationTypeAssignedTo]+relByType[domain.RelationTypeAEnd]+relByType[domain.RelationTypeZEnd] {
		t.Errorf("totalRelations = %d, want %d", r.Summary.TotalRelations,
			relByType[domain.RelationTypeAssignedTo]+relByType[domain.RelationTypeAEnd]+relByType[domain.RelationTypeZEnd])
	}
	for typ, want := range map[domain.RelationType]int{
		domain.RelationTypeAssignedTo: relByType[domain.RelationTypeAssignedTo],
		domain.RelationTypeAEnd:       relByType[domain.RelationTypeAEnd],
		domain.RelationTypeZEnd:       relByType[domain.RelationTypeZEnd],
	} {
		if r.Summary.ByRelationType[typ.String()] != want {
			t.Errorf("byRelationType[%s] = %d, want %d", typ, r.Summary.ByRelationType[typ.String()], want)
		}
	}
	for _, sev := range []domain.Severity{domain.SeverityError, domain.SeverityWarning, domain.SeverityInfo} {
		if r.Summary.IssuesBySeverity[sev.String()] != issBySeverity[sev] {
			t.Errorf("issuesBySeverity[%s] = %d, want %d", sev, r.Summary.IssuesBySeverity[sev.String()], issBySeverity[sev])
		}
	}
	wantScore := 100 - 5*issBySeverity[domain.SeverityError] - issBySeverity[domain.SeverityWarning]
	if wantScore < 0 {
		wantScore = 0
	}
	if r.Summary.QualityScore != wantScore {
		t.Errorf("qualityScore = %d, want %d", r.Summary.QualityScore, wantScore)
	}
	if r.Summary.QualityScore != 32 {
		t.Errorf("qualityScore = %d, want the pinned 32 (E13/W3)", r.Summary.QualityScore)
	}

	// Batches: 3 entries, deterministic (started_at, workbook name) order
	// (started_at has second granularity, so same-second ties fall back to
	// the workbook-name tie-break), completeness 90/80/84.
	if len(r.Summary.Batches) != 3 {
		t.Fatalf("batches = %d, want 3", len(r.Summary.Batches))
	}
	dbBatches, _, err := db.Batches.List(ctx, 0, 0)
	if err != nil {
		t.Fatalf("list batches: %v", err)
	}
	sort.Slice(dbBatches, func(i, j int) bool {
		if !dbBatches[i].StartedAt.Equal(dbBatches[j].StartedAt) {
			return dbBatches[i].StartedAt.Before(dbBatches[j].StartedAt)
		}
		if dbBatches[i].WorkbookName != dbBatches[j].WorkbookName {
			return dbBatches[i].WorkbookName < dbBatches[j].WorkbookName
		}
		return dbBatches[i].ID < dbBatches[j].ID
	})
	wantBatches := map[string]struct {
		imported     int
		total        int
		completeness int
	}{
		"devices.xlsx":  {27, 30, 90},
		"ippools.xlsx":  {16, 20, 80},
		"circuits.xlsx": {21, 25, 84},
	}
	for i, dbb := range dbBatches {
		b := r.Summary.Batches[i]
		want := wantBatches[dbb.WorkbookName]
		if b.Workbook != dbb.WorkbookName || b.BatchID != dbb.ID {
			t.Errorf("batches[%d] = %s/%s, want %s/%s (pinned (started_at, workbook) order)",
				i, b.Workbook, b.BatchID, dbb.WorkbookName, dbb.ID)
		}
		if b.Status != domain.BatchStatusCompleted.String() {
			t.Errorf("batches[%s] status = %q, want completed", b.Workbook, b.Status)
		}
		if b.RowsTotal != want.total || b.RowsImported != want.imported {
			t.Errorf("batches[%s] rows = %d/%d, want %d/%d", b.Workbook, b.RowsTotal, b.RowsImported, want.total, want.imported)
		}
		if b.CompletenessPct != want.completeness {
			t.Errorf("batches[%s] completeness = %d, want %d", b.Workbook, b.CompletenessPct, want.completeness)
		}
	}

	// Issues: 17 total, ordered error → warning → info, then rule_code,
	// then entity key.
	if len(r.Issues) != 17 {
		t.Fatalf("issues = %d, want 17 (14 row-phase + 3 set-level)", len(r.Issues))
	}
	assertIssueOrder(t, r.Issues)

	// Spot checks of source resolution and entity keys.
	byRule := map[string][]Issue{}
	for _, iss := range r.Issues {
		byRule[iss.RuleCode] = append(byRule[iss.RuleCode], iss)
	}
	// Row-phase error: source = batch workbook + evidence sheet/row.
	e001 := single(t, byRule, domain.RuleDuplicateKey, "devices.xlsx")
	if e001.Source == nil || e001.ApplicationGenerated {
		t.Fatalf("E001 must resolve a source, got %+v", e001.Source)
	}
	if e001.Source.Workbook != "devices.xlsx" || e001.Source.Sheet != testfixtures.DeviceSheetName ||
		e001.Source.RowNumber != testfixtures.DeviceDupSecondRow {
		t.Errorf("E001 source = %+v, want devices.xlsx/%s/row %d", e001.Source,
			testfixtures.DeviceSheetName, testfixtures.DeviceDupSecondRow)
	}
	if e001.EntityKey != "" || e001.EntityID != "" {
		t.Errorf("E001 (failed row) must have no entity, got %q/%q", e001.EntityKey, e001.EntityID)
	}
	// Row-phase warning: source = the resource's source ref.
	w002 := single(t, byRule, domain.RuleUnknownStatusValue, "devices.xlsx")
	if w002.Source == nil || w002.Source.Workbook != "devices.xlsx" ||
		w002.Source.Sheet != testfixtures.DeviceSheetName ||
		w002.Source.RowNumber != testfixtures.DeviceUnknownStatusRow {
		t.Errorf("W002 source = %+v, want devices.xlsx/%s/row %d", w002.Source,
			testfixtures.DeviceSheetName, testfixtures.DeviceUnknownStatusRow)
	}
	if w002.EntityKey != "DEV-025" {
		t.Errorf("W002 entityKey = %q, want DEV-025", w002.EntityKey)
	}
	if w002.ApplicationGenerated {
		t.Errorf("W002 must not be application-generated")
	}
	// Set-level: application-generated with no source.
	for _, tc := range []struct {
		rule  string
		key   string
		count int
	}{
		{domain.RuleSubnetOverlap, "POOL-15", 1},
		{domain.RuleDuplicateAddress, "DEV-026", 1},
		{domain.RuleUnlinkedDevice, "DEV-030", 1},
	} {
		iss := single(t, byRule, tc.rule, "")
		if iss.Source != nil || !iss.ApplicationGenerated {
			t.Errorf("%s must be application-generated without source", tc.rule)
		}
		if iss.EntityKey != tc.key {
			t.Errorf("%s entityKey = %q, want %q", tc.rule, iss.EntityKey, tc.key)
		}
	}
	w001 := single(t, byRule, domain.RuleOrphanRecord, "")
	if w001.EntityKey != "POOL-14" {
		t.Errorf("W001 entityKey = %q, want POOL-14", w001.EntityKey)
	}
	w003 := single(t, byRule, domain.RuleCircuitSelfLoop, "")
	if w003.EntityKey != "CKT-021" {
		t.Errorf("W003 entityKey = %q, want CKT-021", w003.EntityKey)
	}

	// ---- Batch scope (devices). ----
	rb, err := gen.JSON(ctx, batches["devices.xlsx"])
	if err != nil {
		t.Fatalf("JSON(devices batch): %v", err)
	}
	if rb.Scope != "batch:"+batches["devices.xlsx"] {
		t.Errorf("batch scope = %q", rb.Scope)
	}
	if len(rb.Issues) != 7 {
		t.Fatalf("batch issues = %d, want 7 (E001/E002/E004/W002 + E005/E006/I001)", len(rb.Issues))
	}
	seen := map[string]int{}
	for _, iss := range rb.Issues {
		seen[iss.RuleCode]++
	}
	for _, rule := range []string{domain.RuleOrphanRecord, domain.RuleCircuitSelfLoop,
		domain.RuleInvalidReference} {
		if seen[rule] != 0 {
			t.Errorf("rule %s belongs to other batches and must not appear in the devices batch scope", rule)
		}
	}
	for _, rule := range []string{
		domain.RuleDuplicateKey, domain.RuleMissingRequiredField, domain.RuleInvalidFormat,
		domain.RuleUnknownStatusValue, domain.RuleSubnetOverlap, domain.RuleDuplicateAddress,
		domain.RuleUnlinkedDevice,
	} {
		if seen[rule] != 1 {
			t.Errorf("batch scope rule %s count = %d, want 1", rule, seen[rule])
		}
	}
	if len(rb.Summary.Batches) != 1 || rb.Summary.Batches[0].BatchID != batches["devices.xlsx"] {
		t.Errorf("batch scope batches = %+v, want only the devices batch", rb.Summary.Batches)
	}
	// Score over the included set: 5 errors, 1 warning ⇒ 74.
	if rb.Summary.IssuesBySeverity["error"] != 5 || rb.Summary.IssuesBySeverity["warning"] != 1 ||
		rb.Summary.IssuesBySeverity["info"] != 1 {
		t.Errorf("batch issuesBySeverity = %v, want E5/W1/I1", rb.Summary.IssuesBySeverity)
	}
	if rb.Summary.QualityScore != 74 {
		t.Errorf("batch qualityScore = %d, want 74 (100 − 5·5 − 1·1)", rb.Summary.QualityScore)
	}
	// Inventory stays whole-database (documented batch-scope semantics).
	if rb.Summary.TotalResources != r.Summary.TotalResources || rb.Summary.TotalRelations != r.Summary.TotalRelations {
		t.Errorf("batch inventory = %d/%d, want the global %d/%d",
			rb.Summary.TotalResources, rb.Summary.TotalRelations, r.Summary.TotalResources, r.Summary.TotalRelations)
	}
	assertIssueOrder(t, rb.Issues)

	// ---- Unknown batch → domain.ErrNotFound. ----
	if _, err := gen.JSON(ctx, "bat_does_not_exist"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("JSON(unknown batch) = %v, want domain.ErrNotFound", err)
	}

	// ---- WriteJSON round-trips the struct as indented JSON. ----
	var sb strings.Builder
	if err := gen.WriteJSON(ctx, "", &sb); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var back Report
	if err := json.Unmarshal([]byte(sb.String()), &back); err != nil {
		t.Fatalf("WriteJSON output is not valid JSON: %v", err)
	}
	if back.Scope != r.Scope || back.Summary.QualityScore != r.Summary.QualityScore || len(back.Issues) != len(r.Issues) {
		t.Errorf("WriteJSON round-trip mismatch: scope %q score %d issues %d", back.Scope, back.Summary.QualityScore, len(back.Issues))
	}
	if !strings.Contains(sb.String(), "\n  \"scope\"") {
		t.Errorf("WriteJSON output is not indented")
	}
}

// assertIssueOrder verifies the pinned ordering: severity error → warning →
// info, then rule_code, then entity key.
func assertIssueOrder(t *testing.T, issues []Issue) {
	t.Helper()
	rank := map[string]int{"error": 0, "warning": 1, "info": 2}
	for i := 1; i < len(issues); i++ {
		prev, cur := issues[i-1], issues[i]
		if rank[prev.Severity] > rank[cur.Severity] {
			t.Errorf("issues[%d] severity %s after %s", i, cur.Severity, prev.Severity)
		}
		if rank[prev.Severity] == rank[cur.Severity] && prev.RuleCode > cur.RuleCode {
			t.Errorf("issues[%d] rule %s after %s (same severity)", i, cur.RuleCode, prev.RuleCode)
		}
		if rank[prev.Severity] == rank[cur.Severity] && prev.RuleCode == cur.RuleCode && prev.EntityKey > cur.EntityKey {
			t.Errorf("issues[%d] entity %s after %s (same rule)", i, cur.EntityKey, prev.EntityKey)
		}
	}
}

// single returns the one issue of a rule (workbook disambiguates E-codes
// that appear in several batches, "" = unique overall).
func single(t *testing.T, byRule map[string][]Issue, rule, workbook string) Issue {
	t.Helper()
	var found []Issue
	for _, iss := range byRule[rule] {
		if workbook == "" || iss.Source != nil && iss.Source.Workbook == workbook {
			found = append(found, iss)
		}
	}
	if len(found) != 1 {
		t.Fatalf("rule %s (workbook %q): got %d issues, want 1", rule, workbook, len(found))
	}
	return found[0]
}

// TestReportHTML renders the standalone HTML report and checks the
// disclaimer, all three severity sections, the DEV-030 unlinked finding and
// the html/template auto-escaping of a hostile resource name injected
// straight through the repo.
func TestReportHTML(t *testing.T) {
	gen, db, batches := newGenerator(t)
	ctx := context.Background()

	// Inject a device whose NAME carries an XSS payload; it has no
	// relations, so the recomputed I001 finding carries the name in message
	// and evidence, and the template must escape it.
	xss := domain.Resource{
		ID:           domain.NewID("res"),
		Type:         domain.ResourceTypeDevice,
		NaturalKey:   "DEV-XSS",
		Name:         "<script>alert(1)</script>",
		FirstBatchID: batches["devices.xlsx"],
	}
	if err := db.Resources.Insert(ctx, xss); err != nil {
		t.Fatalf("inject XSS device: %v", err)
	}
	if _, err := validation.New(db).Run(ctx); err != nil {
		t.Fatalf("re-run validation after injection: %v", err)
	}

	html, err := gen.RenderHTML(ctx, "")
	if err != nil {
		t.Fatalf("RenderHTML(global): %v", err)
	}

	// Standalone document basics.
	if !strings.HasPrefix(html, "<!DOCTYPE html>") {
		t.Errorf("HTML must start with <!DOCTYPE html>, got %.30s", html)
	}
	if !strings.Contains(html, "<html lang=\"zh-CN\">") || !strings.Contains(html, "<title>NetOps Lab 数据质量报告</title>") {
		t.Errorf("HTML document header incomplete")
	}
	if !strings.Contains(html, "<style>") {
		t.Errorf("HTML must embed its CSS (self-contained)")
	}

	// Header block: title, scope, generation time, rule version.
	if !strings.Contains(html, "NetOps Lab 数据质量报告") {
		t.Errorf("HTML must carry the report title")
	}
	if !strings.Contains(html, "全局（全部批次）") || !strings.Contains(html, "生成时间") || !strings.Contains(html, domain.RuleVersion) {
		t.Errorf("HTML header block must show scope, generation time and rule version")
	}

	// Summary cards.
	for _, card := range []string{"资源总数", "问题总数", "质量评分", "数据完整率"} {
		if !strings.Contains(html, card) {
			t.Errorf("HTML must contain the summary card %q", card)
		}
	}

	// Disclaimer, verbatim (Chinese + English line) — issue #57②:
	// source-NEUTRAL wording; the report cannot verify what the
	// database holds, so it must not claim synthetic provenance.
	if !strings.Contains(html, "本报告基于当前已导入的数据生成，请以实际网络情况为准。") {
		t.Errorf("HTML must contain the verbatim Chinese disclaimer")
	}
	if !strings.Contains(html, "Generated from the currently imported datasets; verify against actual network conditions.") {
		t.Errorf("HTML must contain the English disclaimer line")
	}

	// All three severity sections.
	for _, label := range []string{"错误", "警告", "提示"} {
		if !strings.Contains(html, ">"+label+"<") {
			t.Errorf("HTML must contain the severity section %q", label)
		}
	}
	for _, chip := range []string{"chip-error", "chip-warning", "chip-info"} {
		if !strings.Contains(html, chip) {
			t.Errorf("HTML must style the severity chip %q", chip)
		}
	}

	// The DEV-030 unlinked issue is present.
	if !strings.Contains(html, testfixtures.DeviceUnlinkedCode) {
		t.Errorf("HTML must show the unlinked device %s", testfixtures.DeviceUnlinkedCode)
	}

	// Batch table.
	if !strings.Contains(html, "devices.xlsx") || !strings.Contains(html, "ippools.xlsx") ||
		!strings.Contains(html, "circuits.xlsx") {
		t.Errorf("HTML batch table must list the three workbooks")
	}

	// Escaping: the payload is escaped, the raw form never appears.
	if !strings.Contains(html, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("HTML must contain the escaped XSS payload &lt;script&gt;…")
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Errorf("HTML must NOT contain the raw XSS payload")
	}

	// Batch scope rendering works and is scoped.
	batchHTML, err := gen.RenderHTML(ctx, batches["devices.xlsx"])
	if err != nil {
		t.Fatalf("RenderHTML(devices batch): %v", err)
	}
	if !strings.Contains(batchHTML, "批次 "+batches["devices.xlsx"]) {
		t.Errorf("batch HTML must show the batch scope label")
	}
	// The batch-scope batch table lists ONLY the devices workbook.
	if strings.Contains(batchHTML, "ippools.xlsx") || strings.Contains(batchHTML, "circuits.xlsx") {
		t.Errorf("batch HTML must not list other batches' workbooks")
	}
}
