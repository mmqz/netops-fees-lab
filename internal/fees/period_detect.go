package fees

// period_detect.go — issue #15: the declared period (—year/—month) was
// the ONLY period authority and the file's own content was never read
// for cross-checking. Real 4-table verification found three shapes: a
// dedicated period column (铁塔结算详单「账期月份」, 综维工资单「业务账期」,
// 导入工资项「年份」+「月份」), an indirect clue (电费台账's「N月应付电费」
// column NAME carries the month), and nothing at all (综维绩效单 — its
// 日期 column is the FILING time, not the expense period). A wrong
// declared period silently poisons every downstream month-over-month
// and trend analysis.
//
// The three-tier ruling of the issue:
//
//      A 档 strong — a dedicated period field exists: auto-detect, compare
//        with the declaration, MISMATCH REFUSES the import (both values in
//        the message). Match records period_source=auto.
//      B 档 weak — an indirect clue only (month from a column name): compare
//        what is comparable (the month), refuse on disagreement unless the
//        operator explicitly trusts the declaration; record
//        period_source=unverified with the low-confidence basis.
//      C 档 none — no period evidence in the content: the human declaration
//        is the only source; record period_source=manual with the reason.
//
// Detection never guesses: ambiguous evidence (several month columns)
// is reported as such and degrades to B档-unverified without blocking.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// TableKind is the recognized business-table family (issue #15's
// verified五类; the classifier is additive — unknown files degrade to
// C档, never mis-classify).
type TableKind string

const (
	TableTowerSettlement   TableKind = "tower-settlement" // 铁塔结算详单
	TablePayroll           TableKind = "payroll"          // 综维工资单
	TableWageItems         TableKind = "wage-items"       // 导入工资项
	TableElectricityLedger TableKind = "electricity-ledger"
	TablePerfSheet         TableKind = "perf-sheet" // 综维绩效单
	TableUnknown           TableKind = "unknown"
)

// tableKindLabels is the Chinese display name of each family.
var tableKindLabels = map[TableKind]string{
	TableTowerSettlement:   "铁塔结算详单",
	TablePayroll:           "综维工资单",
	TableWageItems:         "导入工资项",
	TableElectricityLedger: "电费台账",
	TablePerfSheet:         "综维绩效单",
	TableUnknown:           "未识别表型",
}

// TableKindLabel renders the Chinese family name.
func TableKindLabel(k TableKind) string { return tableKindLabels[k] }

// tableKindMarkers: the characteristic header COMBOS (every marker must
// appear — the #15 verified sets; a single common word never classifies).
// Markers compare via headerMatchKey (case-folded, separators removed).
var tableKindMarkers = map[TableKind][]string{
	TableTowerSettlement:   {"账期月份", "需求确认单编号", "产品业务确认单编号"},
	TablePayroll:           {"员工工资上传批次", "业务账期", "1.8其他"},
	TableWageItems:         {"年份", "月份", "绩效基数"},
	TableElectricityLedger: {"铁塔公司站点编码", "电表编号", "月应付电费"},
	TablePerfSheet:         {"报账人", "选择开支人员", "清单费用合计"},
}

// orderedTableKinds keeps classification deterministic (first match in
// this order wins — the marker sets are disjoint in practice).
var orderedTableKinds = []TableKind{
	TableTowerSettlement, TablePayroll, TableWageItems, TableElectricityLedger, TablePerfSheet,
}

// ClassifyTableKind recognizes the table family from the header set.
// Every marker of exactly one family must hit (exact key first, contains
// fallback — the recognizer's discipline, so the「N月应付电费」column
// FAMILY matches its「月应付电费」marker); zero hits or ambiguous
// multi-family hits answer TableUnknown (the honest C档 degradation —
// classification never blocks an import on its own).
func ClassifyTableKind(headers []string) TableKind {
	keys := make([]string, len(headers))
	for i, h := range headers {
		keys[i] = headerMatchKey(h)
	}
	contains := func(marker string) bool {
		k := headerMatchKey(marker)
		for _, key := range keys {
			if key == k || strings.Contains(key, k) {
				return true
			}
		}
		return false
	}
	hits := make(map[TableKind]bool, len(orderedTableKinds))
	for _, kind := range orderedTableKinds {
		n := 0
		for _, marker := range tableKindMarkers[kind] {
			if contains(marker) {
				n++
			}
		}
		if n == len(tableKindMarkers[kind]) {
			hits[kind] = true
		}
	}
	if len(hits) != 1 {
		return TableUnknown
	}
	for k := range hits {
		return k
	}
	return TableUnknown
}

