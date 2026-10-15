package detect

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// fixedClock returns a now function advancing by step on every call from
// base — deterministic started_at/finished_at/duration for the recorder
// tests (no wall clock in the data path).
func fixedClock(base time.Time, step time.Duration) func() time.Time {
	n := 0
	return func() time.Time {
		t := base.Add(time.Duration(n) * step)
		n++
		return t
	}
}

// TestRunAndRecordPersistsReceipt pins the success path: RunAndRecord
// wraps Detect and leaves exactly one detection_runs row carrying the
// run identity (run_ prefix + source), the wall-clock bracket, the window
// echo, the counters and the empty error — and returns the SAME RunResult
// Detect would have returned (the wrapper adds the trail, never changes
// the verdict).
func TestRunAndRecordPersistsReceipt(t *testing.T) {
	db := newDetectDB(t)
	ctx := context.Background()
	seedResource(t, db, "res_rec1", "DEV-REC1|GE-0/0/1")
	sid := seedSeries(t, db, "res_rec1", "optical_power_rx_avg_dbm")
	vals := []float64{-8.0, -8.1, -8.0, -8.2, -14.5, -15.0, -15.4, -15.2, -15.1, -15.3}
	seedPoints(t, db, sid, "2026-03-01T00:00:00Z", vals, domain.PointQualityOk)
	insertRule(t, db, rxRule())
	engine := New()

	clock := fixedClock(detectTS, 250*time.Millisecond)
	res, rec, err := RunAndRecord(ctx, db, engine, Options{}, domain.RunSourceAPI, clock)
	if err != nil {
		t.Fatalf("RunAndRecord: %v", err)
	}
	if res.Findings != 1 || res.AlertsCreated != 1 {
		t.Fatalf("RunAndRecord result = %+v, want the Detect verdict unchanged (1/1)", res)
	}
	if !strings.HasPrefix(rec.RunID, "run_") {
		t.Errorf("receipt RunID = %q, want run_ prefix", rec.RunID)
	}
	if rec.Source != domain.RunSourceAPI {
		t.Errorf("receipt Source = %q, want api", rec.Source)
	}
	if rec.Error != "" {
		t.Errorf("receipt Error = %q, want empty on success", rec.Error)
	}
	// The fixed clock: start = base, finish = base + 250ms.
	if rec.StartedAt != detectTS.UTC().Format(time.RFC3339Nano) {
		t.Errorf("receipt StartedAt = %q, want the clock's first tick", rec.StartedAt)
	}
	if rec.DurationMS != 250 {
		t.Errorf("receipt DurationMS = %d, want 250 (the clock step)", rec.DurationMS)
	}
	if rec.EvaluatedAt != res.EvaluatedAt || rec.EvaluatedAt != dayTS(9) {
		t.Errorf("receipt EvaluatedAt = %q, want the data ts %q", rec.EvaluatedAt, dayTS(9))
	}
	if rec.Findings != res.Findings || rec.AlertsCreated != res.AlertsCreated || rec.AlertsCleared != res.AlertsCleared || rec.RunStatesWritten != res.RunStatesWritten {
		t.Errorf("receipt counters = %+v, want the RunResult echo", rec)
	}

	// The trail: exactly one row, readable through the listing.
	runs, total, err := db.DetectionRuns.ListRuns(ctx, 0, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if total != 1 || len(runs) != 1 {
		t.Fatalf("run history = %d rows (total %d), want 1", len(runs), total)
	}
	if runs[0].RunID != rec.RunID || runs[0].Source != domain.RunSourceAPI {
		t.Errorf("listed run = %+v, want the receipt just written", runs[0])
	}

	// A second call (the CLI twin) adds a second row — one row per
	// sweep, sources mix in the same history.
	if _, rec2, err := RunAndRecord(ctx, db, engine, Options{}, domain.RunSourceCLI, fixedClock(detectTS.Add(time.Hour), 10*time.Millisecond)); err != nil {
		t.Fatalf("RunAndRecord cli: %v", err)
	} else if rec2.Source != domain.RunSourceCLI {
		t.Errorf("second receipt Source = %q, want cli", rec2.Source)
	}
	_, total, _ = db.DetectionRuns.ListRuns(ctx, 0, 0)
	if total != 2 {
		t.Errorf("run history after cli run = %d, want 2", total)
	}
}

// TestRunAndRecordFailureReceipt pins the failure path: a cancelled
// context fails the sweep, and the receipt STILL lands — with the error
// text, zero counters, no evaluatedAt — because a failed round is itself
// an auditable operational fact. The caller sees the original error.
func TestRunAndRecordFailureReceipt(t *testing.T) {
	db := newDetectDB(t)
	seedResource(t, db, "res_rec2", "DEV-REC2|GE-0/0/1")
	insertRule(t, db, rxRule())
	engine := New()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the sweep cannot even start
	_, rec, err := RunAndRecord(ctx, db, engine, Options{}, domain.RunSourceScheduler, fixedClock(detectTS, time.Second))
	if err == nil {
		t.Fatal("RunAndRecord with cancelled ctx = nil error, want the Detect failure")
	}
	if rec.Error == "" {
		t.Errorf("failure receipt Error = %q, want the Detect error text", rec.Error)
	}
	if rec.Source != domain.RunSourceScheduler {
		t.Errorf("failure receipt Source = %q, want scheduler", rec.Source)
	}
	if rec.EvaluatedAt != "" || rec.Findings != 0 || rec.AlertsCreated != 0 {
		t.Errorf("failure receipt = %+v, want zero counters / no evaluatedAt", rec)
	}
	runs, total, err := db.DetectionRuns.ListRuns(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if total != 1 || len(runs) != 1 || runs[0].Error == "" {
		t.Fatalf("failure receipt row = %+v (total %d), want the persisted error text", runs, total)
	}
}

// TestRunAndRecordRejectsUnknownSource pins the domain gate: an
// out-of-enum source (the MCP face can never be one) fails BEFORE any
// sweep runs — no receipt, no engine call, no error row.
func TestRunAndRecordRejectsUnknownSource(t *testing.T) {
	db := newDetectDB(t)
	engine := New()
	_, rec, err := RunAndRecord(context.Background(), db, engine, Options{}, domain.RunSource("mcp"), fixedClock(detectTS, time.Second))
	if err == nil || !strings.Contains(err.Error(), "invalid source") {
		t.Fatalf("RunAndRecord(mcp) err = %v, want invalid source error", err)
	}
	if rec.RunID != "" {
		t.Errorf("RunAndRecord(mcp) receipt = %+v, want the zero value (nothing recorded)", rec)
	}
	_, total, _ := db.DetectionRuns.ListRuns(context.Background(), 0, 0)
	if total != 0 {
		t.Errorf("run history after rejected source = %d, want 0", total)
	}
}
