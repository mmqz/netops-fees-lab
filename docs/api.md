# NetOps Lab — HTTP API 参考

Base URL：`http://localhost:8080`（`netopslab serve`）。全部响应 UTF-8 JSON（缩进 2 空格）。

**通用约定**

- 错误信封（任何非 2xx）：`{"error":{"code":"…","message":"…"}}`。常用 code：`bad_request` / `validation` / `not_found` / `conflict` / `batch_in_use` / `too_large` / `not_xlsx` / `empty_workbook` / `internal`。
- 分页参数：`page`（1 起，非法值 400）、`pageSize`（默认 20，>100 截断为 100）；列表信封：`{"items":[…],"total":n,"page":p,"pageSize":s}`。
- 请求日志（slog JSON：method/path/status/duration_ms/bytes）走 stderr；panic 一律转 500 信封，不外泄细节。

以下示例均为真实运行输出（导入 `testdata/` 三本工作簿后的库），仅截断。`id` 类字段每次导入都会变化。

## GET /api/health

```json
{
  "status": "ok",
  "version": "v0.1.0",
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
