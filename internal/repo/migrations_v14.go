package repo

import "sort"

// migrationV14 adds the timeseries metric storage (schema version 14, W20 —
// design data-model-v1.md §5.3/§10.7, ADR-0011 D1, ADR-0014 decision 2):
// the three metric tables (metric_keys / metric_series / metrics), the
// metric batch table of the SEPARATE ingest channel (metric_batches —
// deliberately NOT import_batches: "不与资源批次混表", ADR-0011 D1) and the
// two relations indexes of §10.7 (idx_relations_to / idx_relations_type —
// pure CREATE INDEX on the existing relations table, no rebuild).
//
// The migration is purely additive: no existing table is rebuilt, no data
// moves, foreign_keys enforcement stays ON (the migrations_v11.go style,
// NOT the ForeignKeysOff v13 style). The three metric tables follow §5.3
// verbatim with two deliberate, documented deltas:
//
//   - metrics.batch_id references metric_batches(id), not the
//     import_batches(id) written in §5.3's DDL: the design text predates
//     the ADR-0011 D1 ruling that metric batches live in their own table
//     family. The FK target follows the ruling.
//   - one ADDITIONAL unique index, idx_metric_series_identity, over
//     (resource_id, metric_key, COALESCE(scope, ”)). The §5.3 table-level
//     UNIQUE (resource_id, metric_key, scope) does NOT fire when scope IS
//     NULL (SQLite treats NULLs as distinct in unique constraints), and
//     scope IS NULL is the ONLY case the ingest channel writes (port
//     performance series, §12-A3: the series hangs on the port resource
//     itself). Without the expression index, the mandated INSERT OR IGNORE
//     upsert (ADR-0014 decision 2) would silently duplicate the series row
//     on every re-import. The table-level UNIQUE stays in place verbatim —
//     it is harmless and keeps the DDL diffable against the design.
//
// metric_batches mirrors import_batches' shape but carries no rule_version
// (the E/W/I catalog does not cross domains, ADR-0011 D2) and there is NO
// row-level results table for the metric channel: a metric row is
// value-semantic (same (series, ts) overwrites), so per-row bookkeeping is
// just the batch counters — the audit trail is metrics.batch_id (the last
// writer) plus the batch row.
//
// The metric_keys catalog rows are seeded here via INSERT OR IGNORE from
// the closed registry values (internal/metrics.MetricKeys — the Go fact
// source, same values, same order; the sync is pinned by
// internal/metrics.TestCatalogSyncs and the structural checks in
// migrations_v14_test.go). created_at is a FIXED timestamp, not a clock
// read: catalog rows are static data and the migration must stay
// deterministic (no time.Now in the data path).
//
// Indexes beyond the §5.3 table definitions (all §5.3 + §10.7):
//   - idx_metric_series_resource(resource_id)          §5.3
//   - idx_metric_series_key(metric_key)                §5.3
//   - idx_metrics_ts(ts)                               §5.3
//   - idx_metrics_ts_key(ts, series_id)                §10.7 (cross-series time scan)
//   - idx_metric_series_identity                       this migration (see above)
//   - idx_relations_to(to_resource_id, relation_type)  §10.7 (direction=up)
//   - idx_relations_type(relation_type, from_resource_id) §10.7 (relations= filter)
//   - idx_metric_batches_started(listing order)        mirror of import_batches listing
//
// Regression note: before this migration none of these tables exist and a
// metrics insert fails with "no such table"; after it the catalog round-trips
// and the ingest channel runs (pinned by TestMigrationV14AddsTimeseries and
// the internal/metrics suite).
var migrationV14 = Migration{
	Version: 14,
	Name:    "timeseries_metrics",
	SQL: `
CREATE TABLE metric_keys (
  metric_key   TEXT PRIMARY KEY,       -- optical_power_rx_dbm
  label        TEXT NOT NULL,          -- 接收光功率
  unit         TEXT NOT NULL,          -- dBm / % / Mbps / count / ℃
  value_kind   TEXT NOT NULL           -- gauge | counter | ratio
                 CHECK (value_kind IN ('gauge','counter','ratio')),
  min_value    REAL,                   -- 合理下界（异常值过滤，可空）
  max_value    REAL,                   -- 合理上界
  direction    TEXT                    -- higher_is_better | lower_is_better | neutral
                 CHECK (direction IN ('higher_is_better','lower_is_better','neutral')),
  created_at   TEXT NOT NULL
);

CREATE TABLE metric_series (
  series_id    TEXT PRIMARY KEY,       -- ser_<uuid>
  resource_id  TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  metric_key   TEXT NOT NULL REFERENCES metric_keys(metric_key),
  scope        TEXT,                   -- 细分维度：如端口名（NULL = 资源级）
  first_seen_at TEXT NOT NULL,
  last_seen_at  TEXT NOT NULL,
  UNIQUE (resource_id, metric_key, scope)
);
CREATE INDEX idx_metric_series_resource ON metric_series(resource_id);
CREATE INDEX idx_metric_series_key      ON metric_series(metric_key);
-- See the file comment: the table-level UNIQUE above cannot fire on
-- scope IS NULL (SQLite NULLs are distinct), and scope IS NULL is the only
-- case the ingest channel writes — this expression index makes the
-- (resource_id, metric_key, scope=NULL) identity actually unique so the
-- INSERT OR IGNORE upsert semantics of ADR-0014 decision 2 hold.
CREATE UNIQUE INDEX idx_metric_series_identity
  ON metric_series(resource_id, metric_key, COALESCE(scope, ''));

CREATE TABLE metrics (
  series_id  TEXT NOT NULL REFERENCES metric_series(series_id) ON DELETE CASCADE,
  ts         TEXT NOT NULL,            -- RFC3339 UTC，来自数据（非服务器时钟）
  value      REAL NOT NULL,
  quality    TEXT DEFAULT 'ok'         -- ok | missing | suspect（数据质量）
                 CHECK (quality IN ('ok','missing','suspect')),
  batch_id   TEXT REFERENCES metric_batches(id),
  PRIMARY KEY (series_id, ts)
) WITHOUT ROWID;
CREATE INDEX idx_metrics_ts ON metrics(ts);
CREATE INDEX idx_metrics_ts_key ON metrics(ts, series_id);

-- The separate batch table of the metric ingest channel (ADR-0011 D1:
-- "复用导入引擎的 batch + 行级记账 + 回滚骨架，但独立批次类型，不与资源批次
-- 混表"). Same status state machine as import_batches; no rule_version
-- (rule catalogs do not cross domains); no workbook_path (the one-shot
-- ingest channel analyzes and executes in the same call — the file path is
-- the caller's business, the batch keeps the name + hash).
CREATE TABLE metric_batches (
  id TEXT PRIMARY KEY,
  workbook_name TEXT NOT NULL,
  workbook_sha256 TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('analyzing','awaiting_mapping','importing','completed','failed','rolled_back')),
  mapping TEXT NOT NULL DEFAULT '{}',          -- confirmed ingest mapping JSON (internal/metrics)
  rows_total INTEGER NOT NULL DEFAULT 0,
  rows_imported INTEGER NOT NULL DEFAULT 0,
  rows_failed INTEGER NOT NULL DEFAULT 0,
  error TEXT,
  started_at TEXT NOT NULL,
  finished_at TEXT
);
CREATE INDEX idx_metric_batches_started ON metric_batches(started_at);

-- §10.7: reverse-direction and relation-type-filtered traversal indexes on
-- the EXISTING relations table (pure CREATE INDEX — no rebuild, no data
-- movement, existing behavior untouched).
CREATE INDEX idx_relations_to ON relations(to_resource_id, relation_type);
CREATE INDEX idx_relations_type ON relations(relation_type, from_resource_id);

-- Catalog seed: the closed metric registry (internal/metrics.MetricKeys),
-- same values, same order, INSERT OR IGNORE (idempotent repair when rows
-- were deleted manually). created_at is a fixed constant — see the file
-- comment.
INSERT OR IGNORE INTO metric_keys
  (metric_key, label, unit, value_kind, min_value, max_value, direction, created_at) VALUES
  ('optical_power_rx_avg_dbm',  '接收光功率均值',  'dBm',   'gauge',   -30.0,  5.0,     'higher_is_better', '2026-01-01T00:00:00Z'),
  ('optical_power_tx_avg_dbm',  '发送光功率均值',  'dBm',   'gauge',   -10.0,  8.0,     'higher_is_better', '2026-01-01T00:00:00Z'),
  ('packet_loss_rx_pct',        '接收丢包率',      '%',     'ratio',     0.0,  100.0,   'lower_is_better',  '2026-01-01T00:00:00Z'),
  ('packet_loss_tx_pct',        '发送丢包率',      '%',     'ratio',     0.0,  100.0,   'lower_is_better',  '2026-01-01T00:00:00Z'),
  ('bandwidth_util_rx_pct',     '接收带宽利用率',  '%',     'ratio',     0.0,  100.0,   'neutral',          '2026-01-01T00:00:00Z'),
  ('bandwidth_util_tx_pct',     '发送带宽利用率',  '%',     'ratio',     0.0,  100.0,   'neutral',          '2026-01-01T00:00:00Z'),
  ('rx_rate_mbps',              '接收速率',        'Mbps',  'gauge',     0.0,  100000.0,'neutral',          '2026-01-01T00:00:00Z'),
  ('tx_rate_mbps',              '发送速率',        'Mbps',  'gauge',     0.0,  100000.0,'neutral',          '2026-01-01T00:00:00Z'),
  ('errors_rx_count',           '接收错误包数',    'count', 'counter',   0.0,  NULL,    'lower_is_better',  '2026-01-01T00:00:00Z'),
  ('errors_tx_count',           '发送错误包数',    'count', 'counter',   0.0,  NULL,    'lower_is_better',  '2026-01-01T00:00:00Z'),
  ('module_temp_avg_c',         '光模块温度均值',  '℃',    'gauge',    -10.0,  90.0,    'lower_is_better',  '2026-01-01T00:00:00Z'),
  ('supply_voltage_v',          '供电电压',        'V',     'gauge',     2.7,  5.3,     'neutral',          '2026-01-01T00:00:00Z'),
  ('bias_current_ma',           '偏置电流',        'mA',    'gauge',     0.0,  200.0,   'lower_is_better',  '2026-01-01T00:00:00Z'),
  ('optical_power_rx_min_dbm',  '接收光功率谷值',  'dBm',   'gauge',   -35.0,  5.0,     'higher_is_better', '2026-01-01T00:00:00Z');
`,
}

// init appends v14 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v13.go own) and re-sorts by version,
// following the registration hook pattern of migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV14)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
