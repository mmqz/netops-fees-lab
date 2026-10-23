// graphtools.go — the v0.9 read-only analysis tools of the MCP surface
// (R4): graph_traverse, graph_impact, graph_context, alerts_query and
// metrics_query. With resource_search, the four lab tools and the fee
// risk tool they form the MCP tool surface — a closed read-only set
// pinned by TestMCPToolRegistry (the count lives in the registry test,
// not in prose).
//
// Why: the v0.7 data-model arc (graph engine §10, detection alerts, metric
// timeseries) and the v0.8 real-data milestone were HTTP/CLI-only — the AI
// could search resources and mutate the simulation lab, but could NOT run
// the root-cause analysis loop (query alerts → impact/traverse → context →
// metric evidence) that the workbench exists for. For an agent competition
// the agent must be able to reason over the graph itself.
//
// Non-negotiables (unchanged, and now structurally wider):
//
//   - ALL five new tools are READ-ONLY. No detection run (it writes alerts
//     and run_state — the operator/CLI/HTTP explicit run stays), no ack or
//     clear (human-only audit edges, the alerts §18 lifecycle), no
//     resource-domain writes of any kind. The resource-domain write
//     prohibition of the SEC doctrine holds; only the READ surface widens.
//   - every tool binds the SAME composition-root instances the HTTP API
//     uses: topology.Traverse/Impact over svc.GraphStore() (issue #41's
//     single store binding), svc.GraphContext (the one-shot assembly),
//     svc.DB().Alerts.ListAlerts (the same repo the HTTP handler calls)
//     and svc.Metrics.ResourceSeries. No SQL in this package, no second
//     engine, no bypass.
//   - tool errors use MCP tool-error semantics with the HTTP layer's
//     failure vocabulary (404 not_found → "资源不存在…" texts).
//
// jsonschema tags are plain prose descriptions (the google/jsonschema-go
// contract, ADR-0003 lesson).
package mcpserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/services"
	"github.com/mmqz/netops-lab/internal/topology"
)

// Tool names of the five read-only analysis tools (v0.9 R4).
const (
	graphTraverseName = "graph_traverse"
	graphImpactName   = "graph_impact"
	graphContextName  = "graph_context"
	alertsQueryName   = "alerts_query"
	metricsQueryName  = "metrics_query"
)

// defaultGraphDepth mirrors the HTTP surface defaults so an agent hopping
// between MCP and HTTP sees the same budgets.
const (
	defaultGraphMaxDepth = topology.DefaultTraverseMaxDepth
	defaultGraphMaxNodes = topology.DefaultTraverseMaxNodes
	defaultAlertsLimit   = 20
	maxAlertsLimit       = 100
)

// addGraphTools registers the five read-only analysis tools against the
// composition root. Called by NewServer only — the tool surface stays
// closed (the registry test pins the exact ten).
func addGraphTools(server *mcp.Server, svc *services.Services) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        graphTraverseName,
		Description: "只读图查询·纵向穿透：从指定资源出发沿关系图 BFS（direction=down 向下找根因（默认，§10.8 #4）/ up 向上找业务面 / both）。返回 root、nodes（含 layer/depth/status/runState）、edges（存库方向的依赖关系）、truncated 与 stats。这是跨层穿透分析（如电路→波道→光缆→纤芯）的引擎通道。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in graphTraverseInput) (*mcp.CallToolResult, graphTraverseOutput, error) {
		return graphTraverse(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        graphImpactName,
		Description: "只读图查询·影响与根因分类（§10.5/§10.6）：direction=down 根因追查（从疑点向下，返回 candidates 根因候选——异常节点且其下游无其他异常，附中文判定理由与距离）；direction=up 影响面收集（从异常源向上，返回 impacted 受影响业务链）。异常判定 = run_state ∈ {down, degraded}。返回 root、impacted、candidates、truncated。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in graphImpactInput) (*mcp.CallToolResult, graphImpactOutput, error) {
		return graphImpact(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        graphContextName,
		Description: "只读·一站式分析上下文（§7.3）：一次调用返回资源完整详情（含 runState）、depth 跳邻接子图（neighbors）、每条指标序列的最新点（metrics）与近期异常（recentAnomalies）。AI 根因分析的首选入口——单个资源需要「它是什么、旁边是什么、指标怎么说」三问一答时用本工具，不要分别调用多个。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in graphContextInput) (*mcp.CallToolResult, graphContextOutput, error) {
		return graphContext(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        alertsQueryName,
		Description: "只读·检测告警查询：按 state（firing 触发中（默认）/ acked 已确认 / cleared 已清除）与 severity（error/warning/info）过滤，返回告警列表（规则、阈值比较器、最新值、序列与资源投影、触发/确认/清除时间线）。确认（ack）与清除（clear）是仅人类操作，不提供任何工具。检测运行本身是操作员动作（CLI detect / HTTP），MCP 只读其结果。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in alertsQueryInput) (*mcp.CallToolResult, alertsQueryOutput, error) {
		return alertsQuery(ctx, svc, in)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        metricsQueryName,
		Description: "只读·指标时序查询：返回指定资源的指标序列及数据点（ts/value/quality，quality=suspect 为疑似异常点）。可选 keys 过滤（合法键见指标目录）。质量告警与劣化趋势（如接收光功率 7 日线性劣化）的证据通道——先 alerts_query 拿告警，再本工具取曲线验证。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in metricsQueryInput) (*mcp.CallToolResult, metricsQueryOutput, error) {
		return metricsQuery(ctx, svc, in)
	})
}

