package notify

// lifecycle_test.go — pins the v0.13-R19 alert lifecycle notification:
// the ack/clear event payload contract (wire shape, the operation ts
// not the delivery clock), the SHARED retry skeleton (a blipping
// receiver is absorbed exactly like a run summary — same wire
// discipline), the dead-receiver audit verdict, URL redaction, and the
// nil-channel no-op. The audit rows land in alert_event_deliveries
// (migration v28) keyed by (alert_id, event).

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// sampleAckEvent is one committed ack decision with the alert's
// read-side context (what the API handler reads via GetAlertRowByID).
func sampleAckEvent() AlertEvent {
	return AlertEvent{
		Event:        domain.AlertLifecycleAck,
		AlertID:      "al_r19_1",
		RuleID:       "dr_opt_rx_low",
		RuleLabel:    "接收光功率持续偏低",
		MetricKey:    "optical_power_rx_avg_dbm",
		ResourceID:   "res_port_1",
		ResourceType: "port",
		NaturalKey:   "DEV-001|eth0",
		Severity:     "warning",
		State:        "acked",
		AckedBy:      "运维-张三",
		TS:           "2026-10-18T09:30:00Z",
	}
}

// sampleClearEvent is the clear of the same alert (firing→cleared
// directly — the human override edge).
func sampleClearEvent() AlertEvent {
	ev := sampleAckEvent()
	ev.Event = domain.AlertLifecycleClear
	ev.State = "cleared"
	ev.AckedBy = "" // the field is meaningful only for ack events
	ev.TS = "2026-10-18T11:00:00Z"
	return ev
}

// TestLifecycleNilChannelIsNoOp pins the disabled-channel contract for
// the lifecycle entry point: the nil receiver is a safe no-op.
func TestLifecycleNilChannelIsNoOp(t *testing.T) {
	var wh *Webhook
	if err := wh.NotifyAlertLifecycle(context.Background(), sampleAckEvent()); err != nil {
		t.Fatalf("nil NotifyAlertLifecycle() error = %v, want nil", err)
	}
}

// TestLifecycleAckPayloadContract pins the ack wire contract: POST +
// application/json + the dotted event form aligned with the run
// summary's "detection.run" ("alert.ack"), every field ALWAYS present
// (the R18 wire doctrine), and the ts being the OPERATION timestamp —
// not the delivery clock (deliveredAt is a separate key).
func TestLifecycleAckPayloadContract(t *testing.T) {
	srv, ch := newReceiver()
	defer srv.Close()

	wh := New(srv.URL, time.Second, nil, slog.New(slog.DiscardHandler))
	if err := wh.NotifyAlertLifecycle(context.Background(), sampleAckEvent()); err != nil {
		t.Fatalf("NotifyAlertLifecycle() error = %v", err)
	}

	select {
	case got := <-ch:
		if got.method != http.MethodPost {
			t.Errorf("method = %s, want POST", got.method)
		}
		if got.ctype != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got.ctype)
		}
		if ev, _ := got.body["event"].(string); ev != "alert.ack" {
			t.Errorf("event = %q, want alert.ack", ev)
		}
		if id, _ := got.body["alertId"].(string); id != "al_r19_1" {
			t.Errorf("alertId = %q, want al_r19_1", id)
		}
		for key, want := range map[string]string{
			"ruleId":       "dr_opt_rx_low",
			"ruleLabel":    "接收光功率持续偏低",
			"metricKey":    "optical_power_rx_avg_dbm",
			"resourceId":   "res_port_1",
			"resourceType": "port",
			"naturalKey":   "DEV-001|eth0",
			"severity":     "warning",
			"state":        "acked",
			"ackedBy":      "运维-张三",
		} {
			if gotv, _ := got.body[key].(string); gotv != want {
				t.Errorf("%s = %q, want %q", key, gotv, want)
			}
		}
		// The operation ts (acked_at), never the delivery clock.
		if ts, _ := got.body["ts"].(string); ts != "2026-10-18T09:30:00Z" {
			t.Errorf("ts = %q, want the operation timestamp", ts)
		}
		if _, ok := got.body["deliveredAt"].(string); !ok {
			t.Error("deliveredAt missing or not a string (the delivery clock key)")
		}
		// Every key of the documented shape is present (a receiver never
		// branches on missing keys).
		for _, key := range []string{"event", "alertId", "ruleId", "ruleLabel", "metricKey",
			"resourceId", "resourceType", "naturalKey", "severity", "state", "ackedBy", "ts", "deliveredAt"} {
			if _, ok := got.body[key]; !ok {
				t.Errorf("payload missing %q", key)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no delivery arrived within 2s")
	}
}

