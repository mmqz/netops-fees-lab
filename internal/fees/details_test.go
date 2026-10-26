package fees

// details_test.go — #16's completion line item 6: the three mandates,
// the yuan/cents door forms, the duplicate fingerprint, the status flow
// and the reconcile hint discipline (差额只提示；engine 缺席时系统侧
// 诚实缺席，不造零).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

func detailInput() DetailInput {
	yuan := 150.00
	return DetailInput{
		LinkKey: "HLJ-0001", Period: "202608", FeeType: "electricity",
		Category: "加班餐补", AmountYuan: &yuan, ItemName: "抢修加班餐费补贴",
		Reason: "抢修", SourceFile: "绩效单.xlsx", SourceRow: 3,
	}
}

func TestServiceAddDetailThreeMandates(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	// 1. the link key: whose money — refused empty
	in := detailInput()
	in.LinkKey = "  "
	if _, err := svc.AddDetail(ctx, in, "cli"); !errors.Is(err, ErrImportRefused) {
		t.Fatalf("empty linkKey: %v — want refusal", err)
	}
	// 2. the period: which month — YYYYMM only
	in = detailInput()
	in.Period = "2026-08"
	if _, err := svc.AddDetail(ctx, in, "cli"); !errors.Is(err, ErrImportRefused) {
		t.Fatalf("dash period: %v — want refusal", err)
	}
	in.Period = "202613"
	if _, err := svc.AddDetail(ctx, in, "cli"); !errors.Is(err, ErrImportRefused) {
		t.Fatalf("month 13: %v — want refusal", err)
	}
	// 3. the amount: either door form; both set must agree
	in = detailInput()
	in.AmountYuan = nil
	if _, err := svc.AddDetail(ctx, in, "cli"); !errors.Is(err, ErrImportRefused) {
		t.Fatalf("no amount: %v — want refusal", err)
	}
	cents := int64(15001)
	in.AmountCents = &cents
	yuan := 150.02 // 15002 cents ≠ 15001
	in.AmountYuan = &yuan
	if _, err := svc.AddDetail(ctx, in, "cli"); !errors.Is(err, ErrImportRefused) {
		t.Fatalf("disagreeing forms: %v — want refusal", err)
	}
	yuan = 150.01 // agrees with 15001
	in.AmountYuan = &yuan
	got, err := svc.AddDetail(ctx, in, "cli")
	if err != nil {
		t.Fatalf("agreeing forms: %v", err)
	}
	if got.AmountCents != 15001 {
		t.Fatalf("amount = %d, want the exact cents form 15001", got.AmountCents)
	}
	// 4. the category: free-form (#16 允许自建) but never empty
	in = detailInput()
	in.Category = "本地自建类别-试验费"
	if _, err := svc.AddDetail(ctx, in, "cli"); err != nil {
		t.Fatalf("a local custom category must be accepted (#16): %v", err)
	}
	in.Category = " "
	if _, err := svc.AddDetail(ctx, in, "cli"); !errors.Is(err, ErrImportRefused) {
		t.Fatalf("empty category: %v — want refusal", err)
	}
	// 5. the provenance: no actor = no entry
	in = detailInput()
	if _, err := svc.AddDetail(ctx, in, ""); !errors.Is(err, ErrImportRefused) {
		t.Fatalf("no actor: %v — want refusal", err)
	}
	// 6. the happy path lands pending with every optional field
	in = detailInput()
	d, err := svc.AddDetail(ctx, in, "cli")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if d.Status != domain.FeeDetailStatusPending || d.CreatedBy != "cli" || d.AmountCents != 15000 {
		t.Fatalf("added = %+v", d)
	}
}

func TestServiceAddDetailDuplicateFingerprint(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	in := detailInput()
	if _, err := svc.AddDetail(ctx, in, "cli"); err != nil {
		t.Fatalf("first add: %v", err)
	}
	// the same fingerprint (link triple + category + amount + item)
	// answers the existing id — 铁律 5.
	second, err := svc.AddDetail(ctx, in, "cli")
	if err == nil || second.ID != "" {
		t.Fatalf("duplicate add = %+v, %v — want refusal", second, err)
	}
	if !errors.Is(err, ErrImportRefused) {
		t.Fatalf("duplicate refusal type = %v", err)
	}
	// a different item name is a different entry (the operator's escape)
	in2 := in
	in2.ItemName = "抢修加班餐费补贴（第二批）"
	if _, err := svc.AddDetail(ctx, in2, "cli"); err != nil {
		t.Fatalf("different item name must pass: %v", err)
	}
}

