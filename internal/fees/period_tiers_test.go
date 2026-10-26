package fees

// period_tiers_test.go — issue #15's service-level pins: the three-tier
// enforcement rides EVERY import path (single-sheet + multi-sheet), the
// batch rows carry period_source/period_basis, the override is recorded,
// and the perf-sheet's filing-date column never governs the period.
import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/testsupport"
)

func newFeeService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := testsupport.TempDir(t)
	db, err := repo.Open(dir + "/netops.db")
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewService(db, dir), dir
}

// payrollCSVFee builds the 综维工资单 shape (A档: 业务账期 carries the period).
func payrollCSVFee(t *testing.T, dir, name, period string) string {
	t.Helper()
	return writeFeeCSVP(t, dir, name,
		"站址编码,员工工资上传批次,业务账期,1.8其他,金额\n"+
			"HLJ-01,B-01,"+period+",5.00,10000\n"+
			"HLJ-02,B-02,"+period+",3.00,20000\n")
}

// writeFeeCSVP is the local CSV writer (the fees_test-package sibling of
// ledger_columns_test.go's helper lives in another package).
func writeFeeCSVP(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	return path
}

func TestPeriodTierARefusesMismatchWithBothValues(t *testing.T) {
	svc, dir := newFeeService(t)
	path := payrollCSVFee(t, dir, "payroll-08.csv", "202608")
	_, err := svc.Import(t.Context(), ImportOptions{
		Path: path, FeeType: FeeTypeMaintenance, Year: 2026, Month: 9,
	})
	if err == nil {
		t.Fatal("A档 mismatch must refuse the import")
	}
	if !strings.Contains(err.Error(), "2026-08") || !strings.Contains(err.Error(), "2026-09") {
		t.Fatalf("refusal must carry both values: %v", err)
	}
	if !strings.Contains(err.Error(), "业务账期") {
		t.Fatalf("refusal must name the evidence basis: %v", err)
	}
}

func TestPeriodTierAMatchRecordsAuto(t *testing.T) {
	svc, dir := newFeeService(t)
	path := payrollCSVFee(t, dir, "payroll-09.csv", "202609")
	sum, err := svc.Import(t.Context(), ImportOptions{
		Path: path, FeeType: FeeTypeMaintenance, Year: 2026, Month: 9,
	})
	if err != nil {
		t.Fatalf("A档 match import: %v", err)
	}
	if sum.PeriodSource != PeriodSourceAuto {
		t.Fatalf("periodSource = %q, want auto", sum.PeriodSource)
	}
	if !strings.Contains(sum.PeriodBasis, "业务账期") || !strings.Contains(sum.PeriodBasis, "一致") {
		t.Fatalf("periodBasis = %q, want the evidence", sum.PeriodBasis)
	}
	// The batch row carries the same provenance (the durable record).
	batches, err := svc.List(t.Context(), "")
	if err != nil || len(batches) == 0 {
		t.Fatalf("list: %v (%d)", err, len(batches))
	}
	if batches[0].PeriodSource != PeriodSourceAuto || batches[0].PeriodBasis == "" {
		t.Fatalf("batch provenance = %q / %q", batches[0].PeriodSource, batches[0].PeriodBasis)
	}
}

func TestPeriodTierBWeakAndOverride(t *testing.T) {
	svc, dir := newFeeService(t)
	// 电费台账 shape: the month rides the column name (B档 weak).
	path := writeFeeCSVP(t, dir, "ledger-01.csv",
		"铁塔公司站点编码,电表编号,1月应付电费\nHLJ-01,M-01,110.50\n")
	sum, err := svc.Import(t.Context(), ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 1,
	})
	if err != nil {
		t.Fatalf("B档 month match import: %v", err)
	}
	if sum.PeriodSource != PeriodSourceUnverified {
		t.Fatalf("B档 match periodSource = %q, want unverified", sum.PeriodSource)
	}

	// B档 mismatch refuses; the override proceeds and records.
	path = writeFeeCSVP(t, dir, "ledger-02.csv",
		"铁塔公司站点编码,电表编号,2月应付电费\nHLJ-02,M-02,220.50\n")
	_, err = svc.Import(t.Context(), ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 5,
	})
	if err == nil || !strings.Contains(err.Error(), "2月应付电费") {
		t.Fatalf("B档 mismatch must refuse with the weak basis: %v", err)
	}
	sum, err = svc.Import(t.Context(), ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 5, TrustDeclaredPeriod: true,
	})
	if err != nil {
		t.Fatalf("override import: %v", err)
	}
	if sum.PeriodSource != PeriodSourceUnverified || !strings.Contains(sum.PeriodBasis, "人工裁决") {
		t.Fatalf("override provenance = %q / %q, want unverified + recorded override", sum.PeriodSource, sum.PeriodBasis)
	}
}

