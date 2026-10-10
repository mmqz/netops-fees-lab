package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/testfixtures"
	"github.com/mmqz/netops-lab/internal/workbook"

	"github.com/xuri/excelize/v2"
)

// testdataPath returns the path of a committed test artifact relative to
// this package's directory.
func testdataPath(name string) string {
	return filepath.Join("..", "..", "testdata", name)
}

// expectedWorkbook mirrors one entry of testdata/expected-summary.json.
type expectedWorkbook struct {
	File         string         `json:"file"`
	Sheet        string         `json:"sheet"`
	Rows         int            `json:"rows"`
	RowsImported int            `json:"rowsImported"`
	RowsSkipped  int            `json:"rowsSkipped"`
	RowsFailed   int            `json:"rowsFailed"`
	Expected     map[string]int `json:"expected"`
}

// loadExpectedSummary decodes testdata/expected-summary.json.
func loadExpectedSummary(t *testing.T) map[string]expectedWorkbook {
	t.Helper()
	data, err := os.ReadFile(testdataPath("expected-summary.json"))
	if err != nil {
		t.Fatalf("read expected-summary.json: %v", err)
	}
	var m map[string]expectedWorkbook
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse expected-summary.json: %v", err)
	}
	return m
}

// newService opens a fresh database and returns a Service bound to it. The
// DB is closed at test cleanup — a Close failure fails the test.
func newService(t *testing.T) (*Service, *repo.DB) {
	t.Helper()
	db, err := repo.Open(filepath.Join(t.TempDir(), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	return New(db), db
}

// importWorkbook analyzes and executes one workbook with its suggested
// mapping; the canonical import order contract is devices → ippools →
// circuits (refs resolve against earlier batches).
func importWorkbook(t *testing.T, svc *Service, file string) (Analysis, Summary) {
	t.Helper()
	ctx := context.Background()
	a, err := svc.Analyze(ctx, testdataPath(file))
	if err != nil {
		t.Fatalf("Analyze(%s): %v", file, err)
	}
	if len(a.Sheets) != 1 {
		t.Fatalf("Analyze(%s): got %d sheets, want 1", file, len(a.Sheets))
	}
	sum, err := svc.Execute(ctx, a.BatchID, a.SuggestedMapping)
	if err != nil {
		t.Fatalf("Execute(%s): %v", file, err)
	}
	return a, sum
}

// importOrder is the canonical import order of the three workbooks.
var importOrder = []string{"devices", "ippools", "circuits"}

// rowPhaseCodes are the rule codes whose issues carry a batch id.
var rowPhaseCodes = []string{
	domain.RuleDuplicateKey, domain.RuleMissingRequiredField,
	domain.RuleInvalidReference, domain.RuleInvalidFormat,
	domain.RuleOrphanRecord, domain.RuleUnknownStatusValue,
	domain.RuleCircuitSelfLoop,
}

// batchIssues lists all issues attributed to a batch.
func batchIssues(t *testing.T, db *repo.DB, batchID string) []domain.Issue {
	t.Helper()
	issues, total, err := db.Issues.List(context.Background(), repo.IssueFilter{BatchID: &batchID})
	if err != nil {
		t.Fatalf("list issues of batch %s: %v", batchID, err)
	}
	if total != len(issues) {
		t.Fatalf("issues total %d != len %d", total, len(issues))
	}
	return issues
}

// ruleCodeCount groups issues by rule code.
func ruleCodeCount(issues []domain.Issue) map[string]int {
	counts := map[string]int{}
	for _, iss := range issues {
		counts[iss.RuleCode]++
	}
	return counts
}

// failedRowCodes maps Excel row number → rule code of the failed rows of a
// batch.
func failedRowCodes(t *testing.T, db *repo.DB, batchID string) map[int]string {
	t.Helper()
	rows, _, err := db.RowResults.List(context.Background(),
		repo.RowResultFilter{BatchID: batchID, Status: domain.RowStatusFailed})
	if err != nil {
		t.Fatalf("list failed rows of batch %s: %v", batchID, err)
	}
	m := map[int]string{}
	for _, rr := range rows {
		if _, dup := m[rr.RowNumber]; dup {
			t.Fatalf("duplicate failed row %d in batch %s", rr.RowNumber, batchID)
		}
		m[rr.RowNumber] = rr.RuleCode
	}
	return m
}

// findIssue returns the single issue of a batch with the given rule code.
func findIssue(t *testing.T, issues []domain.Issue, code string) domain.Issue {
	t.Helper()
	var found []domain.Issue
	for _, iss := range issues {
		if iss.RuleCode == code {
			found = append(found, iss)
		}
	}
	if len(found) != 1 {
		t.Fatalf("rule %s: got %d issues, want 1", code, len(found))
	}
	return found[0]
}

// TestImportRowAccounting is ACC-1: every input row of every import sheet is
// recorded, totals reconcile per sheet, and the counts match
// testdata/expected-summary.json exactly. Also asserts the exact Excel row
// numbers and rule codes of the failed rows against the testfixtures seed
// locators.
func TestImportRowAccounting(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	expected := loadExpectedSummary(t)

	analyses := map[string]Analysis{}
	for _, key := range importOrder {
		a, sum := importWorkbook(t, svc, expected[key].File)
		analyses[key] = a

		exp := expected[key]
		if sum.BatchID != a.BatchID || sum.Status != domain.BatchStatusCompleted {
			t.Fatalf("%s: summary = %+v, want completed batch %s", key, sum, a.BatchID)
		}
		if sum.RowsTotal != exp.Rows || sum.RowsImported != exp.RowsImported ||
			sum.RowsSkipped != exp.RowsSkipped || sum.RowsFailed != exp.RowsFailed {
			t.Errorf("%s: counts = %d/%d/%d/%d (total/imported/skipped/failed), want %d/%d/%d/%d",
				key, sum.RowsTotal, sum.RowsImported, sum.RowsSkipped, sum.RowsFailed,
				exp.Rows, exp.RowsImported, exp.RowsSkipped, exp.RowsFailed)
		}
		// RowsTotal equals the sheet's data-row count.
		if sum.RowsTotal != a.Sheets[0].RowCount {
			t.Errorf("%s: RowsTotal %d != sheet data rows %d", key, sum.RowsTotal, a.Sheets[0].RowCount)
		}

		// The persisted batch carries the same accounting and timestamps.
		b, err := db.Batches.Get(ctx, sum.BatchID)
		if err != nil {
			t.Fatalf("get batch: %v", err)
		}
		if b.Status != domain.BatchStatusCompleted || b.FinishedAt == nil {
			t.Errorf("%s: batch = %s (finished %v), want completed with finish time", key, b.Status, b.FinishedAt)
		}
		if b.RowsTotal != exp.Rows || b.RowsImported != exp.RowsImported ||
			b.RowsSkipped != exp.RowsSkipped || b.RowsFailed != exp.RowsFailed {
			t.Errorf("%s: batch counts = %d/%d/%d/%d, want %d/%d/%d/%d", key,
				b.RowsTotal, b.RowsImported, b.RowsSkipped, b.RowsFailed,
				exp.Rows, exp.RowsImported, exp.RowsSkipped, exp.RowsFailed)
		}
		if b.RuleVersion != domain.RuleVersion {
			t.Errorf("%s: batch rule version = %q, want %q", key, b.RuleVersion, domain.RuleVersion)
		}
		if _, err := mapping.Parse([]byte(b.Mapping)); err != nil {
			t.Errorf("%s: stored mapping is not valid JSON: %v", key, err)
		}

		// Row results: one per data row, statuses reconcile with the counts.
		rows, total, err := db.RowResults.List(ctx, repo.RowResultFilter{BatchID: sum.BatchID})
		if err != nil {
			t.Fatalf("list row results: %v", err)
		}
		if total != len(rows) || total != exp.Rows {
			t.Fatalf("%s: row results total = %d, want %d", key, total, exp.Rows)
		}
		byStatus := map[domain.RowStatus]int{}
		sheets := map[string]bool{}
		for _, rr := range rows {
			if !rr.Status.IsValid() {
				t.Errorf("%s: invalid row status %q", key, rr.Status)
			}
			byStatus[rr.Status]++
			sheets[rr.Sheet] = true
			if rr.Sheet != exp.Sheet {
				t.Errorf("%s: row result sheet = %q, want %q", key, rr.Sheet, exp.Sheet)
			}
		}
		if byStatus[domain.RowStatusImported] != exp.RowsImported ||
			byStatus[domain.RowStatusSkipped] != exp.RowsSkipped ||
			byStatus[domain.RowStatusFailed] != exp.RowsFailed {
			t.Errorf("%s: row result statuses = %v, want imported/skipped/failed %d/%d/%d", key,
				byStatus, exp.RowsImported, exp.RowsSkipped, exp.RowsFailed)
		}
		if byStatus[domain.RowStatusImported]+byStatus[domain.RowStatusSkipped]+
			byStatus[domain.RowStatusFailed] != exp.Rows {
			t.Errorf("%s: row statuses do not sum to data rows", key)
		}
		if len(sheets) != 1 {
			t.Errorf("%s: row results span %d sheets, want 1", key, len(sheets))
		}

		// CountsByBatch agrees with the row result scan.
		counts, err := db.RowResults.CountsByBatch(ctx, sum.BatchID)
		if err != nil {
			t.Fatalf("counts by batch: %v", err)
		}
		if counts[domain.RowStatusImported] != exp.RowsImported ||
			counts[domain.RowStatusSkipped] != exp.RowsSkipped ||
			counts[domain.RowStatusFailed] != exp.RowsFailed {
			t.Errorf("%s: CountsByBatch = %v", key, counts)
		}
	}

	// Exact Excel row numbers + rule codes of the failed rows (seed
	// locators of internal/testfixtures).
	wantFailed := map[string]map[int]string{
		"devices": {
			testfixtures.DeviceDupSecondRow:   domain.RuleDuplicateKey,
			testfixtures.DeviceInvalidIPRow:   domain.RuleInvalidFormat,
			testfixtures.DeviceMissingNameRow: domain.RuleMissingRequiredField,
		},
		"ippools": {
			testfixtures.IPPoolMissingCIDRRow: domain.RuleMissingRequiredField,
			testfixtures.IPPoolBadCIDRRow:     domain.RuleInvalidFormat,
			testfixtures.IPPoolBadVLANRow:     domain.RuleInvalidFormat,
			testfixtures.IPPoolUnknownRefRow:  domain.RuleInvalidReference,
		},
		"circuits": {
			testfixtures.CircuitDupSecondRow:    domain.RuleDuplicateKey,
			testfixtures.CircuitMissingZRow:     domain.RuleMissingRequiredField,
			testfixtures.CircuitUnknownRefRow:   domain.RuleInvalidReference,
			testfixtures.CircuitBadBandwidthRow: domain.RuleInvalidFormat,
		},
	}
	for key, want := range wantFailed {
		got := failedRowCodes(t, db, analyses[key].BatchID)
		if len(got) != len(want) {
			t.Errorf("%s: failed rows = %v, want %v", key, got, want)
			continue
		}
		for rowNum, code := range want {
			if got[rowNum] != code {
				t.Errorf("%s: failed row %d rule = %q, want %q", key, rowNum, got[rowNum], code)
			}
		}
	}

	// Aggregate database state after the three imports.
	resCounts, err := db.Resources.CountByType(ctx)
	if err != nil {
		t.Fatalf("count resources: %v", err)
	}
	for _, tc := range []struct {
		typ  domain.ResourceType
		want int
	}{
		{domain.ResourceTypeDevice, expected["devices"].RowsImported},
		{domain.ResourceTypeIPPool, expected["ippools"].RowsImported},
		{domain.ResourceTypeCircuit, expected["circuits"].RowsImported},
	} {
		if resCounts[tc.typ] != tc.want {
			t.Errorf("resource count %s = %d, want %d", tc.typ, resCounts[tc.typ], tc.want)
		}
	}
	relCounts, err := db.Relations.CountByType(ctx)
	if err != nil {
		t.Fatalf("count relations: %v", err)
	}
	// 15 resolved assigned_device refs (POOL-01..13, POOL-15, POOL-16) and
	// 2 refs per imported circuit (21 circuits, self-loop CKT-021 included).
	for _, tc := range []struct {
		typ  domain.RelationType
		want int
	}{
		{domain.RelationTypeAssignedTo, 15},
		{domain.RelationTypeAEnd, 21},
		{domain.RelationTypeZEnd, 21},
	} {
		if relCounts[tc.typ] != tc.want {
			t.Errorf("relation count %s = %d, want %d", tc.typ, relCounts[tc.typ], tc.want)
		}
	}
	// Every imported row leaves a resource source ref, every relation its
	// own relation source ref.
	wantRefs := map[string]int{
		"devices":  27, // 27 resources, no relations
		"ippools":  31, // 16 resources + 15 relations
		"circuits": 63, // 21 resources + 42 relations
	}
	for key, want := range wantRefs {
		n, err := db.SourceRefs.CountByBatch(ctx, analyses[key].BatchID)
		if err != nil {
			t.Fatalf("count source refs: %v", err)
		}
		if n != want {
			t.Errorf("%s: source refs = %d, want %d", key, n, want)
		}
	}
}

// TestImportIssueEvidence is ACC-3: every row-phase issue either carries
// source evidence (sheet + row_number for failed rows) or a source_ref_id
// (warnings on imported rows), and the rule codes per workbook match the
// expected map of expected-summary.json (set-level E005/E006/I001 are T6's
// job and must be absent here).
func TestImportIssueEvidence(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	expected := loadExpectedSummary(t)

	batchIDs := map[string]string{}
	for _, key := range importOrder {
		a, _ := importWorkbook(t, svc, expected[key].File)
		batchIDs[key] = a.BatchID
	}

	for key, batchID := range batchIDs {
		issues := batchIssues(t, db, batchID)
		byCode := ruleCodeCount(issues)

		// Row-phase rule counts match the expected map.
		for _, code := range rowPhaseCodes {
			if byCode[code] != expected[key].Expected[code] {
				t.Errorf("%s: issue count rule %s = %d, want %d", key, code, byCode[code], expected[key].Expected[code])
			}
		}
		// Set-level codes must not appear as batch issues (T6 recompute).
		for _, code := range domain.SetLevelRuleCodes {
			if byCode[code] != 0 {
				t.Errorf("%s: set-level rule %s wrongly recorded as batch issue", key, code)
			}
		}

		for _, iss := range issues {
			sev, ok := domain.SeverityForCode(iss.RuleCode)
			if !ok || iss.Severity != sev {
				t.Errorf("%s: issue %s rule %s severity = %q, want %q", key, iss.ID, iss.RuleCode, iss.Severity, sev)
			}
			if iss.RuleVersion != domain.RuleVersion {
				t.Errorf("%s: issue %s rule version = %q", key, iss.ID, iss.RuleVersion)
			}
			if iss.BatchID == nil || *iss.BatchID != batchID {
				t.Errorf("%s: issue %s has wrong batch id %v", key, iss.ID, iss.BatchID)
			}
			if iss.Message == "" {
				t.Errorf("%s: issue %s has empty message", key, iss.ID)
			}
			if iss.Severity == domain.SeverityError {
				// Failed rows: evidence locates the origin, no source ref.
				if iss.SourceRefID != nil {
					t.Errorf("%s: error issue %s must not have a source_ref_id", key, iss.ID)
				}
				if iss.Evidence["sheet"] == "" || iss.Evidence["row_number"] == "" {
					t.Errorf("%s: error issue %s evidence lacks sheet/row_number: %v", key, iss.ID, iss.Evidence)
					continue
				}
				rowNum, err := strconv.Atoi(iss.Evidence["row_number"])
				if err != nil || rowNum < 2 {
					t.Errorf("%s: error issue %s evidence row_number %q invalid", key, iss.ID, iss.Evidence["row_number"])
				}
				// The referenced row must be a failed row result with the
				// same rule code.
				failed := failedRowCodes(t, db, batchID)
				if code, ok := failed[rowNum]; !ok || code != iss.RuleCode {
					t.Errorf("%s: issue %s (row %d, %s) does not match a failed row result", key, iss.ID, rowNum, iss.RuleCode)
				}
			} else {
				// Warnings: source_ref_id points at the created resource's
				// source ref of the same batch.
				if iss.SourceRefID == nil {
					t.Errorf("%s: warning issue %s has no source_ref_id", key, iss.ID)
					continue
				}
				if iss.EntityKind != domain.EntityKindResource || iss.EntityID == "" {
					t.Errorf("%s: warning issue %s lacks resource entity: kind %q id %q", key, iss.ID, iss.EntityKind, iss.EntityID)
					continue
				}
				refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, iss.EntityID)
				if err != nil {
					t.Fatalf("list source refs of %s: %v", iss.EntityID, err)
				}
				found := false
				for _, ref := range refs {
					if ref.ID == *iss.SourceRefID && ref.BatchID == batchID {
						found = true
					}
				}
				if !found {
					t.Errorf("%s: warning issue %s references source ref %s not in batch", key, iss.ID, *iss.SourceRefID)
				}
			}
		}
	}

	// Spot-check the seeded evidence of the devices batch.
	devIssues := batchIssues(t, db, batchIDs["devices"])
	e001 := findIssue(t, devIssues, domain.RuleDuplicateKey)
	if e001.Evidence["row_number"] != strconv.Itoa(testfixtures.DeviceDupSecondRow) ||
		e001.Evidence["conflicting_row_number"] != strconv.Itoa(testfixtures.DeviceDupFirstRow) ||
		e001.Evidence["natural_key"] != testfixtures.DeviceDupCode {
		t.Errorf("E001 evidence = %v", e001.Evidence)
	}
	e002 := findIssue(t, devIssues, domain.RuleMissingRequiredField)
	if e002.Evidence["row_number"] != strconv.Itoa(testfixtures.DeviceMissingNameRow) ||
		e002.Evidence["field"] != "device_name" {
		t.Errorf("E002 evidence = %v", e002.Evidence)
	}
	e004 := findIssue(t, devIssues, domain.RuleInvalidFormat)
	if e004.Evidence["row_number"] != strconv.Itoa(testfixtures.DeviceInvalidIPRow) ||
		e004.Evidence["field"] != "mgmt_ip" || e004.Evidence["raw_value"] != testfixtures.DeviceInvalidIP {
		t.Errorf("E004 evidence = %v", e004.Evidence)
	}
	w002 := findIssue(t, devIssues, domain.RuleUnknownStatusValue)
	if w002.Evidence["raw_value"] != testfixtures.DeviceUnknownStatus || w002.Evidence["field"] != "status" {
		t.Errorf("W002 evidence = %v", w002.Evidence)
	}
	// The W002 device still imported, without a stored status.
	dev, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-025")
	if err != nil {
		t.Fatalf("DEV-025 must be imported despite W002: %v", err)
	}
	if dev.Status != "" {
		t.Errorf("DEV-025 status = %q, want empty (unknown status not stored)", dev.Status)
	}
	// Ippool/ circuit evidence spot checks.
	poolIssues := batchIssues(t, db, batchIDs["ippools"])
	e003 := findIssue(t, poolIssues, domain.RuleInvalidReference)
	if e003.Evidence["ref_value"] != testfixtures.IPPoolUnknownDevice ||
		e003.Evidence["expected_type"] != domain.ResourceTypeDevice.String() {
		t.Errorf("E003 evidence = %v", e003.Evidence)
	}
	w001 := findIssue(t, poolIssues, domain.RuleOrphanRecord)
	if w001.Evidence["field"] != "assigned_device" {
		t.Errorf("W001 evidence = %v", w001.Evidence)
	}
	cktIssues := batchIssues(t, db, batchIDs["circuits"])
	w003 := findIssue(t, cktIssues, domain.RuleCircuitSelfLoop)
	if w003.Evidence["circuit_code"] != "CKT-021" || w003.Evidence["device"] != testfixtures.CircuitSelfLoopDev {
		t.Errorf("W003 evidence = %v", w003.Evidence)
	}
}

// TestReimportIdempotent is ACC-2: importing the same workbook with the
// same mapping twice leaves the database unchanged — run 2 produces 27
// skipped rows, the same 3 deterministic E-row failures, zero new
// resources and no repeated warning issues.
func TestReimportIdempotent(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	a1, s1 := importWorkbook(t, svc, "devices.xlsx")
	a2, s2 := importWorkbook(t, svc, "devices.xlsx")

	if a1.BatchID == a2.BatchID {
		t.Fatal("the two runs must create two batches")
	}
	if s2.RowsImported != 0 || s2.RowsSkipped != 27 || s2.RowsFailed != 3 || s2.RowsTotal != 30 {
		t.Errorf("run 2 counts = %d/%d/%d/%d, want 0/27/3/30 (imported/skipped/failed/total)",
			s2.RowsImported, s2.RowsSkipped, s2.RowsFailed, s2.RowsTotal)
	}
	if s1.RowsImported != 27 {
		t.Fatalf("run 1 imported = %d, want 27", s1.RowsImported)
	}

	// Resource count unchanged; no resources attributed to batch 2.
	resCounts, err := db.Resources.CountByType(ctx)
	if err != nil {
		t.Fatalf("count resources: %v", err)
	}
	if resCounts[domain.ResourceTypeDevice] != 27 {
		t.Errorf("device resources after re-import = %d, want 27", resCounts[domain.ResourceTypeDevice])
	}
	res2, err := db.Resources.ListByBatch(ctx, a2.BatchID)
	if err != nil {
		t.Fatalf("list resources of batch 2: %v", err)
	}
	if len(res2) != 0 {
		t.Errorf("batch 2 created %d resources, want 0", len(res2))
	}

	// Row results of run 2: 27 skipped + 3 failed.
	rc, err := db.RowResults.CountsByBatch(ctx, a2.BatchID)
	if err != nil {
		t.Fatalf("counts by batch: %v", err)
	}
	if rc[domain.RowStatusImported] != 0 || rc[domain.RowStatusSkipped] != 27 || rc[domain.RowStatusFailed] != 3 {
		t.Errorf("run 2 row results = %v, want 0/27/3", rc)
	}
	// The failed rows are the same three seed rows as run 1 (deterministic).
	for _, failed := range []map[int]string{failedRowCodes(t, db, a1.BatchID), failedRowCodes(t, db, a2.BatchID)} {
		if len(failed) != 3 {
			t.Fatalf("failed rows = %v, want 3", failed)
		}
		if failed[testfixtures.DeviceDupSecondRow] != domain.RuleDuplicateKey ||
			failed[testfixtures.DeviceInvalidIPRow] != domain.RuleInvalidFormat ||
			failed[testfixtures.DeviceMissingNameRow] != domain.RuleMissingRequiredField {
			t.Errorf("failed rows = %v", failed)
		}
	}

	// Skipped rows carry the idempotency message; E-row failures re-record
	// their issues; warnings of skipped rows are NOT re-recorded.
	byCode := ruleCodeCount(batchIssues(t, db, a2.BatchID))
	for _, code := range rowPhaseCodes {
		want := 0
		switch code {
		case domain.RuleDuplicateKey, domain.RuleMissingRequiredField, domain.RuleInvalidFormat:
			want = 1 // E-row failures repeat deterministically
		}
		if byCode[code] != want {
			t.Errorf("run 2 issues rule %s = %d, want %d", code, byCode[code], want)
		}
	}
	// In particular: exactly one W002 issue in the whole database (run 1);
	// the W002 row was SKIPPED in run 2 and recorded nothing.
	w002, total, err := db.Issues.List(ctx, repo.IssueFilter{RuleCode: domain.RuleUnknownStatusValue})
	if err != nil {
		t.Fatalf("list W002 issues: %v", err)
	}
	if total != 1 || len(w002) != 1 || w002[0].BatchID == nil || *w002[0].BatchID != a1.BatchID {
		t.Errorf("W002 issues = %d (batch %v), want 1 from run 1 only", total, w002[0].BatchID)
	}

	// Skipped row result message references the first batch.
	rows, _, err := db.RowResults.List(ctx, repo.RowResultFilter{
		BatchID: a2.BatchID, Status: domain.RowStatusSkipped, Limit: 1,
	})
	if err != nil || len(rows) != 1 {
		t.Fatalf("list skipped rows: %v (%d)", err, len(rows))
	}
	wantMsg := fmt.Sprintf("重复导入：资源已存在于批次 %s", a1.BatchID)
	if rows[0].Message != wantMsg {
		t.Errorf("skipped message = %q, want %q", rows[0].Message, wantMsg)
	}
	if rows[0].RuleCode != domain.RuleDuplicateKey {
		t.Errorf("skipped rule code = %q, want %q", rows[0].RuleCode, domain.RuleDuplicateKey)
	}

	// No set-level issues exist before the validation engine runs (T6).
	issues, _, err := db.Issues.List(ctx, repo.IssueFilter{BatchID: nil})
	if err != nil {
		t.Fatalf("list all issues: %v", err)
	}
	for _, iss := range issues {
		if iss.BatchID == nil {
			t.Errorf("unexpected set-level issue %s (%s) before validation", iss.ID, iss.RuleCode)
		}
	}
}

// TestImportAtomicRollback is S0-6: an internal failure injected after the
// first import sheet rolls back every write of the batch (resources,
// relations, source refs, row results, issues — all zero), the batch is
// marked failed, and the pre-existing state of the database is untouched.
func TestImportAtomicRollback(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	// Baseline: the devices import completes normally.
	dAnalysis, dSummary := importWorkbook(t, svc, "devices.xlsx")

	// The ippools import gets a failure injected after its first (only)
	// sheet, so its resource/relation/source-ref writes must vanish.
	a, err := svc.Analyze(ctx, testdataPath("ippools.xlsx"))
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	injected := errors.New("injected failure after first sheet")
	svc.setAfterSheetHook(func(sheet string) error { return injected })
	defer svc.setAfterSheetHook(nil)

	_, err = svc.Execute(ctx, a.BatchID, a.SuggestedMapping)
	if err == nil {
		t.Fatal("Execute must fail when the hook injects a failure")
	}
	if !errors.Is(err, injected) {
		t.Errorf("Execute error = %v, want it to wrap the injected error", err)
	}

	// Batch failed with the recorded error; nothing of it survived.
	b, err := db.Batches.Get(ctx, a.BatchID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	if b.Status != domain.BatchStatusFailed {
		t.Errorf("batch status = %s, want failed", b.Status)
	}
	if !strings.Contains(b.Error, "injected failure") || b.FinishedAt == nil {
		t.Errorf("batch error/finished = %q / %v", b.Error, b.FinishedAt)
	}
	if n, err := db.Resources.CountByType(ctx); err != nil {
		t.Fatalf("count resources: %v", err)
	} else if n[domain.ResourceTypeIPPool] != 0 {
		t.Errorf("ip_pool resources after rollback = %d, want 0", n[domain.ResourceTypeIPPool])
	}
	res, err := db.Resources.ListByBatch(ctx, a.BatchID)
	if err != nil || len(res) != 0 {
		t.Errorf("resources of failed batch = %d (err %v), want 0", len(res), err)
	}
	if n, err := db.Relations.CountByBatch(ctx, a.BatchID); err != nil || n != 0 {
		t.Errorf("relations of failed batch = %d (err %v), want 0", n, err)
	}
	if n, err := db.SourceRefs.CountByBatch(ctx, a.BatchID); err != nil || n != 0 {
		t.Errorf("source refs of failed batch = %d (err %v), want 0", n, err)
	}
	rows, total, err := db.RowResults.List(ctx, repo.RowResultFilter{BatchID: a.BatchID})
	if err != nil || total != 0 || len(rows) != 0 {
		t.Errorf("row results of failed batch = %d (err %v), want 0", total, err)
	}
	if issues := batchIssues(t, db, a.BatchID); len(issues) != 0 {
		t.Errorf("issues of failed batch = %d, want 0", len(issues))
	}

	// The earlier devices import is fully intact (DB state unchanged).
	if n, err := db.Resources.CountByType(ctx); err != nil {
		t.Fatalf("count resources: %v", err)
	} else if n[domain.ResourceTypeDevice] != 27 {
		t.Errorf("device resources = %d, want 27 (baseline untouched)", n[domain.ResourceTypeDevice])
	}
	rc, err := db.RowResults.CountsByBatch(ctx, dSummary.BatchID)
	if err != nil {
		t.Fatalf("counts by batch: %v", err)
	}
	if rc[domain.RowStatusImported] != 27 || rc[domain.RowStatusFailed] != 3 || rc[domain.RowStatusSkipped] != 0 {
		t.Errorf("baseline row results = %v, want 27 imported / 3 failed", rc)
	}
	if n, err := db.SourceRefs.CountByBatch(ctx, dAnalysis.BatchID); err != nil || n != 27 {
		t.Errorf("baseline source refs = %d (err %v), want 27", n, err)
	}
	// The DB handle stays healthy after the injected rollback and closes
	// cleanly at test cleanup (asserted by newService's cleanup).
}

// TestRollbackBatch covers batch rollback: rolling back the circuits batch
// removes its resources, relations, source refs, row results and issues
// while devices and ip_pools stay intact; rolling back the devices batch is
// refused with domain.ErrConflict because ippool relations of another batch
// reference its devices.
func TestRollbackBatch(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	aDev, _ := importWorkbook(t, svc, "devices.xlsx")
	aPool, _ := importWorkbook(t, svc, "ippools.xlsx")
	aCkt, _ := importWorkbook(t, svc, "circuits.xlsx")

	// Capture one circuit resource and one of its relations to verify
	// their source evidence disappears with the rollback.
	cktResources, _, err := db.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeCircuit})
	if err != nil || len(cktResources) == 0 {
		t.Fatalf("search circuits: %v (%d)", err, len(cktResources))
	}
	var ckt domain.Resource
	for _, r := range cktResources {
		if r.NaturalKey == "CKT-001" {
			ckt = r
		}
	}
	cktRels, err := db.Relations.ListByResource(ctx, ckt.ID)
	if err != nil || len(cktRels) == 0 {
		t.Fatalf("list relations of CKT-001: %v (%d)", err, len(cktRels))
	}
	relID := cktRels[0].ID

	// Pre-rollback sanity: the circuits batch owns 21 resources, 42
	// relations and 63 source refs (21 resource + 42 relation refs).
	if n, _ := db.Relations.CountByBatch(ctx, aCkt.BatchID); n != 42 {
		t.Fatalf("circuits batch relations before rollback = %d, want 42", n)
	}
	if n, _ := db.SourceRefs.CountByBatch(ctx, aCkt.BatchID); n != 63 {
		t.Fatalf("circuits batch source refs before rollback = %d, want 63", n)
	}

	if err := svc.Rollback(ctx, aCkt.BatchID); err != nil {
		t.Fatalf("rollback circuits batch: %v", err)
	}

	// Circuits are gone; devices and pools are intact.
	resCounts, err := db.Resources.CountByType(ctx)
	if err != nil {
		t.Fatalf("count resources: %v", err)
	}
	if resCounts[domain.ResourceTypeCircuit] != 0 ||
		resCounts[domain.ResourceTypeDevice] != 27 ||
		resCounts[domain.ResourceTypeIPPool] != 16 {
		t.Errorf("resource counts after circuits rollback = %v, want 0/27/16 (circuit/device/pool)", resCounts)
	}
	relCounts, err := db.Relations.CountByType(ctx)
	if err != nil {
		t.Fatalf("count relations: %v", err)
	}
	if relCounts[domain.RelationTypeAEnd] != 0 || relCounts[domain.RelationTypeZEnd] != 0 ||
		relCounts[domain.RelationTypeAssignedTo] != 15 {
		t.Errorf("relation counts after circuits rollback = %v, want a_end 0, z_end 0, assigned_to 15", relCounts)
	}
	if n, _ := db.Relations.CountByBatch(ctx, aCkt.BatchID); n != 0 {
		t.Errorf("relations of rolled-back circuits batch = %d, want 0", n)
	}
	if n, _ := db.SourceRefs.CountByBatch(ctx, aCkt.BatchID); n != 0 {
		t.Errorf("source refs of rolled-back circuits batch = %d, want 0", n)
	}
	if total := len(batchIssues(t, db, aCkt.BatchID)); total != 0 {
		t.Errorf("issues of rolled-back circuits batch = %d, want 0", total)
	}
	rows, total, err := db.RowResults.List(ctx, repo.RowResultFilter{BatchID: aCkt.BatchID})
	if err != nil || total != 0 || len(rows) != 0 {
		t.Errorf("row results of rolled-back batch = %d (err %v), want 0", total, err)
	}
	b, err := db.Batches.Get(ctx, aCkt.BatchID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	if b.Status != domain.BatchStatusRolledBack || b.FinishedAt == nil {
		t.Errorf("rolled-back batch status/finished = %s / %v", b.Status, b.FinishedAt)
	}
	// The captured circuit and relation evidence are gone.
	if _, err := db.Resources.GetByID(ctx, ckt.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("circuit resource after rollback: %v, want ErrNotFound", err)
	}
	if refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, ckt.ID); err != nil || len(refs) != 0 {
		t.Errorf("circuit resource source refs after rollback = %d (err %v), want 0", len(refs), err)
	}
	if refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindRelation, relID); err != nil || len(refs) != 0 {
		t.Errorf("circuit relation source refs after rollback = %d (err %v), want 0", len(refs), err)
	}

	// Devices rollback is refused: ippool relations (another batch)
	// reference the devices.
	err = svc.Rollback(ctx, aDev.BatchID)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("rollback of devices batch = %v, want domain.ErrConflict", err)
	}
	bDev, err := db.Batches.Get(ctx, aDev.BatchID)
	if err != nil {
		t.Fatalf("get devices batch: %v", err)
	}
	if bDev.Status != domain.BatchStatusCompleted {
		t.Errorf("devices batch status after refused rollback = %s, want completed", bDev.Status)
	}
	if n, _ := db.Resources.CountByType(ctx); n[domain.ResourceTypeDevice] != 27 {
		t.Errorf("device resources after refused rollback = %d, want 27", n[domain.ResourceTypeDevice])
	}
	// Pool batch is still completed and untouched as well.
	bPool, err := db.Batches.Get(ctx, aPool.BatchID)
	if err != nil || bPool.Status != domain.BatchStatusCompleted {
		t.Errorf("pool batch status = %s (err %v), want completed", bPool.Status, err)
	}
	// Rolling back an already rolled-back batch is refused too.
	err = svc.Rollback(ctx, aCkt.BatchID)
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("second rollback = %v, want domain.ErrConflict", err)
	}
	// Unknown batch id.
	if err := svc.Rollback(ctx, "bat_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("rollback of missing batch = %v, want domain.ErrNotFound", err)
	}
}

