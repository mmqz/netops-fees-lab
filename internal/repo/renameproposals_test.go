package repo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// newRenameDB opens a fresh database seeded with two batches and an old +
// new base_station resource pair (the rename candidates) and returns it
// (closed at test cleanup).
func newRenameDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("db.Close: %v", err)
		}
	})
	ctx := context.Background()
	err = db.Do(ctx, func(tx *Tx) error {
		if err := tx.Batches.Create(ctx, newBatch("bat_ren", simTS)); err != nil {
			return err
		}
		if err := tx.Batches.Create(ctx, newBatch("bat_ren2", simTS)); err != nil {
			return err
		}
		if err := tx.Resources.Insert(ctx, newBaseStationResource("res_ren_old", "bat_ren")); err != nil {
			return err
		}
		twin := newBaseStationResource("res_ren_new", "bat_ren2")
		twin.NaturalKey = "动力机房2新"
		twin.Name = "动力机房2新"
		return tx.Resources.Insert(ctx, twin)
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

// rp builds one pending rename proposal of the seeded pair.
func rp(id string, ts time.Time) domain.RenameProposal {
	return domain.RenameProposal{
		ID: id, ResourceID: "res_ren_old", NewResourceID: "res_ren_new", BatchID: "bat_ren",
		ResourceType: domain.ResourceTypeBaseStation, OldKey: "动力机房2", NewKey: "动力机房2新",
		MatchedFields: []string{"base_station_ip"},
		Status:        domain.RenameStatusPending, Sheet: "无线基站表", RowNumber: 2, CreatedAt: ts,
	}
}

// TestRenameProposalInsertListAndLifecycle pins the repo CRUD of
// rename_proposals: Insert, List filters (status / resource / type / batch)
// with pagination and totals, the created_at ordering, the pending-flip
// methods and the pending-scope delete/count helpers.
func TestRenameProposalInsertListAndLifecycle(t *testing.T) {
	db := newRenameDB(t)
	ctx := context.Background()

	// Insert guards: only pending proposals may be created.
	bad := rp("rp_bad", tsA)
	bad.Status = domain.RenameStatusApplied
	if err := db.RenameProposals.Insert(ctx, bad); err == nil {
		t.Fatalf("insert non-pending proposal must fail")
	}

	// Three pending proposals (two batches) + one rejected one.
	if err := db.RenameProposals.Insert(ctx, rp("rp_1", tsA)); err != nil {
		t.Fatalf("insert rp_1: %v", err)
	}
	second := rp("rp_2", tsB)
	second.BatchID = "bat_ren2"
	second.NewKey = "动力机房2新2"
	second.NewResourceID = "res_ren_new2" // distinct new side → either-side counts differ
	if err := db.RenameProposals.Insert(ctx, second); err != nil {
		t.Fatalf("insert rp_2: %v", err)
	}
	third := rp("rp_3", tsC)
	third.NewKey = "动力机房2新3"
	if err := db.RenameProposals.Insert(ctx, third); err != nil {
		t.Fatalf("insert rp_3: %v", err)
	}

	// List: every status, ordered by created_at.
	rps, total, err := db.RenameProposals.List(ctx, RenameFilter{})
	if err != nil || total != 3 || len(rps) != 3 {
		t.Fatalf("list all = %d items (total %d, err %v), want 3/3", len(rps), total, err)
	}
	if rps[0].ID != "rp_1" || rps[1].ID != "rp_2" || rps[2].ID != "rp_3" {
		t.Fatalf("list order = %s,%s,%s, want rp_1,rp_2,rp_3 (created_at order)", rps[0].ID, rps[1].ID, rps[2].ID)
	}
	if rps[0].MatchedFields[0] != "base_station_ip" {
		t.Errorf("matched_fields round-trip = %v, want [base_station_ip]", rps[0].MatchedFields)
	}

	// Filters: resource (either side — W11-D), batch, type, status;
	// pagination window.
	for _, tc := range []struct {
		f    RenameFilter
		want int
	}{
		{RenameFilter{ResourceID: "res_ren_old"}, 3},  // old side: all three
		{RenameFilter{ResourceID: "res_ren_new"}, 2},  // new side: rp_1 + rp_3 (rp_2 points at res_ren_new2)
		{RenameFilter{ResourceID: "res_ren_new2"}, 1}, // new side of rp_2 only
		{RenameFilter{ResourceID: "res_nope"}, 0},
		{RenameFilter{BatchID: "bat_ren2"}, 1},
		{RenameFilter{ResourceType: domain.ResourceTypeBaseStation}, 3},
		{RenameFilter{ResourceType: domain.ResourceTypeDevice}, 0},
		{RenameFilter{Status: domain.RenameStatusPending}, 3},
		{RenameFilter{Status: domain.RenameStatusApplied}, 0},
		{RenameFilter{Limit: 2}, 3},            // total ignores the limit window
		{RenameFilter{Limit: 2, Offset: 2}, 3}, // total ignores the window
	} {
		_, total, err := db.RenameProposals.List(ctx, tc.f)
		if err != nil || total != tc.want {
			t.Errorf("list %+v = total %d (err %v), want %d", tc.f, total, err, tc.want)
		}
	}
	// Bogus filter values are refused.
	if _, _, err := db.RenameProposals.List(ctx, RenameFilter{Status: "bogus"}); err == nil {
		t.Errorf("list with bogus status must fail")
	}
	if _, _, err := db.RenameProposals.List(ctx, RenameFilter{ResourceType: "vlan"}); err == nil {
		t.Errorf("list with bogus type must fail")
	}
	// The pagination total ignores limit/offset.
	if got, total, _ := db.RenameProposals.List(ctx, RenameFilter{Limit: 2}); total != 3 || len(got) != 2 {
		t.Errorf("limit window = %d items (total %d), want 2/3", len(got), total)
	}

	// Lifecycle: reject rp_2, apply rp_1.
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.RenameProposals.MarkRejected(ctx, "rp_2", tsC)
	}); err != nil {
		t.Fatalf("MarkRejected: %v", err)
	}
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.RenameProposals.MarkApplied(ctx, "rp_1", tsC)
	}); err != nil {
		t.Fatalf("MarkApplied: %v", err)
	}
	for id, wantStatus := range map[string]domain.RenameStatus{
		"rp_1": domain.RenameStatusApplied, "rp_2": domain.RenameStatusRejected,
	} {
		got, err := db.RenameProposals.Get(ctx, id)
		if err != nil || got.Status != wantStatus || got.DecidedAt == nil || !got.DecidedAt.Equal(tsC) {
			t.Errorf("proposal %s after decision = %+v (err %v), want %s with decided_at", id, got, err, wantStatus)
		}
	}
	// Flips refuse non-pending proposals (conflict) and unknown ids (not found).
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.RenameProposals.MarkApplied(ctx, "rp_1", tsC)
	}); err == nil {
		t.Errorf("re-apply must conflict")
	}
	if _, err := db.RenameProposals.Get(ctx, "rp_nope"); err == nil {
		t.Errorf("get unknown id must fail")
	}

	// Pending-scope delete/count: deleting bat_ren's pendings leaves the
	// rejected rp_2 (bat_ren2) and the applied rp_1 in place.
	if n, err := db.RenameProposals.CountPendingByBatch(ctx, "bat_ren"); err != nil || n != 1 {
		t.Fatalf("CountPendingByBatch(bat_ren) = %d (err %v), want 1 (rp_3)", n, err)
	}
	var deleted int64
	if err := db.Do(ctx, func(tx *Tx) error {
		var err error
		deleted, err = tx.RenameProposals.DeletePendingByBatch(ctx, "bat_ren")
		return err
	}); err != nil || deleted != 1 {
		t.Fatalf("DeletePendingByBatch = %d (err %v), want 1", deleted, err)
	}
	if _, err := db.RenameProposals.Get(ctx, "rp_3"); err == nil {
		t.Errorf("pending rp_3 must be deleted by its batch's delete")
	}
	for _, id := range []string{"rp_1", "rp_2"} {
		if _, err := db.RenameProposals.Get(ctx, id); err != nil {
			t.Errorf("proposal %s must survive the pending-only delete: %v", id, err)
		}
	}
}

