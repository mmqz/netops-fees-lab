package repo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

func TestSourceRefCRUDRoundtrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	_, _, _ = seedGraph(t, db)

	ref := domain.SourceRef{
		ID:         "src_1",
		EntityKind: domain.EntityKindResource,
		EntityID:   "res_c",
		BatchID:    "bat_2",
		Workbook:   "circuits.xlsx",
		Sheet:      "电路",
		RowNumber:  7,
		Raw:        map[string]string{"电路编号": "CKT-001", "A端设备": "DEV-001", "Z端设备": "DEV-002"},
	}
	if err := db.SourceRefs.Insert(ctx, ref); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, "res_c")
	if err != nil {
		t.Fatalf("ListByEntity: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("ListByEntity = %d refs, want 1", len(refs))
	}
	got := refs[0]
	if got.ID != "src_1" || got.Workbook != "circuits.xlsx" || got.Sheet != "电路" || got.RowNumber != 7 {
		t.Errorf("source ref roundtrip mismatch: %+v", got)
	}
	if got.Raw["电路编号"] != "CKT-001" || got.Raw["A端设备"] != "DEV-001" {
		t.Errorf("raw roundtrip mismatch: %+v", got.Raw)
	}

	// Entity with no refs returns empty slice.
	refs, err = db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, "res_none")
	if err != nil || len(refs) != 0 {
		t.Errorf("ListByEntity(missing) = %d, %v; want 0", len(refs), err)
	}

	// Duplicate (entity_kind, entity_id, batch_id) → domain.ErrConflict.
	dup := ref
	dup.ID = "src_dup"
	if err := db.SourceRefs.Insert(ctx, dup); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("Insert(dup) = %v, want domain.ErrConflict", err)
	}

	// Different batch for the same entity is allowed (re-import evidence).
	ref2 := ref
	ref2.ID = "src_2"
	ref2.BatchID = "bat_3"
	if err := db.Batches.Create(ctx, newBatch("bat_3", time.Now())); err != nil {
		t.Fatalf("Create bat_3: %v", err)
	}
	if err := db.SourceRefs.Insert(ctx, ref2); err != nil {
		t.Errorf("Insert(second batch) = %v, want success", err)
	}
	refs, _ = db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, "res_c")
	if len(refs) != 2 {
		t.Errorf("ListByEntity = %d refs, want 2", len(refs))
	}

	// DeleteByBatch.
	n, err := db.SourceRefs.DeleteByBatch(ctx, "bat_2")
	if err != nil || n != 1 {
		t.Errorf("DeleteByBatch(bat_2) = %d, %v; want 1", n, err)
	}
	refs, _ = db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, "res_c")
	if len(refs) != 1 || refs[0].BatchID != "bat_3" {
		t.Errorf("after delete: %v", refs)
	}
}

func TestSourceRefGetByID(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	_, _, _ = seedGraph(t, db)
	ref := domain.SourceRef{
		ID: "src_g1", EntityKind: domain.EntityKindRelation, EntityID: "rel_1",
		BatchID: "bat_2", Workbook: "circuits.xlsx", Sheet: "电路", RowNumber: 2,
		Raw: map[string]string{"A端设备": "DEV-001"},
	}
	if err := db.SourceRefs.Insert(ctx, ref); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := db.SourceRefs.GetByID(ctx, "src_g1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != ref.ID || got.EntityKind != domain.EntityKindRelation || got.EntityID != "rel_1" ||
		got.Workbook != "circuits.xlsx" || got.Sheet != "电路" || got.RowNumber != 2 {
		t.Errorf("GetByID roundtrip mismatch: %+v", got)
	}

	if _, err := db.SourceRefs.GetByID(ctx, "src_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("GetByID(missing) = %v, want domain.ErrNotFound", err)
	}
}
