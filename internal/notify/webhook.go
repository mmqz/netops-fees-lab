// Package notify is the outbound notification channel of the system
// (v0.10-R8): after a detection run completes, a JSON summary is
// delivered to the configured webhook URL; since v0.13-R19 the same
// channel also backflows the HUMAN alert lifecycle decisions — every
// committed ack/clear edge is delivered as its own single-event
// payload, so the duty channel that received the alert also learns
// who confirmed it and when it was closed. The channel is a
// BEST-EFFORT side path — a notification failure NEVER fails the
// operation it describes (run or ack/clear) — but since v0.12-R18
// "best effort" means a BOUNDED retry sequence and a durable audit
// row, not a single shot at a warn line: a receiver that blips for a
// second must not cost the NOC its nightly patrol notification, and
// an operator must be able to answer "did the run actually reach the
// receiver" (webhook_deliveries LEFT JOIN, migration v27) and "did the
// ack notification reach it" (alert_event_deliveries LEFT JOIN,
// migration v28) from the history listings.
//
// Wiring: app.New constructs ONE Webhook from Config.WebhookURL (nil
// when the URL is empty = the channel is off) and hands it to the
// HTTP API (POST /api/detection/run and the ack/clear endpoints fire
// it asynchronously after the response data is computed) and the CLI
// detect command (fires the run summary synchronously before the
// process exits). The MCP surface never fires notifications (the
// read-only red line — alerts_ack/alerts_clear/detection_run are
// banned tool names there).
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/mmqz/netops-lab/internal/detect"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// deliveryTimeout bounds ONE webhook POST attempt. The notifier is a
// side path: a stuck receiver must not pile up goroutines or delay the
// CLI exit.
const deliveryTimeout = 5 * time.Second

// defaultBackoff is the pause between retry attempts (v0.12-R18): one
// initial try + len(defaultBackoff) retries = 3 total attempts. The
// whole sequence is bounded by the CALLER's ctx (the API and the
// scheduler hand a 25s budget — three worst-case 5s attempts plus the
// backoff fit; a cancelled sequence stops early and records what it
// managed). Injectable per instance for tests (zero backoff keeps the
// retry matrix fast).
var defaultBackoff = []time.Duration{1 * time.Second, 2 * time.Second}

// maxPayloadAlerts caps the alerts array of the run payload: the per-alert
// mutation lines exist for the receiver's triage UI, not as a data
// export (the API is the source of truth). Large sweeps stay deliverable.
const maxPayloadAlerts = 50

// auditWriteTimeout bounds the audit-row insert (the recorder's
// WithoutCancel discipline: the delivery verdict must land even when
// the caller's ctx is already exhausted by the retry sequence itself).
const auditWriteTimeout = 5 * time.Second

// alertLine is one mutation of the run payload's alerts array — the lean
// projection of detect.AlertSummary for a notification consumer.
type alertLine struct {
	AlertID    string  `json:"alertId"`
	RuleID     string  `json:"ruleId"`
	MetricKey  string  `json:"metricKey"`
	ResourceID string  `json:"resourceId"`
	Severity   string  `json:"severity"`
	Action     string  `json:"action"`
	State      string  `json:"state"`
	LastValue  float64 `json:"lastValue"`
}

// payload is the run-summary POST body delivered to the webhook URL: the run
// envelope's counters plus the (capped) mutation lines. DeliveredAt is
// the delivery timestamp (server clock), NOT the run's evaluatedAt —
// the receiver separates "when the data was current" from "when the
// notification left".
type payload struct {
	Event            string      `json:"event"`
	DeliveredAt      string      `json:"deliveredAt"`
	Findings         int         `json:"findings"`
	AlertsCreated    int         `json:"alertsCreated"`
	AlertsCleared    int         `json:"alertsCleared"`
	RunStatesWritten int         `json:"runStatesWritten"`
	Suppressed       int         `json:"suppressed"`
	FlapHeld         int         `json:"flapHeld"`
	EvaluatedAt      string      `json:"evaluatedAt"`
	SuppressionNote  string      `json:"suppressionNote,omitempty"` // issue #55: wall-active windows suppressed nothing — the silent-failure eliminator, forwarded verbatim
	Alerts           []alertLine `json:"alerts"`
	AlertsTruncated  bool        `json:"alertsTruncated"`
}

