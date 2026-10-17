package repo

// migrationV28 adds the alert lifecycle notification audit (schema
// version 28, v0.13-R19): one row per attempted notification of a human
// ack/clear decision — the retry sequence's durable verdict, surfaced in
// the alerts listing via LEFT JOIN (lifecycleDelivery).
//
//   - alert_id is deliberately NOT an FK (the v27 run_id precedent):
//     the delivery row is written by the notify side path AFTER the
//     ack/clear transition committed (its own transaction); an alert
//     whose notification happened while the channel was off simply has
//     no row, and the honest projection of that is "" (never
//     notified), not a dangling reference;
//   - event CHECK-closed (ack|clear): only the two HUMAN edges of the
//     ADR-0011 D3 state machine are backflowed — the engine recovery
//     clear is a run-notification mutation line, not a lifecycle
//     event;
//   - status CHECK-closed (delivered|failed): same closed set as v27,
//     no pending — the row lands once, after the final attempt;
//   - http_status 0 means "transport error" (the request never got an
//     answer) — the v27 sentinel;
//   - the URL column stores the notify channel's REDACTED form (query
//     string and userinfo stripped) — the audit trail must never
//     become a credential dump.
//
// Pure CREATE TABLE + one index on alert_id (the v0.12 engineering
// hygiene: zero rebuild, zero data movement). The alerts table is
// untouched: the three-edge state machine stays frozen (ADR-0011 D3) —
// this is an observability side path, never a state edge, and there is
// NO timeout/auto action attached to it.
var migrationV28 = Migration{
	Version: 28,
	Name:    "r19_alert_event_delivery_audit",
	SQL: `
CREATE TABLE alert_event_deliveries (
  id           TEXT PRIMARY KEY,
  alert_id     TEXT NOT NULL,
  event        TEXT NOT NULL CHECK (event IN ('ack','clear')),
  url          TEXT NOT NULL,
  status       TEXT NOT NULL CHECK (status IN ('delivered','failed')),
  attempts     INTEGER NOT NULL,
  http_status  INTEGER NOT NULL DEFAULT 0,
  error        TEXT NOT NULL DEFAULT '',
  delivered_at TEXT NOT NULL
);
CREATE INDEX idx_alert_event_deliveries_alert ON alert_event_deliveries (alert_id);
`,
}

func init() {
	migrations = append(migrations, migrationV28)
}
