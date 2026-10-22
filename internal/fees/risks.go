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

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
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
)

// riskScenarios lists every scenario in canonical order (usage + docs).
var riskScenarios = []RiskScenario{
	RiskRetiredBilling, RiskDuplicateSettlement, RiskLedgerMismatch,
	RiskSpike, RiskOutlier, RiskUnlinked, RiskIdleBilling,
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
	SiteCode       string           `json:"siteCode"`
	Year           int              `json:"year"`
	Month          int              `json:"month"`
	MetricValue    float64          `json:"metricValue"`
	MetricLabel    string           `json:"metricLabel"`
	Threshold      *float64         `json:"threshold"`
	ThresholdLabel string           `json:"thresholdLabel"`
	Evidence       []EvidenceAnchor `json:"evidence"`
	EvidenceTotal  int              `json:"evidenceTotal"`
	Note           string           `json:"note,omitempty"`
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
	Note       string     `json:"note"`
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
		err      error
	)
	switch scenario {
	case RiskDuplicateSettlement:
		clues, excluded, err = engine.RiskDuplicatesPreset(ctx, s.analysisDir(), q)
		note = "线索口径：同一 (站址, 账期) 存在多个导入批次的计费记录——批次按设计共存不合并，账单存在不构成重复付款认定，请人工核实"
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
		clues, excluded, note = c.clues, c.excluded, c.note
	case RiskUnlinked:
		var c cluesPack
		c, err = s.risksUnlinked(ctx, engine, q)
		clues, excluded, note = c.clues, c.excluded, c.note
	case RiskIdleBilling:
		var c cluesPack
		c, err = s.risksIdleBilling(ctx, engine, q)
		clues, excluded, note = c.clues, c.excluded, c.note
	default:
		return RiskResult{}, fmt.Errorf("fees: 未实现的风险场景 %q", scenario)
	}
	if err != nil {
		return RiskResult{}, err
	}
	return s.finishClues(ctx, engine, q, scenario, clues, excluded, note), nil
}

// cluesPack is the cross-layer compositions' return shape (clues + the
// excluded count + the scenario-specific note).
type cluesPack struct {
	clues    []RiskClue
	excluded int
	note     string
}

// risksRetiredBilling composes scenario 1: fee-side per-(site, period)
// totals (engine) × confirmed links → resource status (SQLite). A clue
// fires when the linked resource's status is in the query's retired set;
// the metric is the billed amount — the anomaly is "money still flowing",
// the amount is the size of it.
func (s *Service) risksRetiredBilling(ctx context.Context, engine QueryEngine, q RiskQuery) (cluesPack, error) {
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
			EvidenceTotal: r.Rows,
			Note:          fmt.Sprintf("关联资源 %s 状态为 %q，该账期费用仍流入——请人工核实", lr.naturalKey, lr.status),
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
			EvidenceTotal: r.Rows,
			Note:          "该费用编码既无已确认关联也无待确认提案（已驳回的候选视为无匹配）——零资源计费线索，请人工核实",
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
			EvidenceTotal: r.Rows,
			Note:          fmt.Sprintf("关联资源 %s 在 %04d-%02d 账期窗口内无任何指标采集点，但该账期仍有费用记录——两条台账矛盾，请人工核实（无采集也可能是采集覆盖缺失或断链，非空置认定）", lr.naturalKey, r.Year, r.Month),
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
	scenario RiskScenario, clues []RiskClue, excluded int, note string) RiskResult {

	sort.SliceStable(clues, func(i, j int) bool {
		if clues[i].MetricValue != clues[j].MetricValue {
			return clues[i].MetricValue > clues[j].MetricValue
		}
		if clues[i].SiteCode != clues[j].SiteCode {
			return clues[i].SiteCode < clues[j].SiteCode
		}
		return clues[i].Year*12+clues[i].Month < clues[j].Year*12+clues[j].Month
	})
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
	return RiskResult{
		Scenario:   string(scenario),
		ScenarioCn: RiskScenarioLabels[scenario],
		FeeType:    string(q.FeeType),
		Year:       q.Year,
		Month:      q.Month,
		Clues:      clues,
		Total:      total,
		Excluded:   excluded,
		Note:       note,
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
