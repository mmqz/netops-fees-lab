package repo

// FeeSiteLinkRepo persists fee-site links (migration v30, #63/#58 stage
// 3). Writes run in the caller's DB.Do transaction: InsertProposal /
// UpsertAutoLink (the matcher's scan transaction) and Confirm / Dismiss
// (the human confirmation channel, guarded single-statement flips like
// the rename proposals repo).

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// FeeSiteLinkRepo persists fee_site_links rows.
type FeeSiteLinkRepo struct{ q DBTX }

// feeLinkColumns is the canonical SELECT column list of fee_site_links.
const feeLinkColumns = `id, fee_site_code, resource_id, resource_type, resource_key, grade, rule, similarity, status, source, batch_id, created_at, decided_at`

// scanFeeLink scans one fee_site_links row.
func scanFeeLink(s scanner) (domain.FeeSiteLink, error) {
	var (
		l          domain.FeeSiteLink
		rtype      string
		grade      string
		rule       sql.NullString
		similarity sql.NullFloat64
		status     string
		source     string
		batchID    sql.NullString
		createdAt  string
		decidedAt  sql.NullString
	)
	err := s.Scan(&l.ID, &l.FeeSiteCode, &l.ResourceID, &rtype, &l.ResourceKey, &grade,
		&rule, &similarity, &status, &source, &batchID, &createdAt, &decidedAt)
	if err != nil {
		return domain.FeeSiteLink{}, err
	}
	l.ResourceType = domain.ResourceType(rtype)
	l.Grade = domain.LinkGrade(grade)
	l.Status = domain.LinkStatus(status)
	l.Source = domain.LinkSource(source)
	if rule.Valid {
		l.Rule = rule.String
	}
	if similarity.Valid {
		l.Similarity = similarity.Float64
	}
	if batchID.Valid {
		l.BatchID = batchID.String
	}
	if l.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.FeeSiteLink{}, err
	}
	if decidedAt.Valid {
		t, err := parseTime(decidedAt.String)
		if err != nil {
			return domain.FeeSiteLink{}, err
		}
		l.DecidedAt = &t
	}
	return l, nil
}

// InsertProposal adds one link row. Exact/rule links arrive confirmed
// (source auto); fuzzy links arrive pending (a proposal). A pending
// proposal for the same (code, resource) pair may repeat ONLY when the
// existing one was dismissed (the unique live index covers confirmed
// rows only — a dismissed pair is history, a fresh proposal is welcome).
func (r *FeeSiteLinkRepo) InsertProposal(ctx context.Context, l domain.FeeSiteLink) error {
	_, err := r.q.ExecContext(ctx, `INSERT INTO fee_site_links
(id, fee_site_code, resource_id, resource_type, resource_key, grade, rule, similarity, status, source, batch_id, created_at, decided_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,NULL)`,
		l.ID, l.FeeSiteCode, l.ResourceID, l.ResourceType.String(), l.ResourceKey, string(l.Grade),
		nullString(l.Rule), nullableFloat(l.Similarity), string(l.Status), string(l.Source),
		nullString(l.BatchID), formatTime(l.CreatedAt))
	if err != nil {
		return fmt.Errorf("repo: insert fee site link %s: %w", l.ID, err)
	}
	return nil
}

// UpsertAutoLink writes the deterministic matcher's verdict for one fee
// code: an exact/rule match lands as the confirmed link (replacing any
// AUTO confirmed link of the same code — the scan is idempotent and the
// matcher may refine its pick as resources change; a HUMAN link is never
// overwritten). Idempotency: same (code, resource, grade, rule) → no-op.
func (r *FeeSiteLinkRepo) UpsertAutoLink(ctx context.Context, l domain.FeeSiteLink) error {
	// A human-confirmed link of this code wins — the auto scan leaves it.
	var human string
	err := r.q.QueryRowContext(ctx,
		`SELECT id FROM fee_site_links WHERE fee_site_code = ? AND status = 'confirmed' AND source = 'human'`, l.FeeSiteCode).
		Scan(&human)
	if err == nil {
		return nil // human decision outranks the deterministic scan
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("repo: upsert fee site link: %w", err)
	}
	// Delete the previous AUTO confirmed link of this code (a changed
	// pick or grade refinement — the scan is the authority for auto
	// links), then insert the fresh one.
	if _, err := r.q.ExecContext(ctx,
		`DELETE FROM fee_site_links WHERE fee_site_code = ? AND status = 'confirmed' AND source = 'auto'`, l.FeeSiteCode); err != nil {
		return fmt.Errorf("repo: replace auto fee site link: %w", err)
	}
	return r.InsertProposal(ctx, l)
}

