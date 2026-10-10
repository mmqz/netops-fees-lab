package repo

import (
	"context"
	"fmt"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

func TestRowResultInsertBatchAndCounts(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedDevices(t, db, 1) // creates bat_1

	results := make([]domain.RowResult, 0, 30)
	for i := 2; i <= 31; i++ { // Excel rows 2..31, one sheet
		rr := domain.RowResult{
			BatchID:   "bat_1",
			Sheet:     "设备清单",
			RowNumber: i,
			Status:    domain.RowStatusImported,
			EntityID:  fmt.Sprintf("res_%03d", i-1),
		}
		switch i {
		case 11:
			rr.Status, rr.RuleCode, rr.Message = domain.RowStatusFailed, domain.RuleMissingRequiredField, "device_name is empty"
			rr.EntityID = ""
		case 12:
			rr.Status, rr.RuleCode, rr.Message = domain.RowStatusFailed, domain.RuleInvalidFormat, "mgmt_ip 192.168.1.300: octet out of range"
			rr.EntityID = ""
		case 13:
			rr.RuleCode, rr.Message = domain.RuleUnknownStatusValue, "status 未知 not in enum"
		case 32:
			rr.Status, rr.RuleCode = domain.RowStatusSkipped, domain.RuleDuplicateKey
		}
		results = append(results, rr)
	}

	if err := db.RowResults.InsertBatch(ctx, results); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	// Counts by batch.
	counts, err := db.RowResults.CountsByBatch(ctx, "bat_1")
	if err != nil {
		t.Fatalf("CountsByBatch: %v", err)
	}
	if counts[domain.RowStatusImported] != 28 || counts[domain.RowStatusFailed] != 2 || counts[domain.RowStatusSkipped] != 0 {
		t.Errorf("counts = %v, want imported=28 failed=2", counts)
	}

	// List ordered by (sheet, row_number), full roundtrip.
	got, total, err := db.RowResults.List(ctx, RowResultFilter{BatchID: "bat_1", Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 30 || len(got) != 30 {
		t.Fatalf("List total=%d len=%d, want 30/30", total, len(got))
	}
	if got[0].ID == 0 {
		t.Error("autoincrement id must be populated on read")
	}
	if got[0].RowNumber != 2 || got[29].RowNumber != 31 {
		t.Errorf("row numbers = %d..%d, want 2..31", got[0].RowNumber, got[29].RowNumber)
	}
	if got[9].Status != domain.RowStatusFailed || got[9].RuleCode != domain.RuleMissingRequiredField || got[9].EntityID != "" {
		t.Errorf("row 11 (index 9) = %+v", got[9])
	}
	if got[11].Status != domain.RowStatusImported || got[11].RuleCode != domain.RuleUnknownStatusValue {
		t.Errorf("row 13 (index 11) = %+v, want imported with W002", got[11])
	}

	// Status filter + pagination.
	failed, total, err := db.RowResults.List(ctx, RowResultFilter{BatchID: "bat_1", Status: domain.RowStatusFailed, Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("List(failed): %v", err)
	}
	if total != 2 || len(failed) != 1 || failed[0].RowNumber != 12 {
		t.Errorf("List(failed, page 2) = total %d, rows %d, first row %d; want 2/1/12", total, len(failed), failed[0].RowNumber)
	}

	// Sheet filter.
	other := domain.RowResult{BatchID: "bat_1", Sheet: "地址池", RowNumber: 2, Status: domain.RowStatusImported, EntityID: "res_p1"}
	if err := db.RowResults.InsertBatch(ctx, []domain.RowResult{other}); err != nil {
		t.Fatalf("InsertBatch(other sheet): %v", err)
	}
	bySheet, total, err := db.RowResults.List(ctx, RowResultFilter{BatchID: "bat_1", Sheet: "地址池", Limit: 100})
	if err != nil || total != 1 || len(bySheet) != 1 || bySheet[0].Sheet != "地址池" {
		t.Errorf("List(sheet=地址池) = %d/%d, %v", total, len(bySheet), err)
	}

	// Invalid status rejected up front.
	if err := db.RowResults.InsertBatch(ctx, []domain.RowResult{{BatchID: "bat_1", Sheet: "s", RowNumber: 1, Status: domain.RowStatus("pending")}}); err == nil {
		t.Error("InsertBatch with invalid status must fail")
	}

	// Empty insert is a no-op.
	if err := db.RowResults.InsertBatch(ctx, nil); err != nil {
		t.Errorf("InsertBatch(nil) = %v, want nil", err)
	}
}

// TestRowResultInsertBatchChunks exercises the multi-statement chunking
// path with more than rowResultChunkRows rows.
func TestRowResultInsertBatchChunks(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedDevices(t, db, 1)

	n := rowResultChunkRows + 5
	results := make([]domain.RowResult, n)
	for i := range results {
		results[i] = domain.RowResult{
			BatchID:   "bat_1",
			Sheet:     "设备清单",
			RowNumber: i + 2,
			Status:    domain.RowStatusImported,
			EntityID:  fmt.Sprintf("res_%05d", i),
		}
	}
	if err := db.RowResults.InsertBatch(ctx, results); err != nil {
		t.Fatalf("InsertBatch(%d): %v", n, err)
	}
	got, total, err := db.RowResults.List(ctx, RowResultFilter{BatchID: "bat_1", Limit: -1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != n || len(got) != n {
		t.Fatalf("total=%d len=%d, want %d", total, len(got), n)
	}
	if got[0].RowNumber != 2 || got[n-1].RowNumber != n+1 {
		t.Errorf("row numbers = %d..%d, want 2..%d", got[0].RowNumber, got[n-1].RowNumber, n+1)
	}
}

// TestRowResultCountByBatchMatchesList pins the COUNT aggregate used by the
// rollback route's delete-response accounting (R2-P3-2): CountByBatch must
// equal the number of rows a full List would return for the same batch.
func TestRowResultCountByBatchMatchesList(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	seedDevices(t, db, 1)

	results := make([]domain.RowResult, 0, 9)
	for i := 2; i <= 10; i++ {
		results = append(results, domain.RowResult{
			BatchID:   "bat_1",
			Sheet:     "设备清单",
			RowNumber: i,
			Status:    domain.RowStatusImported,
			EntityID:  fmt.Sprintf("res_%03d", i-1),
		})
	}
	if err := db.RowResults.InsertBatch(ctx, results); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	n, err := db.RowResults.CountByBatch(ctx, "bat_1")
	if err != nil {
		t.Fatalf("CountByBatch: %v", err)
	}
	listed, total, err := db.RowResults.List(ctx, RowResultFilter{BatchID: "bat_1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if n != len(listed) || n != total || n != 9 {
		t.Errorf("CountByBatch = %d, List = %d rows / total %d, want all 9", n, len(listed), total)
	}
	if n, err := db.RowResults.CountByBatch(ctx, "bat_none"); err != nil || n != 0 {
		t.Errorf("CountByBatch(missing batch) = %d, %v; want 0", n, err)
	}
}
