# NetOps Lab — TeleAgent 集成指南（MCP 注册 + CLI 回退）

> ⚠️ **诚实声明**：部署态 TeleAgent 的 MCP 调用**未在本开发环境验证**（环境内没有 TeleAgent 实例；v0.2.0 时点状态与 Phase 1 相同）。已验证的是：进程内 MCP stdio 回环（go-sdk 客户端 × `mcpserver.Run`，`TestMCPServerResourceSearch` / `TestMCPToolRegistry` / `TestMCPLab*` ×4 / `TestMCPGraphToolsRoundTrip` / `TestMCPAlertsMetricsRoundTrip`）**以及**真子进程 stdio 冒烟（`netopslab mcp --db <fresh>` 裸 JSON-RPC initialize + tools/list → 服务器身份与工具清单〔v0.2.0 时点为 5 工具，v0.9 起 **10 工具**〕）。**接入部署态时请按本文操作，并把结果（成功/报错/协议差异）反馈给工程组。** 在验证完成前，建议优先使用 CLI 回退路径（§4）——它与 MCP 共用同一引擎，输出同为结构化 JSON。

## 1. 前置

- 二进制：`bin/netopslab`（`make build` 产出；单文件，含前端与全部功能，无运行时依赖）
- 数据库：一个 SQLite 文件（如 `/opt/netops/data/netops.db`；不存在会自动建库/迁移）
- 导入数据：CLI `import`（§4）或页面（serve 模式）先行完成——MCP 工具**不能导入**
- **仿真工作台**：`lab_apply_ops` 等工具只改**已存在**的工作区；工作区创建是人工/API/CLI 动作（`lab workspace create` 或 `POST /api/workspaces`），MCP 无建删工作区工具（安全设计）

## 2. MCP 注册（stdio）

### 2.1 启动命令

```bash
netopslab mcp --db /opt/netops/data/netops.db
# 或用环境变量（--db 优先级高于 NETOPS_DB）：
NETOPS_DB=/opt/netops/data/netops.db netopslab mcp
```

服务器以 **stdio**（stdin/stdout）承载 MCP 协议（newline-delimited JSON-RPC 2.0）；日志与诊断走 **stderr**——宿主切勿把 stderr 混入协议流。

### 2.2 服务器身份与工具清单

- name：`netops-lab`
- version：`v0.6.0`（`domain.AppVersion` 常量，MCP initialize 响应报告；v0.3.2 起该常量由 `TestAppVersionMatchesChangelogHead` 与 CHANGELOG 头版本锚定，此示例值随版本演进——以握手实际响应为准）
- 工具清单（`tools/list`）：**恰好 11 个**（无 SQL/Shell/SSH/文件工具；`TestMCPToolRegistry` 断言排序比较；v0.9 R4 新增 5 个只读分析工具，v0.14 新增费用风险查询）：

| 工具 | 类别 | 一句话职责 |
| --- | --- | --- |
| `resource_search` | 只读 | 检索导入资源（P1，不变） |
| `fee_risks` | 只读分析 | 费用风险六场景查询（#58 阶段 5：场景+费用类型+账期→线索+行级证据锚点；阈值恒用治理默认值，AI 不可调参；默认构建未编译引擎时明确报错） |
| `graph_traverse` | 只读分析 | 纵向穿透（§10.3 层序 BFS，down/up/both） |
| `graph_impact` | 只读分析 | 根因候选 + 影响面分类（§10.5/§10.6） |
| `graph_context` | 只读分析 | 一站式分析上下文（详情 + 邻接 + 最新指标 + 异常） |
| `alerts_query` | 只读分析 | 检测告警查询（firing/acked/cleared × severity） |
| `metrics_query` | 只读分析 | 指标时序查询（劣化曲线证据） |
| `lab_apply_ops` | 仿真写 | 对工作区应用类型化操作（含故障注入），单事务变更集 + 自动校验 |
| `lab_run_checks` | 只读 | 立即执行五项校验，返回发现行 |
| `lab_diff_undo` | 读/写复用 | 一次调用做 run 前后对比（diff）或撤销变更集（undo），`mode` 判别 |
| `lab_checkpoint` | 仿真写 | 检查点的 create / list / restore |

