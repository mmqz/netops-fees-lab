package api_test

// webhook_verdict_test.go — pins the v0.12-R18 delivery-verdict read
// surface through the REAL HTTP channel: after POST /api/detection/run
// (which fires the notification asynchronously), the receipt rows of
// GET /api/detection/runs carry the audit verdict — delivered with the
// attempt count for a healthy receiver, failed with the HTTP status for
// a dead one, and "" (channel off) when no webhook is configured. The
// async gap is bridged by polling the listing until the verdict lands
// (bounded — the same patience a NOC dashboard has).

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/app"
	"github.com/mmqz/netops-lab/internal/notify"
)

// newFlakyWebhookReceiver answers 503 for the first failN requests then
// 200 — the transient receiver the retry sequence must absorb.
func newFlakyWebhookReceiver(t *testing.T, failN int32) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) <= failN {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// setAPIWebhook installs the notify channel on an already-built server
// (before the first request, so no handler observes the assignment in
// flight — the detection_webhook_test precedent). A nil app with an
// empty URL is the no-op call for readability at channel-off call
// sites.
func setAPIWebhook(t *testing.T, a *app.App, url string) {
	t.Helper()
	if a == nil || url == "" {
		return
	}
	a.API.Webhook = notify.New(url, 0, a.DB, a.Log)
}

// verdictOf polls GET /api/detection/runs until the given run's verdict
// is non-empty (or the deadline passes — the async delivery lands well
// inside a second; the poll loop exists precisely because the response
// is computed BEFORE the notification goroutine finishes).
func verdictOf(t *testing.T, srv *httptest.Server, runID string) (string, int, int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, body := doRequest(t, srv, http.MethodGet, "/api/detection/runs?page=1&pageSize=10", nil, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("runs list: %d\n%s", resp.StatusCode, body)
		}
		var listed struct {
			Items []struct {
				RunID           string `json:"runId"`
				WebhookStatus   string `json:"webhookStatus"`
				WebhookAttempts int    `json:"webhookAttempts"`
				WebhookError    string `json:"webhookError"`
			} `json:"items"`
		}
		decodeJSON(t, "runs list", body, &listed)
		for _, it := range listed.Items {
			if it.RunID == runID {
				if it.WebhookStatus != "" {
					return it.WebhookStatus, it.WebhookAttempts, len(it.WebhookError)
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "", 0, 0
}

// TestDetectionRunsCarryWebhookVerdict pins the three-valued read over
// two servers: a flaky receiver (absorbed by the retry — delivered,
// attempts 3) and the channel-off baseline ("" — no audit row, no
// branch in the listing shape).
func TestDetectionRunsCarryWebhookVerdict(t *testing.T) {
	// The flaky receiver: two 503s then 200.
	recv, hits := newFlakyWebhookReceiver(t, 2)
	srv, a := newDetectTestServerWithApp(t)
	setAPIWebhook(t, a, recv.URL)

	run := postRun(t, srv, `{}`)
	status, attempts, _ := verdictOf(t, srv, run.RunID)
	if status != "delivered" {
		t.Fatalf("flaky receiver verdict = %q, want delivered (the retry absorbed the transients)", status)
	}
	if attempts != 3 {
		t.Errorf("delivered attempts = %d, want 3 (initial + 2 retries)", attempts)
	}
	if got := atomic.LoadInt32(hits); got != 3 {
		t.Errorf("receiver hits = %d, want 3", got)
	}

	// Channel off: a fresh server without a webhook — the receipt's
	// verdict stays "" (the honest "no channel" read, never a fake
	// failed).
	srv2 := newDetectTestServer(t)
	setAPIWebhook(t, nil, "")
	run2 := postRun(t, srv2, `{}`)
	resp, body := doRequest(t, srv2, http.MethodGet, "/api/detection/runs?page=1&pageSize=5", nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("runs list: %d\n%s", resp.StatusCode, body)
	}
	var listed struct {
		Items []struct {
			RunID           string `json:"runId"`
			WebhookStatus   string `json:"webhookStatus"`
			WebhookAttempts int    `json:"webhookAttempts"`
		} `json:"items"`
	}
	decodeJSON(t, "runs list", body, &listed)
	found := false
	for _, it := range listed.Items {
		if it.RunID == run2.RunID {
			found = true
			if it.WebhookStatus != "" {
				t.Errorf("channel-off verdict = %q, want \"\"", it.WebhookStatus)
			}
			if it.WebhookAttempts != 0 {
				t.Errorf("channel-off attempts = %d, want 0", it.WebhookAttempts)
			}
		}
	}
	if !found {
		t.Fatalf("run %s not found in the listing", run2.RunID)
	}
}

// TestDetectionRunsCarryFailedVerdict pins the dead-receiver path: a
// receiver answering 503 forever → the audit row says failed, the
// error text is carried (the operator sees WHY it never reached the
// receiver), and the run itself is unaffected (the receipt has no
// error).
func TestDetectionRunsCarryFailedVerdict(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(dead.Close)

	srv, a := newDetectTestServerWithApp(t)
	setAPIWebhook(t, a, dead.URL)

	run := postRun(t, srv, `{}`)
	status, attempts, errLen := verdictOf(t, srv, run.RunID)
	if status != "failed" {
		t.Fatalf("dead receiver verdict = %q, want failed", status)
	}
	if attempts < 1 {
		t.Errorf("failed attempts = %d, want >= 1", attempts)
	}
	if errLen == 0 {
		t.Error("failed verdict carries no error text, want the failure reason")
	}
}
