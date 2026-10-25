package repo

import "sort"

// migrationV35 records the source-file path on every fee batch (schema
// version 35, issue #6): the system itself is read-only on source files
// (os.Open -> snapshot copy, content-addressed), but the integrity of the
// EVIDENCE CHAIN until now rested on "nobody touched the source" — with
// no way to even notice. source_path is the absolute path the import
// actually read (the CLI channel's user file; the HTTP channel's staged
// content-addressed copy under uploads/), and 'fees verify-sources'
// re-hashes it against the batch's recorded sha256 on demand.
//
// ALTER TABLE only — no rebuild, no data movement (the v31/v34 shape).
// ” (the default) means "unknown source" — every pre-v35 batch and the
// intentionally path-less rows — and verification SKIPS those rows
// rather than guessing (acceptance #5 of the issue).
var migrationV35 = Migration{
	Version: 35,
	Name:    "fee_batches source path",
	SQL: `
ALTER TABLE fee_batches ADD COLUMN source_path TEXT NOT NULL DEFAULT '';
`,
}

// init appends v35 to the ordered migration list and re-sorts by version,
// following the registration hook pattern established by migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV35)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
