# NetOps Lab — 数据模型（Data Model）

> Schema 与代码同步维护：本文 DDL 摘自 `internal/repo/migrations.go` / `migrations_v4.go` / `migrations_v5.go`（**实现即规范**）。当前 schema version = **5**（v5 = 仿真工作台表，见文末 DDL v5 一节）。

## 1. ID 与时间戳约定

- **ID**：TEXT。P1 聚合 `前缀_<uuid>`：`res_`（资源）、`rel_`（关系）、`src_`（来源引用）、`bat_`（导入批次）、`iss_`（问题），生成见 `internal/domain` 的 `NewID`；P2 仿真实体 `前缀-<n>`（全局序列，见文末 §6）。工作区为 UUID。
- **自然键**（natural key）：资源唯一业务键——device 用 `device_code`、ip_pool 用 `pool_name`、circuit 用 `circuit_code`。表级唯一约束 `UNIQUE (resource_type, natural_key)`，幂等重导依赖它。
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
