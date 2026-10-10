package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// simIDOrderDesc orders global per-kind sequence ids ("<kind>-<n>", fixed
// prefix per table) numerically DESCENDING: longer ids carry bigger n, and
// within one length lexicographic order equals numeric order. Plain
// `ORDER BY id DESC` would sort "cs-10" before "cs-9" incorrectly.
const simIDOrderDesc = `LENGTH(id) DESC, id DESC`

// ChangeSetRepo persists changesets and their per-op rows. Ids are
// globally monotonic (cs-<n>), so the numerically largest id of a
// workspace is its latest changeset.
type ChangeSetRepo struct{ q DBTX }

// changeSetColumns is the canonical SELECT column list of changesets.
const changeSetColumns = `id, workspace_id, description, trigger, status, ops_applied, created_at, reverted_at`

// scanChangeSet scans one changesets row into a domain.ChangeSet.
// reverted_at NULL maps to the zero time (= not reverted).
func scanChangeSet(s scanner) (domain.ChangeSet, error) {
	var (
		cs         domain.ChangeSet
		trigger    string
		status     string
		createdAt  string
		revertedAt sql.NullString
	)
	if err := s.Scan(&cs.ID, &cs.WorkspaceID, &cs.Description, &trigger, &status,
		&cs.OpsApplied, &createdAt, &revertedAt); err != nil {
		return domain.ChangeSet{}, err
	}
	cs.Trigger = domain.ChangeSetTrigger(trigger)
	cs.Status = domain.ChangeSetStatus(status)
	var err error
	if cs.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.ChangeSet{}, err
	}
	if revertedAt.Valid {
		if cs.RevertedAt, err = parseTime(revertedAt.String); err != nil {
			return domain.ChangeSet{}, err
		}
	}
	return cs, nil
}

// Insert writes the changeset row followed by its op rows (seq order as
// given). It runs inside the caller's DB.Do transaction — the engine relies
// on that for the apply/rollback semantics of PROJECT_BRIEF_P2 §4.
// InverseJSON "" is stored as NULL (no inverse: failed changesets).
// All input is validated BEFORE the first write, so an invalid changeset or
// op never leaves a partially inserted row behind in autocommit mode.
func (r *ChangeSetRepo) Insert(ctx context.Context, cs domain.ChangeSet, ops []domain.ChangeSetOp) error {
	if !cs.Trigger.IsValid() {
		return fmt.Errorf("repo: insert changeset %s: invalid trigger %q", cs.ID, cs.Trigger)
	}
	if !cs.Status.IsValid() {
		return fmt.Errorf("repo: insert changeset %s: invalid status %q", cs.ID, cs.Status)
	}
	for i, op := range ops {
		if op.ChangeSetID != "" && op.ChangeSetID != cs.ID {
			return fmt.Errorf("repo: insert changeset %s: ops[%d]: ChangeSetID %q does not match",
				cs.ID, i, op.ChangeSetID)
		}
		if !op.OpResult.IsValid() {
			return fmt.Errorf("repo: insert changeset %s: ops[%d]: invalid result %q",
				cs.ID, i, op.OpResult)
		}
	}
	if _, err := r.q.ExecContext(ctx, `INSERT INTO changesets
(id, workspace_id, description, trigger, status, ops_applied, created_at, reverted_at)
VALUES (?,?,?,?,?,?,?,?)`,
		cs.ID, cs.WorkspaceID, cs.Description, cs.Trigger.String(), cs.Status.String(),
		cs.OpsApplied, formatTime(cs.CreatedAt), nullableTimePtr(revertedAtPtr(cs.RevertedAt))); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert changeset %s: %w: %w", cs.ID, domain.ErrConflict, err)
		}
		return fmt.Errorf("repo: insert changeset %s: %w", cs.ID, err)
	}
	for _, op := range ops {
		_, err := r.q.ExecContext(ctx, `INSERT INTO changeset_ops
(changeset_id, seq, op_code, op_json, inverse_json, result)
VALUES (?,?,?,?,?,?)`,
			cs.ID, op.Seq, op.OpCode, op.OpJSON, nullString(op.InverseJSON), op.OpResult.String())
		if err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("repo: insert changeset %s: op %d: %w: %w",
					cs.ID, op.Seq, domain.ErrConflict, err)
			}
			return fmt.Errorf("repo: insert changeset %s: op %d: %w", cs.ID, op.Seq, err)
		}
	}
	return nil
}

// revertedAtPtr maps the domain convention "zero time = not reverted" onto
// the nullable-timestamp helper (nil → SQL NULL).
func revertedAtPtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// GetByID returns the changeset with the given id, or an error wrapping
// domain.ErrNotFound.
func (r *ChangeSetRepo) GetByID(ctx context.Context, id string) (domain.ChangeSet, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+changeSetColumns+` FROM changesets WHERE id = ?`, id)
	cs, err := scanChangeSet(row)
	if err != nil {
		return domain.ChangeSet{}, notFound("changeset", id, err)
	}
	return cs, nil
}

