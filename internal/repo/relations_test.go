package repo

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// seedGraph inserts two batches: bat_1 with devices A, B and bat_2 with a
// circuit C plus relations C--a_end-->A and C--z_end-->B.
func seedGraph(t *testing.T, db *DB) (devA, devB, circuit domain.Resource) {
	t.Helper()
	ctx := context.Background()
	ts := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	if err := db.Batches.Create(ctx, newBatch("bat_1", ts)); err != nil {
		t.Fatalf("Create bat_1: %v", err)
	}
	devA = newResource("res_a", "DEV-001", ts)
	devB = newResource("res_b", "DEV-002", ts)
	if err := db.Resources.Insert(ctx, devA); err != nil {
		t.Fatalf("Insert devA: %v", err)
	}
	if err := db.Resources.Insert(ctx, devB); err != nil {
		t.Fatalf("Insert devB: %v", err)
	}
	b2 := newBatch("bat_2", ts.Add(time.Minute))
	if err := db.Batches.Create(ctx, b2); err != nil {
		t.Fatalf("Create bat_2: %v", err)
	}
	circuit = domain.Resource{
		ID: "res_c", Type: domain.ResourceTypeCircuit, NaturalKey: "CKT-001",
		Name: "电路-001", FirstBatchID: "bat_2", CreatedAt: ts, UpdatedAt: ts,
	}
	if err := db.Resources.Insert(ctx, circuit); err != nil {
		t.Fatalf("Insert circuit: %v", err)
	}
	rels := []domain.Relation{
		{ID: "rel_1", Type: domain.RelationTypeAEnd, FromResourceID: "res_c", ToResourceID: "res_a", FirstBatchID: "bat_2", CreatedAt: ts},
		{ID: "rel_2", Type: domain.RelationTypeZEnd, FromResourceID: "res_c", ToResourceID: "res_b", FirstBatchID: "bat_2", CreatedAt: ts},
	}
	for _, rel := range rels {
		if err := db.Relations.Insert(ctx, rel); err != nil {
			t.Fatalf("Insert relation %s: %v", rel.ID, err)
		}
	}
	return devA, devB, circuit
}

func TestRelationInsertAndBothDirectionListing(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	_, devB, circuit := seedGraph(t, db)

	// Listing by the FROM endpoint (circuit) returns both relations.
	rels, err := db.Relations.ListByResource(ctx, circuit.ID)
	if err != nil {
		t.Fatalf("ListByResource(circuit): %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("relations of circuit = %d, want 2", len(rels))
	}
	// Ordered by (relation_type, id): a_end before z_end.
	if rels[0].Type != domain.RelationTypeAEnd || rels[0].ToResourceID != "res_a" {
		t.Errorf("rels[0] = %+v, want a_end → res_a", rels[0])
	}
	if rels[1].Type != domain.RelationTypeZEnd || rels[1].ToResourceID != "res_b" {
		t.Errorf("rels[1] = %+v, want z_end → res_b", rels[1])
	}
	if rels[0].ID != "rel_1" || rels[1].ID != "rel_2" {
		t.Errorf("relation order = [%s, %s], want [rel_1, rel_2]", rels[0].ID, rels[1].ID)
	}

	// Listing by the TO endpoint (device B) returns only the z_end edge.
	rels, err = db.Relations.ListByResource(ctx, devB.ID)
	if err != nil {
		t.Fatalf("ListByResource(devB): %v", err)
	}
	if len(rels) != 1 || rels[0].Type != domain.RelationTypeZEnd || rels[0].FromResourceID != "res_c" {
		t.Errorf("relations of devB = %+v, want single z_end from res_c", rels)
	}

	// Duplicate (from, to, type) → domain.ErrConflict.
	dup := domain.Relation{ID: "rel_dup", Type: domain.RelationTypeAEnd, FromResourceID: "res_c", ToResourceID: "res_a", FirstBatchID: "bat_2", CreatedAt: time.Now()}
	if err := db.Relations.Insert(ctx, dup); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("Insert(dup) = %v, want domain.ErrConflict", err)
	}

	// Same pair with a different type is allowed (a_end vs z_end on a
	// self-loop is legal).
	selfLoop := domain.Relation{ID: "rel_3", Type: domain.RelationTypeZEnd, FromResourceID: "res_c", ToResourceID: "res_a", FirstBatchID: "bat_2", CreatedAt: time.Now()}
	if err := db.Relations.Insert(ctx, selfLoop); err != nil {
		t.Errorf("Insert(z_end a->a) = %v, want success", err)
	}

	// CountByType.
	counts, err := db.Relations.CountByType(ctx)
	if err != nil {
		t.Fatalf("CountByType: %v", err)
	}
	if counts[domain.RelationTypeAEnd] != 1 || counts[domain.RelationTypeZEnd] != 2 {
		t.Errorf("CountByType = %v", counts)
	}
}

