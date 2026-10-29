# NetOps Fees Lab — HTTP API 参考

Base URL：`http://localhost:8080`（`netopsfees serve`）。全部响应 UTF-8 JSON（缩进 2 空格）。

**通用约定**

- 错误信封（任何非 2xx）：`{"error":{"code":"…","message":"…"}}`。常用 code：`bad_request` / `validation` / `not_found` / `conflict` / `batch_in_use` / `too_large` / `not_xlsx` / `empty_workbook` / `workbook_too_large` / `internal`；写门禁（见下节）另用 `unauthorized`（401，缺/错令牌）与 `forbidden`（403，申报级令牌访问管理级路由）；费用域另用 `fee_import_failed`（422，费用文件语义错误）与 `engine_not_built`（501，默认构建未编译 DuckDB 分析引擎）；属性变更确认路由（W005）另用 `attribute_conflict`（409，apply 时旧值漂移，见 §POST /api/attribute-changes/{id}/apply）与 `ref_target_missing`（409，apply 的 ref 字段新值解析不到被引用类型资源，见 §POST /api/attribute-changes/{id}/apply）；改名确认路由（W006）另用 `target_missing`（409，apply 时新资源已不存在而提案仍在，见 §POST /api/renames/{id}/apply）。
- 分页参数（v0.6.2 起双接口，issue #7）：`page`（1 起）+ `pageSize`（默认 20，>100 截断为 100），或 REST 风格 `limit`（1–100，>100 截断为 100）+ `offset`（0 起，页号按 offset/limit 推导）。非数值/负数/零的 page/pageSize/limit 与负数 offset → 400 `bad_request`（消息说明取值范围；此前 limit/offset 是未知名被静默忽略，负值与「已生效」不可区分）。两接口混用（page+offset 或 pageSize+limit）→ 400。列表信封：`{"items":[…],"total":n,"page":p,"pageSize":s}`。
- 请求日志（slog JSON：method/path/status/duration_ms/bytes）走 stderr；panic 一律转 500 信封，不外泄细节。
- `/api/*` 响应一律 `Cache-Control: no-store`（ADR-0006）。

以下示例除特别标注外均为真实运行输出（导入 `testdata/` 三本工作簿后的库），仅截断。`id` 类字段每次导入都会变化；P1/§7 示例取自 2026-10-09 的服务器旅程（v0.2.0）；拓扑节点 `geoLat/geoLng`、`type=port` 与复合自然键条目为 v0.3.x 新增字段/能力的补充说明（已按实码与真实样例标注）。**本文档契约基线：v0.18.0（2026-10-25，issue #9 两级写门禁）。**

## 写门禁与两级权限（issue #9，v0.18.0）

serve 提供两级可选写令牌（GET/HEAD 只读请求与 `/api/health`、`/api/openapi.json`、SPA 永不门禁）：

| 令牌 | 旗标 | 权限 |
| --- | --- | --- |
| 申报级 | `--auth-token T` | 导入/分析/扫描等全部写操作 |
| 管理级 | `--admin-token A` | 全部操作，含 7 条管理级路由（删除批次/删除模板/指标回滚/费用回滚/线索裁定/关联确认/关联驳回） |

判定矩阵：

- 只配 `--auth-token`：**单操作员兼容**——该令牌保留 v0.11 全量权限（含管理级路由），不因忘配第二级而被锁；
- 只配 `--admin-token`：该令牌是唯一写令牌；
- 双令牌同时配置：管理级路由**仅接受**管理令牌，申报令牌得 `403 forbidden`（信封 `error.code="forbidden"`）；两令牌相同则拒绝启动；
- 无任何令牌 + 非回环绑定（`:8080`/`0.0.0.0`/`[::]`/外部地址）：**拒绝启动**（exit 2）；默认地址已改为 `127.0.0.1:8080`，本地单机零配置行为不变；
- 管理级路由成功（2xx）输出一行 `audit: admin op` 审计日志（method/path/remote/status）；403 不产审计行（未发生操作）。

401/403 契约由路由表单源派生进 openapi.json（401 在全部写路由、403 恰在 7 条管理级路由），零手抄。

## GET /api/health

```json
{
  "status": "ok",
  "version": "v0.15.2",
  "ruleVersion": "1.0.0",
  "dbPath": "/abs/path/to/netops.db"
}
```

（`version` = `domain.AppVersion` 单一事实源，v0.3.2 起由 `TestAppVersionMatchesChangelogHead` 锚定与 CHANGELOG 头版本一致，不再手抄漂移。`dbPath` 为 v0.6.2 新增（issue #8）：服务端实际绑定的数据库绝对路径——CLI 建的工作台在 Web 端 404 时，先比对这个字段与 CLI `--db`/输出的 `db` 字段，一步区分「连错库」与「数据丢失」。）

## GET /api/schema — 资源 schema 动态发现（v0.4.0；W19 起含层次/域字段）

返回**全部可导入资源类型的字段规格 + 数据质量规则目录**。响应体由后端 `mapping.BuildSchemaDocument()` 从 `mapping.Specs`（Go 类型规格的唯一事实源）与 domain 规则目录机械投影生成——非手抄快照，后端规格演进时该端点自动同步（mapping 包投影测试逐字段对照 Specs 钉死）。**只读、无参数、不进 MCP 工具面**（MCP 工具面 11 个只读工具，清单见 [teleagent-integration.md](teleagent-integration.md) §工具清单；schema 发现是 HTTP 专属能力）。前端 `web/src/schema.ts` 编译期镜像保留，可用本端点对照校验。

- `types[]`（按 Specs 注册序）：`type`（资源类型）、`label`（类型中文名）、`layer`（七层模型层数，§10.2 经 §12-B5 修订：0=空间/1=设备/2=光缆/3=传输/4=接入/5=逻辑/6=业务）、`layerName`（层中文名）、`domain`（专业域：space/device/optical/transport/access/logic/service）——v0.7.0 W19（issue #26）起投影，供消费者做同层/跨层推理、免第二张表）、`sheetHint`（sheet 名建议锚点）、`nameField`（展示名字段——`NameFieldOverride` 优先、否则「first *_name」启发式）、`nameFieldOverride`（显式展示名钉定，仅 port/base_station/machine_room 有）、`naturalKeyFields[]`（主自然键字段，序 = `|` 拼接序）、`keyFallbackFields[]`（条件键兜底字段，v0.4 仅 base_station）、`fields[]`。
- `fields[]`：`name`（snake_case 字段名，即映射确认的 target key）、`label`（中文列标签）、`required`（必填，E002）、`naturalKey`（入键）、`kind`（string/ipv4/cidr/int/enum/ref/lat/lng/geo）、`enum[]`（enum 字段取值集，W002 语义）、`intMin`（int 字段下界，缺省=无界）、`intMax`（int 字段上界，缺省=无界；v0.5 W9-D 起投影——此前 vlan 上界 4094 对端点消费者不可见）、`refType`（ref 字段的引用目标类型，E003 语义）、`synonyms[]`（建议引擎同义词表）。
- `rules[]`（规则目录，按码序 E001→E006 / W001→W006 / I001）：`code`、`severity`（error/warning/info，与 `domain.SeverityForCode` 一致）、`description`（一行英文释义）。

以下为真实输出截断（完整响应含 **33 类共 219 字段**与 13 条规则——v0.7.0 W21 扩容后；layer/layerName/domain 覆盖全部 33 类，七层模型 L0 空间/L1 设备/L2 光缆/L3 传输/L4 接入/L5 逻辑/L6 业务）：

```json
{
  "types": [
    {
      "type": "device",
      "label": "网元设备",
      "layer": 1,
      "layerName": "物理层（设备）",
      "domain": "device",
      "sheetHint": "设备",
      "nameField": "device_name",
      "naturalKeyFields": [
        "device_code"
      ],
      "fields": [
        {
          "name": "device_code",
          "label": "设备编码",
          "required": true,
          "naturalKey": true,
          "kind": "string",
          "synonyms": [
            "设备编码",
            "设备编号",
            "网元编码",
            "编码",
            "code"
          ]
        },
        {
          "name": "device_name",
          "label": "设备名称",
          "required": true,
          "naturalKey": false,
          "kind": "string",
          "synonyms": [
            "设备名称",
            "网元名称",
            "名称"
          ]
        }
      ]
    },
    {
      "type": "port",
      "label": "设备端口",
      "sheetHint": "端口",
      "nameField": "port_name",
      "nameFieldOverride": "port_name",
      "naturalKeyFields": [
        "device_name",
        "port_name"
      ],
      "fields": [
        "…（17 字段，含 device_ip kind=ipv4 required=true 非键值列）…"
      ]
    },
    {
      "type": "ip_pool",
      "label": "IP地址池",
      "sheetHint": "池",
      "nameField": "pool_name",
      "naturalKeyFields": [
        "pool_name"
      ],
      "fields": [
        {
          "name": "vlan_id",
          "label": "VLAN",
          "required": false,
          "naturalKey": false,
          "kind": "int",
          "intMin": 1,
          "intMax": 4094,
          "synonyms": [
            "VLAN",
            "VLAN_ID",
            "VLAN编号"
          ]
        }
      ]
    },
    {
      "type": "base_station",
      "label": "基站",
      "sheetHint": "基站",
      "nameField": "base_station_name",
      "nameFieldOverride": "base_station_name",
      "naturalKeyFields": [
        "base_station_code"
      ],
      "keyFallbackFields": [
        "city",
        "base_station_name"
      ],
      "fields": [
        "…（15 字段）…"
      ]
    }
  ],
  "rules": [
    {
      "code": "E001",
      "severity": "error",
      "description": "natural key duplicated (row)"
    },
    {
      "code": "W004",
      "severity": "warning",
      "description": "natural key degraded to fallback fields (row)"
    },
    {
      "code": "W005",
      "severity": "warning",
      "description": "attribute diff on a duplicate-key row awaits confirmation (row)"
    },
    {
      "code": "W006",
      "severity": "warning",
      "description": "name-keyed resource likely renamed (same attrs, new key) awaits confirmation (row)"
    },
    {
      "type": "ptp_port",
      "label": "PTP端口",
      "sheetHint": "PTP",
      "nameField": "port_name",
      "nameFieldOverride": "port_name",
      "naturalKeyFields": ["device_model", "port_name"],
      "fields": [
        {
          "name": "device_model",
          "label": "设备型号",
          "kind": "string",
          "required": true,
          "naturalKey": true,
          "synonyms": ["设备型号"]
        }
        /* … 其余 22 字段（接口名称/注释/端口号/端口状态/时钟参数…）同形状，完整输出见 live 端点 … */
      ]
    },
    {
      "code": "I001",
      "severity": "info",
      "description": "device with zero relations (set)"
    }
  ]
}
```

