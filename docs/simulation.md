# NetOps Lab — 仿真工作台（Simulation Workbench）

> 本文是 Phase 2 仿真工作台的规范说明：工作区模型、物化规则、操作目录、校验目录、三层撤销、前后对比、确定性保证与性能实测。示例数字对应 `testdata/` 三本合成工作簿物化出的 27 设备工作区（`scripts/demo-lab.sh` 可完整复现本文全部数字）。源码是唯一事实来源：`internal/sim/`。

## 0. 合规红线（重申）

- **无生产写路径**：仿真引擎只读写本地 SQLite 中的仿真表；全仓库不存在任何设备连接代码。
- **AI 仅经 5 个类型化 MCP 工具改仿真态**（`lab_apply_ops` / `lab_run_checks` / `lab_diff_undo` / `lab_checkpoint`，加上只读的 `resource_search`）；MCP 无 SQL / Shell / SSH / 文件工具，且**不能创建或删除工作区**。
- **每个变更都走同一引擎路径**（API / MCP / CLI 无旁路）：类型化操作 → 引擎校验 → 状态迁移。AI 生成的描述文本永远不会成为仿真状态。
- **`restore_*` 操作码是引擎内部逆操作**，在每个外部边界（HTTP / MCP / CLI 操作文件）都被拒绝（400 `invalid_request`）。

## 1. 工作区模型（workspace）

一个工作区 = 一份从导入资源**物化**出来的虚拟拓扑：

| 实体 | 表 | 说明 |
| --- | --- | --- |
| 虚拟设备 virtual device | `virtual_devices` | `code` = 设备资源自然键（DEV-001…）、`name`、`status`（up/down） |
| 接口 interface | `interfaces` | 属于某设备，`name` = `eth0`/`eth1`…，可选 IPv4 地址/前缀（NULL = 未配置） |
| 链路 link | `links` | 连接两个**不同设备**上的接口，`label` = 电路编号（物化时） |
| 变更集 changeset | `changesets` + `changeset_ops` | 一次操作提交的原子单元（含逐操作逆操作记录） |
| 校验运行 check run | `check_runs` + `check_results` | 一次五项校验的完整结果 + 拓扑边快照 |
| 检查点 checkpoint | `checkpoints` | Online Backup 快照（文件 + 标记行） |

**ID 约定**：`vd-<n>`（设备）/ `if-<n>`（接口）/ `ln-<n>`（链路）/ `cs-<n>`（变更集）/ `run-<n>`（校验运行）/ `cp-<n>`（检查点）——**全局唯一、按类别单调递增**（`sequences` 表，见 [data-model.md](data-model.md)）。工作区 id 为 UUID。人可读、可引用、可排序（引擎按数值比较 `ln-2 < ln-10`）。

### 1.1 物化规则（`sim.Materialize`）

`POST /api/workspaces {name, description?, sourceBatchIds?}`（批次缺省 = 全部 `completed` 批次，按导入顺序）在**单事务**内完成：

1. **设备**：所选批次中的每个 `device` 资源 → 虚拟设备。`code` = 自然键、`name` = `device_name` 属性、`status`：资源状态为 `在网` → `up`，其余（停用/在建/退网/空/未知）→ `down`。`resource_id` 保留溯源。
2. **链路**：每个 `circuit` 资源的 `a_end`/`z_end` 关系**都**解析到工作区设备 → 一条链路（`label` = 电路编号）。自环电路（a_end == z_end）跳过（对应 Phase 1 W003）。电路状态不参与——链路初始 `up`。
3. **端口分配（确定性）**：电路按 `natural_key` 升序处理；每条电路在两端设备上各取**最低空闲端口序号** → `eth<idx>`（0 起算，按设备独立计数）。接口初始 `up`、地址 NULL。
4. 物化**不是变更集**（血缘存 `workspaces.source_json`）；对同批次的第二次物化得到**结构相同、id 全新**的拓扑（不冲突、行为确定）。

