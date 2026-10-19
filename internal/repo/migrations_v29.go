package repo

import "sort"

// migrationV29 adds the fee import batch table (schema version 29, the
// stage-1/2 vertical slice of the fee risk-analysis module — issues
// #58/#60/#61): the SQLite STATE layer of the fee pipeline. The fee data
// itself never lands in SQLite: the immutable snapshot stays on the
// filesystem (content-addressed, evidence chain) and the normalized rows
// live in the hive-partitioned Parquet analysis table (fee_type/year/month
// directories) queried by the DuckDB engine. This table only tracks the
// batch state machine — completed / failed / rolled_back — plus the
// idempotency key and the row counts.
//
// Idempotency: the partial UNIQUE index (sha256, fee_type, fee_year,
// fee_month, mapping) covers ONLY completed batches — a failed or rolled
// back attempt never blocks a clean retry (issue #61: "转换失败的批处理
// 重试不产生半写状态" — the retry path must be open by construction).
//
// The migration is a pure CREATE TABLE + partial index, modeled on v11/v12:
// no table is rebuilt, no data moves, foreign_keys enforcement stays ON.
// No FK to import_batches: fee batches are a SEPARATE pipeline (the
// resource importer never sees them, the fee importer never creates
// resources) — one table, two worlds, zero coupling.
var migrationV29 = Migration{
	Version: 29,
	Name:    "fee_batches",
	SQL: `
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
`,
}

// init appends v29 to the ordered migration list and re-sorts by version,
// following the registration hook pattern of migrations_v4.go and later.
func init() {
	migrations = append(migrations, migrationV29)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
