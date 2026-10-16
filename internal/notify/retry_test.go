package notify

// retry_test.go — pins the v0.12-R18 bounded retry + delivery audit:
// a receiver that blips must not cost the notification (the retry
// sequence absorbs transients), a dead receiver must leave an honest
// failed verdict row linked to the run receipt, the sequence is bounded
// by the caller's ctx (a pre-cancelled ctx makes exactly one attempt),
// and the audit URL is REDACTED (the query string — where webhook
// tokens live — never enters the table).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/detect"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/testsupport"
)

// newAuditDB opens a fresh repository for the audit assertions.
func newAuditDB(t *testing.T) *repo.DB {
	t.Helper()
	db, err := repo.Open(filepath.Join(testsupport.TempDir(t), "netops.db"))
	if err != nil {
		t.Fatalf("repo.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// flakyReceiver answers failures for the first n requests, then 200.
func flakyReceiver(t *testing.T, n int32) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) <= n {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// deliveriesOf reads the audit rows through the runs-listing join (the
// read path the API uses).
func deliveriesOf(t *testing.T, db *repo.DB) map[string]repo.DeliveryJoin {
	t.Helper()
	rows, _, err := db.DetectionRuns.ListRuns(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	out := make(map[string]repo.DeliveryJoin, len(rows))
	for _, row := range rows {
		out[row.RunID] = row.Delivery
	}
	return out
}

// seedReceipt stores one minimal run receipt (the audit row's link
// target) and returns its id.
func seedReceipt(t *testing.T, db *repo.DB, id string) {
	t.Helper()
	err := db.DetectionRuns.InsertRun(context.Background(), domain.DetectionRun{
		RunID: id, Source: domain.RunSourceAPI,
		StartedAt: "2026-10-17T08:00:00Z", FinishedAt: "2026-10-17T08:00:01Z",
	})
	if err != nil {
		t.Fatalf("seed receipt %s: %v", id, err)
	}
}

// TestRetryAbsorbsTransientFailures pins the retry contract: a receiver
// failing twice then succeeding is DELIVERED on the third attempt (the
// blip is absorbed), and the audit row records attempts=3 /
// status=delivered / the 2xx http status.
func TestRetryAbsorbsTransientFailures(t *testing.T) {
	db := newAuditDB(t)
	srv, hits := flakyReceiver(t, 2)
	seedReceipt(t, db, "run_retry_ok")

	// Zero backoff: the retry matrix runs instantly (the seam exists for
	// exactly this).
	wh := newWebhook(srv.URL, time.Second, []time.Duration{0, 0}, db, nil)
	if wh == nil {
		t.Fatal("newWebhook = nil, want live notifier")
	}
	if err := wh.NotifyDetectionRun(context.Background(), sampleResult(), "run_retry_ok"); err != nil {
		t.Fatalf("NotifyDetectionRun after transients: %v (want absorbed)", err)
	}
	if got := atomic.LoadInt32(hits); got != 3 {
		t.Errorf("receiver hits = %d, want 3 (initial + 2 retries)", got)
	}
	d := deliveriesOf(t, db)["run_retry_ok"]
	if d.Status != domain.WebhookDelivered || d.Attempts != 3 {
		t.Errorf("audit = %+v, want delivered / attempts 3", d)
	}
	if d.HTTPStatus != http.StatusOK {
		t.Errorf("audit httpStatus = %d, want 200", d.HTTPStatus)
	}
}

// TestRetryExhaustedLeavesFailedVerdict pins the dead-receiver path:
// three 503s → a non-nil error, and the audit row records
// failed / attempts=3 / httpStatus=503 — the run history answers "the
// nightly patrol never reached the receiver" honestly.
func TestRetryExhaustedLeavesFailedVerdict(t *testing.T) {
	db := newAuditDB(t)
	srv, hits := flakyReceiver(t, 1000) // always 503
	seedReceipt(t, db, "run_retry_dead")

	wh := newWebhook(srv.URL, time.Second, []time.Duration{0, 0}, db, nil)
	err := wh.NotifyDetectionRun(context.Background(), sampleResult(), "run_retry_dead")
	if err == nil {
		t.Fatal("NotifyDetectionRun on a dead receiver = nil error, want failure")
	}
	if got := atomic.LoadInt32(hits); got != 3 {
		t.Errorf("receiver hits = %d, want 3 (bounded — no infinite retry)", got)
	}
	d := deliveriesOf(t, db)["run_retry_dead"]
	if d.Status != domain.WebhookFailed || d.Attempts != 3 {
		t.Fatalf("audit = %+v, want failed / attempts 3", d)
	}
	if d.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("audit httpStatus = %d, want 503", d.HTTPStatus)
	}
	if d.Error == "" {
		t.Error("audit error empty, want the failure text")
	}
}

// TestPreCancelledContextMakesOneAttempt pins the ctx bounding: with a
// caller that has already given up, the sequence makes exactly ONE
// attempt (the in-flight POST fails on the cancelled ctx), stops before
// any retry, and STILL writes the audit verdict (the write detaches
// from the cancellation — WithoutCancel, the recorder doctrine).
func TestPreCancelledContextMakesOneAttempt(t *testing.T) {
	db := newAuditDB(t)
	srv, hits := flakyReceiver(t, 1000)
	seedReceipt(t, db, "run_retry_cancelled")

	wh := newWebhook(srv.URL, time.Second, []time.Duration{0, 0}, db, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the budget is gone BEFORE the sequence starts
	_ = wh.NotifyDetectionRun(ctx, sampleResult(), "run_retry_cancelled")

	// Exactly one transport-level attempt (cancelled ctx fails it before
	// the wire, or mid-flight — either way no retries follow).
	if got := atomic.LoadInt32(hits); got > 1 {
		t.Errorf("receiver hits = %d, want <= 1 (no retries after cancellation)", got)
	}
	d := deliveriesOf(t, db)["run_retry_cancelled"]
	if d.Status != domain.WebhookFailed || d.Attempts != 1 {
		t.Errorf("audit = %+v, want failed / attempts 1 (the cancelled sequence records what it managed)", d)
	}
}

// TestAuditURLRedacted pins the credential hygiene: the audit row's URL
// never carries the query string (where webhook tokens live) nor
// userinfo — scheme://host/path is enough to recognize the receiver.
func TestAuditURLRedacted(t *testing.T) {
	db := newAuditDB(t)
	srv, _ := flakyReceiver(t, 0) // always 200
	seedReceipt(t, db, "run_redact")

	// The configured target carries a token in the query.
	target := srv.URL + "/hook?token=sekrit&sig=abc"
	wh := newWebhook(target, time.Second, nil, db, nil)
	if err := wh.NotifyDetectionRun(context.Background(), sampleResult(), "run_redact"); err != nil {
		t.Fatalf("NotifyDetectionRun: %v", err)
	}
	d, err := db.WebhookDeliveries.GetByRun(context.Background(), "run_redact")
	if err != nil {
		t.Fatalf("GetByRun: %v", err)
	}
	if d.Status != domain.WebhookDelivered {
		t.Fatalf("audit = %+v, want delivered", d)
	}
	if want := srv.URL + "/hook"; d.URL != want {
		t.Errorf("audit url = %q, want %q (query stripped)", d.URL, want)
	}
	// The pure redaction helper's edge cases.
	if got := redactURL("https://user:pass@example.com/x?a=1#f"); got != "https://example.com/x" {
		t.Errorf("redactURL(userinfo+query) = %q, want https://example.com/x", got)
	}
	if got := redactURL("://bad"); got != "unparsed" {
		t.Errorf("redactURL(bad) = %q, want unparsed", got)
	}
}

// TestNilDBStillDelivers pins the audit-less seam: db=nil retries and
// delivers normally (the verdict row is simply skipped — no panic, no
// behavioral change to the delivery itself).
func TestNilDBStillDelivers(t *testing.T) {
	srv, hits := flakyReceiver(t, 1) // fails once, then 200
	wh := newWebhook(srv.URL, time.Second, []time.Duration{0, 0}, nil, nil)
	if err := wh.NotifyDetectionRun(context.Background(), sampleResult(), "run_nodb"); err != nil {
		t.Fatalf("NotifyDetectionRun (nil db): %v (want absorbed)", err)
	}
	if got := atomic.LoadInt32(hits); got != 2 {
		t.Errorf("receiver hits = %d, want 2", got)
	}
}

// TestNotifyFuncSignature pins the scheduler callback contract (the
// notify method value IS the NotifyFunc type — runID rides along).
var _ detect.NotifyFunc = (*Webhook)(nil).NotifyDetectionRun