// TestRollbackInUseCheckHoldsWriteTransaction pins the R2-P2-1 fix: the
// in-use check runs INSIDE the rollback transaction (BEGIN IMMEDIATE), so
// the write lock is already held the moment the check completes. A write
// from a second connection that references the checked batch's entities can
// no longer slip between the check and the deletions — the old layout ran
// the check before the transaction, leaving exactly that window open.
//
// The test pauses the rollback right after its in-use check (via the
// test-only hook) and observes whether a concurrent relation INSERT is
// blocked: blocked ⇒ the lock (and therefore the check) is in-transaction.
func TestRollbackInUseCheckHoldsWriteTransaction(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	aDev, _ := importWorkbook(t, svc, "devices.xlsx")
	aPool, _ := importWorkbook(t, svc, "ippools.xlsx") // its relations reference devices

	// Two device resources of the checked batch for the concurrent insert.
	devs, _, err := db.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeDevice})
	if err != nil || len(devs) < 2 {
		t.Fatalf("search devices: %v (%d)", err, len(devs))
	}
	var rel domain.Relation
	rel.ID = domain.NewID("rel")
	rel.Type = domain.RelationTypeAEnd // unique (from, to, type) triple
	rel.FromResourceID = devs[1].ID
	rel.ToResourceID = devs[0].ID
	rel.FirstBatchID = aPool.BatchID
	rel.CreatedAt = time.Now().UTC()

	insertDone := make(chan error, 1)
	svc.setRollbackCheckHook(func() error {
		// Second pooled connection: the INSERT must block on the write
		// lock held by the rollback transaction. If the check ran
		// outside the transaction (regression), no lock is held and
		// the INSERT completes immediately.
		go func() {
			insertDone <- db.Relations.Insert(ctx, rel)
		}()
		select {
		case err := <-insertDone:
			return fmt.Errorf("concurrent write finished during the in-use window (check outside transaction?): %v", err)
		case <-time.After(750 * time.Millisecond):
			return nil // still blocked ⇒ write lock held at check time
		}
	})

	// Devices batch is in use (ippool relations): refused, nothing deleted.
	if err := svc.Rollback(ctx, aDev.BatchID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("rollback of in-use devices batch = %v, want domain.ErrConflict", err)
	}
	b, err := db.Batches.Get(ctx, aDev.BatchID)
	if err != nil || b.Status != domain.BatchStatusCompleted {
		t.Errorf("devices batch after refused rollback = %s (err %v), want completed", b.Status, err)
	}
	if n, _ := db.Resources.CountByType(ctx); n[domain.ResourceTypeDevice] != 27 {
		t.Errorf("device resources after refused rollback = %d, want 27", n[domain.ResourceTypeDevice])
	}

	// Drain the background INSERT: once the rollback transaction ends the
	// lock is released and the insert must complete promptly (no deadlock).
	select {
	case <-insertDone:
	case <-time.After(10 * time.Second):
		t.Error("concurrent insert never unblocked after the rollback transaction ended")
	}
}

