package repo

import "sort"

// migrationV13 adds the ptp_port resource type (schema version 13, v0.5
// W8-D): the resources CHECK constraint is extended with 'ptp_port' —
// the per-model PTP port rows synthesized from ZTE VRP configuration
// scripts (composite natural key device_model|port_name, see
// docs/natural-keys.md §4). The relations table is NOT touched: ptp_port
// carries no ref fields and derives no relations (the scripts hold
// model-level template identity, no device-instance identity).
//
// SQLite cannot ALTER a CHECK in place, so resources is rebuilt inside one
// ForeignKeysOff migration, exactly following the v9 template (v10 also
// rebuilt relations; v13 has no relations work). The ForeignKeysOff safety
// variant (dedicated connection, pragma off OUTSIDE the transaction,
// PRAGMA foreign_key_check gate before commit, pragma restored after) is
// the simplified safe form of SQLite's official 12-step ALTER procedure.
//
// Regression note: before this migration the resources CHECK rejected
// resource_type='ptp_port' with a CHECK constraint error; after it, the
// insert succeeds (pinned by TestMigrationV13AddsPTPPort).
var migrationV13 = Migration{
	Version:        13,
	Name:           "ptp_port_type",
	ForeignKeysOff: true,
	SQL: `
CREATE TABLE resources_new (
  id TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('device','ip_pool','circuit','port','base_station','site','machine_room','ptp_port')),
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

// init appends v13 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v12.go own) and re-sorts the list by
// version, following the registration hook pattern of migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV13)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