// ---- graph_traverse ----

// graphTraverseInput is the typed input of graph_traverse (mirrors the HTTP
// query surface; every field except resourceId is optional).
type graphTraverseInput struct {
	ResourceID string `json:"resourceId" jsonschema:"起点资源 id（uuid，可先 resource_search 取得）"`
	Direction  string `json:"direction,omitempty" jsonschema:"穿透方向：down（默认，沿 from→to 向下找根因）、up（反向向上找业务影响）、both"`
	Layers     string `json:"layers,omitempty" jsonschema:"层号过滤，0..6 逗号列表（如 1,2,3 = 物理+传输层），缺省不过滤"`
	Relations  string `json:"relations,omitempty" jsonschema:"关系类型过滤，逗号列表（如 carried_by,uses_fiber），缺省不过滤；未登记类型返回错误"`
	MaxDepth   *int   `json:"maxDepth,omitempty" jsonschema:"最大 BFS 深度 1..8，缺省 6（剪枝而非过滤——预算截断时 truncated=true）"`
	MaxNodes   *int   `json:"maxNodes,omitempty" jsonschema:"节点预算 1..500，缺省 200"`
	PathOnly   *bool  `json:"pathOnly,omitempty" jsonschema:"true 时额外返回 root 到各叶节点的 paths，缺省 false"`
}

// graphTraverseOutput re-exports the engine's own wire shape — the SAME
// document the HTTP endpoint serves (issue #41's one-implementation rule:
// the engine result type IS the contract).
type graphTraverseOutput = topology.TraverseResult

// graphTraverse runs the engine over the composition root's store binding.
func graphTraverse(ctx context.Context, svc *services.Services, in graphTraverseInput) (*mcp.CallToolResult, graphTraverseOutput, error) {
	direction := topology.DirectionDown
	if in.Direction != "" {
		switch in.Direction {
		case topology.DirectionDown, topology.DirectionUp, topology.DirectionBoth:
			direction = in.Direction
		default:
			return nil, graphTraverseOutput{}, fmt.Errorf("direction 必须是 down/up/both，收到 %q", in.Direction)
		}
	}

	var layers map[int]bool
	if in.Layers != "" {
		layers = map[int]bool{}
		for _, part := range strings.Split(in.Layers, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			v, err := strconv.Atoi(part)
			if err != nil || v < 0 || v > 6 {
				return nil, graphTraverseOutput{}, fmt.Errorf("layers 含非法层号 %q（层号必须是 0..6）", part)
			}
			layers[v] = true
		}
	}

	var relations map[domain.RelationType]bool
	if in.Relations != "" {
		relations = map[domain.RelationType]bool{}
		for _, part := range strings.Split(in.Relations, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			rt := domain.RelationType(part)
			if !rt.IsValid() {
				return nil, graphTraverseOutput{}, fmt.Errorf("relations 含未登记的关系类型 %q", part)
			}
			relations[rt] = true
		}
	}

	maxDepth := defaultGraphMaxDepth
	if in.MaxDepth != nil {
		if *in.MaxDepth < 1 || *in.MaxDepth > topology.MaxTraverseMaxDepth {
			return nil, graphTraverseOutput{}, fmt.Errorf("maxDepth 必须是 1..%d 的整数，收到 %d", topology.MaxTraverseMaxDepth, *in.MaxDepth)
		}
		maxDepth = *in.MaxDepth
	}
	maxNodes := defaultGraphMaxNodes
	if in.MaxNodes != nil {
		if *in.MaxNodes < 1 || *in.MaxNodes > topology.MaxTraverseMaxNodes {
			return nil, graphTraverseOutput{}, fmt.Errorf("maxNodes 必须是 1..%d 的整数，收到 %d", topology.MaxTraverseMaxNodes, *in.MaxNodes)
		}
		maxNodes = *in.MaxNodes
	}
	pathOnly := false
	if in.PathOnly != nil {
		pathOnly = *in.PathOnly
	}

	result, err := topology.Traverse(ctx, svc.GraphStore(), in.ResourceID, topology.TraverseParams{
		Direction: direction,
		Layers:    layers,
		Relations: relations,
		MaxDepth:  maxDepth,
		MaxNodes:  maxNodes,
		PathOnly:  pathOnly,
	})
	if err != nil {
		return nil, graphTraverseOutput{}, fmt.Errorf("穿透失败：%w", err)
	}
	return nil, *result, nil
}

