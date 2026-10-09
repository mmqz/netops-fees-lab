# NetOps Lab — TeleAgent 集成指南（MCP 注册 + CLI 回退）

> ⚠️ **诚实声明**：部署态 TeleAgent 的 MCP 调用**未在本开发环境验证**（环境内没有 TeleAgent 实例）。已验证的是进程内 MCP stdio 回环（go-sdk 客户端 × `mcpserver.Run`，`TestMCPServerResourceSearch` / `TestMCPToolRegistry`）。**接入部署态时请按本文操作，并把结果（成功/报错/协议差异）反馈给工程组。** 在验证完成前，建议优先使用 CLI 回退路径（§3）——它与 MCP 共用同一服务层，输出同为结构化 JSON。

## 1. 前置

- 二进制：`bin/netopslab`（`make build` 产出；单文件，含前端与全部功能，无运行时依赖）
- 数据库：一个 SQLite 文件（如 `/opt/netops/data/netops.db`；不存在会自动建库/迁移）
- 导入数据：CLI `import`（§3）或页面（serve 模式）先行完成；MCP 工具是**只读**的，不能导入

## 2. MCP 注册（stdio）

### 2.1 启动命令

```bash
netopslab mcp --db /opt/netops/data/netops.db
# 或用环境变量（--db 优先级高于 NETOPS_DB）：
NETOPS_DB=/opt/netops/data/netops.db netopslab mcp
```

服务器以 **stdio**（stdin/stdout）承载 MCP 协议（newline-delimited JSON-RPC 2.0）；日志与诊断走 **stderr**——宿主切勿把 stderr 混入协议流。

### 2.2 服务器身份

- name：`netops-lab`
- version：`v0.1.0`
- 工具清单（`tools/list`）：**恰好一个** `resource_search`（无 SQL/Shell/SSH/文件/写工具）

### 2.3 工具 schema

**输入**（全部可选）：

| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `q` | string | — | 关键词：natural_key/name/attributes 不区分大小写包含匹配 |
| `type` | string | `device` \| `ip_pool` \| `circuit` | 类型过滤；非法值 → 工具错误（IsError=true） |
| `limit` | int | ≥0，缺省 20，上限 100 | 返回条数 |
| `offset` | int | ≥0，缺省 0 | 翻页偏移 |

调用示例：

```json
{ "q": "DEV-0", "type": "device", "limit": 10 }
```

**输出**（结构化结果，`applicationGenerated` 顶层恒为 false）：

```json
{
  "total": 27,
  "items": [
    {
      "id": "res_101da4b6-e483-4f38-bb3a-be5484dfa224",
      "type": "device",
      "naturalKey": "DEV-001",
      "name": "杭州-核心路由器-001",
      "status": "在网",
      "attributes": {
        "city": "杭州",
        "device_name": "杭州-核心路由器-001",
        "mgmt_ip": "10.10.1.1",
        "model": "NE40E",
        "vendor": "华为"
      },
      "source": {
        "workbook": "devices.xlsx",
        "sheet": "设备清单",
        "rowNumber": 2
      },
      "applicationGenerated": false
    }
  ],
  "applicationGenerated": false
}
```

- 每个条目携带 `source`（创建该资源的工作簿/Sheet/行号）或 `applicationGenerated: true`（无来源记录时）；
- 错误语义：参数非法 → MCP **tool error**（`IsError: true`，如 `type 必须是 device、ip_pool 或 circuit，收到 "xxx"`）。

### 2.4 宿主侧注册要点（待部署验证）

- 以 stdio 子进程方式拉起 `netopslab mcp`；长驻即可（数据库按需打开，导入由外部完成后工具立即可查）；
- 注意 stderr 分流；进程退出码非 0 视为服务器启动失败；
- **再次强调**：本节为按契约撰写的操作指引，未经部署态验证——接入时如遇握手/schema 差异，请反馈 `docs/verified-capabilities.md` 的未验证清单。

## 3. CLI 回退（Skills 解析结构化 JSON）

