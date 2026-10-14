package renames

import (
	"context"
	"errors"
	"github.com/mmqz/netops-lab/internal/testsupport"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// simTS is the deterministic decision clock of this test file.
var simTS = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

// newServiceDB opens a fresh database seeded with the batch + old/new
// resource pair of one pending rename proposal (mirror of the repo tests'
// fixture, built here at the service level through repo calls).
func newServiceDB(t *testing.T) (*Service, *repo.DB, string) {
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
	err = db.Do(ctx, func(tx *repo.Tx) error {
		if err := tx.Batches.Create(ctx, domain.ImportBatch{
			ID: "bat_ren", WorkbookName: "b.xlsx", WorkbookSHA256: "abc123",
			Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: domain.RuleVersion,
			StartedAt: simTS,
		}); err != nil {
			return err
		}
		if err := tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_old", Type: domain.ResourceTypeBaseStation, NaturalKey: "动力机房2",
			Name: "动力机房2", Attributes: map[string]string{
				"base_station_ip": "172.7.10.125/30", "base_station_name": "动力机房2",
			},
			FirstBatchID: "bat_ren", CreatedAt: simTS, UpdatedAt: simTS,
		}); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_new", Type: domain.ResourceTypeBaseStation, NaturalKey: "动力机房2新",
			Name: "动力机房2新", Attributes: map[string]string{
				"base_station_ip": "172.7.10.125/30", "base_station_name": "动力机房2新",
			},
			FirstBatchID: "bat_ren", CreatedAt: simTS, UpdatedAt: simTS,
		})
	})
	if err != nil {
		t.Fatalf("seed resources: %v", err)
	}
	if err := db.RenameProposals.Insert(ctx, domain.RenameProposal{
		ID: "rp_1", ResourceID: "res_old", NewResourceID: "res_new", BatchID: "bat_ren",
		ResourceType: domain.ResourceTypeBaseStation, OldKey: "动力机房2", NewKey: "动力机房2新",
		MatchedFields: []string{"base_station_ip"},
		Status:        domain.RenameStatusPending, Sheet: "无线基站表", RowNumber: 2, CreatedAt: simTS,
	}); err != nil {
		t.Fatalf("seed proposal: %v", err)
	}
	svc := New(db)
	svc.Now = func() time.Time { return simTS.Add(time.Hour) }
	return svc, db, "rp_1"
}

// TestApplyMergeIsAtomicAndAudited pins the apply journey: the merge
// rewrites the old resource's key (same res_x id, refreshed updated_at),
// deletes the new resource, flips the proposal to applied with decided_at,
// and the list join reflects the new key. Re-apply and unknown ids answer
// the 404/409 semantics (ErrNotFound / ErrConflict with the current status
// in the message).
func TestApplyMergeIsAtomicAndAudited(t *testing.T) {
	svc, db, id := newServiceDB(t)
	ctx := context.Background()
	decided := simTS.Add(time.Hour)

	item, err := svc.Apply(ctx, id)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if item.Status != "applied" || item.OldKey != "动力机房2" || item.NewKey != "动力机房2新" ||
		item.NaturalKey != "动力机房2新" || item.DecidedAt == nil || !item.DecidedAt.Equal(decided) ||
		item.NewResourceID != "res_new" || len(item.MatchedFields) != 1 {
		t.Fatalf("applied item = %+v, want applied 动力机房2→动力机房2新 with decidedAt", item)
	}
	old, err := db.Resources.GetByID(ctx, "res_old")
	if err != nil || old.NaturalKey != "动力机房2新" || old.UpdatedAt.Equal(simTS) {
		t.Errorf("old resource after apply = %+v (err %v), want key 动力机房2新 with refreshed updated_at", old, err)
	}
	if old.Attributes["base_station_name"] != "动力机房2新" || old.Attributes["base_station_ip"] != "172.7.10.125/30" {
		t.Errorf("old attributes after apply = %v, want the key-bearing name synced to 动力机房2新 and the matched field kept", old.Attributes)
	}
	// W9-D: the display NAME follows the merge (base_station_name is the
	// NameField and an ordinary attribute — the attributes lookup wins).
	if old.Name != "动力机房2新" {
		t.Errorf("old resource name after apply = %q, want 动力机房2新 (the display name follows the merged identity)", old.Name)
	}
	if _, err := db.Resources.GetByID(ctx, "res_new"); err == nil {
		t.Errorf("new resource must be deleted by the merge")
	}
	got, err := db.RenameProposals.Get(ctx, id)
	if err != nil || got.Status != domain.RenameStatusApplied || got.DecidedAt == nil {
		t.Errorf("proposal after apply = %+v (err %v), want applied with decided_at", got, err)
	}

	// Re-apply → conflict naming the current status.
	_, err = svc.Apply(ctx, id)
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("re-apply err = %v, want ErrConflict", err)
	} else if got, _ := db.RenameProposals.Get(ctx, id); !contains(got.Status.String(), err.Error()) {
		t.Errorf("re-apply message %q must name the current status %q", err.Error(), got.Status)
	}
	// Unknown id → not found.
	if _, err := svc.Apply(ctx, "rp_nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("apply unknown err = %v, want ErrNotFound", err)
	}

	// The list join reflects the merged key.
	result, err := svc.List(ctx, Filter{Status: domain.RenameStatusApplied})
	if err != nil || result.Total != 1 || len(result.Items) != 1 {
		t.Fatalf("list applied = %+v (err %v), want 1 item", result, err)
	}
	if result.Items[0].NaturalKey != "动力机房2新" || result.Items[0].ResourceType != "base_station" {
		t.Errorf("joined item = %+v, want naturalKey 动力机房2新 of base_station", result.Items[0])
	}
}

