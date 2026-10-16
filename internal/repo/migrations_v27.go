package repo

// migrationV27 adds the webhook delivery audit (schema version 27,
// v0.12-R18): one row per attempted notification of a detection run —
// the retry sequence's durable verdict, surfaced in the run-history
// listing via LEFT JOIN.
//
//   - run_id is deliberately NOT an FK: the delivery row is written by
//     the notify side path AFTER the run receipt commits (its own
//     transaction); a receipt with the channel off simply has no row,
//     and the honest projection of that is "" (channel off), not a
//     dangling reference. Same division as maintenance_windows' scope —
//     point-in-time operational facts, not relational data;
//   - status CHECK-closed (delivered|failed): there is no "pending" —
//     the row lands once, after the final attempt;
//   - http_status 0 means "transport error" (the request never got an
//     answer) — a sentinel the domain layer documents, not a magic
//     number scattered across readers;
//   - the URL column stores the notify channel's REDACTED form (query
//     and userinfo stripped) — the audit trail must never become a
//     credential dump (webhook URLs frequently carry tokens in the
//     query).
//
// The detection_runs table is untouched: the runs-listing join reads
// this table, the receipt's shape is frozen by the v21 contract.
var migrationV27 = Migration{
	Version: 27,
	Name:    "r18_webhook_delivery_audit",
	SQL: `
CREATE TABLE webhook_deliveries (
  id           TEXT PRIMARY KEY,
  run_id       TEXT NOT NULL,
  url          TEXT NOT NULL,
  status       TEXT NOT NULL CHECK (status IN ('delivered','failed')),
  attempts     INTEGER NOT NULL,
  http_status  INTEGER NOT NULL DEFAULT 0,
  error        TEXT NOT NULL DEFAULT '',
  delivered_at TEXT NOT NULL
);
CREATE INDEX idx_webhook_deliveries_run ON webhook_deliveries (run_id);
`,
}

func init() {
	migrations = append(migrations, migrationV27)
}
