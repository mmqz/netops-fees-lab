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
| E003 | 引用无效 | 错误 | 引用字段非空但批内/库中无匹配资源（machine_room.site_id 依赖导入顺序：先局站后机房） | type, field, ref_value, expected_type |
| E004 | 格式无效 | 错误 | ipv4/cidr/int/enum 解析失败或超界（vlan 1–4094、bandwidth ≥ 1） | type, field, raw_value, reason |
| W001 | 孤儿记录 | 警告 | 可选关系字段留空（assigned_device；machine_room.site_id 不映射/留空同语义） | type, field |
| W002 | 状态值未知 | 警告 | status 非空不在枚举内（行导入，状态丢弃） | type, field, raw_value, allowed |
| W003 | 电路自环 | 警告 | a_end == z_end（两端都解析成功） | circuit_code, device |
| W004 | 身份键退化 | 警告 | 条件自然键类型的主键字段值任一为空，键退化为兜底字段拼接（行照常导入；base_station：基站编码缺失 → 地市\|无线基站名称 兜底，缺地市列则为纯名称） | type, natural_key, missing_fields, fallback_fields |
| W005 | 属性变更待确认 | 警告 | 同键再导命中库内资源且属性值不同（行=skipped 不改库；diff 逐字段记 pending 提案，人工 apply 后生效，v0.4 W4-D） | type, natural_key, resource_id, fields, change_count, sheet, row_number |
| W006 | 疑似改名待确认 | 警告 | 名称类键类型的新键行与某旧键已从源消失的资源属性全等（行照常按新资源导入；记 pending 改名提案，人工 apply 合并身份 / reject 维持独立，v0.5 W6-D） | type, old_key, new_key, resource_id, new_resource_id, matched_fields, matched_count, sheet, row_number |

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
- `netopslab testdata` 与 `cmd/testdatagen` 输出字节级确定（重生成 sha256 一致）；
- **同值再导零 issue 零新提案（W4-D diff-skip 语义；W7-D 起追加谱系版本行）**：同键行属性与库内一致 → 纯 skip，不产生变更提案也不记 W005（「零记录」的精确边界：变更提案与 issue 零新增；**source_refs 版本化例外**——每个再导入批次追加一行来源引用，见 §17）；**重复的 pending diff 去重**：同资源同字段已有 pending 提案且 new 值相同 → 不重复记录（也不重记 W005）；new 值又变 → 新提案（多 pending 并存，按 `(created_at, rowid)` 入队序，先 apply 的生效、后续同字段旧值不匹配 → 409 `attribute_conflict`，详见 §14）。

**回滚（S0-6 / DELETE /api/batches/{id}）**：

- 前置：批次必须 `completed`，否则 409 `conflict`；
- 引用保护：若本批次创建的资源被**其他批次**的关系引用 → 409 `batch_in_use`（如 devices 批次的设备被地址池 assigned_to / 电路 a_end 引用）；
- 执行：单事务删除 关系 → **本批 pending 属性变更提案（W4-D，不对称：applied/dismissed 保留为人工决策审计线；若提案的资源被本批删除则 FK 级联连带删除全部记录——被审计资源已不存在是正确行为）** → 资源（FK 级联）→ source_refs → row_results → 批次 issues → 状态置 `rolled_back`；响应携带各类删除计数（`deleted.attributeChanges` = 本批 pending 数）；
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

## 9. 设备地理坐标列（geo pipeline，v6）

设备 schema 新增三个**可选**可映射列（仅设备 sheet；其他类型不接受）：

| 列 | 目标字段 | E004 校验 |
| --- | --- | --- |
| 纬度 | `geo_lat` | 十进制浮点，[-90, 90]；超界 → `不是合法的纬度（-90 到 90 的数字）` |
| 经度 | `geo_lng` | 十进制浮点，[-180, 180]；超界 → `不是合法的经度（-180 到 180 的数字）` |
| 经纬度 | `geo` | 恰好两个数字（逗号/全角逗号/分号/全角分号/空格分隔，顺序不限）；格式坏 → `经纬度应为两个数字（如 126.63,45.75）` |