// ---- graph_impact ----

// graphImpactInput is the typed input of graph_impact.
type graphImpactInput struct {
	ResourceID string `json:"resourceId" jsonschema:"分析起点资源 id（uuid）"`
	// #99: the default is UP — impact-surface collection, the same default
	// the HTTP handler (GET /api/resources/{id}/impact) and the CLI (query
	// impact) document. The schema used to claim down as the default, so an
	// AI omitting direction ran root-cause tracing per its own schema and
	// impact collection per the code — a verdict over the wrong subgraph.
	Direction string `json:"direction,omitempty" jsonschema:"up = 影响面收集（从异常源向上汇总业务受损面，默认）；down = 根因追查（从疑点向下找异常根因）"`
	MaxDepth  *int   `json:"maxDepth,omitempty" jsonschema:"BFS 深度预算 1..8，缺省 8"`
}

// graphImpactOutput re-exports the engine's wire shape.
type graphImpactOutput = topology.ImpactResult

// graphImpact runs the impact engine (the same call the HTTP handler makes).
func graphImpact(ctx context.Context, svc *services.Services, in graphImpactInput) (*mcp.CallToolResult, graphImpactOutput, error) {
	direction := "up"
	if in.Direction != "" {
		switch in.Direction {
		case "up", "down":
			direction = in.Direction
		default:
			return nil, graphImpactOutput{}, fmt.Errorf("direction 必须是 up/down（impact 不支持 both），收到 %q", in.Direction)
		}
	}
	maxDepth := 8
	if in.MaxDepth != nil {
		if *in.MaxDepth < 1 || *in.MaxDepth > topology.MaxTraverseMaxDepth {
			return nil, graphImpactOutput{}, fmt.Errorf("maxDepth 必须是 1..%d 的整数，收到 %d", topology.MaxTraverseMaxDepth, *in.MaxDepth)
		}
		maxDepth = *in.MaxDepth
	}
	result, err := topology.Impact(ctx, svc.GraphStore(), in.ResourceID, direction, maxDepth)
	if err != nil {
		return nil, graphImpactOutput{}, fmt.Errorf("影响分析失败：%w", err)
	}
	return nil, *result, nil
}

// ---- graph_context ----

// graphContextInput is the typed input of graph_context.
type graphContextInput struct {
	ResourceID string `json:"resourceId" jsonschema:"目标资源 id（uuid）"`
	Depth      *int   `json:"depth,omitempty" jsonschema:"邻接子图跳数 1..2，缺省 1"`
	Metrics    string `json:"metrics,omitempty" jsonschema:"latest（默认，附每条序列最新点）或 none（跳过指标块）"`
}

// graphContextOutput re-exports the composition root's one-shot assembly
// (the §7.3 document both the HTTP endpoint and the CLI serve).
type graphContextOutput = services.GraphContextResult

// graphContext assembles via svc.GraphContext — ONE implementation.
func graphContext(ctx context.Context, svc *services.Services, in graphContextInput) (*mcp.CallToolResult, graphContextOutput, error) {
	depth := 1
	if in.Depth != nil {
		if *in.Depth < 1 || *in.Depth > 2 {
			return nil, graphContextOutput{}, fmt.Errorf("depth 必须是 1 或 2，收到 %d", *in.Depth)
		}
		depth = *in.Depth
	}
	withMetrics := true
	if in.Metrics != "" {
		switch in.Metrics {
		case "latest", "none":
			withMetrics = in.Metrics == "latest"
		default:
			return nil, graphContextOutput{}, fmt.Errorf("metrics 必须是 latest 或 none，收到 %q", in.Metrics)
		}
	}
	result, err := svc.GraphContext(ctx, in.ResourceID, depth, withMetrics)
	if err != nil {
		return nil, graphContextOutput{}, fmt.Errorf("上下文装配失败：%w", err)
	}
	return nil, *result, nil
}