## GET /api/openapi.json — 本 API 的 OpenAPI 3 文档（v0.10-R9）

**由路由表机械生成，与实现零漂移**：`buildOpenAPIDoc(apiRoutes())` 从单一事实源路由表投影 paths/methods/tags/summaries/状态集（含每路由的 201/202/409/422/501 诚实偏差），非手抄快照——新增路由不写这里也不会漂（`TestOpenAPISpecMatchesLiveServer` 双向钉死）。机器消费入口：把本 API 目录喂给代码生成器/网关/契约测试；散文人类契约即本文档（两道防线：本端点 + 下文的 api.md 标题集门禁）。只读、无参数、`system` tag。

## POST /api/imports — 上传并分析工作簿

multipart/form-data，字段名 `file`，扩展名 `.xlsx` / `.txt` / `.cfg`（W8-D 起文本脚本放行），≤10MB（压缩尺寸超出 → 413 `too_large`）。**PTP 配置脚本通道（W8-D）**：`.txt`/`.cfg` 内容须含脚本特征（同时出现 `config ter` 与 `ptp`）→ 解析为「PTP配置」虚拟 sheet 走同一映射/导入管线（解析规则与语义见 [import-and-validation.md](import-and-validation.md) §18）；无特征 → 422 `unsupported_script`；解析失败 → 422 `script_parse_error`（消息含行号）。其它扩展名维持 422 `not_xlsx`。**解压后尺寸另有上限（v0.3.2，zip 炸弹防护）**：单 sheet 物化行数 > 100,000（含表头）或全簿物化单元格总数 > 5,000,000 → **422 `workbook_too_large`**（"工作簿超出大小上限（单表行数或全簿单元格数量过多），请拆分后重试"）。解析为逐 sheet 流式读取，超限即中止，不会整表先物化。返回 **202** + 分析结果（列建议、映射建议）；批次进入 `awaiting_mapping`。

上传落盘为内容寻址存储 `dataDir/uploads/<sha256>_<原名>.xlsx`；**孤儿上传清理（v0.4.0；#106 费用域接入）**：`serve` 启动时（HTTP 监听启动前、串行执行）与 CLI `netopslab gc --db <db> --data <dir>` 会删除 uploads 下未被任何批次 `workbook_path` 引用的文件（任何状态的批次都算引用——含 failed/rolled_back），并降入 `uploads/fees` 清理未被任何 `fee_batches` 行的 `(sha256, workbook_name)` 派生名的费用暂存文件；MCP 进程不触发清理。见 [docs/import-and-validation.md](import-and-validation.md) §15。费用分析层（`analysis/fees`）的 Parquet 孤儿与过期 `.staging` 暂存目录由同一二处触发点清扫（含 1 小时 mtime 里程碑防跨进程在途导入竞态，CLI JSON 多 `analysisFiles`/`analysisStaging` 字段，见 [fees-layer.md](fees-layer.md) §4）。

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

**W24 起每个 sheet 增带 `templateMatched`**（表头指纹命中已存映射模板时为 `{templateId, resourceType, confirmedAt}`，未命中为 `null`）——信息性字段，向导第②步据此展示「已匹配映射模板」横幅；匹配语义见下方 `auto=1`。

```json
{
  "sheet": "设备清单",
  "rowCount": 30,
  "skippedEmptyRows": 0,
  "columns": ["…"],
  "suggestedType": "device",
  "templateMatched": {
    "templateId": "tpl_3a24fa12-35b6-4e90-9d7f-5351408d4b8c",
    "resourceType": "device",
    "confirmedAt": "2026-10-13T04:18:51Z"
  }
}
```

### POST /api/imports?auto=1 — 无人值守导入（v0.7.0 W24，§12-B7 方案 A）

multipart 上传同上，附加无人值守标志（两种等价写法，任选其一）：

- 查询参数 `?auto=1`（或 `auto=true`）；
- multipart 表单字段 `auto=1`（与 `file` 并列，multipart 风格的原生变体）。

语义：分析后对每个建议导入的 sheet 计算表头指纹（sheet 名 + 规范化表头序列的 sha256，列序敏感，见 [import-and-validation.md](import-and-validation.md) §20）——

- **全部导入 sheet 命中已存模板** → 立即以模板存储的确认映射执行导入（跳过 `awaiting_mapping`），返回 **202 + 与 `/api/imports/{id}/mapping` 完全相同的汇总信封**（含导入后自动集合级校验的 `issues`）。请求只携带文件本身——零人工字段。
- **任一导入 sheet 未命中**（表头被改名/增列/删列/换序 → 指纹变化）或**工作簿没有可导入 sheet** → **不执行、不报错**：返回 202 + 普通分析 JSON，附 `templateMatched: null` 与 `autoWarning` 块（列出未命中的 sheet，**表头变更感知，从不静默**）；批次停留在 `awaiting_mapping`，人工确认流（向导 / mapping 端点）是兜底而非错误状态：

```json
{
  "batchId": "bat_d15eb5df-…",
  "sheets": [{ "sheet": "设备清单", "templateMatched": null }],
  "autoWarning": {
    "code": "no_matching_template",
    "unmatchedSheets": ["设备清单"],
    "message": "以下导入工作表的表头指纹未命中任何映射模板（表头可能已变更，需人工重新确认）：设备清单"
  }
}
```

（`no_import_sheets` 变体：建议映射全部为 skip 时的拒绝码——无人值守「导入 0 行」不得报告成功。skip sheet 本身不阻塞 auto：与人工流一样不导入、无需确认。）

## POST /api/imports/{id}/template — 一键保存映射模板（201，v0.7.0 W24）

把**已完成批次**的确认映射复制为逐 sheet 的映射模板（表头指纹从该批次记录的工作簿重算）。请求体可选 `{"sourceName": "…"}`（模板人类可读标签，缺省为批次工作簿名）；空体合法。

- **201** + `{items: [模板…], total}`；`mapping` 字段是单 sheet 的 §8 映射文档（JSON 对象，可直接回传 mapping 端点人工复演）；
- 同指纹 + 同资源类型的重复确认 **幂等 upsert**：更新 `mapping_json` / `confirmed_at` / `confirmed_batch`，保留原 `templateId`，不产生第二行；
- **404** 未知批次；**409** 批次未完成（只有已完成批次的确认映射才值得保存）；
- **回滚不对称**：回滚创建批次**不会**删除模板（模板是人工确认的产物而非批次数据——与 W005/W006 已决策生命周期同理）。

```json
{
  "items": [
    {
      "templateId": "tpl_3a24fa12-…",
      "sourceName": "devices-day1.xlsx",
      "headerFingerprint": "9c2f…",
      "resourceType": "device",
      "mapping": { "sheets": [ { "sheet": "设备清单", "action": "import", "…": "…" } ] },
      "sheetName": "设备清单",
      "confirmedAt": "2026-10-13T04:18:51Z",
      "confirmedBatch": "bat_c82e6f8e-…"
    }
  ],
  "total": 1
}
```

## GET /api/mapping-templates — 模板列表（v0.7.0 W24）

`?page=&pageSize=`（通用分页契约）；按 `confirmed_at` 倒序（`templateId` 决胜，确定性）。条目结构同上。

## DELETE /api/mapping-templates/{id} — 删除模板（v0.7.0 W24）

删除一个模板 = 无人值守通道的退出开关：该表头形状回落到人工确认流。**200** `{"deleted": true, "id": "…"}`；**404** 未知 id。删除模板不触碰任何批次数据（与回滚不对称同理，双向不联动）。

### CLI 通道（与 HTTP 同引擎）

`netopslab import --file x.xlsx --db d --auto`（无人值守：全部导入 sheet 命中模板 → exit 0 + 汇总信封；任一未命中 → **exit 1**，stdout 错误信封 `code: "no_matching_template"` 且消息点名未命中的 sheet——批次停留 `awaiting_mapping` 供 `--map` 人工流兜底；`--auto` 与 `--map` 互斥，同给 → exit 2）。`netopslab templates --db d save --batch <id> [--name n]`（捕获）、`templates --db d list [--page P] [--pageSize S]`（清单）、`templates --db d delete --id <tpl_id>`（删除）——与上述 HTTP 端点同引擎（同一 composition root 的 importer/repo 实例），无第二条模板路径。

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

`type` 可取 `device` / `ip_pool` / `circuit` / `port` / `base_station` / `site` / `machine_room`（v0.3.1 起 port、v0.4 起 base_station、v0.4 W3-D 起 site/machine_room 入封闭集；非法值 → 400 `bad_request`）。`q` 对 natural_key / name / attributes 值做不区分大小写包含匹配；默认按类型分组、每组自然键排序（首页 circuit-first 为排序副作用）。示例 `?pageSize=2`：

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

**sourceRefs 版本化（v0.5 W7-D）**：`sourceRefs[]` 不止首源——同键再导入的 skip 行对每个再导入批次追加一条版本引用（每批至多一条，`UNIQUE (entity_kind, entity_id, batch_id)`；E001 批内重复行不追加），数组按**版本序**（rowid = 插入序：创建批次在前，每次再导在后）返回；`raw` 记录该批次当时工作簿的原话（W005 属性变更尚未 apply 时，版本行已记录新值）。批次回滚（`DELETE /api/batches/{id}`）后该批的版本行随批消失（`batch_id ON DELETE CASCADE` + 回滚删除链），数组回到剩余版本；回滚响应的 `deleted.sourceRefs` 计数含 skip 追加行——诚实语义：该批记录的引用数（含确证版本）。行为细节见 [import-and-validation.md](import-and-validation.md) §17。

