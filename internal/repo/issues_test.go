package repo

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// seedIssues inserts a mix of issues: 3 row-phase batch issues, 3 set-level
// issues with the set-phase rule codes, and 2 batch-NULL issues with
// non-set-level codes (which DeleteSetLevel must NOT touch).
func seedIssues(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	ts := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	if err := db.Batches.Create(ctx, newBatch("bat_1", ts)); err != nil {
		t.Fatalf("Create batch: %v", err)
	}
	batch := "bat_1"
	issues := []domain.Issue{
		{
			ID: "iss_1", BatchID: &batch, RuleCode: domain.RuleDuplicateKey, RuleVersion: domain.RuleVersion,
			Severity: domain.SeverityError, Message: "duplicate device_code DEV-030",
			Evidence: map[string]string{"sheet": "设备清单", "row_number": "31"}, CreatedAt: ts,
		},
		{
			ID: "iss_2", BatchID: &batch, RuleCode: domain.RuleOrphanRecord, RuleVersion: domain.RuleVersion,
			Severity: domain.SeverityWarning, EntityKind: domain.EntityKindResource, EntityID: "res_1",
			Message: "assigned_device is empty", Evidence: map[string]string{"field": "assigned_device"}, CreatedAt: ts,
		},
		{
			ID: "iss_3", BatchID: &batch, RuleCode: domain.RuleCircuitSelfLoop, RuleVersion: domain.RuleVersion,
			Severity: domain.SeverityWarning, EntityKind: domain.EntityKindResource, EntityID: "res_c",
			Message: "a_end == z_end", Evidence: map[string]string{"circuit_code": "CKT-024"}, CreatedAt: ts,
		},
		// Set-level issues (batch_id NULL) with set-phase codes.
		{
			ID: "iss_4", RuleCode: domain.RuleSubnetOverlap, RuleVersion: domain.RuleVersion,
			Severity: domain.SeverityError, EntityKind: domain.EntityKindResource, EntityID: "res_p1",
			Message:  "10.30.1.0/24 overlaps 10.30.1.128/25",
			Evidence: map[string]string{"pool_a": "POOL-17", "pool_b": "POOL-18"}, CreatedAt: ts,
		},
		{
			ID: "iss_5", RuleCode: domain.RuleDuplicateAddress, RuleVersion: domain.RuleVersion,
			Severity: domain.SeverityError, EntityKind: domain.EntityKindResource, EntityID: "res_d1",
			Message:  "mgmt_ip 10.10.10.5 used by 2 devices",
			Evidence: map[string]string{"address": "10.10.10.5"}, CreatedAt: ts,
		},
		{
			ID: "iss_6", RuleCode: domain.RuleUnlinkedDevice, RuleVersion: domain.RuleVersion,
			Severity: domain.SeverityInfo, EntityKind: domain.EntityKindResource, EntityID: "res_d30",
			Message: "device DEV-030 has no relations", Evidence: map[string]string{"device_code": "DEV-030"}, CreatedAt: ts,
		},
		// Batch-NULL issues with NON-set-level codes: must survive DeleteSetLevel.
		{
			ID: "iss_7", RuleCode: domain.RuleMissingRequiredField, RuleVersion: domain.RuleVersion,
			Severity: domain.SeverityError, Message: "legacy row-phase issue without batch",
			Evidence: map[string]string{"row_number": "14"}, CreatedAt: ts,
		},
		{
			ID: "iss_8", RuleCode: domain.RuleOrphanRecord, RuleVersion: domain.RuleVersion,
			Severity: domain.SeverityWarning, Message: "legacy warning without batch",
			Evidence: map[string]string{}, CreatedAt: ts,
		},
	}
	for _, iss := range issues {
		if err := db.Issues.Insert(ctx, iss); err != nil {
			t.Fatalf("Insert issue %s: %v", iss.ID, err)
		}
	}
}

