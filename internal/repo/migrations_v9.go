package repo

import "sort"

// migrationV9 adds the base_station resource type (schema version 9, W2-D):
//
//   - resources: the CHECK constraint of resource_type is extended with
//     'base_station'. SQLite cannot ALTER a CHECK in place, so the table is
//     rebuilt exactly like v7 (create resources_new with the identical
//     column set and constraints, INSERT SELECT every column, DROP the old
//     table, RENAME) under ForeignKeysOff — the simplified safe variant of
//     SQLite's official 12-step ALTER procedure (pragma off outside the
//     transaction, integrity gate before commit, pragma restored after).
//   - no other table is touched: base_station carries no relations
//     (RelationType has no such case) and no workspace-side entity, exactly
//     like port in v7.
//
// Regression note: before this migration the resources CHECK rejected
// resource_type='base_station' with a CHECK constraint error; after it,
// base_station rows insert cleanly (pinned by
// TestMigrationV9AddsBaseStation).
var migrationV9 = Migration{
	Version:        9,
	Name:           "base_station_resource_type",
	ForeignKeysOff: true,
	SQL: `
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

INSERT INTO resources_new (id, resource_type, natural_key, name, attributes, status, first_batch_id, created_at, updated_at)
SELECT id, resource_type, natural_key, name, attributes, status, first_batch_id, created_at, updated_at
FROM resources;

DROP TABLE resources;
ALTER TABLE resources_new RENAME TO resources;
`,
}

// init appends v9 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v8.go own) and re-sorts the list by version,
// following the registration hook pattern established by migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV9)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