**port 类型的复合自然键（v0.3.1 引入；migration v8 换键）**：port 资源的 `naturalKey` 是复合键 `device_name|port_name` 按此顺序以 `|` 拼接落库（例如 `B2|bvi2.1008`，取自真实样例 `samples/telecom-port-multi-header.xlsx`：设备名称 B2 + 端口名 bvi2.1008）；检索/幂等判重（E001/skip）均以拼接后的完整键匹配。单键类型（device/ip_pool/circuit）拼接退化为原值，行为不变。键的设备侧自 migration v8 起锚定设备**名称**而非 IP（设备重划 IP 不再分裂资源；`device_ip` 为必填 ipv4 值列，落在 `attributes` 中）——稳定性评估见 [natural-keys.md](natural-keys.md)。

**base_station 的条件自然键（v0.4 引入，W004）**：基站的主自然键字段是 `base_station_code`（基站编码，非必填——真实表编码列存在但值常为空）。编码值**非空**时 `naturalKey` = 编码（全局唯一）；**为空**时键退化为兜底字段 `(city, base_station_name)` 按序以 `|` 拼接、跳过空值——工作簿无地市列（真实形态）即退化为纯名称（如真实样例的 `动力机房2`），映射了地市列则为 `哈尔滨|花园小区1`。行照常导入并记 **W004**（身份键退化，warning 级）治理 issue，evidence 含 `natural_key`（所用兜底键）、`missing_fields`（缺失的主键字段）、`fallback_fields`。E001 判重与幂等 skip 都在解析后的最终键上判定：编码行与同名兜底行是两个身份。基站IP 真实值带 /30 掩码，为 string 值列（可变属性，非身份）；条件键机制由 spec 声明（`TypeSpec.KeyFallbackFields`）驱动，见 [natural-keys.md](natural-keys.md) §4/§5。

**site / machine_room 的编号自然键与 belongs_to 关系（v0.4 W3-D 引入，migration v10）**：真实机房局站表（8 列：地市/区县/设备名称/IP地址/机房ID/机房名称/局站ID/局站名称；样例 `samples/telecom-site-machine-room.xlsx`）**同一张 sheet 承载两类实体**——导入故事是把同一 sheet 读两遍：先按局站类型导入落 `site` 资源（`naturalKey` = 局站ID，12 位数字编码，全局唯一），再按机房类型导入落 `machine_room` 资源（`naturalKey` = 机房ID，17 位数字编码，全局唯一）。两类 ID 字段均为 string 值列（编号是数字串，前导零是身份的一部分，绝不数值化）。机房读法把 `局站ID` 列映射为可选 ref 字段 `site_id`：按局站ID 解析目标 site 资源，成功即派生一条 `machine_room --belongs_to--> site` 关系（见下节）；解析失败（局站未先入库）→ 该行 **E003** 失败——**导入顺序（先局站后机房）由 E003 语义钉死**；不映射该列则导入成功且不建关系（每行记 W001 孤儿警告）。ref 字段值同 `ip_pool.assigned_device` 一样落在 `attributes` 中。机房名称/局站名称/设备名称均为昵称（展示名/属性），IP地址为可变属性（KindIPv4，坏值 E004）。键策略裁决见 [natural-keys.md](natural-keys.md) §4。W004 无涉（两类 ID 键均为无条件主键，非条件键类型）。

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

`byType` 键集动态（新增类型如 port/site/machine_room 仅在有数据时出现，R1-D4 起前端按实际键动态渲染）；`byRelationType` 的四个已知关系类型恒出现（含 v0.4 W3-D 起的 `belongs_to`，计 0 亦发）。上例为三张合成工作簿的种子数据，故不含新类型键。

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

## GET /api/attribute-changes?status=&resource_id=&batch_id=&limit=&offset= — 属性变更提案列表

（W005，migration v11）同键再导入检测到属性差异时逐字段记录的**待确认变更**。参数：`status=pending|applied|dismissed|all`（缺省 all）；`resource_id` / `batch_id` 精确过滤；分页用 `limit`（默认 20，>100 截断）/ `offset`（非负，默认 0）——边界遵循既有列表端点惯例。信封 `{"items":[…],"total":n}`，按 `created_at, rowid`（提案入队序）排序。`resourceType` / `naturalKey` 为资源侧 join 字段（提案的资源被回滚级联删除后为空串）。`relationRetargeted` ——列表项上为**实时谓词重算**（引用字段且该资源当前已有对应派生关系 → true；非引用字段与无派生关系的引用字段恒 false），pending 项作确认预览（前端提示用它）；apply/dismiss 响应上为**该操作的事实结果**（apply 重挂成功 true / dismiss 恒 false）。生产端：importer 的 skip 分支在属性不同时逐字段记 pending 提案 + 一条 W005 warning issue，同值再导仍纯 skip 零记录（幂等不变）；同字段同新值已 pending → 不重复记录（也不重记 issue）。

## POST /api/attribute-changes/{id}/apply — 应用待确认变更（人工确认通道，管理级）

仅 pending 可应用。校验**资源当前属性值 == 提案旧值**：相等 → 新值写入 `resources.attributes`、`updated_at` 刷新、提案置 `applied`（`appliedAt` 打点），同一事务内原子完成 → 200 返回变更后对象。**变更字段恰为该类型展示名字段（NameField）时 `resources.name` 一并同步**（name 是该字段值的投影）；**ref 类字段的重挂**：新值先按被引用类型的自然键解析（与 E003 同一查法）——解析不到 → 409 `ref_target_missing`（message 含字段/目标键/被引用类型；提案保持 pending，资源与关系零改动）；解析成功且资源已有派生关系 → 该关系的 `to_resource_id` 在同一事务内改挂到新目标，200 响应 `relationRetargeted: true`；解析成功但资源无派生关系 → 只改属性不建边。该字段已被其它变更修改（当前值 ≠ 旧值）→ 409 `attribute_conflict`，message 含字段名与两个值，资源不动。非 pending → 409 `conflict`；未知 id 或提案的资源已不存在 → 404。

## POST /api/attribute-changes/{id}/dismiss — 忽略待确认变更（管理级）

仅 pending 可忽略：提案置 `dismissed`（`appliedAt` 保持 null），资源永不触碰，审计线保留 → 200 返回变更后对象；非 pending → 409 `conflict`；未知 id → 404。

> **AI 通道边界**：变更确认是 HTTP/CLI 的人类通道——MCP 工具面全部只读（无 apply/dismiss 类变更工具），AI 不得经 MCP 确认或驳回变更。

## GET /api/renames?status=&resource_id=&resource_type=&batch_id=&limit=&offset= — 疑似改名提案列表

（W006，migration v12）名称类键类型（ip_pool / base_station / port，`TypeSpec.RenameDetection`）的新键导入行与旧键消失资源属性全等时记录的**待确认改名提案**。参数：`status=pending|applied|rejected|all`（缺省 all）；`resource_id`（**提案任一侧**：`resource_id` 或 `new_resource_id` 命中即返回——双侧匹配，W006 issue 深链落在新资源抽屉时面板同样可见）/ `resource_type` / `batch_id` 精确过滤（`resource_type` 非法 → 400）；分页边界同上。信封 `{"items":[…],"total":n}`，按 `created_at, rowid` 排序。项字段：`id`、`resourceId`（旧资源）、`resourceType` / `naturalKey`、`batchId`、`newResourceId`、`oldKey`、`newKey`、`matchedFields[]`（参与匹配且相等的非键字段名）、`status`、`sheet`、`rowNumber`、`createdAt`、`decidedAt`。检测五条件与幂等语义见 [import-and-validation.md](import-and-validation.md) §16。

## POST /api/renames/{id}/apply — 确认改名（身份合并，人工确认通道，管理级）

确认 pending 提案：单事务完成身份合并——新资源的关系改挂旧资源（UNIQUE 撞车删新侧边）、**键分量属性同步**（新侧非空者覆写旧资源——键换了键分量属性也得换，防假 W005）、**展示名 Name 同步**（`resources.name` 是 NameField 值的投影，合并后跟随新名——键类名称字段如 pool_name 不进 attributes 时取新资源自身的 name）、删新资源（其 pending 属性变更随 v11 CASCADE）、旧资源 `natural_key` 换为新键 + `updated_at` 刷新、提案置 `applied` + `decidedAt` 打点。**旧资源代理键 `res_x`、关系身份、SourceRef 不动**——合并是键改挂，不是行替换。→ 200 返回提案对象。非 pending → 409 `conflict`；**新资源已不存在而提案仍在** → 409 `target_missing`（诚实口径：标准批次回滚会先删本批 pending 提案、随后的 apply 将得 404；此 409 是防御性守卫——典型触发是新资源被其它路径直接删除而提案幸存，消息建议 reject 后重新导入）；未知 id → 404。

## POST /api/renames/{id}/reject — 拒绝改名（维持独立，管理级）

仅 pending 可拒绝：提案置 `rejected` + `decidedAt` 打点，资源零改动——两个资源维持独立（导入的默认结果），提案留作人工决策审计线 → 200 返回提案对象；非 pending → 409 `conflict`；未知 id → 404。CLI 等价：`netopsfees renames --db … list|apply|reject`（结构化 JSON stdout；冲突 exit 1 + 错误信封）；属性变更侧 `netopsfees changes --db … list|apply|dismiss` 同构。

> **AI 通道边界（同上）**：改名确认是 HTTP/CLI 的人类通道——MCP 工具面全部只读（无身份合并/改键类工具），AI 不得经 MCP 合并身份或改写自然键。回滚不对称：批次回滚删本批 pending 提案，applied/rejected 留审计线（与模板生命周期同理）。

## 时序指标端点（v0.7.0 W20 — 数据模型 v1.0 §5/§7.3）

时序存储三表族（migration v14：`metric_keys` 指标目录 / `metric_series` 序列元数据 / `metrics` 窄表测量点，`(series_id, ts)` 主键 + `INSERT OR REPLACE`——**值语义**：同 (series, ts) 重导覆盖，与资源的 skip 语义并存见 ADR-0014）。**MCP 工具面不为此扩容**（metrics_query 是既有只读工具之一，工具计数由 docs/facts.md `mcp-tools` 门禁钉死）：AI 经 HTTP 消费时序；写入通道独立于资源导入（`metric_batches` 独立批次表，ADR-0011 D1）。

