package fees

// risks.go — the risk scenarios of the fee analysis layer (#64, #58
// stage 4; #114 adds scenario 7). Every scenario is a FIXED, parameterized preset: the caller
// passes a scenario id + numeric/enum parameters, never SQL (the red line
// the MCP surface enforces, extended to the engine face).
//
// The output contract follows the module's doctrine — 账单存在 ≠ 认定结论:
// a clue is a TO-BE-VERIFIED signal carrying the amounts, the explicit
// threshold that fired, and row-level evidence anchors (source file +
// row number + import batch) that point back into the immutable snapshot
// layer. No scenario decides "duplicate payment" or "violation"; the
// human investigates with the evidence.
//
// Scenarios 1 (retired-billing) and 6 (unlinked) are CROSS-LAYER: the fee
// side (Parquet) joins the resource side (SQLite links + resource
// status) in GO — no cross-engine ATTACH ever gets introduced (#58's
// architecture ruling). The service orchestrates them here; the pure
// Parquet scenarios (2/3/4/5) run inside the query engine's fixed SQL
// and return bare clues; the service uniformly sorts, caps, fetches the
// evidence anchors and renders the envelope.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// RiskScenario is the closed enum of the risk presets (#64's table + #114's
// scenario 7).
type RiskScenario string

const (
	// RiskRetiredBilling (scenario 1): the fee keeps flowing to a site
	// whose linked resource is retired/disabled (status in the query's
	// Statuses set — default 停用/退网 of the §6 enum).
	RiskRetiredBilling RiskScenario = "retired-billing"
	// RiskDuplicateSettlement (scenario 2): the same (site, period) is
	// billed by more than one import batch — the coexisting-batch design
	// (#60) exists precisely to surface this.
	RiskDuplicateSettlement RiskScenario = "duplicate-settlement"
	// RiskLedgerMismatch (scenario 3): the same (site, period) carries
	// DIFFERENT per-batch totals across batches (the contract-vs-bill
	// shape: two ledgers of one fee domain imported as separate batches
	// disagree). Amount-equal coexistence is scenario 2's business; the
	// mismatch is the per-batch total delta.
	RiskLedgerMismatch RiskScenario = "ledger-mismatch"
	// RiskSpike (scenario 4): a period-over-period rise of the same
	// site's total crosses the ratio threshold. Deterministic on
	// duplicate rows (#59's window-function finding): (site, period)
	// pairs spanning more than one batch are EXCLUDED (they are already
	// scenario 2/3 clues; the excluded count is reported, never silently
	// dropped).
	RiskSpike RiskScenario = "spike"
	// RiskOutlier (scenario 5): a site's period total deviates from the
	// same-period peer distribution (z-score over the single-batch sites
	// of the same fee type) beyond the threshold. Two-sided: a clue may
	// be abnormally LOW as well — the z sign says which.
	RiskOutlier RiskScenario = "outlier"
	// RiskUnlinked (scenario 6): a fee code has neither a confirmed link
	// nor a pending proposal — billing with no resource behind it. A
	// dismissed proposal counts as no match (the human already said so).
	RiskUnlinked RiskScenario = "unlinked"
	// RiskIdleBilling (scenario 7, #114): the fee keeps flowing to a site
	// whose linked resource carries ZERO metric points in the billed
	// period's time window — the two independent ledgers disagree: the
	// fee face says the site exists and costs money, the measurement face
	// has no records at all. The original requirement's "停电告警在账
	// 资源 / 无业务量却有费用" cross-check, finally carried by a scenario.
	// The honest caveat is part of the contract: a zero count may mean
	// idle equipment OR broken/absent collection coverage — the clue is a
	// to-be-verified signal, never an idleness verdict.
	RiskIdleBilling RiskScenario = "idle-billing"
	// RiskLedgerConsistency (scenario 9, #5 refined by #8): the
	// deterministic IN-LEDGER consistency rules over the optional richness
	// columns — reading-gap (multi-party meters excluded per #8 §0: 读数差
	// is the whole meter's consumption, 实际电量 this party's share;
	// comparing them is a false-positive machine), ratio-sum (the four-way
	// share columns must sum to 1), chain-break (last period's reading_end
	// vs this period's reading_start per meter — swap-FLAGGED rows are
	// legal breaks, never clues), zero-quantity-billing (用量 0 却计费 —
	// the missed-reading carry-over shape). Every clue is row-level
	// evidence with its口径 in the note; never a verdict.
	RiskLedgerConsistency RiskScenario = "ledger-consistency"
	// RiskMissingBilling (scenario 8, #118): the mirror of scenario 7 —
	// the measurement face says the resource is ALIVE (metric points
	// exist in the period window) while the fee face has NO billing
	// record of the queried type for the linked site code in that
	// period. 有量无费 is the direct revenue-loss shape (漏收嫌疑):
	// traffic without a bill. The deterministic signal is points > 0
	// with a zero-row fee side — no threshold to tune. Resources whose
	// status is in the retired set (default 停用/退网) are EXCLUDED
	// from the universe: not billing a retired resource is the CORRECT
	// state (scenario 1 owns the reverse direction). The honest caveat
	// is part of the contract: no monthly bill is NOT a missing-payment
	// verdict — quarterly/annual settlement, a fee type not imported
	// yet, or a settlement body billed under another code all look the
	// same from here.
	RiskMissingBilling RiskScenario = "missing-billing"
	// RiskCustomRules (issue #2 phase 1): the L1 CONFIGURABLE rules —
	// {metric, aggregation, dimension, comparator, threshold, window}
	// six-tuples stored in fee_risk_rules (migration v37). New rules
	// start DISABLED (the conservative observation state); a human
	// enables them; the scenario runs every ENABLED rule of the queried
	// fee type and tags each clue with the rule's provenance (name +
	// severity + threshold). The clue set is evidence, never verdicts —
	// the same doctrine as the built-in scenarios.
	RiskCustomRules RiskScenario = "custom-rules"
)

// riskScenarios lists every scenario in canonical order (usage + docs).
var riskScenarios = []RiskScenario{
	RiskRetiredBilling, RiskDuplicateSettlement, RiskLedgerMismatch,
	RiskSpike, RiskOutlier, RiskUnlinked, RiskIdleBilling,
	RiskMissingBilling, RiskLedgerConsistency, RiskCustomRules,
}

