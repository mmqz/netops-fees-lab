package repo

import "sort"

// migrationV11 adds the resource attribute change history table (schema
// version 11, W4-D): the landing spot for the R4-ruled "IP change is an
// attribute-change event" semantics. When a re-imported row hits the same
// natural key as an existing resource (the idempotent-skip path) but carries
// attribute values that differ from the stored ones, the importer records
// one row per differing field here as a PENDING proposal plus a W005 issue
// — the row itself is still skipped, nothing is written to the resource.
// A human then applies (or dismisses) the proposal via the HTTP API / CLI;
// applying writes the new value into resources.attributes and refreshes
// updated_at, all inside one transaction.
//
// The migration is a pure CREATE TABLE (+ indexes): no table is rebuilt, no
// data moves, foreign_keys enforcement can stay ON. resource_id carries ON
// DELETE CASCADE so deleting a resource (the rollback delete chain) removes
// its change history with it; batch_id deliberately has NO cascade — the
// rollback deletes only the batch's PENDING proposals explicitly (the
// applied/dismissed audit trail survives the batch, see rollback.go and
// docs/import-and-validation.md).
var migrationV11 = Migration{
	Version: 11,
	Name:    "resource_attribute_changes",
	SQL: `
CREATE TABLE resource_attribute_changes (
  id TEXT PRIMARY KEY,
  resource_id TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
  batch_id TEXT NOT NULL REFERENCES import_batches(id),
  field TEXT NOT NULL,
  old_value TEXT NOT NULL DEFAULT '',     -- stored value at record time ('' = field was absent)
  new_value TEXT NOT NULL,                -- always non-empty (empty new values record nothing)
  status TEXT NOT NULL CHECK (status IN ('pending','applied','dismissed')),
  sheet TEXT NOT NULL,
  row_number INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  applied_at TEXT
);
CREATE INDEX idx_attr_changes_resource ON resource_attribute_changes(resource_id);
CREATE INDEX idx_attr_changes_batch ON resource_attribute_changes(batch_id);
CREATE INDEX idx_attr_changes_status ON resource_attribute_changes(status);
`,
}

// init appends v11 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v10.go own) and re-sorts the list by version,
// following the registration hook pattern established by migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV11)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