// AlertEvent is the input of one lifecycle notification (v0.13-R19):
// the committed human decision (the ack or clear edge — AFTER it
// landed) plus the alert's read-side context (rule, series →
// resource), read by the API handler while its request context is
// still live and handed to the notifier as a value copy. The wire
// payload derives everything from this struct — no clock, no second
// database read (deterministic: same alert row + same decision →
// same payload except the delivery bookkeeping timestamps).
type AlertEvent struct {
	Event        domain.AlertLifecycleEvent // ack | clear — which human edge
	AlertID      string
	RuleID       string
	RuleLabel    string
	MetricKey    string
	ResourceID   string
	ResourceType string
	NaturalKey   string
	Severity     string
	State        string // the state AFTER the edge (acked | cleared)
	AckedBy      string // the acker's identity; "" on clear events
	TS           string // the OPERATION timestamp (acked_at / cleared_at)
}

// alertEventPayload is the lifecycle POST body: one committed human
// decision with the alert's full context. Every field is ALWAYS
// present (the R18 wire doctrine — a receiver's parser never branches
// on missing keys); ackedBy is "" on clear events (the field is
// meaningful only for ack), and ts is the OPERATION timestamp, not
// the delivery clock — deliveredAt carries that, so the receiver
// separates "when the human acted" from "when the notification left".
type alertEventPayload struct {
	Event        string `json:"event"` // "alert.ack" | "alert.clear"
	AlertID      string `json:"alertId"`
	RuleID       string `json:"ruleId"`
	RuleLabel    string `json:"ruleLabel"`
	MetricKey    string `json:"metricKey"`
	ResourceID   string `json:"resourceId"`
	ResourceType string `json:"resourceType"`
	NaturalKey   string `json:"naturalKey"`
	Severity     string `json:"severity"`
	State        string `json:"state"`
	AckedBy      string `json:"ackedBy"`
	TS           string `json:"ts"`
	DeliveredAt  string `json:"deliveredAt"`
}

// Webhook is the outbound notification channel. A nil *Webhook is a
// valid disabled channel: every method is nil-receiver safe, so call
// sites need no feature branching. db may be nil (audit-less channel —
// the delivery still retries, only the durable verdict rows are
// skipped; the production wiring always passes the repository).
type Webhook struct {
	url     string
	client  *http.Client
	log     *slog.Logger
	db      *repo.DB
	backoff []time.Duration
}

// New returns the notifier for url, or nil when url is empty (the
// channel is off). timeout <= 0 falls back to deliveryTimeout. db may
// be nil (audit-less).
func New(url string, timeout time.Duration, db *repo.DB, log *slog.Logger) *Webhook {
	return newWebhook(url, timeout, defaultBackoff, db, log)
}

// newWebhook builds the notifier with an explicit retry backoff (the
// test seam: a zero schedule runs the retry matrix instantly).
func newWebhook(url string, timeout time.Duration, backoff []time.Duration, db *repo.DB, log *slog.Logger) *Webhook {
	if url == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = deliveryTimeout
	}
	if log == nil {
		log = slog.Default()
	}
	return &Webhook{
		url:     url,
		client:  &http.Client{Timeout: timeout},
		log:     log,
		db:      db,
		backoff: backoff,
	}
}

// URL returns the configured target ("" for the nil channel — useful
// for diagnostics surfaces that show the effective configuration).
func (w *Webhook) URL() string {
	if w == nil {
		return ""
	}
	return w.url
}

// redactURL strips everything sensitive from a webhook target before it
// enters the audit trail: the query string (webhook URLs frequently
// carry a token there) and any userinfo (user:pass@host). The audit row
// must never become a credential dump — it keeps scheme://host/path,
// enough to recognize the receiver, never enough to call it.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "unparsed"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// deliveryVerdict is the outcome of one bounded retry sequence: how
// many POSTs were actually made, the LAST attempt's HTTP status (0 =
// transport error, the request never got an answer) and the failure
// text ("" = some attempt answered 2xx — delivered).
type deliveryVerdict struct {
	attempts   int
	httpStatus int
	errMsg     string
}