func TestServiceDetailStatusFlow(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()
	d, err := svc.AddDetail(ctx, detailInput(), "cli")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// rejected REQUIRES a reason — the 留痕 of the flow
	if err := svc.SetDetailStatus(ctx, d.ID, "rejected", "admin", ""); !errors.Is(err, ErrImportRefused) {
		t.Fatalf("reject without reason: %v — want refusal", err)
	}
	if err := svc.SetDetailStatus(ctx, d.ID, "bogus", "admin", ""); !errors.Is(err, ErrImportRefused) {
		t.Fatalf("foreign status: %v — want refusal", err)
	}
	if err := svc.SetDetailStatus(ctx, d.ID, "confirmed", "", "x"); !errors.Is(err, ErrImportRefused) {
		t.Fatalf("no verifier: %v — want refusal", err)
	}
	if err := svc.SetDetailStatus(ctx, d.ID, "rejected", "admin", "重复填报"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	got, err := svc.DetailByID(ctx, d.ID)
	if err != nil {
		t.Fatalf("reget: %v", err)
	}
	if got.Status != "rejected" || got.VerifiedBy != "admin" || got.RejectReason != "重复填报" || got.VerifiedAt == nil {
		t.Fatalf("trail = %+v", got)
	}
	// unknown id keeps the ErrNotFound semantics
	if err := svc.SetDetailStatus(ctx, "fd_nope", "confirmed", "admin", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown id = %v — want ErrNotFound", err)
	}
}

func TestServiceReconcileDetails(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()

	// the fixture import puts HLJ-0001 @2026-08 at 10000.00 (system side).
	body := "站址编码,账期,电费(元)\nHLJ-0001,2026-08,10000.00\n"
	fixture := filepath.Join(dir, "det.csv")
	if err := os.WriteFile(fixture, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: fixture, FeeType: FeeTypeElectricity, Year: 2026, Month: 8}); err != nil {
		t.Fatalf("import fixture: %v", err)
	}

	// no details yet: sum 0 / rows 0
	rec, err := svc.ReconcileDetails(ctx, unavailableEngine{}, "HLJ-0001", "202608", "electricity")
	if err != nil {
		t.Fatalf("reconcile bare: %v", err)
	}
	if rec.DetailSumCents != 0 || rec.Rows != 0 || rec.SystemCents != nil {
		t.Fatalf("bare reconcile = %+v", rec)
	}

	// two details: 150.00 + 50.00 → the delta against 10000.00 = -8800.00
	y1 := 150.00
	if _, err := svc.AddDetail(ctx, DetailInput{LinkKey: "HLJ-0001", Period: "202608",
		FeeType: "electricity", Category: "加班餐补", AmountYuan: &y1, ItemName: "餐补一"}, "cli"); err != nil {
		t.Fatalf("add 1: %v", err)
	}
	y2 := 50.00
	if _, err := svc.AddDetail(ctx, DetailInput{LinkKey: "HLJ-0001", Period: "202608",
		FeeType: "electricity", Category: "低值易耗品", AmountYuan: &y2, ItemName: "卡钉"}, "cli"); err != nil {
		t.Fatalf("add 2: %v", err)
	}

	// default build (engine not built): the detail side answers, the
	// system side is HONESTLY absent — no fake zero, no 501.
	rec, err = svc.ReconcileDetails(ctx, unavailableEngine{}, "HLJ-0001", "202608", "electricity")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rec.DetailSumCents != 20000 || rec.Rows != 2 {
		t.Fatalf("detail side = %+v", rec)
	}
	if rec.EngineCentsAvailable || rec.SystemCents != nil || rec.DeltaCents != nil {
		t.Fatalf("system side must be honestly absent on the default build: %+v", rec)
	}
	if rec.Note == "" {
		t.Fatal("the honest-absent note must ride the envelope")
	}

	// the duckdb-tagged build answers the real system side (that half
	// belongs to the engine tests); here the unavailableEngine{} shape
	// itself pins the default-build contract.
}
