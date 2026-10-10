package repo

import "sort"

// migrationV6 adds the optional geographic position columns to
// virtual_devices: geo_lat REAL / geo_lng REAL, both nullable, no default
// (NULL = no position — domain.VirtualDevice keeps *float64). Geo is
// presentation metadata only: it never enters the canonical workspace state
// hash, and no other table or query shape changes. Databases created before
// v6 keep NULL on every row — no backfill is required.
//
// ALTER TABLE … ADD COLUMN inside a transaction is supported by SQLite; the
// migration idempotency itself comes from the schema_migrations bookkeeping
// (already-applied versions are skipped by applyMigrations), so re-running
// Open on the same file never executes the ALTER twice.
var migrationV6 = Migration{
	Version: 6,
	Name:    "device_geo_columns",
	SQL: `
ALTER TABLE virtual_devices ADD COLUMN geo_lat REAL;
ALTER TABLE virtual_devices ADD COLUMN geo_lng REAL;
`,
}

// init appends v6 to the ordered migration list and re-sorts by version,
// following the registration hook pattern of migrations_v4.go and
// migrations_v5.go (the migrations.go literal list stays untouched).
func init() {
	migrations = append(migrations, migrationV6)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
