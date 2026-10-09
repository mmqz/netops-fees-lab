package repo

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
)

// CheckRunRepo persists check runs and their result rows. ChangesetID "" is
// stored as SQL NULL (run not tied to a changeset); the DDL turns a deleted
// changeset into NULL via ON DELETE SET NULL, which reads back as "".
type CheckRunRepo struct{ q DBTX }

// checkRunColumns is the canonical SELECT column list of check_runs.
const checkRunColumns = `id, workspace_id, changeset_id, trigger, rule_version, started_at, duration_ms, score, topology_json`

// scanCheckRun scans one check_runs row into a domain.CheckRun.
func scanCheckRun(s scanner) (domain.CheckRun, error) {
	var (
		run         domain.CheckRun
		changesetID sql.NullString
		trigger     string
		startedAt   string
	)
	if err := s.Scan(&run.ID, &run.WorkspaceID, &changesetID, &trigger, &run.RuleVersion,
		&startedAt, &run.DurationMs, &run.Score, &run.TopologyJSON); err != nil {
		return domain.CheckRun{}, err
	}
	run.ChangesetID = stringFromNull(changesetID)
	run.Trigger = domain.CheckTrigger(trigger)
	var err error
	if run.StartedAt, err = parseTime(startedAt); err != nil {
		return domain.CheckRun{}, err
	}
	return run, nil
}

// Insert writes the check run row followed by its result rows (seq order as
// given). It runs inside the caller's DB.Do transaction. Result rows keep
// the exact Seq/CheckCode/Message/DetailJSON given; EntityKind/EntityID ""
// are stored as NULL. All input is validated BEFORE the first write, so an
// invalid run or result never leaves a partially inserted row behind in
// autocommit mode.
func (r *CheckRunRepo) Insert(ctx context.Context, run domain.CheckRun, results []domain.CheckResult) error {
	if !run.Trigger.IsValid() {
		return fmt.Errorf("repo: insert check run %s: invalid trigger %q", run.ID, run.Trigger)
	}
	for i, res := range results {
		if res.RunID != "" && res.RunID != run.ID {
			return fmt.Errorf("repo: insert check run %s: results[%d]: RunID %q does not match",
				run.ID, i, res.RunID)
		}
		if !res.Severity.IsValid() {
			return fmt.Errorf("repo: insert check run %s: results[%d]: invalid severity %q",
				run.ID, i, res.Severity)
		}
	}
	if _, err := r.q.ExecContext(ctx, `INSERT INTO check_runs
(id, workspace_id, changeset_id, trigger, rule_version, started_at, duration_ms, score, topology_json)
VALUES (?,?,?,?,?,?,?,?,?)`,
		run.ID, run.WorkspaceID, nullString(run.ChangesetID), run.Trigger.String(), run.RuleVersion,
		formatTime(run.StartedAt), run.DurationMs, run.Score, run.TopologyJSON); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert check run %s: %w: %w", run.ID, domain.ErrConflict, err)
		}
		return fmt.Errorf("repo: insert check run %s: %w", run.ID, err)
	}
	for _, res := range results {
		_, err := r.q.ExecContext(ctx, `INSERT INTO check_results
(run_id, seq, check_code, severity, passed, entity_kind, entity_id, message, detail_json)
VALUES (?,?,?,?,?,?,?,?,?)`,
			run.ID, res.Seq, res.CheckCode, res.Severity.String(), boolToInt(res.Passed),
			nullString(res.EntityKind), nullString(res.EntityID), res.Message, res.DetailJSON)
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("repo: insert check run %s: result %d: %w: %w",
					run.ID, res.Seq, domain.ErrConflict, err)
			}
			return fmt.Errorf("repo: insert check run %s: result %d: %w", run.ID, res.Seq, err)
		}
	}
	return nil
}

// boolToInt maps a bool onto the SQLite INTEGER representation (1/0).
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// GetByID returns the check run with the given id, or an error wrapping
// domain.ErrNotFound.
func (r *CheckRunRepo) GetByID(ctx context.Context, id string) (domain.CheckRun, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+checkRunColumns+` FROM check_runs WHERE id = ?`, id)
	run, err := scanCheckRun(row)
	if err != nil {
		return domain.CheckRun{}, notFound("check run", id, err)
	}
	return run, nil
}

// GetResults returns the result rows of the run in seq order. An unknown
// run yields an empty slice, not an error — existence is the caller's
// concern (GetByID).
func (r *CheckRunRepo) GetResults(ctx context.Context, runID string) ([]domain.CheckResult, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT run_id, seq, check_code, severity, passed, entity_kind, entity_id, message, detail_json
FROM check_results WHERE run_id = ? ORDER BY seq`, runID)
	if err != nil {
		return nil, fmt.Errorf("repo: list results of check run %s: %w", runID, err)
	}
	defer rows.Close()
	out := make([]domain.CheckResult, 0)
	for rows.Next() {
		var (
			res        domain.CheckResult
			severity   string
			passed     int
			entityKind sql.NullString
			entityID   sql.NullString
		)
		if err := rows.Scan(&res.RunID, &res.Seq, &res.CheckCode, &severity, &passed,
			&entityKind, &entityID, &res.Message, &res.DetailJSON); err != nil {
			return nil, fmt.Errorf("repo: scan result of check run %s: %w", runID, err)
		}
		res.Severity = domain.Severity(severity)
		res.Passed = passed != 0
		res.EntityKind = stringFromNull(entityKind)
		res.EntityID = stringFromNull(entityID)
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list results of check run %s: %w", runID, err)
	}
	return out, nil
}

// ListByWorkspace returns one page of the workspace's check runs plus the
// total count ignoring the page window, newest first (ids are
// globally monotonic run-<n>, ordered numerically descending — see
// simIDOrderDesc). page is 1-based (page < 1 is treated as 1); pageSize <= 0
// means unlimited.
func (r *CheckRunRepo) ListByWorkspace(ctx context.Context, workspaceID string, page, pageSize int) ([]domain.CheckRun, int, error) {
	var total int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_runs WHERE workspace_id = ?`, workspaceID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count check runs of workspace %s: %w", workspaceID, err)
	}
	if page < 1 {
		page = 1
	}
	limit, offset := paging(pageSize, (page-1)*pageSize)
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+checkRunColumns+` FROM check_runs WHERE workspace_id = ?
ORDER BY `+simIDOrderDesc+` LIMIT ? OFFSET ?`,
		workspaceID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list check runs of workspace %s: %w", workspaceID, err)
	}
	defer rows.Close()
	out := make([]domain.CheckRun, 0)
	for rows.Next() {
		run, err := scanCheckRun(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("repo: scan check run: %w", err)
		}
		out = append(out, run)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list check runs of workspace %s: %w", workspaceID, err)
	}
	return out, total, nil
}

// Latest returns the newest check run of the workspace regardless of
// trigger, or an error wrapping domain.ErrNotFound when none exists.
func (r *CheckRunRepo) Latest(ctx context.Context, workspaceID string) (domain.CheckRun, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+checkRunColumns+` FROM check_runs
WHERE workspace_id = ? ORDER BY `+simIDOrderDesc+` LIMIT 1`, workspaceID)
	run, err := scanCheckRun(row)
	if err != nil {
		return domain.CheckRun{}, notFound("latest check run", workspaceID, err)
	}
	return run, nil
}

// CountByWorkspace returns the total number of check runs of the workspace.
func (r *CheckRunRepo) CountByWorkspace(ctx context.Context, workspaceID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM check_runs WHERE workspace_id = ?`, workspaceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count check runs of workspace %s: %w", workspaceID, err)
	}
	return n, nil
}
