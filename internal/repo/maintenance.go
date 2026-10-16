package repo

// maintenance.go — the maintenance-window storage (v0.12-R15, migration
// v24): planned [starts_at, ends_at) windows whose scoped resources do not
// produce NEW alert fires. All SQL of the window channel lives here; the
// suppression VERDICT itself is pure and lives in internal/detect (the
// engine loads the windows through the same repo inside its one
// transaction).

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
)

// MaintenanceRepo persists the maintenance-window rows.
type MaintenanceRepo struct{ q DBTX }

// InsertWindow stores one window. The caller (API layer) has already
// validated and UTC-normalized the bounds; the repo trusts its input the
// way it trusts detection_rules payloads.
func (r *MaintenanceRepo) InsertWindow(ctx context.Context, w domain.MaintenanceWindow) error {
	_, err := r.q.ExecContext(ctx, `
INSERT INTO maintenance_windows
  (id, label, starts_at, ends_at, scope_resource_ids, note, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		w.WindowID, w.Label, w.StartsAt, w.EndsAt,
		domain.EncodeMaintenanceScope(w.ResourceIDs), w.Note, w.CreatedAt)
	if err != nil {
		return fmt.Errorf("repo: insert maintenance window %s: %w", w.WindowID, err)
	}
	return nil
}

// ListWindows returns every window, newest-started first (id ASC as the
// deterministic tie-break — the detection_runs listing precedent). The set
// is small by nature (open plans, cleaned up as they end), so the channel
// is a full list, not a paginated crawl.
func (r *MaintenanceRepo) ListWindows(ctx context.Context) ([]domain.MaintenanceWindow, error) {
	rows, err := r.q.QueryContext(ctx, `
SELECT id, label, starts_at, ends_at, scope_resource_ids, note, created_at
FROM maintenance_windows
ORDER BY starts_at DESC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("repo: list maintenance windows: %w", err)
	}
	defer rows.Close()
	out := make([]domain.MaintenanceWindow, 0, 8)
	for rows.Next() {
		var (
			w     domain.MaintenanceWindow
			scope sql.NullString
			note  sql.NullString
		)
		if err := rows.Scan(&w.WindowID, &w.Label, &w.StartsAt, &w.EndsAt, &scope, &note, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("repo: scan maintenance window row: %w", err)
		}
		w.ResourceIDs, err = domain.DecodeMaintenanceScope(scope.String)
		if err != nil {
			return nil, err
		}
		w.Note = note.String
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: iterate maintenance windows: %w", err)
	}
	return out, nil
}

// DeleteWindow removes one window by id. Missing id → domain.ErrNotFound
// (the honest 404, not a silent no-op); an ended window's audit trail is
// the detection_runs receipts it influenced, not the plan row itself.
func (r *MaintenanceRepo) DeleteWindow(ctx context.Context, id string) error {
	res, err := r.q.ExecContext(ctx, `DELETE FROM maintenance_windows WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("repo: delete maintenance window %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("repo: delete maintenance window %s (affected): %w", id, err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