所有子命令成功时 stdout 输出**恰好一个 JSON 对象**（缩进 2 空格）；失败输出 `{"error":{"code","message"}}`；退出码 **0 成功 / 1 失败 / 2 用法错误**。适合 Skills 用 shell 调用 + JSON 解析。

### 3.1 常用命令与真实输出

`validate`（重算集合级规则 + 评分）：

```bash
netopslab validate --db /path/netops.db
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

`query stats`：

```bash
netopslab query --db /path/netops.db stats
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

`query resources`（检索；`--q/--type/--status/--page/--pageSize`）：

```bash
netopslab query --db /path/netops.db resources --q DEV-001 --type device --pageSize 3
```
```json
{
  "items": [
    {
      "id": "res_be42ace9-0897-4887-b369-e165bb761d70",
      "type": "device",
      "naturalKey": "DEV-001",
      "name": "杭州-核心路由器-001",
      "status": "在网",
      "attributes": {
        "city": "杭州",
        "mgmt_ip": "10.10.1.1",
        "model": "NE40E",
        "vendor": "华为"
      },
      "firstBatchId": "bat_ecf7ba6f-…",
      "updatedAt": "2026-10-09T12:04:40Z"
    }
  ],
  "total": 3,
  "page": 1,
  "pageSize": 3
}
```

`query relations`（1/2 跳邻域；`--id` 必填）：

```bash
netopslab query --db /path/netops.db relations --id <resourceId> --depth 1
```
```json
{
  "center": { "id": "res_be42…", "type": "device", "naturalKey": "DEV-001", "name": "杭州-核心路由器-001" },
  "nodes": [
    { "id": "res_b97d…", "type": "circuit", "naturalKey": "CKT-001", "name": "CKT-001" },
    { "id": "res_0de2…", "type": "device", "naturalKey": "DEV-002", "name": "宁波-接入路由器-002" },
    { "id": "res_698e…", "type": "ip_pool", "naturalKey": "POOL-01", "name": "POOL-01" }
  ],
  "edges": [
    { "id": "rel_…", "relationType": "a_end", "fromId": "res_b97d…", "toId": "res_be42…" }
  ]
}
```

`analyze`（**离线**：解析 + 映射建议，不写库）：

```bash
netopslab analyze --file /path/devices.xlsx
# → { "batchId": "", "workbookName": "devices.xlsx", "sha256": "…",
#     "sheets": [ { "sheet": "设备清单", "rowCount": 30, "skippedEmptyRows": 0,
#       "columns": [ { "header": "设备编码", "index": 1, "samples": ["DEV-001", …],
#                      "suggestedField": "device_code", "confidence": 0.75 }, … ],
#       "suggestedType": "device" } ],
#     "suggestedMapping": { "sheets": [ … ] } }
```

`import`（映射文件必填，schema 同 HTTP API 的 mapping 模型；导入后自动校验）：

```bash
netopslab import --file /path/devices.xlsx --map /path/map.json --db /path/netops.db
```
```json
{
  "batchId": "bat_ecf7ba6f-a8b8-4d33-a9d4-42f42d45124b",
  "status": "completed",
  "summary": {
    "rowsTotal": 30,
    "rowsImported": 27,
    "rowsSkipped": 0,
    "rowsFailed": 3
  },
  "issues": { "error": 4, "info": 27, "warning": 1 }
}
```

`report`（`--format html|json`，默认 json；`--out` 写文件，缺省 stdout）：

```bash
netopslab report --db /path/netops.db --format html --out report.html
```

### 3.2 建议的 Skill 模式

1. `analyze` 拿 `suggestedMapping` →（人工或规则确认后）落成 map.json；
2. `import`（devices → ippools → circuits 顺序，引用先落库）；
3. `validate` / `report` 取评分与问题；
4. `query resources|relations` 供查询型任务；
5. 一切写操作仅 `import`（Excel 数据），**没有设备写命令**——生产操作不在工具面内。

## 4. 安全边界（供宿主审计）

- MCP 工具面 = `["resource_search"]`（只读）；
- CLI 无 SQL/Shell/SSH 子命令；`import` 只消费 xlsx + 映射 JSON；
- 无生产网络凭证、无真实数据；仓库全部数据为合成测试数据。
