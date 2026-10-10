package repo

// extend via new methods; do not rewrite.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// AttributeChangeRepo persists resource attribute change proposals
// (migration v11). InsertBatch runs inside the importer's execution
// transaction; Apply/Mark* are single statements intended to be wrapped in
// one DB.Do transaction by the changes service so the resource mutation and
// the status flip commit atomically.
type AttributeChangeRepo struct{ q DBTX }

// changeColumns is the canonical SELECT column list of
// resource_attribute_changes.
const changeColumns = `id, resource_id, batch_id, field, old_value, new_value, status, sheet, row_number, created_at, applied_at`

// scanChange scans one resource_attribute_changes row into a
// domain.AttributeChange.
func scanChange(s scanner) (domain.AttributeChange, error) {
	var (
		ch        domain.AttributeChange
		status    string
		createdAt string
		appliedAt sql.NullString
	)
	err := s.Scan(&ch.ID, &ch.ResourceID, &ch.BatchID, &ch.Field, &ch.OldValue, &ch.NewValue,
		&status, &ch.Sheet, &ch.RowNumber, &createdAt, &appliedAt)
	if err != nil {
		return domain.AttributeChange{}, err
	}
	ch.Status = domain.ChangeStatus(status)
	if ch.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.AttributeChange{}, err
	}
	if ch.AppliedAt, err = timePtrFromNull(appliedAt); err != nil {
		return domain.AttributeChange{}, err
	}
	return ch, nil
}

// InsertBatch inserts every change of chs in one loop inside the caller's
// transaction (the importer's DB.Do). Each change must be pending; IDs are
// caller-generated (chg_ prefix by convention).
func (r *AttributeChangeRepo) InsertBatch(ctx context.Context, chs []domain.AttributeChange) error {
	for _, ch := range chs {
		if ch.Status != domain.ChangeStatusPending {
			return fmt.Errorf("repo: insert attribute change %s: status %s, want pending",
				ch.ID, ch.Status)
		}
		_, err := r.q.ExecContext(ctx, `INSERT INTO resource_attribute_changes
(id, resource_id, batch_id, field, old_value, new_value, status, sheet, row_number, created_at, applied_at)
VALUES (?,?,?,?,?,?,?,?,?,?,NULL)`,
			ch.ID, ch.ResourceID, ch.BatchID, ch.Field, ch.OldValue, ch.NewValue,
			ch.Status.String(), ch.Sheet, ch.RowNumber, formatTime(ch.CreatedAt))
		if err != nil {
			return fmt.Errorf("repo: insert attribute change %s: %w", ch.ID, err)
		}
	}
	return nil
}

// Get returns the change with the given id, or an error wrapping
// domain.ErrNotFound.
func (r *AttributeChangeRepo) Get(ctx context.Context, id string) (domain.AttributeChange, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+changeColumns+` FROM resource_attribute_changes WHERE id = ?`, id)
	ch, err := scanChange(row)
	if err != nil {
		return domain.AttributeChange{}, notFound("attribute change", id, err)
	}
	return ch, nil
}

// ChangeFilter selects attribute change proposals. Empty fields mean "no
// filter"; an empty Status lists every status (the API's status=all). Limit
// <= 0 means unlimited; offset < 0 means 0.
type ChangeFilter struct {
	Status     domain.ChangeStatus
	ResourceID string
	BatchID    string
	Limit      int
	Offset     int
}