// retryDelivery runs the bounded retry sequence over one encoded body:
// the initial attempt plus the configured backoff retries, the whole
// sequence bounded by ctx (a cancelled sequence stops early and
// reports what it managed). Shared by every notify entry point — the
// run summary (v0.10-R8) and the lifecycle events (v0.13-R19) ride the
// SAME wire discipline; only the payload and the audit table differ.
func (w *Webhook) retryDelivery(ctx context.Context, body []byte) deliveryVerdict {
	attempts := 0
	lastStatus := 0
	lastErr := ""
	for i := 0; ; i++ {
		attempts++
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
		if err != nil {
			// Request building cannot recover by retrying (bad URL shape).
			lastErr = err.Error()
			w.log.Warn("webhook: request build failed", "error", lastErr, "url", w.url)
			break
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := w.client.Do(req)
		if err != nil {
			lastStatus, lastErr = 0, err.Error()
			w.log.Warn("webhook: delivery attempt failed",
				"attempt", attempts, "error", lastErr, "url", w.url)
		} else {
			_ = resp.Body.Close()
			lastStatus, lastErr = resp.StatusCode, ""
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return deliveryVerdict{attempts: attempts, httpStatus: lastStatus}
			}
			w.log.Warn("webhook: receiver answered non-2xx",
				"attempt", attempts, "status", resp.Status, "url", w.url)
			lastErr = fmt.Sprintf("receiver status %d", resp.StatusCode)
		}

		// Any retries left (and is the caller still willing to wait)?
		if i >= len(w.backoff) {
			break
		}
		select {
		case <-ctx.Done():
			// The budget is gone: the sequence ends here, the audit row
			// records exactly what was managed.
		case <-time.After(w.backoff[i]):
		}
		if ctx.Err() != nil {
			break
		}
	}
	return deliveryVerdict{attempts: attempts, httpStatus: lastStatus, errMsg: lastErr}
}

// insertDetached runs one audit-row insert on a context that IGNORES
// the caller's cancellation, with its own timeout — the recorder
// doctrine (v0.12-R18): the retry sequence may have exhausted the very
// ctx it was given, and losing the verdict row would make the failed
// delivery invisible, the opposite of the audit's purpose. A nil db or
// a failed insert only logs: the notification channel must never fail
// anything upstream.
func (w *Webhook) insertDetached(ctx context.Context, insert func(context.Context) error) {
	if w.db == nil {
		return
	}
	insCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditWriteTimeout)
	defer cancel()
	if err := insert(insCtx); err != nil {
		w.log.Warn("webhook: delivery audit write failed", "error", err.Error())
	}
}

// NotifyDetectionRun delivers the run summary to the webhook URL with
// the bounded retry sequence (v0.12-R18): the initial attempt plus the
// configured backoff retries, the whole sequence bounded by ctx (a
// cancelled sequence stops early). After the final attempt the verdict
// is written to webhook_deliveries (runID links the receipt; WithoutCancel
// + own timeout so an exhausted ctx does not lose the audit row). A
// non-nil error means every attempt failed; each failure is already
// logged at warn level, callers may ignore the return value.
func (w *Webhook) NotifyDetectionRun(ctx context.Context, res detect.RunResult, runID string) error {
	if w == nil {
		return nil
	}
	p := payload{
		Event:            "detection.run",
		DeliveredAt:      time.Now().UTC().Format(time.RFC3339),
		Findings:         res.Findings,
		AlertsCreated:    res.AlertsCreated,
		AlertsCleared:    res.AlertsCleared,
		RunStatesWritten: res.RunStatesWritten,
		Suppressed:       res.Suppressed,
		FlapHeld:         res.FlapHeld,
		EvaluatedAt:      res.EvaluatedAt,
		SuppressionNote:  res.SuppressionNote,
		Alerts:           make([]alertLine, 0, min(len(res.Alerts), maxPayloadAlerts)),
	}
	for i, line := range res.Alerts {
		if i >= maxPayloadAlerts {
			p.AlertsTruncated = true
			break
		}
		p.Alerts = append(p.Alerts, alertLine{
			AlertID:    line.AlertID,
			RuleID:     line.RuleID,
			MetricKey:  line.MetricKey,
			ResourceID: line.ResourceID,
			Severity:   line.Severity,
			Action:     line.Action,
			State:      line.State,
			LastValue:  line.LastValue,
		})
	}

	body, err := json.Marshal(p)
	if err != nil {
		// Cannot happen for this struct (no channels/funcs), but the
		// honest branch stays: log, audit the failed verdict, return.
		w.log.Warn("webhook: payload encode failed", "error", err.Error())
		w.audit(ctx, runID, 0, 0, err.Error())
		return fmt.Errorf("notify: encode payload: %w", err)
	}

	v := w.retryDelivery(ctx, body)
	if v.errMsg == "" {
		w.log.Info("webhook: detection run summary delivered",
			"url", w.url, "attempts", v.attempts,
			"findings", res.Findings, "alertsCreated", res.AlertsCreated,
			"alertsCleared", res.AlertsCleared)
		w.audit(ctx, runID, v.attempts, v.httpStatus, "")
		return nil
	}
	w.audit(ctx, runID, v.attempts, v.httpStatus, v.errMsg)
	return fmt.Errorf("notify: deliver: %s", v.errMsg)
}

