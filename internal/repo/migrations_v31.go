package repo

import "sort"

// migrationV31 normalizes the stored fee snapshot paths to the logical
// ('/') form (#75). fee_batches.snapshot_path was built with filepath.Join
// up to v0.15.1, so a Windows-written database carries backslash-separated
// values — the column is a cross-platform data contract (persisted, echoed
// by the HTTP API's snapshotPath and the CLI JSON, "relative to the data
// dir" per domain.FeeBatch), not a local filesystem path. A Windows
// database + data dir copied to Linux (or back) must resolve identically,
// so the write side now emits path.Join (always '/'), and this migration
// rewrites the legacy backslash rows in place:
//
//	fee-imports\electricity\2024\05\<hash>_<name>  →  fee-imports/electricity/2024/05/<hash>_<name>
//
// The rewrite is a pure UPDATE (no rebuild, no schema change; foreign_keys
// can stay ON). Idempotent by construction: a row without backslashes is
// untouched by replace(), and SQLite string literals do not escape the
// backslash, so replace(snapshot_path, '\', '/') is the byte-exact
// substitution. Reachable legacy data is safe on both origins: a Windows
// database cannot carry a backslash inside a FILE NAME (the OS forbids
// it), so every '\' in its rows is a separator; a POSIX database was
// written with '/' separators already and only matches the LIKE when a
// file NAME literally contains '\' — a self-inflicted edge the import
// sources cannot produce (upload filenames never contain it, and the
// rewrite treats it exactly like the fixed write side does: '\' is the
// contract separator). Going forward path.Join cannot emit one, and the
// path components (fee type enum, year, month, sha256 hex) are validated
// at import, which is what makes the one-shot rewrite safe without a
// guard condition.
var migrationV31 = Migration{
	Version: 31,
	Name:    "fee_snapshot_path_logical_form",
	SQL: `
UPDATE fee_batches
SET snapshot_path = replace(snapshot_path, '\', '/')
WHERE snapshot_path LIKE '%\%';
`,
}

// init appends v31 to the ordered migration list and re-sorts it by
// version, following the registration hook pattern of migrations_v4.go and
// every later migration file.
func init() {
	migrations = append(migrations, migrationV31)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
