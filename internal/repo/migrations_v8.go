package repo

import "sort"

// migrationV8 rebases the natural key of EXISTING port resources from
// `device_ip|port_name` to `device_name|port_name` (R4's P1 finding: an IP in
// the key splits one physical port into two resources when the device is
// re-IPped; the key's device side must be the stable device NAME). New
// imports pick the new key up automatically via the portSpec field order —
// this migration only repairs rows already in the database.
//
// Key recomputation per port row (the old key format is `ip|port`, split on
// the FIRST '|', so a port_name containing '|' round-trips):
//
//   - device side: COALESCE(attrs.device_name, <old key's device_ip part>) —
//     rows imported by the pre-v8 schema can never carry a device_name
//     attribute (the old spec had no such field, and key fields never enter
//     attrs), so the fallback is the only realistic source and the row keeps
//     its old key byte-for-byte: honest data preservation, no invented names.
//     A row hand-carrying attrs.device_name moves to the new key format.
//   - port side: unchanged (the old key's port_name part).
//
// Safety properties (all pinned by TestMigrationV8RecomputesPortNaturalKeys):
//
//   - In-place UPDATE, no table rebuild: natural_key has NO external foreign
//     key reference (relations/virtual_devices/links anchor resources.id),
//     it only participates in UNIQUE (resource_type, natural_key) — verified
//     against every REFERENCES clause in the migration history.
//   - Conflict strategy (conservative, keep-original-key): a row only moves
//     when its new key is claimed by exactly one row (itself) AND is not the
//     current key of any other port row. Contested targets — two rows wanting
//     the same new key, or a target occupied by a row that keeps it (fallback
//     identity) — leave BOTH rows on their original unique keys rather than
//     risk data loss; a later re-import of the same workbook then creates the
//     properly keyed row (the legacy row is cleaned up by rolling back its
//     batch). The conditions are evaluated against the frozen
//     port_key_targets_v8 snapshot, never against the table being mutated,
//     so the single UPDATE is deterministic and cannot violate UNIQUE
//     mid-statement.
//   - Rows whose key contains no '|' (not old-format) keep their key as-is;
//     rows of other types are untouched.
//   - updated_at is deliberately NOT touched: this is a schema-level identity
//     repair, not a business mutation — the migration's applied_at in
//     schema_migrations is the provenance record.
//   - Idempotent by construction: re-running the recompute on a converted
//     row yields the same key (identity), so a rewound bookkeeping row is
//     always a no-op on the data.
//
// No runtime log line is emitted for contested rows: the migration framework
// is pure SQL (this file follows the registration pattern of migrations_v4),
// and the mover/conflict path is unreachable for real legacy databases — the
// documented behavior plus the pinned tests are the audit trail.
var migrationV8 = Migration{
	Version: 8,
	Name:    "port_natural_key_device_name",
	SQL: `
CREATE TABLE port_key_targets_v8 (
  id TEXT PRIMARY KEY,
  old_key TEXT NOT NULL,
  new_key TEXT NOT NULL
);

INSERT INTO port_key_targets_v8 (id, old_key, new_key)
SELECT id, natural_key,
       COALESCE(NULLIF(json_extract(attributes, '$.device_name'), ''),
                substr(natural_key, 1, instr(natural_key, '|') - 1))
       || '|' ||
       substr(natural_key, instr(natural_key, '|') + 1)
FROM resources
WHERE resource_type = 'port' AND instr(natural_key, '|') > 0;

UPDATE resources
SET natural_key = (SELECT t.new_key FROM port_key_targets_v8 t WHERE t.id = resources.id)
WHERE id IN (SELECT t.id FROM port_key_targets_v8 t
             WHERE t.new_key != t.old_key
               AND (SELECT COUNT(*) FROM port_key_targets_v8 c
                    WHERE c.new_key = t.new_key) = 1
               AND NOT EXISTS (SELECT 1 FROM port_key_targets_v8 o
                               WHERE o.old_key = t.new_key AND o.id != t.id));

DROP TABLE port_key_targets_v8;
`,
}

// init appends v8 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v7.go own) and re-sorts the list by version,
// following the registration hook pattern established by migrations_v4.go
// (the migrations.go literal list stays untouched).
func init() {
	migrations = append(migrations, migrationV8)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