- 组合列在行内校验**之前**按**中国边界启发式**拆分为 `geo_lat`/`geo_lng` 属性：`f1∈[73,136] ∧ f2∈[3,54]` → (经度,纬度)（高德惯例）；`f1∈[3,54] ∧ f2∈[73,136]` → (纬度,经度)；两序都不满足（境外/歧义坐标）时取合法解读、默认 `lng=f1, lat=f2`，**不硬失败**。详见 [data-model.md §9](data-model.md)。
- 拆分值随后通过与分列完全相同的 KindLat/KindLng 校验（拆分前已通过 KindGeo 的值按构造在界内）；分列**优先**——`纬度`+`经度` 两列同时提供时组合列不参与拆分，原始 `geo` 值仍作为属性保留以便溯源。
- 建议引擎经同义词梯子自动命中：`经度`→geo_lng、`纬度`→geo_lat、`经纬度`/`装机经纬度`/`坐标`/`GPS坐标`/`A端装机经纬度`/`位置`→geo（置信度 0.75）。
- 资源属性落库后由工作台物化读取（→ 虚拟设备 `GeoLat/GeoLng` → 拓扑节点 `geoLat/geoLng`）；**geo 不参与任何数据质量规则**（E005/E006/I001 均与坐标无关）。

## 10. port 资源类型与真实电信工作簿行为（v0.3.1）

port（设备端口）是 v0.3.1 引入的第四类资源类型，面向真实电信端口工作簿（样例 `samples/telecom-port-multi-header.xlsx`，真实样例授权说明见其目录内 README）：

- **复合自然键（migration v8 后）**：`device_name + port_name` 按 schema 字段序以 `|` 拼接（例：`B2|bvi2.1008`，真实样例的设备名称 B2 + 端口名 bvi2.1008）。E002 缺必填字段、E001 批内同键、幂等 skip、`lookupExisting` 判重全部走拼接后的完整键；单键类型（device/ip_pool/circuit）拼接退化为原值，行为不变。`device_ip` 保持 **ipv4 值列**而非 device 引用（v8 起亦非自然键，作为普通属性落入 attributes）：端口工作簿是自含导出，其设备行可能未入库，作 ref 会因 E003 拒掉整表。存量库升级语义与冲突保留策略见 [data-model.md §11](data-model.md)。
- **带宽 0 合法**：`bandwidth_mbps` 无 IntMin 下界（真实表带宽可为 0），与 circuit 的 `bandwidth ≥ 1` 不同。
- **表头形态**：端口流量监控表与端口资源表两种表头形态经同义词梯子命中各自的 port 字段（两种表头均含「设备名称」列）；同一样例 sheet 内多段拼接可共存。
- 键的稳定性裁决（IP 属可变属性 → 已换为设备名称，migration v8 落地）见 [natural-keys.md](natural-keys.md)。

**重复表头行自动跳过（多段导出拼接）**：真实导出常把表头行重新拼进数据区。导入器对「≥3 个非空格且逐格等于列头」的行判为重复表头行——在任何规则（E002/E004）之前跳过：行状态 `skipped`、消息「跳过重复表头行（多段导出拼接）」、**零 issue**、不计入 E001。样例实测：2 imported（含带宽 0）/ 1 skipped（重复表头）/ 2 failed（表头 B 行走正常 E004 证据链）。

## 11. 工作簿解析上限与批次崩溃自愈（v0.3.2）

**解压尺寸上限（zip 炸弹防护）**：上传的 10MB 压缩限制（413 `too_large`）对解压后尺寸不设防，因此 workbook 解析改为逐 sheet 流式读取并加两道硬上限——单 sheet 物化行数 **> 100,000**（含表头）或全簿物化单元格总数 **> 5,000,000** → 中止解析，走既有 422 错误通道 `workbook_too_large`（消息：「工作簿超出大小上限（单表行数或全簿单元格数量过多），请拆分后重试」）。超限即中止，不会整表先物化。

**批次崩溃自愈（importing/analyzing 复位）**：进程在批次进入 `analyzing`/`importing` 瞬态后崩溃，会留下永卡瞬态的批次（此前无清扫器）。v0.3.2 起，**会执行导入的进程入口**在启动时自愈：`serve` 启动与 CLI `import` 入口把所有仍处瞬态的批次复位为 `failed(interrupted)`——失败原因以 `interrupted:` 前缀落库（标记「管线未达终态」而非普通导入失败），补记 `finished_at`。规则：

