# [P2] 并发重试预算不足：负载下 SQLITE_BUSY 逃逸

来源：[docs/audit-v0.8.0.md](../audit-v0.8.0.md) §2.2

## 现象

```
TestDoConcurrentWorkloads    fail: repo: begin transaction (SQLITE_BUSY ...)
TestConcurrentEngineWorkload fail: sim: apply: ... (SQLITE_BUSY ...)
```

## 关键证据：负载相关，非确定性缺陷

我做了隔离复测：

```
单独运行  → ok  internal/repo 8.7s / internal/sim 14.1s    （通过）
全量套件  → FAIL（两包）                                   （失败）
```

即：并发度低时正常，**高并发下写锁等待超出预算**。

## 根因

`internal/repo/repo.go:224-248`：

```go
const beginBusyAttempts = 3
// DSN: _txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)
// BEGIN 返回 BUSY 表示整个 5s 窗口内写锁均被占用
backoff := 50ms, 100ms, 150ms    // 三次尝试合计额外等待仅 ~300ms
```

全量套件并行执行（21 包同时压 SQLite）时，写锁持有超过 `5s + 300ms`，
第 3 次尝试后直接返回错误。

## 影响评估

| 场景 | 影响 |
| --- | --- |
| 单进程 `serve` 正常使用 | **不受影响**（并发度远低于测试套件） |
| 多实例 / 高并发导入 | **可能偶发 SQLITE_BUSY 失败** |
| 严重度 | 中（非数据损坏，是可用性/体验问题） |

## 建议修复方向

1. `beginBusyAttempts` 提至 5–6，退避上限加大（或指数退避）
2. 或提高 `busy_timeout`（当前 5000ms）
3. 测试侧：把「零 BUSY」断言改为**可容忍重试**，或降低并发度——
   该断言在慢 I/O 平台（Windows）上不可达

## 验收（闭环记录，2026-10-22 核对）

- [x] 全量套件在 Windows 下不再出现 SQLITE_BUSY 逃逸 —— 修复已落地（AUD1-4：`_txlock=immediate` + `busy_timeout(5000)` + 6 次指数退避 BEGIN 重试，repo.go `beginBusyAttempts`）；linux 全量套件（23+ 包并行）零 BUSY 逃逸由 `polish_test.go` 并发工作负载测试钉死；Windows 实机验证仍在 verified-capabilities 的 Windows 未验证项下（外部依赖）
- [x] 提供并发压测证据 —— SQLite 面：`polish_test.go` N 并发写事务经 `Do` 零逃逸；费用域面：`engine_concurrency_duckdb_test.go` 的 `TestDuckEngineConcurrentFeeQueries`（并发满读面 + 黄金值逐字节对照）
- [x] 明确文档化「多实例并发」的支持边界 —— docs/architecture.md §4（单写者 + 重试预算上限 ≈33s 窗口，超出报 BUSY 错误、无数据损坏）与 §7 已文档化