// TestRejectLeavesResourcesUntouched pins the reject journey: the proposal
// flips to rejected with decided_at, both resources survive untouched
// (independent identities = the import's default outcome), and re-reject /
// re-apply answer conflicts.
func TestRejectLeavesResourcesUntouched(t *testing.T) {
	svc, db, id := newServiceDB(t)
	ctx := context.Background()

	item, err := svc.Reject(ctx, id)
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if item.Status != "rejected" || item.DecidedAt == nil {
		t.Fatalf("rejected item = %+v, want rejected with decidedAt", item)
	}
	for _, want := range []struct{ id, key string }{{"res_old", "动力机房2"}, {"res_new", "动力机房2新"}} {
		res, err := db.Resources.GetByID(ctx, want.id)
		if err != nil || res.NaturalKey != want.key || !res.UpdatedAt.Equal(simTS) {
			t.Errorf("resource %s after reject = %+v (err %v), want untouched key %q", want.id, res, err, want.key)
		}
	}
	if _, err := svc.Reject(ctx, id); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("re-reject err = %v, want ErrConflict", err)
	}
	if _, err := svc.Apply(ctx, id); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("apply-after-reject err = %v, want ErrConflict", err)
	}
	if _, err := svc.Reject(ctx, "rp_nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("reject unknown err = %v, want ErrNotFound", err)
	}
}

// TestApplyTargetMissing pins the 409 target_missing semantics: when the
// proposal's NEW resource has vanished while the proposal itself survived
// (here simulated by a DIRECT delete — the only reachable trigger: a
// standard batch rollback deletes the batch's pending proposals first, so
// a rolled-back proposal answers 404 instead), the apply
// refuses with *TargetMissingError (recommendation: reject) and the old
// resource keeps its key — re-pointing identity at a key that never
// materialized would be a merge into nothing.
func TestApplyTargetMissing(t *testing.T) {
	svc, db, id := newServiceDB(t)
	ctx := context.Background()

	if err := db.Do(ctx, func(tx *repo.Tx) error {
		_, err := tx.Resources.DeleteByID(ctx, "res_new")
		return err
	}); err != nil {
		t.Fatalf("delete new resource: %v", err)
	}
	_, err := svc.Apply(ctx, id)
	var missing *TargetMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("apply with missing target err = %v, want *TargetMissingError", err)
	}
	if missing.NewResourceID != "res_new" {
		t.Errorf("TargetMissingError.NewResourceID = %q, want res_new", missing.NewResourceID)
	}
	if !contains("驳回", err.Error()) {
		t.Errorf("target missing message %q must recommend rejecting (驳回)", err.Error())
	}
	// The old resource is untouched; the proposal stays pending (reject is
	// the way out).
	old, err := db.Resources.GetByID(ctx, "res_old")
	if err != nil || old.NaturalKey != "动力机房2" {
		t.Errorf("old resource after refused apply = %+v (err %v), want untouched", old, err)
	}
	got, err := db.RenameProposals.Get(ctx, id)
	if err != nil || got.Status != domain.RenameStatusPending {
		t.Errorf("proposal after refused apply = %+v (err %v), want still pending", got, err)
	}
	if _, err := svc.Reject(ctx, id); err != nil {
		t.Fatalf("reject after refused apply: %v", err)
	}
}