func TestRelationExistsFromOtherBatch(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	_, _, _ = seedGraph(t, db)

	// bat_1 created res_a/res_b; bat_2 created relations touching them.
	inUse, err := db.Relations.ExistsFromOtherBatch(ctx, []string{"res_a", "res_b"}, "bat_1")
	if err != nil {
		t.Fatalf("ExistsFromOtherBatch: %v", err)
	}
	if !inUse {
		t.Error("res_a/res_b are referenced by bat_2's relations, want in-use")
	}
	// Asking for bat_2's own relations must not count as foreign usage.
	inUse, err = db.Relations.ExistsFromOtherBatch(ctx, []string{"res_a", "res_b", "res_c"}, "bat_2")
	if err != nil {
		t.Fatalf("ExistsFromOtherBatch: %v", err)
	}
	if inUse {
		t.Error("relations owned by bat_2 itself must not count as in-use for bat_2")
	}
	// Empty candidate list.
	inUse, err = db.Relations.ExistsFromOtherBatch(ctx, nil, "bat_1")
	if err != nil || inUse {
		t.Errorf("ExistsFromOtherBatch(empty) = %v, %v; want false", inUse, err)
	}
}

func TestRelationDeleteByBatchAndCascade(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	_, _, circuit := seedGraph(t, db)

	// Deleting bat_2's relations removes the edges but keeps resources.
	n, err := db.Relations.DeleteByBatch(ctx, "bat_2")
	if err != nil || n != 2 {
		t.Fatalf("DeleteByBatch(bat_2) = %d, %v; want 2", n, err)
	}
	if rels, _ := db.Relations.ListByResource(ctx, circuit.ID); len(rels) != 0 {
		t.Errorf("relations after delete = %d, want 0", len(rels))
	}
	if _, err := db.Resources.GetByID(ctx, circuit.ID); err != nil {
		t.Errorf("circuit must survive relation deletion: %v", err)
	}

	// Deleting bat_1's resources cascades relations that reference them.
	if _, err := db.Resources.DeleteByBatch(ctx, "bat_1"); err != nil {
		t.Fatalf("DeleteByBatch(bat_1): %v", err)
	}
	rels, err := db.Relations.ListByResource(ctx, "res_c")
	if err != nil {
		t.Fatalf("ListByResource: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("relations referencing deleted devices must cascade, got %d", len(rels))
	}
}

func TestRelationRejectsInvalidFK(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedDevices(t, db, 1)
	rel := domain.Relation{ID: "rel_bad", Type: domain.RelationTypeAEnd, FromResourceID: "res_ghost", ToResourceID: "res_001", FirstBatchID: "bat_1", CreatedAt: time.Now()}
	err := db.Relations.Insert(ctx, rel)
	if err == nil {
		t.Fatal("insert with dangling from_resource_id must fail (foreign_keys=ON via DSN)")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Errorf("error = %v, want it to mention FOREIGN KEY constraint failed", err)
	}
}
