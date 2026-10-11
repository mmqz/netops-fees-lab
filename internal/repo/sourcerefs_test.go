package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// countingDBTX wraps a DBTX and counts QueryContext calls — the query-count
// probe of the W9-D N+1 guard: resolving a page of N entities must cost ONE
// ListByEntityIDs query, not one per entity (the pre-W9-D shape of the MCP
// resource_search provenance resolution).
type countingDBTX struct {
	DBTX
	queries int
}

func (c *countingDBTX) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.queries++
	return c.DBTX.QueryContext(ctx, query, args...)
}

// TestSourceRefListByEntityIDs pins the batched read behind the W9-D N+1 fix:
// one call resolves the refs of every listed entity, grouped by entity id,
// each group in rowid (version) order — the same per-entity order
// ListByEntity guarantees; entities without refs stay absent from the map;
// an empty id list never touches the database. The counting wrapper asserts
// the whole resolution is ONE query (N+1 regression guard).
func TestSourceRefListByEntityIDs(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	_, _, _ = seedGraph(t, db)

	// res_a: two versions (bat_1, then bat_3); res_c: one ref; res_b: none.
	if err := db.Batches.Create(ctx, newBatch("bat_3", time.Now())); err != nil {
		t.Fatalf("Create bat_3: %v", err)
	}
	seed := []domain.SourceRef{
		{ID: "src_a1", EntityKind: domain.EntityKindResource, EntityID: "res_a", BatchID: "bat_1",
			Workbook: "devices.xlsx", Sheet: "设备清单", RowNumber: 2, Raw: map[string]string{"设备编码": "DEV-001"}},
		{ID: "src_a2", EntityKind: domain.EntityKindResource, EntityID: "res_a", BatchID: "bat_3",
			Workbook: "devices-v2.xlsx", Sheet: "设备清单", RowNumber: 2, Raw: map[string]string{"设备编码": "DEV-001"}},
		{ID: "src_c1", EntityKind: domain.EntityKindResource, EntityID: "res_c", BatchID: "bat_2",
			Workbook: "circuits.xlsx", Sheet: "电路", RowNumber: 7, Raw: map[string]string{"电路编号": "CKT-001"}},
	}
	for _, ref := range seed {
		if err := db.SourceRefs.Insert(ctx, ref); err != nil {
			t.Fatalf("Insert %s: %v", ref.ID, err)
		}
	}

	counter := &countingDBTX{DBTX: db.sql}
	batched := &SourceRefRepo{q: counter}

	got, err := batched.ListByEntityIDs(ctx, domain.EntityKindResource, []string{"res_a", "res_b", "res_c"})
	if err != nil {
		t.Fatalf("ListByEntityIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("result map has %d entities, want 2 (res_b has no refs and must be absent)", len(got))
	}
	if refs := got["res_a"]; len(refs) != 2 || refs[0].ID != "src_a1" || refs[1].ID != "src_a2" {
		t.Errorf("res_a refs = %+v, want [src_a1 src_a2] in rowid (version) order", refs)
	}
	if refs := got["res_c"]; len(refs) != 1 || refs[0].ID != "src_c1" || refs[0].Workbook != "circuits.xlsx" {
		t.Errorf("res_c refs = %+v, want the single circuits.xlsx ref", refs)
	}
	if _, present := got["res_b"]; present {
		t.Errorf("res_b must be absent from the map (no refs), got %+v", got["res_b"])
	}
	// The N+1 guard itself: three entities resolved in ONE query.
	if counter.queries != 1 {
		t.Errorf("ListByEntityIDs issued %d queries for 3 entities, want exactly 1 (the N+1 regression guard)", counter.queries)
	}

	// Empty ids: empty non-nil map, zero queries.
	counter.queries = 0
	got, err = batched.ListByEntityIDs(ctx, domain.EntityKindResource, nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("ListByEntityIDs(nil) = %v (err %v), want an empty non-nil map", got, err)
	}
	if counter.queries != 0 {
		t.Errorf("ListByEntityIDs(nil) issued %d queries, want 0 (early return)", counter.queries)
	}
}

// TestSourceRefListByEntityIDsChunksLargeIDSets pins the transparent
// chunking of id sets larger than maxINListVars: the result is the union of
// the per-chunk queries (correctness under the split), and the query count
// is ceil(N / maxINListVars) — still independent of the number of entities
// that actually carry refs.
func TestSourceRefListByEntityIDsChunksLargeIDSets(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if err := db.Batches.Create(ctx, newBatch("bat_bulk", time.Now())); err != nil {
		t.Fatalf("Create bat_bulk: %v", err)
	}
	n := maxINListVars + 2 // two full chunks + one remainder chunk
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("res_bulk_%04d", i)
		ref := domain.SourceRef{
			ID: fmt.Sprintf("src_bulk_%04d", i), EntityKind: domain.EntityKindResource, EntityID: ids[i],
			BatchID: "bat_bulk", Workbook: "bulk.xlsx", Sheet: "s", RowNumber: 2, Raw: map[string]string{"k": "v"},
		}
		if err := db.SourceRefs.Insert(ctx, ref); err != nil {
			t.Fatalf("Insert %s: %v", ref.ID, err)
		}
	}

	counter := &countingDBTX{DBTX: db.sql}
	batched := &SourceRefRepo{q: counter}
	got, err := batched.ListByEntityIDs(ctx, domain.EntityKindResource, ids)
	if err != nil {
		t.Fatalf("ListByEntityIDs (chunked): %v", err)
	}
	if len(got) != n {
		t.Fatalf("chunked result has %d entities, want %d (the union of all chunks)", len(got), n)
	}
	wantChunks := (n + maxINListVars - 1) / maxINListVars
	if counter.queries != wantChunks {
		t.Errorf("chunked ListByEntityIDs issued %d queries for %d ids, want %d (chunked, still not per-entity)", counter.queries, n, wantChunks)
	}
}
