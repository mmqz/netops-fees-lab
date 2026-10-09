package repo

import "sort"

// migrationV4 records the file path of the analyzed workbook on every import
// batch (ALTER TABLE import_batches ADD COLUMN workbook_path TEXT). The
// import service needs the path to re-read the workbook when the confirmed
// mapping is executed. Batches created before this migration keep NULL — no
// backfill is required.
var migrationV4 = Migration{
	Version: 4,
	Name:    "batch_workbook_path",
	SQL:     `ALTER TABLE import_batches ADD COLUMN workbook_path TEXT;`,
}

// init appends v4 to the ordered migration list declared in migrations.go
// (which this task leaves untouched) and re-sorts the list by version so it
// stays in canonical order no matter where later migrations register
// themselves — in the literals of migrations.go or in further init hooks.
func init() {
	migrations = append(migrations, migrationV4)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
