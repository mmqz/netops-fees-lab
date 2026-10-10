# NetOps Lab — 数据模型（Data Model）

> Schema 与代码同步维护：本文 DDL 摘自 `internal/repo/migrations.go` / `migrations_v4.go` / `migrations_v5.go`（**实现即规范**）。当前 schema version = **9**（v5 = 仿真工作台表，见文末 DDL v5 一节；v6 geo 列、v7 port 类型、v8 port 换键与 v9 base_station 类型为后续追加节，历史章节保持原貌）。

## 1. ID 与时间戳约定

- **ID**：TEXT。P1 聚合 `前缀_<uuid>`：`res_`（资源）、`rel_`（关系）、`src_`（来源引用）、`bat_`（导入批次）、`iss_`（问题），生成见 `internal/domain` 的 `NewID`；P2 仿真实体 `前缀-<n>`（全局序列，见文末 §6）。工作区为 UUID。
- **自然键**（natural key）：资源唯一业务键——device 用 `device_code`、ip_pool 用 `pool_name`、circuit 用 `circuit_code`、port 用复合键 `device_name|port_name`（v8 换键，见文末 v7/v8 节）、base_station 用**条件键** `base_station_code`（编码空时兜底 `city|base_station_name`，跳空值，记 W004；v9 落地，见文末 v9 节；复合键拼接规则与稳定性评估见 [natural-keys.md](natural-keys.md)）。表级唯一约束 `UNIQUE (resource_type, natural_key)`，幂等重导依赖它。
- **时间戳**：一律 **UTC RFC3339 字符串**（如 `2026-10-09T12:04:40Z`）。
- **JSON 列**：`attributes` / `raw` / `evidence` / `mapping` 均为 TEXT 存 JSON 对象（默认 `'{}'`）。

## 2. 迁移机制（migrations）

- 迁移 = 有序结构体列表 `{Version, Name, SQL}`；`repo.Open(path)` 打开库后自动应用未执行版本。
- 每个版本在**单事务**内执行 SQL + 写 `schema_migrations(version, name, applied_at)`；已应用版本跳过 → `Open` 幂等，重启安全。
- 迁移注册方式有二：`migrations.go` 的字面量列表（v1–v3），或新文件 `init()` 追加 + 按版本排序（v4 `batch_workbook_path` 与 v5 `simulation_tables` 均此模式，保持主文件不动）。
- SQLite 连接 DSN：`file:<path>?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)`（外键必须逐连接开启）。

## 3. DDL v1（Version 1–3，全文）

### import_batches（导入批次）

```sql
CREATE TABLE import_batches (
  id TEXT PRIMARY KEY,
  workbook_name TEXT NOT NULL,
  workbook_sha256 TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('analyzing','awaiting_mapping','importing','completed','failed','rolled_back')),
  mapping TEXT NOT NULL DEFAULT '{}',          -- confirmed mapping JSON
  rule_version TEXT NOT NULL,
  rows_total INTEGER NOT NULL DEFAULT 0,
  rows_imported INTEGER NOT NULL DEFAULT 0,
  rows_skipped INTEGER NOT NULL DEFAULT 0,
  rows_failed INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT
);
```

- `status` 状态机：`analyzing → awaiting_mapping → importing → completed | failed`；回滚 → `rolled_back`。
- 计数四元组（总/成功/跳过/失败）满足 `imported + skipped + failed == rows_total`（= 所有 import sheet 的数据行数；验收 ACC-1）。
- v4 增加 `workbook_path TEXT`（执行映射时重读工作簿用；老批次为 NULL，无需回填）。

### resources（资源）

```sql
CREATE TABLE resources (
  id TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('device','ip_pool','circuit')),
  natural_key TEXT NOT NULL,                   -- device_code | pool_name | circuit_code
  name TEXT NOT NULL,
  attributes TEXT NOT NULL DEFAULT '{}',       -- normalized JSON, keys = 字段名
  status TEXT,
  first_batch_id TEXT NOT NULL REFERENCES import_batches(id),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (resource_type, natural_key)
);
```

