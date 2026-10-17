package api_test

// alert_lifecycle_notify_test.go — pins the v0.13-R19 lifecycle
// notification side path through the REAL HTTP channel: a committed
// ack/clear fires a single-event webhook delivery asynchronously (the
// response never waits for it), the payload carries the operation
// timestamp and the alert's full context, a dead receiver never fails
// the ack/clear itself, the channel-off default writes no audit row,
// and GET /api/alerts projects the honest three-valued
// lifecycleDelivery verdict.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// tripAlerts seeds the degrade scenario + a window-1 tripwire rule
// (threshold -8.1), runs one detection sweep and returns one FIRING
// alert id (the TestDetectionRunAndAlerts recipe).
func tripAlerts(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	return tripAlertsAt(t, srv, -8.1)
}

// tripAlertsAt is tripAlerts with an explicit tripwire threshold — the
// determinism test uses a high threshold so the sweep fires MANY ports.
func tripAlertsAt(t *testing.T, srv *httptest.Server, threshold float64) string {
	t.Helper()
	sensitive := []byte(fmt.Sprintf(`{"metricKey":"optical_power_rx_avg_dbm","comparator":"lte","threshold":%g,"window":1,"severity":"warning","label":"两天劣化触发"}`, threshold))
	resp, body := doRequest(t, srv, http.MethodPost, "/api/detection/rules", sensitive, "application/json")
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("tripwire rule create: %d\n%s", resp.StatusCode, body)
	}
	resp, body = doRequest(t, srv, http.MethodPost, "/api/detection/run", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detection run: %d\n%s", resp.StatusCode, body)
	}
	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts?state=firing&pageSize=5", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alerts list: %d\n%s", resp.StatusCode, body)
	}
	var alerts struct {
		Items []struct {
			AlertID string `json:"alertId"`
		} `json:"items"`
	}
	decodeJSON(t, "alerts list", body, &alerts)
	if len(alerts.Items) == 0 {
		t.Fatal("no firing alerts after the degrade detection")
	}
	return alerts.Items[0].AlertID
}

