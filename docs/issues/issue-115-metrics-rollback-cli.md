# #115 指标批次回滚 CLI 收口——api.md/import-and-validation.md 双处承诺的「随 W24 收口」未兑现

> **状态**：已修复（2026-10-22，v0.15.2 后 main）。GitHub issue：https://github.com/mmqz/netops-lab/issues/115

## 1. 发现背景

v0.15.2 后的全仓遗留调研（只读子代理扫描）确认：生产代码 TODO/FIXME = 0，全部遗留以文档声明管理；扫描出的声明中**唯一的功能欠账**就是这一条——

- `docs/api.md:941`（W20 落地时写下的）：「回滚目前为 HTTP 端点（CLI 侧后续随 W24 无人值守轮次一并收口）」
- `docs/import-and-validation.md:354`（同轮交叉声明）：「指标批次回滚的 CLI 收口不在本轮范围」

W24（无人值守导入，v0.7 W24）已落地多版本，承诺未兑现。三大导入通道中：资源批次有 `rollback`、费用批次有 `fees rollback`，**指标批次是唯一 CLI 无回滚的通道**——离线场景（无浏览器/无 serve）操作者导错指标文件后无法撤销。

## 2. 修复方案

新增 `metrics` 顶层 CLI 命令（`list` / `rollback` 两子命令），全部形状对齐既有命令族教义：

| 面 | 落点 |
| --- | --- |
| 命令形状 | 前置 `--db`（`splitSubArgs` 既有原语）；子命令封闭集 `metricsSubcommands` 切片派生两个 usage 面（#108 教义） |
| 闭集钉死 | `usage_closset_test.go` 的 dispatch 表登记 `metrics`——go/ast 双向钉死（switch ↔ 切片）；顶层 `cliSubcommands` 19→20 |
| `list` | `--page`（≥1）/ `--page-size`（1–100），输出 `{items, total, page, pageSize}`，items 字段与 HTTP `GET /api/metrics/batches` 完全同形（id/workbookName/sha256/status/行计数/时间戳） |
| `rollback` | `--batch <id>`（旗标名对齐费用通道孪生 `fees rollback --batch`）；输出 `metrics.RollbackResult` 原文（batchId/status/deletedPoints/prunedSeries） |
| 错误码表 | 与 HTTP 逐字对齐：非 completed → exit 1 `conflict`（domain.ErrConflict 既有映射）、未知 id → exit 1 `not_found`、usage → exit 2——零新增映射逻辑 |

## 3. 测试

- **e2e 全旅程**（真实工件）：synthdata（2 设备 × 1 端口 × 1 天 × 3 指标列）→ `analyze`+`import` 资源 → `ingest --suggest` → `ingest`（completed，pointsWritten > 0）→ `metrics list`（批次可见、字段同形、分页越界 exit 2）→ `metrics rollback`（deletedPoints = 导入点数、prunedSeries > 0、status=rolled_back）→ list 复查（rolled_back 持久）→ **重复回滚 exit 1 conflict** → **未知 id exit 1 not_found** → usage 五形态 exit 2 + 缺子命令 usage 双子命令命名断言。
- 踩坑记录：`runCLIJSON` 的首参是**标签**不是命令——命令名必须出现在 args 里（与 `runCLI` 不同签名），e2e 首跑因此误报。

## 4. 文档收口

- `docs/api.md`：rollback 小节承诺句删除，改写为 CLI 通道实况；CLI 通道小节补 `metrics list|rollback`，并记录「三大导入通道 CLI 回滚齐全」的里程碑事实；
- `docs/import-and-validation.md`：遗留声明改为已收口实况；
- `README.md`：顶层子命令清单 19→20 + quickstart 命令区补一行；
- `docs/facts.md`：无需改动——CLI 子命令清单**刻意不入 facts**（#108 教义：dispatch 切片派生 + ast 闭集测试结构性防漂移，写死数字反而添乱）。

## 5. 门禁（本地执行）

25 包 ok × 双构建（默认 + `-tags duckdb`）/ gofmt 空 / vet 0 × 双构建 / vitest 188/188 / tsc 0。
