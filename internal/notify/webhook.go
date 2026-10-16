// Package notify is the outbound notification channel of the detection
// loop (v0.10-R8): after a detection run completes, a JSON summary is
// delivered to the configured webhook URL. The channel is a BEST-EFFORT
// side path — a notification failure NEVER fails the detection run
// itself; the operator sees a warn line in the structured log.
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
	"time"

	"github.com/mmqz/netops-lab/internal/detect"
)

// deliveryTimeout bounds one webhook POST. The notifier is a side path:
// a stuck receiver must not pile up goroutines or delay the CLI exit.
const deliveryTimeout = 5 * time.Second

// maxPayloadAlerts caps the alerts array of the payload: the per-alert
// mutation lines exist for the receiver's triage UI, not as a data
// export (the API is the source of truth). Large sweeps stay deliverable.
const maxPayloadAlerts = 50

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
	EvaluatedAt      string      `json:"evaluatedAt"`
	Alerts           []alertLine `json:"alerts"`
	AlertsTruncated  bool        `json:"alertsTruncated"`
}

// Webhook is the detection-run notifier. A nil *Webhook is a valid
// disabled channel: every method is nil-receiver safe, so call sites
// need no feature branching.
type Webhook struct {
	url    string
	client *http.Client
	log    *slog.Logger
}

// New returns the notifier for url, or nil when url is empty (the
// channel is off). timeout <= 0 falls back to deliveryTimeout.
func New(url string, timeout time.Duration, log *slog.Logger) *Webhook {
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
		url:    url,
		client: &http.Client{Timeout: timeout},
		log:    log,
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

// NotifyDetectionRun delivers the run summary to the webhook URL. The
// ctx bounds the whole attempt (the HTTP handler's request ctx is NOT
// used — the delivery outlives the response). A non-nil error means the
// delivery was attempted and failed; the failure is already logged at
// warn level, callers may ignore the return value.
func (w *Webhook) NotifyDetectionRun(ctx context.Context, res detect.RunResult) error {
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
		// honest branch stays: log and return without delivery.
		w.log.Warn("webhook: payload encode failed", "error", err.Error())
		return fmt.Errorf("notify: encode payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		w.log.Warn("webhook: request build failed", "error", err.Error(), "url", w.url)
		return fmt.Errorf("notify: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		w.log.Warn("webhook: delivery failed", "error", err.Error(), "url", w.url)
		return fmt.Errorf("notify: deliver: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		w.log.Warn("webhook: receiver answered non-2xx", "error", resp.Status, "url", w.url)
		return fmt.Errorf("notify: receiver status %d", resp.StatusCode)
	}
	w.log.Info("webhook: detection run summary delivered",
		"url", w.url, "findings", res.Findings, "alertsCreated", res.AlertsCreated,
		"alertsCleared", res.AlertsCleared)
	return nil
}
