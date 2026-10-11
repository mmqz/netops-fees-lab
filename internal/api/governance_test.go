package api_test

// W14-D governance summary endpoint tests: the empty-database contract
// (200 + the all-zero structure — never 204/404), the no-store cache
// discipline shared by every /api response, and the full shape over a dirty
// database seeded through the repository layer (all three lifecycle states
// of both confirmation queues, a multi-rule issue mix, multi-type
// resources, relations and a rolled-back batch) — decoded field by field
// into the mirror struct below, so a field-name regression fails to compile.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// governanceSummary mirrors GET /api/governance/summary (§9 shape, W14-D).
type governanceSummary struct {
	Pending struct {
		AttributeChanges int `json:"attributeChanges"`
		RenameProposals  int `json:"renameProposals"`
	} `json:"pending"`
	Decided struct {
		AttributeChanges map[string]int `json:"attributeChanges"`
		RenameProposals  map[string]int `json:"renameProposals"`
	} `json:"decided"`
	Issues struct {
		BySeverity map[string]int `json:"bySeverity"`
		ByRule     []struct {
			Rule  string `json:"rule"`
			Count int    `json:"count"`
		} `json:"byRule"`
	} `json:"issues"`
	Resources struct {
		Total  int            `json:"total"`
		ByType map[string]int `json:"byType"`
	} `json:"resources"`
	Relations struct {
		Total  int            `json:"total"`
		ByType map[string]int `json:"byType"`
	} `json:"relations"`
	Batches struct {
		Total    int            `json:"total"`
		ByStatus map[string]int `json:"byStatus"`
	} `json:"batches"`
	GeneratedAt time.Time `json:"generatedAt"`
}

// getGovernanceSummary GETs the summary route and decodes its body.
func getGovernanceSummary(t *testing.T, srv *httptest.Server) (*http.Response, governanceSummary) {
	t.Helper()
	resp, body := doRequest(t, srv, http.MethodGet, "/api/governance/summary", nil, "")
	var sum governanceSummary
	decodeJSON(t, "governance summary", body, &sum)
	return resp, sum
}

