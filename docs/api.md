# NetOps Lab — HTTP API 参考

Base URL：`http://localhost:8080`（`netopslab serve`）。全部响应 UTF-8 JSON（缩进 2 空格）。

**通用约定**

- 错误信封（任何非 2xx）：`{"error":{"code":"…","message":"…"}}`。常用 code：`bad_request` / `validation` / `not_found` / `conflict` / `batch_in_use` / `too_large` / `not_xlsx` / `empty_workbook` / `internal`；仿真工作台路由（§7）另用：`invalid_request` / `not_latest` / `checkpoint_missing_file` / `no_source_resources` / `op_failed`。
- 分页参数：`page`（1 起，非法值 400）、`pageSize`（默认 20，>100 截断为 100）；列表信封：`{"items":[…],"total":n,"page":p,"pageSize":s}`。
- 请求日志（slog JSON：method/path/status/duration_ms/bytes）走 stderr；panic 一律转 500 信封，不外泄细节。
- `/api/*` 响应一律 `Cache-Control: no-store`（ADR-0006）。

以下示例均为真实运行输出（导入 `testdata/` 三本工作簿后的库），仅截断。`id` 类字段每次导入都会变化；§7 示例取自一次完整的服务器旅程（2026-10-09，v0.2.0）。

## GET /api/health

```json
{
  "status": "ok",
  "version": "v0.2.0",
  "ruleVersion": "1.0.0"
}
```

## POST /api/imports — 上传并分析工作簿

multipart/form-data，字段名 `file`，仅 `.xlsx`，≤10MB（超出 → 413 `too_large`）。返回 **202** + 分析结果（列建议、映射建议）；批次进入 `awaiting_mapping`。

```json
{
  "batchId": "bat_c82e6f8e-d65f-46af-99ea-76bfab2e5dcf",
  "workbookName": "devices.xlsx",
  "sha256": "929524594404687fb709fad9cf59707cf97d8661d55eaacccc353ab25d02473f",
  "sheets": [
    {
      "sheet": "设备清单",
      "rowCount": 30,
      "skippedEmptyRows": 0,
      "columns": [
        {
          "header": "设备编码",
          "index": 1,
          "samples": ["DEV-001", "DEV-002", "DEV-003"],
          "suggestedField": "device_code",
          "confidence": 0.75
        },
        {
          "header": "设备名称",
          "index": 2,
          "samples": ["杭州-核心路由器-001", "宁波-接入路由器-002", "嘉兴-传输设备-003"],
          "suggestedField": "device_name",
          "confidence": 0.75
        }
      ],
      "suggestedType": "device"
    }
  ],
  "suggestedMapping": {
    "sheets": [
      {
        "sheet": "设备清单",
        "action": "import",
        "resourceType": "device",
        "keyColumns": ["device_code"],
        "columnMapping": {
          "city": "城市",
          "device_code": "设备编码",
          "device_name": "设备名称",
          "mgmt_ip": "管理IP",
          "model": "型号",
          "status": "状态",
          "vendor": "厂商"
        }
      }
    ]
  }
}
```

（`columns` 实际含全部列；`samples` 每列至多 3 个非空值。）

## POST /api/imports/{id}/mapping — 确认映射并执行导入

Body = 最终映射模型（可直接回传 `suggestedMapping`）。执行成功返回 **202**；行级失败不中止导入；导入成功后自动触发一次集合级校验，`issues` = 本批次行级问题 + 本次校验产生的集合级问题。

请求（`suggestedMapping` 原样回传）：

```json
{
  "sheets": [
    {
      "sheet": "设备清单",
      "action": "import",
      "resourceType": "device",
      "keyColumns": ["device_code"],
      "columnMapping": {
        "city": "城市",
        "device_code": "设备编码",
        "device_name": "设备名称",
        "mgmt_ip": "管理IP",
        "model": "型号",
        "status": "状态",
        "vendor": "厂商"
      }
    }
  ]
}
```

响应（devices → 27/0/3；ippools → 16/0/4；circuits → 21/0/4）：

```json
{
  "batchId": "bat_c82e6f8e-d65f-46af-99ea-76bfab2e5dcf",
  "status": "completed",
  "summary": {
    "rowsTotal": 30,
    "rowsImported": 27,
    "rowsSkipped": 0,
    "rowsFailed": 3
  },
  "issues": {
    "error": 4,
    "info": 27,
    "warning": 1
  }
}
```

映射校验失败（必填未映射、表头不存在、keyColumns 非自然键等）→ 400 `validation`，批次停留在 `awaiting_mapping`。

## GET /api/batches — 批次列表（倒序）