- `attributes`：标准化后的字段（非键、非枚举、非空），如 device 的 `{"city":"杭州","vendor":"华为","model":"NE40E","mgmt_ip":"10.10.1.1"}`；键集合即目标 schema（§6）字段名。
- `status`：导入时枚举校验（在网/停用/在建/退网）；未知值触发 W002 且**不落库**（属性丢弃，行照常导入）。
- `first_batch_id`：溯源锚点——重导入 skip 不改写该字段，资源始终指向其创建批次。

### relations（关系，仅三类）

```sql
CREATE TABLE relations (
  id TEXT PRIMARY KEY,
  relation_type TEXT NOT NULL CHECK (relation_type IN ('assigned_to','a_end','z_end')),
  from_resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  to_resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  attributes TEXT NOT NULL DEFAULT '{}',
  first_batch_id TEXT NOT NULL REFERENCES import_batches(id),
  created_at TEXT NOT NULL,
  UNIQUE (from_resource_id, to_resource_id, relation_type)
);
```

派生关系（且仅有这些）：`ip_pool --assigned_to--> device`、`circuit --a_end--> device`、`circuit --z_end--> device`。**绝不从名称相似度推断关系**。

### source_refs（来源引用 / 溯源）

```sql
CREATE TABLE source_refs (
  id TEXT PRIMARY KEY,
  entity_kind TEXT NOT NULL CHECK (entity_kind IN ('resource','relation')),
  entity_id TEXT NOT NULL,
  batch_id TEXT NOT NULL REFERENCES import_batches(id) ON DELETE CASCADE,
  workbook TEXT NOT NULL,
  sheet TEXT NOT NULL,
  row_number INTEGER NOT NULL,
  raw TEXT NOT NULL DEFAULT '{}',              -- original row as {header: value}
  UNIQUE (entity_kind, entity_id, batch_id)
);
CREATE INDEX idx_source_refs_entity ON source_refs(entity_kind, entity_id);
```

`row_number` 为 **1-based Excel 行号**（表头=第 1 行，数据从第 2 行起）；`raw` 保留整行原始值（表头→值）。问题/报告的“来源证据”由此解析。

### import_row_results（行级记账）

```sql
CREATE TABLE import_row_results (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  batch_id TEXT NOT NULL REFERENCES import_batches(id) ON DELETE CASCADE,
  sheet TEXT NOT NULL,
  row_number INTEGER NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('imported','skipped','failed')),
  entity_id TEXT,
  rule_code TEXT,                              -- set for failed/skipped rows
  message TEXT,
  UNIQUE (batch_id, sheet, row_number)
);
```

每个数据行**恰好一条**（`UNIQUE (batch_id, sheet, row_number)`）：`imported`（携带 entity_id）、`skipped`（重复，消息引用既有批次）、`failed`（携带 rule_code + message）。

### issues（数据质量问题）

```sql
CREATE TABLE issues (
  id TEXT PRIMARY KEY,
  batch_id TEXT,                               -- NULL ⇒ set-level (cross-batch) issue
  rule_code TEXT NOT NULL,
  rule_version TEXT NOT NULL,
  severity TEXT NOT NULL CHECK (severity IN ('error','warning','info')),
  entity_kind TEXT,
  entity_id TEXT,
  source_ref_id TEXT,                          -- NULL ⇒ application-generated
  message TEXT NOT NULL,
  evidence TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);
CREATE INDEX idx_issues_batch ON issues(batch_id);
CREATE INDEX idx_issues_rule ON issues(rule_code);
```

- `batch_id IS NULL` ⇒ 集合级（跨批次）问题，校验引擎重算时**整类删除重建**（幂等）。
- `source_ref_id`：行级 W 警告指向已建资源的 source_ref；行级 E 失败行没有实体，故为 NULL 且 `evidence` 内含 `sheet`/`row_number`；集合级问题两者皆 NULL → 报告中标记 `applicationGenerated: true`。
- `rule_version` 固定 `"1.0.0"`（`domain.RuleVersion`），规则目录变更时升版。

### schema_migrations（迁移账本）

```sql
CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  applied_at TEXT NOT NULL
);
```

## 4. 实体关系示意

```
import_batches 1 ──── n resources ──── n relations（端点为 resources）
       │                    │
       │                    └── n source_refs（entity_kind=resource/relation）
       ├── n import_row_results（每数据行一条）
       └── n issues（batch_id 可空 = 集合级）
```

