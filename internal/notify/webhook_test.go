package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/detect"
)

// received is one webhook delivery captured by the test receiver.
type received struct {
	method string
	ctype  string
	body   map[string]any
}

// newReceiver returns an in-memory webhook receiver: every POST body is
// captured (JSON-decoded) and pushed to the channel; non-POST requests
// answer 405.
func newReceiver() (*httptest.Server, chan received) {
	ch := make(chan received, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		ch <- received{method: r.Method, ctype: r.Header.Get("Content-Type"), body: body}
		w.WriteHeader(http.StatusOK)
	}))
	return srv, ch
}

func sampleResult() detect.RunResult {
	return detect.RunResult{
		Findings:         7,
		AlertsCreated:    3,
		AlertsCleared:    1,
		RunStatesWritten: 2,
		Suppressed:       4,
		FlapHeld:         2,
		EvaluatedAt:      "2026-10-15T08:00:00Z",
		Alerts: []detect.AlertSummary{
			{
				AlertID: "al_1", RuleID: "dr_opt", SeriesID: "ms_1", ResourceID: "r_1",
				MetricKey: "optical_power_rx_avg_dbm", Severity: "error", Action: "created",
				State: "firing", FirstSeen: "2026-10-14T00:00:00Z", FiredAt: "2026-10-15T08:00:00Z",
				ClearedAt: "", LastValue: -18.5,
			},
			{
				AlertID: "al_2", RuleID: "dr_loss", SeriesID: "ms_2", ResourceID: "r_2",
				MetricKey: "packet_loss_rx_pct", Severity: "warning", Action: "cleared",
				State: "cleared", FirstSeen: "2026-10-13T00:00:00Z", FiredAt: "2026-10-14T00:00:00Z",
				ClearedAt: "2026-10-15T08:00:00Z", LastValue: 0.2,
			},
		},
	}
}

// TestNilChannelIsNoOp pins the disabled-channel contract: New("") is
// nil and every method on the nil receiver is a safe no-op (call sites
// never branch on the channel being configured).
func TestNilChannelIsNoOp(t *testing.T) {
	if wh := New("", time.Second, nil); wh != nil {
		t.Fatalf("New(\"\") = %v, want nil (channel off)", wh)
	}
	var wh *Webhook
	if got := wh.URL(); got != "" {
		t.Fatalf("nil URL() = %q, want \"\"", got)
	}
	if err := wh.NotifyDetectionRun(context.Background(), sampleResult()); err != nil {
		t.Fatalf("nil NotifyDetectionRun() error = %v, want nil", err)
	}
}

// TestPayloadContract pins the wire contract: POST + application/json +
// the documented envelope (event/deliveredAt/counters/evaluatedAt) and
// the per-alert mutation lines.
func TestPayloadContract(t *testing.T) {
	srv, ch := newReceiver()
	defer srv.Close()

	wh := New(srv.URL, time.Second, slog.New(slog.DiscardHandler))
	if wh == nil {
		t.Fatal("New(url) = nil, want a live notifier")
	}
	if err := wh.NotifyDetectionRun(context.Background(), sampleResult()); err != nil {
		t.Fatalf("NotifyDetectionRun() error = %v", err)
	}

	select {
	case got := <-ch:
		if got.method != http.MethodPost {
			t.Errorf("method = %s, want POST", got.method)
		}
		if got.ctype != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got.ctype)
		}
		if ev, _ := got.body["event"].(string); ev != "detection.run" {
			t.Errorf("event = %q, want detection.run", ev)
		}
		if _, ok := got.body["deliveredAt"].(string); !ok {
			t.Error("deliveredAt missing or not a string")
		}
		for key, want := range map[string]float64{
			"findings": 7, "alertsCreated": 3, "alertsCleared": 1, "runStatesWritten": 2, "suppressed": 4, "flapHeld": 2,
		} {
			if gotv, _ := got.body[key].(float64); gotv != want {
				t.Errorf("%s = %v, want %v", key, gotv, want)
			}
		}
		if ev, _ := got.body["evaluatedAt"].(string); ev != "2026-10-15T08:00:00Z" {
			t.Errorf("evaluatedAt = %q, want the run's data timestamp", ev)
		}
		alerts, _ := got.body["alerts"].([]any)
		if len(alerts) != 2 {
			t.Fatalf("alerts len = %d, want 2", len(alerts))
		}
		first, _ := alerts[0].(map[string]any)
		if id, _ := first["alertId"].(string); id != "al_1" {
			t.Errorf("alerts[0].alertId = %q, want al_1", id)
		}
		if act, _ := first["action"].(string); act != "created" {
			t.Errorf("alerts[0].action = %q, want created", act)
		}
		if v, _ := first["lastValue"].(float64); v != -18.5 {
			t.Errorf("alerts[0].lastValue = %v, want -18.5", v)
		}
		second, _ := alerts[1].(map[string]any)
		if act, _ := second["action"].(string); act != "cleared" {
			t.Errorf("alerts[1].action = %q, want cleared", act)
		}
		if trunc, _ := got.body["alertsTruncated"].(bool); trunc {
			t.Error("alertsTruncated = true, want false (2 < cap)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no delivery arrived within 2s")
	}
}