### GET /api/metrics/keys — 指标目录

封闭注册的指标目录（迁移 v14 从代码注册表 `internal/metrics` 播种，目录表与注册表由测试钉死一致）。**AI 先查此端点知道有哪些指标可分析**。`items[]`：`metricKey`（snake_case 键，查询参数用它）、`label`（中文名）、`unit`（dBm/%/Mbps/count/℃/V/mA）、`valueKind`（gauge|counter|ratio）、`minValue`/`maxValue`（合理界，advisory——越限值入库存 `quality=suspect` 而非拒绝）、`direction`（higher_is_better|lower_is_better|neutral——判断劣化方向）。目录含 14 键：光功率收/发均值、谷值、丢包率收/发、带宽利用率收/发、速率收/发、错误包数收/发、光模块温度、供电电压、偏置电流。

### GET /api/metrics/forecast?resourceId=&metricKey=&windowDays= — 指标趋势预测（ADR-0011 D4，#123）

单序列日均值的**最小二乘线性趋势**：斜率/截距/残差 SE/斜率 SE 四个数 + **N 天后到达阈值**的投影（按目录 Direction 映射建议边界：higher_is_better → min、lower_is_better → max）+ **80% 预测带**（正态近似 1.282）。

- `resourceId` + `metricKey` 必填（metricKey 走封闭目录校验）；`windowDays` 可选 1–90（缺省 30），是最后观测日之后外推的天数；
- **<14 个日聚合点 → 422 `forecast_not_enough_data`**（D4 原话「数据不足时上预测是自欺」）；
- `daysToThreshold`：null = 不可判定（neutral 方向 / 该侧无界 / 斜率远离阈值 / 斜率为零——诚实留白）；**0 = 已越限**（检测的事，预测不冒充）；
- `note` 恒为「外推参考，非承诺——线性趋势基于历史日均值，未含季节性/突发/变更，越远越不可信」。

### POST /api/metrics/analyze — 指标工作簿分析（无状态）

multipart 字段 `file`（.xlsx ≤10MB）→ 200 `{workbookName, sha256, sheet, rowCount, columns[], suggestedMapping, warnings[]}`。同义词引擎把列头匹配到目录指标（接收光功率均值 → `optical_power_rx_avg_dbm`）；`suggestedMapping`：`{tsField, resourceType:"port", resolveBy:["device_name","port_name"]|["device_ip","port_name"], metrics:{目标指标键: 源列头}}`。**不建批次**（指标导入是一次性的：映射与文件同行）。错误：非 xlsx → 422 `not_xlsx`。

### POST /api/metrics/imports — 执行指标导入（upsert）

multipart 字段 `file` + `mapping`（analyze 的 suggestedMapping JSON 原样确认）→ 202 `{batchId, status:"completed", rowsTotal, rowsImported, rowsFailed, pointsWritten, seriesTouched}`。

- **资源解析**（A3 裁决=挂 port 资源）：路径 a 设备名称+端口名 → port 自然键直查；路径 b 设备IP+端口名 → 按 `mgmt_ip` 精确匹配设备再拼 port 键。**均为精确值匹配**（名称相似度推断是项目红线，不存在于此）。解析失败 → 该行 `rowsFailed`（行级失败不致命）。
- **ts 来自数据**（D2）：日期列接受 excel 序列日期 / `YYYY-MM-DD` / `YYYY/M/D`，规范化 RFC3339 UTC。数据路径零服务器时钟（确定性）。
- **幂等**（A2/ADR-0014 验收）：同文件连导 3 次 `COUNT(*) FROM metrics` 不变；同 (series, ts) 异值 → 末次胜出。
- 越限值 → `quality='suspect'`（advisory 界不拒绝）；空指标单元格 → 跳过该点。
- 错误：缺 `mapping` 字段 → 400；未知指标键/非法 resolveBy → 400；未知表头列 → 400（不静默错映）。

### GET /api/resources/{id}/metrics — 资源时序（AI 趋势查询）

`?keys=a,b&from=&to=&agg=raw|daily_avg|daily_min|daily_max`（from/to RFC3339 可选；keys 可选=全部序列；agg 默认 raw）。200 `{resourceId, series[]}`，每序列 `{seriesId, metricKey, unit, points:[{ts,value,quality}]}`，序确定（metricKey, ts 升序）；日聚合在 SQL 内 `GROUP BY date(ts)`。错误：未知资源 404；未知指标键 400；非法 agg 400。

### GET /api/metrics/anomalies — 异常扫描（每日巡检核心调用）

`?metricKey=&from=&to=&threshold=&direction=`（threshold 默认 1.0 正数；direction=declining|rising|any 默认 any；metricKey 可选=全指标扫描）。对窗口内每序列计算首末点与 `delta`，`|delta| >= threshold` 的序列返回，按资源分组：`items[]:{resourceId, resourceName, resourceType, metricKey, unit, n, first{ts,value}, last{ts,value}, delta, degrading}`。**degrading 方向感知**：higher_is_better 且 delta<0、或 lower_is_better 且 delta>0 → true（neutral 指标趋势不判劣化）。确定性排序（resource, metricKey）。设计 §5.5 Q2 的落地——「哪些设备的指标在持续下降」一次调用。

### GET /api/metrics/batches — 指标批次列表

`?page=&pageSize=`（同既有列表契约）→ `{items[], total}`，倒序，含 id/workbookName/sha256/status/行计数/时间戳。

### POST /api/metrics/batches/{id}/rollback — 回滚指标批次

删除该批次写入的测量点（`metrics.batch_id` 锚定）+ 清理因此变空的序列（仍有其他批次点的序列保留）。仅 completed 可回滚 → 200 `{deleted:{points, series}}`；非 completed → 409；未知 id → 404。CLI 通道：`netopslab metrics --db <path> rollback --batch <id>`（#115 收口——错误码表与 HTTP 对齐：conflict / not_found / usage_error）

### CLI 通道（与 HTTP 同引擎）

`netopslab ingest --file perf.xlsx --map m.json --db …`（执行）；`--suggest` 只打印建议映射 JSON；`netopslab synthdata --out DIR --devices N --ports N --days D --scenario normal|degrade|outage --seed S`（确定性合成数据：同 seed 字节级相同；resources.xlsx 走既有资源导入通道，perf-day-*.xlsx 走指标通道）。`netopslab metrics --db <path> list [--page N] [--page-size N] | rollback --batch <id>`（批次生命周期，#115：list 与 `GET /api/metrics/batches` 同形分页，rollback 与 HTTP 端点同引擎同错误码表——至此三大导入通道 CLI 回滚齐全：资源 `rollback` / 费用 `fees rollback` / 指标 `metrics rollback`）。

## 运行状态与图查询端点（v0.7.0 W22 — 数据模型 v1.0 §6/§10/§12-B1）

W22 落地**运行状态模型**（migration v17：`resource_status` 历史表 + `resources.run_state/run_state_at` 快照列——台账 status 与运行 run_state 彻底分立，W005/lab 零触碰）与**纵向穿透三端点**（traverse/impact/context，§10.3-§10.5）。遍历语义 = **层差驱动**（§12 W22 处置）：`direction=down` 只向低层或同层走、`up` 只向高层或同层走、`both` 不限——边携带的存储方向（成员→容器/上层→下层）保留在响应中供 AI 推理，但**可达性由层差决定**。旧端点 `GET /resources/{id}/relations?depth=1|2` 保留（deprecated，行为不变——§10.10 不做破坏性变更）。

### CLI 图查询通道（issue #41，v0.8.1）

traverse / impact / context 三个图查询能力**不再是 HTTP 独占**——同一引擎、同一组合根装配（`services.GraphStore` 的唯一 store 绑定与 `services.GraphContext` 的唯一上下文装配），CLI 与 HTTP 输出**逐字节一致**（跨通道 e2e 以 JSON 相等钉死）：

```
netopslab query --db <path> traverse --id <id> [--direction down|up|both] [--layers 0,1,2]   [--relations carried_by,uses_fiber] [--max-depth 1..8] [--max-nodes 1..500] [--path-only]
netopslab query --db <path> impact   --id <id> [--direction up|down] [--max-depth 1..8]
netopslab query --db <path> context  --id <id> [--depth 1|2] [--metrics latest|none]
netopslab query --db <path> nearest  --type base_station --lat 45.80 --lng 126.53 [--limit 5]
```

参数语义与 HTTP 查询参数一一对应（CLI 惯例的 kebab-case），校验边界同源（同常量）；stdout 恰好一个 JSON 对象（§10 契约）。`query nearest` 是 v20 geo 列的空间查询出口（issue #45）：类型过滤 + haversine 真距离排序，~1.5°×3° 窗口语义见 `repo.ResourceRepo.Nearest`。脚本化巡检（导入→检测→图查询）从此无需先 `serve`。

**空集合契约（audit §2.6，v0.8.1）**：traverse 的 `nodes`/`edges`、impact 的 `impacted`/`candidates`、context 的 `metrics`/`recentAnomalies`/`neighbors.nodes`/`neighbors.edges` 无结果时恒为 `[]`，**不是 `null`**——严格客户端的 `len()`/迭代不再需要判空分支。

### GET /api/stats 扩展

新增 `byRunState{up,down,degraded,maintenance,unknown,null}`（null = 无观测资源数——NULL 与显式 unknown 的区分在聚合层保持）。

## 告警与检测端点（v0.7.0 W23 — ADR-0011 D2/D3，issue #23）

检测规则是**配置数据**（不改代码即可新增阈值规则）；告警是**状态机数据**（触发→确认→恢复三边，无自动升级/超时关闭——本地单机系统，状态机每加一条自动边就多一个不可复现的「告警自己消失了」工单来源，ADR-0011 D3）。规则目录与 E/W/I 导入规则**同构不同表、rule_version 不跨域**（防导入回归与检测演进互相锁死）；越限方向复用 `metric_keys.direction`；判定引擎为纯函数核心（`internal/detect`，ts 全来自数据、确定性）。**MCP 工具面 11 个只读工具已含 alerts_query，不为此再扩容**——AI 经 alerts_query 只读消费告警（ADR-0011 D5）。

