package repo

// migrationV24 adds the maintenance-window model (schema version 24,
// v0.12-R15): planned suppression windows for NEW alert fires.
//
//   - maintenance_windows: label + [starts_at, ends_at) + scope. starts_at
//     and ends_at are RFC3339 UTC strings normalized on write; the
//     ordering (ends > starts) is validated at the API boundary — a CHECK
//     on string comparison would be wrong across offsets, so the schema
//     trusts the domain layer's parse-and-compare gate (same division of
//     labor as detection_rules' comparator, whose closed set IS CHECKable
//     while timestamps are not).
//   - scope_resource_ids: the JSON-array id allowlist ("" = global scope,
//     all resources). Deliberately NOT a child table with FKs: a window's
//     scope is a point-in-time plan of ids, not relational data — deleting
//     a scoped resource must not mutate the plan (the id simply stops
//     matching any finding; honest, no cascade surprises).
//   - no enabled flag: a window is a plan, the removal path is DELETE
//     (edit = delete + recreate; the detection_rules precedent keeps
//     enable/disable for long-lived judgement config, windows are
//     short-lived operational objects).
//   - detection_runs.suppressed: additive ALTER TABLE ADD COLUMN with
//     DEFAULT 0 — v21 rows backfill to "0 suppressed", the v21-style
//     additive-column discipline (no rebuild, no data movement). The
//     counter mirrors RunResult.Suppressed so the run history answers
//     "why did the nightly sweep fire nothing during the 割接".
//
// Engine integration (internal/detect/engine.go): a finding is suppressed
// when its LastTS (data timeline) falls in [starts_at, ends_at) AND the
// finding's resource is in scope. Suppressed findings never become alert
// rows — the alert table's shape is untouched.
var migrationV24 = Migration{
	Version: 24,
	Name:    "r15_maintenance_windows",
	SQL: `
CREATE TABLE maintenance_windows (
  id                  TEXT PRIMARY KEY,
  label               TEXT NOT NULL,
  starts_at           TEXT NOT NULL,
  ends_at             TEXT NOT NULL,
  scope_resource_ids  TEXT NOT NULL DEFAULT '',
  note                TEXT NOT NULL DEFAULT '',
  created_at          TEXT NOT NULL
);
CREATE INDEX idx_maintenance_windows_time ON maintenance_windows (starts_at DESC, id);

ALTER TABLE detection_runs ADD COLUMN suppressed INTEGER NOT NULL DEFAULT 0;
`,
}

func init() {
	migrations = append(migrations, migrationV24)
}
