package repo

import "sort"

// migrationV36 records the period provenance on every fee batch (schema
// version 36, issue #15): the declared period was the ONLY authority and
// the file's own content was never cross-checked — a wrong --month
// silently poisoned every downstream analysis. The three-tier detection
// (#15) now verifies the declaration against the content and records
// HOW the period was established:
//
//	period_source  auto        A档: a dedicated period field agrees with
//	                           the declaration (content-verified)
//	              unverified  B档: only indirect evidence (month from a
//	                           column name) or an operator override of a
//	                           disagreement — flagged, never silent
//	              manual      C档: no period evidence in the content;
//	                           the human declaration is the only source
//	period_basis   the 识别依据 (field names, agreement count, ambiguity
//	              notes) — empty on pre-v36 rows (detection never ran).
//
// ALTER TABLE only — the v31/v34/v35 non-rebuild shape. Legacy rows
// default to 'manual' with ” basis: the pre-#15 reality was exactly
// "human-declared, never verified", and the empty basis distinguishes
// them from true C档 rows (which carry the reason).
var migrationV36 = Migration{
	Version: 36,
	Name:    "fee_batches period provenance",
	SQL: `
ALTER TABLE fee_batches ADD COLUMN period_source TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE fee_batches ADD COLUMN period_basis TEXT NOT NULL DEFAULT '';
`,
}

// init appends v36 to the ordered migration list and re-sorts by version,
// following the registration hook pattern established by migrations_v4.go.
func init() {
	migrations = append(migrations, migrationV36)
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
}
