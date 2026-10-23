# #120 MCP 费用域只读工具缺口：跨批汇总/重复结算候选对 AI 不可达（fee_totals / fee_duplicates）

> **状态**：已修复（2026-10-23，v0.15.2 后 main）。GitHub issue：https://github.com/mmqz/netops-lab/issues/120

## 1. 发现背景

11 个 MCP 工具中费用域只有 `fee_risks`（线索级，v0.15.0 #58 阶段 5）。引擎两个成熟查询预设对 AI 通道不可达：

- **`GET /api/fees/query/totals`**（站址账期汇总——「这个站今年电费多少」的基础事实问句）；
- **`GET /api/fees/query/duplicates`**（跨批次重复结算候选 + 批次计数 + 证据锚点）。

AI 分析闭环在费用域只有风险线索一面，汇总面缺失——`fee_risks` 工具描述的「先拿线索再 graph_traverse/resource_search 下钻」缺了汇总配对面。两预设无统计阈值（无治理调参面），只读、固定 SQL，扩容是纯通道工作。

## 2. 方案

### 2.1 工具面 11 → 13（全部只读）

| 工具 | 参数 | 治理边界 |
| --- | --- | --- |
| `fee_totals` | feeType + year（必填）、month（可选=整年）、limit | 缺省 20、上限 50（比 HTTP 的 500 更紧——fee_risks 同款裁决：AI 消费面不需要五百行） |
| `fee_duplicates` | 同上 | 同上 |

- **一契约两传输**：MCP wire shape = HTTP shape（v0.15.0 fee_risks 裁决）——`{preset, feeType, year, month, items, total}`，金额整数分（#65 契约），空结果 `items: []` 非 null（#93 纪律）；
- **默认构建诚实拒绝**：`engine_not_built` 契约文案原样透传（#103：真实引擎故障 slog 落 stderr、工具错误答通用文案——feeRisks 同款分派 `feeQueryError`）；
- **输入校验**：closed-set feeType、year 2000-2100、month 1-12、limit ≥ 1 显式拒绝（#91 教义：负值不静默钳制）——SDK schema 层先拒 required 字段（handler 内校验为纵深防御，测试如实断言两层词汇）。

### 2.2 实现下沉

`fees.Service` 新增 `QueryTotals` / `QueryDuplicates` 包装（校验 → 引擎门禁 → `engine.Totals/Duplicates(ctx, s.analysisDir(), …)`）：**analysisDir 路径知识单点化**——HTTP 面自行 join 不动（零行为变化），MCP 面经服务层、永不学习路径。

## 3. 封闭集触点

- `TestMCPToolRegistry` 闭集 11→13（want 清单 + 计数 + 描述非空）；
- `TestMCPToolCountMatchesFactsFile` facts 门禁：`mcp-tools` 11→13 + 工具清单散文；
- **七处工具计数散文漂移清扫**（#66/#110 教义的又一次实证——纯文字追不上代码）：teleagent-integration.md（含 **fee_risks 描述停在「六场景」的两轮滞后**，本轮一并修复为八场景+裁定标记）、architecture.md、README ×3（中文两处 + 英文 eleven→thirteen）、data-model.md ×2、verified-capabilities.md 演进注记；fees-layer.md 消费面（另修 Web 行「六场景钻取」滞后为八场景+裁定 UI）。

## 4. 验证

- **服务包装**（`TestQueryPresetsViaService`）：校验先于引擎门禁（五形态）/ 不可用引擎 `ErrEngineNotBuilt`（`unavailableEngine` 桩）/ 可用引擎行透传（fake 扩展 totals/duplicates 字段）；
- **MCP 拒绝契约**（默认构建）：两工具 `engine_not_built` 文案 + 七种非法输入（bogus type / 缺 year（schema 层词汇）/ 越界 year / month / limit=0）；
- **引擎行为**：duckdb 真引擎两预设既有 e2e（`TestDuck*`）不改不动——本工具是通道不是引擎；
- **门禁**：默认 + duckdb 双构建全绿 · gofmt 空 · vet 0 · vitest/tsc 不受影响（零前端变更）。
