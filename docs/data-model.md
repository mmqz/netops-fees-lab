# NetOps Lab — 数据模型（Data Model）

> Schema 与代码同步维护：本文 DDL 摘自各 `internal/repo/migrations*.go`（**实现即规范**）。当前 schema version = **28**。v5 = 仿真工作台表；v6 geo 列、v7 port 类型、v8 port 换键、v9 base_station 类型、v10 site/machine_room 类型 + belongs_to 关系、v11 属性变更历史表、v12 改名提案表、v13 ptp_port 类型为后续追加节；v14–v27（指标表族 / 运行状态 / 检测规则与告警 / 运行回执 / 图层投影 / 模板 / 维护窗口 / 关联列 / 抖动列 / webhook 投递审计）为 v0.7–v0.12 告警运营线的迁移，其表结构散记于 [api.md](api.md) 与 ADR-0011（**本文的历史断层，如实声明**——各迁移文件即权威规范）；v28 起本文恢复逐表记载。历史章节保持原貌。

## 1. ID 与时间戳约定

- **ID**：TEXT。P1 聚合 `前缀_<uuid>`：`res_`（资源）、`rel_`（关系）、`src_`（来源引用）、`bat_`（导入批次）、`iss_`（问题），生成见 `internal/domain` 的 `NewID`；P2 仿真实体 `前缀-<n>`（全局序列，见文末 §6）。工作区为 UUID。
- **自然键**（natural key）：资源唯一业务键——device 用 `device_code`、ip_pool 用 `pool_name`、circuit 用 `circuit_code`、port 用复合键 `device_name|port_name`（v8 换键，见文末 v7/v8 节）、base_station 用**条件键** `base_station_code`（编码空时兜底 `city|base_station_name`，跳空值，记 W004；v9 落地，见文末 v9 节）、site 用 `site_id`（局站ID，12 位数字编码）、machine_room 用 `machine_room_id`（机房ID，17 位数字编码）——两者均为 KindString 无条件编号键、v10 落地（见文末 v10 节；复合键拼接规则与稳定性评估见 [natural-keys.md](natural-keys.md)）。表级唯一约束 `UNIQUE (resource_type, natural_key)`，幂等重导依赖它。
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

**版本化行为注记（v0.5 W7-D 起，零 schema 变更）**：`UNIQUE (entity_kind, entity_id, batch_id)` 使该表天然可作**版本谱系**——导入器自 W7-D 起在 skip 路径也追加一行（每批每实体至多一条：同键再导 = 源确认过该键，append 即版本；E001 批内重复行到不了 skip 检查点故不追加），读侧 `ListByEntity` 按 `rowid`（插入序）返回版本序；`batch_id ON DELETE CASCADE` 使版本随批次回滚消失。DDL 逐字节未动、无 migration v13——行为语义详见 [import-and-validation.md](import-and-validation.md) §17。

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


# DDL v10 — site / machine_room 资源类型 + belongs_to 关系类型（resources 与 relations 双 CHECK 扩容）

> Migration `10` name `site_machine_room_types_and_belongs_to`（`internal/repo/migrations_v10.go`，经 `init()` 自注册追加 + 按版本排序，机制同 v4–v9；migrations.go 字面量列表不动）。**当前 schema version = 10**。

## 13. 机房 / 局站资源（site / machine_room schema）与 belongs_to 关系

面向真实机房局站表（`samples/telecom-site-machine-room.xlsx`：8 真实业务列——地市/区县/设备名称/IP地址/机房ID/机房名称/局站ID/局站名称；**同一张 sheet 承载两类实体**，导入故事为先局站后机房的双读法，行为细节见 [import-and-validation.md](import-and-validation.md) §13）。`site` 键 = `site_id`（12 位数字编码，全局唯一，KindString——前导零即身份）；`machine_room` 键 = `machine_room_id`（17 位数字编码，全局唯一，KindString 同理）。机房 spec 携带**可选 ref 字段** `site_id`（KindRef → site）：映射后按 E003 既有语义解析（局站须先入库），解析成功派生一条 `machine_room --belongs_to--> site` 关系；不映射则零关系 + W001。机房名称/局站名称/设备名称为昵称（展示名/属性），IP地址为 KindIPv4 可变属性——字段表与同义词见 `internal/mapping/schema.go` 的 `siteSpec` / `machineRoomSpec`。