// TestRenameApplyRepoMerge pins the repo primitives of the rename apply
// merge inside ONE transaction: relation retargeting (both endpoints, with
// the UNIQUE-collision delete), the new resource's deletion (its pending
// attribute changes vanish via the v11 CASCADE), the old resource's
// natural_key rewrite, and the proposal flip. The relation retarget cases
// are exercised with SYNTHETIC relations (ip_pool assigned_to edges, both
// directions) because no current ref field of the seven types points AT a
// rename-detection type end-to-end — documented in
// docs/import-and-validation.md §16.
func TestRenameApplyRepoMerge(t *testing.T) {
	db := newRenameDB(t)
	ctx := context.Background()

	// A device both pools attach to, plus a pending attribute change on the
	// new resource (the v11 CASCADE must remove it with the resource).
	dev := newResource("res_ren_dev", "DEV-1", simTS)
	dev.FirstBatchID = "bat_ren"
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.Resources.Insert(ctx, dev)
	}); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.AttributeChanges.InsertBatch(ctx, []domain.AttributeChange{{
			ID: "chg_ren_new", ResourceID: "res_ren_new", BatchID: "bat_ren2",
			Field: "base_station_ip", OldValue: "172.7.10.125/30", NewValue: "172.7.10.200/30",
			Status: domain.ChangeStatusPending, Sheet: "无线基站表", RowNumber: 2, CreatedAt: simTS,
		}})
	}); err != nil {
		t.Fatalf("seed attribute change: %v", err)
	}

	// Synthetic relations on the NEW resource: one from-side (new→device,
	// assigned_to), one to-side (device→new, a synthetic a_end — direction
	// coverage for the retarget), and an OLD-side twin of the from-side edge
	// (old→device, assigned_to) that the retargeted edge must collide with.
	rels := []domain.Relation{
		{ID: "rel_new_from", Type: domain.RelationTypeAssignedTo, FromResourceID: "res_ren_new", ToResourceID: "res_ren_dev",
			Attributes: map[string]string{}, FirstBatchID: "bat_ren2", CreatedAt: simTS},
		{ID: "rel_new_to", Type: domain.RelationTypeAEnd, FromResourceID: "res_ren_dev", ToResourceID: "res_ren_new",
			Attributes: map[string]string{}, FirstBatchID: "bat_ren2", CreatedAt: simTS},
		{ID: "rel_old_from", Type: domain.RelationTypeAssignedTo, FromResourceID: "res_ren_old", ToResourceID: "res_ren_dev",
			Attributes: map[string]string{}, FirstBatchID: "bat_ren", CreatedAt: simTS},
	}
	for _, rel := range rels {
		if err := db.Do(ctx, func(tx *Tx) error {
			return tx.Relations.Insert(ctx, rel)
		}); err != nil {
			t.Fatalf("seed relation %s: %v", rel.ID, err)
		}
	}

	// The proposal to apply.
	if err := db.RenameProposals.Insert(ctx, rp("rp_apply", tsA)); err != nil {
		t.Fatalf("insert proposal: %v", err)
	}

	// The full merge inside one transaction.
	var retargeted, deleted int64
	err := db.Do(ctx, func(tx *Tx) error {
		var err error
		retargeted, deleted, err = tx.Relations.RetargetResource(ctx, "res_ren_old", "res_ren_new")
		if err != nil {
			return err
		}
		if _, err := tx.Resources.DeleteByID(ctx, "res_ren_new"); err != nil {
			return err
		}
		if err := tx.Resources.UpdateNaturalKey(ctx, "res_ren_old", "动力机房2新", tsB); err != nil {
			return err
		}
		return tx.RenameProposals.MarkApplied(ctx, "rp_apply", tsB)
	})
	if err != nil {
		t.Fatalf("merge transaction: %v", err)
	}
	if retargeted != 1 || deleted != 1 {
		t.Fatalf("retarget counters = %d retargeted / %d deleted, want 1/1 (a_end moves, assigned_to collides)", retargeted, deleted)
	}

	// The a_end edge now points at the old resource; the colliding
	// assigned_to edge of the new side is gone (the old side's twin stays).
	moved, err := db.Relations.ListByResource(ctx, "res_ren_dev")
	if err != nil {
		t.Fatalf("list device relations: %v", err)
	}
	seen := map[string]bool{}
	for _, rel := range moved {
		seen[rel.ID+"/"+rel.FromResourceID+"→"+rel.ToResourceID] = true
	}
	if !seen["rel_new_to/res_ren_dev→res_ren_old"] {
		t.Errorf("a_end edge after merge = %v, want retargeted to res_ren_old", seen)
	}
	if seen["rel_new_from/res_ren_dev→res_ren_new"] || seen["rel_new_from/res_ren_new→res_ren_dev"] {
		t.Errorf("colliding new-side assigned_to edge must be deleted, got %v", seen)
	}
	if !seen["rel_old_from/res_ren_old→res_ren_dev"] {
		t.Errorf("old-side assigned_to edge must survive, got %v", seen)
	}

	// The new resource is gone — together with its pending attribute change
	// (v11 resource_id CASCADE); the old resource carries the new key and a
	// refreshed updated_at.
	if _, err := db.Resources.GetByID(ctx, "res_ren_new"); err == nil {
		t.Errorf("new resource must be deleted")
	}
	if ch, err := db.AttributeChanges.Get(ctx, "chg_ren_new"); err == nil {
		t.Errorf("pending attribute change of the deleted new resource must cascade away, got %+v", ch)
	}
	old, err := db.Resources.GetByID(ctx, "res_ren_old")
	if err != nil || old.NaturalKey != "动力机房2新" || !old.UpdatedAt.Equal(tsB) {
		t.Errorf("old resource after merge = %+v (err %v), want key 动力机房2新 with refreshed updated_at", old, err)
	}
	// The old resource is still reachable under the new key.
	if _, err := db.Resources.GetByTypeKey(ctx, domain.ResourceTypeBaseStation, "动力机房2新"); err != nil {
		t.Errorf("GetByTypeKey(动力机房2新): %v", err)
	}
	// The proposal is applied.
	got, err := db.RenameProposals.Get(ctx, "rp_apply")
	if err != nil || got.Status != domain.RenameStatusApplied || got.NewResourceID != "res_ren_new" {
		t.Errorf("proposal after merge = %+v (err %v), want applied with the new resource id kept as text", got, err)
	}

	// A resource still carrying relations is deleted via the relations ON
	// DELETE CASCADE (the same semantics the rollback delete chain relies
	// on): the relations vanish with it, nothing dangles.
	if err := db.Do(ctx, func(tx *Tx) error {
		return tx.Relations.Insert(ctx, domain.Relation{
			ID: "rel_cascade", Type: domain.RelationTypeAEnd, FromResourceID: "res_ren_old", ToResourceID: "res_ren_dev",
			Attributes: map[string]string{}, FirstBatchID: "bat_ren", CreatedAt: simTS,
		})
	}); err != nil {
		t.Fatalf("seed cascade relation: %v", err)
	}
	if err := db.Do(ctx, func(tx *Tx) error {
		_, err := tx.Resources.DeleteByID(ctx, "res_ren_dev")
		return err
	}); err != nil {
		t.Fatalf("delete device with relation: %v", err)
	}
	if rels, err := db.Relations.ListByResource(ctx, "res_ren_old"); err != nil || len(rels) != 0 {
		t.Errorf("relations after cascade delete = %v (err %v), want the rel_cascade edge gone with its endpoint", rels, err)
	}
}