## 5. 设计要点

- **删除语义**：`relations`/`source_refs`/`import_row_results`/`issues` 对批次 `ON DELETE CASCADE`（行级数据随批次回滚删除）；`relations` 端点对资源 `ON DELETE CASCADE`（删资源自动清关系）。回滚 API 手工按序删除并断言计数归零。
- **回滚保护**：批次资源被其他批次关系引用时拒绝（409 `batch_in_use`），防止悬空引用——由 `importer.Rollback` 的 `ExistsFromOtherBatch` 检查保证，而非 DDL。
- **行号约定统一**：evidence、source_refs、row_results 全部用 1-based Excel 行号，前端“来源”列可直接展示。

---

# DDL v5 — 仿真工作台（Phase 2）

> Migration `5` name `simulation_tables`（`internal/repo/migrations_v5.go`，v4 之后经 `init()` 追加 + 按版本排序注册，机制同 v4）。当前 schema version = **5**。DDL 与 `PROJECT_BRIEF_P2` §3 逐字一致（含全局 `sequences` 表修订版）。

## 6. 仿真实体约定

- **ID**：`前缀-<n>`，由 `sequences` 表**全局**分配（每类一行，`UPDATE … SET last_value = last_value + 1 WHERE kind=? RETURNING last_value`，在调用方事务内执行——回滚即归还）。前缀：`vd-`（虚拟设备）、`if-`（接口）、`ln-`（链路）、`cs-`（变更集）、`run-`（校验运行）、`cp-`（检查点）。**全局唯一、按类单调递增**（非按工作区）——这是 v5 的修订决策：原始设计为按工作区序列，但所有 sim 表都是 `id TEXT PRIMARY KEY`（全局唯一约束），第二个工作区物化时必然撞 id；全局序列保住"人可读 id"与"全局唯一"两个性质（`cs-`/`run-` 单调 ⇒ "工作区最新" = 该工作区数值最大的 id）。
- **工作区 id**：`uuid.New()` 字符串（与 P1 `res_/bat_` 前缀体系区分——仿真实体按类计数，工作区按 UUID）。
- **nullable 约定（域层表示 vs 存储表示）**：
  - `interfaces.ipv4_addr` / `ipv4_prefix`：存储 NULL = 未配置；Go 域结构用 `""` / `-1` 表示同一事实（CHECK 无约束，引擎保证同设同空）；
  - `check_runs.changeset_id` NULL = 手动运行（未绑定变更集）；`changesets.reverted_at` NULL = 未撤销；
  - `changeset_ops.inverse_json` NULL = 失败变更集（全部行）；
  - API/拓扑负载用 `""` / `-1` 渲染未配置接口，**状态哈希**与 §3 规范 JSON 用 `null`（两套表示故意不同：负载面向 UI 字符串字段，哈希面向规范 JSON）。
- **状态哈希（workspace_hash）**：仿真状态的确定性指纹 = 对规范 JSON 的 sha256（hex）。规范 JSON：设备按 `code` 排序、接口按 `name` 排序、链路按 `(aDevice, aInterface, zDevice, zInterface, label)` 排序；未配置地址渲染 `null`；字段顺序固定（`{"devices":[{"code","name","status","interfaces":[{"name","status","ipv4Addr","ipv4Prefix"}]}],"links":[{"aDevice","aInterface","zDevice","zInterface","status","label"}]}`）。同状态 ⇒ 同哈希——撤销/检查点恢复的"精确回到基线"验证依赖它。
- **`check_runs.topology_json`**：运行时的规范边快照（全部链路含 down，排序同哈希 links 数组，字段 aDevice/aInterface/zDevice/zInterface/status/label）——diff 无需重放操作即可重算可达性。
- **检查点文件**：`<dbdir>/checkpoints/<workspaceId>/<cpId>.db`（Online Backup API 全库快照）；`file_path` 存**相对路径**；删除工作区时级联删行 + 尽力清理目录。

## 7. DDL v5 全文