**工具错误格式**：`"<code>: <message>"`（MCP tool error 语义，IsError=true）。code 汇用 §7 HTTP 词汇：`invalid_request` / `not_found` / `conflict` / `not_latest` / `checkpoint_missing_file` / `op_failed` / `no_source_resources`；`op_failed` 消息格式与 HTTP 完全一致，如 `op 1 (set_link_status): link not found: ln-99 [changeset cs-2 recorded as failed]`。未知工具名 → MCP 协议级 unknown-tool 错误。

### 2.3 工具 schema（输入 → 输出，字段逐项）

**`resource_search`**（P1 不变）：

- 输入（全部可选）：`q`（string，natural_key/name/attributes 不区分大小写包含）、`type`（`device|ip_pool|circuit`，非法值 → 工具错误）、`limit`（int ≥0，缺省 20，上限 100）、`offset`（int ≥0）
- 输出：`{"total":int, "items":[{id,type,naturalKey,name,status,attributes,source:{workbook,sheet,rowNumber}|null, applicationGenerated:false}], "applicationGenerated":false}`

**`lab_apply_ops`**：

- 输入：`workspaceId`（string，必填；需先物化——MCP 不能创建）、`ops`（**类型化操作数组**，每个元素 `op` 判别：`create_device{code,name}` / `set_device_status{deviceId,status}` / `delete_device{deviceId}` / `create_interface{deviceId,name,status?,ipv4Addr?,ipv4Prefix?}` / `set_interface_status{interfaceId,status}` / `config_interface_ipv4{interfaceId,ipv4Addr,ipv4Prefix}`（`null/null`=清除）/ `delete_interface{interfaceId}` / `create_link{aInterfaceId,zInterfaceId,status?,label?}` / `set_link_status{linkId,status}` / `delete_link{linkId}`；**`restore_*` 引擎内部码会被拒绝**）、`description`（string，可选）、`runChecks`（bool，缺省 true）
- 输出：`{"changesetId","trigger":"user","opsApplied","workspaceHash","checks":{"runId","counts":{"error","warning","info"},"score"}|null}`——`runChecks:false` 时 `checks` 为 JSON **null**（§8 契约，与 HTTP 线格式一致）
- 双重防线：go-sdk 从 `[]sim.Op` 生成的输入 schema（未知字段/未知码先被 schema 拒）+ 处理器重编组后走引擎 `DecodeOps`（引擎仍是封闭操作码集的唯一权威）

**`lab_run_checks`**：

- 输入：`workspaceId`（string，必填）
- 输出：`{"runId","counts":{"error","warning","info"},"score","workspaceHash","results":[{"checkCode","severity","passed","entityKind","entityId","message"}]}`——**仅发现行**（紧凑供 AI 消费；`passed` 恒 false）

**`graph_traverse` / `graph_impact` / `graph_context` / `alerts_query` / `metrics_query`**（v0.9 R4，五个只读分析工具——AI 根因分析闭环）：