// ---- alerts_query ----

// alertsQueryInput is the typed input of alerts_query.
type alertsQueryInput struct {
	State      string `json:"state,omitempty" jsonschema:"状态过滤：firing（触发中，默认）/ acked（已确认）/ cleared（已清除）"`
	Severity   string `json:"severity,omitempty" jsonschema:"级别过滤（按规则的级别）：error / warning / info"`
	ResourceID string `json:"resourceId,omitempty" jsonschema:"只看指定资源的告警（uuid，可选）"`
	Role       string `json:"role,omitempty" jsonschema:"关联角色过滤：primary（根因嫌疑）/ secondary（疑似次生）——只看根因或只看症状的分诊入口"`
	Limit      *int   `json:"limit,omitempty" jsonschema:"返回条数上限，缺省 20，最大 100"`
	Offset     *int   `json:"offset,omitempty" jsonschema:"分页偏移，缺省 0"`
}

// alertItem mirrors the HTTP GET /api/alerts projection (the §18 wire
// contract): the alert + joined rule fields + the series → resource
// projection (the AI reasons over the resource, not the bare series id).
// v0.12-R16: the correlation verdict + cause link — the AI triage reads
// 根因/次生 directly instead of re-deriving it via graph_impact.
type alertItem struct {
	AlertID    string  `json:"alertId"`
	RuleID     string  `json:"ruleId"`
	RuleLabel  string  `json:"ruleLabel"`
	MetricKey  string  `json:"metricKey"`
	Comparator string  `json:"comparator"`
	Threshold  float64 `json:"threshold"`
	Window     int     `json:"window"`
	Severity   string  `json:"severity"`
	SeriesID   string  `json:"seriesId"`
	State      string  `json:"state"`
	FiredAt    string  `json:"firedAt"`
	FirstSeen  string  `json:"firstSeen"`
	ClearedAt  string  `json:"clearedAt"`
	AckedAt    string  `json:"ackedAt"`
	AckedBy    string  `json:"ackedBy"`
	LastValue  float64 `json:"lastValue"`
	// 根因/次生关联裁定（v0.12-R16）："" 未标记 | primary 根因嫌疑 | secondary 疑似次生（带最近告警根因链接）。
	CorrelationRole      string `json:"correlationRole"`
	CorrelatedAlertID    string `json:"correlatedAlertId"`    // secondary only
	CorrelatedResourceID string `json:"correlatedResourceId"` // secondary only
	Resource             struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		NaturalKey string `json:"naturalKey"`
		Name       string `json:"name"`
	} `json:"resource"`
}

// alertsQueryOutput is the §18 envelope: items (always an array, never
// null — audit §2.6) + total + paging echo.
type alertsQueryOutput struct {
	Items  []alertItem `json:"items"`
	Total  int         `json:"total"`
	Offset int         `json:"offset"`
	Limit  int         `json:"limit"`
}