// Confirm flips a pending proposal to confirmed (source human). The
// partial unique index fires when another confirmed link already holds
// the code — the caller surfaces the conflict.
func (r *FeeSiteLinkRepo) Confirm(ctx context.Context, id string, decidedAt time.Time) error {
	if err := r.requirePending(ctx, "确认关联", id); err != nil {
		return err
	}
	res, err := r.q.ExecContext(ctx,
		`UPDATE fee_site_links SET status = 'confirmed', source = 'human', decided_at = ? WHERE id = ? AND status = 'pending'`,
		formatTime(decidedAt), id)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: 确认关联 %s: 该站址编码已有 confirmed 关联（先处理旧的）: %w", id, domain.ErrConflict)
		}
		return fmt.Errorf("repo: confirm fee site link %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("repo: confirm fee site link %s: rows affected: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("repo: confirm fee site link %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// requirePending answers ErrConflict for a non-pending row (message
// naming the current status) and ErrNotFound for an unknown id — the
// confirmation flips only ever apply to pending proposals.
func (r *FeeSiteLinkRepo) requirePending(ctx context.Context, op, id string) error {
	var status string
	err := r.q.QueryRowContext(ctx, `SELECT status FROM fee_site_links WHERE id = ?`, id).Scan(&status)
	if err == sql.ErrNoRows {
		return fmt.Errorf("repo: %s %s: %w", op, id, domain.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("repo: %s %s: %w", op, id, err)
	}
	if status != string(domain.LinkStatusPending) {
		return fmt.Errorf("repo: %s %s: 状态 %s，仅 pending 可操作: %w", op, id, status, domain.ErrConflict)
	}
	return nil
}

// Dismiss flips a pending proposal to dismissed (audit kept, the code
// stays free for a fresh candidate).
func (r *FeeSiteLinkRepo) Dismiss(ctx context.Context, id string, decidedAt time.Time) error {
	if err := r.requirePending(ctx, "驳回关联", id); err != nil {
		return err
	}
	res, err := r.q.ExecContext(ctx,
		`UPDATE fee_site_links SET status = 'dismissed', decided_at = ? WHERE id = ? AND status = 'pending'`,
		formatTime(decidedAt), id)
	if err != nil {
		return fmt.Errorf("repo: dismiss fee site link %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("repo: dismiss fee site link %s: rows affected: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("repo: dismiss fee site link %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// GetByID fetches one link, or domain.ErrNotFound.
func (r *FeeSiteLinkRepo) GetByID(ctx context.Context, id string) (domain.FeeSiteLink, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+feeLinkColumns+` FROM fee_site_links WHERE id = ?`, id)
	l, err := scanFeeLink(row)
	if err != nil {
		return domain.FeeSiteLink{}, notFound("fee site link", id, err)
	}
	return l, nil
}

// FeeLinkFilter selects links; empty fields mean "no filter". The
// resource filter serves the resource→fees direction; the code filter
// serves fee→resource.
type FeeLinkFilter struct {
	Status   domain.LinkStatus
	Grade    domain.LinkGrade
	Code     string
	Resource string
}

// List returns links ordered newest first.
func (r *FeeSiteLinkRepo) List(ctx context.Context, f FeeLinkFilter) ([]domain.FeeSiteLink, error) {
	query := `SELECT ` + feeLinkColumns + ` FROM fee_site_links WHERE 1=1`
	args := []any{}
	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, string(f.Status))
	}
	if f.Grade != "" {
		query += ` AND grade = ?`
		args = append(args, string(f.Grade))
	}
	if f.Code != "" {
		query += ` AND fee_site_code = ?`
		args = append(args, f.Code)
	}
	if f.Resource != "" {
		query += ` AND resource_id = ?`
		args = append(args, f.Resource)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: list fee site links: %w", err)
	}
	defer rows.Close()
	var out []domain.FeeSiteLink
	for rows.Next() {
		l, err := scanFeeLink(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan fee site link: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ConfirmedByCode returns the confirmed link of one fee code (ErrNotFound
// when none — the query join's fast path).
func (r *FeeSiteLinkRepo) ConfirmedByCode(ctx context.Context, code string) (domain.FeeSiteLink, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+feeLinkColumns+` FROM fee_site_links WHERE fee_site_code = ? AND status = 'confirmed'`, code)
	l, err := scanFeeLink(row)
	if err != nil {
		return domain.FeeSiteLink{}, notFound("confirmed fee site link", code, err)
	}
	return l, nil
}

// nullableFloat maps 0 to NULL (similarity only exists on fuzzy links).
func nullableFloat(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}

// DeleteByID removes one link row (the scan's dropout sweep — auto links
// whose code left the live universe).
func (r *FeeSiteLinkRepo) DeleteByID(ctx context.Context, id string) (bool, error) {
	res, err := r.q.ExecContext(ctx, `DELETE FROM fee_site_links WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("repo: delete fee site link %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("repo: delete fee site link %s: rows affected: %w", id, err)
	}
	return n > 0, nil
}

// PairDecided reports whether a (code, resource) pair already has a
// pending proposal or a dismissed rejection — the scan's fuzzy step
// skips both (no duplicate proposals; a human-rejected pair never
// resurrects automatically).
func (r *FeeSiteLinkRepo) PairDecided(ctx context.Context, code, resourceID string) (bool, error) {
	var one int
	err := r.q.QueryRowContext(ctx,
		`SELECT 1 FROM fee_site_links WHERE fee_site_code = ? AND resource_id = ? AND status IN ('pending','dismissed') LIMIT 1`,
		code, resourceID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("repo: pair decided %s/%s: %w", code, resourceID, err)
	}
	return true, nil
}
