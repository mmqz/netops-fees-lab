package repo

// detectionruns.go — the detection run history storage (v0.11-R11,
// migration v21): one durable receipt row per detection sweep, whatever
// the trigger (api / cli / scheduler). Written by detect.RunAndRecord
// AFTER the sweep — success or failure — read by
// GET /api/detection/runs. All SQL of the run-history channel lives here.

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
)

// DetectionRunRepo persists the detection run receipt rows.
type DetectionRunRepo struct{ q DBTX }

// InsertRun stores one run receipt. The CHECK constraint on source makes
// an out-of-enum origin a storage-level failure (the domain layer parses
// first, the constraint is the backstop).
func (r *DetectionRunRepo) InsertRun(ctx context.Context, run domain.DetectionRun) error {
	_, err := r.q.ExecContext(ctx, `
INSERT INTO detection_runs
  (id, source, started_at, finished_at, duration_ms, from_ts, to_ts, evaluated_at,
   findings, alerts_created, alerts_cleared, run_states_written, error)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID, run.Source.String(), run.StartedAt, run.FinishedAt, run.DurationMS,
		run.From, run.To, run.EvaluatedAt,
		run.Findings, run.AlertsCreated, run.AlertsCleared, run.RunStatesWritten, run.Error)
	if err != nil {
		return fmt.Errorf("repo: insert detection run %s: %w", run.RunID, err)
	}
	return nil
}

// ListRuns returns the run receipts newest-started first (id ASC as the
// deterministic tie-break — two runs can share a second-precision
// started_at), with limit/offset pagination. The second return is the
// TOTAL count of the unpaginated set (the envelope's total). limit <= 0
// means unbounded (the CLI/rules listing precedent).
func (r *DetectionRunRepo) ListRuns(ctx context.Context, limit, offset int) ([]domain.DetectionRun, int, error) {
	var total int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM detection_runs`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count detection runs: %w", err)
	}

	query := `
SELECT id, source, started_at, finished_at, duration_ms, from_ts, to_ts, evaluated_at,
       findings, alerts_created, alerts_cleared, run_states_written, error
FROM detection_runs
ORDER BY started_at DESC, id ASC`
	l, o := paging(limit, offset)
	if l >= 0 {
		query += fmt.Sprintf(" LIMIT %d OFFSET %d", l, o)
	}

	rows, err := r.q.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list detection runs: %w", err)
	}
	defer rows.Close()
	out := make([]domain.DetectionRun, 0, 16)
	for rows.Next() {
		var (
			run       domain.DetectionRun
			source    string
			fromTS    sql.NullString
			toTS      sql.NullString
			evaluated sql.NullString
			runErr    sql.NullString
		)
		if err := rows.Scan(&run.RunID, &source, &run.StartedAt, &run.FinishedAt, &run.DurationMS,
			&fromTS, &toTS, &evaluated,
			&run.Findings, &run.AlertsCreated, &run.AlertsCleared, &run.RunStatesWritten, &runErr); err != nil {
			return nil, 0, fmt.Errorf("repo: scan detection run row: %w", err)
		}
		run.Source = domain.RunSource(source)
		run.From = fromTS.String
		run.To = toTS.String
		run.EvaluatedAt = evaluated.String
		run.Error = runErr.String
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: iterate detection runs: %w", err)
	}
	return out, total, nil
}

// AlertStatsRow is one cell of the state × severity alert matrix: the
// count of alerts in exactly that (state, severity) cell.
type AlertStatsRow struct {
	State    domain.AlertState
	Severity domain.Severity
	Count    int
}

// AlertStats returns the state × severity GROUP BY matrix of ALL alerts
// (every lifecycle state — the dashboard's distribution view; filter to
// active cells in the composer). One query, one join (severity lives on
// the rule, the ListAlerts precedent).
func (r *AlertRepo) AlertStats(ctx context.Context) ([]AlertStatsRow, error) {
	rows, err := r.q.QueryContext(ctx, `
SELECT a.state, dr.severity, COUNT(*)
FROM alerts a
JOIN detection_rules dr ON dr.rule_id = a.rule_id
GROUP BY a.state, dr.severity`)
	if err != nil {
		return nil, fmt.Errorf("repo: alert stats: %w", err)
	}
	defer rows.Close()
	out := make([]AlertStatsRow, 0, 8)
	for rows.Next() {
		var (
			row      AlertStatsRow
			state    string
			severity string
			count    int
		)
		if err := rows.Scan(&state, &severity, &count); err != nil {
			return nil, fmt.Errorf("repo: scan alert stats row: %w", err)
		}
		row.State = domain.AlertState(state)
		row.Severity = domain.Severity(severity)
		row.Count = count
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate alert stats: %w", err)
	}
	return out, nil
}
