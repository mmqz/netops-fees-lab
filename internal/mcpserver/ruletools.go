package mcpserver

// ruletools.go — issue #2 phase 1: the fee surface's FIRST write-side MCP
// tool, fee_rules_create — the AI creation channel of the L1 configurable
// rules. The channel doctrine (the W37 design):
//
//	AI 只能创建，人类启用/停用 —— the tool creates one rule row
//	DISABLED (观察态): a fresh rule fires NOTHING until a human flips
//	enabled via the admin-tier HTTP routes or the CLI (fee rules
//	enable). The MCP face has NO enable/disable tool — the same human
//	channel doctrine as risk-clue decisions (#119).
//
//	阈值变更留痕 —— every call lands its 'created' audit row with
//	actor="mcp"; phase 1 has no update channel, so a threshold change
//	IS disable-old + create-new, both audited.
//
//	封闭枚举 —— metric/aggregation/dimension/comparator/severity are
//	closed sets re-validated in the service AND in the engine before any
//	SQL is built; a rule's free text (name/description) never reaches
//	SQL.
//
// Registration: NewServer ONLY. The --read-only variant (issue #10)
// excludes it BY the readOnlyHint filter — the invariant (the read-only
// registry == the readOnlyHint=true set) is pinned by
// TestMCPReadOnlyRegistryIsReadOnlyHintSet.

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-fees-lab/internal/fees"
	"github.com/mmqz/netops-fees-lab/internal/services"
)

const feeRulesCreateName = "fee_rules_create"

// addFeeRuleTool registers the #2 creation tool (write side, NewServer
// only). A nil fee service wires nothing — the same discipline as the
// read-only fee tools.
func addFeeRuleTool(server *mcp.Server, svc *services.Services) {
	if svc.Fees == nil {
		return // the fee service is not wired (no data dir) — no tool
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: feeRulesCreateName,
		Description: "写入·创建可配置费用风险规则（#2 L1 六元组：度量/聚合/维度/算子/阈值/窗口）。" +
			"新规则默认停用（观察态）——创建后不参与任何分析，需人类经 HTTP 管理级令牌或 CLI `fees rules enable` 启用（MCP 无启用/停用面）。" +
			"封闭枚举：metric ∈ {amount 金额, rows 行数}，aggregation ∈ {sum, avg, max, count}（rows 度量固定 count），dimension 固定 site_month（站址×账期），" +
			"comparator ∈ {gt, gte, lt, lte, mom_ratio 环比涨幅比}，severity ∈ {high, medium, low}；threshold ≥ 0（金额类=分、行数类=行数、环比类=涨幅比如 0.30 表示 +30%），windowMonths 1–12（仅 mom_ratio 用作基期回看）。" +
			"每次调用落 'created' 审计行（actor=mcp）；改阈值=停用旧规则+建新规则，全程审计可追溯。规则名唯一，重名拒绝。" +
			"启用后的规则经 fee_risks（scenario=custom-rules）参与分析：线索携带规则名/严重度/阈值口径与行级证据锚点——账单存在 ≠ 认定结论。",
		Annotations: ruleCreateHints(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in feeRuleCreateInput) (*mcp.CallToolResult, feeRuleCreateOutput, error) {
		return feeRulesCreate(ctx, svc, in.toRuleInput())
	})
}

// feeRuleCreateInput is the tool's OWN typed input (not the shared
// fees.RuleInput): the SDK derives the JSON schema from the struct, so
// the optionals must be pointers with omitempty — otherwise windowMonths
// and description turn REQUIRED on the wire (the #2 acceptance found it:
// a call omitting them answered a schema error, not the rule contract).
type feeRuleCreateInput struct {
	Name         string  `json:"name" jsonschema:"规则名，必填（唯一，≤120 字；线索溯源展示）"`
	FeeType      string  `json:"feeType" jsonschema:"费用类型，必填：electricity（电费）/ tower_lease（铁塔租费）/ repair（修理费）/ maintenance（综维费）"`
	Metric       string  `json:"metric" jsonschema:"度量，必填：amount（金额，单位分）/ rows（行数）"`
	Aggregation  string  `json:"aggregation" jsonschema:"聚合，必填：sum / avg / max / count（rows 度量固定 count——行数即聚合）"`
	Dimension    string  `json:"dimension" jsonschema:"维度，本期固定 site_month（站址×账期）"`
	Comparator   string  `json:"comparator" jsonschema:"算子，必填：gt / gte / lt / lte / mom_ratio（环比涨幅比，如 0.30 表示 +30%）"`
	Threshold    float64 `json:"threshold" jsonschema:"阈值，必填，≥ 0（金额类=分、行数类=行数、环比类=涨幅比）"`
	WindowMonths *int    `json:"windowMonths,omitempty" jsonschema:"环比基期回看月数（可选 1-12，仅 mom_ratio 用，缺省 1）"`
	Severity     string  `json:"severity" jsonschema:"严重度，必填：high / medium / low"`
	Description  string  `json:"description,omitempty" jsonschema:"规则口径描述（可选，≤500 字，随线索展示）"`
}

// toRuleInput adapts the wire shape to the shared service payload.
func (in feeRuleCreateInput) toRuleInput() fees.RuleInput {
	window := 0
	if in.WindowMonths != nil {
		window = *in.WindowMonths
	}
	return fees.RuleInput{
		Name: in.Name, FeeType: in.FeeType, Metric: in.Metric,
		Aggregation: in.Aggregation, Dimension: in.Dimension,
		Comparator: in.Comparator, Threshold: in.Threshold,
		WindowMonths: window, Severity: in.Severity, Description: in.Description,
	}
}

// feeRuleCreateOutput passes the created rule through verbatim (the HTTP
// POST /api/fees/rules answers the same six-tuple shape — one contract,
// two transports).
type feeRuleCreateOutput struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	FeeType      string  `json:"feeType"`
	Metric       string  `json:"metric"`
	Aggregation  string  `json:"aggregation"`
	Dimension    string  `json:"dimension"`
	Comparator   string  `json:"comparator"`
	Threshold    float64 `json:"threshold"`
	WindowMonths int     `json:"windowMonths"`
	Severity     string  `json:"severity"`
	Description  string  `json:"description,omitempty"`
	Enabled      bool    `json:"enabled"`
}

// feeRulesCreate runs the same service call the HTTP handler uses. The
// refusal family (closed-set violations, name collisions, bounds) IS the
// operator-facing contract — the texts pass through verbatim; nothing
// here can fail with an engine error (no query runs).
func feeRulesCreate(ctx context.Context, svc *services.Services, in fees.RuleInput) (*mcp.CallToolResult, feeRuleCreateOutput, error) {
	rule, err := svc.Fees.CreateRule(ctx, in, "mcp")
	if err != nil {
		return nil, feeRuleCreateOutput{}, err
	}
	return nil, feeRuleCreateOutput{
		ID: rule.ID, Name: rule.Name, FeeType: string(rule.FeeType),
		Metric: rule.Metric, Aggregation: rule.Aggregation, Dimension: rule.Dimension,
		Comparator: rule.Comparator, Threshold: rule.Threshold,
		WindowMonths: rule.WindowMonths, Severity: rule.Severity,
		Description: rule.Description, Enabled: rule.Enabled,
	}, nil
}