合成数据的演示规模：**27 设备 / 20 链路（21 条电路 − 1 条自环）/ 40 接口**，基线状态哈希 `aea27501fa59…`（sha256，见 §6）。

错误：名称重复 409 `conflict`；所选批次无设备资源 422 `no_source_resources`；批次不存在 404。

## 2. 操作目录（op catalog —— 封闭集合）

外部边界（HTTP `POST /api/workspaces/{id}/ops` / MCP `lab_apply_ops` / CLI `lab apply`）只能提交以下 **10 个前向操作**。字段 camelCase，`op` 判别；**严格字段检查**（写了该操作不用的字段 → 400）。

| op | 字段 | 预校验（纯 payload） | 逆向（引擎自动记录） |
| --- | --- | --- | --- |
| `create_device` | `code`, `name` | 非空；code 工作区内唯一（大小写敏感） | `delete_device {deviceId}` |
| `set_device_status` | `deviceId`, `status` | status ∈ up\|down | `set_device_status {deviceId, status: prev}` |
| `delete_device` | `deviceId` | — | `restore_device`（捕获式级联快照：设备 + 其接口 + 触及的链路） |
| `create_interface` | `deviceId`, `name`, `status?`（缺省 up）, `ipv4Addr?`, `ipv4Prefix?` | name 匹配 `^[A-Za-z0-9._-]{1,32}$` 且设备内唯一；addr+prefix 同设同空；合法点分四段；prefix 0–32 | `delete_interface {interfaceId}` |
| `set_interface_status` | `interfaceId`, `status` | — | `set_interface_status {interfaceId, status: prev}` |
| `config_interface_ipv4` | `interfaceId`, `ipv4Addr`, `ipv4Prefix` | 同设同空；`null/null` = 清除配置；非空则合法四段 + 0–32 | `config_interface_ipv4 {interfaceId, prev…}` |
| `delete_interface` | `interfaceId` | — | `restore_interface`（接口 + 其链路） |
| `create_link` | `aInterfaceId`, `zInterfaceId`, `status?`, `label?` | 两接口存在且**属于不同设备**（自环 ⇒ op_failed）；**同接口对不允许第二条链路**（任一方向，⇒ op_failed；v0.2.1 起，闭环检查点恢复的并行链路歧义） | `delete_link {linkId}` |
| `set_link_status` | `linkId`, `status` | — | `set_link_status {linkId, status: prev}` |
| `delete_link` | `linkId` | — | `restore_link`（链路行） |

**引擎内部操作（`restore_*`，仅在撤销/恢复路径使用，外部提交一律 400）**：

- `restore_device {device:{…}, interfaces:[…], links:[…]}` —— 删除设备时引擎**捕获**被级联删除的完整快照，撤销时原样重插（原 id）；
- `restore_interface {interface:{…}, links:[…]}`；`restore_link {link:{…}}`。

这是"捕获式逆操作"而非 blob 回放：逆操作仍是**类型化、可审计的变更集操作**，与正向操作共用同一事务与校验路径（ADR-0007）。

**应用算法（`sim.ApplyOps`，单 `db.Do` 事务）**：

1. 逐操作校验 → 迁移 → 记录 `(seq, op_code, op_json, inverse_json, 'applied')`；变更集行最后落库。
2. 任何语义失败（实体不存在/重复/自环…）→ **整个事务回滚**（状态与变更集行都不落），随后第二个事务记录**失败变更集**：`ops_applied=0`、失败操作 `failed`、其余 `not_attempted`、`inverse_json` 全 NULL。
3. 应用成功且 `runChecks=true`（默认）→ 立即自动校验（trigger `auto_after_ops`，绑定该变更集）。

**空操作集拒绝（v0.2.1 起）**：`{"ops":[]}` / `{"ops":null}` 在解码边界即被拒绝（400 `invalid_request`，消息 `ops must contain at least one operation`）——不留任何变更集行。此前版本会落一条 0 操作的已应用变更集，使变更集级 undo 被 409 `not_latest` 永久阻塞。

