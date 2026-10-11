package repo

import (
	"context"
	"errors"
	"slices"
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

// TestSourceRefListByEntityVersionOrder pins the v0.5 W7-D ordering of
// ListByEntity: INSERTION order (rowid), not batch_id lexicographic order.
// batch_id is a random UUID, so with multiple versions per entity (re-import
// skips append one ref per batch) a batch_id sort would interleave versions
// meaninglessly. The batch ids below are deliberately chosen so that their
// lexicographic order (bat_a < bat_m < bat_z) differs from the insertion
// order (bat_z, bat_a, bat_m) — the old ORDER BY batch_id would fail this
// test. Deleting a middle version must not reorder the survivors (rowids are
// never renumbered by deletion; SQLite assigns new rowids above the current
// maximum, so a later insert still sorts last).
func TestSourceRefListByEntityVersionOrder(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	_, _, _ = seedGraph(t, db)

	// Insertion order: bat_z, then bat_a, then bat_m.
	for _, tc := range []struct{ batch, id string }{
		{"bat_z", "src_z"}, {"bat_a", "src_a"}, {"bat_m", "src_m"},
	} {
		if err := db.Batches.Create(ctx, newBatch(tc.batch, time.Now())); err != nil {
			t.Fatalf("Create %s: %v", tc.batch, err)
		}
		ref := domain.SourceRef{
			ID: tc.id, EntityKind: domain.EntityKindResource, EntityID: "res_c",
			BatchID: tc.batch, Workbook: "w.xlsx", Sheet: "s", RowNumber: 2,
			Raw: map[string]string{"k": "v"},
		}
		if err := db.SourceRefs.Insert(ctx, ref); err != nil {
			t.Fatalf("Insert %s: %v", tc.id, err)
		}
	}

	refs, err := db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, "res_c")
	if err != nil {
		t.Fatalf("ListByEntity: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("ListByEntity = %d refs, want 3", len(refs))
	}
	gotOrder := []string{refs[0].ID, refs[1].ID, refs[2].ID}
	wantOrder := []string{"src_z", "src_a", "src_m"} // insertion order, NOT bat_a/bat_m/bat_z
	if !slices.Equal(gotOrder, wantOrder) {
		t.Errorf("ref order = %v, want %v (insertion/version order, not batch_id lexicographic)", gotOrder, wantOrder)
	}

	// Deleting the middle version (batch bat_a) leaves the survivors in
	// their relative order, and a NEW version still sorts after both.
	if _, err := db.SourceRefs.DeleteByBatch(ctx, "bat_a"); err != nil {
		t.Fatalf("DeleteByBatch(bat_a): %v", err)
	}
	if err := db.Batches.Create(ctx, newBatch("bat_n", time.Now())); err != nil {
		t.Fatalf("Create bat_n: %v", err)
	}
	refN := domain.SourceRef{
		ID: "src_n", EntityKind: domain.EntityKindResource, EntityID: "res_c",
		BatchID: "bat_n", Workbook: "w.xlsx", Sheet: "s", RowNumber: 2,
		Raw: map[string]string{"k": "v"},
	}
	if err := db.SourceRefs.Insert(ctx, refN); err != nil {
		t.Fatalf("Insert src_n: %v", err)
	}
	refs, err = db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, "res_c")
	if err != nil {
		t.Fatalf("ListByEntity after delete+insert: %v", err)
	}
	gotOrder = []string{refs[0].ID, refs[1].ID, refs[2].ID}
	wantOrder = []string{"src_z", "src_m", "src_n"}
	if !slices.Equal(gotOrder, wantOrder) {
		t.Errorf("ref order after delete+insert = %v, want %v (stable survivor order, new version last)", gotOrder, wantOrder)
	}
}
