package repo

// migrationV26 adds the flap-damped receipt column (schema version 26,
// v0.12-R17): detection_runs.flap_held — the count of would-be re-fires
// held back by the re-fire hold-down of the alert churn control.
//
// The v24 additive-ALTER discipline: ADD COLUMN with DEFAULT 0, no
// rebuild, no data movement — v21/v24/v25 rows backfill to "0 flap-held"
// and never lie about the past (the pre-R17 engine held nothing; the
// counter mirrors RunResult.FlapHeld so the run history answers "why did
// the patrol sweep fire nothing new for the oscillating port").
//
// No alerts-table change: a held fire never becomes an alert row (the
// hold is a gate BEFORE the machine — the R15 pattern), so the alert
// table's shape is untouched and the flap verdict derives entirely from
// existing rows (cleared_at on the pair's history).
var migrationV26 = Migration{
	Version: 26,
	Name:    "r17_flap_held_receipt",
	SQL: `
ALTER TABLE detection_runs ADD COLUMN flap_held INTEGER NOT NULL DEFAULT 0;
`,
}

func init() {
	migrations = append(migrations, migrationV26)
}
