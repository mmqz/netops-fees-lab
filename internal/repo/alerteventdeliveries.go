package repo

// alerteventdeliveries.go — the alert lifecycle notification audit
// storage (v0.13-R19, migration v28): inserts by the notify channel's
// side path (after the ack/clear transition committed and the retry
// sequence reached its verdict), reads by the alerts listing's LEFT
// JOIN (lifecycleDelivery) and LatestByAlert. All SQL of the lifecycle
// delivery-audit channel lives here.

import (
	"context"
	"fmt"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// AlertEventDeliveryRepo persists the alert lifecycle notification
// audit rows.
type AlertEventDeliveryRepo struct{ q DBTX }

// InsertDelivery stores one lifecycle notification verdict. The row is
// complete when written (no pending state, no updates — a retry
// sequence writes exactly one row, after its last attempt).
func (r *AlertEventDeliveryRepo) InsertDelivery(ctx context.Context, d domain.AlertEventDelivery) error {
	_, err := r.q.ExecContext(ctx, `
INSERT INTO alert_event_deliveries (id, alert_id, event, url, status, attempts, http_status, error, delivered_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.AlertID, d.Event.String(), d.URL, d.Status.String(), d.Attempts, d.HTTPStatus, d.Error, d.DeliveredAt)
	if err != nil {
		return fmt.Errorf("repo: insert alert event delivery %s: %w", d.ID, err)
	}
	return nil
}

// LatestByAlert returns the LATEST delivery verdict of one alert (the
// newest human decision the channel learned about). "Latest" is decided
// by the EVENT's edge order, not by any clock: an alert has at most one
// ack and one clear edge (the transition guards reject repeats with
// 409), and whenever both exist the clear is the later decision BY THE
// STATE MACHINE — ack requires firing, clear requires firing|acked, so
// ack ≺ clear always. Neither rowid (concurrent inserts from the ack
// and clear goroutines can take the SQLite write lock in either order)
// nor delivered_at (a wall-clock string) is a trustworthy ordering key
// for this question; the CHECK-closed edge set is. delivered_at and id
// only tie-break hand-fabricated multi-row data. Unknown alert answers
// domain.ErrNotFound.
func (r *AlertEventDeliveryRepo) LatestByAlert(ctx context.Context, alertID string) (domain.AlertEventDelivery, error) {
	row := r.q.QueryRowContext(ctx, `
SELECT id, alert_id, event, url, status, attempts, http_status, error, delivered_at
FROM alert_event_deliveries WHERE alert_id = ?
ORDER BY CASE event WHEN 'clear' THEN 1 ELSE 0 END DESC, delivered_at DESC, id ASC
LIMIT 1`, alertID)
	var (
		d      domain.AlertEventDelivery
		event  string
		status string
	)
	if err := row.Scan(&d.ID, &d.AlertID, &event, &d.URL, &status, &d.Attempts, &d.HTTPStatus, &d.Error, &d.DeliveredAt); err != nil {
		return domain.AlertEventDelivery{}, notFound("alert event delivery", alertID, err)
	}
	if _, err := domain.ParseAlertLifecycleEvent(event); err != nil {
		return domain.AlertEventDelivery{}, fmt.Errorf("repo: alert event delivery %s: %w", d.ID, err)
	}
	d.Event = domain.AlertLifecycleEvent(event)
	d.Status = domain.WebhookDeliveryStatus(status)
	return d, nil
}

// LifecycleJoin is the projection the alerts listing LEFT JOINs onto
// every alert row: the alert's latest lifecycle notification verdict,
// or the zero form (Status "" = the alert was never notified — never
// acked/cleared through this system, or the channel was off at the
// time; Event "" with it) — the honest three-valued read
// (delivered / failed / never) the API and the UI project.
type LifecycleJoin struct {
	Status   domain.WebhookDeliveryStatus // "" = never notified (no row)
	Event    domain.AlertLifecycleEvent   // ack | clear ("" with Status "")
	Attempts int
	TS       string // delivered_at of the verdict row
}

// lifecycleJoinOf scans one LEFT-JOIN projection row (sql.Null* → the
// zero join when the alert has no delivery row).
func lifecycleJoinOf(status, event string, attempts int, ts string) LifecycleJoin {
	return LifecycleJoin{
		Status:   domain.WebhookDeliveryStatus(status),
		Event:    domain.AlertLifecycleEvent(event),
		Attempts: attempts,
		TS:       ts,
	}
}
