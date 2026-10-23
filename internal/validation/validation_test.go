package validation

import (
	"context"
	"encoding/json"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/importer"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testfixtures"
)

// testdataPath returns the path of a committed test artifact relative to
// this package's directory.
func testdataPath(name string) string {
	return filepath.Join("..", "..", "testdata", name)
}

// importOrder is the canonical import order of the three workbooks (refs
// resolve against earlier batches): devices → ippools → circuits.
var importOrder = []string{"devices.xlsx", "ippools.xlsx", "circuits.xlsx"}

// importAll analyzes and executes the three seeded workbooks with their
// suggested mappings against a fresh database and returns the batch id of
// each workbook by file name.
func importAll(t *testing.T, db *repo.DB) map[string]string {
	t.Helper()
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
	return batches
}

// newEngine opens a fresh database, imports the three seeded workbooks and
// returns the engine bound to it plus the batch ids.
func newEngine(t *testing.T) (*Engine, *repo.DB, map[string]string) {
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
	batches := importAll(t, db)
	return New(db), db, batches
}

// setLevelIssues lists the set-level issues (batch_id NULL) of the database.
func setLevelIssues(t *testing.T, db *repo.DB) []domain.Issue {
	t.Helper()
	issues, total, err := db.Issues.List(context.Background(), repo.IssueFilter{})
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if total != len(issues) {
		t.Fatalf("issues total %d != len %d", total, len(issues))
	}
	out := make([]domain.Issue, 0, len(issues))
	for _, iss := range issues {
		if iss.BatchID == nil {
			out = append(out, iss)
		}
	}
	return out
}

