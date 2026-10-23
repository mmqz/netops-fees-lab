# #121 Web /fees 消费面缺半壁：查询预设（汇总/重复候选）在浏览器通道不可达

> **状态**：已修复（2026-10-23，v0.15.2 后 main）。GitHub issue：https://github.com/mmqz/netops-lab/issues/121

## 1. 发现背景

fees-layer.md §消费面声明三通道同契约，但 web/src 对 `GET /api/fees/query/totals` 与 `GET /api/fees/query/duplicates` **零调用**——运营者的主入口（浏览器）只见风险钻取与关联确认，两个基础查询预设只能 CLI/curl：

- 站址账期汇总（「这个站今年电费多少」——运营对账第一步）；
- 跨批次重复结算候选（对账前的人工核对清单）。

#120 刚把这两个预设开给 AI 通道（MCP），浏览器通道反而缺位——「三通道同契约」对 web 是空头支票。

## 2. 方案

/fees 页新增「查询预设」面板（风险钻取与关联确认之间）：

- **预设切换**：站址账期汇总 / 跨批次重复候选（select 切换即清空上一结果——不串台）；
- **口径选择**：费用类型（四闭集下拉）+ 年份 + 月份（可选=整年）；limit 走 **HTTP 诚实上限 500**（浏览器展示存在的全部，与 MCP 的 50 紧界分工——运营查全量、AI 拿要点）；
- **汇总表**：站址 / 行数 / 合计金额（整数分自动 /100，同风险线索的展示约定）；
- **候选表**：站址 / 账期 / 批次数 / 行数 / 合计——面板描述携带「候选供人工对账核对，账单存在 ≠ 重复付款认定」教义；
- **诚实契约**：501 `engine_not_built` 渲染为既有 Docker/WSL2 引导横幅（不裸错误）；空结果诚实空态；年份越界复用既有校验文案；
- i18n 常量 12 个全接线（死常量探测器纪律）；页面头注释的「七场景」滞后一并修复（八场景 + 裁定 UI + 查询预设）。

## 3. 实现

- `web/src/api/types.ts`：`FeeTotalsRow/Result`、`FeeDuplicatesRow/Result`（HTTP 形状镜像）；
- `web/src/api/client.ts`：`fetchFeeTotals` / `fetchFeeDuplicates`；
- `web/src/pages/Fees.tsx`：独立面板状态组（与风险钻取解耦——互不污染口径选择）+ 两表渲染。

## 4. 验证

- tsc strict 0 错误；vitest 188/188；dist 重建后 webui 嵌入测试 + app e2e 复验 ok；
- 后端零变更（通道补全，无新路由——两个端点自 v0.13.0 就在）；浏览器级面板交互回归并入 r20 计划（与身份治理确认流同批）；
- 门禁：默认 + duckdb 双构建全绿。