```json
{
  "items": [
    {
      "id": "bat_b1052d12-0854-441d-96fc-1d4a79363404",
      "workbookName": "circuits.xlsx",
      "sha256": "e333d646defa746ae308980afb13eb9e21eba815b4b6a2524a31bbc3d4257660",
      "status": "completed",
      "rowsTotal": 25,
      "rowsImported": 21,
      "rowsSkipped": 0,
      "rowsFailed": 4,
      "error": "",
      "startedAt": "2026-10-09T12:04:40Z",
      "finishedAt": "2026-10-09T12:04:40Z"
    }
  ],
  "total": 3,
  "page": 1,
  "pageSize": 20
}
```

## GET /api/batches/{id} — 批次详情（含确认映射与每 Sheet 汇总）

```json
{
  "id": "bat_b1052d12-0854-441d-96fc-1d4a79363404",
  "workbookName": "circuits.xlsx",
  "sha256": "e333d646defa746ae308980afb13eb9e21eba815b4b6a2524a31bbc3d4257660",
  "status": "completed",
  "rowsTotal": 25,
  "rowsImported": 21,
  "rowsSkipped": 0,
  "rowsFailed": 4,
  "error": "",
  "startedAt": "2026-10-09T12:04:40Z",
  "finishedAt": "2026-10-09T12:04:40Z",
  "ruleVersion": "1.0.0",
  "mapping": {
    "sheets": [
      {
        "sheet": "电路",
        "action": "import",
        "resourceType": "circuit",
        "keyColumns": ["circuit_code"],
        "columnMapping": {
          "a_end_device": "A端设备",
          "bandwidth_mbps": "带宽Mbps",
          "circuit_code": "电路编号",
          "status": "状态",
          "z_end_device": "Z端设备"
        }
      }
    ]
  },
  "sheets": [
    { "sheet": "电路", "rowsTotal": 25, "rowsImported": 21, "rowsSkipped": 0, "rowsFailed": 4 }
  ]
}
```

## GET /api/batches/{id}/rows?status=&page=&pageSize= — 行级记账

`status` 过滤可选（`imported|skipped|failed`）。示例 `?status=failed`（circuits 批次）：

```json
{
  "items": [
    {
      "id": 72,
      "batchId": "bat_b1052d12-0854-441d-96fc-1d4a79363404",
      "sheet": "电路",
      "rowNumber": 23,
      "status": "failed",
      "entityId": "",
      "ruleCode": "E002",
      "message": "必填字段 circuit.z_end_device 为空"
    },
    {
      "id": 74,
      "batchId": "bat_b1052d12-0854-441d-96fc-1d4a79363404",
      "sheet": "电路",
      "rowNumber": 25,
      "status": "failed",
      "entityId": "",
      "ruleCode": "E004",
      "message": "字段格式无效：circuit.bandwidth_mbps=\"N/A\"（不是十进制整数）"
    }
  ],
  "total": 4,
  "page": 1,
  "pageSize": 20
}
```

`rowNumber` 为 1-based Excel 行号，与问题证据、source_refs 一致。

## DELETE /api/batches/{id} — 回滚批次

仅 `completed` 批次可回滚；批次资源被其他批次关系引用 → **409 `batch_in_use`**。

```json
{
  "batchId": "bat_b1052d12-0854-441d-96fc-1d4a79363404",
  "status": "rolled_back",
  "deleted": {
    "resources": 21,
    "relations": 42,
    "sourceRefs": 63,
    "rowResults": 25,
    "issues": 5
  }
}
```

被引用时（尝试回滚 devices 批次，地址池/电路引用其设备）：

```
HTTP/1.1 409 Conflict
{"error":{"code":"batch_in_use","message":"操作与当前状态冲突"}}
```

## GET /api/resources?type=&q=&status=&page=&pageSize= — 资源检索

`q` 对 natural_key / name / attributes 值做不区分大小写包含匹配；默认按类型分组、每组自然键排序（首页 circuit-first 为排序副作用）。示例 `?pageSize=2`：

```json
{
  "items": [
    {
      "id": "res_c2865a17-9848-4982-a6f5-ad30c0eb58ea",
      "type": "circuit",
      "naturalKey": "CKT-001",
      "name": "CKT-001",
      "status": "在网",
      "attributes": {
        "a_end_device": "DEV-001",
        "bandwidth_mbps": "1000",
        "z_end_device": "DEV-002"
      },
      "firstBatchId": "bat_b1052d12-0854-441d-96fc-1d4a79363404",
      "updatedAt": "2026-10-09T12:04:40Z"
    }
  ],
  "total": 64,
  "page": 1,
  "pageSize": 2
}
```

## GET /api/resources/{id} — 资源详情（+ 来源引用 + 关系计数）