**关系封闭集扩容**：v10 是首个同时重建 `relations` 的迁移——`relation_type` CHECK 增加 `'belongs_to'`（machine_room → site 的容器归属，ref→关系派生映射 `relationTypeFor` 新增 `machine_room.site_id` → `belongs_to` 一例——该映射 v0.6.0 W13-D 起迁至 `internal/mapping.RelationTypeFor` 单一事实源，导入建边与属性确认改挂共用；其余四类旧关系行为不变）。同时 `resources` CHECK 增加 `'site'` 与 `'machine_room'`。

### 13.1 DDL v10 全文（FK 安全的双表重建，v9 同构 ×2）

```sql
CREATE TABLE resources_new (
  id TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('device','ip_pool','circuit','port','base_station','site','machine_room')),
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

CREATE TABLE relations_new (
  id TEXT PRIMARY KEY,
  relation_type TEXT NOT NULL CHECK (relation_type IN ('assigned_to','a_end','z_end','belongs_to')),
  from_resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  to_resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  attributes TEXT NOT NULL DEFAULT '{}',
  first_batch_id TEXT NOT NULL REFERENCES import_batches(id),
  created_at TEXT NOT NULL,
  UNIQUE (from_resource_id, to_resource_id, relation_type)
);
INSERT INTO relations_new (…全列…) SELECT …全列… FROM relations;
DROP TABLE relations;
ALTER TABLE relations_new RENAME TO relations;
```

与 v7/v9 完全同构的 `ForeignKeysOff` 重建（专用连接事务外 `PRAGMA foreign_keys=OFF` → 事务内重建 → `PRAGMA foreign_key_check` 门禁 → 恢复 FK ON），但**同一迁移内串行两次重建**。**重建顺序 = 先 resources 后 relations**：relations 以**表名**引用 resources(id)，v9 已验证的 drop+rename 循环使名字 "resources" 始终指向重建后的表（id/行/约束逐字节保留），relations 重建因此对着一个按名一致的父表进行；FK 关闭时两种顺序都能通过 foreign_key_check 门禁，但 resources-first 保证即使迁移在两次重建之间中断，每个中间态的按名引用都是自洽的。v10 之前 site/machine_room INSERT 与 belongs_to 关系 INSERT 均被 CHECK 拒绝（`TestMigrationV10AddsSiteMachineRoom` 钉死两处回归）；迁移后既有行（device/circuit/a_end 等两表数据）逐字节保留、`first_batch_id` 与 relations→resources 两向 FK 仍强制、`UNIQUE (from, to, relation_type)` 防重、`Open` 幂等。site/machine_room 无工作台侧实体（物化仅消费 device，同 port/base_station）。

# DDL v11 — 资源属性变更历史表 resource_attribute_changes

> Migration `11` name `resource_attribute_changes`（`internal/repo/migrations_v11.go`，经 `init()` 自注册追加 + 按版本排序，机制同 v4–v10；migrations.go 字面量列表不动）。**当前 schema version = 11**。纯 `CREATE TABLE` + 三个索引，**无表重建**、无数据搬迁，FK 始终强制（非 ForeignKeysOff 形）。

## 14. 属性变更历史与人工确认生命周期（W4-D）

