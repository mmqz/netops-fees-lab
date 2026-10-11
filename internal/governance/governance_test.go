package governance_test

// Governance summary tests over a REAL SQLite database seeded through the
// repository layer (no mocks): a dirty database walks every block of the
// shape — the two confirmation queues with all three lifecycle states each,
// a multi-rule issue distribution (including the tie-break and the
// TopRuleLimit cap), multi-type resources, relations and a rolled-back
// batch — and asserts the aggregate field by field. The empty database
// answers the all-zero structure, and two summaries of the same database
// are field-by-field equal except generatedAt (the documented sole
// non-deterministic field).

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/governance"
	"github.com/mmqz/netops-lab/internal/repo"
)

var govTS = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

func newGovernanceDB(t *testing.T) *repo.DB {
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
	return db
}

func govBatch(id string) domain.ImportBatch {
	return domain.ImportBatch{ID: id, WorkbookName: id + ".xlsx", Status: domain.BatchStatusCompleted, StartedAt: govTS}
}

func govDevice(id, key, batchID string) domain.Resource {
	return domain.Resource{
		ID: id, Type: domain.ResourceTypeDevice, NaturalKey: key, Name: key,
		Attributes:   map[string]string{"mgmt_ip": "10.0.0.1"},
		FirstBatchID: batchID, CreatedAt: govTS, UpdatedAt: govTS,
	}
}

func govPool(id, key, batchID string) domain.Resource {
	return domain.Resource{
		ID: id, Type: domain.ResourceTypeIPPool, NaturalKey: key, Name: key,
		Attributes:   map[string]string{"cidr": "10.0.0.0/24"},
		FirstBatchID: batchID, CreatedAt: govTS, UpdatedAt: govTS,
	}
}

func govChange(id string, status domain.ChangeStatus) domain.AttributeChange {
	ch := domain.AttributeChange{
		ID: id, ResourceID: "res_g_dev1", BatchID: "bat_g1",
		Field: "mgmt_ip", OldValue: "10.0.0.1", NewValue: "10.0.0.9",
		Status: status, Sheet: "设备清单", RowNumber: 2, CreatedAt: govTS,
	}
	if status == domain.ChangeStatusApplied {
		applied := govTS.Add(time.Hour)
		ch.AppliedAt = &applied
	}
	return ch
}

func govRename(id string, status domain.RenameStatus) domain.RenameProposal {
	rp := domain.RenameProposal{
		ID: id, ResourceID: "res_g_old", NewResourceID: "res_g_new", BatchID: "bat_g1",
		ResourceType: domain.ResourceTypeIPPool, OldKey: "POOL-1", NewKey: "POOL-1X",
		MatchedFields: []string{"cidr"}, Status: status, Sheet: "地址池", RowNumber: 3,
		CreatedAt: govTS,
	}
	if status == domain.RenameStatusApplied || status == domain.RenameStatusRejected {
		decided := govTS.Add(2 * time.Hour)
		rp.DecidedAt = &decided
	}
	return rp
}

func govIssue(id, rule string, sev domain.Severity) domain.Issue {
	return domain.Issue{
		ID: id, RuleCode: rule, RuleVersion: domain.RuleVersion, Severity: sev,
		Message: "seed " + id, Evidence: map[string]string{}, CreatedAt: govTS,
	}
}

