package repo

import "sort"

// migrationV10 adds the machine_room and site resource types plus the
// belongs_to relation type (schema version 10, W3-D):
//
//   - resources: the CHECK constraint of resource_type is extended with
//     'site' and 'machine_room' — the two readings of the real 机房局站
//     export (机房 keyed by 机房ID, 局站 keyed by 局站ID, both R4-ruled
//     global numeric codes).
//   - relations: the CHECK constraint of relation_type is extended with
//     'belongs_to' (machine_room --belongs_to--> site, derived from the
//     machine_room spec's site_id ref column on import).
//
// SQLite cannot ALTER a CHECK in place, so BOTH tables are rebuilt inside
// ONE ForeignKeysOff migration, following the v9 template for resources.
// The rebuild ORDER is resources first, then relations: relations references
// resources(id) by NAME, and the v9-proven drop+rename cycle leaves the name
// "resources" pointing at the rebuilt table (ids, rows and constraints
// preserved byte-for-byte), so the relations rebuild then builds its new
// table against a consistent parent — with FK enforcement off for the whole
// script either order would pass foreign_key_check, but resources-first
// keeps every intermediate schema state (had the migration failed between
// the two rebuilds) referentially consistent by name. The ForeignKeysOff
// safety variant (dedicated connection, pragma off OUTSIDE the transaction,
// PRAGMA foreign_key_check gate before commit, pragma restored after) is the
// simplified safe form of SQLite's official 12-step ALTER procedure, exactly
// as v7/v9.
//
// Regression note: before this migration the resources CHECK rejected
// resource_type='site'/'machine_room' and the relations CHECK rejected
// relation_type='belongs_to' with CHECK constraint errors; after it, both
// insert cleanly (pinned by TestMigrationV10AddsSiteMachineRoom).
var migrationV10 = Migration{
	Version:        10,
	Name:           "site_machine_room_types_and_belongs_to",
	ForeignKeysOff: true,
	SQL: `
CREATE TABLE resources_new (
  id TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('device','ip_pool','circuit','port','base_station','site','machine_room')),
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

CREATE TABLE relations_new (
  id TEXT PRIMARY KEY,
  relation_type TEXT NOT NULL CHECK (relation_type IN ('assigned_to','a_end','z_end','belongs_to')),
  from_resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  to_resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  attributes TEXT NOT NULL DEFAULT '{}',
  first_batch_id TEXT NOT NULL REFERENCES import_batches(id),
  created_at TEXT NOT NULL,
  UNIQUE (from_resource_id, to_resource_id, relation_type)
);

INSERT INTO relations_new (id, relation_type, from_resource_id, to_resource_id, attributes, first_batch_id, created_at)
SELECT id, relation_type, from_resource_id, to_resource_id, attributes, first_batch_id, created_at
FROM relations;

DROP TABLE relations;
ALTER TABLE relations_new RENAME TO relations;
`,
}

// init appends v10 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v9.go own) and re-sorts the list by version,
// following the registration hook pattern established by migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV10)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