闭环 R4 裁决的「IP 变 = 属性变更事件」（[natural-keys.md](natural-keys.md) §2/§6 差距 #1/#2）：同键再导入命中库内资源且属性字段值不同时，importer（skip 分支）逐字段记录一条 **pending** 提案 + 一条 W005 warning issue，行仍 skip 不改库；人工经 HTTP API / CLI（`changes` 子命令）apply 后属性才真正生效（同一事务内：新值写入 `resources.attributes` + `updated_at` 刷新 + 提案置 `applied` + `applied_at` 打点），dismiss 可忽略。同值再导仍纯 skip（零记录零 issue，幂等不变）。

### 14.1 DDL v11 全文

```sql
CREATE TABLE resource_attribute_changes (
  id TEXT PRIMARY KEY,
  resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  batch_id TEXT NOT NULL REFERENCES import_batches(id),
  field TEXT NOT NULL,
  old_value TEXT NOT NULL DEFAULT '',     -- 记录时的库内值（'' = 资源原本无该属性）
  new_value TEXT NOT NULL,                -- 恒非空（空新值不记录，空值不写属性语义）
  status TEXT NOT NULL CHECK (status IN ('pending','applied','dismissed')),
  sheet TEXT NOT NULL,
  row_number INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  applied_at TEXT
);
CREATE INDEX idx_attr_changes_resource ON resource_attribute_changes(resource_id);
CREATE INDEX idx_attr_changes_batch ON resource_attribute_changes(batch_id);
CREATE INDEX idx_attr_changes_status ON resource_attribute_changes(status);
```

### 14.2 设计决策与不变式

- **生命周期**：`pending`（导入记录，唯一可操作态）→ `applied`（人工确认，属性已生效，`applied_at` 打点）或 `dismissed`（人工拒绝，资源永不触碰）；后两者为终态，重复 apply/dismiss 均拒绝。
- **id 前缀**：`chg_`（`domain.NewID`，UUID）。
- **幂等去重**：同一资源同一字段已有 pending 且 new 值相同 → 不重复记录（也不重记 W005）；new 值又变 → 新提案，多 pending 并存按 `(created_at, rowid)` 入队序，先 apply 的生效、后续同字段提案因旧值不匹配 409 `attribute_conflict`（变更服务校验资源当前值 == `old_value`，不等即拒绝——防丢失中间变更）。
- **apply 的 updated_at 语义**：`resources.updated_at` 仅在两处刷新——导入首次插入与属性变更 apply（`ResourceRepo.UpdateAttributes`）；skip 路径永不触碰。
- **回滚不对称（声明性语义）**：批次回滚删除链含「删本批 pending 提案」（`DeletePendingByBatch`）；**applied/dismissed 保留**——人工决策是与导入批次生命周期解耦的审计线。不对称例外：若提案的资源本身被该批回滚删除，`resource_id ON DELETE CASCADE` 会连带删除其全部变更记录（含终态）——被审计的资源已不存在，记录随之消失是正确行为。`batch_id` 刻意**不**设 CASCADE：回滚只删 pending（显式），终态不随批次死。
- **old_value 可为空串**：资源原本无该属性（attributes 只存非空值）而新导入带来值时，old_value = ''（约束 `NOT NULL DEFAULT ''`，空串非 NULL）；apply 校验同口径（当前值缺失 == ""）。
- **diff 归集口径**：参与比较的字段 = 导入行映射后的**非键、非枚举、非空**属性值（与首次导入的 attributes 归集完全一致；ref 字段（如机房的 site_id 属性快照）也在其中——v0.6.0 W13-D 起 apply 对 ref 字段叠加目标解析守卫与派生关系同事务改挂，语义三分支见 [import-and-validation.md](import-and-validation.md) §14：非 ref 只改属性 / ref 有边同步改挂（谱系不动）/ ref 无边只改属性且绝不建边（建边需重导，谱系不变式））。
- **与撤销红线的零交集**：本表属于**资源域**；lab 三层撤销/检查点属于**仿真域**（仅操作 `virtual_devices`/`sim_links`/变更集等 P2 表），两域无共享表、无代码路径交叉（`internal/sim` 零引用 `resource_attribute_changes`/`resources.attributes`）。
- **API/CLI 面**：`GET /api/attribute-changes`（status/resource_id/batch_id/limit/offset 过滤）与 `POST …/{id}/apply`、`POST …/{id}/dismiss`（契约见 [api.md](api.md)；v0.6.0 W13-D 起项字段含 `relationRetargeted`，apply 响应为改挂事实、列表为预览，ref 目标解析失败 409 `ref_target_missing`）；CLI `netopslab changes --db … list|apply|dismiss`（输出透传同一字段）。**MCP 工具面 5 个冻结不变**——AI 不得经 MCP 改资源属性（引擎仅仿真态操作）。