```sql
CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','archived')),
  source_json TEXT NOT NULL DEFAULT '{}',     -- {"batchIds":[…]} lineage
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE sequences (
  kind TEXT NOT NULL CHECK (kind IN ('vd','if','ln','cs','run','cp')),
  last_value INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (kind)
);

CREATE TABLE virtual_devices (
  id TEXT PRIMARY KEY,                        -- 'vd-<n>' (global sequence)
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  resource_id TEXT REFERENCES resources(id) ON DELETE SET NULL,
  code TEXT NOT NULL,                         -- device_code from the resource
  name TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'up' CHECK (status IN ('up','down')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (workspace_id, code)
);
CREATE INDEX idx_virtual_devices_ws ON virtual_devices(workspace_id);

CREATE TABLE interfaces (
  id TEXT PRIMARY KEY,                        -- 'if-<n>'
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  device_id TEXT NOT NULL REFERENCES virtual_devices(id) ON DELETE CASCADE,
  name TEXT NOT NULL,                         -- 'eth0', 'eth1', …
  status TEXT NOT NULL DEFAULT 'up' CHECK (status IN ('up','down')),
  ipv4_addr TEXT,                             -- dotted quad, normalized; NULL = unconfigured
  ipv4_prefix INTEGER,                       -- 0..32; NULL when addr NULL
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (workspace_id, device_id, name)
);
CREATE INDEX idx_interfaces_ws ON interfaces(workspace_id);
CREATE INDEX idx_interfaces_device ON interfaces(device_id);

CREATE TABLE links (
  id TEXT PRIMARY KEY,                        -- 'ln-<n>'
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  a_interface_id TEXT NOT NULL REFERENCES interfaces(id) ON DELETE CASCADE,
  z_interface_id TEXT NOT NULL REFERENCES interfaces(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'up' CHECK (status IN ('up','down')),
  label TEXT NOT NULL DEFAULT '',             -- circuit_code when derived
  resource_id TEXT REFERENCES resources(id) ON DELETE SET NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  CHECK (a_interface_id <> z_interface_id)
);
CREATE INDEX idx_links_ws ON links(workspace_id);

CREATE TABLE changesets (
  id TEXT PRIMARY KEY,                        -- 'cs-<n>'
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  description TEXT NOT NULL DEFAULT '',
  trigger TEXT NOT NULL CHECK (trigger IN ('user','undo','checkpoint_restore')),
  status TEXT NOT NULL DEFAULT 'applied' CHECK (status IN ('applied','reverted','failed')),
  ops_applied INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  reverted_at TEXT
);
CREATE INDEX idx_changesets_ws ON changesets(workspace_id, id);

CREATE TABLE changeset_ops (
  changeset_id TEXT NOT NULL REFERENCES changesets(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,                       -- 1-based, application order
  op_code TEXT NOT NULL,
  op_json TEXT NOT NULL,                      -- forward op, typed schema
  inverse_json TEXT,                          -- inverse op; NULL in failed changesets
  result TEXT NOT NULL CHECK (result IN ('applied','failed','not_attempted')),
  PRIMARY KEY (changeset_id, seq)
);

CREATE TABLE check_runs (
  id TEXT PRIMARY KEY,                        -- 'run-<n>'
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  changeset_id TEXT REFERENCES changesets(id) ON DELETE SET NULL,
  trigger TEXT NOT NULL CHECK (trigger IN ('manual','api','mcp','auto_after_ops')),
  rule_version TEXT NOT NULL,
  started_at TEXT NOT NULL,
  duration_ms INTEGER NOT NULL,
  score INTEGER NOT NULL,
  topology_json TEXT NOT NULL                 -- canonical edge snapshot (see §8)
);
CREATE INDEX idx_check_runs_ws ON check_runs(workspace_id, id);

CREATE TABLE check_results (
  run_id TEXT NOT NULL REFERENCES check_runs(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  check_code TEXT NOT NULL,                   -- CHK-001…CHK-005
  severity TEXT NOT NULL CHECK (severity IN ('error','warning','info')),
  passed INTEGER NOT NULL,                    -- 1 = check clean, 0 = finding
  entity_kind TEXT,                           -- 'workspace'|'device'|'interface'
  entity_id TEXT,
  message TEXT NOT NULL,                      -- English, deterministic
  detail_json TEXT NOT NULL,                  -- typed per-check detail
  PRIMARY KEY (run_id, seq)
);

CREATE TABLE checkpoints (
  id TEXT PRIMARY KEY,                        -- 'cp-<n>'
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  file_path TEXT NOT NULL,                    -- relative to the lab DB directory
  workspace_hash TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX idx_checkpoints_ws ON checkpoints(workspace_id, id);
```

