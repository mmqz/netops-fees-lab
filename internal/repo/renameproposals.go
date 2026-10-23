package repo

// extend via new methods; do not rewrite.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// RenameProposalRepo persists rename proposals (migration v12, W6-D). Insert
// runs inside the importer's execution transaction; MarkApplied/MarkRejected
// are guarded single statements intended to be wrapped in one DB.Do
// transaction by the renames service so the resource merge (relation
// retarget, resource delete, natural_key rewrite) and the status flip commit
// atomically.
type RenameProposalRepo struct{ q DBTX }

// renameColumns is the canonical SELECT column list of rename_proposals.
const renameColumns = `id, resource_id, new_resource_id, batch_id, resource_type, old_key, new_key, matched_fields, status, sheet, row_number, created_at, decided_at`

// scanRename scans one rename_proposals row into a domain.RenameProposal.
func scanRename(s scanner) (domain.RenameProposal, error) {
	var (
		rp            domain.RenameProposal
		rtype         string
		status        string
		matchedFields string
		createdAt     string
		decidedAt     sql.NullString
	)
	err := s.Scan(&rp.ID, &rp.ResourceID, &rp.NewResourceID, &rp.BatchID, &rtype, &rp.OldKey, &rp.NewKey,
		&matchedFields, &status, &rp.Sheet, &rp.RowNumber, &createdAt, &decidedAt)
	if err != nil {
		return domain.RenameProposal{}, err
	}
	rp.ResourceType = domain.ResourceType(rtype)
	rp.Status = domain.RenameStatus(status)
	if err := json.Unmarshal([]byte(matchedFields), &rp.MatchedFields); err != nil {
		return domain.RenameProposal{}, fmt.Errorf("repo: parse matched_fields %q: %w", matchedFields, err)
	}
	if rp.MatchedFields == nil {
		rp.MatchedFields = []string{}
	}
	if rp.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.RenameProposal{}, err
	}
	if rp.DecidedAt, err = timePtrFromNull(decidedAt); err != nil {
		return domain.RenameProposal{}, err
	}
	return rp, nil
}

// Insert adds a new rename proposal. The proposal must be pending (the
// importer only ever creates pending suggestions); IDs are caller-generated
// (rp_ prefix by convention).
func (r *RenameProposalRepo) Insert(ctx context.Context, rp domain.RenameProposal) error {
	if rp.Status != domain.RenameStatusPending {
		return fmt.Errorf("repo: insert rename proposal %s: status %s, want pending", rp.ID, rp.Status)
	}
	fields := rp.MatchedFields
	if fields == nil {
		fields = []string{}
	}
	matchedJSON, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("repo: marshal matched fields: %w", err)
	}
	_, err = r.q.ExecContext(ctx, `INSERT INTO rename_proposals
(id, resource_id, new_resource_id, batch_id, resource_type, old_key, new_key, matched_fields, status, sheet, row_number, created_at, decided_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,NULL)`,
		rp.ID, rp.ResourceID, rp.NewResourceID, rp.BatchID, rp.ResourceType.String(),
		rp.OldKey, rp.NewKey, string(matchedJSON), rp.Status.String(),
		rp.Sheet, rp.RowNumber, formatTime(rp.CreatedAt))
	if err != nil {
		return fmt.Errorf("repo: insert rename proposal %s: %w", rp.ID, err)
	}
	return nil
}

// Get returns the rename proposal with the given id, or an error wrapping
// domain.ErrNotFound.
func (r *RenameProposalRepo) Get(ctx context.Context, id string) (domain.RenameProposal, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+renameColumns+` FROM rename_proposals WHERE id = ?`, id)
	rp, err := scanRename(row)
	if err != nil {
		return domain.RenameProposal{}, notFound("rename proposal", id, err)
	}
	return rp, nil
}

// RenameFilter selects rename proposals. Empty fields mean "no filter"; an
// empty Status lists every status (the API's status=all). ResourceID matches
// the proposal's EITHER side (resource_id = old resource OR
// new_resource_id — W11-D: the W006 issue anchors on the new resource, so a
// drawer-side query must surface the proposal from both sides). Limit <= 0
// means unlimited; offset < 0 means 0.
type RenameFilter struct {
	Status       domain.RenameStatus
	ResourceID   string
	ResourceType domain.ResourceType
	BatchID      string
	Limit        int
	Offset       int
}

