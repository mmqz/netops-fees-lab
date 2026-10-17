package domain

// alerteventdeliveries.go — the alert lifecycle notification audit
// (v0.13-R19, migration v28): one row per ATTEMPTED notification of a
// human alert lifecycle decision (ack or clear — the two human edges of
// the ADR-0011 D3 three-edge state machine). The alert quality rounds
// (v0.12 R15-R18) closed the machine's side: suppress noise before the
// fire, triage what fired, and prove the run notification reached the
// receiver; what stayed dark was the HUMAN half of the loop — the duty
// channel received the alert, the operator acted, and the channel never
// learned who acked or when it was cleared. This table is the durable
// verdict of that backflow: every ack/clear notification's retry
// sequence lands exactly one row, read back through the alerts listing's
// LEFT JOIN (lifecycleDelivery).
//
// The delivery STATUS vocabulary is deliberately SHARED with
// webhook_deliveries (domain.WebhookDeliveryStatus, migration v27): both
// tables store the verdict of the SAME notify channel retry sequence
// (delivered | failed, no pending — the row lands once, after the final
// attempt), written by the same side path after the triggering
// operation committed. The EVENT vocabulary is genuinely new and lives
// here: which lifecycle edge the notification describes.

import "fmt"

// AlertLifecycleEvent is the closed event set of migration v28's CHECK:
// ack (firing→acked, the human confirmation edge) | clear (the human
// override recovery edge). The engine's own recovery clear is NOT a
// lifecycle event — it is already a mutation line of the run
// notification (v0.10-R8); only the DECISIONS a human made through
// POST /api/alerts/{id}/ack and /clear are backflowed.
type AlertLifecycleEvent string

// The closed lifecycle event set.
const (
	AlertLifecycleAck   AlertLifecycleEvent = "ack"
	AlertLifecycleClear AlertLifecycleEvent = "clear"
)

// String implements fmt.Stringer.
func (e AlertLifecycleEvent) String() string { return string(e) }

// IsValid reports whether e is one of the two closed-enum values.
func (e AlertLifecycleEvent) IsValid() bool {
	switch e {
	case AlertLifecycleAck, AlertLifecycleClear:
		return true
	}
	return false
}

// ParseAlertLifecycleEvent converts s into an AlertLifecycleEvent;
// unknown values are an error (the API boundary).
func ParseAlertLifecycleEvent(s string) (AlertLifecycleEvent, error) {
	v := AlertLifecycleEvent(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid alert lifecycle event %q (ack/clear)", s)
	}
	return v, nil
}

// AlertEventDelivery is one row of the alert_event_deliveries table
// (migration v28, v0.13-R19): the durable outcome of ONE lifecycle
// notification's retry sequence, linked to the alert it describes.
//
//   - AlertID references alerts.alert_id (deliberately NOT an FK — the
//     v27 run_id precedent: the audit row is written by the notify side
//     path AFTER the ack/clear transition committed, in its own
//     transaction; an alert acked while the channel was off simply has
//     no row, and the honest projection of that is "" (never
//     notified), not a dangling reference);
//   - Event is the lifecycle edge the notification describes (CHECK
//     closed to ack|clear — the domain parse is the gate, the
//     constraint the backstop);
//   - Status reuses the WebhookDeliveryStatus closed set (see the file
//     doc — one retry-verdict vocabulary across both audit tables);
//   - URL is the REDACTED target (redactURL strips query string and
//     userinfo before storage — webhook URLs frequently carry tokens
//     in the query, and the audit trail must never become a
//     credential dump);
//   - Attempts is the number of POSTs actually made (1..3, bounded by
//     the caller's async budget);
//   - HTTPStatus is the LAST attempt's response code (0 = transport
//     error);
//   - Error is the last attempt's failure text ("" on success);
//   - DeliveredAt is the wall-clock timestamp of the final verdict
//     (bookkeeping — the payload's ts stays the OPERATION timestamp
//     (acked_at / cleared_at), so the receiver separates "when the
//     human acted" from "when the notification left").
type AlertEventDelivery struct {
	ID          string
	AlertID     string
	Event       AlertLifecycleEvent
	URL         string
	Status      WebhookDeliveryStatus
	Attempts    int
	HTTPStatus  int
	Error       string
	DeliveredAt string // RFC3339 clock
}