**并发安全（v0.2.1 起）**：DSN 追加 `_txlock=immediate`（所有事务以写模式开启，消除 WAL 下读→写升级的 SQLITE_BUSY 竞争）+ 事务开启 3 次有界重试（50–100ms 退避）。

失败响应（HTTP 422 `op_failed`，真实输出）：

```
{"error":{"code":"op_failed","message":"op 0 (set_link_status): link not found: ln-999 [changeset cs-2 recorded as failed]"}}
```

## 3. 校验目录（check catalog，rule_version 1.0.0）

对（设备、接口、链路）的**状态函数**：同状态 + 同 rule_version ⇒ 同结果（发现行序、消息、detail 逐字节一致）。每个 check 恒定附加一条 `passed=1` 基线行（message `"ok"`）——每次运行自描述：**发现行（按 check_code、实体键、detail 规范 JSON 排序）在前，5 条基线在后**。

评分 = `max(0, 100 − 5×错误数 − 1×警告数)`（提示免费，与 Phase 1 公式一致）。

| 码 | 名称 | 严重级别 | 触发条件（状态函数） | 实体 | message / detail 结构 |
| --- | --- | --- | --- | --- | --- |
| CHK-001 | 重复主机地址 | error | ≥2 个 up 接口（设备 up）配置了同一 IPv4 地址 | workspace | `duplicate host address 10.0.0.1 used by N interfaces`；`{addr, interfaces:[{interfaceId, deviceId, deviceCode, name}]}`（按 deviceCode, name 排序） |
| CHK-002 | 网段重叠 | error | 两个 up 接口（设备 up、已配置地址）的网段重叠；**同地址对跳过**（CHK-001 负责） | workspace | `subnets 10.0.0.0/24 and 10.0.1.0/24 overlap (kind)`，kind ∈ `identical`（同网不同主机）\|`contains`（同基包含）\|`partial`（偏移嵌套）；`{aInterface:{…}, bInterface:{…}, kind, networkA, networkB}`（a < b） |
| CHK-003 | 孤岛设备 | warning | up 设备的 up 接口上没有任何 up 链路（或没有接口） | device | `device DEV-005 is isolated (no up links)`；`{deviceId, code, totalLinks, upLinks}` |
| CHK-004 | 部分分区 | info | up 设备经 up 链路分成 ≥2 个连通分量；最大者为参照（并列取成员集字典序最小），其余每个 ≥2 成员的分量一条 | workspace | `component of N devices is disconnected from the main component`；`{members:[设备编码，已排序]}` |
| CHK-005 | 未配置接口 | info | up 接口（设备 up）无 IPv4 地址 | interface | `interface DEV-003/eth1 has no IPv4 address`；`{interfaceId, deviceId, deviceCode, name}` |

**连通性查询**（不是 check）：`Path` 从设备编码到设备编码做 BFS，边条件 = 链路 up **且**两端接口 up **且**两端设备 up；邻居按设备编码升序展开（路径确定，返回首条找到的路径）。同编码 → `{reachable:true, pathLength:0}`。

`check_runs.topology_json`：每次运行存的**规范边快照**（全部链路、含 down，排序同状态哈希 links 数组）——diff 用它重算两次运行的可达性，无需重放操作。

### 3.1 演示数据上的基线数字

物化后首跑 `lab checks --run`（run-1，trigger `manual`）：

- 评分 **95**；发现 `{error 0, warning 5, info 36}`（共 41 行 + 5 基线）；
- W5 = 5 台孤岛 up 设备（DEV-022/026/027/028/030——导入数据中无电路挂接的设备）；
- I36 = 2 条 CHK-004（分量 [DEV-017, DEV-018] 与 [DEV-020, DEV-021]）+ 34 条 CHK-005（未配置接口）。

注入"同一地址配到两个接口 + 关断 ln-1"后（run-2，`auto_after_ops`）：评分 **89**，`{1, 6, 34}`——新增 CHK-001（10.99.0.1 被 DEV-001/eth0 与 DEV-002/eth1 重复使用）与 CHK-003（DEV-001 孤岛），消失 2 条 CHK-005（两接口已配置）。