func TestIssueInsertAndListFilters(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedIssues(t, db)

	// All issues.
	_, total, err := db.Issues.List(ctx, IssueFilter{Limit: 100})
	if err != nil || total != 8 {
		t.Fatalf("List(all) total = %d, %v; want 8", total, err)
	}

	// Filter by batch.
	byBatch, total, err := db.Issues.List(ctx, IssueFilter{BatchID: ptr("bat_1"), Limit: 100})
	if err != nil || total != 3 || len(byBatch) != 3 {
		t.Fatalf("List(batch) = %d/%d, %v; want 3/3", len(byBatch), total, err)
	}
	for _, iss := range byBatch {
		if iss.BatchID == nil || *iss.BatchID != "bat_1" {
			t.Errorf("issue %s has batch %v", iss.ID, iss.BatchID)
		}
	}
	if byBatch[0].RuleCode != domain.RuleDuplicateKey { // ordered by rule_code
		t.Errorf("byBatch[0].RuleCode = %s, want E001", byBatch[0].RuleCode)
	}
	if byBatch[0].Evidence["sheet"] != "设备清单" || byBatch[0].Evidence["row_number"] != "31" {
		t.Errorf("evidence roundtrip mismatch: %v", byBatch[0].Evidence)
	}

	// Filter by severity (global).
	warnings, total, err := db.Issues.List(ctx, IssueFilter{Severity: domain.SeverityWarning, Limit: 100})
	if err != nil || total != 3 || len(warnings) != 3 {
		t.Fatalf("List(warning) = %d/%d, %v; want 3/3", len(warnings), total, err)
	}

	// Filter by rule code.
	overlap, total, err := db.Issues.List(ctx, IssueFilter{RuleCode: domain.RuleSubnetOverlap, Limit: 100})
	if err != nil || total != 1 || len(overlap) != 1 || overlap[0].ID != "iss_4" {
		t.Fatalf("List(E005) = %d/%d %v", len(overlap), total, err)
	}
	if overlap[0].BatchID != nil {
		t.Errorf("set-level issue must have nil BatchID, got %v", *overlap[0].BatchID)
	}
	if overlap[0].Evidence["pool_a"] != "POOL-17" {
		t.Errorf("evidence roundtrip mismatch: %v", overlap[0].Evidence)
	}

	// Combined filter + pagination.
	page, total, err := db.Issues.List(ctx, IssueFilter{Severity: domain.SeverityError, Limit: 2, Offset: 2})
	if err != nil || total != 4 || len(page) != 2 {
		t.Fatalf("List(error, page 2) = %d/%d, %v; want 2/4", len(page), total, err)
	}

	// CountBySeverity global (nil batch): errors=4, warnings=3, info=1.
	global, err := db.Issues.CountBySeverity(ctx, nil)
	if err != nil {
		t.Fatalf("CountBySeverity(nil): %v", err)
	}
	if global[domain.SeverityError] != 4 || global[domain.SeverityWarning] != 3 || global[domain.SeverityInfo] != 1 {
		t.Errorf("global severity counts = %v", global)
	}
	// CountBySeverity scoped to batch: errors=1, warnings=2.
	scoped, err := db.Issues.CountBySeverity(ctx, ptr("bat_1"))
	if err != nil {
		t.Fatalf("CountBySeverity(batch): %v", err)
	}
	if scoped[domain.SeverityError] != 1 || scoped[domain.SeverityWarning] != 2 || scoped[domain.SeverityInfo] != 0 {
		t.Errorf("scoped severity counts = %v", scoped)
	}

	// Invalid severity rejected.
	if _, _, err := db.Issues.List(ctx, IssueFilter{Severity: domain.Severity("fatal")}); err == nil {
		t.Error("List with invalid severity must fail")
	}
}

