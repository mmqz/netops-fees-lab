package repo

import "sort"

// migrationV18 adds the alert detection model (schema version 18, W23 —
// ADR-0011 D2/D3, design data-model-v1.md §12-B2, issue #23):
//
//   - detection_rules: the「运行指标时序越限」judgement standards — CONFIG
//     DATA, not code (ADR-0011 D2). Same severity semantics as the E/W/I
//     import catalog but deliberately NO rule_version column (同构不同表:
//     mixing the catalogs would lock import regression tests and detection
//     rule evolution together). metric_key REFERENCES the closed metric
//     catalog of migration v14; the violation DIRECTION reuses
//     metric_keys.direction for analysis instead of a duplicate field
//     (§12-B2 衔接点 ② — rules define comparator+threshold+window, the
//     catalog defines which way is worse).
//
//   - alerts: the ADR-0011 D3 state machine — firing | acked | cleared with
//     EXACTLY THREE edges (firing→acked human, firing→cleared engine
//     recovery, acked→cleared engine recovery) and NO auto-escalation /
//     timeout auto-close. UNIQUE (rule_id, series_id, first_seen): a
//     rule/series pair re-firing after a cleared alert inserts a NEW row
//     with a fresh first_seen, never re-opens the zombie. last_value is the
//     offending point (evidence), first_seen the first violating ts of the
//     window run.
//
// The migration is purely additive — two plain CREATE TABLE statements plus
// one lookup index, no rebuild, no data movement, foreign_keys enforcement
// stays ON (the migrations_v11/v14/v17 style). Default rules are NOT seeded
// here: rules are USER DATA, not schema (the metric catalog of v14 is
// closed-registry data, a different animal) — the Go-side default set
// lives in internal/repo/detection.go and is seeded lazily on first
// Detect/rules-list when the table is empty (INSERT OR IGNORE by rule_id,
// idempotent; rationale in data-model-v1.md §12 W23 处置).
//
// FK policy: alerts.rule_id and alerts.series_id are plain REFERENCES with
// NO ON DELETE action (deliberate — audit trail): deleting a rule that has
// alerts is BLOCKED at the API layer (409 conflict); deleting a metric
// series that has alerts (metric-batch rollback pruning) fails the FK and
// rolls the rollback back — the operator clears the alerts first. Honest
// failure beats silent alert deletion.
//
// Indexing: idx_alerts_rule_series covers the engine's per-rule active-alert
// lookup (rule_id + series_id + state); the listing endpoint sorts by
// fired_at (lab scale, no index needed).
//
// Regression note: before this migration both tables are absent and any
// rule/alert insert fails with "no such table"; after it the CRUD
// round-trips and the engine runs (pinned by TestMigrationV18AddsDetection
// and the internal/detect suite).
var migrationV18 = Migration{
	Version: 18,
	Name:    "w23_alert_detection",
	SQL: `
CREATE TABLE detection_rules (
  rule_id    TEXT PRIMARY KEY,
  metric_key TEXT NOT NULL REFERENCES metric_keys(metric_key),
  comparator TEXT NOT NULL CHECK (comparator IN ('gt','lt','gte','lte')),
  threshold  REAL NOT NULL,
  window     INTEGER NOT NULL DEFAULT 1,        -- consecutive points needed (去抖)
  severity   TEXT NOT NULL CHECK (severity IN ('error','warning','info')),
  enabled    INTEGER NOT NULL DEFAULT 1,
  label      TEXT NOT NULL DEFAULT '',          -- human name
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE alerts (
  alert_id   TEXT PRIMARY KEY,
  rule_id    TEXT NOT NULL REFERENCES detection_rules(rule_id),
  series_id  TEXT NOT NULL REFERENCES metric_series(series_id),
  fired_at   TEXT NOT NULL,
  cleared_at TEXT,
  acked_at   TEXT,
  acked_by   TEXT,
  state      TEXT NOT NULL CHECK (state IN ('firing','acked','cleared')),
  last_value REAL,                              -- the offending point (evidence)
  first_seen TEXT,                              -- first firing observation
  UNIQUE (rule_id, series_id, first_seen)
);
CREATE INDEX idx_alerts_rule_series ON alerts(rule_id, series_id, state);
`,
}

// init appends v18 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v17.go own) and re-sorts the list by
// version, following the registration hook pattern of migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV18)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