// RiskScenarioLabels is the Chinese display name of each scenario.
var RiskScenarioLabels = map[RiskScenario]string{
	RiskRetiredBilling:      "退租后仍在计费",
	RiskDuplicateSettlement: "跨批次重复结算",
	RiskLedgerMismatch:      "合同账单金额差异",
	RiskSpike:               "电费环比异常上涨",
	RiskOutlier:             "同类站月度金额离群",
	RiskUnlinked:            "零资源计费",
	RiskIdleBilling:         "有费无量",
	RiskMissingBilling:      "有量无费",
	RiskLedgerConsistency:   "台账内部一致性",
	RiskCustomRules:         "可配置规则",
}

// RiskScenarios lists every scenario id (CLI usage text).
func RiskScenarios() []string {
	out := make([]string, 0, len(riskScenarios))
	for _, s := range riskScenarios {
		out = append(out, string(s))
	}
	return out
}

// ParseRiskScenario validates a raw token against the closed enum.
func ParseRiskScenario(raw string) (RiskScenario, error) {
	for _, s := range riskScenarios {
		if string(s) == raw {
			return s, nil
		}
	}
	return "", fmt.Errorf("fees: 未知风险场景 %q（可用：%v）", raw, RiskScenarios())
}

// Default risk thresholds — every value is a documented default, never a
// magic number buried in SQL (#64: 阈值显式参数化).
const (
	// DefaultRetiredStatuses: the resource statuses that count as
	// "retired/disabled" for scenario 1 (the §6 status enum: 在网/停用/在建/退网).
	DefaultRetiredStatuses = "停用,退网"
	// DefaultSpikeRatio: a month-over-month rise >= 30% fires scenario 4.
	DefaultSpikeRatio = 0.30
	// DefaultOutlierZ: |z-score| >= 3.0 fires scenario 5.
	DefaultOutlierZ = 3.0
	// DefaultMinSites: scenario 5 needs at least 8 same-period peers for
	// the distribution to mean anything.
	DefaultMinSites = 8
	// DefaultMaxEvidence: at most 10 evidence anchors per clue (the full
	// row set stays reconstructible from the snapshot + batch lineage).
	DefaultMaxEvidence = 10
	// PurchaseSpanToleranceDays (#11 rule 1): 购电周期容差（天）。正常购电
	// 周期约 30 天（#11 实测）；+5 天吸收跨月抄表的自然摆动。超出的行是
	// 「长期不报后集中补报」的候选——线索，不是错误认定（补报可能是合理
	// 业务：等总账单/季节性用电/审批在途）。
	PurchaseSpanToleranceDays = 35
	// CatchUpSpanDays (#11 §4.3): 补报标注线（天）= 正常周期 × 2。spike
	// 场景里购电周期超过此值的 (站址, 账期) 在线索 note 里标注「疑似集中
	// 补报」——环比涨幅可能是攒报的计量后果而非趋势异常。标注而非排除：
	// 攒报是否合理由人判断。
	CatchUpSpanDays = 70
)

// EvidenceAnchor is one row-level evidence coordinate pointing back into
// the immutable snapshot layer (the file's original row, verbatim).
type EvidenceAnchor struct {
	SourceFile  string `json:"sourceFile"`
	SourceRow   int    `json:"sourceRow"`
	ImportBatch string `json:"importBatch"`
	AmountCents int64  `json:"amountCents"`
}

// EvidenceKey addresses one clue's evidence partition: the site plus the
// period (Month 0 = all months of Year — the cross-period shape of
// scenario 6's clues).
type EvidenceKey struct {
	SiteCode string
	Year     int
	Month    int
}

// RiskClue is one to-be-verified risk signal. MetricValue/Threshold carry
// the fired number and the explicit bar it crossed (labels say what they
// are); Evidence is the bounded anchor set; EvidenceTotal is the full row
// count behind the clue; Note carries scenario-specific caveats (never a
// verdict — 账单存在 ≠ 认定结论).
type RiskClue struct {
	SiteCode string `json:"siteCode"`
	// RecordKey carries the #4 business-document identity when the clue is
	// record-granular (the duplicate-settlement 口径 is (site, recordKey,
	// period) since #4): the order/meter/work-order the evidence rows share.
	// Empty when the underlying rows carry no record keys.
	RecordKey      string           `json:"recordKey,omitempty"`
	Year           int              `json:"year"`
	Month          int              `json:"month"`
	MetricValue    float64          `json:"metricValue"`
	MetricLabel    string           `json:"metricLabel"`
	Threshold      *float64         `json:"threshold"`
	ThresholdLabel string           `json:"thresholdLabel"`
	Evidence       []EvidenceAnchor `json:"evidence"`
	EvidenceTotal  int              `json:"evidenceTotal"`
	Note           string           `json:"note,omitempty"`
	// Decision carries the human verdict when one exists (#119, migration
	// v33): "verified" (confirmed real — the clue stays visible as the
	// known-issues ledger) or "dismissed" (ruled out — only visible with
	// IncludeDecided, counted in the envelope's Dismissed). Empty = a
	// pending clue nobody has looked at yet. DecidedAt is RFC3339 UTC.
	Decision  string `json:"decision,omitempty"`
	DecidedAt string `json:"decidedAt,omitempty"`
}

// RiskResult is the scenario answer envelope: the clue list (capped by
// Limit, largest signal first), the number of excluded multi-batch
// (site, period) pairs (scenarios 4/5 only — the deterministic-on-
// duplicates ruling), and the envelope-level note.
type RiskResult struct {
	Scenario   string     `json:"scenario"`
	ScenarioCn string     `json:"scenarioCn"`
	FeeType    string     `json:"feeType"`
	Year       int        `json:"year"`
	Month      int        `json:"month"`
	Clues      []RiskClue `json:"clues"`
	Total      int        `json:"total"`
	Excluded   int        `json:"excluded"`
	// Dismissed counts the clues a human already ruled out that this run
	// would have re-emitted (#119): they are suppressed from Clues by
	// default (the #69 doctrine — a decided clue stays decided) and
	// counted here, never silently dropped (#83).
	Dismissed int    `json:"dismissed"`
	Note      string `json:"note"`
	// Warnings (#21): non-blocking, structured visibility notices — the
	// exclusion-dominance warning rides here ("0 clues" + a large excluded
	// count is the cannot-analyze shape, NOT the all-clear shape). The note
	// carries the same text (acceptance 1); the array exists so the UI can
	// render it as an alert bar instead of parsing prose.
	Warnings []string `json:"warnings,omitempty"`
}