// TestLifecycleClearPayloadContract pins the clear wire contract: the
// event form flips to alert.clear, the state is the post-edge cleared,
// and ackedBy is "" (the field is meaningful only for ack events) but
// STILL PRESENT — the payload shape is identical across events.
func TestLifecycleClearPayloadContract(t *testing.T) {
	srv, ch := newReceiver()
	defer srv.Close()

	wh := New(srv.URL, time.Second, nil, slog.New(slog.DiscardHandler))
	if err := wh.NotifyAlertLifecycle(context.Background(), sampleClearEvent()); err != nil {
		t.Fatalf("NotifyAlertLifecycle() error = %v", err)
	}

	select {
	case got := <-ch:
		if ev, _ := got.body["event"].(string); ev != "alert.clear" {
			t.Errorf("event = %q, want alert.clear", ev)
		}
		if st, _ := got.body["state"].(string); st != "cleared" {
			t.Errorf("state = %q, want cleared (the state AFTER the edge)", st)
		}
		if by, ok := got.body["ackedBy"].(string); !ok || by != "" {
			t.Errorf("ackedBy = %v (ok=%v), want the always-present empty string on clear", by, ok)
		}
		if ts, _ := got.body["ts"].(string); ts != "2026-10-18T11:00:00Z" {
			t.Errorf("ts = %q, want the clear operation timestamp", ts)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no delivery arrived within 2s")
	}
}

// TestLifecycleRetryAbsorbsTransientFailures pins the SHARED retry
// skeleton: a receiver failing twice then succeeding is DELIVERED on
// the third attempt (exactly the run-summary matrix — the lifecycle
// events ride the same wire discipline), and the audit row records
// attempts=3 / delivered in alert_event_deliveries, keyed by
// (alert_id, event) with the REDACTED URL.
func TestLifecycleRetryAbsorbsTransientFailures(t *testing.T) {
	db := newAuditDB(t)
	srv, hits := flakyReceiver(t, 2)

	// The configured target carries a token in the query.
	wh := newWebhook(srv.URL+"?token=sekrit", time.Second, []time.Duration{0, 0}, db, nil)
	if wh == nil {
		t.Fatal("newWebhook = nil, want live notifier")
	}
	if err := wh.NotifyAlertLifecycle(context.Background(), sampleAckEvent()); err != nil {
		t.Fatalf("NotifyAlertLifecycle after transients: %v (want absorbed)", err)
	}
	if got := atomic.LoadInt32(hits); got != 3 {
		t.Errorf("receiver hits = %d, want 3 (initial + 2 retries)", got)
	}

	d, err := db.AlertEventDeliveries.LatestByAlert(context.Background(), "al_r19_1")
	if err != nil {
		t.Fatalf("LatestByAlert: %v", err)
	}
	if d.Status != domain.WebhookDelivered || d.Attempts != 3 {
		t.Errorf("audit = %+v, want delivered / attempts 3", d)
	}
	if d.HTTPStatus != http.StatusOK {
		t.Errorf("audit httpStatus = %d, want 200", d.HTTPStatus)
	}
	if d.Event != domain.AlertLifecycleAck {
		t.Errorf("audit event = %q, want ack", d.Event)
	}
	if want := srv.URL; d.URL != want {
		t.Errorf("audit url = %q, want %q (query stripped)", d.URL, want)
	}
	if d.Error != "" {
		t.Errorf("audit error = %q, want empty on success", d.Error)
	}
}

// TestLifecycleRetryExhaustedLeavesFailedVerdict pins the dead-receiver
// path: three 503s → a non-nil error and the audit row records
// failed / attempts=3 / httpStatus=503 — the alerts listing answers
// "the ack never reached the duty channel" honestly.
func TestLifecycleRetryExhaustedLeavesFailedVerdict(t *testing.T) {
	db := newAuditDB(t)
	srv, hits := flakyReceiver(t, 1000) // always 503

	wh := newWebhook(srv.URL, time.Second, []time.Duration{0, 0}, db, nil)
	err := wh.NotifyAlertLifecycle(context.Background(), sampleClearEvent())
	if err == nil {
		t.Fatal("NotifyAlertLifecycle on a dead receiver = nil error, want failure")
	}
	if got := atomic.LoadInt32(hits); got != 3 {
		t.Errorf("receiver hits = %d, want 3 (bounded — no infinite retry)", got)
	}

	d, err := db.AlertEventDeliveries.LatestByAlert(context.Background(), "al_r19_1")
	if err != nil {
		t.Fatalf("LatestByAlert: %v", err)
	}
	if d.Status != domain.WebhookFailed || d.Attempts != 3 {
		t.Fatalf("audit = %+v, want failed / attempts 3", d)
	}
	if d.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("audit httpStatus = %d, want 503", d.HTTPStatus)
	}
	if d.Error == "" {
		t.Error("audit error empty, want the failure text")
	}
	if d.Event != domain.AlertLifecycleClear {
		t.Errorf("audit event = %q, want clear", d.Event)
	}
}

// TestLifecycleNilDBStillDelivers pins the audit-less seam for the
// lifecycle entry point: db=nil retries and delivers normally (the
// verdict row is simply skipped — no panic).
func TestLifecycleNilDBStillDelivers(t *testing.T) {
	srv, hits := flakyReceiver(t, 1) // fails once, then 200
	wh := newWebhook(srv.URL, time.Second, []time.Duration{0, 0}, nil, nil)
	if err := wh.NotifyAlertLifecycle(context.Background(), sampleAckEvent()); err != nil {
		t.Fatalf("NotifyAlertLifecycle (nil db): %v (want absorbed)", err)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Errorf("receiver hits = %d, want 2", got)
	}
}