// List returns rename proposals matching f plus the total number of matches
// (ignoring limit/offset), ordered by (created_at, rowid) — the
// chronological queue order, same contract as the attribute change list.
func (r *RenameProposalRepo) List(ctx context.Context, f RenameFilter) ([]domain.RenameProposal, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.Status != "" {
		if !f.Status.IsValid() {
			return nil, 0, fmt.Errorf("repo: list rename proposals: invalid status %q", f.Status)
		}
		where = append(where, "status = ?")
		args = append(args, f.Status.String())
	}
	if f.ResourceID != "" {
		// Either side (W11-D): a proposal must surface from BOTH the
		// old resource's drawer (resource_id = identity keeper) and
		// the NEW resource's drawer (new_resource_id — where the W006
		// issue deep-links to). Single-sided matching hid the panel
		// on the new side.
		where = append(where, "(resource_id = ? OR new_resource_id = ?)")
		args = append(args, f.ResourceID, f.ResourceID)
	}
	if f.ResourceType != "" {
		if !f.ResourceType.IsValid() {
			return nil, 0, fmt.Errorf("repo: list rename proposals: invalid resource type %q", f.ResourceType)
		}
		where = append(where, "resource_type = ?")
		args = append(args, f.ResourceType.String())
	}
	if f.BatchID != "" {
		where = append(where, "batch_id = ?")
		args = append(args, f.BatchID)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rename_proposals WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count rename proposals: %w", err)
	}
	limit, offset := paging(f.Limit, f.Offset)
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+renameColumns+` FROM rename_proposals WHERE `+whereSQL+
			` ORDER BY created_at, rowid LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list rename proposals: %w", err)
	}
	defer rows.Close()
	out := make([]domain.RenameProposal, 0)
	for rows.Next() {
		rp, err := scanRename(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("repo: scan rename proposal: %w", err)
		}
		out = append(out, rp)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list rename proposals: %w", err)
	}
	return out, total, nil
}

// MarkApplied flips a pending proposal to applied and stamps decidedAt.
// The resource merge itself is NOT part of this statement — the renames
// service runs everything inside one DB.Do transaction. A zero RowsAffected
// answers domain.ErrConflict (the proposal was not pending).
func (r *RenameProposalRepo) MarkApplied(ctx context.Context, id string, decidedAt time.Time) error {
	return r.markDecided(ctx, id, domain.RenameStatusApplied, &decidedAt)
}

// MarkRejected flips a pending proposal to rejected (the two resources stay
// independent). A zero RowsAffected answers domain.ErrConflict.
func (r *RenameProposalRepo) MarkRejected(ctx context.Context, id string, decidedAt time.Time) error {
	return r.markDecided(ctx, id, domain.RenameStatusRejected, &decidedAt)
}

// markDecided is the shared UPDATE of the pending→terminal transitions.
func (r *RenameProposalRepo) markDecided(ctx context.Context, id string, status domain.RenameStatus, decidedAt *time.Time) error {
	var decidedArg any
	if decidedAt != nil {
		decidedArg = formatTime(*decidedAt)
	}
	res, err := r.q.ExecContext(ctx,
		`UPDATE rename_proposals SET status = ?, decided_at = ? WHERE id = ? AND status = 'pending'`,
		status.String(), decidedArg, id)
	if err != nil {
		return fmt.Errorf("repo: mark rename proposal %s %s: %w", id, status, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("repo: mark rename proposal %s %s: rows affected: %w", id, status, err)
	} else if n == 0 {
		// Either the proposal does not exist (caller already Get-verified it
		// — the transaction makes a concurrent flip impossible) or it is no
		// longer pending; both are conflicts for the caller's 409.
		return fmt.Errorf("repo: mark rename proposal %s %s: not pending: %w", id, status, domain.ErrConflict)
	}
	return nil
}

// DeletePendingByBatch deletes the batch's PENDING rename proposals and
// returns the number of deleted rows — the rollback delete-chain member
// (mirrors AttributeChangeRepo.DeletePendingByBatch). Applied/rejected
// proposals survive the rollback (the human-decision audit trail is
// decoupled from the import-batch lifecycle); proposals whose old resource
// is deleted by the same rollback are removed by the resource_id ON DELETE
// CASCADE instead.
func (r *RenameProposalRepo) DeletePendingByBatch(ctx context.Context, batchID string) (int64, error) {
	res, err := r.q.ExecContext(ctx,
		`DELETE FROM rename_proposals WHERE batch_id = ? AND status = 'pending'`, batchID)
	if err != nil {
		return 0, fmt.Errorf("repo: delete pending rename proposals of batch %s: %w", batchID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: delete pending rename proposals of batch %s: rows affected: %w", batchID, err)
	}
	return n, nil
}

// CountByStatus returns the number of rename proposals per lifecycle
// status over the whole table (COUNT aggregate — the governance summary's
// pending/decided accounting). The map carries only the statuses actually
// present; an empty table answers an empty map, and the caller zero-fills
// the shape's fixed keys.
func (r *RenameProposalRepo) CountByStatus(ctx context.Context) (map[domain.RenameStatus]int, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT status, COUNT(*) FROM rename_proposals GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("repo: count rename proposals by status: %w", err)
	}
	defer rows.Close()
	counts := make(map[domain.RenameStatus]int)
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, fmt.Errorf("repo: scan count: %w", err)
		}
		counts[domain.RenameStatus(s)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: count rename proposals by status: %w", err)
	}
	return counts, nil
}

// CountPendingByBatch returns the number of pending rename proposals
// attributed to batchID (COUNT aggregate — same scope as
// DeletePendingByBatch, for the rollback response accounting).
func (r *RenameProposalRepo) CountPendingByBatch(ctx context.Context, batchID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rename_proposals WHERE batch_id = ? AND status = 'pending'`,
		batchID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count pending rename proposals of batch %s: %w", batchID, err)
	}
	return n, nil
}
