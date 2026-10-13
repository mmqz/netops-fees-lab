# ADR-0014: 关系语义不合并（belongs_to/housed_in）与时序幂等语义并存（skip/upsert）

- 状态：已接受（设计裁决，随 v0.7.0 W19 落地）
- 日期：2026-10-13（W19）
- 关联 issue：#17（A1）、#18（A2）、#16（数据模型缺层）
- 关联决策：ADR-0004（导入幂等与回滚）、ADR-0011（D1/D3 时序存储）、`docs/design/data-model-v1.md` §12-A1/§12-A2

## 背景

数据模型 v1.0 设计（data-model-v1.md）在 §4.2 与 §5.4 D3 留下两个"实施时必须显式
裁决并记录 ADR"的决策点：空间关系是否合并、资源与时序的重导语义差异是否可接受。
issue #17/#18 各自给出候选方案与评判标准。本 ADR 按「可测的实测、不能决定的用
默认方案并显式标记」原则完成裁决。

## 决策 1：`belongs_to` 与 `housed_in` 不合并（issue #17，方案 A）

**裁决**：保留两个关系——`belongs_to`（machine_room → site，容器归属，
v10 已落地语义，同层边）+ `housed_in`（device → machine_room，物理位置，
跨层边，随 M3/W21 落地）。

**依据**：

1. **迁移成本（实测）**：合并为 `located_in` 需重写 v10 存量 `belongs_to` 行
   （SQLite CHECK 约束下 = 又一次表重建），并修改 6 个非测试包
   （domain/mapping/importer/query/api/changes）+ 8 个测试文件约 60 处断言
   （`rg -c belongs_to internal/ -g '*_test.go'` 实数）。违背 §10.10
   「不做破坏性变更」原则。
2. **查询收益为零**：§10.3 traverse 端点按「层序 BFS + 层次注册表」工作，
   同层/跨层的区分由 `TypeSpec.Layer` + `RelationSpec.CrossLayer`（由层次计算，
   非手工标注）免费提供——不需要靠合并/不合并关系类型来表达。
3. **语义精确性**：`belongs_to` 表达「谁包含谁」（层级容器），
   `housed_in` 表达「谁放在哪」（物理落位）——设备可以「物理在机房 A，
   但台账归属局站 B」的边界用例只有分开建模才能无歧义表达。

**验收（#17）**：两种方案对比数据（迁移成本 60 处断言 / 查询同构论证）+
结论回写 §4.2 与 §12-A1 + 关系注册表落地（`internal/mapping/relations.go`，
CrossLayer 一致性由 `TestRelationRegistryConsistency` 钉死）。
`housed_in` 本体随 W21 关系扩容落地。

## 决策 2：时序 upsert 与资源 skip 语义并存（issue #18，方案 A）

**裁决**：同一系统内两种重导语义**显式并存**，边界写进文档与代码注释：

| 域 | 语义 | 同键重导行为 | 理由 |
| --- | --- | --- | --- |
| 资源（resources） | **身份语义**：同键 = 同资源 | skip，属性差异进 W005 人工确认 | 属性变更需人工确认，防静默改台账 |
| 时序点（metrics） | **值语义**：同 (series_id, ts) 的最新值 = 真值 | `INSERT OR REPLACE` 覆盖 | 重新采集的同一时刻数据应覆盖；skip 会让每日全量重导产生海量 W005 式确认项（方案 B 不可用） |

**依据**：语义按数据本性划分，而非追求表面统一。方案 C（覆盖留痕）的审计需求
由 `metrics.batch_id`（记录最后一次写入批次）+ import_batches 谱系满足，
无需独立变更表。

**验收（#18）**：幂等性测试（同一数据连导 3 次，`SELECT COUNT(*) FROM metrics`
不变；同 (series, ts) 导入不同值，断言最终值为最后一次导入值）随 W20 时序表
落地实测；本 ADR + data-model-v1.md §5.4 D3/§12-A2 回写构成设计侧闭环。

## 后果

- 关系注册表（`RelationSpecs`）成为方向语义唯一事实源，W21 新增 25 种关系
  必须先登记注册表再进 CHECK 约束（注册表测试保证两集合一致）。
- 时序导入引擎实现 `INSERT OR REPLACE`（W20），与资源导入的 skip 通道
  分属不同表族，互不影响既有 E/W/I 规则目录与 W005 确认流。
- 两决策均无破坏性变更：v1-v13 迁移、既有 4 关系、既有测试零触碰
  （W19 全量测试绿为证）。