// GetOps returns the op rows of the changeset in application order (seq
// ascending). An unknown changeset yields an empty slice, not an error —
// existence is the caller's concern (GetByID).
func (r *ChangeSetRepo) GetOps(ctx context.Context, id string) ([]domain.ChangeSetOp, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT changeset_id, seq, op_code, op_json, inverse_json, result
FROM changeset_ops WHERE changeset_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("repo: list ops of changeset %s: %w", id, err)
	}
	defer rows.Close()
	out := make([]domain.ChangeSetOp, 0)
	for rows.Next() {
		var (
			op      domain.ChangeSetOp
			result  string
			inverse sql.NullString
		)
		if err := rows.Scan(&op.ChangeSetID, &op.Seq, &op.OpCode, &op.OpJSON, &inverse, &result); err != nil {
			return nil, fmt.Errorf("repo: scan op of changeset %s: %w", id, err)
		}
		op.InverseJSON = stringFromNull(inverse)
		op.OpResult = domain.OpResult(result)
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list ops of changeset %s: %w", id, err)
	}
	return out, nil
}

// ListByWorkspace returns one page of the workspace's changesets plus the
// total count ignoring the page window, newest first (ids are per-workspace
// monotonic cs-<n>, ordered numerically descending — see simIDOrderDesc).
// page is 1-based (page < 1 is treated as 1); pageSize <= 0 means unlimited.
func (r *ChangeSetRepo) ListByWorkspace(ctx context.Context, workspaceID string, page, pageSize int) ([]domain.ChangeSet, int, error) {
	var total int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM changesets WHERE workspace_id = ?`, workspaceID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count changesets of workspace %s: %w", workspaceID, err)
	}
	if page < 1 {
		page = 1
	}
	limit, offset := paging(pageSize, (page-1)*pageSize)
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+changeSetColumns+` FROM changesets WHERE workspace_id = ?
ORDER BY `+simIDOrderDesc+` LIMIT ? OFFSET ?`,
		workspaceID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list changesets of workspace %s: %w", workspaceID, err)
	}
	defer rows.Close()
	out := make([]domain.ChangeSet, 0)
	for rows.Next() {
		cs, err := scanChangeSet(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("repo: scan changeset: %w", err)
		}
		out = append(out, cs)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list changesets of workspace %s: %w", workspaceID, err)
	}
	return out, total, nil
}

// MarkReverted marks the changeset reverted with the given revert time.
// The engine decides revertibility (LIFO — only the latest applied
// changeset); the repo only records the transition. The UPDATE carries a
// status guard (applied → reverted, AUD1-8): a changeset that is already
// reverted or failed is NEVER re-marked (its reverted_at cannot be
// overwritten), answering domain.ErrConflict instead. A missing changeset
// returns an error wrapping domain.ErrNotFound.
func (r *ChangeSetRepo) MarkReverted(ctx context.Context, id string, revertedAt time.Time) error {
	res, err := r.q.ExecContext(ctx,
		`UPDATE changesets SET status = ?, reverted_at = ? WHERE id = ? AND status = ?`,
		domain.ChangeSetStatusReverted.String(), formatTime(revertedAt.UTC()), id,
		domain.ChangeSetStatusApplied.String())
	if err != nil {
		return fmt.Errorf("repo: mark changeset %s reverted: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("repo: mark changeset %s reverted: %w", id, err)
	} else if n == 1 {
		return nil
	}
	// Zero rows: either the changeset does not exist (ErrNotFound) or its
	// status is no longer applied (ErrConflict — the guard refused).
	if _, err := r.GetByID(ctx, id); err != nil {
		return err
	}
	return fmt.Errorf("repo: mark changeset %s reverted: changeset is not applied: %w", id, domain.ErrConflict)
}

// CountByWorkspace returns the total number of changesets of the workspace.
func (r *ChangeSetRepo) CountByWorkspace(ctx context.Context, workspaceID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM changesets WHERE workspace_id = ?`, workspaceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count changesets of workspace %s: %w", workspaceID, err)
	}
	return n, nil
}

// LatestApplied returns the newest applied (not reverted, not failed)
// changeset of the workspace — the only candidate for undo. An error
// wrapping domain.ErrNotFound is returned when no applied changeset exists.
func (r *ChangeSetRepo) LatestApplied(ctx context.Context, workspaceID string) (domain.ChangeSet, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+changeSetColumns+` FROM changesets
WHERE workspace_id = ? AND status = ?
ORDER BY `+simIDOrderDesc+` LIMIT 1`,
		workspaceID, domain.ChangeSetStatusApplied.String())
	cs, err := scanChangeSet(row)
	if err != nil {
		return domain.ChangeSet{}, notFound("latest applied changeset", workspaceID, err)
	}
	return cs, nil
}