// List returns changes matching f plus the total number of matches
// (ignoring limit/offset), ordered by (created_at, id) — the chronological
// order in which multiple pending proposals on the same field queue up.
func (r *AttributeChangeRepo) List(ctx context.Context, f ChangeFilter) ([]domain.AttributeChange, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.Status != "" {
		if !f.Status.IsValid() {
			return nil, 0, fmt.Errorf("repo: list attribute changes: invalid status %q", f.Status)
		}
		where = append(where, "status = ?")
		args = append(args, f.Status.String())
	}
	if f.ResourceID != "" {
		where = append(where, "resource_id = ?")
		args = append(args, f.ResourceID)
	}
	if f.BatchID != "" {
		where = append(where, "batch_id = ?")
		args = append(args, f.BatchID)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM resource_attribute_changes WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count attribute changes: %w", err)
	}
	limit, offset := paging(f.Limit, f.Offset)
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+changeColumns+` FROM resource_attribute_changes WHERE `+whereSQL+
			` ORDER BY created_at, rowid LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list attribute changes: %w", err)
	}
	defer rows.Close()
	out := make([]domain.AttributeChange, 0)
	for rows.Next() {
		ch, err := scanChange(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("repo: scan attribute change: %w", err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list attribute changes: %w", err)
	}
	return out, total, nil
}

// ListPendingByResource returns the pending changes of one resource — the
// importer's idempotency probe: a re-imported row skips recording a field
// whose (field, new value) pair already has a pending proposal.
func (r *AttributeChangeRepo) ListPendingByResource(ctx context.Context, resourceID string) ([]domain.AttributeChange, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+changeColumns+` FROM resource_attribute_changes
WHERE resource_id = ? AND status = 'pending' ORDER BY created_at, rowid`, resourceID)
	if err != nil {
		return nil, fmt.Errorf("repo: list pending changes of resource %s: %w", resourceID, err)
	}
	defer rows.Close()
	out := make([]domain.AttributeChange, 0)
	for rows.Next() {
		ch, err := scanChange(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan attribute change: %w", err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list pending changes of resource %s: %w", resourceID, err)
	}
	return out, nil
}

// MarkApplied flips a pending change to applied and stamps appliedAt.
// The resource mutation itself is NOT part of this statement — the changes
// service runs both inside one DB.Do transaction. A zero RowsAffected
// answers domain.ErrConflict (the change was not pending).
func (r *AttributeChangeRepo) MarkApplied(ctx context.Context, id string, appliedAt time.Time) error {
	return r.markStatus(ctx, id, domain.ChangeStatusApplied, &appliedAt)
}

// MarkDismissed flips a pending change to dismissed (applied_at stays
// NULL). A zero RowsAffected answers domain.ErrConflict.
func (r *AttributeChangeRepo) MarkDismissed(ctx context.Context, id string) error {
	return r.markStatus(ctx, id, domain.ChangeStatusDismissed, nil)
}

// markStatus is the shared UPDATE of the pending→terminal transitions.
func (r *AttributeChangeRepo) markStatus(ctx context.Context, id string, status domain.ChangeStatus, appliedAt *time.Time) error {
	var appliedArg any
	if appliedAt != nil {
		appliedArg = formatTime(*appliedAt)
	}
	res, err := r.q.ExecContext(ctx,
		`UPDATE resource_attribute_changes SET status = ?, applied_at = ? WHERE id = ? AND status = 'pending'`,
		status.String(), appliedArg, id)
	if err != nil {
		return fmt.Errorf("repo: mark attribute change %s %s: %w", id, status, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("repo: mark attribute change %s %s: rows affected: %w", id, status, err)
	} else if n == 0 {
		// Either the change does not exist (caller already Get-verified it —
		// the transaction makes a concurrent flip impossible) or it is no
		// longer pending; both are conflicts for the caller's 409.
		return fmt.Errorf("repo: mark attribute change %s %s: not pending: %w", id, status, domain.ErrConflict)
	}
	return nil
}

// DeletePendingByBatch deletes the batch's PENDING change proposals and
// returns the number of deleted rows — the rollback delete-chain member.
// Applied/dismissed proposals survive the rollback (the human-decision
// audit trail is decoupled from the import-batch lifecycle); proposals
// whose resource is deleted by the same rollback are removed by the
// resource_id ON DELETE CASCADE instead.
func (r *AttributeChangeRepo) DeletePendingByBatch(ctx context.Context, batchID string) (int64, error) {
	res, err := r.q.ExecContext(ctx,
		`DELETE FROM resource_attribute_changes WHERE batch_id = ? AND status = 'pending'`, batchID)
	if err != nil {
		return 0, fmt.Errorf("repo: delete pending changes of batch %s: %w", batchID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: delete pending changes of batch %s: rows affected: %w", batchID, err)
	}
	return n, nil
}

// CountPendingByBatch returns the number of pending change proposals
// attributed to batchID (COUNT aggregate — the rollback response's
// deleted.attributeChanges accounting, same scope as DeletePendingByBatch).
func (r *AttributeChangeRepo) CountPendingByBatch(ctx context.Context, batchID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM resource_attribute_changes WHERE batch_id = ? AND status = 'pending'`,
		batchID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count pending changes of batch %s: %w", batchID, err)
	}
	return n, nil
}