# DDL v12 — 疑似改名提案表 rename_proposals

> Migration `12` name `rename_proposals`（`internal/repo/migrations_v12.go`，经 `init()` 自注册追加 + 按版本排序，机制同 v4–v11）。**当前 schema version = 12**。纯 `CREATE TABLE` + 三个索引，**无表重建**、无数据搬迁，FK 始终强制（非 ForeignKeysOff 形）——完全照 v11 模式。

## 15. 疑似改名提案与身份合并生命周期（W6-D）

闭环 [natural-keys.md](natural-keys.md) §2/§6 差距 #3 的「名称变 = 身份变更事件」：名称类键类型（ip_pool/base_station/port，`TypeSpec.RenameDetection`）的新键导入行与某「旧键已从源消失」的同类型资源属性全等时，importer 记一条 **pending** 提案 + 一条 W006 warning issue，行照常按新资源导入（绝不自动合并）；人工经 HTTP API / CLI（`renames` 子命令）apply 合并身份或 reject 维持独立。检测五条件与幂等语义见 [import-and-validation.md](import-and-validation.md) §16。

### 15.1 DDL v12 全文

```sql
CREATE TABLE rename_proposals (
  id TEXT PRIMARY KEY,                          -- rp_<uuid>
  resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,  -- 旧资源（身份保留方）
  new_resource_id TEXT NOT NULL,                -- 新资源 id，纯文本留档（apply 后该行已删，无 FK）
  batch_id TEXT NOT NULL REFERENCES import_batches(id),  -- 无 cascade，同 v11 batch_id 口径
  resource_type TEXT NOT NULL,
  old_key TEXT NOT NULL,
  new_key TEXT NOT NULL,
  matched_fields TEXT NOT NULL DEFAULT '[]',    -- JSON 数组：参与匹配且相等的非键字段名
  status TEXT NOT NULL CHECK (status IN ('pending','applied','rejected')),
  sheet TEXT NOT NULL,
  row_number INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  decided_at TEXT
);
CREATE INDEX idx_rename_proposals_status ON rename_proposals(status);
CREATE INDEX idx_rename_proposals_resource ON rename_proposals(resource_id);
CREATE INDEX idx_rename_proposals_batch ON rename_proposals(batch_id);
```

### 15.2 设计决策与不变式