// TestAnalyzeErrors covers the analyze failure path: an unreadable
// (non-xlsx) workbook marks the batch failed and returns a typed error; a
// missing file returns an error without creating any batch.
func TestAnalyzeErrors(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	// A text file masquerading as .xlsx.
	path := filepath.Join(t.TempDir(), "not.xlsx")
	if err := os.WriteFile(path, []byte("this is not an xlsx workbook"), 0o644); err != nil {
		t.Fatalf("write fake workbook: %v", err)
	}
	_, err := svc.Analyze(ctx, path)
	if err == nil {
		t.Fatal("Analyze of a non-xlsx file must fail")
	}
	if !errors.Is(err, workbook.ErrNotXLSX) {
		t.Errorf("Analyze error = %v, want it to wrap workbook.ErrNotXLSX", err)
	}
	batches, total, err := db.Batches.List(ctx, 1, 0)
	if err != nil || total != 1 || len(batches) != 1 {
		t.Fatalf("list batches: %v (total %d)", err, total)
	}
	fb := batches[0]
	if fb.Status != domain.BatchStatusFailed {
		t.Errorf("failed analyze batch status = %s, want failed", fb.Status)
	}
	if fb.Error == "" || fb.FinishedAt == nil {
		t.Errorf("failed analyze batch error/finished = %q / %v", fb.Error, fb.FinishedAt)
	}
	if fb.RuleVersion != domain.RuleVersion {
		t.Errorf("failed batch rule version = %q", fb.RuleVersion)
	}
	if n, err := db.Resources.CountByType(ctx); err != nil || len(n) != 0 {
		t.Errorf("resources after failed analyze = %v (err %v), want none", n, err)
	}

	// A missing file: no batch is created (the file cannot be hashed).
	_, err = svc.Analyze(ctx, filepath.Join(t.TempDir(), "missing.xlsx"))
	if err == nil {
		t.Fatal("Analyze of a missing file must fail")
	}
	if _, total, err := db.Batches.List(ctx, 1, 0); err != nil || total != 1 {
		t.Errorf("batches after missing-file analyze = %d (err %v), want 1", total, err)
	}
}

