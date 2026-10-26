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

	// SuccessStatus declares the endpoint's SUCCESS status (#103: the OpenAPI
	// document used to claim 200 for every route while nine routes answer
	// 201 and four answer 202). 0 means 200.
	SuccessStatus int
	// ExtraStatuses declares response statuses beyond the per-route derived
	// defaults (400 everywhere; 404 on {id} patterns; 401 on mutating verbs
	// — the optional --auth-token write gate; 500 everywhere): the honest
	// deviations like 501 engine_not_built (default builds without the
	// DuckDB tag), 422 semantic file errors, 409 conflicts.
	ExtraStatuses map[int]string

	// Admin marks the two-tier privilege class of issue #9 (v0.18):
	// destructive rollbacks/deletes and the human-channel audit
	// confirmations. With both --auth-token and --admin-token
	// configured these routes accept ONLY the admin token (the
	// declarer tier gets 403); the OpenAPI 403 note derives from
	// this same flag — zero hand-copying.
	Admin bool
}

// apiRoutes returns the full route table in registration order. Order
// matters only for readability — the mux matches by pattern, and the
// OpenAPI document sorts paths deterministically.
func (s *Server) apiRoutes() []route {
	return []route{
		{Method: "GET", Pattern: "/api/health", Tag: "system", Summary: "健康检查：状态、版本、规则目录版本", Handler: s.handleHealth},
		{Method: "GET", Pattern: "/api/schema", Tag: "system", Summary: "资源类型目录：字段规范 + 校验规则（动态发现）", Handler: s.handleSchema},
		{Method: "GET", Pattern: "/api/openapi.json", Tag: "system", Summary: "本 API 的 OpenAPI 3 文档（由路由表生成，与实现零漂移）", Handler: s.handleOpenAPISpec},

		{Method: "POST", Pattern: "/api/imports", Tag: "imports", Summary: "上传工作簿分析（?auto=1 走无人值守模板自动导入）", Handler: s.handleImportUpload,
			SuccessStatus: http.StatusAccepted,
			ExtraStatuses: map[int]string{http.StatusUnprocessableEntity: "文件语义错误（not_xlsx / empty_workbook / workbook_too_large）"}},
		{Method: "POST", Pattern: "/api/imports/{id}/mapping", Tag: "imports", Summary: "确认批次映射并执行导入（含自动校验）", Handler: s.handleImportMapping, SuccessStatus: http.StatusAccepted},
		{Method: "POST", Pattern: "/api/imports/{id}/template", Tag: "imports", Summary: "捕获已完成批次的确认映射为模板", Handler: s.handleImportTemplateCapture, SuccessStatus: http.StatusCreated},
		{Method: "GET", Pattern: "/api/mapping-templates", Tag: "imports", Summary: "映射模板列表（无人值守导入的模板目录）", Handler: s.handleMappingTemplatesList},
		{Method: "DELETE", Pattern: "/api/mapping-templates/{id}", Tag: "imports", Summary: "删除映射模板（管理级）", Handler: s.handleMappingTemplateDelete, Admin: true},
		{Method: "GET", Pattern: "/api/batches", Tag: "imports", Summary: "导入批次分页列表", Handler: s.handleBatchesList},
		{Method: "GET", Pattern: "/api/batches/{id}", Tag: "imports", Summary: "批次详情（统计与状态）", Handler: s.handleBatchDetail},
		{Method: "GET", Pattern: "/api/batches/{id}/rows", Tag: "imports", Summary: "批次行级数据分页（含问题行定位）", Handler: s.handleBatchRows},
		{Method: "DELETE", Pattern: "/api/batches/{id}", Tag: "imports", Summary: "删除批次及其导入数据（回滚，管理级）", Handler: s.handleBatchDelete, Admin: true,
			ExtraStatuses: map[int]string{http.StatusConflict: "批次被工作台引用（batch_in_use）"}},

		{Method: "GET", Pattern: "/api/resources", Tag: "resources", Summary: "资源分页列表（类型/状态/关键字过滤）", Handler: s.handleResourcesList},
		{Method: "GET", Pattern: "/api/resources/{id}", Tag: "resources", Summary: "资源详情（属性 + 运行状态）", Handler: s.handleResourceDetail},
		{Method: "GET", Pattern: "/api/resources/{id}/metrics", Tag: "metrics", Summary: "资源指标序列（原始点，keys/from/to 过滤）", Handler: s.handleResourceMetrics},
		{Method: "GET", Pattern: "/api/metrics/keys", Tag: "metrics", Summary: "封闭指标目录（键 → 标签/单位/区间）", Handler: s.handleMetricKeys},
		{Method: "GET", Pattern: "/api/metrics/forecast", Tag: "metrics", Summary: "指标趋势预测（ADR-0011 D4：日均值最小二乘线性趋势 + N 天到阈值 + 80% 预测带；<14 天数据 422 拒算；外推参考，非承诺）", Handler: s.handleMetricForecast,
			ExtraStatuses: map[int]string{http.StatusUnprocessableEntity: "数据不足（日聚合点 < 14，预测拒绝自欺）"}},
		{Method: "GET", Pattern: "/api/metrics/anomalies", Tag: "metrics", Summary: "指标异常点查询（越限/缺失/疑似）", Handler: s.handleMetricAnomalies},
		{Method: "POST", Pattern: "/api/metrics/analyze", Tag: "metrics", Summary: "指标工作簿分析（映射建议，不落库）", Handler: s.handleMetricsAnalyze},
		{Method: "POST", Pattern: "/api/metrics/imports", Tag: "metrics", Summary: "指标批次摄入（独立通道，含批次审计）", Handler: s.handleMetricsImport,
			SuccessStatus: http.StatusAccepted,
			ExtraStatuses: map[int]string{http.StatusUnprocessableEntity: "文件语义错误（not_xlsx）"}},
		{Method: "GET", Pattern: "/api/metrics/batches", Tag: "metrics", Summary: "指标摄入批次列表", Handler: s.handleMetricBatchesList},
		{Method: "POST", Pattern: "/api/metrics/batches/{id}/rollback", Tag: "metrics", Summary: "回滚指标摄入批次（管理级）", Handler: s.handleMetricBatchRollback, Admin: true},

		{Method: "GET", Pattern: "/api/stats", Tag: "governance", Summary: "资源/问题统计计数", Handler: s.handleStats},
		{Method: "GET", Pattern: "/api/issues", Tag: "governance", Summary: "数据质量问题分页列表（类型过滤）", Handler: s.handleIssues},
		{Method: "POST", Pattern: "/api/validate", Tag: "governance", Summary: "触发全库校验规则运行", Handler: s.handleValidate},
		{Method: "GET", Pattern: "/api/report", Tag: "governance", Summary: "质量报告（HTML 或 JSON，按批次）", Handler: s.handleReport},
		{Method: "POST", Pattern: "/api/fees/imports", Tag: "fees", Summary: "费用数据导入（multipart：file + feeType + year + month [+ map]；快照 + Parquet 分区 + 幂等键）", Handler: s.handleFeesImport,
			SuccessStatus: http.StatusCreated,
			ExtraStatuses: map[int]string{http.StatusUnprocessableEntity: "费用文件语义错误（不支持格式/行规范化失败）"}},
		{Method: "POST", Pattern: "/api/fees/imports/sheets", Tag: "fees", Summary: "多 sheet 台账整本导入（#125：每 sheet 按表名/表头自判类型、独立批次独立证据链；无 feeType 字段；CSV/不可判定 sheet/跨模式重导拒绝）", Handler: s.handleFeesImportSheets,
			SuccessStatus: http.StatusCreated,
			ExtraStatuses: map[int]string{http.StatusUnprocessableEntity: "拒收（CSV 无表名/sheet 不可判定/列映射失败/与单 sheet 模式冲突）"}},
		{Method: "POST", Pattern: "/api/fees/period-probe", Tag: "fees", Summary: "账期内容探测（#15：表型分类 + 三档账期识别 strong/weak/none，只读不落库——导入前确认面板的数据面；multipart file，返回 kind/year/month/confidence/basis）", Handler: s.handleFeesPeriodProbe},
		{Method: "GET", Pattern: "/api/fees/batches", Tag: "fees", Summary: "费用导入批次列表（status 过滤，最新在前；含 periodSource/periodBasis 账期来源与依据 #15）", Handler: s.handleFeesBatchesList},
		{Method: "POST", Pattern: "/api/fees/batches/{id}/rollback", Tag: "fees", Summary: "回滚费用批次（删分析行、保快照证据；管理级）", Handler: s.handleFeesRollback, Admin: true},
		{Method: "GET", Pattern: "/api/fees/batches/{id}/evidence", Tag: "fees", Summary: "费用证据回读：快照第 N 行原文（row=源文件自身编号，表头=1；source_row 锚点的第三级证据，回滚批次仍可读）", Handler: s.handleFeesEvidence},
		{Method: "GET", Pattern: "/api/fees/query/totals", Tag: "fees", Summary: "费用分析：站址汇总（feeType/year[/month]，DuckDB 引擎需 duckdb 构建）", Handler: s.handleFeesQueryTotals,
			ExtraStatuses: map[int]string{http.StatusNotImplemented: "engine_not_built — 默认构建未编译 DuckDB 分析引擎（需 -tags duckdb）"}},
		{Method: "GET", Pattern: "/api/fees/query/monthly", Tag: "fees", Summary: "费用分析：月度趋势序列（#3——feeType/year 全年逐月 rows/sites/totalCents；无数据月份不返回，展示层补空白不造零）", Handler: s.handleFeesQueryMonthly,
			ExtraStatuses: map[int]string{http.StatusNotImplemented: "engine_not_built — 默认构建未编译 DuckDB 分析引擎（需 -tags duckdb）"}},
		{Method: "GET", Pattern: "/api/fees/query/duplicates", Tag: "fees", Summary: "费用分析：跨批次重复结算候选（风险线索 + 证据锚点）", Handler: s.handleFeesQueryDuplicates,
			ExtraStatuses: map[int]string{http.StatusNotImplemented: "engine_not_built — 默认构建未编译 DuckDB 分析引擎（需 -tags duckdb）"}},
		{Method: "GET", Pattern: "/api/fees/query/rows", Tag: "fees", Summary: "费用分析：台账行查询（#5——站址/账期过滤，含可选台账列 读数/用量/单价/四方分摊；旧批次缺列为 null）", Handler: s.handleFeesQueryRows,
			ExtraStatuses: map[int]string{http.StatusNotImplemented: "engine_not_built — 默认构建未编译 DuckDB 分析引擎（需 -tags duckdb）"}},
		{Method: "GET", Pattern: "/api/fees/risks/{scenario}", Tag: "fees", Summary: "费用风险十场景（retired-billing/duplicate-settlement/ledger-mismatch/spike/outlier/unlinked/idle-billing/missing-billing/ledger-consistency/custom-rules；线索 + 行级证据锚点，阈值显式参数化）", Handler: s.handleFeesRisks,
			ExtraStatuses: map[int]string{http.StatusNotImplemented: "engine_not_built — 默认构建未编译 DuckDB 分析引擎（需 -tags duckdb）"}},
		{Method: "GET", Pattern: "/api/fees/rules", Tag: "fees", Summary: "可配置风险规则列表（#2：L1 六元组 规则名/度量/聚合/维度/算子/阈值/窗口/严重度；?enabled=1 仅已启用；读开放——规则集本身是证据）", Handler: s.handleFeesRulesList},
		{Method: "POST", Pattern: "/api/fees/rules", Tag: "fees", Summary: "创建可配置风险规则（#2：AI/运营通道，申报级令牌即可；新规则默认停用观察态，人类启用后才参与分析；六元组封闭枚举校验，违例 422 rule_refused）", Handler: s.handleFeesRulesCreate},
		{Method: "POST", Pattern: "/api/fees/rules/{id}/enable", Tag: "fees", Summary: "启用规则（#2：人类通道，管理级令牌；审计留痕——阈值变更留痕红线的启用侧）", Handler: s.handleFeesRuleSetEnabled, Admin: true},
		{Method: "POST", Pattern: "/api/fees/rules/{id}/disable", Tag: "fees", Summary: "停用规则（#2：人类通道，管理级令牌；改阈值 = 停用旧规则 + 建新规则，全程审计可追溯）", Handler: s.handleFeesRuleSetEnabled, Admin: true},
		{Method: "GET", Pattern: "/api/fees/rules/{id}/audit", Tag: "fees", Summary: "规则动作台账（#2：created/enabled/disabled + 操作者令牌层级 + 变更 JSON，最新优先）", Handler: s.handleFeesRuleAudit},
		{Method: "GET", Pattern: "/api/fees/details", Tag: "fees", Summary: "明细层台账（#16 完成线 6：人工补录佐证层——linkKey/period/feeType/status/category 过滤，最新在前；明细永不参与系统表金额计算）", Handler: s.handleFeesDetailsList},
		{Method: "POST", Pattern: "/api/fees/details", Tag: "fees", Summary: "明细补录（#16：三强制=关联键+金额+录入留痕，其余可选不阻断；分类为建议集+本地自建；重复明细指纹拒绝 422；申报级令牌即录）", Handler: s.handleFeesDetailsCreate},
		{Method: "POST", Pattern: "/api/fees/details/{id}/status", Tag: "fees", Summary: "明细核实裁定（确认/驳回〔需原因〕/无系统对应——审核=管理级，核实留痕 谁/何时/原因）", Handler: s.handleFeesDetailsStatus, Admin: true},
		{Method: "GET", Pattern: "/api/fees/details/reconcile", Tag: "fees", Summary: "明细差额提示（#16：Σ明细 vs 系统表该键金额——只提示不阻断；默认构建明细侧可答、系统侧诚实缺席不造零）", Handler: s.handleFeesDetailsReconcile},
		{Method: "POST", Pattern: "/api/fees/risk-clue-decisions", Tag: "fees", Summary: "风险线索人工裁定（#119：verify 属实留档 / dismiss 排除永不再现；可复议翻转；人类通道，MCP 无此写面；审核=管理级）", Handler: s.handleFeesClueDecisionPost, Admin: true},
		{Method: "GET", Pattern: "/api/fees/risk-clue-decisions", Tag: "fees", Summary: "风险线索裁定台账（scenario/feeType/status/siteCode/year 过滤，最新优先）", Handler: s.handleFeesClueDecisionsList},
		{Method: "POST", Pattern: "/api/fees/links/scan", Tag: "fees", Summary: "费用-资源分级关联扫描（精确→规则自动确认，模糊出提案；人类通道确认）", Handler: s.handleFeesLinksScan},
		{Method: "GET", Pattern: "/api/fees/links", Tag: "fees", Summary: "费用-资源关联列表（status/grade/code/resource_id 过滤，含资源侧 join）", Handler: s.handleFeesLinksList},
		{Method: "POST", Pattern: "/api/fees/links/{id}/confirm", Tag: "fees", Summary: "人工确认模糊关联提案（pending→confirmed；人类决策优先于自动扫描；审核=管理级）", Handler: s.handleFeesLinkConfirm, Admin: true},
		{Method: "POST", Pattern: "/api/fees/links/{id}/dismiss", Tag: "fees", Summary: "驳回模糊关联提案（审计保留，编码留给新候选；审核=管理级）", Handler: s.handleFeesLinkDismiss, Admin: true},
	}
}