## 4. 三层撤销（three-tier undo）

| 层级 | 机制 | 覆盖场景 | 语义 |
| --- | --- | --- | --- |
| ① 事务回滚 | 变更集单事务提交 | **失败的变更集** | 状态零污染；失败事实以 `status='failed'` 变更集留档（`ops_applied=0`），可审计、不参与 LIFO |
| ② 变更集逆向（undo） | 逆操作按**序号倒序**作为**新变更集**应用（trigger `undo`，描述 `undo of <id>`），随后目标标记 `reverted` | 撤销最近一次已应用变更 | 撤销本身也是变更集（带自己的逆操作 ⇒ **撤销的撤销**可行）；仅允许撤销**最新已应用**的前向变更集（LIFO，409 `not_latest`）；failed/reverted/undo 变更集不阻塞 |
| ③ 检查点（checkpoint） | SQLite **Online Backup API** 全库快照 + 工作区级恢复 | 跨多个变更集、跨失败历史回到任一快照点 | 恢复 = 计算 当前状态→快照状态 的差异，作为**前向操作变更集**（trigger `checkpoint_restore`）应用——统一审计、可再撤销；状态哈希相等 ⇒ **no-op**（`changesetId:null, note:"already at checkpoint state"`） |

检查点文件落在 `<dbdir>/checkpoints/<workspaceId>/<cpId>.db`（路径相对库目录存储）；删除工作区时级联清理。快照文件缺失 → 409 `checkpoint_missing_file`。

**检查点完整性（v0.2.1 起）**：创建时快照内嵌状态哈希 marker（从快照重算，而非信任库内当前值）；恢复前校验 marker 与记录的 `workspaceHash` 一致（不一致 ⇒ 409，快照被篡改/库漂移不可恢复）；恢复后复核实际哈希 == 记录哈希，失败 ⇒ 409（删除-重建计划不完整）。恢复的链路匹配键含 label 并按多重集对比（防御旧库中的重复对链路）。

**恢复即前向操作变更集**的理由与"删除-重建"计划（links→interfaces→devices 先删后建、再对齐状态/地址）见 ADR-0008。

## 5. 前后对比（run diff）

`GET /api/workspaces/{id}/checks/{runId}/diff?compareTo=<runId2>` 对比同工作区两次运行：

- **问题匹配键**（对 id 漂移免疫，按 check 提取业务键）：CHK-001 `addr`；CHK-002 排序后的 (aDevice/name, zDevice/name) 端点对；CHK-003 设备编码；CHK-004 排序后的成员编码集；CHK-005 `deviceCode/name`。
- `findingsAdded` / `findingsRemoved`：按匹配键集合差；`scoreDelta = to.score − from.score`。
- **连通性（完整谓词）**：分别从两次运行存的 `topology_json` 快照重算可达对（无向、按分量两两组合），谓词为 **设备 up × 接口 up × 链路 up**（§5 一致）——`set_device_status` / `set_interface_status` 注入的故障同样产生 `connectivityLost`。快照在 v0.2.1 起携带 4 个可选端点状态字段（指针，nil = up），旧格式快照（v0.2.0）自动回退为「视为 up」，与历史 diff 字节级一致；`connectivityLost` / `connectivityGained` 空数组渲染 `[]` 而非 null。
- 同一运行对比两次 → 400；跨工作区 → 404。

演示故障（run-1 → run-2）的真实 diff：`scoreDelta −6`；新增 [CHK-001, CHK-003]；消失 [CHK-005 ×2]；连通性丢失 **20 对**（DEV-001 与其余 20 台设备的全部 pair，含 DEV-001/DEV-003）；恢复 0 对。撤销后反向 diff 显示 findingsRemoved/connectivityGained 恢复。

## 6. 确定性保证

