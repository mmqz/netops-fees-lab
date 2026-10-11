package repo

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

func TestBatchCRUDRoundtrip(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	started := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)

	b := newBatch("bat_1", started)
	b.WorkbookName = "ippools.xlsx"
	b.WorkbookSHA256 = "deadbeef"
	if err := db.Batches.Create(ctx, b); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := db.Batches.Get(ctx, "bat_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "bat_1" || got.WorkbookName != "ippools.xlsx" || got.WorkbookSHA256 != "deadbeef" {
		t.Errorf("Get mismatch: %+v", got)
	}
	if got.Status != domain.BatchStatusAnalyzing || got.RuleVersion != domain.RuleVersion {
		t.Errorf("status/rule version mismatch: %+v", got)
	}
	if !got.StartedAt.Equal(started) {
		t.Errorf("StartedAt = %v, want %v (UTC RFC3339 roundtrip)", got.StartedAt, started)
	}
	if got.FinishedAt != nil {
		t.Errorf("FinishedAt = %v, want nil", got.FinishedAt)
	}
	if got.Mapping != "{}" {
		t.Errorf("Mapping = %q, want \"{}\"", got.Mapping)
	}

	// Missing batch → domain.ErrNotFound.
	if _, err := db.Batches.Get(ctx, "bat_missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get(missing) = %v, want domain.ErrNotFound", err)
	}

	// Update status: non-terminal must not stamp finished_at.
	if err := db.Batches.UpdateStatus(ctx, "bat_1", domain.BatchStatusAwaitingMapping); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, _ = db.Batches.Get(ctx, "bat_1")
	if got.Status != domain.BatchStatusAwaitingMapping || got.FinishedAt != nil {
		t.Errorf("after awaiting_mapping: %+v", got)
	}

	// SetMapping.
	if err := db.Batches.SetMapping(ctx, "bat_1", `{"sheets":[]}`); err != nil {
		t.Fatalf("SetMapping: %v", err)
	}
	got, _ = db.Batches.Get(ctx, "bat_1")
	if got.Mapping != `{"sheets":[]}` {
		t.Errorf("Mapping = %q", got.Mapping)
	}

	// UpdateCounts.
	if err := db.Batches.UpdateCounts(ctx, "bat_1", BatchCounts{Total: 20, Imported: 16, Skipped: 0, Failed: 4}); err != nil {
		t.Fatalf("UpdateCounts: %v", err)
	}
	got, _ = db.Batches.Get(ctx, "bat_1")
	if got.RowsTotal != 20 || got.RowsImported != 16 || got.RowsSkipped != 0 || got.RowsFailed != 4 {
		t.Errorf("counts mismatch: %+v", got)
	}

	// Terminal status stamps finished_at exactly once.
	if err := db.Batches.UpdateStatus(ctx, "bat_1", domain.BatchStatusCompleted); err != nil {
		t.Fatalf("UpdateStatus(completed): %v", err)
	}
	got, _ = db.Batches.Get(ctx, "bat_1")
	if got.FinishedAt == nil {
		t.Fatal("finished_at not stamped on completion")
	}
	first := *got.FinishedAt
	if err := db.Batches.UpdateStatus(ctx, "bat_1", domain.BatchStatusRolledBack); err != nil {
		t.Fatalf("UpdateStatus(rolled_back): %v", err)
	}
	got, _ = db.Batches.Get(ctx, "bat_1")
	if got.Status != domain.BatchStatusRolledBack || got.FinishedAt == nil || !got.FinishedAt.Equal(first) {
		t.Errorf("after rolled_back: status=%s finishedAt=%v, want rolled_back and original timestamp", got.Status, got.FinishedAt)
	}

	// MarkFailed on a fresh batch.
	b2 := newBatch("bat_2", started.Add(time.Minute))
	b2.Status = domain.BatchStatusImporting
	if err := db.Batches.Create(ctx, b2); err != nil {
		t.Fatalf("Create bat_2: %v", err)
	}
	if err := db.Batches.MarkFailed(ctx, "bat_2", "boom at sheet 2"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, _ = db.Batches.Get(ctx, "bat_2")
	if got.Status != domain.BatchStatusFailed || got.Error != "boom at sheet 2" || got.FinishedAt == nil {
		t.Errorf("after MarkFailed: %+v", got)
	}

	// List desc (bat_2 started later than bat_1).
	list, total, err := db.Batches.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("List total=%d len=%d, want 2/2", total, len(list))
	}
	if list[0].ID != "bat_2" || list[1].ID != "bat_1" {
		t.Errorf("List order = [%s, %s], want [bat_2, bat_1]", list[0].ID, list[1].ID)
	}

	// Pagination.
	page, total, err := db.Batches.List(ctx, 1, 1)
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if total != 2 || len(page) != 1 || page[0].ID != "bat_1" {
		t.Errorf("List(1,1) = %d/%v", total, ids(listOfIDs(page)))
	}

	// Updates on missing ids → ErrNotFound.
	if err := db.Batches.UpdateStatus(ctx, "bat_none", domain.BatchStatusFailed); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateStatus(missing) = %v, want domain.ErrNotFound", err)
	}
	if err := db.Batches.MarkFailed(ctx, "bat_none", "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("MarkFailed(missing) = %v, want domain.ErrNotFound", err)
	}
	if err := db.Batches.SetMapping(ctx, "bat_none", "{}"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("SetMapping(missing) = %v, want domain.ErrNotFound", err)
	}
	if err := db.Batches.UpdateCounts(ctx, "bat_none", BatchCounts{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateCounts(missing) = %v, want domain.ErrNotFound", err)
	}

	// Invalid status is rejected.
	if err := db.Batches.UpdateStatus(ctx, "bat_1", domain.BatchStatus("paused")); err == nil {
		t.Error("UpdateStatus(invalid) succeeded, want error")
	}
}

// listOfIDs / ids are tiny helpers to keep failure messages readable.
func listOfIDs(bs []domain.ImportBatch) []domain.ImportBatch { return bs }
func ids(bs []domain.ImportBatch) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}

// TestBatchCountByStatus pins the W14-D COUNT aggregate: real SQL GROUP BY
// counts per batch status; an empty table answers an empty map.
func TestBatchCountByStatus(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	counts, err := db.Batches.CountByStatus(ctx)
	if err != nil {
		t.Fatalf("CountByStatus on empty: %v", err)
	}
	if len(counts) != 0 {
		t.Errorf("empty table counts = %v, want empty map", counts)
	}

	completed := newBatch("bat_cs1", simTS)
	completed.Status = domain.BatchStatusCompleted
	completed2 := newBatch("bat_cs2", simTS)
	completed2.Status = domain.BatchStatusCompleted
	failed := newBatch("bat_cs3", simTS)
	failed.Status = domain.BatchStatusFailed
	rolled := newBatch("bat_cs4", simTS)
	rolled.Status = domain.BatchStatusRolledBack
	for _, b := range []domain.ImportBatch{completed, completed2, failed, rolled} {
		if err := db.Batches.Create(ctx, b); err != nil {
			t.Fatalf("Create %s: %v", b.ID, err)
		}
	}

	counts, err = db.Batches.CountByStatus(ctx)
	if err != nil {
		t.Fatalf("CountByStatus: %v", err)
	}
	want := map[domain.BatchStatus]int{
		domain.BatchStatusCompleted:  2,
		domain.BatchStatusFailed:     1,
		domain.BatchStatusRolledBack: 1,
	}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("counts = %v, want %v", counts, want)
	}
}