// PeriodConfidence is the tier of the detected evidence.
type PeriodConfidence string

const (
	// PeriodStrong: A档 — a dedicated period field (column value or the
	// 年份+月份 pair) carries the period itself.
	PeriodStrong PeriodConfidence = "strong"
	// PeriodWeak: B档 — an indirect clue only (the month baked into a
	// column NAME); low confidence, operator confirmation required.
	PeriodWeak PeriodConfidence = "weak"
	// PeriodNone: C档 — no period evidence in the content at all.
	PeriodNone PeriodConfidence = "none"
)

// periodDetectSample is how many data rows the value probes read (the
// payroll proof read all 30 and they agreed; sampling the first 50 keeps
// the check cheap on 10k-row ledgers while still catching mixed files —
// any sampled disagreement marks the detection inconsistent).
const periodDetectSample = 50

// PeriodDetection is the content-side period evidence of one sheet.
type PeriodDetection struct {
	Kind        TableKind        `json:"kind"`
	KindLabel   string           `json:"kindLabel"`
	Year        int              `json:"year"`  // 0 = unknown
	Month       int              `json:"month"` // 0 = unknown
	Confidence  PeriodConfidence `json:"confidence"`
	Basis       string           `json:"basis"` // the 识别依据 (field names + agreement)
	RowsSampled int              `json:"rowsSampled"`
	Detail      string           `json:"detail,omitempty"` // ambiguity/exception notes
}

// PeriodSource is how the batch's period was established (the
// fee_batches.period_source values).
const (
	PeriodSourceAuto       = "auto"       // A档: content-verified match
	PeriodSourceUnverified = "unverified" // B档: weak/indirect evidence only
	PeriodSourceManual     = "manual"     // C档: no content evidence, human-declared
)

// DetectPeriod probes the content for period evidence. headers are the
// sheet's headers; rows the data rows (the first periodDetectSample are
// read). Pure function — no I/O.
func DetectPeriod(headers []string, rows []SourceRow) PeriodDetection {
	kind := ClassifyTableKind(headers)
	det := PeriodDetection{Kind: kind, KindLabel: TableKindLabel(kind), Confidence: PeriodNone}
	sample := rows
	if len(sample) > periodDetectSample {
		sample = sample[:periodDetectSample]
	}
	det.RowsSampled = len(sample)

	switch kind {
	case TableTowerSettlement:
		det.probeValueColumn(headers, sample, "账期月份")
	case TablePayroll:
		det.probeValueColumn(headers, sample, "业务账期")
	case TableWageItems:
		det.probeYearMonthPair(headers, sample)
	case TableElectricityLedger:
		det.probeAmountColumnMonth(headers)
	case TablePerfSheet, TableUnknown:
		// C档: no probe. The perf-sheet's 日期 column is the FILING time
		// (the #15 measured shape: 7 sheets' 日期 values span 2026.6–10
		// while every period is 202609) — deliberately NOT a probe.
		det.Basis = "无账期字段（该表型日期列为填表时间，不构成账期证据）"
		if kind == TableUnknown {
			det.Basis = "表型未识别且无账期字段"
		}
	}
	return det
}