// TestSetLevelRules imports the three seeded workbooks into a fresh database
// and checks the engine's recompute: exactly one E005 (the POOL-15/POOL-16
// overlap), one E006 (DEV-026/DEV-027 share 10.10.10.5) and one I001
// (DEV-030 unlinked); all inserted with batch_id NULL, rule_version 1.0.0
// and the §7 severities, and the (rule_code, entity natural key) ordering
// is deterministic. Row-phase issues stay untouched.
func TestSetLevelRules(t *testing.T) {
	eng, db, _ := newEngine(t)
	ctx := context.Background()

	rowPhaseBefore, _, err := db.Issues.List(ctx, repo.IssueFilter{})
	if err != nil {
		t.Fatalf("list issues before run: %v", err)
	}
	nRowPhaseBefore := 0
	for _, iss := range rowPhaseBefore {
		if iss.BatchID != nil {
			nRowPhaseBefore++
		}
	}
	if nRowPhaseBefore == 0 {
		t.Fatal("imports must record row-phase issues before the engine runs")
	}

	res, err := eng.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Result: nothing purged on the first run, one insert per set rule.
	if res.RuleVersion != domain.RuleVersion {
		t.Errorf("Result.RuleVersion = %q, want %q", res.RuleVersion, domain.RuleVersion)
	}
	if res.Deleted != 0 {
		t.Errorf("Result.Deleted = %d, want 0 (first run)", res.Deleted)
	}
	if res.Inserted != 3 {
		t.Errorf("Result.Inserted = %d, want 3", res.Inserted)
	}
	wantByRule := map[string]int{
		domain.RuleSubnetOverlap:    1,
		domain.RuleDuplicateAddress: 1,
		domain.RuleUnlinkedDevice:   1,
	}
	if len(res.InsertedByRule) != len(wantByRule) {
		t.Errorf("Result.InsertedByRule = %v, want %v", res.InsertedByRule, wantByRule)
	}
	for code, want := range wantByRule {
		if res.InsertedByRule[code] != want {
			t.Errorf("Result.InsertedByRule[%s] = %d, want %d", code, res.InsertedByRule[code], want)
		}
	}

	issues := setLevelIssues(t, db)
	if len(issues) != 3 {
		t.Fatalf("set-level issues = %d, want 3", len(issues))
	}

	// Common invariants: batch_id NULL, rule_version, severity, entity.
	dev26, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, "DEV-026")
	if err != nil {
		t.Fatalf("DEV-026: %v", err)
	}
	pool15, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeIPPool, "POOL-15")
	if err != nil {
		t.Fatalf("POOL-15: %v", err)
	}
	dev30, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, testfixtures.DeviceUnlinkedCode)
	if err != nil {
		t.Fatalf("DEV-030: %v", err)
	}
	for _, iss := range issues {
		if iss.BatchID != nil {
			t.Errorf("%s: batch_id = %v, want NULL", iss.RuleCode, *iss.BatchID)
		}
		if iss.RuleVersion != domain.RuleVersion {
			t.Errorf("%s: rule_version = %q, want %q", iss.RuleCode, iss.RuleVersion, domain.RuleVersion)
		}
		if sev, ok := domain.SeverityForCode(iss.RuleCode); !ok || iss.Severity != sev {
			t.Errorf("%s: severity = %q, want %q", iss.RuleCode, iss.Severity, sev)
		}
		if iss.EntityKind != domain.EntityKindResource || iss.EntityID == "" {
			t.Errorf("%s: entity = %q/%q, want resource with id", iss.RuleCode, iss.EntityKind, iss.EntityID)
		}
	}

	// Deterministic (rule_code, entity natural key) sequence: E005(POOL-15),
	// E006(DEV-026), I001(DEV-030).
	type entry struct {
		code string
		key  string
	}
	sequence := make([]entry, 0, len(issues))
	byCode := map[string]domain.Issue{}
	for _, iss := range issues {
		var key string
		switch iss.RuleCode {
		case domain.RuleSubnetOverlap:
			key = pool15.NaturalKey
		case domain.RuleDuplicateAddress:
			key = dev26.NaturalKey
		case domain.RuleUnlinkedDevice:
			key = dev30.NaturalKey
		}
		if iss.EntityID != "" {
			if res, err := db.Resources.GetByID(ctx, iss.EntityID); err == nil {
				key = res.NaturalKey
			}
		}
		sequence = append(sequence, entry{code: iss.RuleCode, key: key})
		byCode[iss.RuleCode] = iss
	}
	sort.Slice(sequence, func(i, j int) bool {
		if sequence[i].code != sequence[j].code {
			return sequence[i].code < sequence[j].code
		}
		return sequence[i].key < sequence[j].key
	})
	wantSequence := []entry{
		{domain.RuleSubnetOverlap, "POOL-15"},
		{domain.RuleDuplicateAddress, "DEV-026"},
		{domain.RuleUnlinkedDevice, "DEV-030"},
	}
	for i, want := range wantSequence {
		if sequence[i] != want {
			t.Errorf("canonical sequence[%d] = %+v, want %+v", i, sequence[i], want)
		}
	}

	// E005: the seeded overlap pair with the narrower prefix as intersection.
	e005 := byCode[domain.RuleSubnetOverlap]
	if e005.EntityID != pool15.ID {
		t.Errorf("E005 entity = %s, want POOL-15 (%s)", e005.EntityID, pool15.ID)
	}
	if e005.Evidence["pool_a"] != "POOL-15("+testfixtures.IPPoolOverlapCIDRA+")" ||
		e005.Evidence["pool_b"] != "POOL-16("+testfixtures.IPPoolOverlapCIDRB+")" {
		t.Errorf("E005 evidence = %v", e005.Evidence)
	}
	if e005.Evidence["overlap_cidr"] != testfixtures.IPPoolOverlapCIDRB {
		t.Errorf("E005 overlap_cidr = %q, want %q (the narrower prefix)",
			e005.Evidence["overlap_cidr"], testfixtures.IPPoolOverlapCIDRB)
	}
	if e005.Evidence["pool_a_id"] != pool15.ID || e005.Evidence["pool_b_id"] == "" {
		t.Errorf("E005 pool ids missing: %v", e005.Evidence)
	}
	if !strings.Contains(e005.Message, "POOL-15") || !strings.Contains(e005.Message, "POOL-16") ||
		!strings.Contains(e005.Message, testfixtures.IPPoolOverlapCIDRA) {
		t.Errorf("E005 message = %q, want both pool names and a cidr", e005.Message)
	}

	// E006: the seeded shared management IP of DEV-026/DEV-027.
	e006 := byCode[domain.RuleDuplicateAddress]
	if e006.EntityID != dev26.ID {
		t.Errorf("E006 entity = %s, want DEV-026 (%s)", e006.EntityID, dev26.ID)
	}
	if e006.Evidence["address"] != testfixtures.DeviceSharedMgmtIP {
		t.Errorf("E006 address = %q, want %q", e006.Evidence["address"], testfixtures.DeviceSharedMgmtIP)
	}
	wantResources := "DEV-026" + "=" + testfixtures.DeviceSharedMgmtIP +
		", " + "DEV-027" + "=" + testfixtures.DeviceSharedMgmtIP
	if e006.Evidence["resources"] != wantResources {
		t.Errorf("E006 resources = %q, want %q", e006.Evidence["resources"], wantResources)
	}
	if !strings.Contains(e006.Message, testfixtures.DeviceSharedMgmtIP) ||
		!strings.Contains(e006.Message, "DEV-026") ||
		!strings.Contains(e006.Message, "DEV-027") {
		t.Errorf("E006 message = %q, want the address and both device codes", e006.Message)
	}

	// I001: DEV-030 is the only device without any relation.
	i001 := byCode[domain.RuleUnlinkedDevice]
	if i001.EntityID != dev30.ID {
		t.Errorf("I001 entity = %s, want DEV-030 (%s)", i001.EntityID, dev30.ID)
	}
	if i001.Evidence["device_code"] != testfixtures.DeviceUnlinkedCode ||
		i001.Evidence["device_name"] != "上海-传输设备-030" || i001.Evidence["device_id"] != dev30.ID {
		t.Errorf("I001 evidence = %v", i001.Evidence)
	}
	if !strings.Contains(i001.Message, testfixtures.DeviceUnlinkedCode) {
		t.Errorf("I001 message = %q, want the device code", i001.Message)
	}

	// Row-phase issues are untouched by the recompute.
	after, _, err := db.Issues.List(ctx, repo.IssueFilter{})
	if err != nil {
		t.Fatalf("list issues after run: %v", err)
	}
	nRowPhaseAfter := 0
	for _, iss := range after {
		if iss.BatchID != nil {
			nRowPhaseAfter++
		}
	}
	if nRowPhaseAfter != nRowPhaseBefore {
		t.Errorf("row-phase issues changed: %d → %d", nRowPhaseBefore, nRowPhaseAfter)
	}
}

