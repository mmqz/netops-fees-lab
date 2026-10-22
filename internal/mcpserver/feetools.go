package mcpserver

// feetools.go — the read-only fee risk-analysis tool of the MCP surface
// (#58 stage 5, delivered with v0.15.0): the AI loop's entry into the six
// risk scenarios
// of #64. The tool runs ONE fixed preset with the documented default
// thresholds — the AI may not tune statistical bars (ratio/z/minSites
// stay governed defaults; a human adjusts them via HTTP/CLI), it may
// only pick the scenario, the fee type, the period and the result size.
// Evidence anchors travel with every clue (source file + row + batch),
// so the AI can quote the snapshot coordinates in its analysis — the
// same 账单存在 ≠ 认定结论 doctrine the HTTP/CLI surfaces enforce.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-lab/internal/fees"
	"github.com/mmqz/netops-lab/internal/services"
)

const feeRisksName = "fee_risks"

// addFeeTools registers the fee analysis tools (read-only).
func addFeeTools(server *mcp.Server, svc *services.Services) {
	if svc.Fees == nil {
		return // the fee service is not wired (no data dir) — no tools
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: feeRisksName,
		Description: "只读·费用风险七场景查询：运行一个固定风险预设（" +
			"retired-billing 退租后仍在计费 / duplicate-settlement 跨批次重复结算 / ledger-mismatch 合同账单金额差异 / " +
			"spike 电费环比异常上涨 / outlier 同类站单位成本离群 / unlinked 零资源计费 / idle-billing 有费无量（账期在计费但指标面零采集点）），返回风险线索" +
			"（触发指标、显式阈值、行级证据锚点 sourceFile+sourceRow+importBatch 可回读快照原文）。" +
			"阈值用治理默认值（ratio=0.30 / z=3.0 / minSites=8 / status=停用,退网），AI 不可调——调参是人类通道（HTTP/CLI）。" +
			"输出是待核实线索而非认定结论（账单存在 ≠ 重复付款认定）。默认构建未编译分析引擎时本工具明确报错。" +
			"典型配合：先本工具拿线索，再 graph_traverse / resource_search 下钻资源侧。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feeRisksInput) (*mcp.CallToolResult, feeRisksOutput, error) {
		return feeRisks(ctx, svc, in)
	})
}

// feeRisksInput is the typed input: scenario + scope + size only.
type feeRisksInput struct {
	Scenario string `json:"scenario" jsonschema:"风险场景（必填）：retired-billing（退用仍计费）/ duplicate-settlement（跨批次重复）/ ledger-mismatch（台账金额差异）/ spike（环比突增）/ outlier（同批离群）/ unlinked（零资源计费）/ idle-billing（有费无量——账期在计费但指标面零采集点，采集缺失与空置不可区分仅供人工核实）"`
	FeeType  string `json:"feeType" jsonschema:"费用类型（必填）：electricity（电费）/ tower_lease（铁塔租费）/ repair（修理费）/ maintenance（综维费）"`
	Year     *int   `json:"year" jsonschema:"年份（必填，2000–2100）"`
	Month    *int   `json:"month,omitempty" jsonschema:"月份（可选 1–12，缺省整年各月）"`
	Limit    *int   `json:"limit,omitempty" jsonschema:"线索条数上限，缺省 20，最大 50（工具面比 HTTP 更紧——AI 消费面不需要千条）；必须 ≥ 1"`
}

// feeRisksOutput passes the fees RiskResult contract through verbatim —
// the MCP wire shape IS the HTTP shape (one contract, two transports).
type feeRisksOutput struct {
	fees.RiskResult
}

// feeRisks runs one scenario through the same service call the HTTP
// handler uses (fixed presets; no SQL ever crosses the tool boundary).
func feeRisks(ctx context.Context, svc *services.Services, in feeRisksInput) (*mcp.CallToolResult, feeRisksOutput, error) {
	scenario, err := fees.ParseRiskScenario(in.Scenario)
	if err != nil {
		return nil, feeRisksOutput{}, fmt.Errorf("scenario %s", err)
	}
	feeType, err := fees.ParseFeeType(in.FeeType)
	if err != nil {
		return nil, feeRisksOutput{}, fmt.Errorf("feeType %s", err)
	}
	if in.Year == nil {
		return nil, feeRisksOutput{}, fmt.Errorf("year 必填（2000–2100）")
	}
	limit := 20
	if in.Limit != nil {
		// #91: explicit over silent — resource_search/alerts_query answer a
		// negative limit with a validation error; this tool used to silently
		// clamp it to the default 20, one tool contradicting the surface's
		// own contract.
		if *in.Limit < 1 {
			return nil, feeRisksOutput{}, fmt.Errorf("limit 必须是 ≥ 1 的整数，收到 %d", *in.Limit)
		}
		limit = *in.Limit
	}
	if limit > 50 {
		limit = 50
	}
	month := 0
	if in.Month != nil {
		month = *in.Month
	}
	engine := fees.Query(ctx)
	result, err := svc.Fees.Risks(ctx, engine, scenario, fees.RiskQuery{
		FeeType: feeType, Year: *in.Year, Month: month, Limit: limit,
	})
	if err != nil {
		// #103: engine failures carry DuckDB/parquet error texts with absolute
		// filesystem paths; the HTTP twin logs the detail and answers the
		// generic 500 message. The MCP face has no operator response body —
		// the detail goes to the server's stderr (slog default logger), and
		// the tool error carries the same generic vocabulary. The ENGINE-
		// NOT-BUILT refusal is a CONTRACT answer (the HTTP twin's 501), not
		// an internal leak — it passes through verbatim.
		if errors.Is(err, fees.ErrEngineNotBuilt) {
			return nil, feeRisksOutput{}, err
		}
		slog.Error("fee_risks engine failure", "scenario", scenario, "error", err.Error())
		return nil, feeRisksOutput{}, fmt.Errorf("费用风险查询失败（引擎内部错误，详情见服务端日志）")
	}
	return nil, feeRisksOutput{RiskResult: result}, nil
}
