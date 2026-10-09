# NetOps Lab — 数据模型（Data Model）

> Schema 与代码同步维护：本文 DDL 摘自 `internal/repo/migrations.go` / `migrations_v4.go`（**实现即规范**）。当前 schema version = **4**。

## 1. ID 与时间戳约定

- **ID**：TEXT，`前缀_<uuid>`。前缀按聚合：`res_`（资源）、`rel_`（关系）、`src_`（来源引用）、`bat_`（导入批次）、`iss_`（问题）。生成见 `internal/domain` 的 `NewID`。
- **自然键**（natural key）：资源唯一业务键——device 用 `device_code`、ip_pool 用 `pool_name`、circuit 用 `circuit_code`。表级唯一约束 `UNIQUE (resource_type, natural_key)`，幂等重导依赖它。
- **时间戳**：一律 **UTC RFC3339 字符串**（如 `2026-10-09T12:04:40Z`）。
- **JSON 列**：`attributes` / `raw` / `evidence` / `mapping` 均为 TEXT 存 JSON 对象（默认 `'{}'`）。

## 2. 迁移机制（migrations）

- 迁移 = 有序结构体列表 `{Version, Name, SQL}`；`repo.Open(path)` 打开库后自动应用未执行版本。
- 每个版本在**单事务**内执行 SQL + 写 `schema_migrations(version, name, applied_at)`；已应用版本跳过 → `Open` 幂等，重启安全。
- 迁移注册方式有二：`migrations.go` 的字面量列表（v1–v3），或新文件 `init()` 追加 + 按版本排序（v4 `batch_workbook_path` 即此模式，保持主文件不动）。
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
