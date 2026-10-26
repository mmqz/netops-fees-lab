package fees_test

// period_detect_test.go — issue #15's unit pins: the five-table
// classification (the issue's verified marker combos), the per-kind
// period probes (value column / year+month pair / column-name month),
// and the three-tier comparison matrix (A refuse with both values,
// B weak, C manual, override recorded).
import (
	"strings"
	"testing"

	"github.com/mmqz/netops-fees-lab/internal/fees"
)

func rowsOf(cells ...string) []fees.SourceRow {
	return []fees.SourceRow{{Number: 2, Cells: cells}}
}

func TestClassifyTableKind(t *testing.T) {
	cases := []struct {
		name    string
		headers []string
		want    fees.TableKind
	}{
		{"tower settlement", []string{"账期月份", "需求确认单编号", "产品业务确认单编号", "站址编码", "金额"}, fees.TableTowerSettlement},
		{"payroll", []string{"员工工资上传批次", "业务账期", "1.8其他", "站址编码"}, fees.TablePayroll},
		{"wage items", []string{"年份", "月份", "绩效基数", "站址编码"}, fees.TableWageItems},
		{"electricity ledger", []string{"铁塔公司站点编码", "电表编号", "1月应付电费"}, fees.TableElectricityLedger},
		{"perf sheet", []string{"报账人", "选择开支人员", "清单费用合计", "日期"}, fees.TablePerfSheet},
		{"unknown generic", []string{"站址编码", "电费(元)", "账期"}, fees.TableUnknown},
		{"partial markers never classify", []string{"账期月份", "需求确认单编号", "站址编码"}, fees.TableUnknown},
	}
	for _, c := range cases {
		if got := fees.ClassifyTableKind(c.headers); got != c.want {
			t.Errorf("%s: classify = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDetectPeriodTowersAndPayroll(t *testing.T) {
	// 铁塔结算详单: the dedicated 账期月份 column — A档 strong.
	headers := []string{"账期月份", "需求确认单编号", "产品业务确认单编号", "站址编码", "金额"}
	rows := []fees.SourceRow{
		{Number: 2, Cells: []string{"202609", "OD-1", "P-1", "S-1", "100"}},
		{Number: 3, Cells: []string{"2026-09", "OD-2", "P-2", "S-2", "200"}},
	}
	det := fees.DetectPeriod(headers, rows)
	if det.Kind != fees.TableTowerSettlement || det.Confidence != fees.PeriodStrong {
		t.Fatalf("tower detection = %+v, want strong tower-settlement", det)
	}
	if det.Year != 2026 || det.Month != 9 {
		t.Fatalf("tower period = %d-%d, want 2026-09", det.Year, det.Month)
	}
	if !strings.Contains(det.Basis, "账期月份") || !strings.Contains(det.Basis, "2 行一致") {
		t.Fatalf("tower basis = %q, want the column and agreement count", det.Basis)
	}

	// 综维工资单: 业务账期 — the same A档 shape (the issue's 30-agree proof).
	headers = []string{"员工工资上传批次", "业务账期", "1.8其他"}
	rows = rowsOf("B-01", "202609", "5.00")
	det = fees.DetectPeriod(headers, rows)
	if det.Kind != fees.TablePayroll || det.Confidence != fees.PeriodStrong || det.Year != 2026 || det.Month != 9 {
		t.Fatalf("payroll detection = %+v", det)
	}
}

func TestDetectPeriodWageItemsAndLedger(t *testing.T) {
	// 导入工资项: 年份+月份 pair — A档 strong, full period.
	det := fees.DetectPeriod([]string{"年份", "月份", "绩效基数"}, rowsOf("2026", "09", "10.00"))
	if det.Kind != fees.TableWageItems || det.Confidence != fees.PeriodStrong {
		t.Fatalf("wage-items detection = %+v", det)
	}
	if det.Year != 2026 || det.Month != 9 {
		t.Fatalf("wage-items period = %d-%d, want 2026-09", det.Year, det.Month)
	}

	// 电费台账: the month rides the「N月应付电费」column NAME — B档 weak,
	// year unknown (0).
	det = fees.DetectPeriod([]string{"铁塔公司站点编码", "电表编号", "1月应付电费", "起始读数"},
		rowsOf("S-1", "M-1", "110.50", "100"))
	if det.Kind != fees.TableElectricityLedger || det.Confidence != fees.PeriodWeak {
		t.Fatalf("ledger detection = %+v", det)
	}
	if det.Month != 1 || det.Year != 0 {
		t.Fatalf("ledger period = %d-%d, want month 1 year 0", det.Year, det.Month)
	}
	if !strings.Contains(det.Basis, "1月应付电费") {
		t.Fatalf("ledger basis = %q, want the column name", det.Basis)
	}

	// A yearly ledger (several month columns) is genuinely ambiguous: weak
	// with no month and the candidates listed.
	det = fees.DetectPeriod([]string{"铁塔公司站点编码", "电表编号", "1月应付电费", "2月应付电费", "3月应付电费"}, nil)
	if det.Confidence != fees.PeriodWeak || det.Month != 0 {
		t.Fatalf("ambiguous ledger detection = %+v, want weak month-0", det)
	}
	if !strings.Contains(det.Basis, "3 个") || !strings.Contains(det.Detail, "候选列") {
		t.Fatalf("ambiguity basis/detail = %q / %q, want the candidate count and list", det.Basis, det.Detail)
	}
}

func TestDetectPeriodPerfSheetAndUnknown(t *testing.T) {
	// 综维绩效单: no probe at all — C档 none, the basis names the ruling.
	det := fees.DetectPeriod([]string{"报账人", "选择开支人员", "清单费用合计", "日期"},
		rowsOf("张三", "李四", "100.00", "2026.6.29"))
	if det.Kind != fees.TablePerfSheet || det.Confidence != fees.PeriodNone {
		t.Fatalf("perf detection = %+v, want none", det)
	}
	if !strings.Contains(det.Basis, "填表时间") {
		t.Fatalf("perf basis = %q, want the filing-time ruling", det.Basis)
	}
	// The 日期 column itself is NEVER a probe — the detection stays none
	// whatever the date values say.
	if det.Year != 0 || det.Month != 0 {
		t.Fatalf("perf period = %d-%d, want 0-0 (filing time is not period evidence)", det.Year, det.Month)
	}

	// Unknown table: C档 too, with its own basis wording.
	det = fees.DetectPeriod([]string{"站址编码", "电费(元)"}, nil)
	if det.Kind != fees.TableUnknown || det.Confidence != fees.PeriodNone {
		t.Fatalf("unknown detection = %+v", det)
	}
}

func TestCheckPeriodMatrix(t *testing.T) {
	strong202609 := fees.PeriodDetection{Year: 2026, Month: 9, Confidence: fees.PeriodStrong, Basis: "「业务账期」列（30行一致）"}
	weakMonth9 := fees.PeriodDetection{Month: 9, Confidence: fees.PeriodWeak, Basis: "列名「9月应付电费」"}
	weakAmbiguous := fees.PeriodDetection{Confidence: fees.PeriodWeak, Basis: "3 个「N月应付电费」列并存"}
	none := fees.PeriodDetection{Confidence: fees.PeriodNone, Basis: "无账期字段"}

	// A档 match → auto, no refusal.
	out := fees.CheckPeriod(strong202609, 2026, 9, false)
	if out.Refuse || out.Source != fees.PeriodSourceAuto {
		t.Fatalf("A match: %+v, want auto pass", out)
	}
	// A档 mismatch → REFUSE with both values in the message.
	out = fees.CheckPeriod(strong202609, 2026, 8, false)
	if !out.Refuse {
		t.Fatal("A mismatch must refuse")
	}
	if !strings.Contains(out.Message, "2026-09") || !strings.Contains(out.Message, "2026-08") {
		t.Fatalf("A mismatch message must carry both values: %q", out.Message)
	}
	// A档 mismatch + override → proceed, recorded.
	out = fees.CheckPeriod(strong202609, 2026, 8, true)
	if out.Refuse {
		t.Fatal("override must clear the refusal")
	}
	if out.Source != fees.PeriodSourceUnverified || !strings.Contains(out.Message, "人工裁决") {
		t.Fatalf("override: %+v, want unverified + recorded", out)
	}

	// B档 month match → unverified (low confidence by definition).
	out = fees.CheckPeriod(weakMonth9, 2026, 9, false)
	if out.Refuse || out.Source != fees.PeriodSourceUnverified {
		t.Fatalf("B match: %+v, want unverified pass", out)
	}
	// B档 month mismatch → refuse (weak basis named).
	out = fees.CheckPeriod(weakMonth9, 2026, 10, false)
	if !out.Refuse || !strings.Contains(out.Message, "弱识别") {
		t.Fatalf("B mismatch: %+v, want refusal with the weak basis named", out)
	}
	// B档 ambiguous → nothing comparable, unverified, no refusal.
	out = fees.CheckPeriod(weakAmbiguous, 2026, 9, false)
	if out.Refuse || out.Source != fees.PeriodSourceUnverified {
		t.Fatalf("B ambiguous: %+v, want unverified pass", out)
	}

	// C档 → manual, never refused, the reason recorded.
	out = fees.CheckPeriod(none, 2026, 9, false)
	if out.Refuse || out.Source != fees.PeriodSourceManual {
		t.Fatalf("C: %+v, want manual pass", out)
	}
	if !strings.Contains(out.Message, "人工指定") {
		t.Fatalf("C message = %q, want the manual ruling", out.Message)
	}
}