// alertsQuery reads through the SAME repo call the HTTP handler uses.
func alertsQuery(ctx context.Context, svc *services.Services, in alertsQueryInput) (*mcp.CallToolResult, alertsQueryOutput, error) {
	if in.State != "" {
		if _, err := domain.ParseAlertState(in.State); err != nil {
			return nil, alertsQueryOutput{}, fmt.Errorf("state 必须是 firing/acked/cleared，收到 %q", in.State)
		}
	}
	if in.Severity != "" {
		if _, err := domain.ParseSeverity(in.Severity); err != nil {
			return nil, alertsQueryOutput{}, fmt.Errorf("severity 必须是 error/warning/info，收到 %q", in.Severity)
		}
	}
	if in.Role != "" {
		if _, err := domain.ParseCorrelationRole(in.Role); err != nil {
			return nil, alertsQueryOutput{}, fmt.Errorf("role 必须是 primary/secondary，收到 %q", in.Role)
		}
	}
	limit := defaultAlertsLimit
	if in.Limit != nil {
		// #100: zero must be a validation error, not "unlimited" — the
		// repo maps limit <= 0 to -1 (all rows), so an explicit 0 used
		// to dump the whole alert table into one MCP result while the
		// schema promised "缺省 20，最大 100".
		if *in.Limit < 1 {
			return nil, alertsQueryOutput{}, fmt.Errorf("limit 必须是 ≥ 1 的整数（缺省 %d，最大 %d），收到 %d", defaultAlertsLimit, maxAlertsLimit, *in.Limit)
		}
		if *in.Limit > maxAlertsLimit {
			limit = maxAlertsLimit
		} else {
			limit = *in.Limit
		}
	}
	offset := 0
	if in.Offset != nil {
		if *in.Offset < 0 {
			return nil, alertsQueryOutput{}, fmt.Errorf("offset 必须是不小于 0 的整数，收到 %d", *in.Offset)
		}
		offset = *in.Offset
	}

	rows, total, err := svc.DB().Alerts.ListAlerts(ctx, alertFilterOf(in, limit, offset))
	if err != nil {
		return nil, alertsQueryOutput{}, fmt.Errorf("告警查询失败：%w", err)
	}
	items := make([]alertItem, 0, len(rows))
	for _, row := range rows {
		var item alertItem
		item.AlertID = row.AlertID
		item.RuleID = row.RuleID
		item.RuleLabel = row.RuleLabel
		item.MetricKey = row.MetricKey
		item.Comparator = string(row.Comparator)
		item.Threshold = row.Threshold
		item.Window = row.Window
		item.Severity = string(row.Severity)
		item.SeriesID = row.SeriesID
		item.State = string(row.State)
		item.FiredAt = row.FiredAt
		item.FirstSeen = row.FirstSeen
		item.ClearedAt = row.ClearedAt
		item.AckedAt = row.AckedAt
		item.AckedBy = row.AckedBy
		item.LastValue = row.LastValue
		item.Resource.ID = row.ResourceID
		item.Resource.Type = row.ResourceType
		item.Resource.NaturalKey = row.NaturalKey
		item.Resource.Name = row.ResourceName
		item.CorrelationRole = row.CorrelationRole
		item.CorrelatedAlertID = row.CorrelatedAlertID
		item.CorrelatedResourceID = row.CorrelatedResourceID
		items = append(items, item)
	}
	return nil, alertsQueryOutput{Items: items, Total: total, Offset: offset, Limit: limit}, nil
}

// alertFilterOf projects the tool input into the repo filter (the same
// projection the HTTP handler performs).
func alertFilterOf(in alertsQueryInput, limit, offset int) repo.AlertFilter {
	return repo.AlertFilter{
		State:      in.State,
		Severity:   in.Severity,
		ResourceID: in.ResourceID,
		Role:       in.Role,
		Limit:      limit,
		Offset:     offset,
	}
}

// ---- metrics_query ----

// metricsQueryInput is the typed input of metrics_query.
type metricsQueryInput struct {
	ResourceID string `json:"resourceId" jsonschema:"目标资源 id（uuid；端口/设备类资源有性能序列）"`
	Keys       string `json:"keys,omitempty" jsonschema:"指标键过滤，逗号列表（如 optical_power_rx_avg_dbm,packet_loss_rx_pct），缺省返回全部序列"`
	// #100: from/to are RFC3339 timestamps — the storage contract compares
	// the normalized UTC strings lexicographically (chronological), so a
	// date-only "2024-05-01" as `to` silently EXCLUDED the entire final day
	// and garbage strings silently matched everything or nothing. The
	// schema used to advertise the wrong format ("ISO 日期") while the HTTP
	// twin already validated RFC3339 — the tool face now does the same.
	From string `json:"from,omitempty" jsonschema:"窗口起点（RFC3339 时间戳，如 2024-05-01T00:00:00Z；缺省不设下界）"`
	To   string `json:"to,omitempty" jsonschema:"窗口终点（RFC3339 时间戳；缺省不设上界；长窗口建议配 agg 降载荷）"`
	Agg  string `json:"agg,omitempty" jsonschema:"聚合粒度：raw（原始点，缺省）/ daily_avg / daily_min / daily_max（按自然日聚合，长窗口首选）"`
	// #123 (ADR-0011 D4): the forecast mode — same tool, no new tool (the
	// D5 freeze). When set, keys must name exactly ONE metric and the
	// output is the linear-trend forecast instead of raw series.
	Forecast   bool `json:"forecast,omitempty" jsonschema:"趋势预测模式（ADR-0011 D4）：改为返回日均值最小二乘线性趋势 + N 天到阈值 + 80% 预测带；此时 keys 必须且只能指定一个指标；<14 天数据会得到诚实的拒算错误；外推参考，非承诺"`
	WindowDays int  `json:"windowDays,omitempty" jsonschema:"预测模式的外推天数（1–90，缺省 30；仅 forecast=true 时有效）"`
}