- `graph_traverse` 输入：`resourceId`（必填）、`direction`（down（默认）/up/both）、`layers`（0..6 逗号列表）、`relations`（逗号列表）、`maxDepth`（1..8，缺省 6）、`maxNodes`（1..500，缺省 200）、`pathOnly`；输出与 HTTP `GET /api/resources/{id}/traverse` **同一引擎文档**（root/nodes/edges/truncated/stats）；
- `graph_impact` 输入：`resourceId`、`direction`（down 根因追查 / up 影响面收集（默认））、`maxDepth`（缺省 8）；输出 = HTTP impact 契约（root/impacted/candidates/truncated，空集合恒 `[]`）；
- `graph_context` 输入：`resourceId`、`depth`（1..2，缺省 1）、`metrics`（latest（默认）/none）；输出 = §7.3 一站式文档（资源详情 + neighbors + metrics 最新点 + recentAnomalies）——**AI 分析单资源的首选入口**；
- `alerts_query` 输入：`state`（firing（默认）/acked/cleared）、`severity`、`resourceId`、`limit`（缺省 20 上限 100）、`offset`；输出 `{items, total, offset, limit}`，item 含规则/阈值/最新值/资源投影与时间线——**ack/clear 不存在对应工具**（仅人类 HTTP/CLI）；
- `metrics_query` 输入：`resourceId`、`keys`（逗号列表，封闭目录校验）；输出 `{resourceId, series[]}`（每序列 points：ts/value/quality）；
- 推荐分析旅程：`alerts_query(state=firing)` → 取告警的 `resource.id` → `graph_context` 看邻接与指标 → `graph_impact(direction=down)` 取根因候选（中文判定理由）→ `metrics_query` 取劣化曲线佐证。

**`lab_diff_undo`**（一个工具两种模式，`mode` 判别，联动字段校验）：

- 输入：`workspaceId`（必填）、`mode`（`"diff"|""undo"`）、`fromRunId` / `toRunId`（diff 模式**两者必填**；同时给了 `changesetId` → 工具错误）、`changesetId`（undo 模式可选 = 最新已应用；给了 from/to → 工具错误）；未知 mode → 工具错误
- 输出（`mode` 回显 + 按段 omit）：
  - diff 段：`{"from":{runId,startedAt,score,counts},"to":{…},"findingsAdded":[{"checkCode","severity","message","detail"}],"findingsRemoved":[…],"connectivityLost":[{fromDevice,toDevice}],"connectivityGained":[…],"scoreDelta"}`——空列表渲染 `[]` 而非 null；`detail` 为解析后的 JSON 对象
  - undo 段：`{"changesetId","trigger":"undo","reverted","opsApplied","workspaceHash","checks"}`

**`lab_checkpoint`**（一个工具三种动作，`action` 判别，联动字段校验）：

- 输入：`workspaceId`（必填）、`action`（`"create"|"list"|"restore"`）、`label`（create 必填；同时给 `checkpointId` → 工具错误）、`checkpointId`（restore 必填；同时给 `label` → 工具错误）；list 两者都给 → 工具错误
- 输出（`action` 回显 + 按段 omit）：
  - create：`{"checkpointId","label","workspaceHash","createdAt"}`（无 checks 字段）
  - list：`{"items":[{"checkpointId","label","workspaceHash","createdAt"}],"total"}`
  - restore：`{"checkpointId","changesetId","opsApplied","restoredToHash","workspaceHash","checks"}`；**no-op 恢复**（已在检查点状态）省略 `changesetId` 并带 `note:"already at checkpoint state"`

### 2.4 宿主侧注册要点（待部署验证）

- 以 stdio 子进程方式拉起 `netopslab mcp`；长驻即可；
- 注意 stderr 分流；进程退出码非 0 视为服务器启动失败；
- **再次强调**：本节为按契约撰写的操作指引，未经部署态验证——接入时如遇握手/schema 差异，请反馈 `docs/verified-capabilities.md` 的未验证清单。

## 3. 安全边界（供宿主审计）

- MCP 工具面 = 5 个（上表；`TestMCPToolRegistry` 断言恰好 5 个、无第六个）；`lab_create_workspace` / `run_sql` 等名字 → 协议级 unknown-tool 错误；
- 仿真写只能经 `lab_apply_ops` 的 10 个类型化前向操作（`restore_*` 引擎内部码被 schema + 引擎双重拒绝）；AI 的自然语言**永不**成为仿真状态；
- CLI 无 SQL/Shell/SSH 子命令；`import` 只消费 xlsx + 映射 JSON；
- 无生产网络凭证；仓库数据除 `samples/` 真实样例（项目所有者授权、仅限本私有仓库，见 [samples/README.md](../samples/README.md)）外均为合成测试数据——工具面接触不到 `samples/` 目录本身（MCP 无文件工具，导入须经用户主动走 HTTP/CLI 上传）。