## 8. 实体关系示意（仿真部分）

```
workspaces 1 ──── n virtual_devices ──── n interfaces ──── n links（两端指向 interfaces）
       │                  │（resource_id 溯源 P1 resources）
       ├─ 1 sequences（全局，vd/if/ln/cs/run/cp 各一行）
       ├── n changesets ──── n changeset_ops（seq + op_json/inverse_json/result）
       ├── n check_runs ──── n check_results（发现 + 基线行）
       └── n checkpoints（快照文件 + workspace_hash）
check_runs.changeset_id → changesets（SET NULL：自动校验运行绑定其触发变更集）
```

- 仿真表删除语义：对工作区 `ON DELETE CASCADE`（`repo.DeleteWorkspace` 按表级联删除并返回计数；检查点目录尽力 `os.RemoveAll`）。
- 索引 `idx_changesets_ws(workspace_id, id)` 等按**字典序**排 id——仓库层的"最新/倒序"查询用 `ORDER BY LENGTH(id) DESC, id DESC` 兜数值序（`cs-9 < cs-10`）。
- 工作区支持多工作区共存（schema 全面携带 workspace_id + 全局 id）；**单工作区活跃**是受支持的使用模式（多工作区并发写显式 out-of-scope）。

---

# DDL v6 — 设备地理坐标（geo pipeline）

> Migration `6` name `device_geo_columns`（`internal/repo/migrations_v6.go`，经 `init()` 追加 + 按版本排序注册，机制同 v4/v5）。**当前 schema version = 6**（本文之上的 v5 章节描述保持历史原貌）。改动仅为两列可空 `REAL`，无默认值、无回填；v6 之前的库打开后自动升级，旧行坐标为 NULL（无位置）。

## 9. 设备地理字段（可选，展示元数据）

设备资源与虚拟设备获得**可选**地理坐标，全链路为：导入列 → 资源属性 → 物化 → 拓扑 API（`geoLat`/`geoLng`）。**红线：geo 绝不进入状态哈希**（坐标是展示元数据——在地图上挪动一台设备不允许改变哈希/校验/diff；回归测试 `TestWorkspaceHashIgnoresGeo` 双层证明：含坐标与不含坐标的同业务数据物化出逐字节相同的哈希）。

### 9.1 目标 schema 三个新字段（设备 schema 专属，全部可选）

| 字段 | 标签 | Kind | 约束 | 同义词（建议引擎） |
| --- | --- | --- | --- | --- |
| `geo_lat` | 纬度 | `lat`（新） | float，[-90, 90] | 纬度, lat, LAT, GPS纬度, 北纬 |
| `geo_lng` | 经度 | `lng`（新） | float，[-180, 180] | 经度, lng, lon, LON, GPS经度, 东经 |
| `geo` | 经纬度 | `geo`（新） | 两个数字（顺序不限，见下） | 经纬度, 装机经纬度, 坐标, GPS坐标, A端装机经纬度, 位置 |

- E004 拒绝原因（中文）：纬度超界 → `不是合法的纬度（-90 到 90 的数字）`；经度超界 → `不是合法的经度（-180 到 180 的数字）`；组合列不是两个数字 → `经纬度应为两个数字（如 126.63,45.75）`。
- 浮点范围检查在 Kind 分支内实现（`FieldKind.CheckFormat`）；FieldSpec 的 `IntMin/IntMax` 整数区间机制不受影响。

### 9.2 组合列「经纬度」拆分（中国边界启发式）

设备表映射了 `geo` 列且 `geo_lat`/`geo_lng` **未同时**由各自分列提供时，导入器在行内校验**之前**拆分（拆分出的值随后过与分列完全相同的 KindLat/KindLng 校验）：