// TestExecuteErrors covers the Execute error contract: mapping validation
// failures keep the batch awaiting_mapping; a non-awaiting or unknown batch
// is refused; an all-skip mapping completes with zero rows.
func TestExecuteErrors(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	t.Run("validation error keeps awaiting_mapping", func(t *testing.T) {
		a, err := svc.Analyze(ctx, testdataPath("devices.xlsx"))
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		bad := a.SuggestedMapping
		bad.Sheets[0].Sheet = "不存在的表"
		_, err = svc.Execute(ctx, a.BatchID, bad)
		if err == nil {
			t.Fatal("Execute with unknown sheet must fail")
		}
		var fe *mapping.FieldError
		if !errors.As(err, &fe) {
			t.Fatalf("Execute error %v is not *mapping.FieldError", err)
		}
		b, err := db.Batches.Get(ctx, a.BatchID)
		if err != nil {
			t.Fatalf("get batch: %v", err)
		}
		if b.Status != domain.BatchStatusAwaitingMapping {
			t.Errorf("batch status after validation error = %s, want awaiting_mapping", b.Status)
		}
		if n, _ := db.Resources.CountByType(ctx); n[domain.ResourceTypeDevice] != 0 {
			t.Errorf("resources after validation error = %d, want 0", n[domain.ResourceTypeDevice])
		}
	})

	t.Run("all-skip mapping completes with zero rows", func(t *testing.T) {
		a, err := svc.Analyze(ctx, testdataPath("devices.xlsx"))
		if err != nil {
			t.Fatalf("Analyze: %v", err)
		}
		m := mapping.Mapping{Sheets: []mapping.SheetMapping{
			{Sheet: testfixtures.DeviceSheetName, Action: mapping.ActionSkip},
		}}
		sum, err := svc.Execute(ctx, a.BatchID, m)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if sum.Status != domain.BatchStatusCompleted || sum.RowsTotal != 0 ||
			sum.RowsImported != 0 || sum.RowsSkipped != 0 || sum.RowsFailed != 0 {
			t.Errorf("all-skip summary = %+v, want completed with zero counts", sum)
		}
		if n, _ := db.Resources.CountByType(ctx); n[domain.ResourceTypeDevice] != 0 {
			t.Errorf("resources after all-skip import = %d, want 0", n[domain.ResourceTypeDevice])
		}
	})

	t.Run("execute on non-awaiting batch is refused", func(t *testing.T) {
		a, _ := importWorkbook(t, svc, "devices.xlsx")
		_, err := svc.Execute(ctx, a.BatchID, a.SuggestedMapping)
		if !errors.Is(err, domain.ErrConflict) {
			t.Errorf("re-execute of completed batch = %v, want domain.ErrConflict", err)
		}
	})

	t.Run("unknown batch is refused", func(t *testing.T) {
		_, err := svc.Execute(ctx, "bat_missing", mapping.Mapping{})
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("execute of unknown batch = %v, want domain.ErrNotFound", err)
		}
	})
}