### CLI 通道

`netopslab detect --db … [--from --to]`（执行检测，输出 run 信封 JSON）；`netopslab detect --rules`（列规则）。**不做定时**——cron 是运维方的职责（确定性哲学：引擎被调用才运行）。

## 维护窗口端点（v0.12-R15 — 计划内告警抑制；双时基 v0.12.1，issue #55）

维护窗口是**计划数据**（与检测规则同先例）：一段时间内、范围内资源的新告警触发被抑制（已触发告警的生命周期不受影响；恢复清除照常——窗口只挡新噪音，不改写历史）。**时间基准（双时基，与 "/api/detection/run" 节一致）**：窗口 `[startsAt, endsAt)` 半开区间，证据时刻（数据时间线）或运行时刻（扫描执行的现实时刻）**任一**落入即抑制；前端创建表单同时展示「当前数据时刻」辅助对齐。无 PATCH——编辑 = 删除 + 重建（窗口是计划不是档案）；删除窗口不影响历史运行回执。POST/DELETE 走 token 写门。

### CLI 通道

无独立子命令——窗口经 HTTP（或直接 SQL）管理；检测引擎在扫描事务内读取全部窗口（事务一致性：扫描中途建窗口不会半生效）。

## POST /api/fees/imports — 费用数据导入（multipart，#58/#60/#61）

multipart 字段：`file`（.csv/.xlsx，≤10MB；**.xls 为 Excel 2003 二进制旧格式，不受支持**——识别层与快照层均以 #92 专门消息拒收并引导「另存为 .xlsx（或导出 .csv）」，CLI `fees import` 帮助文案同口径）+ `feeType`（electricity/tower_lease/repair/maintenance）+ `year` + `month`（声明账期）+ 可选 `map`（列映射 JSON：`{"columns":{"siteCode":"站址编码","amount":"电费(元)","period":"账期"}}`，缺省表头自动识别；识别不到必需列 → 422）。管线：快照（内容寻址不可变）→ 规范化（金额/账期/站址编码；行失败计数不致命）→ Parquet 分区大表（`fee_type/year/month` hive 分区，行级账期优先于声明账期）→ 批次行（幂等键 = 哈希×类型×账期×映射）。**幂等**：同文件重导 → **200**（非 201）+ `skippedDuplicate=true` + `existingBatch`（informational，与 CLI 同严重级语义）。**重复应答的 `warnings` 为空（#25 方案 B）**：重复导入未写入任何数据，当前次运行的提醒（源文件可写建议 #6、骤降线 #12）描述的是一个没有发生的导入事件——不应出现；持久审计记录由 `periodSource`/`periodBasis` 字段携带（#20 覆盖留痕等），测试钉死「warnings 无重复项」且重复路径为空。新导入 → **201** + 摘要（计数/分区文件清单/行错误/`amountColumn`（实际取数的金额表头，#29——多金额列变体表上写明口径）；`warnings` 携带本次导入的提醒——源文件可写建议等，绝不重复拼接；**全零止血（#29）**：全部行金额为 0 时警告具名「无法参与任何金额分析」+ 映射核对指引，绝不静默导好；**表型口径（#29）**：塔类结算详单的金额列按类型专表 exact 匹配（产品服务费合计（出账费用）（不含税）优先，多行表头先归一化），显式 `map` 永远胜出，同族变体 >1 时说明取了哪列）。全部行失败 → 422 `fee_rows_failed`（错误明细在摘要，重试通道开放）。参数非法 → 400；扩展名不支持（.csv/.xlsx 之外）→ 422 `unsupported_fee_file`。设计全貌见 [fees-layer.md](fees-layer.md)。

## POST /api/fees/imports/sheets — 多 sheet 台账整本导入（#125）

真实月度台账的自然形态：**一个工作簿 = 一个月，每张 sheet 一种费用类型**（电费/铁塔租费/修理费）。此端点一次吃掉整本工作簿——每张 sheet 由**自身的证据**（表名 + 表头标记）判定费用类型，产出**独立批次**（独立幂等键 `(sha256, sheet, feeType, 账期, 映射)`、独立证据链、独立回滚面）。

multipart 字段：`file`（.xlsx；**CSV 拒收**——没有表名就没有类型证据，请用单 sheet 通道显式声明）+ `year` + `month`。**没有 `feeType` 字段**——传了直接 400 指引去单 sheet 通道。

- 幂等重复导入（同一文件、同模式）→ **200**，每个条目 `skippedDuplicate=true` + `existingBatch`（informational，与单 sheet 通道同一教义）；被跳过的 sheet 条目 `warnings` 为空（#25：未写入即无当前次提醒，库内留痕在 `periodBasis`）；
- 全新导入 → **201** + `batches[]`（sheet/feeType/batchId/行数/文件清单）+ `skippedSheets[]`（装饰性 sheet 按名列出，绝不静默丢弃）；
- CSV、不可判定 sheet、必备列映射失败、与单 sheet 模式跨模式重导 → **422 `fee_import_refused`**（**全部 sheet 先分类后落地**，拒绝发生在任何数据落地之前，列出全部肇事 sheet）；
- 中途服务器故障（部分 sheet 已落地）→ **500 `fee_import_partial`**，消息点名已落地批次（可查询/可回滚）与失败原因。

**验收锚点**（#125）：三 sheet 台账（电费 12345.67 / 铁塔租费 5000.00 / 修理费）整本导入后，`tower_lease` 的 totals 汇总必须等于**铁塔租费 sheet 的金额**（5000.00），而不是第一张 sheet 的 12345.67；单 sheet / CSV 行为零回归。语义细节见 [fees-layer.md](fees-layer.md)。

## GET /api/fees/batches?status= — 费用导入批次列表

`status=completed/failed/rolled_back`（缺省全部）；最新在前。`feeTypeLabel`（中文）与 `period`（2024-05）为展示字段；`snapshotPath` 是内容寻址快照相对路径（证据锚点；**逻辑路径，恒 `/` 分隔**——跨平台契约，migration v31 起存量统一归一）。

## POST /api/fees/batches/{id}/rollback — 回滚费用批次

仅 completed 可回滚（否则 409 `conflict`；未知 id 404）：删除该批次的 Parquet 分析行（空分区目录一并清理），**快照保留**（证据寿命 > 批次寿命），批次行置 `rolled_back`（终态审计）。键随回滚释放——同文件可再导入（201）。

**回滚是原子事务（#107）**：删除集先物化、逐文件以可逆 rename 移入 `analysis/fees/.rollback/<批次id>/` 隔离区，**批次行 flip `rolled_back` 是唯一提交点**。提交点前任何故障（IO/DB，5xx 上报）→ 隔离整体 UNDO——批次保持 `completed`、分析行**一行不少**（回滚要么整体发生要么整体不发生，可干净重试）；连 UNDO 都被阻断的极端双重故障 → 批次行如实标 `failed`（error 文本记录隔离区滞留位置；`failed` 不占幂等键——重导快照原件即恢复分析行，滞留物过 1 小时里程碑后由 GC 收走）。并发边角：两请求同批回滚时 SQLite flip 串行化出唯一赢家，输家的 UNDO 对已被赢家带走的文件按 ENOENT 空转，终态恒收敛。

## GET /api/fees/batches/{id}/evidence?row= — 费用证据回读（快照原文，#86）

证据链的**第三级兑现**：风险线索/导入错误携带 `source_file + source_row + import_batch` 锚点，本端点把锚点回读成**快照里的原始行**——从「结论」到「原文」一步可查。`row` 是**源文件自身的行号**（表头 = 1；与每条线索的 `sourceRow`、导入错误 detail 里的行号同口径），非法（非整数 / < 1）→ 400。返回 `EvidenceRow{batchId, sourceFile, originalName, feeType, year, month, row, header[], cells[], note}`（`header` + `cells` 逐列对位，运营者无需再打开原始文件）。未知批次 id → 404。**纯文件读取，无引擎参与**：任何构建（含默认构建）都可读。快照不可变且**按设计在回滚后存活**，故 `rolled_back` 批次的证据同样可回读（证据寿命 > 批次寿命，与 §`POST /api/fees/batches/{id}/rollback` 同一教义）。CLI 等价：`netopslab fees evidence --batch <id> --row <n>`。

**未建模列同样可读（#39）**：`cells` 是快照原文的**逐列完整副本**——含导入映射未建模的原始列（塔类详单的 154 列里：折扣生效日期/分项单价/折扣率/共享用户数……都在，只是分析层不消费）；溯源时把同一站相邻账期两行原始行逐列比对即可定位变更字段（#39 实测：+60% 涨价线索的根因就在原始表第 92 列「折扣生效日期」）。`note` 携带 #39 教义：「原始行字段，未经系统解释……不是认定结论」——回读是证据兑现，不是结论生成；`spike` / `outlier` 场景的每条线索 note 亦附带首锚点的真实 `(batch, row)` 溯源指引（三面同文：CLI `fees evidence --batch <id> --row <n>`、本端点、MCP `fee_evidence` 原始行模式）。

## GET /api/fees/years — 费用数据年份发现（v0.20.3）

年份输入的**数据优先默认**与总览**年份诚实空态**的判据面：`{"years":[2023,2024]}`——有分析行的年份**升序**列表。旧观感缺陷：年份输入一律默认当前年，2024 数据环境打开总览是 2026 的空白仪表盘 + 风险地图把 `month=0` 发给后端浮出「month 必须是 1–12 的整数」校验错——日历替数据做了主。本端点让前端「默认跟随数据」：有数据年取最新有数据年，无数据回退当前年。

**路径级发现，两种构建都作答（无 501）**：年份清单解析自引擎读表用的同一 glob（`analysis/fees/fee_type=*/year=*/month=*/*.parquet`）的 `year=` 分区组件——目录在场是元数据不是引擎查询，与 `read_parquet` 实际会扫的面**零漂移**。**回滚后的空分区目录不算在场**（glob 匹配文件不匹配目录——回滚删净的年份诚实缺席）；坏 glob（数据目录含 `[`）如实 500 上报，绝不伪装成「无数据」（#105 教义）；stray 路径不具写入器五段分区形则跳过，不炸年份选择器。无参数；只读开放（同其它读面）。