// RiskQuery is the parameter set of one scenario query. Every threshold
// has a documented default (the constants above); zero values select
// those defaults, so the CLI/HTTP layers never invent numbers.
type RiskQuery struct {
	FeeType     FeeType
	Year        int
	Month       int      // 0 = the whole year
	Statuses    []string // scenario 1: retired resource statuses
	DiffCents   int64    // scenario 3: minimum per-batch total delta (default 0 = any)
	Ratio       float64  // scenario 4: month-over-month rise ratio (default 0.30)
	ZScore      float64  // scenario 5: |z| bar (default 3.0)
	MinSites    int      // scenario 5: minimum same-period peers (default 8)
	Limit       int      // clue cap (default 100, max 1000)
	MaxEvidence int      // anchors per clue (default 10, max 50)
	// IncludeDecided returns dismissed clues too (marked) — the full-set
	// audit view. Default false: dismissed clues are suppressed and
	// counted (verified clues stay visible either way — they are the
	// known-issues ledger, not noise).
	IncludeDecided bool
}

// normalize fills the documented defaults and clamps the bounds.
func (q RiskQuery) normalized() RiskQuery {
	if q.Ratio <= 0 {
		q.Ratio = DefaultSpikeRatio
	}
	if q.ZScore <= 0 {
		q.ZScore = DefaultOutlierZ
	}
	if q.MinSites <= 0 {
		q.MinSites = DefaultMinSites
	}
	if q.Limit <= 0 {
		q.Limit = 100
	}
	if q.Limit > 1000 {
		q.Limit = 1000
	}
	if q.MaxEvidence <= 0 {
		q.MaxEvidence = DefaultMaxEvidence
	}
	if q.MaxEvidence > 50 {
		q.MaxEvidence = 50
	}
	if len(q.Statuses) == 0 {
		q.Statuses = splitComma(DefaultRetiredStatuses)
	}
	return q
}

// validQueryPeriod bounds a QUERY period: month 0 = the whole year (the
// analysis-scope convention), 1-12 = a single month.
func validQueryPeriod(y, m int) bool {
	if m == 0 {
		return y >= 2000 && y <= 2100
	}
	return validPeriod(y, m)
}

// Risks runs one risk scenario. Scenarios 2-5 delegate to the query
// engine's fixed presets (bare clues out, evidence and envelope filled
// here); scenarios 1 and 6 are the cross-layer compositions (fee side
// via the engine, resource side via SQLite — joined in Go, never via a
// cross-engine ATTACH).
func (s *Service) Risks(ctx context.Context, engine QueryEngine, scenario RiskScenario, q RiskQuery) (RiskResult, error) {
	if _, err := ParseFeeType(string(q.FeeType)); err != nil {
		return RiskResult{}, err
	}
	if _, err := ParseRiskScenario(string(scenario)); err != nil {
		return RiskResult{}, err
	}
	if !validQueryPeriod(q.Year, q.Month) {
		return RiskResult{}, fmt.Errorf("fees: 账期 %04d-%02d 不在合法范围", q.Year, q.Month)
	}
	if !engine.Available() {
		// the honest contract: every scenario answers 501 on default builds
		return RiskResult{}, ErrEngineNotBuilt
	}
	q = q.normalized()

	var (
		clues    []RiskClue
		excluded int
		note     string
		warnings []string
		err      error
	)
	switch scenario {
	case RiskDuplicateSettlement:
		clues, excluded, err = engine.RiskDuplicatesPreset(ctx, s.analysisDir(), q)
		note = "线索口径（#4 起）：同一 (站址, 记录键, 账期) 被多个导入批次计费——记录键=订单号/电表编号/工单号，同站多条记录键是正常业务形态（多订单/多电表）不算重复；无记录键的批次仍按 (站址, 账期) 对口径。批次按设计共存不合并，账单存在不构成重复付款认定，请人工核实"
	case RiskLedgerMismatch:
		clues, excluded, err = engine.RiskLedgerMismatchPreset(ctx, s.analysisDir(), q)
		note = "线索口径：同一 (站址, 账期) 的各批次合计金额不一致（合同台账与账单台账作为不同批次导入时的差异形态）——差异金额供核对，非认定结论"
	case RiskSpike:
		clues, excluded, err = engine.RiskSpikePreset(ctx, s.analysisDir(), q)
		note = "线索口径：单批次口径的站址月度合计环比涨幅达阈值；排除数含两类（全年口径，月份参数只收窄显示的线索窗口，#83/#105）：跨批次 (站址, 账期) 对（那属于重复结算/金额差异场景）；环比基期 ≤ 0 的对组——零基期后产生费用是最强烈的突增形态，但涨幅比无定义，无法出线索，请对排除数人工核查"
	case RiskOutlier:
		clues, excluded, err = engine.RiskOutlierPreset(ctx, s.analysisDir(), q)
		note = "线索口径：同期同类（同费用类型）单批次站址集合的月度总额 z-score 离群（双侧，符号在 metricValue）——注意：本场景对月度总额而非单位成本计算，规模大的站天然偏高，请结合站点规模人工判断（#117：规模归一化待基数字段落地后显式参数化）；统计线索，非认定结论；跨批次 (站址, 账期) 已排除，排除数见 excluded"
	case RiskRetiredBilling:
		var c cluesPack
		c, err = s.risksRetiredBilling(ctx, engine, q)
		clues, excluded, note, warnings = c.clues, c.excluded, c.note, c.warnings
	case RiskUnlinked:
		var c cluesPack
		c, err = s.risksUnlinked(ctx, engine, q)
		clues, excluded, note, warnings = c.clues, c.excluded, c.note, c.warnings
	case RiskIdleBilling:
		var c cluesPack
		c, err = s.risksIdleBilling(ctx, engine, q)
		clues, excluded, note, warnings = c.clues, c.excluded, c.note, c.warnings
	case RiskMissingBilling:
		var c cluesPack
		c, err = s.risksMissingBilling(ctx, engine, q)
		clues, excluded, note, warnings = c.clues, c.excluded, c.note, c.warnings
	case RiskLedgerConsistency:
		var c cluesPack
		c, err = s.risksLedgerConsistency(ctx, engine, q)
		clues, excluded, note = c.clues, c.excluded, c.note
	case RiskCustomRules:
		var c cluesPack
		c, err = s.risksCustomRules(ctx, engine, q)
		clues, excluded, note = c.clues, c.excluded, c.note
	default:
		return RiskResult{}, fmt.Errorf("fees: 未实现的风险场景 %q", scenario)
	}
	if err != nil {
		return RiskResult{}, err
	}
	return s.finishClues(ctx, engine, q, scenario, clues, excluded, note, warnings), nil
}