// TestSetLevelRecomputeIdempotent runs the engine twice: the second run
// purges the first run's set-level issues, re-inserts the same findings and
// leaves the total unchanged — no duplicates accumulate.
func TestSetLevelRecomputeIdempotent(t *testing.T) {
	eng, db, _ := newEngine(t)
	ctx := context.Background()

	if _, err := eng.Run(ctx); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	first := setLevelIssues(t, db)
	if len(first) != 3 {
		t.Fatalf("run 1: set-level issues = %d, want 3", len(first))
	}

	res2, err := eng.Run(ctx)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if res2.Deleted != 3 {
		t.Errorf("run 2: Deleted = %d, want 3 (the first run's issues)", res2.Deleted)
	}
	if res2.Inserted != 3 {
		t.Errorf("run 2: Inserted = %d, want 3", res2.Inserted)
	}

	second := setLevelIssues(t, db)
	if len(second) != 3 {
		t.Fatalf("run 2: set-level issues = %d, want 3 (no duplicates)", len(second))
	}
	// Business content identical across runs (ids and timestamps differ).
	a := canonicalSetLevel(t, db, first)
	b := canonicalSetLevel(t, db, second)
	if string(a) != string(b) {
		t.Errorf("set-level issues changed across recompute:\n%s\n%s", a, b)
	}
}

// canonicalSetLevel renders the business content of set-level issues
// (everything except id/created_at) as canonical JSON.
func canonicalSetLevel(t *testing.T, db *repo.DB, issues []domain.Issue) []byte {
	t.Helper()
	ctx := context.Background()
	type entry struct {
		RuleCode  string            `json:"ruleCode"`
		Severity  string            `json:"severity"`
		Message   string            `json:"message"`
		EntityKey string            `json:"entityKey"`
		Evidence  map[string]string `json:"evidence"`
	}
	entries := make([]entry, 0, len(issues))
	for _, iss := range issues {
		key := ""
		if res, err := db.Resources.GetByID(ctx, iss.EntityID); err == nil {
			key = res.NaturalKey
		}
		entries = append(entries, entry{
			RuleCode: iss.RuleCode, Severity: iss.Severity.String(), Message: iss.Message,
			EntityKey: key, Evidence: iss.Evidence,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].RuleCode != entries[j].RuleCode {
			return entries[i].RuleCode < entries[j].RuleCode
		}
		return entries[i].EntityKey < entries[j].EntityKey
	})
	b, err := json.MarshalIndent(entries, "", " ")
	if err != nil {
		t.Fatalf("marshal canonical issues: %v", err)
	}
	return b
}