// TestApplyVanishedOldResource pins the 404 side: an old resource deleted
// by a rollback cascade removes the proposal with it (resource_id CASCADE),
// so an apply of a cascade-orphaned id answers not-found — and a proposal
// whose old resource vanished through any other path answers the same.
func TestApplyVanishedOldResource(t *testing.T) {
	svc, db, id := newServiceDB(t)
	ctx := context.Background()

	if err := db.Do(ctx, func(tx *repo.Tx) error {
		_, err := tx.Resources.DeleteByID(ctx, "res_old")
		return err
	}); err != nil {
		t.Fatalf("delete old resource: %v", err)
	}
	// The resource_id CASCADE removed the proposal too.
	if _, err := db.RenameProposals.Get(ctx, id); err == nil {
		t.Fatalf("proposal must be cascade-deleted with its old resource")
	}
	if _, err := svc.Apply(ctx, id); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("apply of cascade-orphaned proposal err = %v, want ErrNotFound", err)
	}
}

// TestListFiltersAndPagination pins the list contract: status / resource
// (either side — W11-D) / type / batch filters, the limit clamps and the
// created_at ordering.
func TestListFiltersAndPagination(t *testing.T) {
	svc, db, _ := newServiceDB(t)
	ctx := context.Background()

	// Two more proposals (distinct resources, distinct times).
	if err := db.Batches.Create(ctx, domain.ImportBatch{
		ID: "bat_ren2", WorkbookName: "b.xlsx", WorkbookSHA256: "abc123",
		Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: domain.RuleVersion,
		StartedAt: simTS,
	}); err != nil {
		t.Fatalf("seed batch 2: %v", err)
	}
	if err := db.Resources.Insert(ctx, domain.Resource{
		ID: "res_old2", Type: domain.ResourceTypeBaseStation, NaturalKey: "学府路2",
		Name: "学府路2", Attributes: map[string]string{}, FirstBatchID: "bat_ren",
		CreatedAt: simTS, UpdatedAt: simTS,
	}); err != nil {
		t.Fatalf("seed res_old2: %v", err)
	}
	for i, key := range []string{"学府路2新", "学府路2新2"} {
		if err := db.RenameProposals.Insert(ctx, domain.RenameProposal{
			ID: "rp_2" + key, ResourceID: "res_old2", NewResourceID: "res_new2", BatchID: "bat_ren2",
			ResourceType: domain.ResourceTypeBaseStation, OldKey: "学府路2", NewKey: key,
			MatchedFields: []string{"base_station_ip"},
			Status:        domain.RenameStatusPending, Sheet: "无线基站表", RowNumber: 3,
			CreatedAt: simTS.Add(time.Duration(i+1) * time.Minute),
		}); err != nil {
			t.Fatalf("seed proposal %s: %v", key, err)
		}
	}

	result, err := svc.List(ctx, Filter{})
	if err != nil || result.Total != 3 || len(result.Items) != 3 {
		t.Fatalf("list all = %d items (total %d, err %v), want 3/3", len(result.Items), result.Total, err)
	}
	if result.Items[0].OldKey != "动力机房2" || result.Items[1].OldKey != "学府路2" || result.Items[2].NewKey != "学府路2新2" {
		t.Fatalf("list order = %+v, want created_at order", result.Items)
	}
	for _, tc := range []struct {
		f    Filter
		want int
	}{
		{Filter{Status: domain.RenameStatusPending}, 3},
		{Filter{Status: domain.RenameStatusApplied}, 0},
		{Filter{ResourceID: "res_old2"}, 2}, // old side (identity keeper), semantics unchanged
		{Filter{ResourceID: "res_new"}, 1},  // new side of rp_1 (either side — W11-D)
		{Filter{ResourceID: "res_new2"}, 2}, // new side of rp_2/rp_3
		{Filter{BatchID: "bat_ren2"}, 2},
		{Filter{ResourceType: domain.ResourceTypeBaseStation}, 3},
		{Filter{ResourceType: domain.ResourceTypeDevice}, 0},
		{Filter{Limit: 2}, 3}, // total ignores the window
	} {
		result, err := svc.List(ctx, tc.f)
		if err != nil || result.Total != tc.want {
			t.Errorf("list %+v = total %d (err %v), want %d", tc.f, result.Total, err, tc.want)
		}
	}
	// The pagination window itself.
	result, err = svc.List(ctx, Filter{Limit: 2})
	if err != nil || len(result.Items) != 2 || result.Total != 3 {
		t.Errorf("limit 2 window = %d items (total %d), want 2/3", len(result.Items), result.Total)
	}
	// Default page size clamps at MaxPageSize.
	svc.db = db // no-op, keeps db referenced for clarity
	result, err = svc.List(ctx, Filter{Limit: 500})
	if err != nil || len(result.Items) != 3 {
		t.Errorf("limit 500 clamp = %d items (err %v), want all 3 (clamped to 100)", len(result.Items), err)
	}
}