// TestAnalyzeStructure verifies the analysis payload of the committed
// workbooks: sheet metadata, column samples and the suggested mapping that
// passes validation against the workbook headers.
func TestAnalyzeStructure(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	expected := loadExpectedSummary(t)

	for _, key := range importOrder {
		a, err := svc.Analyze(ctx, testdataPath(expected[key].File))
		if err != nil {
			t.Fatalf("Analyze(%s): %v", key, err)
		}
		if a.BatchID == "" || a.WorkbookName != expected[key].File || len(a.SHA256) != 64 {
			t.Errorf("%s: analysis header = %q/%q/%q", key, a.BatchID, a.WorkbookName, a.SHA256)
		}
		sa := a.Sheets[0]
		if sa.Sheet != expected[key].Sheet || sa.RowCount != expected[key].Rows || sa.SkippedEmptyRows != 0 {
			t.Errorf("%s: sheet analysis = %+v", key, sa)
		}
		wantCols := map[string][]string{
			"devices":  testfixtures.DeviceHeaders,
			"ippools":  testfixtures.IPPoolHeaders,
			"circuits": testfixtures.CircuitHeaders,
		}[key]
		if len(sa.Columns) != len(wantCols) {
			t.Fatalf("%s: %d columns, want %d", key, len(sa.Columns), len(wantCols))
		}
		for i, col := range sa.Columns {
			if col.Header != wantCols[i] {
				t.Errorf("%s: column %d header = %q, want %q", key, i, col.Header, wantCols[i])
			}
			if len(col.Samples) == 0 || len(col.Samples) > maxSamples {
				t.Errorf("%s: column %q samples = %v", key, col.Header, col.Samples)
			}
		}
		// First sample of the key column is the first data row's value.
		// The suggested mapping validates against the real headers.
		headers := make([]string, len(sa.Columns))
		for i, col := range sa.Columns {
			headers[i] = col.Header
		}
		if err := mapping.Validate(a.SuggestedMapping, map[string][]string{sa.Sheet: headers}); err != nil {
			t.Errorf("%s: suggested mapping does not validate: %v", key, err)
		}
		// Batch awaits mapping with the workbook path recorded.
		b, err := svc.db.Batches.Get(ctx, a.BatchID)
		if err != nil {
			t.Fatalf("get batch: %v", err)
		}
		if b.Status != domain.BatchStatusAwaitingMapping || b.WorkbookSHA256 != a.SHA256 {
			t.Errorf("%s: batch = %s (sha %q), want awaiting_mapping with the analysis hash", key, b.Status, b.WorkbookSHA256)
		}
		if p, err := svc.db.Batches.WorkbookPath(ctx, a.BatchID); err != nil || p != testdataPath(expected[key].File) {
			t.Errorf("%s: workbook path = %q (err %v)", key, p, err)
		}
	}
}

