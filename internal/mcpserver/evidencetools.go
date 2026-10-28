package mcpserver

// evidencetools.go — #36's first MCP batch: the two P0 read-only tools
// that give an AI client the EVIDENCE half of the fee analysis loop.
//
// The gap the issue measured: the AI could run a scenario (fee_risks) and
// then... nothing. The clue carries row-level anchors
// (sourceFile+sourceRow+importBatch), the link/对账/批次 faces all exist
// on CLI/HTTP — but the MCP surface exposed none of them, so the analysis
// closed after step one ("AI 在第 1 步之后就断了"). These two tools are the
// first batch of the issue's own priority table:
//
//      fee_evidence     P0  the clue's raw-row evidence — the evidence
//                           chain's entry point (anchors + the #39 raw row)
//      fee_links_list   P0  the site's link states — the premise for judging
//                           whether a clue is even plausible
//
// fee_evidence answers in TWO modes (one tool, one contract):
//
//      clue mode  {scenario, feeType, year, month?, site} — run the SAME
//                  fixed preset fee_risks runs, keep the clues of ONE site,
//                  answer their anchors (sourceFile/sourceRow/amountCents/
//                  importBatch). The engine-not-built refusal passes through
//                  verbatim on default builds (the contract answer).
//      raw mode   {batch, row} — resolve an anchor to the snapshot's
//                  ORIGINAL row (#39: header + cells, the unmapped columns
//                  included — 折扣生效日期/分项单价…), pure file read, works
//                  on ANY build, no engine involved.
//
// fee_links_list answers the confirmed/pending/dismissed link rows of a
// fee site code (repo.FeeLinkFilter underneath) — the "这个站是不是根本
// 没用电信的电" premise check. Confirm/dismiss stay HUMAN channels
// (HTTP/CLI): the AI sees the state, never moves it.
//
// Governance (the surface's own discipline): both tools readOnlyHint=true
// (they join --read-only), limit default 20 cap 50 (feetools.go:13),
// every output carries its note ("关联状态为待核实，非认定结论" — the
// acceptance's own wording), arrays never null (#93).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/repo"
	"github.com/mmqz/netops-fees-lab/internal/services"
)

const (
	feeEvidenceName     = "fee_evidence"
	feeLinksListName    = "fee_links_list"
	feeEvidenceNoteClue = "线索证据为行级锚点（sourceFile+sourceRow+importBatch），可用本工具的 batch/row 模式回读快照原始行；账单存在 ≠ 认定结论，场景阈值用治理默认值（与 fee_risks 同源），线索请人工核实"
	feeLinksNote        = "关联是费用编码与资源的辅助映射：confirmed=已确认（至多一条）、pending=模糊提案待人工核实、dismissed=已驳回（留痕）；关联状态为待核实线索的辅助信息，非费用认定结论——确认/驳回是人类通道（HTTP/CLI），AI 只读"
)