- `awaiting_mapping` 是稳定的等待用户确认态，**不动**；终态（completed/failed/rolled_back）不可触碰；
- 幂等：第二次运行找不到瞬态批次，复位 0 条；
- 与并发导入共存：导入的终态写在其导入事务内，之后完成的其他进程导入会覆盖复位结果（可容忍的假阳性按设计接受）；
- **MCP 进程不自愈**（`netopslab mcp` 不调用复位）——避免与并行 HTTP 进程的真实在途导入互相误伤；自愈仅在确定本进程无在途导入的入口（cmd 层）运行。

## 12. base_station 资源类型与条件自然键（v0.4，W2-D）

base_station（基站）是 v0.4 引入的第五类资源类型，面向真实无线基站工作簿（样例 `samples/telecom-base-station.xlsx`，真实表头 14 列：基站类型/无线基站名称/基站编码/电联共享/归属A IP/环/链/归属A名称/归属A端口/网关IP/基站IP/描述/工单号/备注，**无地市列**；授权与披露见其目录内 README）：

- **条件自然键（R4 裁决落地）**：主键 `base_station_code`（基站编码）**非必填**——真实表编码列存在但值为空。编码非空 → 键 = 编码（全局唯一）；**编码为空 → 键退化为兜底字段 `(city, base_station_name)` 按序拼接、跳过空值**（真实表无地市列 → 纯名称；映射了地市列 → `地市|名称`，同名不同地市为两个资源）。**每次退化记 W004**（warning：行照常导入），evidence：`type / natural_key（所用兜底键）/ missing_fields（缺失主键字段）/ fallback_fields`。E002 仅约束 Required 字段（`base_station_name`）；E001 批内判重与幂等 skip 均在**解析后的最终键**上判定——编码行与名称兜底行天然异键。机制通用（`mapping.TypeSpec.KeyFallbackFields` 声明驱动，键解析不硬编码类型名），base_station 是首个使用者。
- **`base_station_ip` 为 string 值列**（非 ipv4）：真实值带掩码（如 `172.7.10.125/30`），且 IP 属可变属性不作身份——KindIPv4 会因 E004 误拒掩码形态。`uplink_a_ip` / `gateway_ip` 为真实 IPv4 列，保持 KindIPv4（坏值 → E004）。
- **无 ref 字段**：基站表是自含导出（归属A名称/端口为自由文本，同 port 的 device_ip 决策），不派生关系；展示名钉定 `base_station_name`（NameFieldOverride）。
- **主键字段不进 attributes**（`base_station_code` 同 port 键字段待遇）；兜底字段 `city` / `base_station_name` 是普通字段，落库后可在 attributes 中检视。
- 样例实测（CLI，两表 8 行）：4 imported（真实行 动力机房2 兜底键 + 编码行 BTS-HLJ-0001 + 哈尔滨|花园小区1 + 齐齐哈尔|花园小区1）/ 4 failed（2×E001 兜底键重复、E002 名称空、E004 归属A IP 坏值）+ 3×W004；重导幂等（0 新增 / 4 skipped / 4 failed，W004 不重记——skipped 行不记警告，与 W002 同语义）。
- 键策略与稳定性裁决见 [natural-keys.md](natural-keys.md) §4/§5；schema v9（resources CHECK 扩容）见 [data-model.md](data-model.md) §12。

## 13. site / machine_room 资源类型与 belongs_to 关联（v0.4，W3-D）

site（局站）与 machine_room（机房）是 v0.4 W3-D 引入的第六、七类资源类型，面向真实机房局站表（样例 `samples/telecom-site-machine-room.xlsx`，真实表头 8 列：地市/区县/设备名称/IP地址/机房ID/机房名称/局站ID/局站名称；授权与披露见其目录内 README）。**同一张 sheet 承载两类实体**——导入故事是把同一 sheet 读两遍：