// TestPayloadCap pins the 50-line cap: a sweep with more mutations
// delivers exactly maxPayloadAlerts lines and sets alertsTruncated.
func TestPayloadCap(t *testing.T) {
	srv, ch := newReceiver()
	defer srv.Close()

	res := detect.RunResult{AlertsCreated: 120}
	for i := 0; i < 120; i++ {
		res.Alerts = append(res.Alerts, detect.AlertSummary{
			AlertID: "al", Severity: "info", Action: "created", State: "firing",
		})
	}
	wh := New(srv.URL, time.Second, slog.New(slog.DiscardHandler))
	if err := wh.NotifyDetectionRun(context.Background(), res); err != nil {
		t.Fatalf("NotifyDetectionRun() error = %v", err)
	}

	select {
	case got := <-ch:
		alerts, _ := got.body["alerts"].([]any)
		if len(alerts) != maxPayloadAlerts {
			t.Errorf("alerts len = %d, want %d", len(alerts), maxPayloadAlerts)
		}
		if trunc, _ := got.body["alertsTruncated"].(bool); !trunc {
			t.Error("alertsTruncated = false, want true (120 > cap)")
		}
		if created, _ := got.body["alertsCreated"].(float64); created != 120 {
			t.Errorf("alertsCreated = %v, want 120 (the counter is never capped)", created)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no delivery arrived within 2s")
	}
}

// TestReceiverErrorIsReturnedNotFatal pins the side-path semantics: a
// non-2xx receiver (and an unreachable URL) yield an error, but the
// call itself completes normally — the detection run is never affected.
func TestReceiverErrorIsReturnedNotFatal(t *testing.T) {
	wh := New("http://127.0.0.1:1/unreachable", 500*time.Millisecond, slog.New(slog.DiscardHandler))
	if err := wh.NotifyDetectionRun(context.Background(), sampleResult()); err == nil {
		t.Fatal("unreachable receiver: error = nil, want the delivery error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	wh = New(srv.URL, time.Second, slog.New(slog.DiscardHandler))
	if err := wh.NotifyDetectionRun(context.Background(), sampleResult()); err == nil {
		t.Fatal("503 receiver: error = nil, want the status error")
	}
}

// TestDeliveryTimeout pins the client timeout: a receiver that sleeps
// past the configured bound aborts with an error instead of hanging the
// caller (the CLI exit / the API goroutine).
func TestDeliveryTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh := New(srv.URL, 300*time.Millisecond, slog.New(slog.DiscardHandler))
	start := time.Now()
	err := wh.NotifyDetectionRun(context.Background(), sampleResult())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("sleeping receiver: error = nil, want timeout error")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("delivery took %v, want the ~300ms timeout bound", elapsed)
	}
}