// seedDirty populates every block the summary aggregates:
//
//   - batches:        2 completed + 1 rolled_back
//   - resources:      3 device + 1 ip_pool
//   - relations:      2 assigned_to (pool → device)
//   - attr changes:   2 pending + 1 applied + 1 dismissed
//   - rename props:   1 pending + 1 applied + 1 rejected
//   - issues:         E001×3 + E002×1 (error), W001×2 + W005×2 (warning), I001×1 (info)
//
// The byRule expectation of the dirty test below is derived from exactly
// this seed: [E001:3, W001:2, W005:2, E002:1, I001:1] (count DESC, code ASC).
func seedDirty(t *testing.T, db *repo.DB) {
	t.Helper()
	ctx := context.Background()
	err := db.Do(ctx, func(tx *repo.Tx) error {
		for _, b := range []domain.ImportBatch{govBatch("bat_g1"), govBatch("bat_g2")} {
			if err := tx.Batches.Create(ctx, b); err != nil {
				return err
			}
		}
		rb := govBatch("bat_g3")
		rb.Status = domain.BatchStatusRolledBack
		if err := tx.Batches.Create(ctx, rb); err != nil {
			return err
		}
		// res_g_old / res_g_new feed the rename proposal's two FK sides.
		for _, res := range []domain.Resource{
			govDevice("res_g_dev1", "DEV-G1", "bat_g1"),
			govDevice("res_g_dev2", "DEV-G2", "bat_g1"),
			govDevice("res_g_dev3", "DEV-G3", "bat_g2"),
			govPool("res_g_pool1", "POOL-G1", "bat_g2"),
			govPool("res_g_old", "POOL-G2", "bat_g1"),
			govPool("res_g_new", "POOL-G2X", "bat_g2"),
		} {
			if err := tx.Resources.Insert(ctx, res); err != nil {
				return err
			}
		}
		for i, rel := range []domain.Relation{
			{ID: "rel_g1", Type: domain.RelationTypeAssignedTo, FromResourceID: "res_g_pool1", ToResourceID: "res_g_dev1", FirstBatchID: "bat_g2", CreatedAt: govTS},
			{ID: "rel_g2", Type: domain.RelationTypeAssignedTo, FromResourceID: "res_g_pool1", ToResourceID: "res_g_dev2", FirstBatchID: "bat_g2", CreatedAt: govTS},
		} {
			_ = i
			if err := tx.Relations.Insert(ctx, rel); err != nil {
				return err
			}
		}
		// InsertBatch only accepts pending rows; the applied/dismissed
		// audit entries are produced through the same guarded transitions
		// the confirmation service uses (MarkApplied / MarkDismissed).
		if err := tx.AttributeChanges.InsertBatch(ctx, []domain.AttributeChange{
			govChange("chg_p1", domain.ChangeStatusPending),
			govChange("chg_p2", domain.ChangeStatusPending),
			govChange("chg_a1", domain.ChangeStatusPending),
			govChange("chg_d1", domain.ChangeStatusPending),
		}); err != nil {
			return err
		}
		if err := tx.AttributeChanges.MarkApplied(ctx, "chg_a1", govTS.Add(time.Hour)); err != nil {
			return err
		}
		if err := tx.AttributeChanges.MarkDismissed(ctx, "chg_d1"); err != nil {
			return err
		}
		// Same discipline on the rename side: Insert creates pending
		// proposals, MarkApplied/MarkRejected produce the decided states.
		for _, rp := range []domain.RenameProposal{
			govRename("rp_p1", domain.RenameStatusPending),
			govRename("rp_a1", domain.RenameStatusPending),
			govRename("rp_r1", domain.RenameStatusPending),
		} {
			if err := tx.RenameProposals.Insert(ctx, rp); err != nil {
				return err
			}
		}
		if err := tx.RenameProposals.MarkApplied(ctx, "rp_a1", govTS.Add(2*time.Hour)); err != nil {
			return err
		}
		if err := tx.RenameProposals.MarkRejected(ctx, "rp_r1", govTS.Add(2*time.Hour)); err != nil {
			return err
		}
		issues := []domain.Issue{
			govIssue("iss_e1", domain.RuleDuplicateKey, domain.SeverityError),
			govIssue("iss_e2", domain.RuleDuplicateKey, domain.SeverityError),
			govIssue("iss_e3", domain.RuleDuplicateKey, domain.SeverityError),
			govIssue("iss_e4", domain.RuleMissingRequiredField, domain.SeverityError),
			govIssue("iss_w1", domain.RuleOrphanRecord, domain.SeverityWarning),
			govIssue("iss_w2", domain.RuleOrphanRecord, domain.SeverityWarning),
			govIssue("iss_w3", domain.RuleAttributeChange, domain.SeverityWarning),
			govIssue("iss_w4", domain.RuleAttributeChange, domain.SeverityWarning),
			govIssue("iss_i1", domain.RuleUnlinkedDevice, domain.SeverityInfo),
		}
		for _, iss := range issues {
			if err := tx.Issues.Insert(ctx, iss); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestGovernanceSummaryDirtyDB walks every field of the aggregate over the
// dirty seed (see seedDirty for the exact expectation arithmetic).
func TestGovernanceSummaryDirtyDB(t *testing.T) {
	db := newGovernanceDB(t)
	seedDirty(t, db)
	svc := governance.New(db)

	got, err := svc.Summary(context.Background())
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}

	if got.Pending != (governance.PendingCounts{AttributeChanges: 2, RenameProposals: 1}) {
		t.Errorf("pending = %+v, want {attributeChanges:2 renameProposals:1}", got.Pending)
	}
	if want := map[string]int{"applied": 1, "dismissed": 1}; !reflect.DeepEqual(got.Decided.AttributeChanges, want) {
		t.Errorf("decided.attributeChanges = %v, want %v", got.Decided.AttributeChanges, want)
	}
	if want := map[string]int{"applied": 1, "rejected": 1}; !reflect.DeepEqual(got.Decided.RenameProposals, want) {
		t.Errorf("decided.renameProposals = %v, want %v", got.Decided.RenameProposals, want)
	}

	if want := map[string]int{"error": 4, "warning": 4, "info": 1}; !reflect.DeepEqual(got.Issues.BySeverity, want) {
		t.Errorf("issues.bySeverity = %v, want %v", got.Issues.BySeverity, want)
	}
	wantRule := []governance.RuleCount{
		{Rule: "E001", Count: 3},
		{Rule: "W001", Count: 2},
		{Rule: "W005", Count: 2},
		{Rule: "E002", Count: 1},
		{Rule: "I001", Count: 1},
	}
	if !reflect.DeepEqual(got.Issues.ByRule, wantRule) {
		t.Errorf("issues.byRule = %+v, want %+v (count DESC, code ASC)", got.Issues.ByRule, wantRule)
	}

	if got.Resources.Total != 6 || !reflect.DeepEqual(got.Resources.ByType, map[string]int{"device": 3, "ip_pool": 3}) {
		t.Errorf("resources = %+v, want total 6 {device:3 ip_pool:3}", got.Resources)
	}
	if got.Relations.Total != 2 || !reflect.DeepEqual(got.Relations.ByType, map[string]int{"assigned_to": 2}) {
		t.Errorf("relations = %+v, want total 2 {assigned_to:2}", got.Relations)
	}
	if got.Batches.Total != 3 || !reflect.DeepEqual(got.Batches.ByStatus, map[string]int{"completed": 2, "rolled_back": 1}) {
		t.Errorf("batches = %+v, want total 3 {completed:2 rolled_back:1}", got.Batches)
	}
	if got.GeneratedAt.IsZero() {
		t.Error("generatedAt is zero — it must be a wall-clock stamp")
	}
}

// TestGovernanceSummaryEmptyDB pins the empty-database contract: the
// all-zero structure (scalars 0, fixed map keys present at 0, dynamic maps
// present but empty, byRule an empty non-nil list) — never an error.
func TestGovernanceSummaryEmptyDB(t *testing.T) {
	db := newGovernanceDB(t)
	got, err := governance.New(db).Summary(context.Background())
	if err != nil {
		t.Fatalf("Summary on empty db: %v", err)
	}
	if got.Pending != (governance.PendingCounts{}) {
		t.Errorf("pending = %+v, want zero", got.Pending)
	}
	if want := map[string]int{"applied": 0, "dismissed": 0}; !reflect.DeepEqual(got.Decided.AttributeChanges, want) {
		t.Errorf("decided.attributeChanges = %v, want %v", got.Decided.AttributeChanges, want)
	}
	if want := map[string]int{"applied": 0, "rejected": 0}; !reflect.DeepEqual(got.Decided.RenameProposals, want) {
		t.Errorf("decided.renameProposals = %v, want %v", got.Decided.RenameProposals, want)
	}
	if want := map[string]int{"error": 0, "warning": 0, "info": 0}; !reflect.DeepEqual(got.Issues.BySeverity, want) {
		t.Errorf("issues.bySeverity = %v, want the three zeros", got.Issues.BySeverity)
	}
	if got.Issues.ByRule == nil || len(got.Issues.ByRule) != 0 {
		t.Errorf("issues.byRule = %#v, want empty non-nil list", got.Issues.ByRule)
	}
	if got.Resources.Total != 0 || got.Resources.ByType == nil || len(got.Resources.ByType) != 0 {
		t.Errorf("resources = %+v, want total 0 with empty non-nil map", got.Resources)
	}
	if got.Relations.Total != 0 || got.Relations.ByType == nil || len(got.Relations.ByType) != 0 {
		t.Errorf("relations = %+v, want total 0 with empty non-nil map", got.Relations)
	}
	if got.Batches.Total != 0 || got.Batches.ByStatus == nil || len(got.Batches.ByStatus) != 0 {
		t.Errorf("batches = %+v, want total 0 with empty non-nil map", got.Batches)
	}
}

// TestGovernanceSummaryDeterministicExceptGeneratedAt pins the determinism
// claim: two summaries of the same database agree field by field (the maps
// included — Go's encoding/json sorts map keys, so even the serialized form
// is identical); generatedAt is the documented exception.
func TestGovernanceSummaryDeterministicExceptGeneratedAt(t *testing.T) {
	db := newGovernanceDB(t)
	seedDirty(t, db)
	svc := governance.New(db)

	a, err := svc.Summary(context.Background())
	if err != nil {
		t.Fatalf("first Summary: %v", err)
	}
	b, err := svc.Summary(context.Background())
	if err != nil {
		t.Fatalf("second Summary: %v", err)
	}

	if !reflect.DeepEqual(a.Pending, b.Pending) || !reflect.DeepEqual(a.Decided, b.Decided) ||
		!reflect.DeepEqual(a.Issues, b.Issues) || !reflect.DeepEqual(a.Resources, b.Resources) ||
		!reflect.DeepEqual(a.Relations, b.Relations) || !reflect.DeepEqual(a.Batches, b.Batches) {
		t.Fatalf("two summaries of the same database disagree outside generatedAt:\n%+v\n%+v", a, b)
	}

	// The serialized form of the six data blocks is byte-identical (map keys
	// are sorted by encoding/json — the JSON output is deterministic too).
	block := func(s governance.Summary) []byte {
		data, err := json.Marshal(struct {
			Pending   governance.PendingCounts    `json:"pending"`
			Decided   governance.DecidedCounts    `json:"decided"`
			Issues    governance.IssuesSummary    `json:"issues"`
			Resources governance.ResourcesSummary `json:"resources"`
			Relations governance.RelationsSummary `json:"relations"`
			Batches   governance.BatchesSummary   `json:"batches"`
		}{s.Pending, s.Decided, s.Issues, s.Resources, s.Relations, s.Batches})
		if err != nil {
			t.Fatalf("marshal block: %v", err)
		}
		return data
	}
	if string(block(a)) != string(block(b)) {
		t.Errorf("serialized data blocks differ:\n%s\n%s", block(a), block(b))
	}

	// generatedAt is a live stamp: present on both, and the determinism
	// contract deliberately does not pin its value.
	if a.GeneratedAt.IsZero() || b.GeneratedAt.IsZero() {
		t.Error("generatedAt missing on one of the summaries")
	}
}

// TestGovernanceSummaryByRuleCapAndTiebreak pins the TopRuleLimit cap and
// the deterministic ordering at the boundary: 12 rule codes with counts
// 5,4,4,3,3,3,2,2,2,1,1,1 — the top 10 keep (count DESC, code ASC), so the
// count-1 group contributes exactly its alphabetically first member (E005)
// while W005/W006 (same count, later codes) are cut by the cap.
func TestGovernanceSummaryByRuleCapAndTiebreak(t *testing.T) {
	db := newGovernanceDB(t)
	ctx := context.Background()
	if err := db.Batches.Create(ctx, govBatch("bat_g_cap")); err != nil {
		t.Fatalf("Create batch: %v", err)
	}

	// counts per code, keyed by position in domain.AllRuleCodes():
	// E001:5, E002:3, E003:3, E004:2, E005:1, E006:0, W001:4, W002:4,
	// W003:3, W004:2, W005:1, W006:1, I001:2  (E006 seeded zero times).
	counts := []int{5, 3, 3, 2, 1, 0, 4, 4, 3, 2, 1, 1, 2}
	n := 0
	for i, code := range domain.AllRuleCodes() {
		for range counts[i] {
			n++
			if err := db.Issues.Insert(ctx, govIssue(
				"iss_cap_"+string(rune('a'+n)), code, domain.SeverityWarning)); err != nil {
				t.Fatalf("Insert issue %d: %v", n, err)
			}
		}
	}

	got, err := governance.New(db).Summary(ctx)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if len(got.Issues.ByRule) != governance.TopRuleLimit {
		t.Fatalf("byRule length = %d, want the cap %d", len(got.Issues.ByRule), governance.TopRuleLimit)
	}
	want := []governance.RuleCount{
		{Rule: "E001", Count: 5},
		{Rule: "W001", Count: 4},
		{Rule: "W002", Count: 4},
		{Rule: "E002", Count: 3},
		{Rule: "E003", Count: 3},
		{Rule: "W003", Count: 3},
		{Rule: "E004", Count: 2},
		{Rule: "I001", Count: 2},
		{Rule: "W004", Count: 2},
		{Rule: "E005", Count: 1},
	}
	if !reflect.DeepEqual(got.Issues.ByRule, want) {
		t.Errorf("byRule = %+v\nwant      %+v\ncap cut W005/W006 (count 1, later codes) and keeps E005 (same count, earlier code)",
			got.Issues.ByRule, want)
	}
}
