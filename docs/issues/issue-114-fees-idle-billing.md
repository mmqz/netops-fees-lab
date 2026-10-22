# #114 费用×指标交叉验证缺口：六场景缺「有费无量」（idle-billing）

> **状态**：已修复（2026-10-22，v0.15.2 后 main）。GitHub issue：https://github.com/mmqz/netops-lab/issues/114

## 1. 发现背景

v0.15.2 收口后的全仓调研（费用层现状梳理）确认：六场景的交叉验证只覆盖**资源 `status` 字段**（retired-billing）与**关联面**（unlinked）——`internal/fees/risks.go` 的跨层组合未引用 metrics 层任何查询；docs 全局 grep「停电/共享/分摊/outage」零命中。原始需求「**停电告警在账资源 / 无告警却有费用**」中，费用台账 × 业务量台账的交叉**整块缺失**：一个站点可以同时满足"费用面该账期计费在流"与"指标面关联资源该账期零采集点"，而没有任何场景能发现这种矛盾。

## 2. 根因

- #64 落地六场景时，跨层组合只做了 SQLite **状态/关联**两面的交叉（当时指标层刚随 W20 落地，数据厚度不足以支撑验收断言）；
- 后续轮次（#83/#93/#105/#110）都在既有六场景的口径/证据/诚实化上打磨，没有回头补交叉面。

## 3. 修复方案：第七场景 `idle-billing`（有费无量）

### 3.1 判定口径

**已确认关联的站址在该账期窗口内指标点数为零，且该账期仍有费用记录。**

- 跨层组合照抄 retired-billing 先例（#58 架构裁决：Go 内组合，禁跨引擎 ATTACH）：
  1. 引擎 `SitePeriodTotals`（费用面，Parquet）取有费用的 (站址, 账期) 行；
  2. 逐站 memoize 确认关联 → 资源 id（#85 纪律）；
  3. 按 (资源, 账期) memoize **指标点计数**——新读原语 `MetricRepo.CountPointsOfResource(resourceID, from, to)`（一次索引 join 查询）；
  4. **窗口内计数 = 0 触发**。
- 窗口 = 账期自然月 `[1日00:00:00Z, 末日23:59:59Z]` 的 RFC3339 字典序比较，与 metrics 存储约定（`ParseTS` 归一到 UTC 午夜、秒精度）一致；month=0（整年口径）= `[1月1日, 12月31日]`，逐 (站址, 账期) 行各自开窗。
- **无阈值**：零点是确定性信号——不引入"近乎无业务量"的魔法阈值（阈值化会把认定结论偷运回线索，违反输出契约）。

### 3.2 诚实注意义务（写进契约的一部分）

线索 note 恒携带告诫：**无采集也可能是采集覆盖缺失或采集断链，与真正空置在该证据形态下不可区分**——线索仅供人工核实，不作空置/闲置认定。这是"账单存在 ≠ 认定结论"在指标面的镜像：**无账单记录 ≠ 未工作**。

边界裁决：
- 无 confirmed 关联的编码**不在本场景测量**（无法确定测量哪个资源——那是 unlinked 的业务）；
- 点查缺失的业务态（无关联/资源消失）静默，真实 DB 故障中止报告（#85：绝不静默掉行）。

### 3.3 消费面闭集同步（六→七）

| 面 | 落点 |
| --- | --- |
| MCP `fee_risks` | 描述 + `scenario` schema 枚举（`internal/mcpserver/feetools.go`） |
| HTTP | 路由摘要（`internal/api/routes.go`）；`ParseRiskScenario` 封闭集自动派生 |
| CLI | usage 文本经 `fees.RiskScenarios()` 自动派生（`internal/app/fees_cli.go`） |
| Web /fees | 场景下拉 + i18n 常量 `RISK_SCENARIO_IDLE_BILLING`（死常量探测器防漏接线） |
| 文档 | fees-layer.md §7/§8（场景表 + 诚实注意义务段）、api.md、facts.md、README |

## 4. 测试

- **组合逻辑单测**（默认构建，fake 引擎 + 真实 SQLite repo）：窗口内有点静默 / 窗口外有点不救 / 无关联不测量 / 整年口径同站逐月开窗（5 月有点 0 线索、6 月无点 1 线索）/ #93 空锚点数组非 null / 信封 note 与 scenarioCn 钉死。
- **窗口数学单测**：平年/闰年 2 月/12 月/整年口径的 from-to 边界。
- **封闭集**：`TestParseRiskScenario` 计数 6→7；mcpserver 与 e2e 的场景循环补 idle-billing（默认构建 501 契约随枚举自动覆盖）。
- **duckdb 真实引擎 e2e**：fixture 复用 retired/unlinked 的资源对——RETIRED-SITE 资源零点数（触发，777 元/2024-06）vs ACTIVE-SITE 种 6 月点（静默）；单月 5 月口径诚实空（费用面无关联站 5 月行）。
- **门禁**：gofmt 空 · vet 0（默认 + duckdb 双构建）· 全包 go test 双构建全绿 · vitest 188/188 · tsc 0（本地执行；CI 配额外部阻塞中不依赖）。

## 5. 遗留与后续候选

- **outlier 口径偏差**（调研顺带发现）：文档口径"同类站**单位成本**离群"，实现对月度**总额**直接 z-score，无站点规模归一化（载频/端口/租户数）——备选小改进，另行立项；
- **风险线索核实闭环**：RiskClue 无 verified/dismissed 状态沉淀，重复运行重复出现，人工核实成果不落库（对照 alerts 的 firing→acked→cleared 状态机）——下一轮候选；
- **共享站费用分摊**：关联是一码一资源，无多归属/分摊比例概念——架构级，需先过数据模型设计。