- **双类型导入（先局站后机房）**：①按局站类型导入（映射 局站ID→site_id、局站名称→site_name 等）落 `site` 资源，**键 = 局站ID**（12 位数字编码，全局唯一）；②再按机房类型导入同一 sheet（映射 机房ID→machine_room_id、机房名称→machine_room_name、**局站ID→site_id（ref）** 等）落 `machine_room` 资源，**键 = 机房ID**（17 位数字编码，全局唯一），并自动派生 `machine_room --belongs_to--> site` 关系。两遍导入各建一个批次（同一工作簿可反复上传，各批次独立记账）；也可在一个工作簿里用两个 sheet（局站 sheet 在前）一次完成——批内 ref 解析覆盖「本批次更早 sheet 导入的资源」。
- **E003 钉死导入顺序**：机房读法映射了 局站ID 列时，引用值按局站ID 解析（批内 site 资源 ∪ 库内已有）——**局站未先入库则该行失败 E003**（`引用不存在：machine_room.site_id=…（目标类型 site）`）。这是特性而非缺陷：容器先落地，归属后建立，顺序错误立即显式失败。
- **可选 ref 与 W001**：机房读法可不映射 局站ID 列（site_id 非 Required）——导入成功、零关系、每个导入行记 W001 孤儿警告（与 ip_pool.assigned_device 留空同语义，行照常导入）。
- **幂等与防重**：重导同一批次语义不变——site/machine_room 已存在的行 skipped；`relations` 的 `UNIQUE (from, to, relation_type)` 天然防止 belongs_to 重复插入，且 skipped 行根本不走到关系创建路径（测试钉死：重导后 belongs_to 计数不变）。
- **ID 为 KindString（前导零即身份）**：机房ID/局站ID 是系统签发的数字串，**绝不数值化**——数字解析会剥前导零导致身份分裂（如 `000123` → `123`）；两 spec 均为 string 键，无算术语义。
- **名称与 IP 的处置**：机房名称/局站名称为展示名（machine_room 用 NameFieldOverride 钉定，site 走 first *_name 启发式）；设备名称为机房/局站内设备昵称——普通属性，**非 device ref**（设备键是 device_code，本表自由文本无法解析，同 port 的 device_ip 决策）；IP地址为 KindIPv4 可变属性（坏值 → E004）。
- 样例实测（CLI，6 行双读法）：局站读法 2 imported / 4 failed（2×E001 局站ID 批内重复 + E004 + E002）；机房读法 4 imported / 2 failed + 1×W001；**3×belongs_to**；前导零键 `000123`、`00045600000000013/14` 逐字节保留；重导幂等（0 新增、关系不重复）。
- 键策略裁决见 [natural-keys.md](natural-keys.md) §4；schema v10（resources + relations 双表重建）见 [data-model.md](data-model.md) §13；API 形态见 [api.md](api.md) 的 site / machine_room 段落。

## 14. 属性变更历史与人工确认（v0.4，W4-D）

**update-on-diff 导入模式**（闭环 R4 裁决「IP 变 = 属性变更事件」，natural-keys.md §2/§6 差距 #1/#2；表结构 `resource_attribute_changes` 见 [data-model.md](data-model.md) §14）：