1. `f1 ∈ [73,136]` 且 `f2 ∈ [3,54]` → **(经度, 纬度) 序**——高德/百度惯例（如 `126.63,45.75`）；
2. 否则 `f1 ∈ [3,54]` 且 `f2 ∈ [73,136]` → **(纬度, 经度) 序**（如 `45.75,126.63`）；
3. 否则（境外/歧义坐标，不硬失败）：取**两种解读中合法的那一种**（纬度槽须 ∈[-90,90]、经度槽须 ∈[-180,180]）；两序皆合法时默认 `lng=f1, lat=f2`（高德惯例），仅当 f2 不可能是纬度而 f1 可以时交换。境外坐标照常接受。
4. 分隔符：半角逗号 / 全角逗号 / 分号 / 全角分号 / 空白（任意混用）。

两个窗口 [73,136] 与 [3,54] 不相交 → 启发式确定性。拆分结果以十进制原码串（`strconv.FormatFloat 'f' -1`）写入资源属性 `geo_lat`/`geo_lng`；原始 `geo` 属性同时保留（溯源无害）。**分列优先**：`纬度`/`经度` 两列同时提供时忽略组合列的拆分（原始值仍保留在属性里）。

### 9.3 DDL v6 全文

```sql
ALTER TABLE virtual_devices ADD COLUMN geo_lat REAL;  -- nullable，NULL = 无位置
ALTER TABLE virtual_devices ADD COLUMN geo_lng REAL;  -- nullable，NULL = 无位置
```

- 域层表示：`domain.VirtualDevice.GeoLat/GeoLng *float64`（nil = 无位置）；仓储层 `sql.NullFloat64 ↔ *float64`（`nullFloatPtr`/`floatPtrFromNull`），`Insert` 对 nil 写 NULL。`virtualDeviceColumns`/`scanVirtualDevice` 同步扩展，其余查询形状不变。
- 物化（`sim/materialize.go`）：解析资源属性 `geo_lat`/`geo_lng`（ParseFloat，仅在**合法且在界内**时采纳；导入已校验，此处忽略非法值属防御，永不硬失败）。两坐标相互独立，半对坐标保留合法的一半。
- 拓扑 API（`sim/reads.go`）：`TopologyNode.GeoLat/GeoLng *float64`，JSON `geoLat`/`geoLng`（`omitempty` —— 无位置节点不带键）。
- 撤销路径：`delete_device` 的逆操作快照 `DeviceSnap` 携带 `geoLat`/`geoLng`（camelCase；v6 之前的 inverse_json 行缺键解码为 nil，恢复成无位置——无害，geo 不入哈希）。
- **已知边界（诚实清单）**：检查点恢复重建被删设备走 `create_device`（封闭操作面无 geo 字段）→ 该设备坐标不随快照重建（哈希不受影响，撤销路径不受影响）；前端 2D/3D 地图视图随下一任务交付。

# DDL v7 — port 资源类型（真实电信端口表导入）

> Migration `7` name `port_resource_type`（`internal/repo/migrations_v7.go`，经 `init()` 追加 + 按版本排序注册，机制同 v4/v5/v6）。v7 引入 port 类型（`resources` 表的 `resource_type` CHECK 扩展 `'port'`；其余表不动——port 无 Phase 1 工作台实体、无派生关系）。**当前 schema version = 8**（v8 见下节；本文之上的 v6 章节描述保持历史原貌）。

## 10. 设备端口资源（port schema）

面向真实电信端口工作簿（`samples/telecom-port-multi-header.xlsx`：端口流量监控表 / 端口资源表两种表头形态）。**复合自然键（migration v8 后）**：`device_name + port_name`（端口名跨设备重复，必须连同设备名称才唯一；键的设备侧自 v8 起锚定设备**名称**而非 IP——设备重划 IP 不再分裂资源）；导入器按 spec 字段序以 `|` 连接键值落 `resources.natural_key`（如 `B2|bvi2.1008`）。**单键类型零扰动**：单元素 join 恒等原值，device/ip_pool/circuit 的键、E001 证据、幂等重导行为逐字节不变（同库回归测试钉死）。

### 10.1 目标 schema（全部可选字段除键外）