func TestIssueDeleteSetLevelOnlyDeletesSetCodes(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedIssues(t, db)

	n, err := db.Issues.DeleteSetLevel(ctx)
	if err != nil {
		t.Fatalf("DeleteSetLevel: %v", err)
	}
	if n != 3 {
		t.Errorf("DeleteSetLevel deleted %d, want 3 (E005/E006/I001 with NULL batch)", n)
	}

	remaining, total, err := db.Issues.List(ctx, IssueFilter{Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 5 {
		t.Fatalf("remaining issues = %d, want 5", total)
	}
	for _, iss := range remaining {
		if iss.BatchID == nil && (iss.RuleCode == domain.RuleSubnetOverlap ||
			iss.RuleCode == domain.RuleDuplicateAddress || iss.RuleCode == domain.RuleUnlinkedDevice) {
			t.Errorf("set-level issue %s (%s) survived DeleteSetLevel", iss.ID, iss.RuleCode)
		}
	}

	// Second call is a no-op (idempotent).
	n, err = db.Issues.DeleteSetLevel(ctx)
	if err != nil || n != 0 {
		t.Errorf("second DeleteSetLevel = %d, %v; want 0", n, err)
	}
}

func TestIssueDeleteByBatch(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedIssues(t, db)

	n, err := db.Issues.DeleteByBatch(ctx, "bat_1")
	if err != nil || n != 3 {
		t.Fatalf("DeleteByBatch = %d, %v; want 3", n, err)
	}
	// Set-level and legacy NULL-batch issues survive.
	_, total, err := db.Issues.List(ctx, IssueFilter{Limit: 100})
	if err != nil || total != 5 {
		t.Errorf("issues after DeleteByBatch = %d, %v; want 5", total, err)
	}
	// Deleting a batch with no issues returns 0.
	n, err = db.Issues.DeleteByBatch(ctx, "bat_none")
	if err != nil || n != 0 {
		t.Errorf("DeleteByBatch(missing) = %d, %v; want 0", n, err)
	}
}

// ptr is a tiny helper for *string literals.
func ptr(s string) *string { return &s }

// TestIssueCountByBatchMatchesList pins the COUNT aggregate used by the
// rollback route's delete-response accounting (R2-P3-2): CountByBatch must
// equal the number of row-phase issues a List with the same batch filter
// would return (set-level issues excluded on both sides).
func TestIssueCountByBatchMatchesList(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedIssues(t, db)

	batch := "bat_1"
	n, err := db.Issues.CountByBatch(ctx, batch)
	if err != nil {
		t.Fatalf("CountByBatch: %v", err)
	}
	listed, total, err := db.Issues.List(ctx, IssueFilter{BatchID: &batch})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if n != len(listed) || n != total || n != 3 {
		t.Errorf("CountByBatch = %d, List = %d rows / total %d, want all 3", n, len(listed), total)
	}
	if n, err := db.Issues.CountByBatch(ctx, "bat_none"); err != nil || n != 0 {
		t.Errorf("CountByBatch(missing batch) = %d, %v; want 0", n, err)
	}
}

// TestIssueCountByRule pins the W14-D COUNT aggregate: real SQL GROUP BY
// counts per rule_code with the deterministic (count DESC, rule_code ASC)
// order and the limit cap. The seedIssues mix yields W001 x2 plus seven
// singletons (E001/E002/E005/E006/I001/W003); limit <= 0 returns all.
func TestIssueCountByRule(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Empty table: empty (non-nil) slice.
	got, err := db.Issues.CountByRule(ctx, 0)
	if err != nil {
		t.Fatalf("CountByRule on empty: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("empty table by-rule = %#v, want empty non-nil slice", got)
	}

	seedIssues(t, db)

	got, err = db.Issues.CountByRule(ctx, 0)
	if err != nil {
		t.Fatalf("CountByRule: %v", err)
	}
	want := []IssueRuleCount{
		{RuleCode: "W001", Count: 2},
		{RuleCode: "E001", Count: 1},
		{RuleCode: "E002", Count: 1},
		{RuleCode: "E005", Count: 1},
		{RuleCode: "E006", Count: 1},
		{RuleCode: "I001", Count: 1},
		{RuleCode: "W003", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("by-rule = %v\nwant    %v (count DESC, code ASC)", got, want)
	}

	// The limit cap keeps the head of the same order.
	capped, err := db.Issues.CountByRule(ctx, 3)
	if err != nil {
		t.Fatalf("CountByRule limit=3: %v", err)
	}
	if len(capped) != 3 || capped[0] != want[0] || capped[1] != want[1] || capped[2] != want[2] {
		t.Errorf("capped = %v, want the first 3 of %v", capped, want)
	}
}
