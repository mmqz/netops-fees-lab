package detect

// recorder.go — the run receipt persistence (v0.11-R11): RunAndRecord
// wraps Engine.Detect so every trigger origin (HTTP / CLI / scheduler)
// leaves the SAME durable trail in detection_runs (migration v21), one
// row per sweep — success or failure. Until v0.10 the RunResult lived
// only in the HTTP response and the webhook payload: gone the moment the
// connection closed; the scheduled-inspection story needs the history.
//
// The engine itself stays stateless and pure (ADR-0011 D2: 判定引擎为纯
// 函数，无时钟依赖) — recording is a wrapper concern, and the clock is
// injected (now func) so tests stay deterministic.

import (
	"context"
	"fmt"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// RunAndRecord executes one engine sweep over (db state, rules, window)
// and persists the receipt: on success the RunResult counts and
// evaluatedAt; on failure the error text (a failed run is itself an
// operational fact worth auditing — an empty error column means success,
// a non-empty one names the failure). The returned domain.DetectionRun is
// the persisted row; the error is the ORIGINAL Detect error when the
// sweep failed (callers keep their existing error paths), or a recording
// failure when the receipt could not be stored (the history must never
// silently lie, so a persistence failure fails the caller even though
// the sweep itself succeeded).
func RunAndRecord(ctx context.Context, db *repo.DB, eng *Engine, opts Options, source domain.RunSource, now func() time.Time) (RunResult, domain.DetectionRun, error) {
	if !source.IsValid() {
		return RunResult{}, domain.DetectionRun{}, fmt.Errorf("detect: record run: invalid source %q (api/cli/scheduler)", source)
	}
	started := now()
	res, err := eng.Detect(ctx, db, opts)
	finished := now()

	rec := domain.DetectionRun{
		RunID:            domain.NewID("run"),
		Source:           source,
		StartedAt:        formatRunTS(started),
		FinishedAt:       formatRunTS(finished),
		DurationMS:       finished.Sub(started).Milliseconds(),
		From:             opts.From,
		To:               opts.To,
		EvaluatedAt:      res.EvaluatedAt,
		Findings:         res.Findings,
		AlertsCreated:    res.AlertsCreated,
		AlertsCleared:    res.AlertsCleared,
		RunStatesWritten: res.RunStatesWritten,
		Suppressed:       res.Suppressed,
	}
	if err != nil {
		rec.Error = err.Error()
		// The sweep failed: the receipt still lands (that is the point of
		// failure auditing), and the caller sees the ORIGINAL error. The
		// insert deliberately detaches from the run's cancellation — a
		// cancelled/timed-out sweep is exactly the failure worth persisting,
		// so the receipt write uses context.WithoutCancel plus a bounded
		// timeout of its own (a stuck insert must not hang the caller).
		insCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if insErr := db.DetectionRuns.InsertRun(insCtx, rec); insErr != nil {
			return res, rec, fmt.Errorf("detect: sweep failed (%v) AND recording it failed: %w", err, insErr)
		}
		return res, rec, err
	}
	if insErr := db.DetectionRuns.InsertRun(ctx, rec); insErr != nil {
		return res, rec, fmt.Errorf("detect: record run %s: %w", rec.RunID, insErr)
	}
	return res, rec, nil
}

// formatRunTS renders the wall-clock run timestamps: RFC3339 with
// sub-second precision — two scheduler rounds can start inside the same
// second, and the listing's deterministic tie-break (started_at DESC,
// id ASC) should not degenerate to id order for them.
func formatRunTS(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