| 字段 | 标签 | Kind | 约束 | 同义词（节选） |
| --- | --- | --- | --- | --- |
| `device_name` | 设备名称 | string | **必填·自然键**（v8 起键的设备侧） | 设备名称, 网元名称, 设备名 |
| `port_name` | 端口名称 | string | **必填·自然键** | 端口名称, 端口名, 端口 |
| `device_ip` | 设备IP | ipv4 | **必填·非键值列**（v8 起从键降级，进 attributes） | 设备IP地址, 设备ip, 设备IP, 网元IP |
| `port_desc` / `port_type` / `admin_status` / `oper_status` / `device_type` / `district` / `record_date` | … | string | — | 端口描述 / 端口类型 / 管理状态 / 运行状态 / 设备类型 / 区县 / 日期 |
| `bandwidth_mbps` | 带宽Mbps | int | **无 IntMin**（真实表带宽可为 0） | 端口带宽, 端口速率(M), 速率 |
| `city` / `vendor` / `model` | … | string | — | 地市, 城市通用同义词族 |
| `geo_lat` / `geo_lng` / `geo` | 纬度/经度/经纬度 | lat/lng/geo | 同设备 §9.1 | 同设备 §9.1（基站端口表可携带坐标） |

设计决策：`device_ip` 保持 ipv4 值列（**非 device ref**、v8 起**非自然键**）——端口工作簿是自含导出，其设备行可能尚未入库，E003 引用失败会拒绝整表；端口与设备的关联由 IP 值匹配承担，不建派生关系（`relation_type` 枚举不动）。展示名（`NameField`）显式钉定为 `port_name`——`device_name` 是键的作用域分量，不是端口自身的名字。sheet 名含 `端口` 提示 port 类型（`设备端口表` 类含 `设备` 的名字仍按 Specs 顺序提示 device——确定性优先）。

### 10.2 重复表头行（多段导出拼接）

真实多表头工作簿会把表头行拼回数据区（样例 Sheet1 的第 3 行与首行表头逐字相同）。导入器在行规则之前检测：**≥3 个非空单元格且每个非空单元格等于其列头** → 判定重复表头行，行结果 `skipped`（message `跳过重复表头行（多段导出拼接）`，rule_code 留空，不记 issue）。另一种表头形态（表头 B）与表头 A 至少一个映射列不同 → 走正常行规则，E002/E004 失败且证据清晰（样例第 5/6 行即此路径）。

### 10.3 DDL v7 全文（FK 安全的表重建）

```sql
CREATE TABLE resources_new (
  id TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('device','ip_pool','circuit','port')),
  natural_key TEXT NOT NULL,
  name TEXT NOT NULL,
  attributes TEXT NOT NULL DEFAULT '{}',
  status TEXT,
  first_batch_id TEXT NOT NULL REFERENCES import_batches(id),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (resource_type, natural_key)
);
INSERT INTO resources_new (…全列…) SELECT …全列… FROM resources;
DROP TABLE resources;
ALTER TABLE resources_new RENAME TO resources;
```

SQLite 无法 ALTER CHECK，需重建表；`DROP TABLE resources` 在外键约束开启时会触发隐式 DELETE（relations 的 ON DELETE CASCADE、sim 表的 SET NULL——静默丢数据）。v7 为首个 `ForeignKeysOff` 迁移：专用连接**事务外** `PRAGMA foreign_keys=OFF` → 事务内重建 → `PRAGMA foreign_key_check` 空结果门禁（违例即整体回滚）→ commit 后恢复 FK ON 归还连接池。普通迁移路径行为逐字节不变。

# DDL v8 — port 自然键换稳定键（device_ip|port_name → device_name|port_name）

> Migration `8` name `port_natural_key_device_name`（`internal/repo/migrations_v8.go`，经 `init()` 自注册追加 + 按版本排序，机制同 v4–v7；migrations.go 字面量列表不动）。**当前 schema version = 8**。

## 11. 存量 port 行的键重算（就地 UPDATE，非重建）

R4 审查定性 P1：旧键 `device_ip|port_name` 的设备侧是 IP，设备重划 IP 后同一物理端口分裂为两个资源。v8 把键的设备侧换成设备**名称**（真实端口表无设备编码列，设备名称是唯一现成稳定列；新导入由 portSpec 字段序自动产出新键）：

