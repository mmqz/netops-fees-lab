# #119 风险线索核实闭环：人工核实成果不落库，重复运行线索重复出现（verified/dismissed 状态机）

> **状态**：已修复（2026-10-23，v0.15.2 后 main）。GitHub issue：https://github.com/mmqz/netops-lab/issues/119

## 1. 发现背景（#114 点名的下一轮候选）

八场景输出的是**待核实线索**，但核实成果无处沉淀：

- 人工查完一条 duplicate-settlement 线索（结论：合同台账+账单台账，双份合法），下次运行**同一条线索原样再现**——每轮都是全量噪音；
- 人工确认的真实异常（该核实的漏收）与未看过的线索在输出里**无法区分**——运营没有已知问题台账；
- 对照 alerts 的 firing→acked→cleared 状态机与 W005/W006 人工确认先例，风险线索是唯一没有决策通道的治理面。

## 2. 方案

### 2.1 migration v33 `risk_clue_decisions`

- **线索身份 = 复合键** `(scenario, fee_type, site_code, year, month)` UNIQUE——线索本体每次运行从 Parquet 重算、无行可指，**裁定行即唯一记录**；
- **无 FK（设计裁决）**：站址编码来自费用文件（代码空间事实，同 fee_site_links 的 fee 侧）；scenario/fee_type/status 走 CHECK 闭集；month 0..12（unlinked 线索可携带整年口径 0）；
- **回滚不触碰**（W005/W006 教义：人工决策是审计，applied/dismissed 存活）；
- **UPSERT 语义**：再裁定翻转状态、刷新 decided_at（人随证据改判，台账保留最新裁定；note 留因）；
- 两状态：`verified`（属实——保持可见并标记，构成已知问题台账）/ `dismissed`（排除——默认隐藏、信封计数）。

### 2.2 消费语义（finishClues 联接）

- 默认输出 = pending + verified（verified 携带 `decision`/`decidedAt` 标记）；
- **dismissed 过滤但计数入信封 `dismissed`**——绝不静默丢弃（#83 教义）；重跑不再重复出现（#69 PairDecided 教义：已裁定保持已裁定）；
- `includeDecided` 参数（HTTP/CLI）返回全量标记集（审计视图）；
- 裁定按 (scenario, feeType) 隔离——驳回一条 missing-billing 线索不影响同站的 spike 线索或其铁塔租费口径；
- 决策读 best-effort（与证据锚点同裁决）：决策面故障时显示未裁定集——线索多显示永远比隐藏安全；
- 过滤先于 Limit 截断——已排除线索不占线索名额，计数完整。

### 2.3 通道治理

- **写通道仅人类**：HTTP `POST /api/fees/risk-clue-decisions`（+GET 台账）与 CLI `fees clue-decisions list|verify|dismiss`；**MCP 无写面**——AI 不得替人核实（attribute-changes apply / links confirm 同一治理红线）；`fee_risks` 只读面自动携带标记（MCP wire = HTTP shape）；
- **不依赖引擎**：默认构建即可裁定（纯审计行）；抑制在下一次引擎运行时生效——duckdb 真引擎 e2e 钉死该闭环。

## 3. 封闭集触点

migration v32→33（facts 门禁 + 42 处 maxV 断言随版本前进）/ 路由 82→84、fees 11→13（facts 门禁）/ MCP 描述 / api.md 两新端点 + risks `includeDecided` 参数 + 消费语义段 / fees-layer §7 路由表 + §8 信封字段与闭环段 / /fees 线索行裁定按钮 + 决策 chip + 已排除计数 + 审计开关 + i18n 八常量 / CLI usage 闭集（feesSubcommands 派生）。

## 4. 验证

- **迁移**：前形态守卫（v32 库表不存在）/ CHECK 五态拒收 / UNIQUE 身份 / 幂等重开 / 注册表 v33。
- **repo**：UPSERT 插入-翻转一行保全 / GetByKey 往返 / ListForKeys 批读去重、缺席键不捏造 / List 最新序（**同秒并列回退 id 序——秒粒度内无次序契约，显式时钟钉死有序部分**）/ 过滤器 / 身份缺失拒收。
- **组合**（fake 引擎 + 真 repo）：驳回隐藏+计数 / verified 标记保持可见 / includeDecided 全量标记 / 裁定跨场景跨费用类型隔离 / 翻转复原 / 台账过滤。
- **服务**：closed-set 与账期校验先于写（与 Risks 入口同构）。
- **HTTP**：七种非法输入 400 零写入 / 200 完整项形状 / 翻转一行 / 台账集合语义（同秒并列不断言次序）/ GET 过滤器闭集 400 / risks includeDecided 解析 + 默认构建 501 契约不变。
- **CLI**：usage exit 2 五形态（含闭集）/ verify 完整形状 / 翻转 / 台账过滤 / 默认构建可裁定（无需引擎）。
- **duckdb 真引擎**：UNBILLED-SITE 线索驳回 → 重跑 total=0/dismissed=1 → includeDecided 标记回收——闭环在真引擎上钉死。
- **门禁**：默认 + duckdb 双构建 25 包全绿 · gofmt 空 · vet 0（双构建）· tsc 0 · vitest 188/188 · dist 重建后 webui/app 复验 ok。