// cluesPack is the cross-layer compositions' return shape (clues + the
// excluded count + the scenario-specific note). warnings (#27) carries
// the scenario's own "未分析" notices — the empty-cross-face prechecks
// (empty resource library, no confirmed links, empty metrics face, empty
// fee face) ride here so the UI can render them as alert bars; they flow
// into RiskResult.warnings alongside finishClues' #21 exclusion warnings.
type cluesPack struct {
	clues    []RiskClue
	excluded int
	note     string
	warnings []string
}

// risksLedgerConsistency composes scenario 9 (#5/#8): the engine preset
// answers the raw rule findings (site, period, rule, metric, row anchor);
// the service attaches the shared evidence anchors and the 口径 note that
// carries EVERY rule's semantics — the clue set is evidence, never
// verdicts (acceptance #6).
func (s *Service) risksLedgerConsistency(ctx context.Context, engine QueryEngine, q RiskQuery) (cluesPack, error) {
	raw, excluded, err := engine.RiskLedgerConsistencyPreset(ctx, s.analysisDir(), q)
	if err != nil {
		return cluesPack{}, err
	}
	if len(raw) == 0 {
		return cluesPack{
			clues: []RiskClue{}, excluded: excluded,
			note: "线索口径：台账内部一致性六条确定性规则（reading-gap 读数差≠填报用量，一表多户已排除；ratio-sum 四方分摊之和≠1；chain-break 跨账期读数断链，已标记换表的合法断开不计；zero-quantity-billing 用量0却有金额；purchase-period 购电周期>35天——长期不报后集中补报候选；receivable-unreceived 应收>实收——资金回收风险）——全部为行级确定性校验，非统计推断；线索不构成认定结论；缺相应列的文件对应规则自动跳过",
		}, nil
	}
	keys := make([]EvidenceKey, 0, len(raw))
	seen := map[EvidenceKey]bool{}
	for _, c := range raw {
		k := EvidenceKey{SiteCode: c.SiteCode, Year: c.Year, Month: c.Month}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	anchors, err := engine.EvidenceFor(ctx, s.analysisDir(), q.FeeType, keys, q.MaxEvidence)
	if err != nil {
		return cluesPack{}, err
	}
	clues := make([]RiskClue, 0, len(raw))
	for _, c := range raw {
		k := EvidenceKey{SiteCode: c.SiteCode, Year: c.Year, Month: c.Month}
		clue := RiskClue{
			SiteCode: c.SiteCode, Year: c.Year, Month: c.Month,
			MetricValue: c.MetricValue, MetricLabel: c.MetricLabel,
			Evidence: anchors[k], EvidenceTotal: len(anchors[k]),
		}
		// The rule rides the threshold label slot — the deterministic rules
		// have fixed tolerances, shown instead of an invented number.
		switch c.Rule {
		case "reading-gap":
			clue.ThresholdLabel = "rule: reading-gap（容差 0.5 度；一表多户已排除）"
			clue.Note = "读数差与填报用量之差超过容差——手工填写或抄表进位嫌疑，请核对原始行"
		case "ratio-sum":
			clue.ThresholdLabel = "rule: ratio-sum（容差 0.005）"
			clue.Note = "四方分摊比例之和偏离 1——分摊口径嫌疑，请核对分摊列"
		case "chain-break":
			clue.ThresholdLabel = "rule: chain-break（未标记换表的跨账期断链）"
			clue.Note = "上月止数与本月起数不连续且未标记换表——错抄/漏抄或漏标换表，请核对台账换表衔接列"
		case "zero-quantity-billing":
			clue.ThresholdLabel = "rule: zero-quantity-billing（用量=0 且金额>0）"
			clue.Note = "零用量却有计费金额——疑似上期漏抄表后用量顺延至本期，财务未多收但台账自相矛盾，请人工核实"
		case "purchase-period":
			clue.ThresholdLabel = fmt.Sprintf("rule: purchase-period（购电周期 > %d 天）", PurchaseSpanToleranceDays)
			clue.Note = "购电周期显著超过常规月度周期——长期不报后集中补报的候选：期间用电量不可知，账实存在不符风险；补报本身可能是合理业务（等总账单/季节性用电/审批在途），请核对抄表与结算安排"
		case "receivable-unreceived":
			clue.ThresholdLabel = "rule: receivable-unreceived（应收-实收 > 0）"
			clue.Note = "应收与实收存在差额——资金回收风险：需核实是否已收款或仍在流程中（实收=0 为完全未收形态）；差额为待核对事实，非认定结论"
		default:
			clue.ThresholdLabel = "rule: " + c.Rule
		}
		if c.SourceFile != "" {
			clue.Note += "；锚点：" + c.SourceFile + " 第 " + itoa(c.SourceRow) + " 行"
		}
		clues = append(clues, clue)
	}
	return cluesPack{
		clues: clues, excluded: excluded,
		note: "线索口径：台账内部一致性六条确定性规则（reading-gap 读数差≠填报用量，一表多户已排除；ratio-sum 四方分摊之和≠1；chain-break 跨账期读数断链，已标记换表的合法断开不计；zero-quantity-billing 用量0却有金额；purchase-period 购电周期>35天——长期不报后集中补报候选；receivable-unreceived 应收>实收——资金回收风险）——全部为行级确定性校验，非统计推断；线索不构成认定结论；缺相应列的文件对应规则自动跳过；excluded=一表多户未判读数差行数+已标记换表的断链行数",
	}, nil
}

// itoa keeps the anchor note free of a strconv import in this file.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

// unanalyzedPack (#27) builds the "cannot analyze — here is why and what
// to do next" answer: zero clues, the explanation as the note (CLI and
// MCP consumers read the note), and the SAME text in warnings so UIs
// render it as an alert bar — the #21 channel: 「无法分析」必须与「分析后
// 无发现」在输出上可区分, an all-hit or all-exclude answer from an empty
// cross face is noise or vacuity, never a finding.
func unanalyzedPack(why string) cluesPack {
	return cluesPack{clues: nil, note: why, warnings: []string{why}}
}

// risksRetiredBilling composes scenario 1: fee-side per-(site, period)
// totals (engine) × confirmed links → resource status (SQLite). A clue
// fires when the linked resource's status is in the query's retired set;
// the metric is the billed amount — the anomaly is "money still flowing",
// the amount is the size of it.
func (s *Service) risksRetiredBilling(ctx context.Context, engine QueryEngine, q RiskQuery) (cluesPack, error) {
	// #27 §8: the scenario's universe is the confirmed-link pairs. With
	// ZERO confirmed links (an empty resource library, or links not yet
	// proposed) nothing can ever fire — "0 clues" would dress a vacuous
	// face up as "analyzed, all clear". Name the state instead.
	confirmed, err := s.db.FeeSiteLinks.List(ctx, repo.FeeLinkFilter{Status: domain.LinkStatusConfirmed})
	if err != nil {
		return cluesPack{}, fmt.Errorf("fees: retired-billing 列已确认关联: %w", err)
	}
	if len(confirmed) == 0 {
		return unanalyzedPack("停用计费未分析：无任何已确认关联（confirmed 关联为空——资源库为空或尚未建立费用编码关联）——本场景的宇宙是已确认关联的站址×资源状态，关联面为空时无可分析对象；请先导入资源并建立关联后重跑本场景"), nil
	}
	rows, err := engine.SitePeriodTotals(ctx, s.analysisDir(), q.FeeType, q.Year, q.Month)
	if err != nil {
		return cluesPack{}, err
	}
	retired := make(map[string]struct{}, len(q.Statuses))
	for _, st := range q.Statuses {
		retired[st] = struct{}{}
	}
	// #85: SitePeriodTotals returns one row per (site, period) — a 12-month
	// scope hits the SAME site's link + resource lookups 12 times. The memo
	// resolves each distinct code once; and the two point-query misses that
	// are business states (no confirmed link → scenario 6's business;
	// linked resource vanished → rollback cascade) are classified apart
	// from real DB failures, which now abort the report instead of silently
	// dropping rows from it.
	type linkedResource struct {
		ok         bool
		naturalKey string
		status     string
	}
	memo := make(map[string]linkedResource, len(rows))
	var clues []RiskClue
	for _, r := range rows {
		lr, seen := memo[r.SiteCode]
		if !seen {
			link, err := s.db.FeeSiteLinks.ConfirmedByCode(ctx, r.SiteCode)
			if err == nil {
				res, rerr := s.db.Resources.GetByID(ctx, link.ResourceID)
				if rerr == nil {
					lr = linkedResource{ok: true, naturalKey: res.NaturalKey, status: res.Status}
				} else if !errors.Is(rerr, domain.ErrNotFound) {
					return cluesPack{}, fmt.Errorf("fees: retired-billing 查资源 %s: %w", link.ResourceID, rerr)
				}
			} else if !errors.Is(err, domain.ErrNotFound) {
				return cluesPack{}, fmt.Errorf("fees: retired-billing 查关联 %s: %w", r.SiteCode, err)
			}
			memo[r.SiteCode] = lr
		}
		if !lr.ok {
			continue
		}
		if _, hit := retired[lr.status]; !hit {
			continue
		}
		clues = append(clues, RiskClue{
			SiteCode: r.SiteCode, Year: r.Year, Month: r.Month,
			MetricValue: float64(r.TotalCents), MetricLabel: "totalCents",
			// #23: the "threshold" of a no-numeric-threshold scenario IS the
			// verdict's own condition — never an invented number.
			ThresholdLabel: "关联资源状态 ∈ {" + joinComma(q.Statuses) + "} 且该账期仍有费用",
			EvidenceTotal:  r.Rows,
			Note:           fmt.Sprintf("关联资源 %s 状态为 %q，该账期费用仍流入——请人工核实", lr.naturalKey, lr.status),
		})
	}
	return cluesPack{
		clues: clues,
		note:  "线索口径：已确认关联的资源状态 ∈ {" + joinComma(q.Statuses) + "} 且该站址仍有费用记录",
	}, nil
}

// risksUnlinked composes scenario 6: fee-side per-code totals (engine) ×
// link states (SQLite). A code fires when it has NEITHER a confirmed link
// NOR a pending proposal (a dismissed proposal counts as no match — the
// human already ruled it out; a pending one is the human channel's
// business, not a fresh clue).
func (s *Service) risksUnlinked(ctx context.Context, engine QueryEngine, q RiskQuery) (cluesPack, error) {
	// #27: the empty-library precheck — with ZERO linkable resources every
	// fee code necessarily looks unlinked, and the scenario would emit the
	// whole fee face as "clues" (200+ noise rows drowning the real finds).
	// "Cannot analyze" and "analyzed, found some" must stay distinguishable
	// (#21's principle, the reverse direction). A PARTIAL library keeps
	// the normal path: how much to import is the operator's call, the
	// system does not second-guess it (the issue's explicit ruling).
	byType, err := s.db.Resources.CountByType(ctx)
	if err != nil {
		return cluesPack{}, fmt.Errorf("fees: unlinked 统计可关联资源: %w", err)
	}
	linkable := 0
	for _, t := range LinkableResourceTypes {
		linkable += byType[t]
	}
	if linkable == 0 {
		return unanalyzedPack("零资源计费未分析：资源库为空（站址/机房/基站等可关联资源计数为 0）——此时所有费用编码都必然显得「无关联」，产出的是噪音而非线索；请先导入资源（站址/机房/基站）后重跑本场景"), nil
	}
	rows, err := engine.SiteTotalsAll(ctx, s.analysisDir(), q.FeeType, q.Year, q.Month)
	if err != nil {
		return cluesPack{}, err
	}
	covered := make(map[string]struct{})
	for _, status := range []domain.LinkStatus{domain.LinkStatusConfirmed, domain.LinkStatusPending} {
		links, err := s.db.FeeSiteLinks.List(ctx, repo.FeeLinkFilter{Status: status})
		if err != nil {
			return cluesPack{}, err
		}
		for _, l := range links {
			covered[l.FeeSiteCode] = struct{}{}
		}
	}
	var clues []RiskClue
	for _, r := range rows {
		if _, ok := covered[r.SiteCode]; ok {
			continue
		}
		clues = append(clues, RiskClue{
			SiteCode: r.SiteCode, Year: q.Year, Month: q.Month,
			MetricValue: float64(r.TotalCents), MetricLabel: "totalCents",
			// #23: the verdict condition as the label (no numeric threshold
			// exists — the empty-link face IS the signal).
			ThresholdLabel: "关联面为空（confirmed 与 pending 均无）",
			EvidenceTotal:  r.Rows,
			Note:           "该费用编码既无已确认关联也无待确认提案（已驳回的候选视为无匹配）——零资源计费线索，请人工核实",
		})
	}
	return cluesPack{
		clues: clues,
		note:  "线索口径：费用侧有记录且关联面（confirmed+pending）为空；已驳回提案计入线索侧",
	}, nil
}

// risksIdleBilling composes scenario 7 (#114): fee-side per-(site,
// period) totals (engine) × confirmed links → the linked resource's
// metric-point count in the period's time window (SQLite metrics). A
// clue fires when the count is ZERO — the deterministic signal, no
// threshold to tune (a thresholded "nearly no volume" would smuggle a
// verdict back into a clue). A site without a confirmed link never fires
// here (which resource to measure is unknown — that is unlinked's
// business). The note carries the honest caveat: 采集缺失与空置在该证据
// 形态下不可区分.
func (s *Service) risksIdleBilling(ctx context.Context, engine QueryEngine, q RiskQuery) (cluesPack, error) {
	// #27 §8: with a COMPLETELY empty metrics face every confirmed-linked
	// billed site fires at once (the count is necessarily zero) — the
	// mirror of unlinked's empty-library flood. A metrics face that merely
	// lacks points for SOME resources is the normal per-site signal shape
	// (the note already carries the collection-gap caveat honestly).
	if total, err := s.db.Metrics.CountMetricPoints(ctx); err != nil {
		return cluesPack{}, fmt.Errorf("fees: idle-billing 统计指标总点数: %w", err)
	} else if total == 0 {
		return unanalyzedPack("零量计费未分析：指标采集面为空（全库无任何指标点）——此时所有已关联站址的指标点数必然为 0，全部命中是数据覆盖问题而非发现；请先导入指标数据后重跑本场景"), nil
	}
	rows, err := engine.SitePeriodTotals(ctx, s.analysisDir(), q.FeeType, q.Year, q.Month)
	if err != nil {
		return cluesPack{}, err
	}
	// #85's memo discipline, twice over: one link lookup per distinct site
	// (a whole-year scope repeats the same site per month) and one point
	// count per (resource, period) — the count is period-scoped, so the
	// 12 rows of one site share the link memo but not the count memo.
	// Point-query misses that are business states stay silent; real DB
	// failures abort the report (#85: never silently drop rows from it).
	type linkedResource struct {
		ok         bool
		resourceID string
		naturalKey string
	}
	linkedMemo := make(map[string]linkedResource, len(rows))
	countMemo := make(map[string]int, len(rows))
	var clues []RiskClue
	for _, r := range rows {
		lr, seen := linkedMemo[r.SiteCode]
		if !seen {
			link, err := s.db.FeeSiteLinks.ConfirmedByCode(ctx, r.SiteCode)
			if err == nil {
				res, rerr := s.db.Resources.GetByID(ctx, link.ResourceID)
				if rerr == nil {
					lr = linkedResource{ok: true, resourceID: link.ResourceID, naturalKey: res.NaturalKey}
				} else if !errors.Is(rerr, domain.ErrNotFound) {
					return cluesPack{}, fmt.Errorf("fees: idle-billing 查资源 %s: %w", link.ResourceID, rerr)
				}
			} else if !errors.Is(err, domain.ErrNotFound) {
				return cluesPack{}, fmt.Errorf("fees: idle-billing 查关联 %s: %w", r.SiteCode, err)
			}
			linkedMemo[r.SiteCode] = lr
		}
		if !lr.ok {
			continue
		}
		countKey := fmt.Sprintf("%s|%04d-%02d", lr.resourceID, r.Year, r.Month)
		n, seen := countMemo[countKey]
		if !seen {
			from, to := periodWindow(r.Year, r.Month)
			n, err = s.db.Metrics.CountPointsOfResource(ctx, lr.resourceID, from, to)
			if err != nil {
				return cluesPack{}, fmt.Errorf("fees: idle-billing 统计指标点 %s: %w", lr.resourceID, err)
			}
			countMemo[countKey] = n
		}
		if n > 0 {
			continue
		}
		clues = append(clues, RiskClue{
			SiteCode: r.SiteCode, Year: r.Year, Month: r.Month,
			MetricValue: float64(r.TotalCents), MetricLabel: "totalCents",
			// #23: the deterministic zero-point verdict, stated as the label.
			ThresholdLabel: "关联资源指标点数 = 0 且仍有费用",
			EvidenceTotal:  r.Rows,
			Note:           fmt.Sprintf("关联资源 %s 在 %04d-%02d 账期窗口内无任何指标采集点，但该账期仍有费用记录——两条台账矛盾，请人工核实（无采集也可能是采集覆盖缺失或断链，非空置认定）", lr.naturalKey, r.Year, r.Month),
		})
	}
	return cluesPack{
		clues: clues,
		note:  "线索口径：已确认关联的站址在该账期窗口内指标点数为零且费用仍在流（跨层组合：引擎费用面 × SQLite 指标面）——账单存在 ≠ 认定结论，采集缺失与空置不可区分，请人工核实",
	}, nil
}

// periodWindow renders the [from, to] ts bounds of one billing period in
// the metrics layer's normalized-RFC3339 lexicographic convention:
// month 0 (the whole-year scope) spans the year, 1-12 the calendar
// month. Metric points are UTC-midnight day-granular (ParseTS), so the
// month's last day at 23:59:59Z covers the full window under the ts <=
// comparison CountPointsOfResource uses.
// risksMissingBilling composes scenario 8 (#118), the mirror of scenario
// 7: walk the CONFIRMED-link universe (the human-ruled pairs) → skip
// retired-status resources (not billing them is correct; scenario 1 owns
// the reverse) → count metric points per (resource, month) window
// (SQLite metrics, deterministic points>0, no threshold) → look the
// (site, month) up in the fee face's billed set (ONE engine call —
// SitePeriodTotals over the scope, the set of pairs WITH rows). A clue
// fires when the resource is measured ALIVE in a month whose fee side
// holds no record of the queried type. The composition runs link-side →
// fee-side because the anomaly's subject is a RESOURCE with traffic, not
// a billed row — the universe is every confirmed link, months the scope
// selects (1..12 for the whole-year shape, one for the single month).
//
// #85's memo discipline applies twice: one resource fetch per distinct
// resource id (two site codes may share a resource) and one point count
// per (resource, period). Point-query misses that are business states
// (resource vanished → rollback cascade) stay silent; real DB failures
// abort the report, never silently dropping links from it.
func (s *Service) risksMissingBilling(ctx context.Context, engine QueryEngine, q RiskQuery) (cluesPack, error) {
	// #27 §8: three empty-face prechecks, most fundamental first —
	//
	//   1. no confirmed links: the scenario's universe IS the confirmed
	//      pairs; empty universe is vacuous, and "0 clues" must say so
	//      instead of dressing as clean;
	//   2. an entirely empty metrics face: no resource can be "alive",
	//      every count is zero — the all-exclude vacuity;
	//   3. an empty fee face for the queried type/period: EVERY alive
	//      resource would fire at once — the all-hit flood (querying a
	//      month whose ledger was never imported looks identical to a
	//      month where every bill genuinely vanished; the honest answer
	//      names the range problem, it does not invent 200 漏收 clues).
	links, err := s.db.FeeSiteLinks.List(ctx, repo.FeeLinkFilter{Status: domain.LinkStatusConfirmed})
	if err != nil {
		return cluesPack{}, fmt.Errorf("fees: missing-billing 列关联: %w", err)
	}
	if len(links) == 0 {
		return unanalyzedPack("有量无费未分析：无任何已确认关联（资源库为空或尚未建立费用编码关联）——本场景的宇宙是已确认关联的资源，关联面为空时无可分析对象；请先导入资源并建立关联后重跑本场景"), nil
	}
	if total, err := s.db.Metrics.CountMetricPoints(ctx); err != nil {
		return cluesPack{}, fmt.Errorf("fees: missing-billing 统计指标总点数: %w", err)
	} else if total == 0 {
		return unanalyzedPack("有量无费未分析：指标采集面为空（全库无任何指标点）——「有量无费」的判定需要指标面先有数据（「有量」无从谈起）；请先导入指标数据后重跑本场景"), nil
	}
	rows, err := engine.SitePeriodTotals(ctx, s.analysisDir(), q.FeeType, q.Year, q.Month)
	if err != nil {
		return cluesPack{}, err
	}
	if len(rows) == 0 {
		return unanalyzedPack("有量无费未分析：费用侧该类型/账期无任何计费记录——空费用面会让全部有量资源命中，属数据范围问题而非漏收线索；请核对费用类型与账期范围，或先导入该账期费用数据后重跑本场景"), nil
	}
	billed := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		key := fmt.Sprintf("%s|%04d-%02d", r.SiteCode, r.Year, r.Month)
		billed[key] = struct{}{}
	}
	// links were fetched by the #27 precheck above — the confirmed list
	// is the scenario's universe and doubles as the walk set here.
	months := []int{q.Month}
	if q.Month == 0 {
		months = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	}
	retired := make(map[string]struct{}, len(q.Statuses))
	for _, st := range q.Statuses {
		retired[st] = struct{}{}
	}
	type resInfo struct {
		ok     bool
		key    string
		status string
	}
	resMemo := make(map[string]resInfo, len(links))
	countMemo := make(map[string]int, len(links))
	var clues []RiskClue
	for _, link := range links {
		ri, seen := resMemo[link.ResourceID]
		if !seen {
			res, rerr := s.db.Resources.GetByID(ctx, link.ResourceID)
			if rerr == nil {
				ri = resInfo{ok: true, key: res.NaturalKey, status: res.Status}
			} else if !errors.Is(rerr, domain.ErrNotFound) {
				return cluesPack{}, fmt.Errorf("fees: missing-billing 查资源 %s: %w", link.ResourceID, rerr)
			}
			resMemo[link.ResourceID] = ri
		}
		if !ri.ok {
			continue
		}
		if _, hit := retired[ri.status]; hit {
			continue
		}
		for _, m := range months {
			countKey := fmt.Sprintf("%s|%04d-%02d", link.ResourceID, q.Year, m)
			n, seen := countMemo[countKey]
			if !seen {
				from, to := periodWindow(q.Year, m)
				n, err = s.db.Metrics.CountPointsOfResource(ctx, link.ResourceID, from, to)
				if err != nil {
					return cluesPack{}, fmt.Errorf("fees: missing-billing 统计指标点 %s: %w", link.ResourceID, err)
				}
				countMemo[countKey] = n
			}
			if n == 0 {
				continue
			}
			billKey := fmt.Sprintf("%s|%04d-%02d", link.FeeSiteCode, q.Year, m)
			if _, ok := billed[billKey]; ok {
				continue
			}
			clues = append(clues, RiskClue{
				SiteCode: link.FeeSiteCode, Year: q.Year, Month: m,
				MetricValue: float64(n), MetricLabel: "metricPoints",
				// #23: the reverse-verdict condition as the label.
				ThresholdLabel: "指标点数 > 0 且费用侧无记录",
				EvidenceTotal:  0,
				Note:           fmt.Sprintf("关联资源 %s 在 %04d-%02d 账期窗口内有 %d 个指标采集点，但费用侧该账期无任何本类型计费记录——两条台账反向矛盾，疑似漏收线索仅供人工核实（无月度账单不等于漏收认定：可能季度/年度结算、该费用类型未导入或结算主体挂账在其他编码）", ri.key, q.Year, m, n),
			})
		}
	}
	return cluesPack{
		clues: clues,
		note:  "线索口径：已确认关联的在网/在建资源在该账期窗口内有指标采集点，而费用侧同账期无该类型计费记录（跨层组合：SQLite 关联与指标面 × 引擎费用面反查）；账期无记录 ≠ 漏收认定，请人工核实",
	}, nil
}