```json
{
  "id": "res_c2865a17-9848-4982-a6f5-ad30c0eb58ea",
  "type": "circuit",
  "naturalKey": "CKT-001",
  "name": "CKT-001",
  "status": "在网",
  "attributes": {
    "a_end_device": "DEV-001",
    "bandwidth_mbps": "1000",
    "z_end_device": "DEV-002"
  },
  "firstBatchId": "bat_b1052d12-0854-441d-96fc-1d4a79363404",
  "updatedAt": "2026-10-09T12:04:40Z",
  "sourceRefs": [
    {
      "id": "src_10a8e2f8-950c-4996-827f-bcaec655d957",
      "batchId": "bat_b1052d12-0854-441d-96fc-1d4a79363404",
      "workbook": "circuits.xlsx",
      "sheet": "电路",
      "rowNumber": 2,
      "raw": {
        "A端设备": "DEV-001",
        "Z端设备": "DEV-002",
        "带宽Mbps": "1000",
        "状态": "在网",
        "电路编号": "CKT-001"
      }
    }
  ],
  "relationCount": 2
}
```

`raw` 即原始 Excel 行（表头→值）。

## GET /api/resources/{id}/relations?depth=1|2 — 关系子图（BFS）

`depth` 默认 1，最大 2。返回以该资源为中心的邻域子图（`nodes` 不含 center；`edges` 全部落在 visited 集合内）。示例 `?depth=2`（CKT-001 中心）：

```json
{
  "center": { "id": "res_c286…", "type": "circuit", "naturalKey": "CKT-001", "name": "CKT-001" },
  "nodes": [
    { "id": "res_e59e…", "type": "circuit", "naturalKey": "CKT-002", "name": "CKT-002" },
    { "id": "res_101d…", "type": "device", "naturalKey": "DEV-001", "name": "杭州-核心路由器-001" },
    { "id": "res_f3fe…", "type": "device", "naturalKey": "DEV-002", "name": "宁波-接入路由器-002" },
    { "id": "res_85ac…", "type": "device", "naturalKey": "DEV-003", "name": "嘉兴-传输设备-003" },
    { "id": "res_784e…", "type": "ip_pool", "naturalKey": "POOL-01", "name": "POOL-01" }
  ],
  "edges": [
    {
      "id": "rel_f789…",
      "relationType": "a_end",
      "fromId": "res_c286…",
      "toId": "res_101d…"
    }
  ]
}
```

遍历无向（a_end 可从设备侧走回电路）；节点按 `(type, natural_key)` 排序，输出确定。

## GET /api/stats — 全库统计

```json
{
  "totalResources": 64,
  "byType": { "circuit": 21, "device": 27, "ip_pool": 16 },
  "byStatus": { "停用": 2, "在建": 1, "在网": 43, "退网": 1 },
  "byRelationType": { "a_end": 21, "assigned_to": 15, "z_end": 21 },
  "issuesBySeverity": { "error": 13, "info": 1, "warning": 3 },
  "latestBatchId": "bat_b1052d12-0854-441d-96fc-1d4a79363404"
}
```

## GET /api/issues?batchId=&severity=&ruleCode=&page=&pageSize= — 问题清单

条目带**解析后的来源**（`source`）或 `applicationGenerated: true`。示例 `?severity=error&pageSize=1`：

```json
{
  "items": [
    {
      "id": "iss_8206e58c-5c50-4df1-b210-6cb5ad30ea65",
      "ruleCode": "E001",
      "severity": "error",
      "message": "重复键 device/DEV-012：与本批次第 13 行冲突",
      "source": {
        "workbook": "devices.xlsx",
        "sheet": "设备清单",
        "rowNumber": 27
      },
      "applicationGenerated": false,
      "evidence": {
        "conflicting_row_number": "13",
        "conflicting_sheet": "设备清单",
        "existing_entity_id": "res_4e0f3540-1ce1-4884-bbc1-983a6c21c3b6",
        "natural_key": "DEV-012",
        "row_number": "27",
        "sheet": "设备清单",
        "type": "device"
      }
    }
  ],
  "total": 13,
  "page": 1,
  "pageSize": 1
}
```

集合级问题示例（`applicationGenerated: true`，无 source）：

```json
{
  "ruleCode": "E006",
  "severity": "error",
  "message": "地址 10.10.10.5 被 2 个资源重复使用：DEV-026、DEV-027",
  "entityKind": "resource",
  "entityId": "res_747b…",
  "entityKey": "DEV-026",
  "applicationGenerated": true,
  "evidence": {
    "address": "10.10.10.5",
    "resources": "DEV-026=10.10.10.5, DEV-027=10.10.10.5"
  }
}
```

## POST /api/validate — 重算集合级规则

可选 body `{"batchId":"…"}`（限定口径：该批次行级问题 + 全部集合级问题；引用未知批次 → 404）。全局口径示例：

```json
{
  "issues": { "error": 13, "info": 1, "warning": 3 },
  "score": 32
}
```

评分 = `100 − 5×error − 1×warning`，下限 0。

## GET /api/report?batchId=&format=html|json — 质量报告