- **状态哈希**（`workspaces.workspaceHash`）：对规范状态 JSON 的 sha256（64 hex）。规范 JSON：设备按 `code` 排序、接口按 `name` 排序、链路按 (aDevice, aInterface, zDevice, zInterface, label) 排序；未配置地址渲染 `null`；字段顺序固定。**同状态 ⇒ 同哈希**（撤销/恢复的"精确回到基线"验证即依赖此）。
- **canonical 排序无处不在**：拓扑节点按 code、接口按 name、边按数值 id（`ln-2 < ln-10`）；校验发现按 (check_code, 实体键, detail JSON)；diff 匹配键如上；BFS 邻居按设备编码升序。
- **可预测的 id 分配**：`sequences` 表在事务内 `UPDATE … RETURNING` 分配；回滚即归还（失败变更集不烧 id——第二个事务重新分配同一号）；`DET-P2` 断言：两个全新库同物化 + 同操作脚本（含一次失败变更集）⇒ 运行/变更集的规范 JSON 相等（id/时间戳除外）。
- **物化确定性**（ACC-P2-1）：同批次两次物化（无论同库还是新库）拓扑结构与业务字段一致；`TestSimulationDeterminism` 固化。
- 时钟可注入（`sim.Service` 的 `Now` 钩子），时间戳不进入哈希/排序键。

## 7. 性能实测

测量环境：2 核沙箱（linux/amd64，Go 1.27.2），27 设备 / 40 接口 / 20 链路工作区，`TestSimulationPerformance`（`go test -v ./internal/sim -run TestSimulationPerformance`）：

| 操作 | 实测耗时 | 预算（PERF-P2） |
| --- | --- | --- |
| 物化 Materialize（27/40/20） | 3–4 ms | < 2 s |
| 物化（同批次第二次，暖缓存） | 3 ms | — |
| ApplyOps（10 个配置操作，单事务） | 0–1 ms | < 2 s |
| ApplyOps（1 操作 + 自动校验运行） | 1 ms | < 2 s |
| RunChecks（全量校验，含基线行） | 1 ms | < 2 s |
| Path（BFS，最长 11 跳） | 0 ms（<1 ms） | < 2 s |
| Topology + 状态哈希（27 节点 / 20 边） | 0 ms（<1 ms） | — |

全部低于预算**三个数量级以上**；量级来源是单文件 SQLite + 全内存状态加载（每操作一次完整 `loadState`）——规模增到数千实体前无需优化。

## 8. 出口层与安全边界汇总

| 出口 | 操作提交方式 | restore_* | 工作区增删 |
| --- | --- | --- | --- |
| HTTP `POST /api/workspaces/{id}/ops` | `{"ops":[…], description?, runChecks?}` | 400 `invalid_request` | ✔（人工/程序化动作） |
| MCP `lab_apply_ops` | 类型化输入（go-sdk schema 生成；引擎 `DecodeOps` 复核） | 工具错误（schema 层 + 引擎层双重拒绝） | ✘（红线：AI 不能建/删工作区） |
| CLI `lab apply --ops ops.json` | 文件（§7 请求体同构） | 400（CLI 退出码 1 + 错误信封） | `lab workspace create` / API |

错误码 → HTTP 状态（英文消息）：`invalid_request` 400 · `not_found` 404 · `conflict` / `not_latest` / `checkpoint_missing_file` 409 · `no_source_resources` / `op_failed` 422。

## 9. 演示

- 脚本：`./scripts/demo-lab.sh`（或 `make demo-lab`）——全新库导入 → 物化 → 基线校验 → 故障注入（CHK-001 + 链路关断）→ 不可达 → 失败变更集原子性演示 → diff → undo（哈希回到基线）→ 检查点创建/破坏/恢复/no-op，每步中文摘要。
- 浏览器：`./bin/netopslab serve` 后打开 `/lab`（拓扑画布 + 设备/接口/链路/检查结果/变更集/检查点六个标签页）。
- 全部 API 形状与真实请求/响应见 [api.md](api.md) §7。