// probeValueColumn reads the dedicated period column: every sampled
// non-empty value must parse (ParsePeriod) and agree — the strong A档
// evidence. Mixed values degrade to weak with the disagreement in the
// detail (never a silent majority vote).
func (d *PeriodDetection) probeValueColumn(headers []string, rows []SourceRow, header string) {
	idx := -1
	key := headerMatchKey(header)
	for i, h := range headers {
		if headerMatchKey(h) == key {
			idx = i
			break
		}
	}
	if idx < 0 {
		d.Basis = fmt.Sprintf("分类命中但缺少「%s」列", header)
		return
	}
	seen, y, m, parseFail := 0, 0, 0, 0
	for _, row := range rows {
		raw := strings.TrimSpace(cellAt(row, idx))
		if raw == "" {
			continue
		}
		cy, cm, err := ParsePeriod(raw)
		if err != nil {
			parseFail++
			continue
		}
		if seen == 0 {
			y, m = cy, cm
		} else if cy != y || cm != m {
			parseFail++ // disagreement counts as unusable evidence
			continue
		}
		seen++
	}
	label := fmt.Sprintf("「%s」列（%d 行一致）", header, seen)
	switch {
	case seen > 0 && parseFail == 0:
		d.Year, d.Month = y, m
		d.Confidence = PeriodStrong
		d.Basis = label
	case seen > 0:
		// values disagree or some rows unparseable — usable as a hint only
		d.Year, d.Month = y, m
		d.Confidence = PeriodWeak
		d.Basis = label
		d.Detail = fmt.Sprintf("另有 %d 行值不一致或不可解析（弱化为 B 档）", parseFail)
	default:
		d.Basis = fmt.Sprintf("「%s」列存在但无可解析值", header)
	}
}

// probeYearMonthPair reads the 年份+月份 two-column combo (导入工资项):
// both columns must carry consistent values — the A档 pair evidence.
func (d *PeriodDetection) probeYearMonthPair(headers []string, rows []SourceRow) {
	yIdx, mIdx := -1, -1
	for i, h := range headers {
		k := headerMatchKey(h)
		if k == "年份" && yIdx < 0 {
			yIdx = i
		}
		if k == "月份" && mIdx < 0 {
			mIdx = i
		}
	}
	if yIdx < 0 || mIdx < 0 {
		d.Basis = "分类命中但缺少「年份」/「月份」列"
		return
	}
	seen, y, m, bad := 0, 0, 0, 0
	for _, row := range rows {
		ys := strings.TrimSpace(cellAt(row, yIdx))
		ms := strings.TrimSpace(cellAt(row, mIdx))
		if ys == "" && ms == "" {
			continue
		}
		cy, err1 := strconv.Atoi(ys)
		cm, err2 := strconv.Atoi(ms)
		if err1 != nil || err2 != nil || !validPeriod(cy, cm) {
			bad++
			continue
		}
		if seen == 0 {
			y, m = cy, cm
		} else if cy != y || cm != m {
			bad++
			continue
		}
		seen++
	}
	label := fmt.Sprintf("「年份」+「月份」列组合（%d 行一致）", seen)
	switch {
	case seen > 0 && bad == 0:
		d.Year, d.Month = y, m
		d.Confidence = PeriodStrong
		d.Basis = label
	case seen > 0:
		d.Year, d.Month = y, m
		d.Confidence = PeriodWeak
		d.Basis = label
		d.Detail = fmt.Sprintf("另有 %d 行值不一致或不可解析（弱化为 B 档）", bad)
	default:
		d.Basis = "「年份」/「月份」列存在但无可解析值"
	}
}

// monthAmountColumnRe matches the 电费台账's per-month amount column
// names: 「1月应付电费」「12月应付电费」 — the month baked into the NAME.
var monthAmountColumnRe = regexp.MustCompile(`^(\d{1,2})月应付电费$`)

