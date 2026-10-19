package api

// routes.go — the single route table of the HTTP API (v0.10-R9).
//
// Every /api route is registered from THIS table (Server.Routes loops
// over it) and the OpenAPI 3 document (GET /api/openapi.json) is
// generated from the SAME table — one source of truth, so the spec
// cannot drift from the actual surface: a new route without a summary
// is a compile-time missing field, a route removed from the table
// disappears from both the mux and the spec, and the anti-drift test
// walks every table entry against the live server asserting the
// registered 404s (route exists) are never the unknown-path fallback.
//
// The tags organize the surface for the OpenAPI consumer (the AI
// integration story of the contest: an agent reads /api/openapi.json
// and discovers the whole API without reading the repo).

import "net/http"

// route is one registered API endpoint: the HTTP registration triple
// plus the documentation the OpenAPI document projects.
type route struct {
	Method  string
	Pattern string
	Tag     string
	Summary string
	Handler http.HandlerFunc
}

// apiRoutes returns the full route table in registration order. Order
// matters only for readability — the mux matches by pattern, and the
// OpenAPI document sorts paths deterministically.
func (s *Server) apiRoutes() []route {
	return []route{
		{Method: "GET", Pattern: "/api/health", Tag: "system", Summary: "健康检查：状态、版本、规则目录版本", Handler: s.handleHealth},
		{Method: "GET", Pattern: "/api/schema", Tag: "system", Summary: "资源类型目录：字段规范 + 校验规则（动态发现）", Handler: s.handleSchema},
		{Method: "GET", Pattern: "/api/openapi.json", Tag: "system", Summary: "本 API 的 OpenAPI 3 文档（由路由表生成，与实现零漂移）", Handler: s.handleOpenAPISpec},

		{Method: "POST", Pattern: "/api/imports", Tag: "imports", Summary: "上传工作簿分析（?auto=1 走无人值守模板自动导入）", Handler: s.handleImportUpload},
		{Method: "POST", Pattern: "/api/imports/{id}/mapping", Tag: "imports", Summary: "确认批次映射并执行导入（含自动校验）", Handler: s.handleImportMapping},
		{Method: "POST", Pattern: "/api/imports/{id}/template", Tag: "imports", Summary: "捕获已完成批次的确认映射为模板", Handler: s.handleImportTemplateCapture},
		{Method: "GET", Pattern: "/api/mapping-templates", Tag: "imports", Summary: "映射模板列表（无人值守导入的模板目录）", Handler: s.handleMappingTemplatesList},
		{Method: "DELETE", Pattern: "/api/mapping-templates/{id}", Tag: "imports", Summary: "删除映射模板", Handler: s.handleMappingTemplateDelete},
		{Method: "GET", Pattern: "/api/batches", Tag: "imports", Summary: "导入批次分页列表", Handler: s.handleBatchesList},
		{Method: "GET", Pattern: "/api/batches/{id}", Tag: "imports", Summary: "批次详情（统计与状态）", Handler: s.handleBatchDetail},
		{Method: "GET", Pattern: "/api/batches/{id}/rows", Tag: "imports", Summary: "批次行级数据分页（含问题行定位）", Handler: s.handleBatchRows},
		{Method: "DELETE", Pattern: "/api/batches/{id}", Tag: "imports", Summary: "删除批次及其导入数据（回滚）", Handler: s.handleBatchDelete},

		{Method: "GET", Pattern: "/api/resources", Tag: "resources", Summary: "资源分页列表（类型/状态/关键字过滤）", Handler: s.handleResourcesList},
		{Method: "GET", Pattern: "/api/resources/{id}", Tag: "resources", Summary: "资源详情（属性 + 运行状态）", Handler: s.handleResourceDetail},
		{Method: "GET", Pattern: "/api/resources/{id}/relations", Tag: "graph", Summary: "资源关系图（邻接视图，旧端点）", Handler: s.handleResourceRelations},
		{Method: "GET", Pattern: "/api/resources/{id}/traverse", Tag: "graph", Summary: "图查询：分层垂直穿透（BFS + 预算截断）", Handler: s.handleResourceTraverse},
		{Method: "GET", Pattern: "/api/resources/{id}/impact", Tag: "graph", Summary: "图查询：影响面 + 根因候选（含中文理由）", Handler: s.handleResourceImpact},
		{Method: "GET", Pattern: "/api/resources/{id}/context", Tag: "graph", Summary: "图查询：一次性 AI 分析上下文组装", Handler: s.handleResourceContext},

		{Method: "POST", Pattern: "/api/resources/{id}/run-state", Tag: "resources", Summary: "人工设置运行状态观测（up/down/degraded/maintenance）", Handler: s.handleRunStateSet},
		{Method: "GET", Pattern: "/api/resources/{id}/run-state/history", Tag: "resources", Summary: "运行状态历史时序（含来源：人工/告警引擎）", Handler: s.handleRunStateHistory},

		{Method: "GET", Pattern: "/api/resources/{id}/metrics", Tag: "metrics", Summary: "资源指标序列（原始点，keys/from/to 过滤）", Handler: s.handleResourceMetrics},
		{Method: "GET", Pattern: "/api/metrics/keys", Tag: "metrics", Summary: "封闭指标目录（键 → 标签/单位/区间）", Handler: s.handleMetricKeys},
		{Method: "GET", Pattern: "/api/metrics/anomalies", Tag: "metrics", Summary: "指标异常点查询（越限/缺失/疑似）", Handler: s.handleMetricAnomalies},
		{Method: "POST", Pattern: "/api/metrics/analyze", Tag: "metrics", Summary: "指标工作簿分析（映射建议，不落库）", Handler: s.handleMetricsAnalyze},
		{Method: "POST", Pattern: "/api/metrics/imports", Tag: "metrics", Summary: "指标批次摄入（独立通道，含批次审计）", Handler: s.handleMetricsImport},
		{Method: "GET", Pattern: "/api/metrics/batches", Tag: "metrics", Summary: "指标摄入批次列表", Handler: s.handleMetricBatchesList},
		{Method: "POST", Pattern: "/api/metrics/batches/{id}/rollback", Tag: "metrics", Summary: "回滚指标摄入批次", Handler: s.handleMetricBatchRollback},

		{Method: "GET", Pattern: "/api/detection/rules", Tag: "detection", Summary: "检测规则列表（含禁用，懒播种默认规则集）", Handler: s.handleDetectionRulesList},
		{Method: "POST", Pattern: "/api/detection/rules", Tag: "detection", Summary: "新建检测规则（封闭目录校验，ruleId 可自动生成）", Handler: s.handleDetectionRulesCreate},
		{Method: "PATCH", Pattern: "/api/detection/rules/{id}", Tag: "detection", Summary: "部分更新规则（metricKey 身份不可改）", Handler: s.handleDetectionRulesPatch},
		{Method: "DELETE", Pattern: "/api/detection/rules/{id}", Tag: "detection", Summary: "删除规则（被告警引用时 409 审计保护）", Handler: s.handleDetectionRulesDelete},
		{Method: "POST", Pattern: "/api/detection/run", Tag: "detection", Summary: "执行一次全库检测扫描（可选 from/to 数据窗口；回执落库，完成后触发 webhook 通知；维护窗口按数据时间+运行时刻双时基判定抑制）", Handler: s.handleDetectionRun},
		{Method: "GET", Pattern: "/api/detection/runs", Tag: "detection", Summary: "检测运行历史分页（每双回执：来源 api/cli/scheduler、窗口、计数、时长、失败原因）", Handler: s.handleDetectionRunsList},

		{Method: "GET", Pattern: "/api/maintenance/windows", Tag: "maintenance", Summary: "维护窗口列表（计划内抑制新告警的时间区间，最新在前）", Handler: s.handleMaintenanceWindowsList},
		{Method: "POST", Pattern: "/api/maintenance/windows", Tag: "maintenance", Summary: "新建维护窗口（RFC3339 起止、可选资源范围，空范围 = 全局；窗口按双时基判定：数据时间含证据时刻 或 运行时刻含扫描时刻，其一命中即抑制新告警）", Handler: s.handleMaintenanceWindowsCreate},
		{Method: "DELETE", Pattern: "/api/maintenance/windows/{id}", Tag: "maintenance", Summary: "删除维护窗口（历史运行回执不受影响）", Handler: s.handleMaintenanceWindowsDelete},

		{Method: "GET", Pattern: "/api/alerts", Tag: "alerts", Summary: "告警分页列表（state/severity/resource/role 过滤；role=primary/secondary 根因/次生分诊）", Handler: s.handleAlertsList},
		{Method: "GET", Pattern: "/api/alerts/stats", Tag: "alerts", Summary: "告警分布统计（byState 三态计数 + 待处置级别矩阵，闭集零填充）", Handler: s.handleAlertsStats},
		{Method: "POST", Pattern: "/api/alerts/{id}/ack", Tag: "alerts", Summary: "确认告警（firing→acked，ackedBy 必填审计字段）", Handler: s.handleAlertAck},
		{Method: "POST", Pattern: "/api/alerts/{id}/clear", Tag: "alerts", Summary: "清除告警（firing|acked→cleared，记录保留审计）", Handler: s.handleAlertClear},

		{Method: "GET", Pattern: "/api/exports/resources", Tag: "resources", Summary: "导出当前资源清单为导入同构 xlsx", Handler: s.handleExportResources},

		{Method: "GET", Pattern: "/api/governance/summary", Tag: "governance", Summary: "治理总览（待确认队列/决策审计/问题分布/清单形态）", Handler: s.handleGovernanceSummary},
		{Method: "GET", Pattern: "/api/stats", Tag: "governance", Summary: "资源/问题统计计数", Handler: s.handleStats},
		{Method: "GET", Pattern: "/api/issues", Tag: "governance", Summary: "数据质量问题分页列表（类型过滤）", Handler: s.handleIssues},
		{Method: "POST", Pattern: "/api/validate", Tag: "governance", Summary: "触发全库校验规则运行", Handler: s.handleValidate},
		{Method: "GET", Pattern: "/api/report", Tag: "governance", Summary: "质量报告（HTML 或 JSON，按批次）", Handler: s.handleReport},
		{Method: "GET", Pattern: "/api/topology/check", Tag: "governance", Summary: "拓扑连通性检查", Handler: s.handleTopologyCheck},

		{Method: "GET", Pattern: "/api/attribute-changes", Tag: "governance", Summary: "属性变更确认队列（更新差异导入模式）", Handler: s.handleAttributeChangesList},
		{Method: "POST", Pattern: "/api/attribute-changes/{id}/apply", Tag: "governance", Summary: "人工确认应用一条属性变更", Handler: s.handleAttributeChangeApply},
		{Method: "POST", Pattern: "/api/attribute-changes/{id}/dismiss", Tag: "governance", Summary: "人工驳回一条属性变更", Handler: s.handleAttributeChangeDismiss},

		{Method: "GET", Pattern: "/api/renames", Tag: "governance", Summary: "疑似重名合并提案列表（身份治理）", Handler: s.handleRenamesList},
		{Method: "POST", Pattern: "/api/renames/{id}/apply", Tag: "governance", Summary: "人工确认合并资源身份", Handler: s.handleRenameApply},
		{Method: "POST", Pattern: "/api/renames/{id}/reject", Tag: "governance", Summary: "人工驳回身份合并提案", Handler: s.handleRenameReject},
		{Method: "POST", Pattern: "/api/fees/imports", Tag: "fees", Summary: "费用数据导入（multipart：file + feeType + year + month [+ map]；快照 + Parquet 分区 + 幂等键）", Handler: s.handleFeesImport},
		{Method: "GET", Pattern: "/api/fees/batches", Tag: "fees", Summary: "费用导入批次列表（status 过滤，最新在前）", Handler: s.handleFeesBatchesList},
		{Method: "POST", Pattern: "/api/fees/batches/{id}/rollback", Tag: "fees", Summary: "回滚费用批次（删分析行、保快照证据）", Handler: s.handleFeesRollback},
		{Method: "GET", Pattern: "/api/fees/query/totals", Tag: "fees", Summary: "费用分析：站址汇总（feeType/year[/month]，DuckDB 引擎需 duckdb 构建）", Handler: s.handleFeesQueryTotals},
		{Method: "GET", Pattern: "/api/fees/query/duplicates", Tag: "fees", Summary: "费用分析：跨批次重复结算候选（风险线索 + 证据锚点）", Handler: s.handleFeesQueryDuplicates},
		{Method: "GET", Pattern: "/api/fees/risks/{scenario}", Tag: "fees", Summary: "费用风险六场景（retired-billing/duplicate-settlement/ledger-mismatch/spike/outlier/unlinked；线索 + 行级证据锚点，阈值显式参数化）", Handler: s.handleFeesRisks},
		{Method: "POST", Pattern: "/api/fees/links/scan", Tag: "fees", Summary: "费用-资源分级关联扫描（精确→规则自动确认，模糊出提案；人类通道确认）", Handler: s.handleFeesLinksScan},
		{Method: "GET", Pattern: "/api/fees/links", Tag: "fees", Summary: "费用-资源关联列表（status/grade/code/resource_id 过滤，含资源侧 join）", Handler: s.handleFeesLinksList},
		{Method: "POST", Pattern: "/api/fees/links/{id}/confirm", Tag: "fees", Summary: "人工确认模糊关联提案（pending→confirmed；人类决策优先于自动扫描）", Handler: s.handleFeesLinkConfirm},
		{Method: "POST", Pattern: "/api/fees/links/{id}/dismiss", Tag: "fees", Summary: "驳回模糊关联提案（审计保留，编码留给新候选）", Handler: s.handleFeesLinkDismiss},

		{Method: "GET", Pattern: "/api/workspaces", Tag: "lab", Summary: "仿真工作台分页列表", Handler: s.handleWorkspacesList},
		{Method: "POST", Pattern: "/api/workspaces", Tag: "lab", Summary: "物化新工作台（从已完成批次快照）", Handler: s.handleWorkspacesCreate},
		{Method: "GET", Pattern: "/api/workspaces/{id}", Tag: "lab", Summary: "工作台详情（拓扑摘要与统计）", Handler: s.handleWorkspaceDetail},
		{Method: "DELETE", Pattern: "/api/workspaces/{id}", Tag: "lab", Summary: "删除工作台（含其仿真数据）", Handler: s.handleWorkspaceDelete},
		{Method: "GET", Pattern: "/api/workspaces/{id}/topology", Tag: "lab", Summary: "工作台拓扑图数据（分层布局）", Handler: s.handleWorkspaceTopology},
		{Method: "GET", Pattern: "/api/workspaces/{id}/layer-members", Tag: "lab", Summary: "图层聚簇成员分页（issue #51：基站簇下钻，cluster=锚设备 id）", Handler: s.handleWorkspaceLayerMembers},
		{Method: "POST", Pattern: "/api/workspaces/{id}/topology/path", Tag: "lab", Summary: "拓扑两设备间路径查询", Handler: s.handleWorkspacePath},
		{Method: "POST", Pattern: "/api/workspaces/{id}/ops", Tag: "lab", Summary: "仿真操作（故障注入/恢复，写变更集）", Handler: s.handleWorkspaceOps},
		{Method: "POST", Pattern: "/api/workspaces/{id}/checks/run", Tag: "lab", Summary: "运行一次连通性检查", Handler: s.handleWorkspaceChecksRun},
		{Method: "GET", Pattern: "/api/workspaces/{id}/checks", Tag: "lab", Summary: "检查运行历史分页", Handler: s.handleWorkspaceChecksList},
		{Method: "GET", Pattern: "/api/workspaces/{id}/checks/{runId}", Tag: "lab", Summary: "单次检查详情（分链路结果）", Handler: s.handleWorkspaceCheckRun},
		{Method: "GET", Pattern: "/api/workspaces/{id}/checks/{runId}/diff", Tag: "lab", Summary: "两次检查的差异对比", Handler: s.handleWorkspaceRunDiff},
		{Method: "GET", Pattern: "/api/workspaces/{id}/changesets", Tag: "lab", Summary: "仿真变更集分页（undo 粒度）", Handler: s.handleWorkspaceChangeSets},
		{Method: "POST", Pattern: "/api/workspaces/{id}/undo", Tag: "lab", Summary: "撤销变更集（可选指定，默认最近）", Handler: s.handleWorkspaceUndo},
		{Method: "POST", Pattern: "/api/workspaces/{id}/checkpoints", Tag: "lab", Summary: "创建检查点（带标签）", Handler: s.handleCheckpointCreate},
		{Method: "GET", Pattern: "/api/workspaces/{id}/checkpoints", Tag: "lab", Summary: "检查点列表", Handler: s.handleCheckpointsList},
		{Method: "POST", Pattern: "/api/workspaces/{id}/checkpoints/{cpId}/restore", Tag: "lab", Summary: "恢复到检查点（生成恢复变更集）", Handler: s.handleCheckpointRestore},
		{Method: "GET", Pattern: "/api/lab/report", Tag: "lab", Summary: "仿真工作台实验报告", Handler: s.handleLabReport},
	}
}
