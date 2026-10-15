package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/notify"
)

// This file pins the v0.10-R8 notification side path of POST
// /api/detection/run: the run summary is delivered to the configured
// webhook asynchronously (the 200 does not wait for the delivery), with
// the documented payload contract, and a delivery failure never fails
// the run itself. The channel-off default (nil webhook) is pinned by
// every other detection test already (they run without any receiver).

// webhookDelivery is one captured POST body.
type webhookDelivery struct {
	ctype string
	body  map[string]any
}

// newWebhookReceiver returns an in-memory receiver capturing deliveries.
func newWebhookReceiver(t *testing.T) (*httptest.Server, chan webhookDelivery) {
	t.Helper()
	ch := make(chan webhookDelivery, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		ch <- webhookDelivery{ctype: r.Header.Get("Content-Type"), body: body}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, ch
}

// TestDetectionRunFiresWebhook pins the HTTP trigger channel: one run →
// exactly one delivery, POST + application/json, the run envelope's
// counters, and the per-alert mutation lines of the degrade scenario.
func TestDetectionRunFiresWebhook(t *testing.T) {
	srv, a := newDetectTestServerWithApp(t)

	recv, ch := newWebhookReceiver(t)
	// The server is already built; the field is set before the first
	// request, so no handler observes the assignment in flight.
	a.API.Webhook = notify.New(recv.URL, 0, a.Log)

	resp, body := doRequest(t, srv, http.MethodPost, "/api/detection/rules", []byte(`{"metricKey":"optical_power_rx_avg_dbm","comparator":"lte","threshold":-8.1,"window":1,"severity":"warning","label":"两天劣化触发"}`), "application/json")
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("tripwire rule create: %d\n%s", resp.StatusCode, body)
	}

	resp, body = doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detection run: %d\n%s", resp.StatusCode, body)
	}
	var run struct {
		Findings      int `json:"findings"`
		AlertsCreated int `json:"alertsCreated"`
		AlertsCleared int `json:"alertsCleared"`
	}
	decodeJSON(t, "run", body, &run)
	if run.AlertsCreated == 0 {
		t.Fatal("degrade scenario produced no alerts — seeded data changed?")
	}

	select {
	case got := <-ch:
		if got.ctype != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got.ctype)
		}
		if ev, _ := got.body["event"].(string); ev != "detection.run" {
			t.Errorf("event = %q, want detection.run", ev)
		}
		if v, _ := got.body["findings"].(float64); int(v) != run.Findings {
			t.Errorf("findings = %v, want %d (the payload mirrors the run envelope)", v, run.Findings)
		}
		if v, _ := got.body["alertsCreated"].(float64); int(v) != run.AlertsCreated {
			t.Errorf("alertsCreated = %v, want %d", v, run.AlertsCreated)
		}
		if v, _ := got.body["alertsCleared"].(float64); int(v) != run.AlertsCleared {
			t.Errorf("alertsCleared = %v, want %d", v, run.AlertsCleared)
		}
		alerts, _ := got.body["alerts"].([]any)
		if len(alerts) != run.AlertsCreated {
			t.Errorf("payload alerts len = %d, want %d mutation lines", len(alerts), run.AlertsCreated)
		}
		if len(alerts) > 0 {
			first, _ := alerts[0].(map[string]any)
			for _, key := range []string{"alertId", "metricKey", "resourceId", "severity", "action", "state", "lastValue"} {
				if _, ok := first[key]; !ok {
					t.Errorf("payload alerts[0] missing %q", key)
				}
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook delivery within 5s of the run")
	}
}

// TestDetectionRunWebhookFailureIsNotFatal pins the side-path semantics:
// a receiver that answers 503 (or is unreachable) never turns the run's
// 200 into an error — the delivery failure is the notifier's business.
func TestDetectionRunWebhookFailureIsNotFatal(t *testing.T) {
	srv, a := newDetectTestServerWithApp(t)

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer dead.Close()
	a.API.Webhook = notify.New(dead.URL, 0, a.Log)

	resp, body := doRequest(t, srv, http.MethodPost, "/api/detection/rules", []byte(`{"metricKey":"optical_power_rx_avg_dbm","comparator":"lte","threshold":-8.1,"window":1,"severity":"warning","label":"两天劣化触发"}`), "application/json")
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("tripwire rule create: %d\n%s", resp.StatusCode, body)
	}

	resp, body = doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("run with failing webhook: status = %d (delivery failures must not fail the run)\n%s", resp.StatusCode, body)
	}
	var run struct {
		AlertsCreated int `json:"alertsCreated"`
	}
	decodeJSON(t, "run", body, &run)
	if run.AlertsCreated == 0 {
		t.Fatal("run did not create alerts — seeded data changed?")
	}
}