`format` 默认 `html`（`Content-Type: text/html; charset=utf-8`，独立文档：内联 CSS、卡片、分节问题表、页脚免责声明）。`format=json` 示例（全局口径，截断）：

```json
{
  "generatedAt": "2026-10-09T12:05:42.762297821Z",
  "ruleVersion": "1.0.0",
  "scope": "global",
  "summary": {
    "totalResources": 27,
    "byType": { "device": 27 },
    "totalRelations": 0,
    "byRelationType": {},
    "issuesBySeverity": { "error": 4, "info": 27, "warning": 1 },
    "qualityScore": 79,
    "batches": [
      {
        "batchId": "bat_c82e…",
        "workbook": "devices.xlsx",
        "status": "completed",
        "rowsTotal": 30,
        "rowsImported": 27,
        "rowsSkipped": 0,
        "rowsFailed": 3,
        "completenessPct": 90
      }
    ]
  },
  "issues": [ "…同 /api/issues 条目结构…" ]
}
```

（上例为仅导入 devices 后的库；三本全导后为 64 资源、E13/W3/I1、评分 32。批次口径 `?batchId=…` 时 `scope` 为 `batch:<id>`，`batches[]` 仅含该批次，问题 = 该批次行级 + 全部集合级。）

## GET /api/topology/check?fromDevice=&toDevice=&maxHops= — 可达性检查

设备用 `device_code` 标识；`maxHops` 默认 2、最大 5。示例 `?fromDevice=DEV-001&toDevice=DEV-003`：

```json
{
  "reachable": true,
  "hops": [
    { "deviceId": "res_101d…", "deviceCode": "DEV-001" },
    { "deviceId": "res_f3fe…", "deviceCode": "DEV-002" },
    { "deviceId": "res_85ac…", "deviceCode": "DEV-003" }
  ],
  "pathLength": 2
}
```

不可达：`{"reachable":false,"hops":[],"pathLength":0}`（`hops` 为空数组）；未知设备码 → 404。

## 静态资源与 SPA

- `GET /` 与一切非 `/api` 路径 → 嵌入式 React SPA（`go:embed`）；未知子路径回退 `index.html`（客户端路由）。
- 未知 `/api/*` 路径 → JSON 404 信封（绝不回退 SPA）。
- 方法不匹配（如 `POST /api/health`）→ stdlib 405（text/plain）。

## 典型调用序列（等价于 scripts/demo.sh）

```
POST /api/imports           (file=devices.xlsx)    → 202 analysis
POST /api/imports/{id}/mapping  (suggestedMapping)  → 202 27/0/3
… ippools.xlsx（16/0/4）… circuits.xlsx（21/0/4）…
POST /api/validate                                  → {E13,W3,I1}, score 32
GET  /api/report?format=html                        → HTML 报告
GET  /api/stats                                     → 64 资源 / 57 关系
GET  /api/topology/check?fromDevice=DEV-001&toDevice=DEV-003 → 路径长 2
```

---

# 仿真工作台 API（Phase 2 §7，v0.2.0）

以下路由操作**仿真工作区**（从导入批次物化的虚拟拓扑）。全部示例为**真实服务器捕获**（`netopslab serve` 于 127.0.0.1:8095，全新库 + CLI 导入三本工作簿，2026-10-09），uuid 截断以 `…` 表示；cs-/run-/cp-/vd-/if-/ln- 序号在同一旅程内单调递增，故具有跨示例的可读性。

本节路由的错误信封与 P1 同构（`{"error":{"code","message"}}`），但消息为**英文**（§7 约定）；错误码 → 状态：`invalid_request` 400 · `not_found` 404 · `conflict` / `not_latest` / `checkpoint_missing_file` 409 · `no_source_resources` / `op_failed` 422。`{"checks":null}` 出现在 `runChecks=false` 的 ops/undo 响应中。

## GET /api/workspaces?page=&pageSize= — 工作区列表

```json
{
  "items": [
    {
      "id": "7fe21f56-…",
      "name": "演示工作台二",
      "description": "",
      "deviceCount": 27,
      "interfaceCount": 40,
      "linkCount": 20,
      "upDeviceCount": 23,
      "changesetCount": 0,
      "checkpointCount": 0,
      "lastRunId": "run-6",
      "lastRunScore": 95,
      "createdAt": "2026-10-09T17:09:56Z"
    }
  ],
  "total": 1,
  "page": 1,
  "pageSize": 20
}
```

（`lastRunId` / `lastRunScore` 在首次校验运行前为 `null`。）

## POST /api/workspaces — 物化工作区（201）

请求体 `{name, description?, sourceBatchIds?}`（批次缺省 = 全部 `completed` 批次，按导入顺序）。物化规则、端口分配见 [simulation.md](simulation.md) §1.1。

