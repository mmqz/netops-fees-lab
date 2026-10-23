package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// WorkspaceRepo persists simulation workspaces (PROJECT_BRIEF_P2 §3).
type WorkspaceRepo struct{ q DBTX }

// workspaceColumns is the canonical SELECT column list of workspaces.
const workspaceColumns = `id, name, description, status, source_json, created_at, updated_at`

// scanWorkspace scans one workspaces row into a domain.Workspace.
func scanWorkspace(s scanner) (domain.Workspace, error) {
	var (
		w         domain.Workspace
		status    string
		createdAt string
		updatedAt string
	)
	if err := s.Scan(&w.ID, &w.Name, &w.Description, &status, &w.SourceJSON, &createdAt, &updatedAt); err != nil {
		return domain.Workspace{}, err
	}
	w.Status = domain.WorkspaceStatus(status)
	var err error
	if w.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Workspace{}, err
	}
	if w.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Workspace{}, err
	}
	return w, nil
}

// Insert adds a new workspace. An empty SourceJSON is stored as "{}". A
// duplicate name returns an error wrapping domain.ErrConflict.
func (r *WorkspaceRepo) Insert(ctx context.Context, w domain.Workspace) error {
	if !w.Status.IsValid() {
		return fmt.Errorf("repo: insert workspace %s: invalid status %q", w.ID, w.Status)
	}
	source := w.SourceJSON
	if source == "" {
		source = "{}"
	}
	_, err := r.q.ExecContext(ctx, `INSERT INTO workspaces
(id, name, description, status, source_json, created_at, updated_at)
VALUES (?,?,?,?,?,?,?)`,
		w.ID, w.Name, w.Description, w.Status.String(), source,
		formatTime(w.CreatedAt), formatTime(w.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert workspace %s (name %q): %w: %w",
				w.ID, w.Name, domain.ErrConflict, err)
		}
		return fmt.Errorf("repo: insert workspace %s: %w", w.ID, err)
	}
	return nil
}

// GetByID returns the workspace with the given id, or an error wrapping
// domain.ErrNotFound.
func (r *WorkspaceRepo) GetByID(ctx context.Context, id string) (domain.Workspace, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces WHERE id = ?`, id)
	w, err := scanWorkspace(row)
	if err != nil {
		return domain.Workspace{}, notFound("workspace", id, err)
	}
	return w, nil
}

// GetByName returns the workspace with the given unique name, or an error
// wrapping domain.ErrNotFound.
func (r *WorkspaceRepo) GetByName(ctx context.Context, name string) (domain.Workspace, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+workspaceColumns+` FROM workspaces WHERE name = ?`, name)
	w, err := scanWorkspace(row)
	if err != nil {
		return domain.Workspace{}, notFound("workspace name", name, err)
	}
	return w, nil
}

// List returns one page of workspaces plus the total count ignoring the
// page window. page is 1-based (page < 1 is treated as 1); pageSize <= 0
// means unlimited. Order: created_at DESC then rowid DESC (insertion order),
// so equal-timestamp workspaces stay deterministic — workspace ids are
// random UUIDs and cannot order.
func (r *WorkspaceRepo) List(ctx context.Context, page, pageSize int) ([]domain.Workspace, int, error) {
	var total int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count workspaces: %w", err)
	}
	if page < 1 {
		page = 1
	}
	limit, offset := paging(pageSize, (page-1)*pageSize)
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+workspaceColumns+` FROM workspaces ORDER BY created_at DESC, rowid DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list workspaces: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Workspace, 0)
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("repo: scan workspace: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list workspaces: %w", err)
	}
	return out, total, nil
}

// UpdateTimestamps stamps updated_at to now on the workspace. A missing
// workspace returns an error wrapping domain.ErrNotFound.
func (r *WorkspaceRepo) UpdateTimestamps(ctx context.Context, id string) error {
	res, err := r.q.ExecContext(ctx, `UPDATE workspaces SET updated_at = ? WHERE id = ?`,
		formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("repo: update timestamps of workspace %s: %w", id, err)
	}
	return requireUpdated(res, "workspace", id)
}

// Delete removes a workspace together with all of its simulation rows (the
// DDL v5 ON DELETE CASCADEs do the work) and returns the per-table removed
// counts keyed by table name: virtual_devices, interfaces, links,
// changesets, check_runs, checkpoints. Child rows are deleted explicitly
// first (child → parent order) so each DELETE reports the rows that carried
// the workspace_id, unaffected by cascades. changeset_ops and check_results
// are cascade-only children of changesets / check_runs and are not counted.
// Deleting an unknown workspace returns an error wrapping
// domain.ErrNotFound. Best-effort removal of the workspace's checkpoint
// files on disk is the service layer's job (P2-T3), not the repo's.
func (r *WorkspaceRepo) Delete(ctx context.Context, id string) (map[string]int64, error) {
	counts := map[string]int64{
		"virtual_devices": 0,
		"interfaces":      0,
		"links":           0,
		"changesets":      0,
		"check_runs":      0,
		"checkpoints":     0,
	}
	for _, table := range []string{
		"links", "interfaces", "virtual_devices", "changesets", "check_runs", "checkpoints",
	} {
		res, err := r.q.ExecContext(ctx, `DELETE FROM `+table+` WHERE workspace_id = ?`, id)
		if err != nil {
			return nil, fmt.Errorf("repo: delete workspace %s: purge %s: %w", id, table, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("repo: delete workspace %s: purge %s: rows affected: %w", id, table, err)
		}
		counts[table] = n
	}
	res, err := r.q.ExecContext(ctx, `DELETE FROM workspaces WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("repo: delete workspace %s: %w", id, err)
	}
	if err := requireUpdated(res, "workspace", id); err != nil {
		return nil, err
	}
	return counts, nil
}
