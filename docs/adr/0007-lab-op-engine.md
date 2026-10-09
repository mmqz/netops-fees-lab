# ADR 0007 — 类型化操作引擎（lab op engine）

- 状态：已采纳（2026-10-09，Phase 2 / P2-T2）
- 背景：仿真工作台要求"用户与 AI 都能改仿真态"，且每一次改动**可审计、可撤销、失败零污染**。AI 是不受信任的输入源——不能给它任意 SQL、任意脚本或"自然语言改状态"的通道。
- 关联：[ADR-0001](0001-go-single-backend.md)（无框架、类型优先），[docs/simulation.md](../simulation.md) §2。

## 决策

1. **封闭的前向操作集（10 个，见 §2 表）**。外部边界（HTTP / MCP / CLI）只能提交这 10 个类型化操作码；操作码是 `internal/domain` 常量。理由：
   - **AI 安全边界可枚举**：工具面 = 类型 schema（MCP 由 go-sdk 生成）+ 引擎 `DecodeOps` 双重校验；任何拼写错误、多余字段、未知码在进事务前被拒（400 `invalid_request`）。
   - **可逆性可计算**：封闭集合使"每个操作都有可静态确定的逆操作"成为一条**可测试的不变量**（`inverse_test.go` 逐操作断言），撤销不需要解析 AI 文本。
   - **审计原子单元**：一次提交 = 一个变更集（`changesets` + `changeset_ops`），逐操作记录 op_json / inverse_json / result。
2. **严格字段检查**：`opAllowedFields` 白名单 + `DisallowUnknownFields`——set-but-unused 字段直接拒绝。防止"多余字段悄悄改变语义"。
3. **失败变更集零状态污染**（两段式）：
   - 第一段：单 `db.Do` 事务按序应用；任何失败 ⇒ 整体回滚（状态与变更集行均不落库）；
   - 第二段：独立事务记录**失败变更集**（`ops_applied=0`，失败操作 `failed`、其余 `not_attempted`，`inverse_json` 全 NULL）——失败留档但**不可执行**。
   - op_json 在进事务**前**全部序列化好，保证失败记录携带完整操作负载（而非仅失败前的部分）。
4. **捕获式逆操作（restore_*）而非 blob 回放**：`delete_device/interface/link` 的逆不是"把旧行 JSON 塞回去"，而是引擎在删除时**捕获**被级联实体的类型化快照（设备 + 接口 + 触及链路），逆操作 `restore_device` 按原 id 原样重插。理由：
   - 逆操作仍是**类型化变更集操作**，走同一校验路径、可审计（`op_json` 可读）；
   - 避免"任意 SQL 回放"——restore_* 在每个外部边界被拒（`IsForwardOp` 封闭集合），仅引擎内部（undo / 检查点恢复的 `allowRestore` 路径）可达；
   - 与 LIFO undo 天然配合（见 5）。
5. **LIFO undo**：只允许撤销**最新已应用**的前向变更集（更晚的已应用前向变更集阻塞 ⇒ 409 `not_latest`；failed / reverted / undo 变更集不阻塞）。撤销 = 逆操作按**序号倒序**作为**新变更集**（trigger `undo`）应用，目标随后标记 `reverted`。理由：
   - 倒序保证依赖安全（先恢复后创建的实体）；
   - "撤销也是变更集" ⇒ 撤销自带逆操作，**撤销的撤销**可行；审计链永不出现"删除事件"的空档；
   - undo 变更集不阻塞下一次 undo：`undo(cs-2)` 后，cs-2 之前的最新有效前向变更（cs-1）重新成为 LIFO 头。
   - 备选与否决：任意跳撤销（维护"已撤销集合"做补偿图）——复杂度爆炸且极易产生不一致中间态；快照回滚——越权到第 ③ 层的职责（ADR-0008）。
6. **引擎独占仿真表写路径**：处理器 / MCP 工具 / CLI 一律调 `sim.Service`，没有任何 SQL 直写（§13 质量红线）。

## 后果

- 正面：169 个测试中 34 个 sim 测试覆盖操作原子性、逆操作、LIFO、确定性；`SEC-P2` 断言 MCP 无法提交 restore_*、无法建删工作区；演示脚本一步展示 422 op_failed + 零污染。
- 权衡：新操作类型必须同时给出（字段表、校验、应用器、逆操作、测试）五件套——这是**故意的摩擦**，封闭集合的演化成本应当显式。

- 验证：`TestApplyOpsAtomicity`（ACC-P2-2）、`inverse_test.go`、`TestUndoLIFO`（ACC-P2-8）、`TestMCPLabApplyOps`（restore_* 双重拒绝）、`scripts/demo-lab.sh` 第 6/8 步。