```json
{
  "workspaceId": "bb72d10a-…",
  "name": "演示工作台",
  "sourceBatchIds": [
    "bat_d8f67408-…",
    "bat_deeb22d2-…",
    "bat_cfe93a31-…"
  ],
  "counts": {
    "devices": 27,
    "interfaces": 40,
    "links": 20
  },
  "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa"
}
```

错误：名称重复 → `409 {"error":{"code":"conflict","message":"operation conflicts with the current state"}}`；空名称 → `400 invalid_request "name must not be empty"`；所选批次无设备资源 → `422 no_source_resources`；未知批次 → 404。

## GET /api/workspaces/{id} — 工作区详情

```json
{
  "id": "bb72d10a-…",
  "name": "演示工作台",
  "description": "docs api.md 采集",
  "status": "active",
  "sourceBatchIds": ["bat_d8f67408-…", "bat_deeb22d2-…", "bat_cfe93a31-…"],
  "counts": {
    "devices": 27,
    "upDevices": 23,
    "downDevices": 4,
    "interfaces": 40,
    "upInterfaces": 40,
    "links": 20,
    "upLinks": 20,
    "changesets": 0,
    "checkpoints": 0
  },
  "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
  "latestChangeset": null,
  "latestRun": {
    "runId": "run-6",
    "score": 95,
    "counts": { "error": 0, "warning": 5, "info": 36 }
  },
  "createdAt": "2026-10-09T17:09:56Z",
  "updatedAt": "2026-10-09T17:09:56Z"
}
```

（`latestChangeset` / `latestRun` 在无变更集/无运行时为 `null`，此处 `latestRun` 取自另一已运行的详情。未知工作区 → 404 `not_found`。）

## GET /api/workspaces/{id}/topology — 拓扑（节点按 code、接口按 name、边按数值 id）

```json
{
  "workspaceId": "bb72d10a-…",
  "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
  "nodes": [
    {
      "id": "vd-1",
      "code": "DEV-001",
      "name": "杭州-核心路由器-001",
      "status": "up",
      "interfaces": [
        { "id": "if-1", "name": "eth0", "status": "up", "ipv4Addr": "", "ipv4Prefix": -1 }
      ]
    },
    {
      "id": "vd-2",
      "code": "DEV-002",
      "name": "宁波-接入路由器-002",
      "status": "up",
      "interfaces": [
        { "id": "if-2", "name": "eth0", "status": "up", "ipv4Addr": "", "ipv4Prefix": -1 },
        { "id": "if-3", "name": "eth1", "status": "up", "ipv4Addr": "", "ipv4Prefix": -1 }
      ]
    }
  ],
  "edges": [
    {
      "id": "ln-1",
      "aInterfaceId": "if-1",
      "aDeviceId": "vd-1",
      "aDeviceCode": "DEV-001",
      "zInterfaceId": "if-2",
      "zDeviceId": "vd-2",
      "zDeviceCode": "DEV-002",
      "status": "up",
      "label": "CKT-001"
    }
  ]
}
```

（27 节点 / 20 边，此处截断到前 2 节点与第 1 边；`ipv4Addr:""`、`ipv4Prefix:-1` 表示未配置——与状态哈希的 `null` 表示法故意不同，§7 vs §3。）

## POST /api/workspaces/{id}/topology/path — 连通性查询

请求体 `{fromDevice, toDevice}`（**设备编码**）。可达示例（DEV-001→DEV-003，2 跳）：

```json
{
  "reachable": true,
  "hops": [
    { "deviceId": "vd-1", "code": "DEV-001" },
    { "deviceId": "vd-2", "code": "DEV-002" },
    { "deviceId": "vd-3", "code": "DEV-003" }
  ],
  "pathLength": 2
}
```

不可达（注入 ln-1 down 之后）：`{"reachable":false,"hops":null,"pathLength":0}`；同编码 → `{"reachable":true,"hops":[{…self}],"pathLength":0}`。未知设备编码 → 404；缺字段 → `400 {"error":{"code":"invalid_request","message":"fromDevice and toDevice are required"}}`。

## POST /api/workspaces/{id}/ops — 应用操作（变更集）

请求体 `{"ops":[…], "description?", "runChecks?"(默认 true)}`——操作目录（10 个前向操作）见 [simulation.md](simulation.md) §2。成功（故障注入示例：同地址两接口 + 链路 down，自动校验附带）：

```json
{
  "changesetId": "cs-1",
  "trigger": "user",
  "opsApplied": 3,
  "workspaceHash": "005c7b2066f4b82e38baef75dbf30f286ad964754fb0d1dbbf924e19646b5cc3",
  "checks": {
    "runId": "run-2",
    "counts": { "error": 1, "warning": 6, "info": 34 },
    "score": 89
  }
}
```

（`runChecks:false` 时 `"checks": null` 且 `workspaceHash` 由哈希读取填充。）

语义失败 → **422 op_failed**（状态零污染，失败变更集留档）：