// TestValidationDeterminism is ACC-4: the same input workbooks plus the same
// rule version must produce the same validation results. Two fresh
// databases go through the identical flow (3 imports + Run); the canonical
// projection of ALL issues — {ruleCode, severity, message, entityKey,
// evidence, source} with ids, timestamps and source_ref ids excluded and
// source_ref ids resolved to their workbook/sheet/row evidence — must be
// byte-identical.
func TestValidationDeterminism(t *testing.T) {
	canonicalA := buildCanonicalState(t)
	canonicalB := buildCanonicalState(t)
	if string(canonicalA) != string(canonicalB) {
		t.Errorf("ACC-4: canonical issues differ between two identical runs:\n--- A ---\n%s\n--- B ---\n%s",
			canonicalA, canonicalB)
	}
}

// buildCanonicalState runs the full flow (3 imports + validation Run) on a
// fresh database and renders all issues in the canonical projection as JSON.
func buildCanonicalState(t *testing.T) []byte {
	t.Helper()
	eng, db, _ := newEngine(t)
	ctx := context.Background()
	if _, err := eng.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	issues, _, err := db.Issues.List(ctx, repo.IssueFilter{})
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	resources, _, err := db.Resources.Search(ctx, repo.ResourceSearchFilter{})
	if err != nil {
		t.Fatalf("search resources: %v", err)
	}
	keys := map[string]string{}
	for _, res := range resources {
		keys[res.ID] = res.NaturalKey
	}
	// resolveEvidence copies ev and replaces every value that is a resource
	// id with the resource's natural key — entity ids (existing_entity_id,
	// pool_a_id, pool_b_id, device_id …) are random per database and are
	// resolved to their stable business meaning instead of excluded.
	resolveEvidence := func(ev map[string]string) map[string]string {
		out := make(map[string]string, len(ev))
		for k, v := range ev {
			if key, ok := keys[v]; ok {
				v = key
			}
			out[k] = v
		}
		return out
	}

	type source struct {
		Workbook  string `json:"workbook"`
		Sheet     string `json:"sheet"`
		RowNumber int    `json:"rowNumber"`
	}
	type entry struct {
		RuleCode             string            `json:"ruleCode"`
		Severity             string            `json:"severity"`
		Message              string            `json:"message"`
		EntityKey            string            `json:"entityKey"`
		Evidence             map[string]string `json:"evidence"`
		Source               *source           `json:"source,omitempty"`
		ApplicationGenerated bool              `json:"applicationGenerated"`
	}
	entries := make([]entry, 0, len(issues))
	for _, iss := range issues {
		e := entry{
			RuleCode: iss.RuleCode, Severity: iss.Severity.String(), Message: iss.Message,
			EntityKey: keys[iss.EntityID], Evidence: resolveEvidence(iss.Evidence), ApplicationGenerated: false,
		}
		switch {
		case iss.SourceRefID != nil:
			ref, err := db.SourceRefs.GetByID(ctx, *iss.SourceRefID)
			if err != nil {
				t.Fatalf("source ref %s: %v", *iss.SourceRefID, err)
			}
			e.Source = &source{Workbook: ref.Workbook, Sheet: ref.Sheet, RowNumber: ref.RowNumber}
		case iss.BatchID != nil && iss.Evidence["sheet"] != "" && iss.Evidence["row_number"] != "":
			b, err := db.Batches.Get(ctx, *iss.BatchID)
			if err != nil {
				t.Fatalf("batch %s: %v", *iss.BatchID, err)
			}
			e.Source = &source{Workbook: b.WorkbookName, Sheet: iss.Evidence["sheet"]}
			if n, err := strconv.Atoi(iss.Evidence["row_number"]); err == nil {
				e.Source.RowNumber = n
			}
		default:
			e.ApplicationGenerated = true
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].RuleCode != entries[j].RuleCode {
			return entries[i].RuleCode < entries[j].RuleCode
		}
		if entries[i].Severity != entries[j].Severity {
			return entries[i].Severity < entries[j].Severity
		}
		if entries[i].Message != entries[j].Message {
			return entries[i].Message < entries[j].Message
		}
		return entries[i].EntityKey < entries[j].EntityKey
	})
	b, err := json.MarshalIndent(entries, "", " ")
	if err != nil {
		t.Fatalf("marshal canonical issues: %v", err)
	}
	return b
}