- **diff-skip 语义**：同键再导入命中库内资源时，取该行映射后的属性字段值（**非键、非枚举、非空**——与首次导入的 attributes 归集同口径），与库内资源逐字段比较。**仅 new 值非空且 ≠ old 的字段**产生一条 pending 变更提案（field/old_value/new_value + sheet/row_number）；行本身**仍走 skip**（row_result 语义不变、rule_code 保持 E001），发一条 **W005** warning issue（evidence：type/natural_key/resource_id/fields/change_count/sheet/row_number），diff 非空才记。diff 为空 → 纯 skip，零 issue 零新提案（TestReimportIdempotent 语义不破坏；W7-D 起 skip 追加一行 source_refs 版本记录，行语义不变，见 §17）。**空新值永不记录**（空值不写属性的既有语义：首轮导入该列为空的字段，再导也带不来空值覆盖）；枚举（status）字段不参与（attrs 本就不含）。
- **人工确认通道（属性才真正生效）**：`POST /api/attribute-changes/{id}/apply`（校验资源当前值 == 提案旧值——不等 → 409 `attribute_conflict`，防「同字段已被其它变更修改后丢失中间变更」；相等 → 新值写入 attributes + updated_at 刷新 + 提案置 applied，同一事务）与 `…/{id}/dismiss`（忽略，资源不动）；CLI `netopslab changes --db … list|apply|dismiss`。**Name 同步**（v0.5 W9-D）：apply 的变更字段恰为该类型展示名字段（NameField，如 base_station_name / device_name）时，`resources.name` 一并写成新值——name 是该字段值的投影，不同步则资源列表永远显示旧名；非 NameField 字段的 apply 不触碰 name。前端资源详情抽屉有「待确认属性变更」面板（应用/忽略按钮，无 pending 不渲染）。**MCP 工具面 5 个冻结**——AI 不得经 MCP 改资源属性。
- **幂等去重口径**：同一资源同一字段已有 **pending** 且 new 值相同 → 再导不重复记录（也不重记 W005，零痕迹）；new 值又变 → 新提案，多 pending 并存按 `(created_at, rowid)` 入队序，先 apply 的生效、后续同字段提案因旧值不匹配 409。
- **回滚不对称**：批次回滚删除链删**本批 pending 提案**；applied/dismissed 保留（人工决策与导入批次生命周期解耦的审计线）；提案的资源被本批删除时 FK 级联连带删除其全部记录（含终态）——被审计资源已不存在，正确行为。见 §5 回滚节。
- **边界声明**：apply 只写 attributes 快照，**不重建派生关系**——ref 类字段（如 machine_room.site_id 属性快照）的变更走属性通道时，belongs_to 关系不随之变化；ref 变更的正确路径是回滚批次后重新导入（E003 顺序约束重新生效）。

## 15. 孤儿上传文件清理（uploads GC，v0.4 W5-D）

上传的工作簿以内容寻址落盘（`dataDir/uploads/<sha256>_<原名>.xlsx`），批次表记录其 `workbook_path`。两类垃圾会产生**从未被任何批次引用**的孤儿文件：崩溃窗口（`analyzing` 瞬态批次失败但文件已写盘）与同内容不同名的重复上传。v0.4.0 起 GC 清理它们：

- **引用口径（保守）**：`import_batches.workbook_path` 非空的**任何状态**批次都算引用——completed 批次回读工作簿出具 issue 证据；failed/rolled_back 批次的路径是「导入了什么」的审计记录（操作者可能据此重导）。**只删从未被任何批次引用的文件**。
- **触发点（cmd 层，二处）**：①`serve` 启动时——HTTP 监听启动**前**串行执行（此时本进程不可能有在途上传，判定无竞态；与 R1-D6 崩溃自愈同款先例）；②CLI 子命令 `netopslab gc --db <db> --data <dir>`（手动可跑，stdout 单 JSON 计数对象 `{"scanned","deleted","referenced","errors"}`，n>0 时 slog Info 走 stderr）。**MCP 进程零触发**（mcp 子命令不经过 serve 启动路径）；HTTP API 无 GC 路由（维护动作归进程所有者）。
- **非致命语义**：删除失败（权限等）记 slog Warn 并计入 `errors`，不中断启动、不改变退出码；uploads 目录不存在（全新安装）为 no-op；uploads 下的子目录跳过不递归（保守）。
- **匹配口径**：引用路径按 Clean 后原值、绝对形式与**基名**三形态匹配（上传文件名含 sha256 前缀，全局唯一）；基名命中优先保留——误保留（多留一个文件）远轻于误删除（丢证据）。
- 测试钉死（internal/app/gc_test.go）：孤儿被删 / completed+failed+rolled_back 三状态引用保留（含相对路径形态）/ 幂等二跑 / 目录缺失 no-op / 删除失败非致命 / CLI 契约（exit 0 + 单 JSON + usage 错误 2）。


## 16. 疑似改名检测与身份合并人工流（v0.5，W6-D）

闭环 [natural-keys.md](natural-keys.md) §2/§6 差距 #3——「名称变 = 身份变更事件」的人工确认流（migration v12 的 `rename_proposals` 表，DDL 与设计决策见 [data-model.md](data-model.md) §15）。

**与 W005 的对偶关系**：W005 是「键没变、数据变」（同键行 skip + 属性 diff 待确认）；W006 是「键变了、数据没变」（新键行照常导入 + 改名提案待确认）——两者合起来覆盖「带变化再导入」的两半。

### 16.1 检测五条件（全部满足才提案，否则保守沉默）