## 4. CLI 回退（Skills 解析结构化 JSON）

所有子命令成功时 stdout 输出**恰好一个 JSON 对象**（缩进 2 空格）；失败输出 `{"error":{"code","message"}}`；退出码 **0 成功 / 1 失败 / 2 用法错误**。适合 Skills 用 shell 调用 + JSON 解析。P1 命令见 §4.1（真实输出不变）；**Phase 2 新增 `lab` 子命令树**（§4.2，输出为真实运行捕获）。

### 4.1 P1 命令速查

```bash
netopslab analyze  --file devices.xlsx                      # 离线解析 + 映射建议（不写库）
netopslab import   --file devices.xlsx --map map.json --db ./data/netops.db
netopslab validate --db ./data/netops.db [--batch <id>]    # → {"issues":{error,info,warning},"score":32}
netopslab report   --db ./data/netops.db --format html --out report.html
netopslab query    --db ./data/netops.db resources|stats|relations …
```

真实输出（三本工作簿导入后的库）：

```bash
netopslab validate --db ./data/netops.db
```
```json
{
  "issues": {
    "error": 13,
    "info": 1,
    "warning": 3
  },
  "score": 32
}
```

```bash
netopslab query --db ./data/netops.db stats
```
```json
{
  "totalResources": 64,
  "byType": { "circuit": 21, "device": 27, "ip_pool": 16 },
  "byStatus": { "停用": 2, "在建": 1, "在网": 43, "退网": 1 },
  "byRelationType": { "a_end": 21, "assigned_to": 15, "z_end": 21 },
  "issuesBySeverity": { "error": 13, "info": 1, "warning": 3 },
  "latestBatchId": "bat_b1052d12-…"
}
```

```bash
netopslab import --file devices.xlsx --map map.json --db ./data/netops.db
```
```json
{
  "batchId": "bat_ecf7ba6f-…",
  "status": "completed",
  "summary": { "rowsTotal": 30, "rowsImported": 27, "rowsSkipped": 0, "rowsFailed": 3 },
  "issues": { "error": 4, "info": 27, "warning": 1 }
}
```

### 4.2 lab 子命令树（Phase 2，全部真实输出）

```bash
netopslab lab workspace create --db ./data/netops.db --name 故障注入演示 --all-batches
#            （或 --batches id1,id2 限定批次）
netopslab lab topology      --db ./data/netops.db --workspace <wsId>
netopslab lab path          --db ./data/netops.db --workspace <wsId> --from DEV-001 --to DEV-003
netopslab lab apply         --db ./data/netops.db --workspace <wsId> --ops ops.json [--desc <s>] [--no-checks]
netopslab lab checks        --db ./data/netops.db --workspace <wsId> [--run]   # 缺省打印最近一次运行
netopslab lab diff          --db ./data/netops.db --workspace <wsId> --from run-1 --to run-2
netopslab lab undo          --db ./data/netops.db --workspace <wsId> [--changeset <id>]
netopslab lab checkpoint    --db ./data/netops.db --workspace <wsId> (--list | --create --label <l> | --restore --id <cpId>)
```

`ops.json` = §7 ops 请求体同构（`{"ops":[…], "description?", "runChecks?"}`）。

`lab workspace create`（物化 + 状态哈希）：

```json
{
  "workspaceId": "606f28dd-a63d-4ddf-a2a5-f877bb6df58e",
  "name": "故障注入演示",
  "sourceBatchIds": ["bat_…", "bat_…", "bat_…"],
  "counts": { "devices": 27, "interfaces": 40, "links": 20 },
  "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa"
}
```

`lab checks --run`（基线校验）：

```json
{
  "runId": "run-1",
  "trigger": "manual",
  "counts": { "error": 0, "warning": 5, "info": 36 },
  "score": 95,
  "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
  "durationMs": 1
}
```

`lab path`（连通性；不可达时 `{"reachable":false,"hops":null,"pathLength":0}`）：