// lifecycleOf polls GET /api/alerts until the given alert's
// lifecycleDelivery verdict is non-empty (or the deadline passes — the
// delivery is async; the poll loop is the same patience a dashboard
// has). wantEvent narrows the wait when an EARLIER verdict already
// landed (ack then clear: the clear row lands after the receiver
// answered, so the poll must not exit on the ack verdict — pass "ack"
// or "clear"; "" accepts any non-empty verdict).
func lifecycleOf(t *testing.T, srv *httptest.Server, alertID, wantEvent string) (status, event string, attempts int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, body := doRequest(t, srv, http.MethodGet, "/api/alerts?pageSize=100", nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("alerts list: %d\n%s", resp.StatusCode, body)
		}
		var alerts struct {
			Items []struct {
				AlertID string `json:"alertId"`
				Life    struct {
					Status   string `json:"status"`
					Event    string `json:"event"`
					Attempts int    `json:"attempts"`
					TS       string `json:"ts"`
				} `json:"lifecycleDelivery"`
			} `json:"items"`
		}
		decodeJSON(t, "alerts list", body, &alerts)
		for _, it := range alerts.Items {
			if it.AlertID == alertID && it.Life.Status != "" {
				if wantEvent == "" || it.Life.Event == wantEvent {
					return it.Life.Status, it.Life.Event, it.Life.Attempts
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", "", 0
}

// drainRunDelivery consumes the detection-run summary notification the
// tripAlerts sweep fires asynchronously (the same receiver serves both
// event kinds — the run summary arrives first and is NOT what the
// lifecycle assertions read).
func drainRunDelivery(t *testing.T, ch chan webhookDelivery) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case got := <-ch:
			if ev, _ := got.body["event"].(string); ev == "detection.run" {
				return
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("no detection.run delivery drained within 5s")
}

// TestAlertAckFiresLifecycleNotification pins the ack trigger end to
// end: ack 200 → exactly one webhook POST with the alert.ack payload
// (context fields, the OPERATION ts from the request, the post-edge
// state), the audit verdict delivered, and the listing's
// lifecycleDelivery carrying status/event/attempts.
func TestAlertAckFiresLifecycleNotification(t *testing.T) {
	recv, ch := newWebhookReceiver(t)
	srv, a := newDetectTestServerWithApp(t)
	setAPIWebhook(t, a, recv.URL)

	id := tripAlerts(t, srv)
	drainRunDelivery(t, ch)

	// The explicit operation ts (deterministic payload input — the
	// notification mirrors the decision's stamp, never the delivery
	// clock).
	const ackTS = "2026-10-18T09:30:00Z"
	resp, body := doRequest(t, srv, http.MethodPost, "/api/alerts/"+id+"/ack",
		[]byte(`{"ackedBy":"运维-张三","ts":"`+ackTS+`"}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ack: %d\n%s", resp.StatusCode, body)
	}
	var ackResp struct {
		State   string `json:"state"`
		AckedAt string `json:"ackedAt"`
	}
	decodeJSON(t, "ack", body, &ackResp)
	if ackResp.State != "acked" || ackResp.AckedAt != ackTS {
		t.Fatalf("ack response = %+v, want acked @ explicit ts", ackResp)
	}

	select {
	case got := <-ch:
		if ev, _ := got.body["event"].(string); ev != "alert.ack" {
			t.Errorf("event = %q, want alert.ack", ev)
		}
		if aid, _ := got.body["alertId"].(string); aid != id {
			t.Errorf("alertId = %q, want %q", aid, id)
		}
		if st, _ := got.body["state"].(string); st != "acked" {
			t.Errorf("state = %q, want acked (the state AFTER the edge)", st)
		}
		if by, _ := got.body["ackedBy"].(string); by != "运维-张三" {
			t.Errorf("ackedBy = %q, want the acker identity", by)
		}
		if ts, _ := got.body["ts"].(string); ts != ackTS {
			t.Errorf("ts = %q, want the operation timestamp %q (never the delivery clock)", ts, ackTS)
		}
		// The alert's context (rule + resource) rides along — the duty
		// channel reads the label, not the bare id.
		if mk, _ := got.body["metricKey"].(string); mk != "optical_power_rx_avg_dbm" {
			t.Errorf("metricKey = %q, want the rule's metric", mk)
		}
		if _, ok := got.body["ruleLabel"].(string); !ok {
			t.Error("ruleLabel missing from the payload")
		}
		res, _ := got.body["resourceId"].(string)
		if res == "" {
			t.Error("resourceId missing or empty — the payload must carry the resource context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no lifecycle delivery within 5s of the ack")
	}

	status, event, attempts := lifecycleOf(t, srv, id, "ack")
	if status != "delivered" || event != "ack" {
		t.Fatalf("lifecycleDelivery = %q/%q, want delivered/ack", status, event)
	}
	if attempts != 1 {
		t.Errorf("lifecycleDelivery attempts = %d, want 1 (healthy receiver, one POST)", attempts)
	}

	// The repo-level audit row agrees with the listing (same verdict
	// row, keyed by alert + event).
	d, err := a.DB.AlertEventDeliveries.LatestByAlert(t.Context(), id)
	if err != nil {
		t.Fatalf("LatestByAlert: %v", err)
	}
	if d.Status != domain.WebhookDelivered || d.Event != domain.AlertLifecycleAck {
		t.Errorf("audit row = %s/%s, want delivered/ack", d.Status, d.Event)
	}
	if d.URL == "" || d.URL != recv.URL {
		t.Errorf("audit url = %q, want the receiver URL", d.URL)
	}
}

// TestAlertClearFiresLifecycleNotification pins the clear trigger and
// the LATEST-wins read: an acked alert cleared → the alert.clear
// payload (ackedBy "", state cleared, the clear ts), and the listing's
// lifecycleDelivery flips to the clear verdict (the newest human
// decision the channel learned).
func TestAlertClearFiresLifecycleNotification(t *testing.T) {
	recv, ch := newWebhookReceiver(t)
	srv, a := newDetectTestServerWithApp(t)
	setAPIWebhook(t, a, recv.URL)

	id := tripAlerts(t, srv)
	drainRunDelivery(t, ch)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/alerts/"+id+"/ack", []byte(`{"ackedBy":"运维-李四"}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ack: %d\n%s", resp.StatusCode, body)
	}
	// Drain the ack delivery — the clear is what this test reads.
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no ack delivery within 5s")
	}

	const clearTS = "2026-10-18T11:00:00Z"
	resp, body = doRequest(t, srv, http.MethodPost, "/api/alerts/"+id+"/clear",
		[]byte(`{"ts":"`+clearTS+`"}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear: %d\n%s", resp.StatusCode, body)
	}
	var clearResp struct {
		State     string `json:"state"`
		ClearedAt string `json:"clearedAt"`
	}
	decodeJSON(t, "clear", body, &clearResp)
	if clearResp.State != "cleared" || clearResp.ClearedAt != clearTS {
		t.Fatalf("clear response = %+v, want cleared @ explicit ts", clearResp)
	}

	select {
	case got := <-ch:
		if ev, _ := got.body["event"].(string); ev != "alert.clear" {
			t.Errorf("event = %q, want alert.clear", ev)
		}
		if st, _ := got.body["state"].(string); st != "cleared" {
			t.Errorf("state = %q, want cleared", st)
		}
		if by, _ := got.body["ackedBy"].(string); by != "" {
			t.Errorf("ackedBy = %q, want \"\" on clear events", by)
		}
		if ts, _ := got.body["ts"].(string); ts != clearTS {
			t.Errorf("ts = %q, want the clear operation timestamp", ts)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no clear delivery within 5s")
	}

	status, event, attempts := lifecycleOf(t, srv, id, "clear")
	if status != "delivered" || event != "clear" {
		t.Fatalf("lifecycleDelivery = %q/%q, want delivered/clear (the LATEST verdict wins)", status, event)
	}
	if attempts != 1 {
		t.Errorf("lifecycleDelivery attempts = %d, want 1", attempts)
	}

	// The repo-level verdict agrees with the listing.
	d, err := a.DB.AlertEventDeliveries.LatestByAlert(t.Context(), id)
	if err != nil {
		t.Fatalf("LatestByAlert: %v", err)
	}
	if d.Event != domain.AlertLifecycleClear {
		t.Errorf("LatestByAlert event = %q, want clear", d.Event)
	}
}

// TestAlertLifecycleNotifyFailureIsNotFatal pins the side-path doctrine
// on the human edges: a receiver that answers 503 forever never turns
// the ack's 200 into an error, the alert IS acked, and the audit row
// honestly says failed — the listing answers "the ack never reached
// the duty channel".
func TestAlertLifecycleNotifyFailureIsNotFatal(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(dead.Close)

	srv, a := newDetectTestServerWithApp(t)
	setAPIWebhook(t, a, dead.URL)

	id := tripAlerts(t, srv)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/alerts/"+id+"/ack", []byte(`{"ackedBy":"运维-王五"}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ack with dead receiver: status = %d (delivery failures must not fail the ack)\n%s", resp.StatusCode, body)
	}

	// The ack itself committed (state machine unaffected).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts?state=acked&pageSize=5", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("acked list: %d\n%s", resp.StatusCode, body)
	}
	var alerts struct {
		Items []struct {
			AlertID string `json:"alertId"`
			State   string `json:"state"`
		} `json:"items"`
	}
	decodeJSON(t, "acked list", body, &alerts)
	foundAcked := false
	for _, it := range alerts.Items {
		if it.AlertID == id {
			foundAcked = it.State == "acked"
		}
	}
	if !foundAcked {
		t.Fatal("the alert is not acked — the notification failure leaked into the operation")
	}

	status, event, attempts := lifecycleOf(t, srv, id, "ack")
	if status != "failed" || event != "ack" {
		t.Fatalf("lifecycleDelivery = %q/%q, want failed/ack", status, event)
	}
	if attempts < 1 {
		t.Errorf("failed attempts = %d, want >= 1", attempts)
	}
}

// TestAlertLifecycleChannelOff pins the honest zero read: without a
// webhook configured, the ack is a plain state change — no goroutine,
// no audit row, and the listing's lifecycleDelivery stays the
// always-present zero block (status "", event "", attempts 0).
func TestAlertLifecycleChannelOff(t *testing.T) {
	srv, a := newDetectTestServerWithApp(t) // no setAPIWebhook: channel off

	id := tripAlerts(t, srv)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/alerts/"+id+"/ack", []byte(`{"ackedBy":"运维-赵六"}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ack (channel off): %d\n%s", resp.StatusCode, body)
	}

	// No async work exists — a short settle is enough for honesty, then
	// the zero verdict is FINAL (nothing will ever land).
	time.Sleep(200 * time.Millisecond)
	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts?state=acked&pageSize=5", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("acked list: %d\n%s", resp.StatusCode, body)
	}
	var alerts struct {
		Items []struct {
			AlertID string `json:"alertId"`
			Life    struct {
				Status   string `json:"status"`
				Event    string `json:"event"`
				Attempts int    `json:"attempts"`
				TS       string `json:"ts"`
			} `json:"lifecycleDelivery"`
		} `json:"items"`
	}
	decodeJSON(t, "acked list", body, &alerts)
	for _, it := range alerts.Items {
		if it.AlertID == id {
			if it.Life.Status != "" || it.Life.Event != "" || it.Life.Attempts != 0 || it.Life.TS != "" {
				t.Errorf("channel-off lifecycleDelivery = %+v, want the zero block", it.Life)
			}
		}
	}
	// The channel-off gate is not just an empty read — NO audit row was
	// written either (the channel door: no delivery attempt, no verdict
	// row — the "" read stays honest forever).
	if _, err := a.DB.AlertEventDeliveries.LatestByAlert(t.Context(), id); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("channel-off LatestByAlert = %v, want not-found (no audit row)", err)
	}
}