- **生命周期**：`pending`（导入记录）→ `applied`（人工确认合并，`decided_at` 打点）或 `rejected`（人工拒绝，两资源独立）；后两者为终态，重复 apply/reject 均拒绝（409）。
- **id 前缀**：`rp_`（`domain.NewID`，UUID）。
- **resource_id = 旧资源（身份保留方）**：ON DELETE CASCADE——旧资源被删（含回滚删除链）则其改名史随之消失；`new_resource_id` 刻意**无 FK**（apply 删除它指向的行，id 必须以纯文本活下来作审计）。`batch_id` 刻意**无 cascade**（同 v11 裁决）：回滚只显式删本批 pending 提案，applied/rejected 是与批次生命周期解耦的人工决策审计线。
- **apply 的合并事务**（单 DB.Do）：关系 retarget（UNIQUE 撞车删新侧边）→ 键分量属性同步（`TypeSpec.KeyComponentNames`，新侧非空者覆写旧资源——防「键换了而 base_station_name 还留旧值」引发的假 W005）→ **展示名 Name 同步**（v0.5 W9-D：`resources.name` 跟随新名——NameField 属性值优先，键类名称字段取新资源自身 name；非空才覆写，与键分量同步同口径）→ 删新资源（其 pending 属性变更随 v11 CASCADE）→ 旧资源换键 + updated_at 刷新 → 提案置 applied。**旧资源代理键 res_x 与 SourceRef 不动**。
- **与 W005 的对偶**：W005 记「键不变数据变」，本表记「键变数据不变」；两通道都只产生待确认提案，人工确认才生效——「自动合并」在整个身份治理体系里不存在。
- **API/CLI 面**：`GET /api/renames` + `POST …/{id}/apply|reject`；CLI `netopslab renames … list|apply|reject`。**MCP 工具面 5 个冻结不变**。
- **与撤销红线的零交集**：本表属**资源域**；`internal/sim` 零引用（同 v11 立场）。


# DDL v13 — ptp_port 资源类型（resources CHECK 扩容重建）

> Migration `13` name `ptp_port_type`（`internal/repo/migrations_v13.go`，`init()` 自注册模式同 v4–v12）。**当前 schema version = 13**。SQLite 无法就地改 CHECK，resources 表在单个 ForeignKeysOff 迁移内重建（v9 模板：专用连接、pragma 在事务外、`PRAGMA foreign_key_check` 提交前门、恢复 pragma）——relations 表零触碰（ptp_port 无 ref 字段、不派生关系）。新 CHECK 八类型封闭集 `('device','ip_pool','circuit','port','base_station','site','machine_room','ptp_port')`。

## 16. ptp_port 类型（W8-D）

`ptp_port` = PTP 配置脚本解析行（第 4 类真实电信材料，脚本导入通道见 [import-and-validation.md](import-and-validation.md) §18）：复合自然键 `device_model|port_name`（`|` 拼接，如 `6150|gei-1/1/0/7`），23 字段（接口级 PTP 参数 + 型号级时钟参数扁平重复落列 + 接口行 tab 注释；唯一枚举字段 `delay_mechanism` 按通用枚举机制落 `resources.status` 列而非 attributes——W002/幂等语义同既有类型，见 import-and-validation.md §18.2）。存储复用 resources 表本体（attributes JSON），代理键 `res_x` 与 UNIQUE(resource_type, natural_key) 语义同既有七类；模型级模板身份的诚实声明见 §18.4。v13 全文：

```sql
CREATE TABLE resources_new (
  id TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('device','ip_pool','circuit','port','base_station','site','machine_room','ptp_port')),
  natural_key TEXT NOT NULL,
  name TEXT NOT NULL,
  attributes TEXT NOT NULL DEFAULT '{}',
  status TEXT,
  first_batch_id TEXT NOT NULL REFERENCES import_batches(id),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE (resource_type, natural_key)
);

INSERT INTO resources_new (id, resource_type, natural_key, name, attributes, status, first_batch_id, created_at, updated_at)
SELECT id, resource_type, natural_key, name, attributes, status, first_batch_id, created_at, updated_at
FROM resources;

DROP TABLE resources;
ALTER TABLE resources_new RENAME TO resources;
```

---

# DDL v28 — 告警生命周期投递审计（alert_event_deliveries）

> Migration `28` name `r19_alert_event_delivery_audit`（`internal/repo/migrations_v28.go`，`init()` 自注册直接 append，模式同 v20–v27——v4–v9 的 init 兼带全表 sort，v10–v19 之后的注册写法以最新先例 v27 为准）。**当前 schema version = 28**。纯 `CREATE TABLE` + 一个索引，**无表重建**、无数据搬迁（v0.12 工程卫生教义）；alerts 表零触碰。

## 17. 告警生命周期投递审计（v0.13-R19）

