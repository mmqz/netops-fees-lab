package repo

import "sort"

// migrationV32 normalizes the stored checkpoint paths to the logical
// ('/') form (#80) — the sim-layer twin of v31 (#75). checkpoints.file_path
// was built with filepath.Join up to now, so a Windows-written database
// carries backslash-separated values — the column is a cross-platform data
// contract (persisted, "relative to the lab DB directory" per
// domain.Checkpoint), not a local filesystem path. A Windows lab DB copied
// to Linux (or back) must resolve identically, so the write side now emits
// path.Join (always '/'), and this migration rewrites the legacy backslash
// rows in place:
//
//	checkpoints\<workspaceID>\cp-1.db  →  checkpoints/<workspaceID>/cp-1.db
//
// The rewrite is a pure UPDATE (no rebuild, no schema change; foreign_keys
// can stay ON). Idempotent by construction: a row without backslashes is
// untouched by replace(), and SQLite string literals do not escape the
// backslash, so replace(file_path, '\', '/') is the byte-exact
// substitution. Reachable legacy data is safe on both origins: the three
// path components are system-generated — the checkpointDir constant,
// uuid.NewString() workspace ids and cp-<n> sequence ids — so a backslash
// inside a FILE NAME component cannot be produced by the write side; every
// '\' in a legacy row is a separator, and the rewrite treats it exactly
// like the fixed write side does: '\' is the contract separator. The
// snapshot FILES on disk are not touched — only the marker row's reference
// is rewritten; the restore path crosses the filepath.FromSlash boundary
// at disk access, which resolves the normalized form on every platform.
var migrationV32 = Migration{
	Version: 32,
	Name:    "checkpoint_path_logical_form",
	SQL: `
UPDATE checkpoints
SET file_path = replace(file_path, '\', '/')
WHERE file_path LIKE '%\%';
`,
}

// init appends v32 to the ordered migration list (which migrations.go,
// migrations_v4.go … migrations_v31.go own) and re-sorts the list by
// version, following the registration hook pattern of migrations_v4.go and
// every later migration file.
func init() {
	migrations = append(migrations, migrationV32)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