func periodWindow(year, month int) (string, string) {
	if month == 0 {
		return fmt.Sprintf("%04d-01-01T00:00:00Z", year),
			fmt.Sprintf("%04d-12-31T23:59:59Z", year)
	}
	start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, -1) // the month's last day
	return start.Format(time.RFC3339), end.Format("2006-01-02") + "T23:59:59Z"
}

// finishClues is the shared tail: sort, cap, fetch the evidence anchors,
// render the envelope. Deterministic order: metric value descending, then
// (site, period ascending). Evidence fetching is BEST-EFFORT at the
// envelope level (#93: the code, this comment and the tests now say the
// same thing): an anchor-fetch failure leaves the affected clues with an
// EMPTY anchor list — the clue itself (site, period, metric, threshold)
// stays, and the snapshot layer remains the source of truth for the
// read-back (#86). A clue's Evidence is therefore never null — an
// anchor-less clue carries [] (the arrays-are-never-null discipline of
// the whole API surface), and the frontend renders it defensively.
func (s *Service) finishClues(ctx context.Context, engine QueryEngine, q RiskQuery,
	scenario RiskScenario, clues []RiskClue, excluded int, note string, packWarnings []string) RiskResult {

	sort.SliceStable(clues, func(i, j int) bool {
		if clues[i].MetricValue != clues[j].MetricValue {
			return clues[i].MetricValue > clues[j].MetricValue
		}
		if clues[i].SiteCode != clues[j].SiteCode {
			return clues[i].SiteCode < clues[j].SiteCode
		}
		return clues[i].Year*12+clues[i].Month < clues[j].Year*12+clues[j].Month
	})

	// #119: attach the human decisions, then apply the suppression
	// semantics. verified clues STAY visible (marked — they are the
	// known-issues ledger); dismissed ones drop out of the default view
	// and are counted (never silently dropped, #83). IncludeDecided
	// returns the full set — every decided clue visible and marked.
	// Decisions are scoped per scenario: a dismissed duplicate-settlement
	// clue says nothing about the same site's spike clue.
	dismissedCount := 0
	if len(clues) > 0 {
		keys := make([]repo.ClueKey, 0, len(clues))
		for _, c := range clues {
			keys = append(keys, repo.ClueKey{
				Scenario: string(scenario), FeeType: q.FeeType,
				SiteCode: c.SiteCode, Year: c.Year, Month: c.Month,
			})
		}
		if decisions, derr := s.db.RiskClueDecisions.ListForKeys(ctx, keys); derr == nil {
			marked := make([]RiskClue, 0, len(clues))
			for i := range clues {
				k := repo.ClueKey{
					Scenario: string(scenario), FeeType: q.FeeType,
					SiteCode: clues[i].SiteCode, Year: clues[i].Year, Month: clues[i].Month,
				}
				d, ok := decisions[k]
				if ok {
					clues[i].Decision = string(d.Status)
					clues[i].DecidedAt = d.DecidedAt.Format(time.RFC3339)
				}
				if ok && d.Status == domain.ClueDecisionDismissed {
					dismissedCount++
					if !q.IncludeDecided {
						continue
					}
				}
				marked = append(marked, clues[i])
			}
			clues = marked
		}
		// the decision read is best-effort at the envelope level (the
		// same ruling as the evidence anchors): a decision-store failure
		// shows the UNDECIDED clue set rather than an empty answer — a
		// clue nobody marked pending is always safer than hiding it.
	}

	total := len(clues)
	if len(clues) > q.Limit {
		clues = clues[:q.Limit]
	}
	if len(clues) > 0 {
		keys := make([]EvidenceKey, 0, len(clues))
		for _, c := range clues {
			keys = append(keys, EvidenceKey{SiteCode: c.SiteCode, Year: c.Year, Month: c.Month})
		}
		anchors, err := engine.EvidenceFor(ctx, s.analysisDir(), q.FeeType, keys, q.MaxEvidence)
		if err == nil {
			for i := range clues {
				k := EvidenceKey{SiteCode: clues[i].SiteCode, Year: clues[i].Year, Month: clues[i].Month}
				if a := anchors[k]; a != nil {
					clues[i].Evidence = a
				}
			}
		}
		// anchor fetch is best-effort at the envelope level: a missing
		// anchor set never fabricates evidence — the clue stays, the
		// snapshot remains the source of truth (errors would surface in
		// the engine query itself for the real failures)
	}
	// #93: arrays are never null — a clue whose anchors could not be
	// fetched (or that has none in range) serializes as "evidence": [],
	// not null; the frontend row expander maps over it without guards.
	for i := range clues {
		if clues[i].Evidence == nil {
			clues[i].Evidence = make([]EvidenceAnchor, 0)
		}
	}
	// The envelope's own array rides the same #93 doctrine: a zero-clue
	// scenario (idle-billing on a bare fixture) must answer "clues": [],
	// never null — the browser-side run-all (#3) iterates every envelope
	// and a null kills the whole page, not just the empty scenario.
	if clues == nil {
		clues = make([]RiskClue, 0)
	}
	// #21: "cannot analyze" must never dress up as "analyzed, all clear".
	// A zero-clue answer with a non-zero excluded count is ambiguous to a
	// human — the honest text names the ambiguity and the audit channel;
	// the wording deliberately does NOT claim "everything was excluded"
	// (the excluded/(total+excluded) ratio cannot know the full
	// participation face without another engine roundtrip, and a false
	// "全部被排除" would be its own misinformation).
	// #27: the cross-layer compositions' own precheck notices ("未分析")
	// ride the SAME channel — they arrive as packWarnings and merge here.
	warnings := packWarnings
	if excluded > 0 {
		switch {
		case total == 0:
			warnings = append(warnings, fmt.Sprintf(
				"本场景返回 0 条线索，另有 %d 个对象被排除出统计——0 条线索可能是「数据被排除后无法分析」而非「无异常」；请核对是否存在重复导入/多批次（可用 fees verify-batches 审计跨账期重复）",
				excluded))
		case excluded*2 >= total+excluded:
			pct := excluded * 100 / (total + excluded)
			warnings = append(warnings, fmt.Sprintf(
				"本场景排除数 %d 已达线索与排除合计的 %d%%——统计面被显著削弱，结果可能不具备代表性；请核对是否存在重复导入/多批次（可用 fees verify-batches 审计跨账期重复）",
				excluded, pct))
		}
		if len(warnings) > 0 {
			// acceptance 1: the note carries the count + the advice too —
			// CLI and MCP consumers read the note, the array is for UIs.
			note += "；【注意】" + warnings[0]
		}
	}
	return RiskResult{
		Scenario:   string(scenario),
		ScenarioCn: RiskScenarioLabels[scenario],
		FeeType:    string(q.FeeType),
		Year:       q.Year,
		Month:      q.Month,
		Clues:      clues,
		Total:      total,
		Excluded:   excluded,
		Dismissed:  dismissedCount,
		Note:       note,
		Warnings:   warnings,
	}
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// SplitComma exposes the comma list parsing to the CLI/HTTP layers
// (full-width commas tolerated — the operator's input shape).
func SplitComma(s string) []string { return splitComma(s) }

// splitComma splits a comma list (half/full-width) into TRIMMED, non-empty
// items (#105: "停用, 退网" — a URL-encoded space or a manual space — used to
// keep " 退网" verbatim, which then matched nothing in the free-text status
// column and silently narrowed retired-billing while the note displayed the
// spacey form; every item is now trimmed, empties dropped).
func splitComma(s string) []string {
	var raw []string
	cur := make([]rune, 0, len(s))
	for _, r := range s {
		if r == ',' || r == '，' {
			if len(cur) > 0 {
				raw = append(raw, string(cur))
				cur = cur[:0]
			}
			continue
		}
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		raw = append(raw, string(cur))
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(strings.NewReplacer(" ", "", "　", "").Replace(item))
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