1. **类型开启**：`mapping.TypeSpec.RenameDetection`——仅名称类键类型（ip_pool / base_station / port）；编号类键类型（device/circuit/site/machine_room）按教义不开启（编号是系统签发身份，变了就是另一资源）。
2. **旧键消失**：候选旧资源的 natural_key 不在本次导入该类型的键全集里（全集 = 本批全部成功解析行的最终键，含 skip 行——改名 = 旧名字从源消失）。
3. **属性全等**：候选旧资源与新行在全部非键、非兜底键、非 ref、非枚举、非计算字段上归一化后相等（空串视同缺省，与 attributes()/W005 diff 同一取值口径）；未映射列产生 "" 与库内非空值**不等**（sheet 停止映射某列不会静默「匹配」）。
4. **双射**：恰一个候选匹配该新行、且该候选不被其它新行同时匹配；0 个或 ≥2 个（任一侧）一律沉默。
5. **非空洞**：至少一个非空属性参与匹配（名称-only 表的空属性全等不发 W006）。

**已知误报面（诚实声明）**：条件 2 假定本 sheet 是该类型的**全量快照**；增量导入（sheet 只含部分资产）时，未出现在 sheet 里的旧资源会被误判「消失」——若其属性恰与新行全等则产生一条假提案。这是保守方向的误报（假提案只产生一条 W006 warning + 一条 pending 提案等待人工，绝不自动合并），人工 reject 即可；按教义宁可误报不可漏报+自动合并。

### 16.2 行为与幂等

- 命中后：行**照常作为新资源导入**（安全默认 = 既有行为，绝不拦截导入）+ 行挂 W006 warning（evidence 含 old_key/new_key/matched_fields 等）+ 同一执行事务写入一条 pending 提案（半账不可能）。
- **再导同一 sheet 幂等**：新键资源已存在 → 行走 skip 路径属性全等 → 纯 skip 零 issue 零新提案（首轮 pending 提案仍在等人工决策；W7-D 起 skip 追加 source_refs 版本行，见 §17）。
- **apply 之后**：旧资源换键且键分量属性同步（见 16.3），再导该 sheet → 纯 skip 零 issue。

### 16.3 确认改名（apply）——单事务身份合并

`POST /api/renames/{id}/apply` / CLI `netopslab renames --db … apply --id <id>`：

1. 校验：提案必须 pending（否则 409 `conflict`）；旧资源存在（被删则提案已 CASCADE 消失 → 404）；新资源必须仍存在——被批次回滚了则 409 `target_missing`（改名证据随批次消失，把旧资源改向从未落地的键是错的，建议 reject）。
2. 新资源的关系 retarget 到旧资源 id（from/to 两端；撞 UNIQUE(from,to,type) 的删新侧边——重复边是双重记账的产物）。
3. **键分量属性同步 + Name 同步**：新资源的键分量字段（自然键字段 + 兜底字段，`TypeSpec.KeyComponentNames`）里非空者覆写旧资源同名属性——改名后键与其来源属性（如 base_station_name）必须一致，否则每次再导都触发假 W005；新侧为空/缺省则保留库内值（未映射列不是空值证据）。其余属性**证明相等**（匹配前置条件），无需同步。**展示名 Name 一并同步**（v0.5 W9-D）：`resources.name` 是该类型 NameField 值的投影，合并后取新名——取值先看新资源 attributes 里的 NameField 值，键类名称字段（port/ptp_port 的 port_name、ip_pool 的 pool_name）不进 attributes 时取新资源自身的 name（导入时正是按该字段算出的），非空才覆写、同「非空者胜」口径。
4. 删新资源（其 pending 属性变更随 v11 CASCADE 消失——文档化行为）。
5. 旧资源 natural_key = 新键（新资源已删故 UNIQUE 无碰撞）+ updated_at 刷新；提案置 applied + decided_at。**旧资源代理键 res_x、关系、SourceRef 不动**（新资源的 SourceRef 行刻意保留为审计痕迹；旧资源的来源谱系版本化自 v0.5 W7-D 起落地——skip 即版本，见 §17 与 natural-keys §6 #4）。

### 16.4 拒绝（reject）与回滚不对称

