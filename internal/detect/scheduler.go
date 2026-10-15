package detect

// scheduler.go — the serve-mode interval scheduler (v0.11-R11): the last
// mile of the operations loop. Until v0.10 every detection sweep was an
// EXPLICIT act (HTTP POST / CLI / the R7 dialog) — "触发得了" by design,
// but a real inspection regime runs itself. serve --detect-interval 15m
// turns the same engine into a periodic patrol:
//
//   - every interval, ONE Engine.Detect sweep with the default window,
//     recorded via RunAndRecord (source = scheduler — the CHECK-ed
//     origin column of migration v21);
//   - each completed round fires the webhook (the notify func is
//     injected as a plain callback so this package never imports
//     internal/notify — notify imports detect, the dependency direction
//     is one-way);
//   - single-flight: a round still running when the next tick arrives is
//     SKIPPED with a warn log (never queued, never overlapped — the
//     honest behavior for a sweep that outgrew its interval);
//   - Status() snapshots the schedule for GET /api/health: enabled,
//     interval, running, rounds, lastRunAt, nextRunAt — the browser's
//     "下次自动巡检" line reads exactly this;
//   - Run(ctx) exits on ctx.Done — the serve shutdown path cancels it
//     with the HTTP server, one signal, both stop.
//
// The engine stays the pure function; the scheduler is the only place a
// clock drives detection. nil *Scheduler is the OFF channel: Status() on
// a nil receiver reports the disabled snapshot, Run() on nil returns
// immediately — the call sites never branch.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// NotifyFunc is the outbound notification callback of a completed
// scheduled round: the notify.Webhook method value matches this shape
// (notify imports detect — the callback keeps this package
// dependency-clean). nil = channel off. Errors are logged, never fail
// the round (the v0.10-R8 best-effort contract).
type NotifyFunc func(ctx context.Context, res RunResult) error

// Scheduler runs the detection sweep on a fixed interval.
type Scheduler struct {
	interval time.Duration
	db       *repo.DB
	engine   *Engine
	notify   NotifyFunc
	log      *slog.Logger

	// mu guards the mutable status block below.
	mu        sync.Mutex
	running   bool
	rounds    int
	lastRunAt string // started_at of the most recent round, "" = none yet
	nextRunAt string // the next tick's time, "" = not scheduled yet
}

// NewScheduler wires the patrol. interval > 0 is the caller's job (the
// serve flag validates the minimum).
func NewScheduler(interval time.Duration, db *repo.DB, engine *Engine, notify NotifyFunc, log *slog.Logger) *Scheduler {
	return &Scheduler{interval: interval, db: db, engine: engine, notify: notify, log: log}
}

// Run blocks until ctx is done, firing one recorded sweep per interval
// tick. The FIRST round lands on the first tick (interval after start),
// not immediately: the operator who just started the server can run the
// R7 dialog for the initial sweep, the patrol keeps the cadence after.
func (s *Scheduler) Run(ctx context.Context) {
	if s == nil {
		return
	}
	s.setNext(time.Now().Add(s.interval))
	s.log.Info("detection scheduler enabled",
		"interval", s.interval.String(), "nextRunAt", s.snapshotNext())
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("detection scheduler stopped", "rounds", s.snapshotRounds())
			return
		case tick := <-t.C:
			// nextRunAt advances on EVERY tick (fired or skipped): a
			// skipped round does not freeze the "下次" line in the past.
			s.setNext(tick.Add(s.interval))
			s.fire(ctx)
		}
	}
}

// fire performs one round under the single-flight guard.
func (s *Scheduler) fire(ctx context.Context) {
	if !s.beginRound() {
		s.log.Warn("scheduled detection skipped: previous round still running",
			"interval", s.interval.String())
		return
	}
	res, rec, err := RunAndRecord(ctx, s.db, s.engine, Options{}, domain.RunSourceScheduler, time.Now)
	s.endRound()
	if err != nil {
		// The receipt row already carries the failure (RunAndRecord
		// records failed sweeps too); here it only needs the log.
		s.log.Error("scheduled detection round failed",
			"runId", rec.RunID, "error", err.Error())
		return
	}
	s.log.Info("scheduled detection round complete",
		"runId", rec.RunID,
		"findings", res.Findings,
		"alertsCreated", res.AlertsCreated,
		"alertsCleared", res.AlertsCleared,
		"durationMs", rec.DurationMS)
	if s.notify != nil {
		nctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.notify(nctx, res); err != nil {
			s.log.Warn("scheduled run notification failed", "error", err.Error())
		}
	}
}

// beginRound acquires the single-flight slot; false = a round is active.
func (s *Scheduler) beginRound() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	s.lastRunAt = time.Now().UTC().Format(time.RFC3339)
	return true
}

// endRound releases the single-flight slot and counts the round.
func (s *Scheduler) endRound() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.rounds++
}

func (s *Scheduler) setNext(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextRunAt = t.UTC().Format(time.RFC3339)
}

func (s *Scheduler) snapshotNext() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextRunAt
}

func (s *Scheduler) snapshotRounds() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rounds
}

// SchedulerStatus is the GET /api/health scheduler block: the patrol's
// live state, zero-config when off. lastRunAt/nextRunAt are RFC3339
// strings ("" = not yet / not scheduled).
type SchedulerStatus struct {
	Enabled         bool   `json:"enabled"`
	IntervalSeconds int    `json:"intervalSeconds"`
	Running         bool   `json:"running"`
	Rounds          int    `json:"rounds"`
	LastRunAt       string `json:"lastRunAt"`
	NextRunAt       string `json:"nextRunAt"`
}

// Status snapshots the schedule (nil receiver = the disabled snapshot —
// the OFF channel is a value, the health handler never branches).
func (s *Scheduler) Status() SchedulerStatus {
	if s == nil {
		return SchedulerStatus{Enabled: false}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return SchedulerStatus{
		Enabled:         true,
		IntervalSeconds: int(s.interval / time.Second),
		Running:         s.running,
		Rounds:          s.rounds,
		LastRunAt:       s.lastRunAt,
		NextRunAt:       s.nextRunAt,
	}
}
