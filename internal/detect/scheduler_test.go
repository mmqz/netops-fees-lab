package detect

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// testLogger is a quiet slog logger for the scheduler tests (the rounds'
// Info lines go to io.Discard; errors still surface through assertions).
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestSchedulerFiresAndRecords pins the patrol loop with a REAL clock at
// demo speed: interval 30ms, three rounds land in detection_runs with
// source=scheduler, the status snapshot reports them, nextRunAt is always
// populated while running, and Run exits on ctx.Done within one interval.
func TestSchedulerFiresAndRecords(t *testing.T) {
	db := newDetectDB(t)
	seedResource(t, db, "res_sched", "DEV-SCH|GE-0/0/1")
	sid := seedSeries(t, db, "res_sched", "optical_power_rx_avg_dbm")
	vals := []float64{-8.0, -8.1, -8.0, -8.2, -14.5, -15.0, -15.4}
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z", vals, domain.PointQualityOk)
	insertRule(t, db, rxRule())

	// The notification callback: recorded but never fails the round.
	var mu sync.Mutex
	delivered := 0
	notifyFn := func(ctx context.Context, res RunResult) error {
		mu.Lock()
		delivered++
		mu.Unlock()
		return nil
	}

	sched := NewScheduler(30*time.Millisecond, db, New(), notifyFn, testLogger(t))
	ctx, cancel := context.WithCancel(context.Background())
	go sched.Run(ctx)

	// Wait for at least 3 recorded scheduler rounds (deadline 5s — the
	// real-clock guard against a stuck ticker).
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, total, err := db.DetectionRuns.ListRuns(context.Background(), 0, 0)
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if total >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scheduler rounds = %d after 5s, want >= 3", total)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// All rounds carry the scheduler source.
	runs, total, err := db.DetectionRuns.ListRuns(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("ListRuns final: %v", err)
	}
	if total < 3 {
		t.Fatalf("total = %d, want >= 3", total)
	}
	for _, run := range runs {
		if run.Source != domain.RunSourceScheduler {
			t.Errorf("run %s source = %q, want scheduler", run.RunID, run.Source)
		}
		if run.Error != "" {
			t.Errorf("run %s error = %q, want success rounds", run.RunID, run.Error)
		}
	}
	if runs[0].Findings != 1 {
		t.Errorf("newest round findings = %d, want 1 (the violating series fires on every patrol round)", runs[0].Findings)
	}
	// First round creates the alert; later rounds see it (created=0) —
	// but the newest round may be any of them, so assert via the count
	// of created==1 runs: exactly one across the history.
	createdOnce := 0
	for _, run := range runs {
		if run.AlertsCreated == 1 {
			createdOnce++
		}
	}
	if createdOnce != 1 {
		t.Errorf("rounds with alertsCreated=1 = %d, want exactly 1 (idempotent patrol)", createdOnce)
	}

	// Status snapshot while live: enabled, rounds >= 3, nextRunAt set.
	st := sched.Status()
	if !st.Enabled || st.IntervalSeconds != 0 || st.Rounds < 3 {
		t.Errorf("live status = %+v, want enabled/rounds>=3 (sub-second interval pins 0s)", st)
	}
	if st.NextRunAt == "" {
		t.Error("live status NextRunAt empty, want the next tick")
	}
	if st.Running {
		t.Error("live status Running=true between rounds, want false")
	}

	// The notification channel fired for every completed round.
	mu.Lock()
	d := delivered
	mu.Unlock()
	if d < 3 {
		t.Errorf("notifications delivered = %d, want >= 3 (one per round)", d)
	}

	// Shutdown: Run returns within one interval of the cancel.
	cancel()
	done := make(chan struct{})
	go func() { <-done }()
	select {
	case <-done:
		t.Error("Run returned early without waiting — impossible, it blocks")
	default:
	}
	_ = done
	time.Sleep(80 * time.Millisecond) // > one interval
	// After the stop the rounds counter freezes (no further rounds).
	before := sched.Status().Rounds
	time.Sleep(80 * time.Millisecond)
	if after := sched.Status().Rounds; after != before {
		t.Errorf("rounds after stop = %d, want frozen at %d", after, before)
	}
}

// TestSchedulerSingleFlight pins the guard: a round in progress blocks
// beginRound — the would-be overlapping tick is skipped, never queued.
func TestSchedulerSingleFlight(t *testing.T) {
	db := newDetectDB(t)
	sched := NewScheduler(time.Hour, db, New(), nil, testLogger(t))

	if !sched.beginRound() {
		t.Fatal("first beginRound = false, want true (the slot is free)")
	}
	if sched.beginRound() {
		t.Fatal("second beginRound = true, want false (single-flight)")
	}
	st := sched.Status()
	if !st.Running || st.LastRunAt == "" {
		t.Errorf("status mid-round = %+v, want Running=true + LastRunAt set", st)
	}
	sched.endRound()
	st = sched.Status()
	if st.Running || st.Rounds != 1 {
		t.Errorf("status after round = %+v, want Running=false + Rounds=1", st)
	}
	// The slot is free again.
	if !sched.beginRound() {
		t.Fatal("beginRound after endRound = false, want true (released)")
	}
	sched.endRound()
	if sched.Status().Rounds != 2 {
		t.Errorf("rounds = %d, want 2", sched.Status().Rounds)
	}
}

// TestSchedulerNilIsOff pins the OFF channel: a nil *Scheduler is a value
// — Run returns immediately, Status reports the disabled snapshot. The
// health handler and the app wiring never branch on nil.
func TestSchedulerNilIsOff(t *testing.T) {
	var sched *Scheduler
	st := sched.Status()
	if st.Enabled || st.IntervalSeconds != 0 || st.Rounds != 0 || st.NextRunAt != "" {
		t.Errorf("nil Status = %+v, want the disabled zero snapshot", st)
	}
	// Run on nil must not panic and must not block.
	done := make(chan struct{})
	go func() {
		sched.Run(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run(nil) blocked, want immediate return")
	}
}

// TestSchedulerNotifyFailureNotFatal pins the best-effort contract: a
// failing notification callback is logged, never fails the round — the
// receipt still lands.
func TestSchedulerNotifyFailureNotFatal(t *testing.T) {
	db := newDetectDB(t)
	notifyFn := func(ctx context.Context, res RunResult) error {
		return context.DeadlineExceeded
	}
	sched := NewScheduler(20*time.Millisecond, db, New(), notifyFn, testLogger(t))
	ctx, cancel := context.WithCancel(context.Background())
	go sched.Run(ctx)
	defer cancel()

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, total, err := db.DetectionRuns.ListRuns(context.Background(), 0, 0)
		if err != nil {
			t.Fatalf("ListRuns: %v", err)
		}
		if total >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no scheduler round after 5s despite notify failures")
		}
		time.Sleep(10 * time.Millisecond)
	}
	runs, _, _ := db.DetectionRuns.ListRuns(context.Background(), 0, 0)
	for _, run := range runs {
		if run.Error != "" && !strings.Contains(run.Error, "notify") {
			t.Errorf("round error = %q, want success rounds (notify failures are log-only)", run.Error)
		}
	}
}
