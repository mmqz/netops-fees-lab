package metrics

// recover_test.go — the metric channel's crash-window heal (#87): the third
// import channel gets the same self-heal discipline the resource channel
// has had since R1-D6 (importer.RecoverInterrupted) and the fee channel has
// via retry-open batch rows. A process death mid-ingest leaves the batch row
// stuck at importing FOREVER otherwise — no other write ever touches it —
// and the channel becomes permanently blocked on that row.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// TestRecoverInterruptedHealsCrashWindow pins the heal's full behavior:
// transient batches flip to failed(interrupted) with finished_at stamped,
// terminal batches are untouched, and a second run is a no-op.
func TestRecoverInterruptedHealsCrashWindow(t *testing.T) {
	svc, db := newMetricService(t)
	ctx := context.Background()

	seed := func(id string, status domain.BatchStatus, age time.Time) {
		t.Helper()
		b := domain.MetricBatch{
			ID: id, WorkbookName: id + ".xlsx", WorkbookSHA256: "sha_" + id,
			Status: status, Mapping: "{}", StartedAt: age,
		}
		if err := db.MetricBatches.Create(ctx, b); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	now := time.Now().UTC()
	seed("mbat_crashed", domain.BatchStatusImporting, now.Add(-time.Hour)) // crash debris
	seed("mbat_done", domain.BatchStatusCompleted, now.Add(-2*time.Hour))  // terminal: untouchable
	seed("mbat_failed", domain.BatchStatusFailed, now.Add(-3*time.Hour))   // terminal: untouchable

	n, err := svc.RecoverInterrupted(ctx)
	if err != nil {
		t.Fatalf("RecoverInterrupted: %v", err)
	}
	if n != 1 {
		t.Fatalf("healed = %d, want exactly 1 (only the importing debris)", n)
	}

	crashed, err := db.MetricBatches.Get(ctx, "mbat_crashed")
	if err != nil {
		t.Fatalf("get healed batch: %v", err)
	}
	if crashed.Status != domain.BatchStatusFailed {
		t.Fatalf("healed status = %s, want failed", crashed.Status)
	}
	if !strings.HasPrefix(crashed.Error, "interrupted:") {
		t.Fatalf("healed error = %q, want the contractual interrupted: prefix (recognizable as never-reached-terminal, not an ordinary failure)", crashed.Error)
	}
	if crashed.FinishedAt == nil {
		t.Fatal("healed batch must have finished_at stamped once")
	}

	// terminal batches keep their own status AND error text (the seeded
	// completed row has no finished_at — real imports stamp it in their
	// transaction; the heal's job is to NOT touch these rows, which this
	// asserts via status + error staying verbatim).
	done, err := db.MetricBatches.Get(ctx, "mbat_done")
	if err != nil || done.Status != domain.BatchStatusCompleted || done.Error != "" {
		t.Fatalf("completed batch must be untouched, got %+v (err %v)", done, err)
	}
	failed, err := db.MetricBatches.Get(ctx, "mbat_failed")
	if err != nil || failed.Status != domain.BatchStatusFailed {
		t.Fatalf("failed batch must be untouched, got %+v (err %v)", failed, err)
	}

	// idempotent: a second sweep finds nothing transient.
	n2, err := svc.RecoverInterrupted(ctx)
	if err != nil || n2 != 0 {
		t.Fatalf("second heal = %d (err %v), want 0 — the heal is idempotent", n2, err)
	}
}

// TestMarkFailedStampsTerminalError covers the repo's per-batch error
// writer, whose union coverage was 0% before #87 (the service only ever
// wrote completed/rolled_back — the ONLY failed-state writer of the metric
// channel had never been executed by any test).
func TestMarkFailedStampsTerminalError(t *testing.T) {
	_, db := newMetricService(t)
	ctx := context.Background()
	b := domain.MetricBatch{
		ID: "mbat_err", WorkbookName: "err.xlsx", WorkbookSHA256: "sha_err",
		Status: domain.BatchStatusImporting, Mapping: "{}", StartedAt: time.Now().UTC(),
	}
	if err := db.MetricBatches.Create(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := db.MetricBatches.MarkFailed(ctx, "mbat_err", "行错误：第 3 行数值无法解析"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, err := db.MetricBatches.Get(ctx, "mbat_err")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.BatchStatusFailed || got.Error != "行错误：第 3 行数值无法解析" || got.FinishedAt == nil {
		t.Fatalf("MarkFailed result = %+v, want failed + error text + finished_at", got)
	}
	// an unknown id answers ErrNotFound, not silent success.
	if err := db.MetricBatches.MarkFailed(ctx, "mbat_nope", "x"); err == nil {
		t.Fatal("MarkFailed(unknown id) = nil, want an error")
	}
}