// contains reports whether sub appears in s (a tiny helper to keep the
// assertions above readable).
func contains(sub, s string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestApplySyncsResourceNameForKeyClassNameField pins the W9-D name sync on
// the KEY-CLASS name field shape (W8-Q P3-5, the original complaint): for
// port/ptp_port the NameField (port_name) is a natural-key component, so it
// never enters the attributes — the new resource's own Name (computed from
// exactly that field at import time) carries the renamed value. A port
// fixture mirrors the shape: the merged resource must end up with the new
// key AND the new display name.
func TestApplySyncsResourceNameForKeyClassNameField(t *testing.T) {
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
	err = db.Do(ctx, func(tx *repo.Tx) error {
		if err := tx.Batches.Create(ctx, domain.ImportBatch{
			ID: "bat_port", WorkbookName: "ports.xlsx", WorkbookSHA256: "abc123",
			Status: domain.BatchStatusCompleted, Mapping: "{}", RuleVersion: domain.RuleVersion,
			StartedAt: simTS,
		}); err != nil {
			return err
		}
		// port natural key = device_name|port_name; port_name (the NameField)
		// is a KEY field, so it is absent from the attributes — exactly the
		// shape that used to leave the stale display name behind.
		if err := tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_port_old", Type: domain.ResourceTypePort, NaturalKey: "B2|gei_1/7", Name: "gei_1/7",
			Attributes:   map[string]string{"device_ip": "10.0.0.2"},
			FirstBatchID: "bat_port", CreatedAt: simTS, UpdatedAt: simTS,
		}); err != nil {
			return err
		}
		return tx.Resources.Insert(ctx, domain.Resource{
			ID: "res_port_new", Type: domain.ResourceTypePort, NaturalKey: "B2|gei_1/8", Name: "gei_1/8",
			Attributes:   map[string]string{"device_ip": "10.0.0.2"},
			FirstBatchID: "bat_port", CreatedAt: simTS, UpdatedAt: simTS,
		})
	})
	if err != nil {
		t.Fatalf("seed port resources: %v", err)
	}
	if err := db.RenameProposals.Insert(ctx, domain.RenameProposal{
		ID: "rp_port", ResourceID: "res_port_old", NewResourceID: "res_port_new", BatchID: "bat_port",
		ResourceType: domain.ResourceTypePort, OldKey: "B2|gei_1/7", NewKey: "B2|gei_1/8",
		MatchedFields: []string{"device_ip"},
		Status:        domain.RenameStatusPending, Sheet: "端口资源表", RowNumber: 2, CreatedAt: simTS,
	}); err != nil {
		t.Fatalf("seed proposal: %v", err)
	}
	svc := New(db)
	svc.Now = func() time.Time { return simTS.Add(time.Hour) }

	if _, err := svc.Apply(ctx, "rp_port"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	merged, err := db.Resources.GetByID(ctx, "res_port_old")
	if err != nil {
		t.Fatalf("GetByID after apply: %v", err)
	}
	if merged.NaturalKey != "B2|gei_1/8" {
		t.Errorf("merged natural key = %q, want B2|gei_1/8", merged.NaturalKey)
	}
	// The name-sync pin itself: the display name carries the renamed value
	// even though port_name never lives in the attributes.
	if merged.Name != "gei_1/8" {
		t.Errorf("merged display name = %q, want gei_1/8 (the new-side name follows the merge; the W8-Q P3-5 stale-name gap)", merged.Name)
	}
	if merged.Attributes["device_ip"] != "10.0.0.2" {
		t.Errorf("merged attributes = %v, want the matched field kept", merged.Attributes)
	}
}