// NotifyAlertLifecycle delivers one committed human decision (ack or
// clear edge, v0.13-R19) to the webhook URL — the same bounded retry
// sequence, the same redaction and the same detached audit discipline
// as the run summary; the verdict lands in alert_event_deliveries
// keyed by (alert_id, event). The payload's ts is the OPERATION
// timestamp (acked_at / cleared_at — ev.TS), never the delivery clock:
// the receiver separates "when the human acted" from deliveredAt. A
// non-nil error means every attempt failed; the ack/clear operation
// itself is already committed and unaffected — callers may ignore the
// return value.
func (w *Webhook) NotifyAlertLifecycle(ctx context.Context, ev AlertEvent) error {
	if w == nil {
		return nil
	}
	p := alertEventPayload{
		Event:        "alert." + ev.Event.String(),
		AlertID:      ev.AlertID,
		RuleID:       ev.RuleID,
		RuleLabel:    ev.RuleLabel,
		MetricKey:    ev.MetricKey,
		ResourceID:   ev.ResourceID,
		ResourceType: ev.ResourceType,
		NaturalKey:   ev.NaturalKey,
		Severity:     ev.Severity,
		State:        ev.State,
		AckedBy:      ev.AckedBy,
		TS:           ev.TS,
		DeliveredAt:  time.Now().UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(p)
	if err != nil {
		// Cannot happen for this struct (no channels/funcs), but the
		// honest branch stays: log, audit the failed verdict, return.
		w.log.Warn("webhook: lifecycle payload encode failed", "error", err.Error())
		w.auditLifecycle(ctx, ev, 0, 0, err.Error())
		return fmt.Errorf("notify: encode lifecycle payload: %w", err)
	}

	v := w.retryDelivery(ctx, body)
	if v.errMsg == "" {
		w.log.Info("webhook: alert lifecycle event delivered",
			"url", w.url, "attempts", v.attempts,
			"event", ev.Event.String(), "alertId", ev.AlertID)
		w.auditLifecycle(ctx, ev, v.attempts, v.httpStatus, "")
		return nil
	}
	w.auditLifecycle(ctx, ev, v.attempts, v.httpStatus, v.errMsg)
	return fmt.Errorf("notify: deliver lifecycle: %s", v.errMsg)
}

// audit persists the run-delivery verdict into webhook_deliveries
// (migration v27). See insertDetached for the cancellation discipline.
func (w *Webhook) audit(ctx context.Context, runID string, attempts, httpStatus int, errMsg string) {
	if w.db == nil {
		return
	}
	status := domain.WebhookDelivered
	if errMsg != "" {
		status = domain.WebhookFailed
	}
	row := domain.WebhookDelivery{
		ID:          domain.NewID("wd"),
		RunID:       runID,
		URL:         redactURL(w.url),
		Status:      status,
		Attempts:    attempts,
		HTTPStatus:  httpStatus,
		Error:       errMsg,
		DeliveredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	w.insertDetached(ctx, func(insCtx context.Context) error {
		return w.db.WebhookDeliveries.InsertDelivery(insCtx, row)
	})
}

// auditLifecycle persists the lifecycle-delivery verdict into
// alert_event_deliveries (migration v28) — the same discipline as
// audit, keyed by the alert + event it describes.
func (w *Webhook) auditLifecycle(ctx context.Context, ev AlertEvent, attempts, httpStatus int, errMsg string) {
	if w.db == nil {
		return
	}
	status := domain.WebhookDelivered
	if errMsg != "" {
		status = domain.WebhookFailed
	}
	row := domain.AlertEventDelivery{
		ID:          domain.NewID("ad"),
		AlertID:     ev.AlertID,
		Event:       ev.Event,
		URL:         redactURL(w.url),
		Status:      status,
		Attempts:    attempts,
		HTTPStatus:  httpStatus,
		Error:       errMsg,
		DeliveredAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	w.insertDetached(ctx, func(insCtx context.Context) error {
		return w.db.AlertEventDeliveries.InsertDelivery(insCtx, row)
	})
}