人工 ack/clear 决策的**通知回流审计**：值班渠道收到了告警，操作者的处置动作（谁确认了、何时恢复）也要回流到渠道——每次 ack/clear 通知的重试终局一行，`GET /api/alerts` 经 LEFT JOIN 读回（`lifecycleDelivery`）。v28 全文：

```sql
CREATE TABLE alert_event_deliveries (
  id           TEXT PRIMARY KEY,
  alert_id     TEXT NOT NULL,
  event        TEXT NOT NULL CHECK (event IN ('ack','clear')),
  url          TEXT NOT NULL,
  status       TEXT NOT NULL CHECK (status IN ('delivered','failed')),
  attempts     INTEGER NOT NULL,
  http_status  INTEGER NOT NULL DEFAULT 0,
  error        TEXT NOT NULL DEFAULT '',
  delivered_at TEXT NOT NULL
);
CREATE INDEX idx_alert_event_deliveries_alert ON alert_event_deliveries (alert_id);
```

### 17.1 设计决策与不变式

- **`alert_id` 刻意非 FK**（v27 `run_id` 先例）：审计行由 notify 侧路径在 ack/clear 转换**提交后**的自有事务写入；通道未配置时该告警无行，读面诚实投 `''`（从未通知），而非悬空引用。告警行是审计数据永不删除（无 DELETE 端点），故实际生命周期内不会出现孤儿——非 FK 是防御性纪律而非日常路径（测试钉死：删除告警后审计行存续）。
- **`event` CHECK 闭集 ack|clear**：只回流**人工**两条边（ADR-0011 D3 三边状态机的 human edges）；引擎恢复清除是检测运行通知的变更行（v0.10-R8 载荷），不是生命周期事件。`escalate` 之类的值在存储层被拒——**本表绝不承载任何超时/自动动作**（D3 教义：通知是旁路观测，不是状态机的新边）。
- **`status` CHECK 复用 v27 的闭集**（delivered|failed，无 pending）：同一 notify 通道、同一重试序列的同一套裁决词表——无行 = 通道未配置/从未触发（三值诚实读），行存在即终局（重试序列在最后一次尝试后恰写一行）。
- **`url` 存脱敏形态**：notify 通道入库前剥离查询串（webhook token 常在查询串）与 userinfo——审计表永不成为凭据堆（`redactURL`，与 v27 同一函数）。
- **`http_status 0` = 传输层错误**（请求未得到应答）——v27 的哨兵值约定。
- **`delivered_at` 是投递钟表时间**（bookkeeping），与载荷里的 `ts`（操作时间戳 acked_at/cleared_at）分立——接收方区分「人何时处置」与「通知何时离开」；确定性教义下同操作同数据同载荷，仅 `delivered_at` 允许随投递钟变化。
- **读面（`GET /api/alerts` 每行）**：`lifecycleDelivery = {status, event, attempts, ts}`——每告警**最近一次**通知终局（先 ack 后 clear → 读 clear 行）；`''` 三值语义见 [api.md](api.md)。实现为 alerts 列表查询的关联子查询 LEFT JOIN，`idx_alert_event_deliveries_alert` 支撑子查询定位。「最近」由**事件的边序**裁定而非任何时钟：每告警至多一行 ack + 一行 clear（转换守卫 409 拒绝重复边），且两行并存时 clear 必为后决策（ack 需 firing、clear 需 firing|acked——状态机保证 ack ≺ clear），故 `ORDER BY CASE event WHEN 'clear' …` 即精确答案。刻意不用 rowid（ack/clear 审计行是并发 goroutine，取 SQLite 写锁次序不定——插入序 ≠ 裁决序）也不用 `delivered_at`（RFC3339Nano 变宽小数 `.41Z` vs `.4125Z` 在亚秒精度上非字典序可比，且投递钟可因调度延迟倒挂）；两者仅作并列数据的人工排序兜底（测试钉死时钟倒挂场景）。
- **主流程语义零变化**：ack/clear 处理器在转换提交后**异步**触发通知（副本载荷 + 25s 异步预算——与检测运行通知同界），通知失败绝不阻塞或改变处置响应。

