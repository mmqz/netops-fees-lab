# NetOps Lab — 导入管线与数据质量校验

> 本文是导入→校验→报告全链路的规范说明。示例数字对应 `testdata/` 三本合成工作簿（`cmd/testdatagen` 确定性生成，`testdata/expected-summary.json` 固化预期计数）。

## 1. 导入管线状态机

```
上传/指定工作簿
      │  parse + sha256
      ▼
 analyzing ──（解析失败）──► failed
      │  列分析 + 映射建议
      ▼
 awaiting_mapping ──（映射非法）──► 仍是 awaiting_mapping（400，不落任何资源）
      │  用户确认映射（POST mapping / CLI --map）
      ▼
 importing ──（内部错误）──► failed（整个事务回滚，零半写）
      │  单事务写全部 Sheet
      ▼
 completed ──（DELETE /api/batches/{id}）──► rolled_back
```

- **Analyze**（`POST /api/imports` 或 CLI `analyze` 离线模式）：解析工作簿、计算 sha256、逐列给出建议（header==field 1.0 → 归一化相等 0.9 → 同义词 0.75 → 无建议）；不写任何资源。API 版本创建批次并保存工作簿路径（`workbook_path`）；CLI `analyze` 完全离线（无 DB 写）。
- **执行**（`POST /api/imports/{id}/mapping` / CLI `import --map`）：所有 import sheet 的全部写入在**同一个事务**内完成；每行独立判定结果；行级 E 规则失败**不**中止批次。
- **自动校验**：导入成功后立即重算一次集合级规则（E005/E006/I001），随后任何时刻可再重算（`POST /api/validate`）。

## 2. 行级记账（row accounting）

每个数据行（Excel 第 2 行起、非全空）**恰好获得一个结果**，落 `import_row_results`（`UNIQUE (batch_id, sheet, row_number)`）：

| 行状态 | 语义 | 资源 | 问题 |
| --- | --- | --- | --- |
| `imported` | 通过全部行级规则 | 插入 resource + 派生关系 + source_refs（资源与每条关系各一） | W/I 问题照记（带 source_ref_id） |
| `skipped` | 库中已存在同 `(type, natural_key)` 资源（更早批次创建） | **零改动**（first_batch_id 不变） | 无新问题；行消息引用既有批次 |
| `failed` | 行级 E 规则命中（E001–E004） | 不插入 | 记 issue：batch_id + evidence{sheet,row_number}（无实体故 source_ref_id 为 NULL） |

验收 ACC-1：`imported + skipped + failed == 该批次数据行数 == row_results 条数`。

合成数据实际计数（首导）：

| 工作簿 | Sheet | 数据行 | imported / skipped / failed | 行级问题分布 |
| --- | --- | --- | --- | --- |
| devices.xlsx | 设备清单 | 30 | 27 / 0 / 3 | E001×1、E002×1、E004×1、W002×1（E006/I001 为集合级，见 §4） |
| ippools.xlsx | 地址池 | 20 | 16 / 0 / 4 | E002×1、E003×1、E004×2、W001×1（E005 为集合级） |
| circuits.xlsx | 电路 | 25 | 21 / 0 / 4 | E001×1、E002×1、E003×1、E004×1、W003×1 |

重导同一工作簿（同映射）：30/0/3 → **0 新增 / 27 skipped / 3 failed**；E 类问题重复出现（同源同证据），W002 只在首导记录一次（第二遍该行是 skipped，不重记警告）。资源数、关系数、集合级问题数不变。

## 3. 规则目录（rule_version 1.0.0）

**行级**（随导入逐行触发；E ⇒ 行 failed，W ⇒ 行 imported 且记问题）：

| 码 | 名称 | 严重级别 | 触发条件 | 证据键（evidence） |
| --- | --- | --- | --- | --- |
| E001 | 重复键 | 错误 | 自然键与本批次内已处理的行重复，或库中已有同 `(type, natural_key)` 资源（后者行=skipped） | type, natural_key, conflicting_sheet, conflicting_row_number, existing_entity_id |
| E002 | 必填字段缺失 | 错误 | 必填字段空或未映射 | type, field, raw_value |
| E003 | 引用无效 | 错误 | 引用字段非空但批内/库中无匹配资源 | type, field, ref_value, expected_type |
| E004 | 格式无效 | 错误 | ipv4/cidr/int/enum 解析失败或超界（vlan 1–4094、bandwidth ≥ 1） | type, field, raw_value, reason |
| W001 | 孤儿记录 | 警告 | 可选关系字段留空（assigned_device） | type, field |
| W002 | 状态值未知 | 警告 | status 非空不在枚举内（行导入，状态丢弃） | type, field, raw_value, allowed |
| W003 | 电路自环 | 警告 | a_end == z_end（两端都解析成功） | circuit_code, device |