```
{"error":{"code":"op_failed","message":"op 0 (set_link_status): link not found: ln-999 [changeset cs-2 recorded as failed]"}}
```

畸形负载 → 400 `invalid_request`。真实示例（restore_* 引擎内部操作码 / 未知操作码）：

```
{"error":{"code":"invalid_request","message":"sim: op 0: \"restore_link\": not a forward op code (restore_* ops are engine-internal): domain: invalid operation"}}
{"error":{"code":"invalid_request","message":"sim: op 0: \"reboot_device\": not a forward op code (restore_* ops are engine-internal): domain: invalid operation"}}
```

（字段拼写错误、多余字段、坏类型同走 400——`DisallowUnknownFields` + 严格字段表。）

## POST /api/workspaces/{id}/checks/run — 执行校验

```json
{
  "runId": "run-1",
  "trigger": "manual",
  "counts": { "error": 0, "warning": 5, "info": 36 },
  "score": 95,
  "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
  "durationMs": 0
}
```

## GET /api/workspaces/{id}/checks?page=&pageSize= — 运行列表（倒序）

```json
{
  "items": [
    {
      "runId": "run-2",
      "changesetId": "cs-1",
      "trigger": "auto_after_ops",
      "startedAt": "2026-10-09T17:09:08Z",
      "counts": { "error": 1, "warning": 6, "info": 34 },
      "score": 89
    },
    {
      "runId": "run-1",
      "changesetId": null,
      "trigger": "manual",
      "startedAt": "2026-10-09T17:09:07Z",
      "counts": { "error": 0, "warning": 5, "info": 36 },
      "score": 95
    }
  ],
  "total": 2,
  "page": 1,
  "pageSize": 20
}
```

（手动运行的 `changesetId` 为 `null`。）

## GET /api/workspaces/{id}/checks/{runId} — 运行详情（发现行在前 + 5 条基线）

```json
{
  "runId": "run-2",
  "workspaceId": "bb72d10a-…",
  "changesetId": "cs-1",
  "trigger": "auto_after_ops",
  "ruleVersion": "1.0.0",
  "startedAt": "2026-10-09T17:09:08Z",
  "durationMs": 0,
  "score": 89,
  "counts": { "error": 1, "warning": 6, "info": 34 },
  "results": [
    {
      "seq": 1,
      "checkCode": "CHK-001",
      "severity": "error",
      "passed": false,
      "entityKind": "workspace",
      "entityId": "bb72d10a-…",
      "message": "duplicate host address 10.99.0.1 used by 2 interfaces",
      "detail": {
        "addr": "10.99.0.1",
        "interfaces": [
          { "interfaceId": "if-1", "deviceId": "vd-1", "deviceCode": "DEV-001", "name": "eth0" },
          { "interfaceId": "if-3", "deviceId": "vd-2", "deviceCode": "DEV-002", "name": "eth1" }
        ]
      }
    },
    {
      "seq": 2,
      "checkCode": "CHK-003",
      "severity": "warning",
      "passed": false,
      "entityKind": "device",
      "entityId": "vd-1",
      "message": "device DEV-001 is isolated (no up links)",
      "detail": { "deviceId": "vd-1", "code": "DEV-001", "totalLinks": 1, "upLinks": 0 }
    },
    { "seq": 3, "checkCode": "CHK-003", "severity": "warning", "passed": false, "entityKind": "device", "entityId": "vd-22", "message": "device DEV-022 is isolated (no up links)", "detail": { "deviceId": "vd-22", "code": "DEV-022", "totalLinks": 0, "upLinks": 0 } },
    "…（共 41 条发现行：CHK-001×1、CHK-003×6、CHK-004×2、CHK-005×34）…",
    { "seq": 42, "checkCode": "CHK-001", "severity": "error", "passed": true, "entityKind": "", "entityId": "", "message": "ok", "detail": {} },
    { "seq": 43, "checkCode": "CHK-002", "severity": "error", "passed": true, "entityKind": "", "entityId": "", "message": "ok", "detail": {} },
    { "seq": 44, "checkCode": "CHK-003", "severity": "warning", "passed": true, "entityKind": "", "entityId": "", "message": "ok", "detail": {} },
    { "seq": 45, "checkCode": "CHK-004", "severity": "info", "passed": true, "entityKind": "", "entityId": "", "message": "ok", "detail": {} },
    { "seq": 46, "checkCode": "CHK-005", "severity": "info", "passed": true, "entityKind": "", "entityId": "", "message": "ok", "detail": {} }
  ]
}
```

（`detail` 是**解析后的 JSON 对象**，结构与 check 目录一一对应 [simulation.md](simulation.md) §3。）

## GET /api/workspaces/{id}/checks/{runId}/diff?compareTo=<runId2> — 前后对比