// metricsQueryOutput mirrors the HTTP GET /api/resources/{id}/metrics
// envelope: resourceId + series (each with points; always arrays). The
// #123 forecast mode keeps the same envelope with an empty series array
// and the forecast object beside it — one tool, two read shapes.
type metricsQueryOutput struct {
	ResourceID string                  `json:"resourceId"`
	Series     []metrics.SeriesResult  `json:"series"`
	Forecast   *metrics.ForecastResult `json:"forecast,omitempty"`
}

// metricsQuery reads through the SAME metrics.Service the HTTP handler
// calls (raw points, unbounded window — the agent decides the horizon).
func metricsQuery(ctx context.Context, svc *services.Services, in metricsQueryInput) (*mcp.CallToolResult, metricsQueryOutput, error) {
	var keys []string
	if in.Keys != "" {
		for _, part := range strings.Split(in.Keys, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, known := metrics.SpecForKey(part); !known {
				return nil, metricsQueryOutput{}, fmt.Errorf("keys 含未知指标键 %q（封闭目录：%s）", part, strings.Join(metrics.KeySet(), ", "))
			}
			keys = append(keys, part)
		}
	}
	// #91: explicit agg validation — an unknown AggKind would hit no switch
	// case and silently answer empty points; the HTTP twin (parseAgg)
	// answers a 400 instead. Same closed set, same explicitness.
	var agg metrics.AggKind
	switch in.Agg {
	case "":
		agg = metrics.AggRaw
	case "raw":
		agg = metrics.AggRaw
	case "daily_avg":
		agg = metrics.AggDailyAvg
	case "daily_min":
		agg = metrics.AggDailyMin
	case "daily_max":
		agg = metrics.AggDailyMax
	default:
		return nil, metricsQueryOutput{}, fmt.Errorf("agg 参数 %q 不是 raw/daily_avg/daily_min/daily_max", in.Agg)
	}
	// #100: the from/to validation the HTTP twin (parseRFC3339Query) already
	// performs — garbage or date-only strings must answer a tool error, not
	// a silently wrong window (a date-only `to` excluded the final day; a
	// garbage `to` matched everything).
	from := ""
	if in.From != "" {
		t, err := time.Parse(time.RFC3339, in.From)
		if err != nil {
			return nil, metricsQueryOutput{}, fmt.Errorf("from 参数 %q 不是 RFC3339 时间戳（示例 2024-05-01T00:00:00Z）", in.From)
		}
		from = t.UTC().Format(time.RFC3339)
	}
	to := ""
	if in.To != "" {
		t, err := time.Parse(time.RFC3339, in.To)
		if err != nil {
			return nil, metricsQueryOutput{}, fmt.Errorf("to 参数 %q 不是 RFC3339 时间戳（示例 2024-05-31T23:59:59Z）", in.To)
		}
		to = t.UTC().Format(time.RFC3339)
	}

	if in.Forecast {
		// The forecast is single-series by definition: exactly one key.
		if len(keys) != 1 {
			return nil, metricsQueryOutput{}, fmt.Errorf("forecast 模式需要且只能指定一个指标键（keys=%d 个）——趋势是单序列的", len(keys))
		}
		fc, err := svc.Metrics.Forecast(ctx, in.ResourceID, keys[0], in.WindowDays)
		if err != nil {
			// The D4 refusal (<14 days / no series) is a HONEST tool error,
			// the same text the HTTP 422 carries.
			return nil, metricsQueryOutput{}, fmt.Errorf("趋势预测失败：%w", err)
		}
		return nil, metricsQueryOutput{ResourceID: in.ResourceID, Series: []metrics.SeriesResult{}, Forecast: &fc}, nil
	}
	series, err := svc.Metrics.ResourceSeries(ctx, in.ResourceID, keys, from, to, agg)
	if err != nil {
		return nil, metricsQueryOutput{}, fmt.Errorf("指标查询失败：%w", err)
	}
	if series == nil {
		series = []metrics.SeriesResult{}
	}
	return nil, metricsQueryOutput{ResourceID: in.ResourceID, Series: series}, nil
}
