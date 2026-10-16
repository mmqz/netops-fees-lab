package domain

// webhookdeliveries.go — the outbound notification audit (v0.12-R18):
// one row per ATTEMPTED webhook notification of a detection run, written
// by the notify channel after its (bounded) retry sequence reaches a
// final verdict. The notification is a BEST-EFFORT side path — the run
// itself never fails on a delivery failure — but "best effort" is not
// "invisible": the operator must be able to answer "did the nightly
// patrol actually reach the receiver" from the run history, which is
// exactly what the runs listing's LEFT JOIN over this table surfaces.

import "fmt"

// WebhookDeliveryStatus is the closed verdict set of one notification's
// retry sequence (migration v27 CHECK): delivered (some attempt answered
// 2xx) | failed (every attempt failed — transport error or non-2xx).
// There is no "pending": the row is written once, after the final
// attempt, so a run without a row means the channel was OFF (nil
// webhook), not "still trying" — the honest three-valued read the
// listing projects as "" (channel off) / delivered / failed.
type WebhookDeliveryStatus string

// The closed delivery status set.
const (
	WebhookDelivered WebhookDeliveryStatus = "delivered"
	WebhookFailed    WebhookDeliveryStatus = "failed"
)

// String implements fmt.Stringer.
func (s WebhookDeliveryStatus) String() string { return string(s) }

// IsValid reports whether s is one of the two closed-enum values.
func (s WebhookDeliveryStatus) IsValid() bool {
	switch s {
	case WebhookDelivered, WebhookFailed:
		return true
	}
	return false
}

// ParseWebhookDeliveryStatus converts s into a WebhookDeliveryStatus;
// unknown values are an error (the API boundary).
func ParseWebhookDeliveryStatus(s string) (WebhookDeliveryStatus, error) {
	v := WebhookDeliveryStatus(s)
	if !v.IsValid() {
		return "", fmt.Errorf("domain: invalid webhook delivery status %q (delivered/failed)", s)
	}
	return v, nil
}

// WebhookDelivery is one row of the webhook_deliveries table (migration
// v27, v0.12-R18): the durable outcome of ONE notification's retry
// sequence, linked to its triggering run receipt.
//
//   - RunID references detection_runs.id (NOT a FK: the audit row is
//     written AFTER the receipt commits, in the side-path's own
//     transaction — a receipt can outlive its delivery record or, when
//     the channel is off, never have one; the listing's LEFT JOIN is the
//     honest projection of both cases);
//   - URL is the REDACTED target (query string and userinfo stripped by
//     the notify channel before storage — a webhook URL frequently
//     carries a token in the query, and the audit trail must never
//     become a credential dump);
//   - Attempts is the number of POSTs actually made (1..maxAttempts —
//     bounded by the caller's ctx: a cancelled sequence records what it
//     managed);
//   - HTTPStatus is the LAST attempt's response code (0 = transport
//     error, the request never got an answer);
//   - Error is the last attempt's failure text ("" on success);
//   - DeliveredAt is the wall-clock timestamp of the final verdict
//     (bookkeeping, not data path — the run's data timestamps stay on
//     the data timeline).
type WebhookDelivery struct {
	ID          string
	RunID       string
	URL         string
	Status      WebhookDeliveryStatus
	Attempts    int
	HTTPStatus  int
	Error       string
	DeliveredAt string // RFC3339 clock
}