# DDL v29 — 费用导入批次表 fee_batches

> Migration `29` name `fee_batches`（`internal/repo/migrations_v29.go`，`init()` 自注册 + 按版本排序，机制同 v4–v28）。**当前 schema version = 29**。纯 `CREATE TABLE` + 三个索引（其一为**部分唯一索引**），无表重建、无数据搬迁，FK 始终强制。与 `import_batches` **零外键、零耦合**——费用管道与资源管道是两个世界（快照/Parquet/查询的完整分层设计见 [fees-layer.md](fees-layer.md)）。

## 18. 费用导入状态层（#58/#60/#61 阶段 1/2）

`fee_batches` 只承载状态：幂等键、计数、回滚记账。费用数据本体**永不进 SQLite**——不可变快照在文件系统（内容寻址），规范化行在 hive 分区 Parquet 大表（DuckDB 查询）。

### 18.1 DDL v29 全文

```sql
CREATE TABLE fee_batches (
  id TEXT PRIMARY KEY,                          -- fee_<uuid>
  workbook_name TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  fee_type TEXT NOT NULL CHECK (fee_type IN ('electricity','tower_lease','repair','maintenance')),
  fee_year INTEGER NOT NULL CHECK (fee_year BETWEEN 2000 AND 2100),
  fee_month INTEGER NOT NULL CHECK (fee_month BETWEEN 1 AND 12),
  snapshot_path TEXT NOT NULL,                  -- 相对 data 目录（内容寻址快照）
  mapping TEXT NOT NULL,                        -- 规范化列映射 JSON（幂等键成分）
  status TEXT NOT NULL CHECK (status IN ('completed','failed','rolled_back')),
  rows_total INTEGER NOT NULL DEFAULT 0,
  rows_imported INTEGER NOT NULL DEFAULT 0,
  rows_failed INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  created_at TEXT NOT NULL,
  completed_at TEXT
);
CREATE INDEX idx_fee_batches_status ON fee_batches(status);
CREATE INDEX idx_fee_batches_sha ON fee_batches(sha256);
-- 幂等键：同一 (文件哈希, 费用类型, 账期, 映射) 至多一个 completed 批次；
-- failed/rolled_back 不占键（重试通道永远开放）。
CREATE UNIQUE INDEX idx_fee_batches_idem ON fee_batches(sha256, fee_type, fee_year, fee_month, mapping)
  WHERE status = 'completed';
```

### 18.2 设计决策与不变式

- **部分唯一索引 = 幂等键**：`WHERE status='completed'` 使 failed/rolled_back 批次**不占键**——#61「转换失败的批处理重试不产生半写状态」由构造保证（重试永远能成功；提交点 `MarkCompleted` 才触发索引）。
- **提交点语义**：`completed` 是唯一权威态——其 Parquet 文件是 keep-set；`failed`（转换失败，error 记因）/`rolled_back`（人工回滚，快照保留、Parquet 已删）的文件（若有崩溃残留）是 GC 孤儿。
- **`mapping` 列存规范化 JSON**（字段名按字典序、单行紧凑形）——同映射不同键序不产生第二身份。
- **计数三列** `rows_total / rows_imported / rows_failed`：行失败是**计数而非致命**（快照保留证据，修正后重导）。
- **CLI 契约**：`netopslab fees --db --data import|list|rollback|query`（单 JSON stdout；幂等重复导入 informational exit 0）；`mapping` 亦可由表头同义词自动识别（识别不到必需列即拒绝）。
- **AI 通道边界**：查询面是固定预设 + 参数（totals / duplicates）——调用方传不了 SQL；MCP 工具面 5 个冻结不变（阶段 5 才考虑扩展）。