// TestServiceClock verifies the Now hook default and override.
func TestServiceClock(t *testing.T) {
	svc, _ := newService(t)
	if svc.Now == nil {
		t.Fatal("New must install a clock")
	}
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc.Now = func() time.Time { return fixed }
	if got := svc.clock(); !got.Equal(fixed) {
		t.Errorf("clock() = %v, want %v", got, fixed)
	}
	svc.Now = nil
	if svc.clock().Before(fixed) {
		t.Errorf("clock() with nil hook should fall back to time.Now")
	}
}

// writeWorkbookFile builds an xlsx file with one sheet of raw string rows
// (row 0 = header) at a temp path and returns the path. It mirrors the
// testfixtures generator's excelize usage for synthetic test sheets.
func writeWorkbookFile(t *testing.T, sheet string, rows [][]string) string {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	if sheet != "Sheet1" {
		if err := f.SetSheetName("Sheet1", sheet); err != nil {
			t.Fatalf("rename default sheet: %v", err)
		}
	}
	for r, row := range rows {
		for c, v := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("cell name: %v", err)
			}
			if err := f.SetCellStr(sheet, cell, v); err != nil {
				t.Fatalf("write %s: %v", cell, err)
			}
		}
	}
	path := filepath.Join(t.TempDir(), "workbook.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save workbook: %v", err)
	}
	return path
}

