package repo

// migrations_v38_test.go — #16's completion line item 6: the fee detail
// layer. The v38 CHECK enums (status flow) and the entry-fingerprint
// duplicate probe are driven through the REAL repo; the chain reaches
// v38 from scratch (newTestDB).

import (
	"context"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

func TestMigrationV38FeeDetails(t *testing.T) {
	db := newTestDB(t) // migrates to head (v38) from an empty file
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

	qty := 30.0
	d := domain.FeeDetail{
		ID: "fd_v38", LinkKey: "W8522883", Period: "202609", FeeType: domain.FeeTypeMaintenance,
		Category: "低值易耗品", AmountCents: 23523, ItemName: "卡钉、胶带",
		Quantity: &qty, QuantityUnit: "个", SourceFile: "绩效单.xlsx", SourceRow: 12,
		Status: domain.FeeDetailStatusPending, CreatedBy: "cli", CreatedAt: now,
	}
	if err := db.FeeDetails.Insert(ctx, d); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// roundtrip: the optional pointer columns survive nil-absent
	got, err := db.FeeDetails.GetByID(ctx, "fd_v38")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LinkKey != "W8522883" || got.Period != "202609" || got.Category != "低值易耗品" ||
		got.AmountCents != 23523 || got.Status != domain.FeeDetailStatusPending || got.CreatedBy != "cli" {
		t.Fatalf("roundtrip = %+v", got)
	}
	if got.Quantity == nil || *got.Quantity != 30 {
		t.Fatalf("quantity pointer lost: %+v", got.Quantity)
	}
	if got.VerifiedAt != nil || got.VerifiedBy != "" {
		t.Fatalf("a pending row must carry no verification trail: %+v", got)
	}

	// the entry fingerprint: an exact duplicate answers the existing id
	dup, err := db.FeeDetails.ExactDuplicate(ctx, d)
	if err != nil || dup != "fd_v38" {
		t.Fatalf("duplicate probe = %q, %v — want fd_v38", dup, err)
	}
	// a different amount is NOT a duplicate
	other := d
	other.AmountCents = 999
	if dup, err := db.FeeDetails.ExactDuplicate(ctx, other); err != nil || dup != "" {
		t.Fatalf("different amount must not be a duplicate: %q %v", dup, err)
	}

	// the status CHECK: a foreign status token refuses at the DB floor
	if err := db.FeeDetails.SetStatus(ctx, "fd_v38", "bogus", "admin", "", now); err == nil {
		t.Fatal("a foreign status must hit the v38 CHECK refusal")
	}
	// the human channel flip carries the trail
	if err := db.FeeDetails.SetStatus(ctx, "fd_v38", domain.FeeDetailStatusRejected, "admin", "重复填报", now); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, err = db.FeeDetails.GetByID(ctx, "fd_v38")
	if err != nil {
		t.Fatalf("reget: %v", err)
	}
	if got.Status != domain.FeeDetailStatusRejected || got.VerifiedBy != "admin" ||
		got.VerifiedAt == nil || got.RejectReason != "重复填报" {
		t.Fatalf("verification trail = %+v", got)
	}

	// the filter list: link triple narrows, status narrows
	rows, err := db.FeeDetails.List(ctx, domain.FeeDetailFilter{LinkKey: "W8522883", Period: "202609"})
	if err != nil || len(rows) != 1 {
		t.Fatalf("filter list = %d rows, %v — want 1", len(rows), err)
	}
	rows, err = db.FeeDetails.List(ctx, domain.FeeDetailFilter{Status: domain.FeeDetailStatusPending})
	if err != nil || len(rows) != 0 {
		t.Fatalf("pending list after reject = %d rows, %v — want 0", len(rows), err)
	}

	// the evidence-side sum of the link triple
	sum, n, err := db.FeeDetails.SumCents(ctx, "W8522883", "202609", domain.FeeTypeMaintenance)
	if err != nil || sum != 23523 || n != 1 {
		t.Fatalf("sum = %d / %d, %v — want 23523 / 1", sum, n, err)
	}

	// the fee_type CHECK: a foreign fee type refuses
	bad := d
	bad.ID = "fd_v38b"
	bad.FeeType = domain.FeeType("groceries")
	if err := db.FeeDetails.Insert(ctx, bad); err == nil {
		t.Fatal("a foreign fee type must hit the v38 CHECK refusal")
	}
}