## GET /api/fees/query/totals?feeType=&year=&month=&limit= — 站址费用汇总

**固定预设 + 参数**（调用方传不了 SQL——MCP 红线延续到引擎面）。`month` 可省（整年）；`limit` 默认 20。返回 `{preset,feeType,year,month,items:[{siteCode,rows,totalCents}],total}`（按 totalCents 降序）。**金额为整数分**（`totalCents`，#65 P1-a：金额在解析/存储/聚合/输出全链路走 int64 分，杜绝浮点对账误差；展示层自行 /100）。**默认构建 501 `engine_not_built`**（DuckDB 引擎随 `-tags duckdb` CGO 构建编译；Windows 原生无 duckdb 路径——用 Docker/WSL2，见 fees-layer.md §5；导入/快照/Parquet 写入不受影响）。

## GET /api/fees/query/monthly?feeType=&year= — 月度趋势序列（#3）

**展示层趋势图的数据面**：所选费用类型在所选年份的逐月聚合 `{preset,feeType,year,items:[{month,rows,sites,totalCents}],total}`（按月升序）。**不接受 `month` 参数**（400——序列覆盖全年；有数据的月份才出现，无数据月份不返回，展示层补空白**不造零**）；`sites` 为该月去重站址数（总览卡「站址数」同源）。整数分契约同 totals（#65 P1-a）；501 契约同 totals。CLI：`fees query monthly --type --year`。

## GET /api/fees/query/duplicates?feeType=&year=&month= — 跨批次重复结算候选

同键多批次共存的行（#4 起**口径为 (站址, 记录键, 账期) 三元组**：记录键=订单号/电表编号/工单号；同站多条记录键是正常多订单/多电表形态，不算重复；无记录键的批次仍按 (站址, 账期) 对口径，`HAVING count(DISTINCT import_batch) > 1`）——**风险线索非结论**：输出候选（site/recordKey/period/批次数/行数/`totalCents` 整数分总额），人工持行级证据（source_file + source_row）核实。账单存在 ≠ 重复付款认定。整数分保证比对与求和顺序无关（#65 P1-a）。参数与 501 契约同 totals。

## GET /api/fees/query/rows?feeType=&year=&month=&site=&recordKey=&limit= — 台账行查询（#5/#4）

分析层的原始行回读：`site/账期/recordKey`（#4 记录键钻取）过滤 + `limit`（1–500），返回每行的核心身份（site/year/month/`amountCents` 整数分/`recordKey` 记录键）与**可选台账列**（`meterNo`/`readingStart`/`readingEnd`/`meterPrevEnd`/`meterSwapped`/`quantity`/`unitPrice`/`shareMobile`/`shareUnicom`/`shareTelecom`/`shareTower`——缺列/空值为 null；#11 转供电补报四列：`purchaseStart`/`purchaseEnd`〔ISO `YYYY-MM-DD`〕/`receivableCents`/`receivedCents`〔整数分〕）与证据坐标（`sourceFile`/`sourceRow`/`importBatch`）。**v35 前的旧批次没有这些列，union_by_name 读取下按 null 返回**——新旧批次混读同表。#5 台账列消费扩展的查询面；501 契约同 totals。

### 场景 9：ledger-consistency 台账内部一致性（#5/#8，#11 扩展）

`GET /api/fees/risks/{scenario}` 的第 9 个成员（参数同其它场景：feeType/year[/month]/limit）。六条**确定性**规则（非统计推断，阈值固定无参数）：`reading-gap`（读数差与填报用量之差 > 0.5 度——**一表多户已排除**：仅当无分摊列或某一方分摊为全量时才判读数差，#8 §0 撤回教训）/ `ratio-sum`（四方分摊比例之和与 1 偏差 > 0.005）/ `chain-break`（同电表跨账期 上月止数≠本月起数 且未标记换表——**已标记换表的合法断开不算线索**）/ `zero-quantity-billing`（用量=0 且金额>0——疑似漏抄后顺延计费）/ `purchase-period`（购电截止日−购电起始日 > 35 天——转供电「长期不报+集中补报」候选，#11：补报可能是合理业务，线索非错误认定）/ `receivable-unreceived`（应收电信−实收电信 > 0——资金回收风险，实收=0 为完全未收形态，#11）。每条线索带行级证据锚点与口径 note（不构成认定结论）；`excluded` = 一表多户未判读数差行数 + 已标记换表的断链行数；缺相应列的文件对应规则自动跳过；#11 两规则依赖购电起止日/应收实收列，旧批次（无该列的一代文件）同样自动跳过（列探测守卫）。

## GET /api/fees/details?linkKey=&period=&feeType=&status=&category= — 明细层台账（#16 完成线 6）

人工补录佐证层的台账（最新在前，全过滤项可选）。明细**永不参与系统表金额计算**——它是证据不是权威。读开放。

## POST /api/fees/details — 明细补录（#16：三强制=关联键+金额+录入留痕）

申报级令牌即可（AI/运营通道）。body：`{linkKey, period:"YYYYMM", feeType, category, amountYuan|amountCents, ...可选}`——除三强制外**全部可选不阻断**（itemName/reason/sourceFile/sourceRow/remark 等）；**分类为建议集+本地自建**（#16：「提供分类建议，但允许本地自建分类」——后端只强制非空）；金额整数分契约（#65 P1-a，amountYuan 二选一、两形态并存必须一致、负数=冲销）；**同指纹重复拒绝 422 detail_refused**（同键同类别同金额同项目名——铁律 5，拒绝文案携带既有 id）；created_by 由令牌层级诚实承担（admin/declarer/http）。422 携带三强制违规文案。

## POST /api/fees/details/{id}/status — 明细核实裁定（管理级人类通道）

`{status:"confirmed|rejected|unmatched", rejectReason}`——rejected **必填原因**；核实留痕（verified_by/verified_at/reject_reason）。401/403/404/422 同全局纪律。

## GET /api/fees/details/reconcile?linkKey=&period=&feeType= — 差额提示（只提示不阻断）

`{detailSumCents, rows, engineCentsAvailable, systemCents, deltaCents, note}`——明细侧（SQLite）恒可答；系统表侧由 DuckDB 引擎回答该键该账期的分析层金额；**差额只提示不阻断**（可能明细待补，也可能系统表待解释——明细永不覆盖系统表）。默认构建：engineCentsAvailable=false + systemCents/deltaCents 为 null + 诚实 note（**不造零不 501**——半答也是答）。键在分析层无行：systemCents=null +「明细有、系统无」警示 note。

## GET /api/fees/site-code-aliases — 站址编码映射台账（#12 验收 1/5）

读开放——映射是证据（canonical/alias/source/confidence/createdBy/createdAt）。

## POST /api/fees/site-code-aliases — 建立编码映射（#12 人工确认通道）

申报级。`{canonical, alias, source, confidence}`——source ∈ 四费用类型或 manual（纯人工跨界映射）；confidence ∈ exact/rule/manual；**UNIQUE 三元组**（重复 422 携带既有 id）；**留痕**（createdBy=令牌层级/CLI --actor，验收 5：谁在何时建立）。

## POST /api/fees/site-code-aliases/scan — 扫描提议（只提议不落库）

两库站址**骨架比对**（大写+去噪声+数字段去前导零——"HLJ-0001"↔"hlj001" 实测形态），同骨架不同字面=疑为同站双码。**只提议不落库**——确认为建立动作（POST），无任何自动写入。引擎面：默认构建 501（站址宇宙在分析层）。申报级（POST 形态、读的心）。

## DELETE /api/fees/site-code-aliases/{id} — 移除编码映射（管理级治理）

错误映射不得永久毒化对账——移除是刻意的治理动作，Admin 令牌。

## GET /api/fees/cross-reconcile?year=&month= — 跨数据集对账表（#12 验收 2/3）

**甲方核对乙方**：电费台账与塔类详单按**主编码**归并（别名经映射桥重定向，痕迹随行）。逐站 `{canonical, class, electricityCents, towerCents, deltaCents(电−塔), elecRows, towerRows, zeroSide?, redirectedFrom?, attribution?}`；信封 `{onlyElectricity, onlyTower, both, aliasesApplied, note, warnings?}`。**零金额纪律（#31）**：对账输入是全域原语（有行的站全量参与，0 分含在内——展示原语的 top-500 截断不再能切掉零金额尾部）；`zeroSide` 标记「两侧有行但一侧金额 0」的行（排查方向=金额列，不是编码映射）；`warnings` 在一侧「有记录站全部零金额」（差额退化为对侧全额，不具参考价值）或一侧完全无行（对账前提不成立）时给出侧级健康告警，健康表静默。**三类分拣**：only_electricity=疑似自建站或编码不一致（#12 实测 7 站形态）/ only_tower=多为共享站（正常）/ both=可进一步核对；排序=异常优先（只在电费→只在塔表→双方），确定性 tiebreak。**带判定的三分类（#38）**：每行 only_* 差异携带 `attribution`（归因，主数据的结构化判读，非认定结论）——闭集六令牌：`shared_no_power_needed` 共享站且无供电（正常）/ `self_built_no_tower_needed` 自建站无塔租（正常）/ `retired_but_billed` 已退网或已拆仍计费（风险，应追回）/ `power_should_exist` 应供电但电费无记录（漏登风险，应补记）/ `code_mismatch` 疑两系统编码不一致（存在映射提案，先人工确认）/ `unknown` 缺站址主数据（无已确认关联或资源未带站址性质/供电方式/生命周期），**无法判断差异性质——不是正常**。判定读经确认关联的站址主数据三结构化列（迁移 v40：`site_nature`/`power_mode`/`lifecycle`，从基站/局站/机房导入表的「站址性质/供电方式/生命周期」列识别，口径外值警告 W002 并留空）；硬主数据优先于映射提案提示；主数据缺失时 **绝不默认归为正常**（洗白禁止），信封 `warnings` 携带 unknown 计数（「N 行差异 attribution=unknown……不是正常」）。both 行不携带 attribution（无可解释的差异）。**对账表是待核实差异而非认定结论**。参数校验先于引擎检查（400 先于 501）；合并为纯函数（同数据同输出）。CLI：`fees cross-reconcile --year [--month]`。