// probeAmountColumnMonth reads the month from the「N月应付电费」column
// NAME (B档 weak: indirect clue, year unknown). Exactly one such column
// is a usable hint; several (a yearly ledger) are genuinely ambiguous —
// reported in the detail, compared against nothing.
func (d *PeriodDetection) probeAmountColumnMonth(headers []string) {
	months := []int{}
	cols := []string{}
	for _, h := range headers {
		if m := monthAmountColumnRe.FindStringSubmatch(strings.TrimSpace(h)); m != nil {
			if v, err := strconv.Atoi(m[1]); err == nil && v >= 1 && v <= 12 {
				months = append(months, v)
				cols = append(cols, strings.TrimSpace(h))
			}
		}
	}
	switch len(months) {
	case 0:
		d.Basis = "分类命中但无「N月应付电费」形态列"
	case 1:
		d.Month = months[0]
		d.Confidence = PeriodWeak
		d.Basis = fmt.Sprintf("列名「%s」（月份取自列名，年份无字段——弱识别）", cols[0])
	default:
		d.Confidence = PeriodWeak
		d.Basis = fmt.Sprintf("%d 个「N月应付电费」列并存，月份无法唯一判定", len(months))
		d.Detail = "候选列：" + strings.Join(cols, "、") + "（年度台账形态，账期以申报为准）"
	}
}

// PeriodCheckOutcome is the three-tier verdict of detection vs
// declaration.
type PeriodCheckOutcome struct {
	Detection PeriodDetection
	Source    string // period_source: auto / unverified / manual
	// Refuse is true when the content evidence CONTRADICTS the declared
	// period and no operator override was given.
	Refuse bool
	// Message carries both values on refusal (and the override note when
	// TrustDeclared rode over a disagreement).
	Message string
}

// CheckPeriod applies the three-tier ruling: A档 strong-full mismatch
// refuses; B档 weak month-only mismatch refuses (weak basis named);
// agreement records auto (strong) / unverified (weak); no evidence
// records manual. trustDeclared overrides any refusal — the
// disagreement is still recorded in the message and the source stays
// unverified (留痕, never a silent pass).
func CheckPeriod(det PeriodDetection, declaredYear, declaredMonth int, trustDeclared bool) PeriodCheckOutcome {
	out := PeriodCheckOutcome{Detection: det}
	switch det.Confidence {
	case PeriodStrong:
		if det.Year == declaredYear && det.Month == declaredMonth {
			out.Source = PeriodSourceAuto
			out.Message = fmt.Sprintf("账期内容核验一致（%04d-%02d，依据：%s）", det.Year, det.Month, det.Basis)
			return out
		}
		out.Refuse = true
		out.Source = PeriodSourceUnverified
		out.Message = fmt.Sprintf("识别账期 %04d-%02d 与申报账期 %04d-%02d 不一致（依据：%s）——账期错误会污染全部环比/趋势分析，请改用识别账期或核实文件；确认申报无误可加 --period-trust-declared 重跑",
			det.Year, det.Month, declaredYear, declaredMonth, det.Basis)
	case PeriodWeak:
		if det.Month == 0 {
			// ambiguous weak evidence — nothing comparable
			out.Source = PeriodSourceUnverified
			out.Message = fmt.Sprintf("账期弱识别不可判定（%s）；账期以申报 %04d-%02d 为准，批次标注 unverified", det.Basis, declaredYear, declaredMonth)
			if det.Detail != "" {
				out.Message += "；" + det.Detail
			}
			return out
		}
		if det.Month == declaredMonth {
			out.Source = PeriodSourceUnverified
			out.Message = fmt.Sprintf("账期弱识别一致（月份 %02d，依据：%s）——置信度低，批次标注 unverified", det.Month, det.Basis)
			return out
		}
		out.Refuse = true
		out.Source = PeriodSourceUnverified
		out.Message = fmt.Sprintf("识别月份 %02d 与申报账期 %04d-%02d 的月份不一致（弱识别依据：%s）——请核实；确认申报无误可加 --period-trust-declared 重跑",
			det.Month, declaredYear, declaredMonth, det.Basis)
	default: // PeriodNone
		out.Source = PeriodSourceManual
		out.Message = fmt.Sprintf("文件内容无账期字段（%s）——账期由人工指定（%04d-%02d），批次标注 manual，无法内容核验", det.Basis, declaredYear, declaredMonth)
	}
	if out.Refuse && trustDeclared {
		out.Refuse = false
		out.Message = fmt.Sprintf("人工裁决：申报账期 %04d-%02d 覆盖识别结果（识别：%s）——不一致已留痕，批次标注 unverified", declaredYear, declaredMonth, det.Basis)
	}
	return out
}
