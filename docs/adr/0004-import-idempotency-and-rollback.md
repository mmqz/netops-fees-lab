# ADR 0004 — 导入幂等与批次回滚

- 状态：已采纳（2026-10-09，Phase 1）
- 背景：Excel 台账会被反复导入（更新周期、人工重传）。要求：同一工作簿重导**零副作用**；错误导入可**整批撤销**；每一行都可追溯。

## 决策

1. **skip-duplicate 策略**：执行时逐行查 `(type, natural_key)`；库中已有（更早批次创建）→ 行状态 `skipped`，消息"重复导入：资源已存在于批次 \<id\>"，**不产生新资源/关系/问题**，`first_batch_id` 不改写。幂等性 = 重导后资源数不变（ACC-2 断言 0 新增）。
2. **批内 E001 语义**：同批次内自然键重复时，**后到者** failed 且记 E001（证据含 conflicting_sheet/row_number 与 existing_entity_id）。实现细节：行级"已见"表登记的是**到达 E001 检查点的行**（imported 与 skipped 均登记）——这使"重导批次"里 27 skipped + 3 failed 且 E001×1 恰好复现（首行导入、副本行 failed）。字段级失败（E002/E004）的行**不**进入已见表（它们从未拥有合法键）。
3. **409 in-use 回滚保护**：`DELETE /api/batches/{id}` 仅对 `completed` 批次开放；若本批次创建的资源被**其他批次**的关系引用（`ExistsFromOtherBatch`）→ 409 `batch_in_use`。这防止"回滚 devices 批次留下地址池/电路悬空引用"。注意区分：非 completed 状态的拒绝是普通 `conflict`（同一个 409 状态码，不同 error code）。
4. **行级记账（row accounting）**：每数据行恰好一条 `import_row_results`（`UNIQUE (batch_id, sheet, row_number)`）：imported（带 entity_id）/ skipped（带消息）/ failed（带 rule_code + message）。批次计四元组 imported/skipped/failed/total 恒等式：三者之和 = total = 数据行数（ACC-1）。
5. **单事务原子性**：全部 import sheet 的全部写入一个 `db.Do`；任何内部错误（含注入的钩子错误）整体回滚、批次 `failed`、零半写（S0-6 测试）。**行级 E 失败不是错误**——行记 failed，批次继续（"每一行必有结果"优先于"快速失败"）。
6. 回滚执行序：relations → resources（FK 级联）→ source_refs → row_results → issues → 状态 `rolled_back`，单事务；响应携带各类删除计数。

## 后果

- 正面：重导/回滚/重放均为确定操作；账目可对账（UI 行表 + API rows + issues 三方一致）；来源证据（source_refs）与行号（1-based Excel 行号）贯穿；
- 权衡：W 级问题只记在**首次**导入的批次上（重导的 skipped 行不重记）——问题归属保持稳定，避免重导刷问题数；"更新已有资源"不在 Phase 1 范围（导入只有 insert-or-skip，无 update 语义）。
- 已验证：`TestReimportIdempotent`（ACC-2）、`TestImportAtomicRollback`（S0-6）、回滚 409（`TestAPIImportFlow` 的 DELETE 双例）。