- **重算规则**（逐行，旧键按第一个 `|` 拆分，port_name 含 `|` 时可完整还原）：新键 = `COALESCE(attrs.device_name, 旧键的 device_ip 分量) + '|' + 旧键的 port_name 分量`。存量行（v7 及以前导入）不可能带 device_name 属性——旧 schema 无该字段且键字段不进 attrs——故 fallback 是唯一现实来源，**旧键逐字节保留**（诚实保数据，不发明设备名）；手工携带 device_name 属性的行迁移到新键格式。
- **就地 UPDATE、无表重建、无 ForeignKeysOff**：`natural_key` 无外部外键引用（relations / virtual_devices / links 全部锚 `resources.id`），它只参与 `UNIQUE (resource_type, natural_key)`——对每一条 REFERENCES 子句核实过。
- **UNIQUE 冲突策略（保守保留原键）**：迁移先把全部 port 行的目标键快照进 `port_key_targets_v8` 临时表，再对满足「目标键仅本行认领 ∧ 不被任何其它行当前持有」的行执行单条 UPDATE；两行认领同一目标键、或目标键被保留行（fallback 恒等）占用时，涉事行一律保留原键。冲突条件全部对冻结快照求值，单语句确定性、不可能中途违反 UNIQUE。快照表随迁移 DROP，不留痕。
- **幂等**：对已换键行重算结果恒等（新键的 `|` 前分量即设备侧，回代不变）；账本（schema_migrations）跳过已应用版本。
- **不动 `updated_at`**：属模式级身份修复而非业务变更，迁移时刻由 `schema_migrations.applied_at` 记录。
- 语义提示：旧键保留的存量行与新键导入的同端口行互不判重（键不同）——需清理旧行时回滚其批次再重导即可；详见 [natural-keys.md](natural-keys.md) §4。

# DDL v9 — base_station 资源类型（resources CHECK 扩容）

> Migration `9` name `base_station_resource_type`（`internal/repo/migrations_v9.go`，经 `init()` 自注册追加 + 按版本排序，机制同 v4–v8；migrations.go 字面量列表不动）。**当前 schema version = 9**。

## 12. 基站资源（base_station schema）与条件自然键

面向真实无线基站工作簿（`samples/telecom-base-station.xlsx`：14 真实业务列，无地市列）。**条件自然键**：主键 `base_station_code`（非必填）；编码空时键退化为 `(city, base_station_name)` 按序拼接、跳过空值（真实表 → 纯名称；含地市列的表 → `地市|名称`），行照常导入并记 **W004** 治理 issue（warning，evidence：type / natural_key / missing_fields / fallback_fields）。E002 仅约束 `base_station_name`（Required）；E001/幂等判重都在解析后的最终键上。`base_station_ip` 为 **string 值列**（真实值带 /30 掩码，如 `172.7.10.125/30`；IP 属可变属性不作身份）；`uplink_a_ip`/`gateway_ip` 为 KindIPv4。无 ref 字段（自含导出，同 port 的 device_ip 决策），不派生关系；展示名钉定 `base_station_name`。键退化机制由 `mapping.TypeSpec.KeyFallbackFields` 通用声明驱动（键解析不硬编码类型名），base_station 是首个使用者——字段表与同义词见 `internal/mapping/schema.go` 的 `baseStationSpec`，行为细节见 [import-and-validation.md](import-and-validation.md) §12。

### 12.1 DDL v9 全文（FK 安全的表重建，v7 同构）

```sql
CREATE TABLE resources_new (
  id TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('device','ip_pool','circuit','port','base_station')),
  natural_key TEXT NOT NULL,
  name TEXT NOT NULL,
  attributes TEXT NOT NULL DEFAULT '{}',
  status TEXT,
  first_batch_id TEXT NOT NULL REFERENCES import_batches(id),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (resource_type, natural_key)
);
INSERT INTO resources_new (…全列…) SELECT …全列… FROM resources;
DROP TABLE resources;
ALTER TABLE resources_new RENAME TO resources;
```

与 v7 完全同构的 `ForeignKeysOff` 重建（专用连接事务外 `PRAGMA foreign_keys=OFF` → 事务内重建 → `PRAGMA foreign_key_check` 门禁 → 恢复 FK ON）：v9 之前 base_station INSERT 被 CHECK 拒绝（`TestMigrationV9AddsBaseStation` 钉死该回归）；迁移后既有行（含 port 行）逐字节保留、`first_batch_id` FK 仍强制、`Open` 幂等。base_station 无 relations / 无工作台侧实体（RelationType 封闭集不动）。