- `POST /api/renames/{id}/reject` / CLI `… reject --id`：提案置 rejected + decided_at，资源零改动（两资源各自独立 = 导入的默认结果）。
- **回滚不对称（镜像 v11 语义）**：批次回滚显式删除该批 **pending** 提案（新资源随之消失，悬空提案无意义）；applied/rejected 留审计线（人工决策与导入批次生命周期解耦）。例外：旧资源本身被回滚删除 → 提案随 resource_id CASCADE 消失（含终态）——被审计资源已不存在，记录随之消失是正确行为。
- **MCP 工具面 5 个冻结**：改名确认是 HTTP/CLI 的人类通道，AI 不得经 MCP 合并身份或改键（同 W005 apply 红线）。

### 16.5 API / CLI / 前端面

- `GET /api/renames?status=pending|applied|rejected|all&resource_id=&resource_type=&batch_id=&limit=&offset=`（分页边界同 §9 惯例，信封 `{"items":[…],"total":n}`，按 `created_at, rowid` 排序）；`POST /api/renames/{id}/apply|reject`——契约见 [api.md](api.md)。
- CLI：`netopslab renames --db <db> list [--status --type --limit] | apply --id <id> | reject --id <id>`（结构化 JSON stdout，错误信封 exit 1）。
- 前端：资源详情抽屉「疑似改名待确认」面板（旧资源侧；确认/拒绝 + 行内错误；无 pending 不渲染）。

## 17. 来源引用版本化（v0.5 W7-D）

闭环 [natural-keys.md](natural-keys.md) §6 差距 #4——「这个键最近被哪个工作簿确认过」的谱系。**零迁移**：`source_refs` 表的 `UNIQUE (entity_kind, entity_id, batch_id)`（每批每实体至多一行）与 `batch_id ON DELETE CASCADE`（随批消失）天生支持版本化，append 即版本，无 migration v13。

**语义：skip = 源确认过该键。**

- **skip 路径追加版本行**：同键再导入（纯 skip 与 W005 diff-skip 两形态——同一代码点）的行依然纯 skip——行结果（skipped 状态、E001 rule_code、消息）、计数、issue 语义**零变化**，但对当前批次**追加一条** `source_refs` 版本行：`entity_kind=resource`、`entity_id=既有资源`、`batch_id=本批`、`workbook/sheet/row_number/raw=本行证据`（与 insert 路径的来源引用同一构造，镜像先例）。这是纯增量的谱系记录，不是数据变更。
- **每批每实体至多一行**：UNIQUE 约束保证；同批内第二行同键在 **E001 检查点**失败返回，到不了 skip 检查点，不追加（这也保证 skip 路径的插入永不撞 UNIQUE）。
- **版本序（读侧）**：`SourceRefRepo.ListByEntity` 排序自 W7-D 起为 `ORDER BY rowid`（插入序 = 时间上的版本序；batch_id 是随机 UUID，字典序无意义）。`GET /api/resources/{id}` 的 `sourceRefs[]` 按此序返回——创建批次在前，每次再导在后。回滚删行不重编号剩余行，序稳定。
- **回滚随批消失**：版本行与批次共存亡（回滚删除链的 `DeleteByBatch`；DDL 的 `batch_id ON DELETE CASCADE` 同语义）。回滚再导入批次 → 该批版本行消失，资源回到创建批次时的引用数；创建批次的引用不受影响。`DELETE /api/batches/{id}` 响应的 `deleted.sourceRefs` 计数现含 skip 追加行——诚实语义（该批记录的引用数，含确证版本）。
- **版本行记录当时工作簿的原话**：W005 diff-skip 的版本行 `raw` 记录**新值**（如新管理 IP），即使属性变更提案尚 pending 未 apply——谱系不等人工决策，先记录事实。
- **E001 批内重复不记**：失败行到不了 skip 检查点（见上），同批重复键不会产生第二条版本行。
- **读侧触点**：资源详情 API/前端「来源」标签展示完整版本列表（列表渲染已有，零改动）；**MCP 溯源仍展示首源**（FirstBatchID 匹配逻辑不受排序与多版本影响，展示语义不变）；rename apply 后新资源的 SourceRef 行保留为审计痕迹（W6-D 语义不变），旧资源的版本链完整。
- 测试钉死（internal/importer/sourcerefversion_test.go + repo/api 同名测试）：纯 skip 2 版本 / diff-skip 2 版本且 pending 提案并存 / E001 批内重复不追加 / 回滚版本随批消失 / ListByEntity 版本序（插入序非 batch_id 字典序）/ 详情 API 三轮导入三版本。


