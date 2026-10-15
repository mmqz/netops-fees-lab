package repo

// migrationV21 creates the detection run history table (schema version 21,
// v0.11-R11): every completed — or failed — detection sweep leaves one
// durable receipt row, whatever the trigger origin.
//
//   - source is the closed RunOrigin set, enforced by a CHECK constraint
//     (api | cli | scheduler). The MCP face cannot appear: it is the
//     read-only analysis red line, and the storage CHECK is the physical
//     twin of the v0.9.0 TestMCPToolRegistry forbidden-name pin.
//   - started_at/finished_at/duration_ms: the wall-clock bracket of the
//     sweep (bookkeeping); evaluated_at carries the DATA timestamp of the
//     newest point (RunResult.EvaluatedAt) so a stale-data run is
//     recognizable in the history next to a fresh one.
//   - from_ts/to_ts echo the request's evaluation window bounds ("" =
//     unbounded); the four counters mirror RunResult; error is "" on
//     success — a failed scheduled round is itself an auditable fact.
//   - idx_detection_runs_started: the listing order is newest-started
//     first (id ASC as the deterministic tie-break), the index carries
//     that ORDER BY shape.
//
// Purely additive: one CREATE TABLE + one CREATE INDEX, no rebuild, no
// data change — the migrations_v19/v20 style. A re-run is a no-op
// (idempotent DDL guarded by the migration runner's version bookkeeping).
var migrationV21 = Migration{
	Version: 21,
	Name:    "r11_detection_run_history",
	SQL: `
CREATE TABLE detection_runs (
  id                 TEXT PRIMARY KEY,
  source             TEXT NOT NULL CHECK (source IN ('api', 'cli', 'scheduler')),
  started_at         TEXT NOT NULL,
  finished_at        TEXT NOT NULL,
  duration_ms        INTEGER NOT NULL,
  from_ts            TEXT NOT NULL DEFAULT '',
  to_ts              TEXT NOT NULL DEFAULT '',
  evaluated_at       TEXT NOT NULL DEFAULT '',
  findings           INTEGER NOT NULL DEFAULT 0,
  alerts_created     INTEGER NOT NULL DEFAULT 0,
  alerts_cleared     INTEGER NOT NULL DEFAULT 0,
  run_states_written INTEGER NOT NULL DEFAULT 0,
  error              TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_detection_runs_started ON detection_runs (started_at DESC, id);
`,
}

func init() {
	migrations = append(migrations, migrationV21)
}