**集合级（全局）**（对全库状态计算，可重算，`batch_id IS NULL`，报告中 `applicationGenerated: true`）：

| 码 | 名称 | 严重级别 | 触发条件 | 证据键 |
| --- | --- | --- | --- | --- |
| E005 | 网段重叠 | 错误 | 两个 ip_pool 的 CIDR 重叠（IPv4 前缀两两 Overlaps） | pool_a, pool_b, pool_a_id, pool_b_id, overlap_cidr |
| E006 | 地址重复使用 | 错误 | 同一 mgmt_ip 或 gateway 出现在 >1 个资源 | address, resources（"code=value" 列表） |
| I001 | 孤岛设备 | 提示 | 设备零关系（未关联任何地址池/电路） | device_id, device_code, device_name |

严重级别映射在 `internal/domain/rules.go` 的 `SeverityForCode`（导入器与校验引擎共用，无重复定义）。

## 4. 质量评分与报告口径

```
score = 100 − 5 × #error − 1 × #warning      （下限 0）
completeness = round(100 × imported / rows_total)   （rows_total = 0 时记 100）
```

- **全局口径**（默认）：全部问题 + 全部批次。三本导入后：E13 / W3 / I1 → **评分 32**；完整率 90 / 80 / 84。
- **批次口径**（`?batchId=` / `--batch`）：该批次行级问题 + **全部集合级问题**（集合级发现本质是全局的）+ `batches[]` 仅该批次；资源/关系清单仍为全库。设备批次口径：E5 / W1 / I1 → **评分 74**。
- 报告问题排序：severity（error→warning→info）→ rule_code → entityKey → source（workbook, sheet, rowNumber）→ message → id。

## 5. 幂等与回滚策略

**幂等（ACC-2）**：

- 重导同一工作簿（同映射）→ 0 新资源、全部数据行 `skipped`（消息"重复导入：资源已存在于批次 \<id\>"）、集合级问题经重算后不变；
- 集合级校验本身幂等：Run = 单事务"删全部集合级问题 → 重算 → 按序插入"，重复运行结果集相同（`TestSetLevelRecomputeIdempotent`）；
- `netopslab testdata` 与 `cmd/testdatagen` 输出字节级确定（重生成 sha256 一致）。

**回滚（S0-6 / DELETE /api/batches/{id}）**：

- 前置：批次必须 `completed`，否则 409 `conflict`；
- 引用保护：若本批次创建的资源被**其他批次**的关系引用 → 409 `batch_in_use`（如 devices 批次的设备被地址池 assigned_to / 电路 a_end 引用）；
- 执行：单事务删除 关系 → 资源（FK 级联）→ source_refs → row_results → 批次 issues → 状态置 `rolled_back`；响应携带各类删除计数；
- 回滚后重导同工作簿 = 干净重放（幂等语义同样适用）。

## 6. 确定性保证（ACC-4）

- 集合级问题插入顺序 `(rule_code, entity natural key)`；同一库状态 + 同一 `rule_version` 的两次 Run 输出**业务内容**完全一致（id 与时间戳除外）；
- 测试对两个全新库跑相同导入+校验，把 evidence 内实体 id 解析回自然键后比较规范化 JSON，逐字节相等；
- 建议引擎为纯函数（同义词表 + 置信度阶梯），无随机；
- 所有列表输出有确定排序（资源 `(type, natural_key)`、批次 `(started_at, workbook, rowid DESC)`、topology 节点 `(type, natural_key)`）。

## 7. 事务边界一览

| 操作 | 事务 | 失败语义 |
| --- | --- | --- |
| 迁移 | 每版本一个事务（DDL + 账本） | 回滚该版本 |
| 导入执行 | **全部 Sheet 一个事务** | 整体回滚，批次 failed，零半写 |
| 集合级校验 | 一个事务（删+插） | 回滚，保留旧问题集 |
| 批次回滚 | 一个事务 | 回滚，批次保持 completed |
| 行级失败 | 不开新事务 | 仅该行 failed，批次继续 |

## 8. 时间戳与时钟

`importer.Service` 与 `report.Generator` 均有可注入的 `Now` 钩子（生产用 `time.Now().UTC()`，测试可钉住）；所有落库时间戳为 UTC RFC3339 字符串。
