package mcpserver

// feetools.go — the read-only fee tools of the MCP surface:
// fee_risks (#58 stage 5, v0.15.0), fee_totals / fee_duplicates (#120).
// The risk tool runs ONE fixed preset with the documented default
// thresholds — the AI may not tune statistical bars (ratio/z/minSites
// stay governed defaults; a human adjusts them via HTTP/CLI), it may
// only pick the scenario, the fee type, the period and the result size.
// The two query tools open the aggregate half to the AI loop: the
// per-site totals and the cross-batch duplicate candidates, with the
// SAME wire shapes the HTTP face answers (one contract, two transports,
// the v0.15.0 ruling) and a TIGHTER result bound than HTTP (limit
// default 20, cap 50 — the fee_risks governed bound; the AI consumption
// face does not need five hundred rows).
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

const (
	feeRisksName      = "fee_risks"
	feeTotalsName     = "fee_totals"
	feeDuplicatesName = "fee_duplicates"
)

// addFeeTools registers the fee analysis tools (read-only).
func addFeeTools(server *mcp.Server, svc *services.Services) {
	if svc.Fees == nil {
		return // the fee service is not wired (no data dir) — no tools
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: feeRisksName,
		Description: "只读·费用风险八场景查询：运行一个固定风险预设（" +
			"retired-billing 退租后仍在计费 / duplicate-settlement 跨批次重复结算 / ledger-mismatch 合同账单金额差异 / " +
			"spike 电费环比异常上涨 / outlier 同类站月度金额离群 / unlinked 零资源计费 / idle-billing 有费无量（账期在计费但指标面零采集点）/ " +
			"missing-billing 有量无费（资源有采集点但费用面无该类型计费记录，疑似漏收）），返回风险线索" +
			"（触发指标、显式阈值、行级证据锚点 sourceFile+sourceRow+importBatch 可回读快照原文）。" +
			"阈值用治理默认值（ratio=0.30 / z=3.0 / minSites=8 / status=停用,退网），AI 不可调——调参是人类通道（HTTP/CLI）。" +
			"输出是待核实线索而非认定结论（账单存在 ≠ 重复付款认定）；线索可携带人工裁定标记 decision=verified（已核实属实）——已驳回（dismissed）的线索默认隐藏并在信封 dismissed 计数（裁定是人类通道 HTTP/CLI，AI 只读）。默认构建未编译分析引擎时本工具明确报错。" +
			"典型配合：先本工具拿线索，再 graph_traverse / resource_search 下钻资源侧。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feeRisksInput) (*mcp.CallToolResult, feeRisksOutput, error) {
		return feeRisks(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: feeTotalsName,
		Description: "只读·费用汇总查询：某费用类型下各站址的账期合计（金额为整数分），AI 的基础事实问句（「这个站今年电费多少」）。" +
			"参数：feeType + year 必填、month 可选（缺省整年各月）、limit 缺省 20 上限 50（比 HTTP 面更紧）。" +
			"与 HTTP GET /api/fees/query/totals 同形（一契约两传输）。默认构建未编译分析引擎时明确报错。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feeQueryInput) (*mcp.CallToolResult, feeTotalsOutput, error) {
		return feeTotals(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name: feeDuplicatesName,
		Description: "只读·跨批次重复结算候选查询：同 (站址, 账期) 被多个导入批次计费的形态（批次共存是设计——候选供人工核对，账单存在 ≠ 重复付款认定）。" +
			"参数：feeType + year 必填、month 可选、limit 缺省 20 上限 50。与 HTTP GET /api/fees/query/duplicates 同形。默认构建未编译分析引擎时明确报错。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feeQueryInput) (*mcp.CallToolResult, feeDuplicatesOutput, error) {
		return feeDuplicates(ctx, svc, in)
	})
}

// feeQueryInput is the shared typed input of the two query tools: the
// scope plus the result bound, nothing else (no thresholds exist on
// these presets — nothing to govern, nothing to smuggle).
type feeQueryInput struct {
	FeeType string `json:"feeType" jsonschema:"费用类型（必填）：electricity（电费）/ tower_lease（铁塔租费）/ repair（修理费）/ maintenance（综维费）"`
	Year    *int   `json:"year" jsonschema:"年份（必填，2000–2100）"`
	Month   *int   `json:"month,omitempty" jsonschema:"月份（可选 1–12，缺省整年各月）"`
	Limit   *int   `json:"limit,omitempty" jsonschema:"结果条数上限，缺省 20，最大 50（与 fee_risks 同款治理边界）；必须 ≥ 1"`
}

// feeTotalsOutput mirrors the HTTP totals envelope verbatim.
type feeTotalsOutput struct {
	Preset  string           `json:"preset"`
	FeeType string           `json:"feeType"`
	Year    int              `json:"year"`
	Month   int              `json:"month"`
	Items   []fees.TotalsRow `json:"items"`
	Total   int              `json:"total"`
}

// feeDuplicatesOutput mirrors the HTTP duplicates envelope verbatim.
type feeDuplicatesOutput struct {
	Preset  string              `json:"preset"`
	FeeType string              `json:"feeType"`
	Year    int                 `json:"year"`
	Month   int                 `json:"month"`
	Items   []fees.DuplicateRow `json:"items"`
	Total   int                 `json:"total"`
}

// parseFeeQueryScope validates the shared input: closed fee type, the
// period bounds, and the governed limit (default 20, cap 50, negative
// refused explicitly — #91's ruling).
func parseFeeQueryScope(in feeQueryInput) (fees.FeeType, int, int, int, error) {
	feeType, err := fees.ParseFeeType(in.FeeType)
	if err != nil {
		return "", 0, 0, 0, fmt.Errorf("feeType %s", err)
	}
	if in.Year == nil {
		return "", 0, 0, 0, fmt.Errorf("year 必填（2000–2100）")
	}
	year := *in.Year
	if year < 2000 || year > 2100 {
		return "", 0, 0, 0, fmt.Errorf("year 必须是 2000–2100，收到 %d", year)
	}
	month := 0
	if in.Month != nil {
		month = *in.Month
		if month < 1 || month > 12 {
			return "", 0, 0, 0, fmt.Errorf("month 必须是 1–12，收到 %d", month)
		}
	}
	limit := 20
	if in.Limit != nil {
		if *in.Limit < 1 {
			return "", 0, 0, 0, fmt.Errorf("limit 必须是 ≥ 1 的整数，收到 %d", *in.Limit)
		}
		limit = *in.Limit
	}
	if limit > 50 {
		limit = 50
	}
	return feeType, year, month, limit, nil
}

// feeQueryError maps the two query tools' failures the #103 way: the
// engine-not-built refusal is a CONTRACT answer and passes through
// verbatim; real engine failures carry DuckDB/parquet texts with
// absolute paths — the detail goes to stderr (slog), the tool error
// answers the generic vocabulary (the feeRisks ruling).
func feeQueryError(tool string, err error) error {
	if errors.Is(err, fees.ErrEngineNotBuilt) {
		return err
	}
	slog.Error("fee query engine failure", "tool", tool, "error", err.Error())
	return fmt.Errorf("费用查询失败（引擎内部错误，详情见服务端日志）")
}

// feeTotals answers the per-site aggregate question.
func feeTotals(ctx context.Context, svc *services.Services, in feeQueryInput) (*mcp.CallToolResult, feeTotalsOutput, error) {
	feeType, year, month, limit, err := parseFeeQueryScope(in)
	if err != nil {
		return nil, feeTotalsOutput{}, err
	}
	rows, err := svc.Fees.QueryTotals(ctx, fees.Query(ctx), feeType, year, month, limit)
	if err != nil {
		return nil, feeTotalsOutput{}, feeQueryError(feeTotalsName, err)
	}
	if rows == nil {
		rows = []fees.TotalsRow{} // arrays are never null (#93 discipline)
	}
	return nil, feeTotalsOutput{
		Preset: "totals", FeeType: string(feeType), Year: year, Month: month,
		Items: rows, Total: len(rows),
	}, nil
}

// feeDuplicates answers the cross-batch candidate question.
func feeDuplicates(ctx context.Context, svc *services.Services, in feeQueryInput) (*mcp.CallToolResult, feeDuplicatesOutput, error) {
	feeType, year, month, limit, err := parseFeeQueryScope(in)
	if err != nil {
		return nil, feeDuplicatesOutput{}, err
	}
	rows, err := svc.Fees.QueryDuplicates(ctx, fees.Query(ctx), feeType, year, month, limit)
	if err != nil {
		return nil, feeDuplicatesOutput{}, feeQueryError(feeDuplicatesName, err)
	}
	if rows == nil {
		rows = []fees.DuplicateRow{} // arrays are never null (#93 discipline)
	}
	return nil, feeDuplicatesOutput{
		Preset: "duplicates", FeeType: string(feeType), Year: year, Month: month,
		Items: rows, Total: len(rows),
	}, nil
}

// feeRisksInput is the typed input: scenario + scope + size only.
type feeRisksInput struct {
	Scenario string `json:"scenario" jsonschema:"风险场景（必填）：retired-billing（退用仍计费）/ duplicate-settlement（跨批次重复）/ ledger-mismatch（台账金额差异）/ spike（环比突增）/ outlier（同批离群）/ unlinked（零资源计费）/ idle-billing（有费无量——账期在计费但指标面零采集点，采集缺失与空置不可区分仅供人工核实）/ missing-billing（有量无费——资源有采集点但费用面无该类型计费记录，无月度账单不等于漏收认定仅供人工核实）"`
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