// TestAlertClearNotifyFailureIsNotFatal is the CLEAR-side twin of
// TestAlertLifecycleNotifyFailureIsNotFatal: a receiver that answers
// 503 forever never turns the clear's 200 into an error, the alert IS
// cleared, and the listing's verdict honestly says failed/clear.
func TestAlertClearNotifyFailureIsNotFatal(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(dead.Close)

	srv, a := newDetectTestServerWithApp(t)
	setAPIWebhook(t, a, dead.URL)

	id := tripAlerts(t, srv)
	resp, body := doRequest(t, srv, http.MethodPost, "/api/alerts/"+id+"/clear", []byte(`{}`), "application/json")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("clear with dead receiver: status = %d (delivery failures must not fail the clear)\n%s", resp.StatusCode, body)
	}

	// The clear itself committed (the engine-state machine is untouched
	// by the notification side path).
	resp, body = doRequest(t, srv, http.MethodGet, "/api/alerts?state=cleared&pageSize=5", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cleared list: %d\n%s", resp.StatusCode, body)
	}
	var alerts struct {
		Items []struct {
			AlertID string `json:"alertId"`
			State   string `json:"state"`
		} `json:"items"`
	}
	decodeJSON(t, "cleared list", body, &alerts)
	foundCleared := false
	for _, it := range alerts.Items {
		if it.AlertID == id {
			foundCleared = it.State == "cleared"
		}
	}
	if !foundCleared {
		t.Fatal("the alert is not cleared — the notification failure leaked into the operation")
	}

	status, event, attempts := lifecycleOf(t, srv, id, "clear")
	if status != "failed" || event != "clear" {
		t.Fatalf("lifecycleDelivery = %q/%q, want failed/clear", status, event)
	}
	if attempts < 1 {
		t.Errorf("failed attempts = %d, want >= 1", attempts)
	}
}

