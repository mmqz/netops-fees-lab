package repo

import "sort"

// migrationV16 extends the relations CHECK to the 29-value closed set of
// data-model v1.0 §4.3 (schema version 16, W21-D M3): the 4 pre-W21
// relations plus the 25 new relations of the §4.2 catalog. The IN-list is
// §4.3's migration skeleton with ONE deliberate W21 correction: §4.2's
// 'carries_fiber' (optical_cable → fiber) is registered as 'fiber_of'
// (fiber → optical_cable) — the original direction violated the
// member→container pattern every other composition relation follows
// (belongs_to machine_room→site, segment_of cable_segment→optical_cable);
// see the design doc §12 "W21 落地勘误与处置" addendum.
//
// SQLite cannot ALTER a CHECK in place, so relations is rebuilt inside one
// ForeignKeysOff migration, following the v10 template (relations rebuild,
// resources untouched). The v14 indexes idx_relations_to /
// idx_relations_type are attached to the relations table and therefore die
// with the DROP — v16 re-creates them verbatim after the rename so §10.7's
// query plans survive the rebuild.
//
// B4 conservative migration (§12-B4): pure CHECK extension — every
// pre-v16 relation row survives byte-identical, the count is unchanged,
// and NO new relations are derived automatically (环/链 stays an
// attribute; 归属A stays text; zero auto-derivation — new relations come
// only from new imports carrying the explicit ref columns). The guard
// tests pin this on a populated pre-v16 database.
//
// Regression note: before this migration the relations CHECK rejected
// relation_type='fiber_of'/'carried_by'/… with CHECK constraint errors;
// after it, the inserts succeed (pinned by TestMigrationV16AddsW21Relations).
var migrationV16 = Migration{
	Version:        16,
	Name:           "w21_relation_types",
	ForeignKeysOff: true,
	SQL: `
CREATE TABLE relations_new (
  id TEXT PRIMARY KEY,
  relation_type TEXT NOT NULL CHECK (relation_type IN (
    'assigned_to','a_end','z_end','belongs_to',
    'located_in','housed_in','installed_in','mounted_on',
    'cable_a_end','cable_z_end','fiber_of','fiber_terminates',
    'segment_of','connects_device',
    'carried_by','routed_over','uses_fiber',
    'member_of_ring','protects','in_domain','in_vpn',
    'serves','subscribed_by','provided_by','depends_on',
    'uplink_to','home_bbu','covers','connects_onu'
  )),
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

CREATE INDEX idx_relations_to ON relations(to_resource_id, relation_type);
CREATE INDEX idx_relations_type ON relations(relation_type, from_resource_id);
`,
}

// init appends v16 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v15.go own) and re-sorts the list by
// version, following the registration hook pattern of migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV16)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
