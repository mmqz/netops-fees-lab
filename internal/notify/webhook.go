// Package notify is the outbound notification channel of the detection
// loop (v0.10-R8): after a detection run completes, a JSON summary is
// delivered to the configured webhook URL. The channel is a BEST-EFFORT
// side path — a notification failure NEVER fails the detection run
// itself — but since v0.12-R18 "best effort" means a BOUNDED retry
// sequence and a durable audit row, not a single shot at a warn line:
// a receiver that blips for a second must not cost the NOC its nightly
// patrol notification, and an operator must be able to answer "did the
// run actually reach the receiver" from the run history (the
// webhook_deliveries LEFT JOIN, migration v27).
//
// Wiring: app.New constructs ONE Webhook from Config.WebhookURL (nil
// when the URL is empty = the channel is off) and hands it to both the
// HTTP API (POST /api/detection/run fires it asynchronously after the
// 200 response is computed) and the CLI detect command (fires it
// synchronously before the process exits, so the delivery is guaranteed
// to be attempted). The MCP surface never fires notifications (the
// read-only red line — alerts_ack/alerts_clear/detection_run are banned
// tool names there).
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

// maxPayloadAlerts caps the alerts array of the payload: the per-alert
// mutation lines exist for the receiver's triage UI, not as a data
// export (the API is the source of truth). Large sweeps stay deliverable.
const maxPayloadAlerts = 50

// auditWriteTimeout bounds the audit-row insert (the recorder's
// WithoutCancel discipline: the delivery verdict must land even when
// the caller's ctx is already exhausted by the retry sequence itself).
const auditWriteTimeout = 5 * time.Second

// alertLine is one mutation of the payload's alerts array — the lean
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

// payload is the POST body delivered to the webhook URL: the run
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
	Alerts           []alertLine `json:"alerts"`
	AlertsTruncated  bool        `json:"alertsTruncated"`
}

// Webhook is the detection-run notifier. A nil *Webhook is a valid
// disabled channel: every method is nil-receiver safe, so call sites
// need no feature branching. db may be nil (audit-less channel — the
// delivery still retries, only the durable verdict row is skipped; the
// production wiring always passes the repository).
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

	// The retry sequence: attempts 1..(1+len(backoff)), bounded by ctx.
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
				w.log.Info("webhook: detection run summary delivered",
					"url", w.url, "attempts", attempts,
					"findings", res.Findings, "alertsCreated", res.AlertsCreated,
					"alertsCleared", res.AlertsCleared)
				w.audit(ctx, runID, attempts, lastStatus, "")
				return nil
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

	w.audit(ctx, runID, attempts, lastStatus, lastErr)
	return fmt.Errorf("notify: deliver: %s", lastErr)
}

// audit persists the delivery verdict. The write detaches from the
// caller's cancellation (the retry sequence may have exhausted the very
// ctx it was given — losing the verdict row would make the failed
// delivery invisible, the opposite of the audit's purpose). A nil db or
// a failed insert only logs: the notification channel must never fail
// anything upstream.
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
	insCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), auditWriteTimeout)
	defer cancel()
	if err := w.db.WebhookDeliveries.InsertDelivery(insCtx, row); err != nil {
		w.log.Warn("webhook: delivery audit write failed", "error", err.Error())
	}
}
