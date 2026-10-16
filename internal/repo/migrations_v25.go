package repo

// migrationV25 adds the alert correlation columns (schema version 25,
// v0.12-R16): which of the active alerts look like the ROOT CAUSE of the
// storm and which look like collateral damage — the 根因/次生 verdict the
// graph already knows how to compute (the impact engine's layer logic),
// now attached to the alert rows themselves so the queue, the filters and
// the MCP AI triage can consume it without re-deriving it.
//
//   - correlation_role: ” (unmarked — single alert, or info severity),
//     'primary' (根因嫌疑: an alerting DEPENDENT exists and no alerting
//     CAUSE), 'secondary' (疑似次生: an alerting resource exists in the
//     alert's down-graph — the physical-ward, cause direction). The role
//     is ENGINE-managed observation state, recomputed by every detection
//     run (a lifecycle no-op: it is NOT a state-machine edge; the three
//     edges of ADR-0011 D3 remain the whole lifecycle).
//   - correlated_alert_id / correlated_resource_id: for secondary rows,
//     the nearest alerting cause (deterministic pick: BFS depth, then
//     layer, then id) — the link the UI and the AI follow to jump from a
//     symptom to its suspected cause. Empty for primary and unmarked.
//
// Purely additive: three ALTER TABLE ADD COLUMN with DEFAULT ” — existing
// rows backfill to unmarked (the roles arrive with the next detect run).
// Cleared alerts keep their last computed role: the verdict recorded at
// clear time is the audit truth of "what it looked like while it lived".
var migrationV25 = Migration{
	Version: 25,
	Name:    "r16_alert_correlation",
	SQL: `
ALTER TABLE alerts ADD COLUMN correlation_role TEXT NOT NULL DEFAULT '' CHECK (correlation_role IN ('', 'primary', 'secondary'));
ALTER TABLE alerts ADD COLUMN correlated_alert_id TEXT NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN correlated_resource_id TEXT NOT NULL DEFAULT '';
`,
}

func init() {
	migrations = append(migrations, migrationV25)
}
