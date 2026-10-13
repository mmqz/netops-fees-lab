package repo

import "sort"

// migrationV15 extends the resources CHECK to the 33-type closed set of
// data-model v1.0 §3.3 (schema version 15, W21-D M2): the 8 pre-W21 types
// plus the 25 new types of the §3 catalog — rack/duct/manhole/pole (L0),
// board (L1), optical_cable/cable_segment/fiber/odf/odf_terminal/
// splice_box (L2), otn_och/sdh_timeslot/protection_group (L3/L5),
// ring/igp_domain/vpn_instance (L5), customer/service/leased_line (L6),
// bbu/rru/olt/onu/pon_port (L4). The IN-list is verbatim §3.3's migration
// skeleton.
//
// SQLite cannot ALTER a CHECK in place, so resources is rebuilt inside one
// ForeignKeysOff migration, exactly following the v9/v10/v13 template: the
// dedicated-connection pragma dance, the foreign_key_check gate before
// commit and the post-restore are the simplified safe form of SQLite's
// official 12-step ALTER procedure. The relations table is NOT touched
// here (its CHECK extension is v16); source_refs and every other child
// reference resources by id which the drop+rename cycle preserves
// byte-for-byte.
//
// B4 conservative migration (§12-B4): the rebuild is a pure CHECK
// extension — every pre-v15 row survives byte-identical (id, type, key,
// attributes, timestamps), zero rows are added, removed or rewritten, and
// NO new relations are derived. The guard tests pin this on a populated
// pre-v15 database.
//
// Regression note: before this migration the resources CHECK rejected
// resource_type='rack'/'optical_cable'/… with CHECK constraint errors;
// after it, the inserts succeed (pinned by TestMigrationV15AddsW21Types).
var migrationV15 = Migration{
	Version:        15,
	Name:           "w21_resource_types",
	ForeignKeysOff: true,
	SQL: `
CREATE TABLE resources_new (
  id TEXT PRIMARY KEY,
  resource_type TEXT NOT NULL CHECK (resource_type IN (
    'device','ip_pool','circuit','port','base_station','site','machine_room','ptp_port',
    'rack','duct','manhole','pole',
    'board',
    'optical_cable','cable_segment','fiber','odf','odf_terminal','splice_box',
    'otn_och','sdh_timeslot','protection_group',
    'ring','igp_domain','vpn_instance',
    'customer','service','leased_line',
    'bbu','rru','olt','onu','pon_port'
  )),
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

// init appends v15 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v14.go own) and re-sorts the list by
// version, following the registration hook pattern of migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV15)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