// addEvidenceTools registers #36's two P0 evidence tools (read-only, both
// faces: NewServer and the --read-only variant).
func addEvidenceTools(server *mcp.Server, svc *services.Services) {
	if svc.Fees == nil {
		return // the fee service is not wired (no data dir) — no tools
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: feeEvidenceName,
		Description: "只读·费用线索证据查询（#36 第一批）：双模式。" +
			"线索模式 {scenario, feeType, year, month?, site}——运行与 fee_risks 同源的固定风险预设，只保留指定站址的线索，返回行级证据锚点（sourceFile+sourceRow+amountCents+importBatch）；" +
			"原始行模式 {batch, row}——把锚点回读成快照原始行（header+cells 逐列对位，含导入映射未建模的原始列，如塔类详单的折扣生效日期/分项单价），纯文件读取，任何构建可用（#39）。" +
			"两模式的输出都是待核实证据而非认定结论。默认构建未编译分析引擎时线索模式明确报错（原始行模式不受影响）。",
		Annotations: readOnlyHints(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feeEvidenceInput) (*mcp.CallToolResult, feeEvidenceOutput, error) {
		return feeEvidence(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: feeLinksListName,
		Description: "只读·站址关联状态查询（#36 第一批）：给定费用站址编码（可选），返回其关联行——" +
			"confirmed=已确认关联（exact/rule 自动确认或人工确认模糊提案，每编码至多一条）/ pending=模糊提案待人工核实 / dismissed=已驳回留痕；" +
			"不给编码则返回最近关联（limit 缺省 20 上限 50）。status 可过滤（confirmed/pending/dismissed）。" +
			"关联状态是判断线索是否合理的辅助信息（如「这个站是否根本没用本侧的电」），非认定结论；确认/驳回是人类通道，AI 只读。",
		Annotations: readOnlyHints(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feeLinksListInput) (*mcp.CallToolResult, feeLinksListOutput, error) {
		return feeLinksList(ctx, svc, in)
	})
}

// feeEvidenceInput is the two-mode union: batch+row selects the raw-row
// mode; the scenario half selects the clue mode. Exactly one mode per
// call — the validation refuses the in-between shapes explicitly (#91's
// explicit-over-silent).
type feeEvidenceInput struct {
	Scenario string `json:"scenario,omitempty" jsonschema:"线索模式·风险场景（与 fee_risks 同一闭集）：retired-billing（退用仍计费）/ duplicate-settlement（跨批次重复）/ ledger-mismatch（台账金额差异）/ spike（环比突增）/ outlier（同批离群）/ unlinked（零资源计费）/ idle-billing（有费无量）/ missing-billing（有量无费）/ ledger-consistency（台账内部一致性）/ custom-rules（可配置规则）"`
	FeeType  string `json:"feeType,omitempty" jsonschema:"线索模式·费用类型：electricity（电费）/ tower_lease（铁塔租费）/ repair（修理费）/ maintenance（综维费）"`
	Year     *int   `json:"year,omitempty" jsonschema:"线索模式·年份（2000–2100）"`
	Month    *int   `json:"month,omitempty" jsonschema:"线索模式·月份（可选 1–12，缺省整年）"`
	Site     string `json:"site,omitempty" jsonschema:"线索模式·站址编码（线索按此过滤，必填于线索模式）"`
	Limit    *int   `json:"limit,omitempty" jsonschema:"线索模式·场景线索条数上限，缺省 20，最大 50（过滤前的场景边界——站点线索若不在场景前 N 条内，本工具如实答空）；必须 ≥ 1"`
	Batch    string `json:"batch,omitempty" jsonschema:"原始行模式·导入批次 id（线索锚点的 importBatch）——给定即进入原始行模式"`
	Row      *int   `json:"row,omitempty" jsonschema:"原始行模式·源文件行号（表头为 1，与线索锚点 sourceRow 同口径，≥ 1）"`
}

// feeEvidenceOutput embeds the mode's answer. Clue mode answers the
// filtered clues verbatim (fees.RiskClue — the same wire shape fee_risks
// answers, one contract two transports); raw mode answers fees.EvidenceRow
// (the HTTP GET /api/fees/batches/{id}/evidence shape).
type feeEvidenceOutput struct {
	Mode string `json:"mode"` // "clue" | "raw"
	// clue mode (raw mode answers the empty array — #93: arrays are never
	// null, NOT even the "other half's" array; the mode field alone tells
	// which half answered. Scenario/Site ride omitempty: absent strings are
	// the honest raw-mode wire, absent ARRAYS are not.)
	Scenario string          `json:"scenario,omitempty"`
	Site     string          `json:"site,omitempty"`
	Clues    []fees.RiskClue `json:"clues"`
	// raw mode
	fees.EvidenceRow
	Note string `json:"note"`
}

// feeEvidence routes the two modes.
func feeEvidence(ctx context.Context, svc *services.Services, in feeEvidenceInput) (*mcp.CallToolResult, feeEvidenceOutput, error) {
	if in.Batch != "" {
		return feeEvidenceRaw(ctx, svc, in)
	}
	return feeEvidenceClue(ctx, svc, in)
}

// feeEvidenceClue runs the preset for one site and answers its clues.
func feeEvidenceClue(ctx context.Context, svc *services.Services, in feeEvidenceInput) (*mcp.CallToolResult, feeEvidenceOutput, error) {
	if in.Site == "" {
		return nil, feeEvidenceOutput{}, fmt.Errorf("site 必填（线索模式按站址过滤——查全部线索用 fee_risks；查原始行用 batch/row 模式）")
	}
	scenario, err := fees.ParseRiskScenario(in.Scenario)
	if err != nil {
		return nil, feeEvidenceOutput{}, fmt.Errorf("scenario %s", err)
	}
	feeType, err := fees.ParseFeeType(in.FeeType)
	if err != nil {
		return nil, feeEvidenceOutput{}, fmt.Errorf("feeType %s", err)
	}
	if in.Year == nil {
		return nil, feeEvidenceOutput{}, fmt.Errorf("year 必填（2000–2100）")
	}
	year := *in.Year
	if year < 2000 || year > 2100 {
		return nil, feeEvidenceOutput{}, fmt.Errorf("year 必须是 2000–2100，收到 %d", year)
	}
	month := 0
	if in.Month != nil {
		month = *in.Month
		if month < 1 || month > 12 {
			return nil, feeEvidenceOutput{}, fmt.Errorf("month 必须是 1–12，收到 %d", month)
		}
	}
	limit := 20
	if in.Limit != nil {
		if *in.Limit < 1 {
			return nil, feeEvidenceOutput{}, fmt.Errorf("limit 必须是 ≥ 1 的整数，收到 %d", *in.Limit)
		}
		limit = *in.Limit
	}
	if limit > 50 {
		limit = 50
	}
	engine := fees.Query(ctx)
	result, err := svc.Fees.Risks(ctx, engine, scenario, fees.RiskQuery{
		FeeType: feeType, Year: year, Month: month, Limit: limit,
	})
	if err != nil {
		// The fee_risks ruling (#103): the engine-not-built refusal is a
		// CONTRACT answer and passes through verbatim; real engine failures
		// carry filesystem detail — stderr gets it, the tool error stays
		// generic.
		if errors.Is(err, fees.ErrEngineNotBuilt) {
			return nil, feeEvidenceOutput{}, err
		}
		slog.Error("fee_evidence engine failure", "scenario", scenario, "site", in.Site, "error", err.Error())
		return nil, feeEvidenceOutput{}, fmt.Errorf("费用线索证据查询失败（引擎内部错误，详情见服务端日志）")
	}
	// keep the site's clues — the scenario ran with the governed bound
	// (limit, largest signal first); a site outside the bound is an honest
	// empty answer, the note says so.
	clues := make([]fees.RiskClue, 0, len(result.Clues))
	for _, c := range result.Clues {
		if c.SiteCode == in.Site {
			clues = append(clues, c)
		}
	}
	return nil, feeEvidenceOutput{
		Mode:     "clue",
		Scenario: string(scenario),
		Site:     in.Site,
		Clues:    clues,
		Note:     feeEvidenceNoteClue,
	}, nil
}

// feeEvidenceRaw resolves an anchor to the snapshot's original row (#39).
func feeEvidenceRaw(ctx context.Context, svc *services.Services, in feeEvidenceInput) (*mcp.CallToolResult, feeEvidenceOutput, error) {
	if in.Row == nil {
		return nil, feeEvidenceOutput{}, fmt.Errorf("row 必填（原始行模式：源文件行号，表头=1，与线索锚点 sourceRow 同口径）")
	}
	if *in.Row < 1 {
		return nil, feeEvidenceOutput{}, fmt.Errorf("row 必须是 ≥ 1 的整数，收到 %d", *in.Row)
	}
	ev, err := svc.Fees.EvidenceRow(ctx, in.Batch, *in.Row)
	if err != nil {
		// not-found and read failures carry no filesystem secrets worth
		// hiding, but the generic-vs-detail ruling is the query tools':
		// detail to stderr, vocabulary to the client. ErrNotFound is a
		// contract answer (404 on the HTTP twin) and passes through.
		if errors.Is(err, domain.ErrNotFound) {
			return nil, feeEvidenceOutput{}, err
		}
		slog.Error("fee_evidence raw read failure", "batch", in.Batch, "row", *in.Row, "error", err.Error())
		return nil, feeEvidenceOutput{}, fmt.Errorf("原始行回读失败（快照读取错误，详情见服务端日志）")
	}
	return nil, feeEvidenceOutput{
		Mode:        "raw",
		EvidenceRow: ev,
		Note:        ev.Note,
	}, nil
}

// feeLinksListInput is the links filter: code optional (empty = the most
// recent links), status optional (the closed lifecycle set), the
// governed limit.
type feeLinksListInput struct {
	Code   string `json:"code,omitempty" jsonschema:"费用站址编码（可选——给定则只答该编码的关联行；缺省答最近的关联）"`
	Status string `json:"status,omitempty" jsonschema:"状态过滤（可选）：confirmed（已确认）/ pending（模糊提案待核实）/ dismissed（已驳回留痕）"`
	Limit  *int   `json:"limit,omitempty" jsonschema:"结果条数上限，缺省 20，最大 50（与 fee_risks 同款治理边界）；必须 ≥ 1"`
}

// linkItem is the camelCase wire shape of one link row (domain.FeeSiteLink
// carries Go field names; the MCP/HTTP faces answer camelCase).
type linkItem struct {
	ID           string  `json:"id"`
	FeeSiteCode  string  `json:"feeSiteCode"`
	ResourceID   string  `json:"resourceId"`
	ResourceType string  `json:"resourceType"`
	ResourceKey  string  `json:"resourceKey"`
	Grade        string  `json:"grade"`
	Status       string  `json:"status"`
	Similarity   float64 `json:"similarity,omitempty"`
	Source       string  `json:"source"`
	BatchID      string  `json:"batchId,omitempty"`
	CreatedAt    string  `json:"createdAt"`
	DecidedAt    string  `json:"decidedAt,omitempty"`
}

type feeLinksListOutput struct {
	Code  string     `json:"code"`
	Items []linkItem `json:"items"`
	Total int        `json:"total"`
	Note  string     `json:"note"`
}

// feeLinksList answers the link states.
func feeLinksList(ctx context.Context, svc *services.Services, in feeLinksListInput) (*mcp.CallToolResult, feeLinksListOutput, error) {
	filter := repo.FeeLinkFilter{Code: in.Code}
	if in.Status != "" {
		switch domain.LinkStatus(in.Status) {
		case domain.LinkStatusConfirmed, domain.LinkStatusPending, domain.LinkStatusDismissed:
			filter.Status = domain.LinkStatus(in.Status)
		default:
			return nil, feeLinksListOutput{}, fmt.Errorf(
				"status 必须是 confirmed / pending / dismissed，收到 %q", in.Status)
		}
	}
	limit := 20
	if in.Limit != nil {
		if *in.Limit < 1 {
			return nil, feeLinksListOutput{}, fmt.Errorf("limit 必须是 ≥ 1 的整数，收到 %d", *in.Limit)
		}
		limit = *in.Limit
	}
	if limit > 50 {
		limit = 50
	}
	filter.Limit = limit
	links, err := svc.Fees.ListLinks(ctx, filter)
	if err != nil {
		slog.Error("fee_links_list failure", "code", in.Code, "error", err.Error())
		return nil, feeLinksListOutput{}, fmt.Errorf("关联状态查询失败（数据库错误，详情见服务端日志）")
	}
	items := make([]linkItem, 0, len(links))
	for _, l := range links {
		item := linkItem{
			ID: l.ID, FeeSiteCode: l.FeeSiteCode, ResourceID: l.ResourceID,
			ResourceType: l.ResourceType.String(), ResourceKey: l.ResourceKey,
			Grade: string(l.Grade), Status: string(l.Status), Similarity: l.Similarity,
			Source: string(l.Source), BatchID: l.BatchID,
			CreatedAt: l.CreatedAt.UTC().Format(time.RFC3339),
		}
		if l.DecidedAt != nil {
			item.DecidedAt = l.DecidedAt.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	return nil, feeLinksListOutput{
		Code:  in.Code,
		Items: items, // never nil (#93)
		Total: len(items),
		Note:  feeLinksNote,
	}, nil
}
