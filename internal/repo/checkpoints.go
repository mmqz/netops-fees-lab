package repo

import (
	"context"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
)

// CheckpointRepo persists checkpoint markers. The snapshot database files
// themselves live on disk (managed by the service layer via the SQLite
// Online Backup API, P2-T3); this repo only records their relative path and
// the workspace state hash they captured.
type CheckpointRepo struct{ q DBTX }

// checkpointColumns is the canonical SELECT column list of checkpoints.
const checkpointColumns = `id, workspace_id, label, file_path, workspace_hash, created_at`

// scanCheckpoint scans one checkpoints row into a domain.Checkpoint.
func scanCheckpoint(s scanner) (domain.Checkpoint, error) {
	var (
		cp        domain.Checkpoint
		createdAt string
	)
	if err := s.Scan(&cp.ID, &cp.WorkspaceID, &cp.Label, &cp.FilePath, &cp.WorkspaceHash, &createdAt); err != nil {
		return domain.Checkpoint{}, err
	}
	var err error
	if cp.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Checkpoint{}, err
	}
	return cp, nil
}

// Insert adds a checkpoint marker. FilePath is stored verbatim (expected to
// be relative to the lab DB directory, per PROJECT_BRIEF_P2 §4).
func (r *CheckpointRepo) Insert(ctx context.Context, cp domain.Checkpoint) error {
	_, err := r.q.ExecContext(ctx, `INSERT INTO checkpoints
(id, workspace_id, label, file_path, workspace_hash, created_at)
VALUES (?,?,?,?,?,?)`,
		cp.ID, cp.WorkspaceID, cp.Label, cp.FilePath, cp.WorkspaceHash, formatTime(cp.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert checkpoint %s: %w: %w", cp.ID, domain.ErrConflict, err)
		}
		return fmt.Errorf("repo: insert checkpoint %s: %w", cp.ID, err)
	}
	return nil
}

// GetByID returns the checkpoint with the given id, or an error wrapping
// domain.ErrNotFound.
func (r *CheckpointRepo) GetByID(ctx context.Context, id string) (domain.Checkpoint, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+checkpointColumns+` FROM checkpoints WHERE id = ?`, id)
	cp, err := scanCheckpoint(row)
	if err != nil {
		return domain.Checkpoint{}, notFound("checkpoint", id, err)
	}
	return cp, nil
}

// ListByWorkspace returns every checkpoint of the workspace, newest first
// (ids are globally monotonic cp-<n>, ordered numerically descending —
// see simIDOrderDesc).
func (r *CheckpointRepo) ListByWorkspace(ctx context.Context, workspaceID string) ([]domain.Checkpoint, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+checkpointColumns+` FROM checkpoints WHERE workspace_id = ?
ORDER BY `+simIDOrderDesc, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repo: list checkpoints of workspace %s: %w", workspaceID, err)
	}
	defer rows.Close()
	out := make([]domain.Checkpoint, 0)
	for rows.Next() {
		cp, err := scanCheckpoint(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan checkpoint: %w", err)
		}
		out = append(out, cp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list checkpoints of workspace %s: %w", workspaceID, err)
	}
	return out, nil
}

// CountByWorkspace returns the total number of checkpoints of the
// workspace.
func (r *CheckpointRepo) CountByWorkspace(ctx context.Context, workspaceID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM checkpoints WHERE workspace_id = ?`, workspaceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count checkpoints of workspace %s: %w", workspaceID, err)
	}
	return n, nil
}

// Delete removes the checkpoint marker. The snapshot file on disk is the
// service layer's concern (P2-T3). A missing checkpoint returns an error
// wrapping domain.ErrNotFound.
func (r *CheckpointRepo) Delete(ctx context.Context, id string) error {
	res, err := r.q.ExecContext(ctx, `DELETE FROM checkpoints WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("repo: delete checkpoint %s: %w", id, err)
	}
	return requireUpdated(res, "checkpoint", id)
}