// portHeadersA mirrors the real telecom sheet's header row A
// (samples/telecom-port-multi-header.xlsx, Sheet1): 12 business columns.
var portHeadersA = []string{"地市", "区县", "设备名称", "设备ip", "端口", "端口ip", "端口带宽", "端口类型", "端口描述", "厂家", "型号", "日期"}

// portMapping builds the confirmed mapping for the port type: composite key
// (device_name, port_name) against the header row A columns.
func portMapping(sheet string) mapping.Mapping {
	return mapping.Mapping{Sheets: []mapping.SheetMapping{{
		Sheet:        sheet,
		Action:       mapping.ActionImport,
		ResourceType: domain.ResourceTypePort,
		KeyColumns:   []string{"device_name", "port_name"},
		ColumnMapping: map[string]string{
			"device_name":    "设备名称",
			"port_name":      "端口",
			"device_ip":      "设备ip",
			"port_desc":      "端口描述",
			"port_type":      "端口类型",
			"bandwidth_mbps": "端口带宽",
			"city":           "地市",
			"vendor":         "厂家",
			"model":          "型号",
			"record_date":    "日期",
		},
	}}}
}

// TestImportPortCompositeKeyAndHeaderRepeat drives the real telecom sheet
// shape through the importer: a verbatim repeated header row (multi-segment
// export concatenation) is skipped with a dedicated message and no issue,
// the composite natural key (device_name, port_name) drives E001 and
// idempotent re-import, missing key fields fail E002 per field, and
// bandwidth 0 imports (no IntMin on the port schema). device_ip is a
// required ipv4 VALUE column (migration v8 removed it from the key): it
// fails E002/E004 as a field and lands in attributes. A single-key device
// import in the same database pins the legacy natural-key behavior.
func TestImportPortCompositeKeyAndHeaderRepeat(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()

	data1 := []string{"", "", "B2", "12.1.53.24", "bvi2.1008", "", "0", "业务口", "BBU_CT_1333", "中兴", "9000-3E", "2026-10-09"}
	data2 := []string{"", "", "道西30米1", "4.8.2.5", "xgei-1/1/0/2.186", "", "1000", "业务口", "BBU_CU_108709", "中兴", "6120H-BL", "2026-10-09"}
	noPort := []string{"", "", "X1", "5.6.7.8", "", "", "100", "业务口", "D", "华为", "S", "2026-10-09"}
	noIP := []string{"", "", "X2", "", "gei-1/1/0/8", "", "100", "业务口", "D2", "华为", "S", "2026-10-09"}
	rows := [][]string{
		portHeadersA, // row 1: header A
		data1,        // row 2: imported (bandwidth 0!)
		portHeadersA, // row 3: verbatim repeated header row
		data2,        // row 4: imported
		data2,        // row 5: E001 duplicate of row 4 (same composite key)
		noPort,       // row 6: E002 port_name empty
		noIP,         // row 7: E002 device_ip empty
	}
	path := writeWorkbookFile(t, "Sheet1", rows)

	a, err := svc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// The real sheet name carries no type hint: the suggested mapping skips
	// it and the user confirms the port mapping manually.
	if a.SuggestedMapping.Sheets[0].Action != mapping.ActionSkip {
		t.Fatalf("suggested action for Sheet1 = %q, want skip (no type hint)", a.SuggestedMapping.Sheets[0].Action)
	}
	sum, err := svc.Execute(ctx, a.BatchID, portMapping("Sheet1"))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if sum.Status != domain.BatchStatusCompleted ||
		sum.RowsTotal != 6 || sum.RowsImported != 2 || sum.RowsSkipped != 1 || sum.RowsFailed != 3 {
		t.Fatalf("summary = %+v, want completed 6 total / 2 imported / 1 skipped / 3 failed", sum)
	}

	// Row results: row 3 is the repeated header row.
	rr, _, err := db.RowResults.List(ctx, repo.RowResultFilter{BatchID: a.BatchID})
	if err != nil || len(rr) != 6 {
		t.Fatalf("row results = %d (err %v), want 6", len(rr), err)
	}
	byRow := map[int]domain.RowResult{}
	for _, r := range rr {
		byRow[r.RowNumber] = r
	}
	headerRow := byRow[3]
	if headerRow.Status != domain.RowStatusSkipped || headerRow.RuleCode != "" ||
		!strings.Contains(headerRow.Message, "重复表头") {
		t.Errorf("repeated header row result = %+v, want skipped without rule code and 重复表头 message", headerRow)
	}
	if headerRow.EntityID != "" {
		t.Errorf("repeated header row entity id = %q, want empty", headerRow.EntityID)
	}
	if byRow[2].Status != domain.RowStatusImported || byRow[4].Status != domain.RowStatusImported {
		t.Errorf("data rows 2/4 = %s/%s, want imported", byRow[2].Status, byRow[4].Status)
	}
	if byRow[5].Status != domain.RowStatusFailed || byRow[5].RuleCode != domain.RuleDuplicateKey {
		t.Errorf("row 5 = %s/%s, want failed E001", byRow[5].Status, byRow[5].RuleCode)
	}
	if byRow[6].Status != domain.RowStatusFailed || byRow[6].RuleCode != domain.RuleMissingRequiredField {
		t.Errorf("row 6 = %s/%s, want failed E002", byRow[6].Status, byRow[6].RuleCode)
	}
	if byRow[7].Status != domain.RowStatusFailed || byRow[7].RuleCode != domain.RuleMissingRequiredField {
		t.Errorf("row 7 = %s/%s, want failed E002", byRow[7].Status, byRow[7].RuleCode)
	}

	// The repeated header row records NO issue; E001/E002 evidence carries
	// the composite key.
	issues := batchIssues(t, db, a.BatchID)
	if len(issues) != 3 {
		t.Fatalf("batch issues = %d, want 3 (E001 + 2x E002, header row records nothing)", len(issues))
	}
	var e001 domain.Issue
	e002ByRow := map[string]domain.Issue{}
	for _, iss := range issues {
		switch iss.RuleCode {
		case domain.RuleDuplicateKey:
			e001 = iss
		case domain.RuleMissingRequiredField:
			e002ByRow[iss.Evidence["row_number"]] = iss
		}
	}
	if e001.ID == "" || len(e002ByRow) != 2 {
		t.Fatalf("missing E001/E002 issues: %+v", issues)
	}
	wantKey := "道西30米1|xgei-1/1/0/2.186"
	if e001.Evidence["natural_key"] != wantKey || e001.Evidence["row_number"] != "5" ||
		e001.Evidence["conflicting_row_number"] != "4" || e001.Evidence["type"] != "port" {
		t.Errorf("E001 evidence = %v, want key %q conflicting row 4", e001.Evidence, wantKey)
	}
	// E002 reports the empty required field BY NAME: port_name at row 6,
	// device_ip at row 7 (a value column since migration v8, still
	// required — the real sheets always carry the device IP).
	if e := e002ByRow["6"]; e.Evidence["field"] != "port_name" {
		t.Errorf("E002(row 6) field = %q, want port_name", e.Evidence["field"])
	}
	if e := e002ByRow["7"]; e.Evidence["field"] != "device_ip" {
		t.Errorf("E002(row 7) field = %q, want device_ip", e.Evidence["field"])
	}

	// Port resources: joined natural key, name from port_name (the display
	// override — device_name is the key's scope component), attributes
	// carry the non-key fields INCLUDING device_ip as a value column
	// (bandwidth 0 stored; the two key fields never enter attrs).
	res1, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypePort, "B2|bvi2.1008")
	if err != nil {
		t.Fatalf("GetByTypeKey(port): %v", err)
	}
	if res1.Type != domain.ResourceTypePort || res1.Name != "bvi2.1008" {
		t.Errorf("port resource = %s/%q, want port/bvi2.1008", res1.Type, res1.Name)
	}
	for field, want := range map[string]string{
		"device_ip": "12.1.53.24", "port_type": "业务口", "port_desc": "BBU_CT_1333", "bandwidth_mbps": "0",
		"vendor": "中兴", "model": "9000-3E", "record_date": "2026-10-09",
	} {
		if res1.Attributes[field] != want {
			t.Errorf("port attrs[%s] = %q, want %q", field, res1.Attributes[field], want)
		}
	}
	if _, isKey := res1.Attributes["device_name"]; isKey {
		t.Errorf("port attrs must not contain the key field device_name: %v", res1.Attributes)
	}
	if _, isKey := res1.Attributes["port_name"]; isKey {
		t.Errorf("port attrs must not contain the key field port_name: %v", res1.Attributes)
	}
	if n, _ := db.Resources.CountByType(ctx); n[domain.ResourceTypePort] != 2 {
		t.Errorf("port resources = %d, want 2 (bandwidth 0 imports)", n[domain.ResourceTypePort])
	}

	// Idempotent re-import: the same workbook skips the two existing rows,
	// skips the header row again, and repeats the three deterministic
	// failures.
	a2, err := svc.Analyze(ctx, path)
	if err != nil {
		t.Fatalf("Analyze 2: %v", err)
	}
	sum2, err := svc.Execute(ctx, a2.BatchID, portMapping("Sheet1"))
	if err != nil {
		t.Fatalf("Execute 2: %v", err)
	}
	if sum2.RowsImported != 0 || sum2.RowsSkipped != 3 || sum2.RowsFailed != 3 || sum2.RowsTotal != 6 {
		t.Fatalf("run 2 = %d/%d/%d/%d, want 0/3/3/6 (imported/skipped/failed/total)",
			sum2.RowsImported, sum2.RowsSkipped, sum2.RowsFailed, sum2.RowsTotal)
	}

	// Single-key regression: a device import in the same database keeps the
	// bare device_code natural key (the composite join must not leak a "|").
	_, dSum := importWorkbook(t, svc, "devices.xlsx")
	if dSum.RowsImported != 27 {
		t.Fatalf("device import regression: imported = %d, want 27", dSum.RowsImported)
	}
	dev, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-001")
	if err != nil {
		t.Fatalf("GetByTypeKey(device, DEV-001): %v", err)
	}
	if dev.NaturalKey != "DEV-001" || strings.Contains(dev.NaturalKey, "|") {
		t.Errorf("device natural key = %q, want bare DEV-001 (single-key join is a no-op)", dev.NaturalKey)
	}
}