```json
{
  "from": { "runId": "run-1", "startedAt": "2026-10-09T17:09:07Z", "score": 95, "counts": { "error": 0, "warning": 5, "info": 36 } },
  "to": { "runId": "run-2", "startedAt": "2026-10-09T17:09:08Z", "score": 89, "counts": { "error": 1, "warning": 6, "info": 34 } },
  "findingsAdded": [
    {
      "checkCode": "CHK-001",
      "severity": "error",
      "message": "duplicate host address 10.99.0.1 used by 2 interfaces",
      "detail": { "addr": "10.99.0.1", "interfaces": [ { "interfaceId": "if-1", "deviceId": "vd-1", "deviceCode": "DEV-001", "name": "eth0" }, { "interfaceId": "if-3", "deviceId": "vd-2", "deviceCode": "DEV-002", "name": "eth1" } ] }
    },
    {
      "checkCode": "CHK-003",
      "severity": "warning",
      "message": "device DEV-001 is isolated (no up links)",
      "detail": { "deviceId": "vd-1", "code": "DEV-001", "totalLinks": 1, "upLinks": 0 }
    }
  ],
  "findingsRemoved": [
    {
      "checkCode": "CHK-005",
      "severity": "info",
      "message": "interface DEV-001/eth0 has no IPv4 address",
      "detail": { "interfaceId": "if-1", "deviceId": "vd-1", "deviceCode": "DEV-001", "name": "eth0" }
    },
    { "checkCode": "CHK-005", "severity": "info", "message": "interface DEV-002/eth1 has no IPv4 address", "detail": { "interfaceId": "if-3", "deviceId": "vd-2", "deviceCode": "DEV-002", "name": "eth1" } }
  ],
  "connectivityLost": [
    { "fromDevice": "DEV-001", "toDevice": "DEV-002" },
    { "fromDevice": "DEV-001", "toDevice": "DEV-003" },
    { "fromDevice": "DEV-001", "toDevice": "DEV-004" },
    "…（共 20 对：DEV-001 与其余全部设备）…"
  ],
  "connectivityGained": [],
  "scoreDelta": -6
}
```

（匹配键与连通性重算规则见 [simulation.md](simulation.md) §5；空数组渲染 `[]` 而非 null。同一运行对比 → `400 {"error":{"code":"invalid_request","message":"cannot diff a run against itself; pass a different compareTo run"}}`；跨工作区运行 → 404。）

## GET /api/workspaces/{id}/changesets?page=&pageSize= — 变更集列表（倒序）

```json
{
  "items": [
    { "id": "cs-2", "description": "", "trigger": "user", "status": "failed", "opsApplied": 0, "createdAt": "2026-10-09T17:09:08Z", "revertedAt": null },
    { "id": "cs-1", "description": "docs 示例：地址冲突 + 链路中断", "trigger": "user", "status": "applied", "opsApplied": 3, "createdAt": "2026-10-09T17:09:08Z", "revertedAt": null }
  ],
  "total": 2,
  "page": 1,
  "pageSize": 20
}
```

（`trigger` ∈ `user | undo | checkpoint_restore`；`status` ∈ `applied | reverted | failed`。）

## POST /api/workspaces/{id}/undo — 撤销变更集（LIFO）

请求体 `{"changesetId?"}`（缺省 = 最新已应用）。撤销后哈希精确回到物化基线：

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

撤销非最新已应用（有更晚的已应用前向变更集）→ 真实 409：

```
{"error":{"code":"not_latest","message":"changeset is not the latest applied changeset of the workspace (undo is LIFO)"}}
```

（撤销已 reverted / failed 的目标 → 409 `conflict`（普通）；未知变更集 → 404。）

## POST /api/workspaces/{id}/checkpoints — 创建检查点（201）

请求体 `{"label"}`（必填）。

```json
{
  "checkpointId": "cp-1",
  "label": "演示基线",
  "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
  "createdAt": "2026-10-09T17:09:08.121473516Z"
}
```

（空 label → `400 invalid_request "label must not be empty"`。快照文件在 `<dbdir>/checkpoints/<workspaceId>/<cpId>.db`，Online Backup API 写入。）

## GET /api/workspaces/{id}/checkpoints — 检查点列表（倒序）

```json
{
  "items": [
    {
      "checkpointId": "cp-1",
      "label": "演示基线",
      "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
      "createdAt": "2026-10-09T17:09:08Z"
    }
  ],
  "total": 1
}
```

## POST /api/workspaces/{id}/checkpoints/{cpId}/restore — 恢复到检查点

先执行破坏性操作（`delete_device vd-1`，变更集 cs-4）后恢复——恢复本身是一个前向操作变更集（删除的设备/接口/链路按快照重建）：

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

重复恢复（当前状态已是检查点状态）→ no-op：