func TestPeriodTierCManualOnPlainFile(t *testing.T) {
	svc, dir := newFeeService(t)
	path := writeFeeCSVP(t, dir, "plain.csv", "站址编码,电费(元)\nHLJ-01,10000\n")
	sum, err := svc.Import(t.Context(), ImportOptions{
		Path: path, FeeType: FeeTypeElectricity, Year: 2026, Month: 9,
	})
	if err != nil {
		t.Fatalf("C档 import: %v", err)
	}
	if sum.PeriodSource != PeriodSourceManual {
		t.Fatalf("C档 periodSource = %q, want manual", sum.PeriodSource)
	}
	if !strings.Contains(sum.PeriodBasis, "人工指定") {
		t.Fatalf("C档 basis = %q, want the manual ruling", sum.PeriodBasis)
	}
}

func TestPeriodPerfSheetFilingDateNeverGoverns(t *testing.T) {
	svc, dir := newFeeService(t)
	// The 综维绩效单 shape: the 日期 column carries FILING times
	// (2026.6.29 / 2026.10.8 — the #15 measured shape) while the declared
	// period is 2026-09. The date mapping is dropped: every row lands in
	// the DECLARED period, and the basis records the ruling.
	path := writeFeeCSVP(t, dir, "perf.csv",
		"站址编码,报账人,选择开支人员,清单费用合计,日期\n"+
			"HLJ-01,张三,李四,1000.00,2026.6.29\n"+
			"HLJ-02,王五,赵六,2000.00,2026.10.8\n")
	sum, err := svc.Import(t.Context(), ImportOptions{
		Path: path, FeeType: FeeTypeMaintenance, Year: 2026, Month: 9,
	})
	if err != nil {
		t.Fatalf("perf-sheet import: %v", err)
	}
	if sum.RowsImported != 2 || sum.RowsFailed != 0 {
		t.Fatalf("perf rows = %d/%d, want 2/0 (%+v)", sum.RowsImported, sum.RowsFailed, sum)
	}
	if sum.PeriodSource != PeriodSourceManual {
		t.Fatalf("perf periodSource = %q, want manual (C档 — no period field)", sum.PeriodSource)
	}
	if !strings.Contains(sum.PeriodBasis, "填表时间") || !strings.Contains(sum.PeriodBasis, "不参与账期解析") {
		t.Fatalf("perf basis = %q, want the filing-date ruling", sum.PeriodBasis)
	}
	// The batch's canonical mapping must NOT carry the dropped date column.
	batches, err := svc.List(t.Context(), "")
	if err != nil || len(batches) != 1 {
		t.Fatalf("list: %v (%d)", err, len(batches))
	}
	if strings.Contains(batches[0].Mapping, "日期") {
		t.Fatalf("canonical mapping still carries the dropped date column: %s", batches[0].Mapping)
	}
}

func TestPeriodTierSheetsChannelPerSheet(t *testing.T) {
	// The multi-sheet channel runs the same detection PER SHEET: a mixed
	// workbook (an A档-consistent sheet + a mismatching one) refuses the
	// whole import BEFORE anything lands (the all-or-nothing guard), and
	// the override rides every sheet.
	svc, dir := newFeeService(t)
	// Two single-sheet CSVs cannot test the sheets channel (CSV refuses);
	// a two-sheet xlsx with distinct period evidence is the honest shape —
	// built via the same helper the #125 tests use.
	path := writeLedgerXLSX(t, dir, "mixed.xlsx", []sheetSpec{
		{name: "综维工资单A", rows: [][]string{
			{"站址编码", "员工工资上传批次", "业务账期", "1.8其他", "金额"},
			{"HLJ-01", "B-01", "202609", "5.00", "10000"},
		}},
		{name: "综维工资单B", rows: [][]string{
			{"站址编码", "员工工资上传批次", "业务账期", "1.8其他", "金额"},
			{"HLJ-02", "B-02", "202610", "5.00", "20000"},
		}},
	})
	if _, err := svc.ImportSheets(t.Context(), SheetsImportOptions{
		Path: path, OriginalName: "mixed.xlsx", Year: 2026, Month: 9,
	}); err == nil {
		t.Fatal("the mismatching sheet (2026-10 vs declared 2026-09) must refuse the workbook")
	} else if !strings.Contains(err.Error(), "2026-10") {
		t.Fatalf("sheets refusal must carry the offending period: %v", err)
	}
	// Override: the workbook lands — the CONSISTENT sheet records auto
	// (its evidence still agrees), the OVERRIDDEN one unverified.
	sum, err := svc.ImportSheets(t.Context(), SheetsImportOptions{
		Path: path, OriginalName: "mixed.xlsx", Year: 2026, Month: 9, TrustDeclaredPeriod: true,
	})
	if err != nil {
		t.Fatalf("override sheets import: %v", err)
	}
	bySource := map[string]int{}
	for _, b := range sum.Batches {
		bySource[b.PeriodSource]++
	}
	if bySource[PeriodSourceAuto] != 1 || bySource[PeriodSourceUnverified] != 1 {
		t.Fatalf("sheet periodSources = %v, want 1 auto + 1 unverified", bySource)
	}
	for _, b := range sum.Batches {
		if b.PeriodSource == PeriodSourceUnverified && !strings.Contains(b.PeriodBasis, "人工裁决") {
			t.Fatalf("overridden sheet basis = %q, want the recorded override", b.PeriodBasis)
		}
	}
}
