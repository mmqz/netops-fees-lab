//go:build duckdb

package fees

// details_duckdb_test.go — the reconcile's REAL system side (the tagged
// build's half of the acceptance): the engine answers the analysis-layer
// amount of the link triple; the delta is computed and the hint text
// states the doctrine (差额只提示不阻断——明细永不覆盖系统表).

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDuckReconcileDetailsRealSystemSide(t *testing.T) {
	svc, _, dir := newService(t)
	ctx := context.Background()
	e := newQueryEngine()

	// system side: HLJ-0001 @2026-08 = 10000.00.
	body := "站址编码,账期,电费(元)\nHLJ-0001,2026-08,10000.00\nHLJ-0002,2026-08,500.00\n"
	fixture := filepath.Join(dir, "recon.csv")
	if err := os.WriteFile(fixture, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Import(ctx, ImportOptions{Path: fixture, FeeType: FeeTypeElectricity, Year: 2026, Month: 8}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// no details: delta = −system (the 补齐 gap, honestly negative).
	rec, err := svc.ReconcileDetails(ctx, e, "HLJ-0001", "202608", "electricity")
	if err != nil {
		t.Fatalf("reconcile bare: %v", err)
	}
	if !rec.EngineCentsAvailable || rec.SystemCents == nil || *rec.SystemCents != 1_000_000 {
		t.Fatalf("bare system side = %+v", rec)
	}
	if rec.DeltaCents == nil || *rec.DeltaCents != -1_000_000 {
		t.Fatalf("bare delta = %+v", rec)
	}

	// two details 150.00 + 150.00: delta = 300.00 - 10000.00 = -9700.00.
	yuan := 150.00
	for _, name := range []string{"餐补一", "餐补二"} {
		if _, err := svc.AddDetail(ctx, DetailInput{LinkKey: "HLJ-0001", Period: "202608",
			FeeType: "electricity", Category: "加班餐补", AmountYuan: &yuan, ItemName: name}, "cli"); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}
	rec, err = svc.ReconcileDetails(ctx, e, "HLJ-0001", "202608", "electricity")
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rec.DetailSumCents != 30000 || rec.Rows != 2 {
		t.Fatalf("detail side = %+v", rec)
	}
	if rec.SystemCents == nil || *rec.SystemCents != 1_000_000 || rec.DeltaCents == nil || *rec.DeltaCents != -970_000 {
		t.Fatalf("reconcile = %+v", rec)
	}

	// equal: the delta reaches zero and the note says so.
	yuan = 9700.00
	if _, err := svc.AddDetail(ctx, DetailInput{LinkKey: "HLJ-0001", Period: "202608",
		FeeType: "electricity", Category: "线路改迁", AmountYuan: &yuan, ItemName: "改迁尾款"}, "cli"); err != nil {
		t.Fatalf("add final: %v", err)
	}
	rec, err = svc.ReconcileDetails(ctx, e, "HLJ-0001", "202608", "electricity")
	if err != nil {
		t.Fatalf("reconcile equal: %v", err)
	}
	if rec.DeltaCents == nil || *rec.DeltaCents != 0 {
		t.Fatalf("equal delta = %+v", rec)
	}

	// a key absent from the analysis layer: system side nil + the
	// 明细有、系统无 warning shape (never a fake zero).
	yuan = 10.00
	if _, err := svc.AddDetail(ctx, DetailInput{LinkKey: "HLJ-NOPE", Period: "202608",
		FeeType: "electricity", Category: "计件工资", AmountYuan: &yuan, ItemName: "x"}, "cli"); err != nil {
		t.Fatalf("add nope: %v", err)
	}
	rec, err = svc.ReconcileDetails(ctx, e, "HLJ-NOPE", "202608", "electricity")
	if err != nil {
		t.Fatalf("reconcile nope: %v", err)
	}
	if rec.SystemCents != nil || rec.DeltaCents != nil {
		t.Fatalf("absent system side = %+v — want nil (no fake zero)", rec)
	}
}
