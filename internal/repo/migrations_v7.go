package repo

import "sort"

// migrationV7 adds the port resource type (schema version 7):
//
//   - resources: the CHECK constraint of resource_type is extended with
//     'port'. SQLite cannot ALTER a CHECK in place, so the table is rebuilt
//     (create resources_new with the identical column set and constraints,
//     INSERT SELECT every column, DROP the old table, RENAME). The rebuild
//     must run with PRAGMA foreign_keys=OFF: with enforcement on, the DROP
//     would run an implicit DELETE firing relations' ON DELETE CASCADE and
//     virtual_devices/links' ON DELETE SET NULL (silent data loss), and the
//     RENAME would reject the transiently broken schema. ForeignKeysOff
//     therefore implements the simplified safe variant of SQLite's official
//     12-step ALTER procedure (pragma off outside the transaction, integrity
//     gate before commit, pragma restored after — see migrations.go).
//   - virtual_devices is NOT touched: v6 (device_geo_columns) already added
//     geo_lat/geo_lng; port carries no workspace-side entity in Phase 1.
//
// Regression note: before this migration the resources CHECK rejected
// resource_type='port' with a CHECK constraint error; after it, port rows
// insert cleanly (pinned by TestMigrationV7UpgradesV6Database).
var migrationV7 = Migration{
	Version:        7,
	Name:           "port_resource_type",
	ForeignKeysOff: true,
	SQL: `
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

INSERT INTO resources_new (id, resource_type, natural_key, name, attributes, status, first_batch_id, created_at, updated_at)
SELECT id, resource_type, natural_key, name, attributes, status, first_batch_id, created_at, updated_at
FROM resources;

DROP TABLE resources;
ALTER TABLE resources_new RENAME TO resources;
`,
}

// init appends v7 to the ordered migration list (which migrations.go,
// migrations_v4.go, migrations_v5.go and migrations_v6.go own) and re-sorts
// the list by version, following the registration hook pattern established
// by migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV7)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