// seedAPIGovernance populates one dirty database through the repository in
// a single transaction: 2 completed batches + 1 rolled back, 2 devices + 2
// ip_pools, 1 assigned_to relation, attribute changes 2 pending / 1 applied
// / 1 dismissed, rename proposals 1 pending / 1 applied / 1 rejected and a
// five-rule issue mix (E001 x2, W001 x2, E002, I001 — severities 2E/2W/1I).
func seedAPIGovernance(t *testing.T, db *repo.DB, ts time.Time) {
	t.Helper()
	ctx := context.Background()
	batch := func(id string, status domain.BatchStatus) domain.ImportBatch {
		b := domain.ImportBatch{
			ID: id, WorkbookName: id + ".xlsx", WorkbookSHA256: "abc123",
			Status: status, Mapping: "{}", RuleVersion: domain.RuleVersion, StartedAt: ts,
		}
		return b
	}
	device := func(id, key, firstBatch string) domain.Resource {
		return domain.Resource{
			ID: id, Type: domain.ResourceTypeDevice, NaturalKey: key, Name: key,
			Attributes:   map[string]string{"mgmt_ip": "10.0.0.1"},
			FirstBatchID: firstBatch, CreatedAt: ts, UpdatedAt: ts,
		}
	}
	pool := func(id, key, firstBatch string) domain.Resource {
		return domain.Resource{
			ID: id, Type: domain.ResourceTypeIPPool, NaturalKey: key, Name: key,
			Attributes:   map[string]string{"cidr": "10.0.0.0/24"},
			FirstBatchID: firstBatch, CreatedAt: ts, UpdatedAt: ts,
		}
	}
	issue := func(id, rule string, sev domain.Severity) domain.Issue {
		return domain.Issue{
			ID: id, RuleCode: rule, RuleVersion: domain.RuleVersion, Severity: sev,
			Message: "seed " + id, Evidence: map[string]string{}, CreatedAt: ts,
		}
	}

	err := db.Do(ctx, func(tx *repo.Tx) error {
		if err := tx.Batches.Create(ctx, batch("bat_api_g1", domain.BatchStatusCompleted)); err != nil {
			return err
		}
		if err := tx.Batches.Create(ctx, batch("bat_api_g2", domain.BatchStatusCompleted)); err != nil {
			return err
		}
		if err := tx.Batches.Create(ctx, batch("bat_api_g3", domain.BatchStatusRolledBack)); err != nil {
			return err
		}
		for _, res := range []domain.Resource{
			device("res_api_dev1", "DEV-API-1", "bat_api_g1"),
			device("res_api_dev2", "DEV-API-2", "bat_api_g1"),
			pool("res_api_pool1", "POOL-API-1", "bat_api_g2"),
			pool("res_api_pool2", "POOL-API-2", "bat_api_g2"),
		} {
			if err := tx.Resources.Insert(ctx, res); err != nil {
				return err
			}
		}
		if err := tx.Relations.Insert(ctx, domain.Relation{
			ID: "rel_api_1", Type: domain.RelationTypeAssignedTo,
			FromResourceID: "res_api_pool1", ToResourceID: "res_api_dev1",
			FirstBatchID: "bat_api_g2", CreatedAt: ts,
		}); err != nil {
			return err
		}
		if err := tx.AttributeChanges.InsertBatch(ctx, []domain.AttributeChange{
			{ID: "chg_api_p1", ResourceID: "res_api_dev1", BatchID: "bat_api_g1", Field: "mgmt_ip",
				OldValue: "10.0.0.1", NewValue: "10.0.0.9", Status: domain.ChangeStatusPending,
				Sheet: "设备清单", RowNumber: 2, CreatedAt: ts},
			{ID: "chg_api_p2", ResourceID: "res_api_dev2", BatchID: "bat_api_g1", Field: "vendor",
				OldValue: "华为", NewValue: "中兴", Status: domain.ChangeStatusPending,
				Sheet: "设备清单", RowNumber: 3, CreatedAt: ts},
			{ID: "chg_api_a1", ResourceID: "res_api_dev1", BatchID: "bat_api_g1", Field: "model",
				OldValue: "NE40E", NewValue: "NE80E", Status: domain.ChangeStatusPending,
				Sheet: "设备清单", RowNumber: 4, CreatedAt: ts},
			{ID: "chg_api_d1", ResourceID: "res_api_dev2", BatchID: "bat_api_g1", Field: "city",
				OldValue: "杭州", NewValue: "宁波", Status: domain.ChangeStatusPending,
				Sheet: "设备清单", RowNumber: 5, CreatedAt: ts},
		}); err != nil {
			return err
		}
		if err := tx.AttributeChanges.MarkApplied(ctx, "chg_api_a1", ts.Add(time.Hour)); err != nil {
			return err
		}
		if err := tx.AttributeChanges.MarkDismissed(ctx, "chg_api_d1"); err != nil {
			return err
		}
		for _, id := range []string{"rp_api_p1", "rp_api_a1", "rp_api_r1"} {
			if err := tx.RenameProposals.Insert(ctx, domain.RenameProposal{
				ID: id, ResourceID: "res_api_pool1", NewResourceID: "res_api_pool2", BatchID: "bat_api_g2",
				ResourceType: domain.ResourceTypeIPPool, OldKey: "POOL-API-1", NewKey: "POOL-API-2",
				MatchedFields: []string{"cidr"}, Status: domain.RenameStatusPending,
				Sheet: "地址池", RowNumber: 2, CreatedAt: ts,
			}); err != nil {
				return err
			}
		}
		if err := tx.RenameProposals.MarkApplied(ctx, "rp_api_a1", ts.Add(2*time.Hour)); err != nil {
			return err
		}
		if err := tx.RenameProposals.MarkRejected(ctx, "rp_api_r1", ts.Add(2*time.Hour)); err != nil {
			return err
		}
		for _, iss := range []domain.Issue{
			issue("iss_api_1", domain.RuleDuplicateKey, domain.SeverityError),
			issue("iss_api_2", domain.RuleDuplicateKey, domain.SeverityError),
			issue("iss_api_3", domain.RuleMissingRequiredField, domain.SeverityError),
			issue("iss_api_4", domain.RuleOrphanRecord, domain.SeverityWarning),
			issue("iss_api_5", domain.RuleOrphanRecord, domain.SeverityWarning),
			issue("iss_api_6", domain.RuleUnlinkedDevice, domain.SeverityInfo),
		} {
			if err := tx.Issues.Insert(ctx, iss); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed governance: %v", err)
	}
}

// TestAPIGovernanceSummaryEmpty pins the endpoint over an empty database:
// 200 (never 204/404 — it is a live view of the whole database), the
// no-store cache discipline, the JSON content type and the all-zero shape.
func TestAPIGovernanceSummaryEmpty(t *testing.T) {
	srv, _ := newExportTestServer(t)
	resp, sum := getGovernanceSummary(t, srv)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("governance summary: status = %d, want 200", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("governance summary: cache-control = %q, want no-store", cc)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("governance summary: content-type = %q", ct)
	}
	if sum.Pending.AttributeChanges != 0 || sum.Pending.RenameProposals != 0 {
		t.Errorf("empty pending = %+v, want zeros", sum.Pending)
	}
	if want := map[string]int{"applied": 0, "dismissed": 0}; !reflect.DeepEqual(sum.Decided.AttributeChanges, want) {
		t.Errorf("empty decided.attributeChanges = %v, want %v", sum.Decided.AttributeChanges, want)
	}
	if want := map[string]int{"applied": 0, "rejected": 0}; !reflect.DeepEqual(sum.Decided.RenameProposals, want) {
		t.Errorf("empty decided.renameProposals = %v, want %v", sum.Decided.RenameProposals, want)
	}
	if want := map[string]int{"error": 0, "warning": 0, "info": 0}; !reflect.DeepEqual(sum.Issues.BySeverity, want) {
		t.Errorf("empty issues.bySeverity = %v, want the three zeros", sum.Issues.BySeverity)
	}
	if sum.Issues.ByRule == nil || len(sum.Issues.ByRule) != 0 {
		t.Errorf("empty issues.byRule = %#v, want []", sum.Issues.ByRule)
	}
	if sum.Resources.Total != 0 || sum.Resources.ByType == nil || len(sum.Resources.ByType) != 0 {
		t.Errorf("empty resources = %+v, want total 0 {}", sum.Resources)
	}
	if sum.Relations.Total != 0 || sum.Relations.ByType == nil || len(sum.Relations.ByType) != 0 {
		t.Errorf("empty relations = %+v, want total 0 {}", sum.Relations)
	}
	if sum.Batches.Total != 0 || sum.Batches.ByStatus == nil || len(sum.Batches.ByStatus) != 0 {
		t.Errorf("empty batches = %+v, want total 0 {}", sum.Batches)
	}
	if sum.GeneratedAt.IsZero() {
		t.Error("governance summary: generatedAt is zero, want a wall-clock stamp")
	}
}

// TestAPIGovernanceSummaryDirty walks the full shape over the dirty seed:
// every block asserted field by field after decoding (see
// seedAPIGovernance for the expectation arithmetic).
func TestAPIGovernanceSummaryDirty(t *testing.T) {
	srv, a := newExportTestServer(t)
	seedAPIGovernance(t, a.DB, time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC))

	resp, sum := getGovernanceSummary(t, srv)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("governance summary: status = %d, want 200", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("governance summary: cache-control = %q, want no-store", cc)
	}

	if sum.Pending.AttributeChanges != 2 || sum.Pending.RenameProposals != 1 {
		t.Errorf("pending = %+v, want attributeChanges 2 / renameProposals 1", sum.Pending)
	}
	if want := map[string]int{"applied": 1, "dismissed": 1}; !reflect.DeepEqual(sum.Decided.AttributeChanges, want) {
		t.Errorf("decided.attributeChanges = %v, want %v", sum.Decided.AttributeChanges, want)
	}
	if want := map[string]int{"applied": 1, "rejected": 1}; !reflect.DeepEqual(sum.Decided.RenameProposals, want) {
		t.Errorf("decided.renameProposals = %v, want %v", sum.Decided.RenameProposals, want)
	}
	if want := map[string]int{"error": 3, "warning": 2, "info": 1}; !reflect.DeepEqual(sum.Issues.BySeverity, want) {
		t.Errorf("issues.bySeverity = %v, want %v", sum.Issues.BySeverity, want)
	}
	wantRule := []struct {
		Rule  string `json:"rule"`
		Count int    `json:"count"`
	}{
		{Rule: "E001", Count: 2},
		{Rule: "W001", Count: 2},
		{Rule: "E002", Count: 1},
		{Rule: "I001", Count: 1},
	}
	if !reflect.DeepEqual(sum.Issues.ByRule, wantRule) {
		t.Errorf("issues.byRule = %+v, want %+v (count DESC, code ASC)", sum.Issues.ByRule, wantRule)
	}
	if sum.Resources.Total != 4 || !reflect.DeepEqual(sum.Resources.ByType, map[string]int{"device": 2, "ip_pool": 2}) {
		t.Errorf("resources = %+v, want total 4 {device:2 ip_pool:2}", sum.Resources)
	}
	if sum.Relations.Total != 1 || !reflect.DeepEqual(sum.Relations.ByType, map[string]int{"assigned_to": 1}) {
		t.Errorf("relations = %+v, want total 1 {assigned_to:1}", sum.Relations)
	}
	if sum.Batches.Total != 3 || !reflect.DeepEqual(sum.Batches.ByStatus, map[string]int{"completed": 2, "rolled_back": 1}) {
		t.Errorf("batches = %+v, want total 3 {completed:2 rolled_back:1}", sum.Batches)
	}
}
