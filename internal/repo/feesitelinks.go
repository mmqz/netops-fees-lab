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
	"strings"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
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
	// Limit/Offset page the list (#90). Limit <= 0 means unbounded — the
	// internal call sites (the link scan's own bookkeeping) still want
	// everything; the HTTP surface always pages.
	Limit  int
	Offset int
}

// Count returns the number of links matching the filter — the pagination
// envelope's total (#90: the old list returned every row and reported
// len(rows) as "total", so the operator never saw how far the list went).
func (r *FeeSiteLinkRepo) Count(ctx context.Context, f FeeLinkFilter) (int, error) {
	query := `SELECT count(*) FROM fee_site_links WHERE 1=1`
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
	var n int
	if err := r.q.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count fee site links: %w", err)
	}
	return n, nil
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
	if f.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, f.Offset)
	}
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

// PairDecided reports whether a (code, resource) pair already has a
// decided-or-proposed row — pending (no duplicate proposals), dismissed
// (a human-rejected pair never resurrects automatically), or confirmed
// (a human-approved pair never re-proposes either — re-proposing an
// already-confirmed pair would strand a permanent pending row next to
// its own confirmed row, since ConfirmLink 409s on it; found by the
// r19 fees regression, #69).
func (r *FeeSiteLinkRepo) PairDecided(ctx context.Context, code, resourceID string) (bool, error) {
	var one int
	err := r.q.QueryRowContext(ctx,
		`SELECT 1 FROM fee_site_links WHERE fee_site_code = ? AND resource_id = ? AND status IN ('pending','dismissed','confirmed') LIMIT 1`,
		code, resourceID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("repo: pair decided %s/%s: %w", code, resourceID, err)
	}
	return true, nil
}

// DeleteAutoNotIn removes the AUTO links (confirmed or pending) whose
// fee_site_code is NOT in keep — the scan's dropout sweep, pushed down to
// SQL (#65 P1-b: the sweep used to load the whole link table and filter
// in memory, which at link counts proportional to a 10k+ code universe
// is both a large allocation and a slow per-row delete loop). Dismissed
// proposals and human-sourced links are DECISIONS — they outlive the
// data and are never deleted here (the W006 doctrine). An empty keep
// sweeps every auto non-dismissed link (the empty-universe case).
//
// CONTRACT: must run inside ONE DB.Do transaction (the caller's — the
// scan wraps its sweep). The keep-set lands in a per-connection TEMP
// table and the deletion is one NOT IN (SELECT …) statement; a chunked
// NOT IN over the parameter ceiling would be WRONG, not just slow —
// every chunk after the first only protects its own slice and would
// re-delete the codes the earlier chunks kept. The transaction both
// pins the temp table to one connection and keeps the sweep atomic.
// Returns the number of deleted rows.
func (r *FeeSiteLinkRepo) DeleteAutoNotIn(ctx context.Context, keep []string) (int64, error) {
	if len(keep) == 0 {
		res, err := r.q.ExecContext(ctx,
			`DELETE FROM fee_site_links WHERE source = 'auto' AND status != 'dismissed'`)
		if err != nil {
			return 0, fmt.Errorf("repo: sweep auto fee site links: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("repo: sweep auto fee site links: rows affected: %w", err)
		}
		return n, nil
	}
	if _, err := r.q.ExecContext(ctx,
		`CREATE TEMP TABLE IF NOT EXISTS fee_link_keep(code TEXT PRIMARY KEY)`); err != nil {
		return 0, fmt.Errorf("repo: sweep keep table: %w", err)
	}
	if _, err := r.q.ExecContext(ctx, `DELETE FROM fee_link_keep`); err != nil {
		return 0, fmt.Errorf("repo: sweep keep table reset: %w", err)
	}
	const chunk = 400 // host-parameter inserts stay under every SQLite ceiling
	for lo := 0; lo < len(keep); lo += chunk {
		hi := lo + chunk
		if hi > len(keep) {
			hi = len(keep)
		}
		query := `INSERT OR IGNORE INTO fee_link_keep(code) VALUES ` +
			strings.TrimRight(strings.Repeat("(?),", hi-lo), ",")
		args := make([]any, 0, hi-lo)
		for _, c := range keep[lo:hi] {
			args = append(args, c)
		}
		if _, err := r.q.ExecContext(ctx, query, args...); err != nil {
			return 0, fmt.Errorf("repo: sweep keep table fill (chunk %d-%d): %w", lo, hi, err)
		}
	}
	res, err := r.q.ExecContext(ctx,
		`DELETE FROM fee_site_links WHERE source = 'auto' AND status != 'dismissed' AND fee_site_code NOT IN (SELECT code FROM fee_link_keep)`)
	if err != nil {
		return 0, fmt.Errorf("repo: sweep auto fee site links: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: sweep auto fee site links: rows affected: %w", err)
	}
	return n, nil
}
