package repo

// webhookdeliveries.go — the notification audit storage (v0.12-R18,
// migration v27): inserts by the notify channel's side path (after the
// retry sequence's final verdict), reads by the runs listing's LEFT
// JOIN. All SQL of the delivery-audit channel lives here.

import (
	"context"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
)

// WebhookDeliveryRepo persists the webhook delivery audit rows.
type WebhookDeliveryRepo struct{ q DBTX }

// InsertDelivery stores one notification verdict. The row is complete
// when written (no pending state, no updates — a retry sequence writes
// exactly one row, after its last attempt).
func (r *WebhookDeliveryRepo) InsertDelivery(ctx context.Context, d domain.WebhookDelivery) error {
	_, err := r.q.ExecContext(ctx, `
INSERT INTO webhook_deliveries (id, run_id, url, status, attempts, http_status, error, delivered_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.RunID, d.URL, d.Status.String(), d.Attempts, d.HTTPStatus, d.Error, d.DeliveredAt)
	if err != nil {
		return fmt.Errorf("repo: insert webhook delivery %s: %w", d.ID, err)
	}
	return nil
}

// GetByRun returns the delivery verdict of one run (the latest when a
// run somehow has more than one row — the production channel writes
// exactly one). Unknown run answers domain.ErrNotFound.
func (r *WebhookDeliveryRepo) GetByRun(ctx context.Context, runID string) (domain.WebhookDelivery, error) {
	row := r.q.QueryRowContext(ctx, `
SELECT id, run_id, url, status, attempts, http_status, error, delivered_at
FROM webhook_deliveries WHERE run_id = ? ORDER BY delivered_at DESC, id ASC LIMIT 1`, runID)
	var (
		d      domain.WebhookDelivery
		status string
	)
	if err := row.Scan(&d.ID, &d.RunID, &d.URL, &status, &d.Attempts, &d.HTTPStatus, &d.Error, &d.DeliveredAt); err != nil {
		return domain.WebhookDelivery{}, notFound("webhook delivery", runID, err)
	}
	d.Status = domain.WebhookDeliveryStatus(status)
	return d, nil
}

// DeliveryJoin is the projection the runs listing LEFT JOINs onto every
// receipt row: the run's notification verdict, or the zero form (Status
// "" = the channel was off, no row exists) — the honest three-valued
// read (delivered / failed / off) the API and the UI project.
type DeliveryJoin struct {
	Status     domain.WebhookDeliveryStatus // "" = channel off (no row)
	Attempts   int
	HTTPStatus int
	Error      string
}

// deliveryJoinOf scans one LEFT-JOIN projection row.
func deliveryJoinOf(scan func(...any) error) (DeliveryJoin, error) {
	var (
		status string
		j      DeliveryJoin
	)
	if err := scan(&status, &j.Attempts, &j.HTTPStatus, &j.Error); err != nil {
		return DeliveryJoin{}, err
	}
	j.Status = domain.WebhookDeliveryStatus(status)
	return j, nil
}