```json
{
  "reachable": true,
  "hops": [ { "deviceId": "vd-1", "code": "DEV-001" },
            { "deviceId": "vd-2", "code": "DEV-002" },
            { "deviceId": "vd-3", "code": "DEV-003" } ],
  "pathLength": 2
}
```

`lab apply`（故障注入：3 操作 + 自动校验）：

```json
{
  "changesetId": "cs-1",
  "trigger": "user",
  "opsApplied": 3,
  "workspaceHash": "005c7b2066f4b82e38baef75dbf30f286ad964754fb0d1dbbf924e19646b5cc3",
  "checks": { "runId": "run-2", "counts": { "error": 1, "warning": 6, "info": 34 }, "score": 89 }
}
```

失败路径（`--ops` 内含未知实体）→ 退出码 1 + 错误信封（消息为引擎完整错误链，与 HTTP 422 同源）：

```json
{"error":{"code":"op_failed","message":"sim: op 1 (set_link_status): link not found: ln-999 [changeset cs-2 recorded as failed]"}}
```

`lab undo`（撤销最近变更集；哈希回到基线）：

```json
{
  "changesetId": "cs-3",
  "trigger": "undo",
  "reverted": "cs-1",
  "opsApplied": 3,
  "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
  "checks": { "runId": "run-3", "counts": { "error": 0, "warning": 5, "info": 36 }, "score": 95 }
}
```

`lab checkpoint --create` / `--restore`（快照往返；注意 CLI 的 no-op 恢复渲染 `"changesetId": ""`，HTTP 层为 `null`——两者语义相同，消费方按空值处理）：

```json
{
  "checkpointId": "cp-1",
  "label": "演示基线",
  "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
  "createdAt": "2026-10-09T17:06:04.992866702Z"
}
```

```json
{
  "checkpointId": "cp-1",
  "label": "演示基线",
  "changesetId": "cs-5",
  "opsApplied": 3,
  "restoredToHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
  "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
  "checks": { "runId": "run-5", "counts": { "error": 0, "warning": 5, "info": 36 }, "score": 95 }
}
```

### 4.3 建议的 Skill 模式

1. `analyze` 拿 `suggestedMapping` →（人工或规则确认后）落成 map.json；
2. `import`（devices → ippools → circuits 顺序，引用先落库）；
3. `lab workspace create --all-batches` 物化工作台（人确认批次范围）；
4. `lab checks --run` 取基线评分 → `lab apply` 注入变更/故障 → `lab path` / `lab diff` 观察影响 → `lab undo` 或 `lab checkpoint --restore` 恢复；
5. 查询型任务用 `query resources|relations`；一切写操作仅 `import`（Excel）与 `lab apply`（类型化仿真操作）——**没有设备写命令**，生产操作不在工具面内。

## 5. 附录 — jsonschema tag 教训（P2-T5 复盘）

go-sdk 通过 `google/jsonschema-go` 从结构体 tag 生成工具输入 schema；tag 内容是**纯文本属性描述**，规则比 P1 认识到的更严：

1. **`WORD=` 前缀是保留语法**：任何 `=` 出现在第一个空白之前都会让 `AddTool` **启动即 panic**（"tag must not begin with 'WORD='"）。P1 踩过 `description=…` 前缀；P2 又踩到**中文描述里内嵌 `mode=` / `action=`** 的写法（如 `"mode=diff 必填：…"`）——结论：**描述文本的第一个词不能含等号**，改写为 `"diff 模式必填"`。
2. tag 写纯中文/纯英文描述即可，不要携带任何 `key=value` 形态；
3. 该类错误会被 MCP 回环测试（双 `os.Pipe` + initialize 握手）当场捕获——**工具注册发生在服务器启动时**，不跑握手就发现不了（若上线，`netopslab mcp` 对每个用户都会崩溃）；
4. 输出结构体的 `json.RawMessage` 会被推断为 array-of-integer（对象 detail 会挂 SDK 输出校验）——需要嵌套 JSON 对象时用 `any` + 显式解析。