// TestRecoverInterruptedBatches pins the R1-D6 crash-window self-heal:
// batches stuck in the transient pipeline states (analyzing, importing)
// are reset to failed with the interrupted reason; the stable
// awaiting_mapping state, terminal batches and other batches' imports are
// untouched; a second run heals nothing. It also pins the tolerated
// false-positive semantics: an import that finishes AFTER a heal
// overwrites the healed failed status with its real terminal status.
func TestRecoverInterruptedBatches(t *testing.T) {
	svc, db := newService(t)
	ctx := context.Background()
	aDev, _ := importWorkbook(t, svc, "devices.xlsx") // completed (terminal)

	ts := time.Date(2026, 10, 9, 9, 30, 0, 0, time.UTC)
	batches := map[string]domain.BatchStatus{
		"bat_stuck_imp": domain.BatchStatusImporting,
		"bat_stuck_ana": domain.BatchStatusAnalyzing,
		"bat_waiting":   domain.BatchStatusAwaitingMapping,
		"bat_failed":    domain.BatchStatusFailed,
		"bat_rolled":    domain.BatchStatusRolledBack,
	}
	for id, status := range batches {
		b := domain.ImportBatch{
			ID:             id,
			WorkbookName:   id + ".xlsx",
			WorkbookSHA256: "sha_" + id,
			Status:         status,
			RuleVersion:    domain.RuleVersion,
			StartedAt:      ts,
		}
		if err := db.Batches.Create(ctx, b); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	n, err := svc.RecoverInterrupted(ctx)
	if err != nil || n != 2 {
		t.Fatalf("RecoverInterrupted = %d, %v; want 2 (analyzing + importing)", n, err)
	}
	for _, id := range []string{"bat_stuck_imp", "bat_stuck_ana"} {
		b, err := db.Batches.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get %s after heal: %v", id, err)
		}
		if b.Status != domain.BatchStatusFailed || b.FinishedAt == nil {
			t.Errorf("%s after heal = %s (finishedAt %v), want failed with finished_at", id, b.Status, b.FinishedAt)
		}
		if !strings.Contains(b.Error, "interrupted") {
			t.Errorf("%s error after heal = %q, want the interrupted reason", id, b.Error)
		}
	}
	// The stable user-input state and terminal states keep their status.
	for _, id := range []string{"bat_waiting", "bat_failed", "bat_rolled"} {
		b, err := db.Batches.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get %s after heal: %v", id, err)
		}
		if b.Status != batches[id] {
			t.Errorf("%s after heal = %s, want untouched %s", id, b.Status, batches[id])
		}
		if b.FinishedAt != nil && id == "bat_waiting" {
			t.Errorf("%s got finished_at stamped by the heal", id)
		}
	}
	bDev, err := db.Batches.Get(ctx, aDev.BatchID)
	if err != nil || bDev.Status != domain.BatchStatusCompleted {
		t.Errorf("imported devices batch after heal = %s (err %v), want completed", bDev.Status, err)
	}

	// Idempotent: the transient set is empty now.
	if n, err := svc.RecoverInterrupted(ctx); err != nil || n != 0 {
		t.Fatalf("second RecoverInterrupted = %d, %v; want 0", n, err)
	}

	// Late terminal write wins over a heal (tolerated false positive: the
	// import of another process was in flight and completes afterwards —
	// execute.go writes its terminal status inside the import transaction).
	if err := db.Batches.UpdateStatus(ctx, "bat_stuck_imp", domain.BatchStatusCompleted); err != nil {
		t.Fatalf("late terminal write: %v", err)
	}
	bLate, err := db.Batches.Get(ctx, "bat_stuck_imp")
	if err != nil || bLate.Status != domain.BatchStatusCompleted {
		t.Errorf("late terminal write: status = %s (err %v), want completed", bLate.Status, err)
	}
}