## POST /api/fees/period-probe — 账期内容探测（#15）

**只读探测，不落库**（无快照、无批次）：multipart `file`（.csv/.xlsx，同导入通道的格式与大小约束），返回该文件首 sheet 的**表型分类 + 三档账期识别**——导入前确认面板的数据面（「先选账期 → 内容识别 → 一致性校验」三档流程的第一步）。

```json
{
  "kind": "payroll",
  "kindLabel": "综维工资单",
  "year": 2026, "month": 9,
  "confidence": "strong",
  "basis": "「业务账期」列（30行一致）",
  "rowsSampled": 30
}
```

字段：`kind`/`kindLabel`（表型：tower-settlement 铁塔结算详单 / payroll 综维工资单 / wage-items 导入工资项 / electricity-ledger 电费台账 / perf-sheet 综维绩效单 / unknown 未识别）；`year`/`month`（识别账期，0 = 未知——电费台账仅列名月线索时 year=0）；`confidence`（`strong` A档专用账期字段 / `weak` B档间接线索 / `none` C档无账期字段）；`basis`（识别依据：字段名 + 一致行数 + 歧义注记）；`rowsSampled`（值探测采样行数，上限 50）。

与 `POST /api/fees/imports` 的三档校验**同一套探测逻辑**：A档识别账期与申报不一致 → 422 `fee_import_refused`（两个值都在 message 里）；B档月线索不一致 → 422（依据列名注明弱识别）；一致/无法识别 → 正常导入，批次携带 `periodSource`（auto/unverified/manual）与 `periodBasis`。申报覆盖通道：multipart `periodTrustDeclared=1`（人工确认采用申报值，不一致留痕于批次，`periodSource=unverified`）。

**跨账期重复导入拦截（#20）**：同一文件（SHA256 相同）已按**另一账期**存在 completed 批次时，导入默认 422 `fee_import_refused`（message 携带两个账期值、既有批次号与覆盖方式提示）——幂等键含账期，跨月重导会静默翻倍数据且批次/快照/分析层三处账期口径互相矛盾。显式覆盖：multipart `allowPeriodDuplicate=1`（覆盖事实留痕于批次 `period_basis` 与响应 `warnings`）。同账期重复导入不受影响——仍走幂等信息性应答（200 + `skippedDuplicate`）。**分析层分区跟随申报账期**（#20 根因 B 根治）：行内账期列与申报不一致的行按申报账期入分区，不一致计数进 `warnings` 与批次留痕，绝不静默改写分区归属。配套审计子命令 `fees verify-batches`（#20 + #32 两档，#33 起 dataProblems 携带 DQ 标注）：`periodProblems` = 逐 completed 批次核对 fee_batches ↔ 快照目录 ↔ 分析层分区三面账期 + 同 SHA 多账期家族报告（只报告不改动）；`dataProblems` = 数据有效性面，每个条目携带 `check`（DQ 编号）与 `severity`（high/medium）：`zero-amount`（DQ1/high，整批金额全 0，#29 形态指纹）/ `empty-site-code`（DQ2/high，站址编码全空，写入通道回归指纹）/ `single-site-code`（DQ3/high，整批单一站址，粘贴错误指纹——issue 字面的 distinct==0 在本 schema 不可达：site_code 永不为 NULL，全空形态归 DQ2）/ `row-count-cliff`（DQ4/medium，同类型相邻有数据账期行数不足上期一半——账期缺月不比较，首期无前驱不比较，同账期多批次按账期合并后比较，双构建可用）/ `all-negative-amount`（DQ5/high，整批负数）/ `majority-negative-amount`（DQ5/medium，负数行占比超 30% 而非全负）。DQ6（多候选金额列）按设计**不在 verify 侧**：金额列口径是导入时面（#29 的 `amountColumn` + 同族变体 warnings 在持有表头证据时回答），每次 verify 重解析快照会以更少上下文重做导入面——映射如实文档化，不伪造。`problems` = 两档合并（pre-#32 消费者兼容）；默认构建 `dataChecksSkipped: true` 诚实声明数据面未运行（绝不静默跳过——DQ4 是纯 DB 面双构建照常运行，skip 标志只声明引擎侧未查）。problems 非空时 exit 非零。综维绩效单（perf-sheet）的「日期」列判定为填表时间，**不参与账期解析**（日期映射被丢弃并留痕）。

## POST /api/fees/links/scan — 费用-资源分级关联扫描（#63，#58 阶段 3）

对活跃 Parquet 编码全集跑三级匹配：**exact**（逐字节 → 自动确认）/ **rule**（fold / zero_pad 命名规则 → 自动确认 + 规则留痕）/ **fuzzy**（相似度 ≥ 0.8 → pending 提案，每编码至多 3 候选）。人类确认的链接**永不**被扫描覆写；数据面消失的编码其自动链接被清扫（人工决策除外）。返回计数信封 `{codes, exactLinks, ruleLinks, fuzzyProposed, unlinked, humanKept}`。幂等：重复扫描同判定、不重复提案。

## GET /api/fees/links?status=&grade=&code=&resource_id= — 关联列表

`status=confirmed/pending/dismissed`、`grade=exact/rule/fuzzy`、`code`、`resource_id` 过滤，最新在前。项含资源侧 join（`resourceName` / `resourceStatus`——资源被回滚删除后为空，记录键仍在）与审计字段（`grade/rule/similarity/source/decidedAt`）。非法参数 400。

## POST /api/fees/links/{id}/confirm — 人工确认模糊提案

仅 pending 可确认：`confirmed` + `source=human` + `decidedAt` 打点。该编码已有 confirmed 关联 → 409（先处理旧的）；非 pending → 409（message 含当前状态）；未知 id 404。**人类确认优先于自动扫描**（后续 scan 不覆写）。

## POST /api/fees/links/{id}/dismiss — 驳回模糊提案

仅 pending 可驳回：`dismissed`（审计保留，编码与资源对不再自动复提）；非 pending → 409；未知 id 404。CLI 等价：`netopslab fees links confirm|dismiss --id <id>`。

## GET /api/fees/risk-map?feeType=&year=&month= — 站点风险地图数据面（#18——十场景线索按站址聚合 + confirmed 关联资源坐标）

**云端只做渲染能力，经纬度数据由本地资源表自带**（#16 职责边界同款）。参数校验先于引擎检查：非法 `feeType` / 越界 `year`（2000–2100）/ `month`（1–12）→ 400；**默认构建 501 `engine_not_built`**。数据面：十场景（九内置 + custom-rules）逐个执行按站址聚合，站址经 confirmed 关联（`fee_site_links`）连到资源，读取 migration v20 起的结构化 `geo_lat`/`geo_lng` 属性键（导入器把复合「经纬度」列按中国边界启发式拆分入库）。着色语义：红=已核实（verified，人工确认的真问题）/ 黄=待核实（pending）/ 绿=无线索（confirmed 关联但该账期十场景无线索）。

返回 `RiskMapReport`：`points[]` 按站址升序确定序（每点 `lat`/`lng` 可为 null——关联资源无坐标时诚实空值，前端不落点不猜零；`clueCount`/`pending`/`verified`/`dismissed` 按人工裁定状态计数；`scenarios` 该站线索的场景集合）；`linkedSites`/`geoSites` 诚实覆盖面计数（关联无坐标的站址是地图的已知缺口，报告而非静默缩面）：

```json
{
  "feeType": "electricity", "year": 2026, "month": 2,
  "points": [
    {"siteCode": "HLJ-01", "lat": 45.75, "lng": 126.63, "clueCount": 1,
     "pending": 1, "verified": 0, "dismissed": 0,
     "scenarios": ["idle-billing"], "resourceKey": "HLJ-01"}
  ],
  "linkedSites": 2, "geoSites": 1
}
```

前端（FeesRiskMap 组件）：有坐标点时地图面板渲染（MapLibre 圆点层 + 点选弹窗：站址/资源/线索计数/场景；maplibre 库+CSS+worker URL 同点动态 import——首屏零成本，主仓 W5-D 同款纪律）；**无坐标数据时面板整体不渲染**（验收 2：列表照常）。

## GET /api/fees/risks/{scenario}?feeType=&year=&month=&status=&diffCents=&ratio=&z=&minSites=&limit=&maxEvidence=&includeDecided= — 费用风险十场景（#58 阶段 4；#114 加场景 7；#118 加场景 8；#2 加场景 10 custom-rules——逐条执行已启用的 L1 六元组规则）

**固定场景 + 显式参数，调用方传不了 SQL**。`scenario` 封闭集：`retired-billing`（退租后仍在计费）/ `duplicate-settlement`（跨批次重复结算）/ `ledger-mismatch`（合同账单金额差异）/ `spike`（电费环比异常上涨——**#11 补报标注**：线索账期内含购电周期 > 70 天〔正常周期×2〕的行时，note 标注「疑似长期不报后集中补报」并携带最长周期天数，环比涨幅可能是攒报的计量后果而非趋势异常；标注而非排除，攒报是否合理由人判断；依赖购电起止日列，旧批次无列则退化为原行为）/ `outlier`（同类站月度金额离群——对总额而非单位成本计算，note 携带规模告诫）/ `unlinked`（零资源计费）/ `idle-billing`（有费无量——已确认关联的站址该账期指标点数为零且费用仍在流，跨层组合，采集缺失与空置不可区分，note 恒携带告诫）/ `missing-billing`（有量无费——已确认关联的在网/在建资源该账期窗口内有指标采集点而费用侧无该类型计费记录，跨层组合 #118，账期无记录 ≠ 漏收认定，note 恒携带告诫）。参数校验先于引擎检查：非法 `scenario` / `feeType` / 越界 `year`（2000–2100）/ `month`（1–12）→ 400；`ratio`/`z` 必须为正数、`minSites`/`limit`/`maxEvidence` 必须为正整数、`diffCents` 必须为非负整数 → 400。**默认构建 501 `engine_not_built`**（与查询预设同契约；`month` 缺省 = 整年各月）。

返回 `RiskResult`（金额均为整数分；线索按 metric 降序、(站址, 账期) 升序确定序；`evidence` 为行级证据锚点，可回读快照原文）：