## 18. PTP 配置脚本导入通道（v0.5，W8-D）

第 4 类真实电信材料形态：中兴 VRP 风格 PTP 配置脚本（文本，非 xlsx）。**架构 = 解析器产出「虚拟 sheet」→ 完整复用既有 analyze → 映射确认 → 导入执行管线**——脚本通道没有旁路，E001-E006/W001-W006/I001 全规则、幂等、回滚、SourceRef 版本化（§17）、改名检测（§16）全部天然适用。

### 18.1 通道入口与嗅探

- `workbook.Read`：扩展名 `.txt`/`.cfg` 且内容同时出现 `config ter` 与 `ptp` 脚本特征 → 走 `internal/ptpconfig` 解析器；否则 xlsx 路径（语义零变化）。`.xlsx` 扩展名永远走 excelize。
- HTTP `POST /api/imports`：扩展名白名单 `.xlsx`/`.txt`/`.cfg`；`.txt`/`.cfg` 内容无脚本特征 → 422 `unsupported_script`；解析失败（语法）→ 422 `script_parse_error`（消息含行号）；其它扩展名维持 422 `not_xlsx`。
- CLI：`netopslab analyze --file <script.txt>` / `import --file <script.txt> --map <map.json>` 与 xlsx 完全同流程。

### 18.2 解析规则（internal/ptpconfig，确定性、带行号错误）

- 脚本块以「<型号>配置脚本」标题行分界（如 `6150配置脚本` → device_model=6150）；`config ter`/`end`/`write`/`$`/`!`/`exit` 为结构关键字；CRLF/空行容错。
- 接口块 `interface <name>[\t<注释>]`——tab 后缀为该端口的注释属性（如「互联口」「BBU」）；块内 `ptp enable` 及各 `ptp <param>` 行归该端口；`port interface <name> portnum <n>` 归 PTP 块端口参数。
- 全局 PTP 时钟参数（`ptp clock-mode`/`priority1`/`domain-num`/`ssmclock *` 等）记录在该型号的所有端口行上（扁平重复落列——表格式资源的诚实形态）。
- 块外说明行（`show ptp port-state all\t查看接口状态…`）不是配置，解析忽略（样例保留该行作为真实形态证据）。
- **枚举字段落点（通用机制声明）**：`delay_mechanism` 是 ptp_port 唯一枚举字段——按既有枚举机制落入 `resources.status` 列（不进 attributes JSON）：坏值触发 W002（文案为通用的「状态值未知」）、值丢弃行照常导入；枚举不参与 W005 属性 diff（再导改枚举值=纯 skip 零 warning）。
- vlan 块（`vlan 4003`）不是接口，不产生行；`synchronization-clock line` 行的接口归属解析器按测试钉死的规则处理（见 ptpconfig golden 测试）。

### 18.3 虚拟 sheet 与映射

- 解析结果合成单 sheet「PTP配置」，列头用 ptp_port spec 的中文同义词（设备型号/接口名称/注释/端口号/…）——**既有 SuggestType 同义词匹配自动建议 ptp_port 类型**（实测 confidence 0.75、23 列全映射），映射确认页与 xlsx 完全同交互（可改选类型/改映射）。
- 执行时按 batch.workbook_path 重新读取脚本并重新解析（解析确定性 ⇒ 同文件同结果），行级记账/issue/幂等与 xlsx 通道一致：**再导同一脚本 → 纯 skip 零 issue + SourceRef 版本行追加（§17）**；改接口名再导 → W006 疑似改名提案（§16，键名称类分量）。

### 18.4 诚实声明

- **模型级模板身份**：脚本只携带设备型号（标题行），不携带设备实例身份——ptp_port 行表达「该型号模板下此接口的 PTP 配置」；与 device 资源的实例级关联需要脚本未提供的数据（编号/名称），故无 ref 字段、无派生关系。
- 脚本通道当前仅支持 PTP 配置形态（第 1 个解析器）；其它脚本家族沿用此框架扩展（解析器接口不变，新 spec + 列头同义词即可）。