// TestAlertLifecycleDeterministicPayload pins the determinism doctrine
// at the payload level: two alerts acked with the SAME request body
// (same ackedBy + ts) deliver byte-identical payloads except the
// alertId — the notification is a function of the alert row + the
// decision, never of the delivery clock (deliveredAt is the only
// allowed difference, plus nothing else).
func TestAlertLifecycleDeterministicPayload(t *testing.T) {
	recv, ch := newWebhookReceiver(t)
	srv, a := newDetectTestServerWithApp(t)
	setAPIWebhook(t, a, recv.URL)

	first := tripAlertsAt(t, srv, 0)
	drainRunDelivery(t, ch)
	resp, body := doRequest(t, srv, http.MethodGet, "/api/alerts?state=firing&pageSize=10", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alerts list: %d\n%s", resp.StatusCode, body)
	}
	var alerts struct {
		Items []struct {
			AlertID string `json:"alertId"`
		} `json:"items"`
	}
	decodeJSON(t, "alerts list", body, &alerts)
	second := ""
	for _, it := range alerts.Items {
		if it.AlertID != first {
			second = it.AlertID
			break
		}
	}
	if second == "" {
		t.Fatal("degrade scenario produced only one firing alert — need two for the determinism pair")
	}

	ackBody := []byte(`{"ackedBy":"运维-张三","ts":"2026-10-18T09:30:00Z"}`)
	for _, id := range []string{first, second} {
		resp, body = doRequest(t, srv, http.MethodPost, "/api/alerts/"+id+"/ack", ackBody, "application/json")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("ack %s: %d\n%s", id, resp.StatusCode, body)
		}
	}

	var payloads []map[string]any
	for i := 0; i < 2; i++ {
		select {
		case got := <-ch:
			payloads = append(payloads, got.body)
		case <-time.After(5 * time.Second):
			t.Fatalf("delivery %d missing within 5s", i)
		}
	}
	// Same alert context (rule/metric/severity differ only where the
	// ALERTS differ — the degrade scenario alerts share the rule): every
	// field must agree except alertId and deliveredAt.
	p1, p2 := payloads[0], payloads[1]
	delete(p1, "alertId")
	delete(p1, "deliveredAt")
	delete(p2, "alertId")
	delete(p2, "deliveredAt")
	for key, want := range p1 {
		got := p2[key]
		if key == "resourceId" || key == "naturalKey" || key == "resourceType" {
			continue // different ports of the scenario — context keys legitimately differ
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("payload key %q = %v vs %v (same decision + same rule context must give the same payload)", key, got, want)
		}
	}
}
