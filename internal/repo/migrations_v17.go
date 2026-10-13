package repo

import "sort"

// migrationV17 adds the run-state model (schema version 17, W22 — design
// data-model-v1.md §12-B1 方案 C, issue #22): the
//
//   - resource_status history table: one row per (resource, ts) observation
//     with VALUE semantics (the same (resource_id, ts) upserts — INSERT OR
//     REPLACE, mirroring the metrics tables of v14; history accumulates for
//     flap analysis), and
//   - the resources snapshot columns run_state / run_state_at: the
//     projection of the NEWEST observation, so whole-DB statistics (GET
//     /api/stats byRunState) never join the history table.
//
// run_state and 台账 status (resources.status, free text from the import
// channel) are STRICTLY separated: nothing in the import/W005/sim-materialize
// paths reads or writes run_state (B1's core ruling); only
// ResourceStatusRepo.UpsertStatus writes the two sides, in ONE transaction.
//
// The migration is purely additive — a plain ALTER TABLE ADD COLUMN on
// resources (no rebuild, no ForeignKeysOff, the migrations_v11.go style):
// every existing row keeps its bytes, the new columns read as NULL
// (= no observation; distinct from the explicit 'unknown' enum value).
// No backfill: NULL honestly means "no run-state data yet".
//
// The source enum (alert/poll/manual) is closed by CHECK: 'alert' is the W23
// detection engine's writer (the HTTP channel rejects non-manual sources
// with 400 until W23 lands — see internal/domain/status.go), 'poll' is
// reserved for a future scheduled probe writer.
//
// Indexing: PRIMARY KEY (resource_id, ts) covers the history query
// (ListHistory: one resource, ascending ts window) — no secondary index is
// needed. The stats count reads the resources snapshot (a GROUP BY over
// resources, same shape as CountByStatus). §10.7's traversal indexes
// (idx_relations_to / idx_relations_type) are untouched.
//
// Regression note: before this migration the resource_status insert fails
// with "no such table" and resources has no run_state column; after it the
// upsert round-trips and the snapshot is readable via GetByID (pinned by
// TestMigrationV17AddsRunState).
var migrationV17 = Migration{
	Version: 17,
	Name:    "w22_run_state",
	SQL: `
CREATE TABLE resource_status (
  resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  ts          TEXT NOT NULL,            -- RFC3339 UTC，来自请求或写入时刻
  run_state   TEXT NOT NULL CHECK (run_state IN ('up','down','degraded','maintenance','unknown')),
  source      TEXT NOT NULL CHECK (source IN ('alert','poll','manual')),
  PRIMARY KEY (resource_id, ts)
);

ALTER TABLE resources ADD COLUMN run_state TEXT;
ALTER TABLE resources ADD COLUMN run_state_at TEXT;
`,
}

// init appends v17 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v16.go own) and re-sorts the list by
// version, following the registration hook pattern of migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV17)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