```json
{
  "scenario": "duplicate-settlement",
  "scenarioCn": "跨批次重复结算",
  "feeType": "electricity",
  "year": 2024,
  "month": 5,
  "clues": [
    {
      "siteCode": "S-DUP",
      "year": 2024,
      "month": 5,
      "metricValue": 2,
      "metricLabel": "batches",
      "threshold": 1,
      "thresholdLabel": ">1 批次",
      "evidence": [
        {"sourceFile": "<sha256>_dup-a.csv", "sourceRow": 2, "importBatch": "fee_…", "amountCents": 10000},
        {"sourceFile": "<sha256>_dup-b.csv", "sourceRow": 2, "importBatch": "fee_…", "amountCents": 10000}
      ],
      "evidenceTotal": 2
    }
  ],
  "total": 1,
  "excluded": 0,
  "note": "线索口径：同一 (站址, 账期) 存在多个导入批次的计费记录——批次按设计共存不合并，账单存在不构成重复付款认定，请人工核实"
}
```

阈值默认值（显式参数化，见 fees-layer.md §8）：`status=停用,退网`、`diffCents=0`、`ratio=0.30`、`z=3.0`、`minSites=8`、`limit=100`（上限 1000）、`maxEvidence=10`（上限 50）。`spike`/`outlier` 的 `excluded` 为被排除的跨批次 (站址, 账期) 数——重复行令窗口/分布非确定（#59），跨批次对属 duplicate-settlement/ledger-mismatch 的线索域，排除即声明。

**信封 `warnings[]`（#21 排除主导 + #27 未分析）**：非阻塞结构化告警，note 携带同文（CLI/MCP 读 note，UI 以告警条渲染数组）。两类来源：① **排除主导**（#21）——`0 条线索 + 有排除数`或排除占比过半时，「无法分析」绝不伪装成「分析后无异常」；② **跨层场景前置检查「未分析」（#27）**——四个跨层组合场景（`retired-billing`/`unlinked`/`idle-billing`/`missing-billing`）在**交叉面整体为空**时不产出线索、以 note+warnings 说明原因与下一步：`unlinked` 的资源库为空（可关联资源计数 0——否则全部编码必然「无关联」，是 200+ 条噪音而非发现；**部分导入照常分析**，导入多少是运营决策）；`retired-billing` 无任何已确认关联（宇宙为空，0 条线索不得伪装成「干净」）；`idle-billing` 全库无任何指标点（否则全部已关联站址必然零点，是数据覆盖问题而非发现）；`missing-billing` 三重门（无已确认关联 / 全库无指标点 / 费用侧该类型账期无任何记录——空费用面会让全部有量资源命中，属数据范围问题而非漏收线索）。前端据此渲染第四态空状态（`not-analyzed`，告警色——与「干净零」可区分）。**排除分类计数（#30，仅 spike）**：`excludedBreakdown{crossBatch, zeroBase}`——两类排除各带各的计数与排查动作（跨批次对 → verify-batches；零基期对 → 基期金额核查，普遍为 0 即 #29 形态查金额列映射）；排除告警文案按场景分化（spike 双动作 / ledger-consistency 说明 #8 规则口径排除 / 其余保持查重复批次指引），`excluded` 总数算术不变。

**人工裁定的消费语义（#119，migration v33）**：线索可携带 `decision`（`verified` 已核实属实 / `dismissed` 已排除）与 `decidedAt`。默认输出 = pending + verified（verified 是运营的已知问题台账，标记可见）；**dismissed 默认隐藏、计数入信封 `dismissed`**——绝不静默丢弃（#83 教义），重跑不再重复出现（#69 的 PairDecided 教义：已裁定的线索保持已裁定）。`includeDecided=1|true` 返回全量标记集（审计视图）；`includeDecided` 非法值 → 400。裁定按 (scenario, feeType) 隔离——驳回一条 missing-billing 线索不影响同站的 spike 线索或其铁塔租费口径。

## POST /api/fees/risk-clue-decisions — 风险线索人工裁定（#119）

**人类通道**（MCP 无此写面——AI 不得替人核实线索，与 attribute-changes apply / links confirm 同一治理红线）。body：

```json
{"scenario": "duplicate-settlement", "feeType": "electricity", "siteCode": "S-DUP",
 "year": 2024, "month": 5, "status": "dismissed", "note": "合同台账+账单台账，双份合法"}
```

- `status` 封闭集：`verified`（属实——线索保持可见并标记，构成已知问题台账）/ `dismissed`（排除——默认输出隐藏、信封计数）；`note` 可选（审计留档）。
- **UPSERT**：同一 (scenario, feeType, siteCode, year, month) 再裁定翻转状态并刷新 `decidedAt`（人可随证据变化改判，台账保留最新裁定）。
- 校验先于写入：非法 `scenario`/`feeType`/`status`、缺 `siteCode`、越界 `year`（2000-2100）/`month`（0-12）→ 400。
- **不依赖分析引擎**：默认构建即可裁定（决策是纯审计行，无 FK——站址编码是代码空间事实，同 fee_site_links 的 fee 侧裁决；批次回滚不触碰，W005/W006 教义），抑制在下一次引擎运行的场景查询时生效。
- 200 返回完整裁定项 `{id, scenario, scenarioCn, feeType, siteCode, year, month, status, note, decidedAt}`（`id` 形如 `rcd_<uuid>`）。

## GET /api/fees/risk-clue-decisions?scenario=&feeType=&status=&siteCode=&year=&page=&pageSize= — 风险线索裁定台账（#119）

**审计视图**：全部人工裁定，`decidedAt` 最新优先（同秒并列回退 id 序——秒粒度内无次序契约）。过滤器全部可选且为封闭集校验（非法 `scenario`/`feeType`/`status` → 400）；分页走 page/pageSize（limit/offset 亦收，缺省 20、上限 100，PROJECT_BRIEF §9）。返回 `{items[], total, limit, offset}`，items 元素同 POST 的裁定项形状。

## GET /api/fees/rules?enabled= — 可配置风险规则列表（#2）

**读开放**（规则集本身是证据——WHAT fires 不是秘密）。`enabled=1` 仅返回已启用规则；缺省返回全部（观察态停用规则可见，供人类评审待启清单）。返回 `{items[], total, enabledOnly}`，items 元素为完整六元组形状（见 POST 的 201 形状）。

## POST /api/fees/rules — 创建可配置风险规则（#2）

**AI/运营通道**（申报级令牌即可——两级写门禁的第一层；`fee_rules_create` MCP 工具同款服务调用）。**新规则默认停用（观察态）**：创建后不参与任何分析，需人类经管理级 `enable` 路由或 CLI `fees rules enable` 启用（MCP 无启用/停用面——与 #119 人工裁定同一治理红线）。body：

```json
{"name": "电费单月超阈值", "feeType": "electricity", "metric": "amount",
 "aggregation": "sum", "dimension": "site_month", "comparator": "gt",
 "threshold": 500000, "windowMonths": 1, "severity": "high",
 "description": "单站单月电费合计超过 5000 元（500000 分）"}
```

- **封闭枚举**（v37 CHECK + 服务双重校验）：`metric ∈ {amount 金额, rows 行数}`、`aggregation ∈ {sum, avg, max, count}`（rows 度量固定 count——行数即聚合）、`dimension` 固定 `site_month`（站址×账期）、`comparator ∈ {gt, gte, lt, lte, mom_ratio 环比涨幅比}`、`severity ∈ {high, medium, low}`；违例 → **422 rule_refused**（message 携带收到的全部枚举值）。
- **数值边界**：`threshold ≥ 0`（金额类=分、行数类=行数、环比类=涨幅比如 0.30 表示 +30%）；`windowMonths` 1–12（仅 mom_ratio 用作基期回看月数，缺省 1，0 视为 1）；`name` 唯一且 ≤120 字、`description` ≤500 字。
- **阈值变更留痕**：每次创建落 `created` 审计行（actor=令牌层级：admin/declarer；门禁关闭时 http）。第一期没有更新通道——**改阈值 = 停用旧规则 + 建新规则**，全程审计可追溯。
- 201 返回 `{id, name, feeType, metric, aggregation, dimension, comparator, threshold, windowMonths, severity, description, enabled:false, createdAt, updatedAt}`（`id` 形如 `rule_<uuid>`）。重名 → 422（name 唯一约束的诚实话术）。

## POST /api/fees/rules/{id}/enable — 启用规则（#2）

**人类通道**（管理级令牌——两级门禁的第二层；申报令牌 403，与 #119 裁定同款）。审计行 `enabled` + `{"enabled":{"from":0,"to":1}}` + actor=admin。200 返回启用后的完整规则形状（`enabled:true`）。不存在 → 404。

## POST /api/fees/rules/{id}/disable — 停用规则（#2）

**人类通道**（管理级令牌）。停用即从 custom-rules 场景消失（已在案线索的裁定不受影响——规则与裁定是两个账本）。审计行 `disabled` + actor=admin。200 返回停用后的完整规则形状（`enabled:false`）。**改阈值的正确姿势**：`disable` 旧规则 → `POST /api/fees/rules` 建新规则 → `enable` 新规则——三步各自留痕，阈值的历史口径完整可追溯（这正是第一期没有 update 通道的原因）。

## GET /api/fees/rules/{id}/audit — 规则动作台账（#2）

**读开放**（留痕的可见性是留痕的意义）。返回 `{ruleId, items[], total}`，items 元素 `{id, ruleId, action, actor, changes, at}`，`action ∈ {created, enabled, disabled}`，`changes` 为变更 JSON（created 行为空），最新优先。不存在 → 404。

**custom-rules 场景消费语义**：`GET /api/fees/risks/custom-rules?feeType=&year=[&month=]` 逐条执行该费用类型下全部已启用规则（引擎在 SQL 构建前对每条规则重校验封闭枚举——规则文本永不进 SQL）；线索携带规则溯源（`metricLabel`=规则口径、`thresholdLabel`=`rule: <规则名>（<聚合> <度量> <算子> <阈值>）`、note 携带 severity+description+教义告诫）、行级证据锚点与已启用规则数的口径 note。默认构建 → 501 engine_not_built（规则表管理不受影响，仅引擎执行需要 duckdb 构建）。

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