```json
{
  "checkpointId": "cp-1",
  "label": "演示基线",
  "changesetId": null,
  "note": "already at checkpoint state",
  "opsApplied": 0,
  "restoredToHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
  "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
  "checks": null
}
```

（快照文件被删 → `409 checkpoint_missing_file "checkpoint snapshot file is missing on disk"`；未知检查点 → 404。）

## GET /api/lab/report?workspaceId=&format=html|json — 仿真工作台报告

`format` 默认 `html`（`Content-Type: text/html; charset=utf-8`，独立文档：P1 报告视觉风格，设备表 + 按严重级别分节的发现 + 变更集表（中文触发标签：用户操作/撤销/检查点恢复）+ 检查点表 + 页脚免责声明）。`format=json` 示例（旅程末尾，已恢复基线，截断）：

```json
{
  "generatedAt": "2026-10-09T17:09:08.189409423Z",
  "engineVersion": "v0.2.0",
  "workspace": {
    "id": "bb72d10a-…",
    "name": "演示工作台",
    "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa",
    "counts": { "devices": 27, "upDevices": 23, "downDevices": 4, "interfaces": 40, "links": 20, "upLinks": 20, "changesets": 5, "checkpoints": 1 }
  },
  "score": 95,
  "checks": {
    "counts": { "error": 0, "warning": 5, "info": 36 },
    "results": [
      { "checkCode": "CHK-003", "severity": "warning", "message": "device DEV-022 is isolated (no up links)", "detail": { "deviceId": "vd-22", "code": "DEV-022", "totalLinks": 0, "upLinks": 0 } },
      "…（仅发现行，共 41 条）…"
    ]
  },
  "changesets": [
    { "id": "cs-5", "description": "restore checkpoint cp-1 \"演示基线\"", "trigger": "checkpoint_restore", "status": "applied", "opsApplied": 3, "createdAt": "2026-10-09T17:09:08Z", "revertedAt": null },
    { "id": "cs-4", "description": "docs 示例：删除设备", "trigger": "user", "status": "applied", "opsApplied": 1, "createdAt": "2026-10-09T17:09:08Z", "revertedAt": null },
    { "id": "cs-3", "description": "undo of cs-1", "trigger": "undo", "status": "applied", "opsApplied": 3, "createdAt": "2026-10-09T17:09:08Z", "revertedAt": null },
    { "id": "cs-2", "description": "", "trigger": "user", "status": "failed", "opsApplied": 0, "createdAt": "2026-10-09T17:09:08Z", "revertedAt": null },
    { "id": "cs-1", "description": "docs 示例：地址冲突 + 链路中断", "trigger": "user", "status": "reverted", "opsApplied": 3, "createdAt": "2026-10-09T17:09:08Z", "revertedAt": "2026-10-09T17:09:08Z" }
  ],
  "checkpoints": [
    { "checkpointId": "cp-1", "label": "演示基线", "workspaceHash": "aea27501fa592c3fdf54675a75ef8e0339e296adc8fb70bdb3cb6ecf6081d7fa", "createdAt": "2026-10-09T17:09:08Z" }
  ],
  "disclaimer": "本报告基于合成测试数据生成，仅用于工程方法验证，不代表真实网络运行情况。"
}
```

（无运行的工作区 `score` 为 `null`（绝不编造评分）；变更集取最近 10 条倒序。未知 workspaceId → 404；非法 format → 400。）

## DELETE /api/workspaces/{id} — 删除工作区（级联 + 清理检查点目录）

```json
{
  "deleted": true,
  "removed": {
    "devices": 27,
    "interfaces": 40,
    "links": 20,
    "changesets": 5,
    "checkRuns": 5,
    "checkpoints": 1
  }
}
```

## 典型调用序列（等价于 scripts/demo-lab.sh 的 API 版）

```
POST /api/workspaces                       → 201 {27/40/20, hash aea27501…}
POST /api/workspaces/{id}/checks/run       → run-1 {0,5,36} 95
POST /api/workspaces/{id}/topology/path    → DEV-001→DEV-003 2 跳
POST /api/workspaces/{id}/ops              → cs-1 + 自动 run-2 {1,6,34} 89
POST /api/workspaces/{id}/topology/path    → 不可达
GET  /api/workspaces/{id}/checks/run-1/diff?compareTo=run-2 → −6 / +[CHK-001,CHK-003] / 丢失 20 对
POST /api/workspaces/{id}/undo             → cs-3，哈希回到 aea27501…
POST /api/workspaces/{id}/checkpoints      → 201 cp-1
POST /api/workspaces/{id}/ops (delete)     → cs-4
POST /api/workspaces/{id}/checkpoints/cp-1/restore → cs-5，restoredToHash == 当前哈希
POST /api/workspaces/{id}/checkpoints/cp-1/restore → no-op（changesetId null）
GET  /api/lab/report?workspaceId=…         → HTML/JSON 报告
```
