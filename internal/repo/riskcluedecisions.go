package repo

// RiskClueDecisionRepo persists the human decisions on computed risk
// clues (migration v33, #119). The clue itself is recomputed from the
// Parquet analysis layer on every risk run — the decision row IS the
// only record. Writes run in the caller's DB.Do transaction: Decide (the
// UPSERT of the human channel — verify or dismiss, re-deciding flips the
// status) and reads via GetByKey / ListForKeys / List / Count.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// RiskClueDecisionRepo persists risk_clue_decisions rows.
type RiskClueDecisionRepo struct{ q DBTX }

// clueDecisionColumns is the canonical SELECT column list.
const clueDecisionColumns = `id, scenario, fee_type, site_code, year, month, status, note, decided_at`

// scanClueDecision scans one risk_clue_decisions row.
func scanClueDecision(s scanner) (domain.RiskClueDecision, error) {
	var (
		d         domain.RiskClueDecision
		feeType   string
		status    string
		decidedAt string
	)
	err := s.Scan(&d.ID, &d.Scenario, &feeType, &d.SiteCode, &d.Year, &d.Month,
		&status, &d.Note, &decidedAt)
	if err != nil {
		return domain.RiskClueDecision{}, err
	}
	d.FeeType = domain.FeeType(feeType)
	d.Status = domain.ClueDecisionStatus(status)
	if d.DecidedAt, err = time.Parse(time.RFC3339, decidedAt); err != nil {
		return domain.RiskClueDecision{}, fmt.Errorf("repo: 解析线索裁定时间 %q: %w", decidedAt, err)
	}
	return d, nil
}

// ClueDecisionFilter is the GET /api/fees/risk-clue-decisions filter set:
// every field optional; empty = the whole ledger. Limit follows the
// PROJECT_BRIEF §9 bounds the caller enforces.
type ClueDecisionFilter struct {
	Scenario string
	FeeType  string
	Status   string
	SiteCode string
	Year     int
	Limit    int
	Offset   int
}

// Decide UPSERTS one decision on the clue identity (scenario, fee type,
// site, period): the FIRST decision inserts, a re-decision flips the
// status and refreshes decided_at (the human may change their mind as
// evidence evolves — the row keeps the LATEST decision). The unique key
// is the migration v33 UNIQUE constraint; the CHECK constraints on
// scenario/fee_type/status are the last-line guards (the service
// validates first).
func (r *RiskClueDecisionRepo) Decide(ctx context.Context, d domain.RiskClueDecision) (domain.RiskClueDecision, error) {
	if d.Scenario == "" || d.FeeType == "" || d.SiteCode == "" {
		return domain.RiskClueDecision{}, errors.New("repo: 线索裁定缺身份字段（scenario/feeType/siteCode）")
	}
	id := d.ID
	if id == "" {
		id = "rcd_" + uuid.NewString()
	}
	decidedAt := d.DecidedAt
	if decidedAt.IsZero() {
		decidedAt = time.Now().UTC()
	}
	if _, err := r.q.ExecContext(ctx, `
INSERT INTO risk_clue_decisions (id, scenario, fee_type, site_code, year, month, status, note, decided_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (scenario, fee_type, site_code, year, month) DO UPDATE SET
  status = excluded.status,
  note = excluded.note,
  decided_at = excluded.decided_at`,
		id, d.Scenario, string(d.FeeType), d.SiteCode, d.Year, d.Month,
		string(d.Status), d.Note, decidedAt.Format(time.RFC3339)); err != nil {
		return domain.RiskClueDecision{}, fmt.Errorf("repo: upsert 线索裁定: %w", err)
	}
	return r.GetByKey(ctx, d.Scenario, d.FeeType, d.SiteCode, d.Year, d.Month)
}

// GetByKey reads one decision by the clue identity.
func (r *RiskClueDecisionRepo) GetByKey(ctx context.Context, scenario string, feeType domain.FeeType, siteCode string, year, month int) (domain.RiskClueDecision, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+clueDecisionColumns+` FROM risk_clue_decisions
WHERE scenario = ? AND fee_type = ? AND site_code = ? AND year = ? AND month = ?`,
		scenario, string(feeType), siteCode, year, month)
	d, err := scanClueDecision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.RiskClueDecision{}, fmt.Errorf("repo: %w：%s/%s/%s/%04d-%02d", domain.ErrNotFound, scenario, feeType, siteCode, year, month)
	}
	if err != nil {
		return domain.RiskClueDecision{}, fmt.Errorf("repo: 读线索裁定: %w", err)
	}
	return d, nil
}

// ClueKey addresses one clue identity for the batch read.
type ClueKey struct {
	Scenario string
	FeeType  domain.FeeType
	SiteCode string
	Year     int
	Month    int
}

// ListForKeys batch-reads the decisions of a computed clue set (one
// IN-clause query — the finishClues join; keys without a decision are
// simply absent from the map). The query de-duplicates the key list.
func (r *RiskClueDecisionRepo) ListForKeys(ctx context.Context, keys []ClueKey) (map[ClueKey]domain.RiskClueDecision, error) {
	out := make(map[ClueKey]domain.RiskClueDecision, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	seen := make(map[ClueKey]struct{}, len(keys))
	dedup := make([]ClueKey, 0, len(keys))
	for _, k := range keys {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			dedup = append(dedup, k)
		}
	}
	// chunked OR-groups over the five identity columns (the IN tuple
	// form is capped by SQLITE_MAX_VARIABLE_NUMBER on big clue sets)
	const chunk = 100
	for start := 0; start < len(dedup); start += chunk {
		end := start + chunk
		if end > len(dedup) {
			end = len(dedup)
		}
		part := dedup[start:end]
		args := make([]any, 0, len(part)*5)
		conds := make([]string, 0, len(part))
		for _, k := range part {
			conds = append(conds, "(scenario = ? AND fee_type = ? AND site_code = ? AND year = ? AND month = ?)")
			args = append(args, k.Scenario, string(k.FeeType), k.SiteCode, k.Year, k.Month)
		}
		rows, err := r.q.QueryContext(ctx, `SELECT `+clueDecisionColumns+` FROM risk_clue_decisions WHERE `+
			strings.Join(conds, " OR "), args...)
		if err != nil {
			return nil, fmt.Errorf("repo: 批读线索裁定: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			d, err := scanClueDecision(rows)
			if err != nil {
				return nil, fmt.Errorf("repo: 扫描线索裁定行: %w", err)
			}
			k := ClueKey{Scenario: d.Scenario, FeeType: d.FeeType, SiteCode: d.SiteCode, Year: d.Year, Month: d.Month}
			out[k] = d
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("repo: 批读线索裁定: %w", err)
		}
	}
	return out, nil
}

// List reads the decision ledger (newest decided first, the audit view).
func (r *RiskClueDecisionRepo) List(ctx context.Context, f ClueDecisionFilter) ([]domain.RiskClueDecision, error) {
	query := `SELECT ` + clueDecisionColumns + ` FROM risk_clue_decisions WHERE 1=1`
	args := []any{}
	if f.Scenario != "" {
		query += ` AND scenario = ?`
		args = append(args, f.Scenario)
	}
	if f.FeeType != "" {
		query += ` AND fee_type = ?`
		args = append(args, f.FeeType)
	}
	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.SiteCode != "" {
		query += ` AND site_code = ?`
		args = append(args, f.SiteCode)
	}
	if f.Year != 0 {
		query += ` AND year = ?`
		args = append(args, f.Year)
	}
	query += ` ORDER BY decided_at DESC, id DESC`
	if f.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, f.Offset)
	}
	rows, err := r.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: list 线索裁定: %w", err)
	}
	defer rows.Close()
	out := []domain.RiskClueDecision{}
	for rows.Next() {
		d, err := scanClueDecision(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: 扫描线索裁定行: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list 线索裁定: %w", err)
	}
	return out, nil
}

// Count counts the ledger under the same filter (the pagination total).
func (r *RiskClueDecisionRepo) Count(ctx context.Context, f ClueDecisionFilter) (int, error) {
	query := `SELECT COUNT(*) FROM risk_clue_decisions WHERE 1=1`
	args := []any{}
	if f.Scenario != "" {
		query += ` AND scenario = ?`
		args = append(args, f.Scenario)
	}
	if f.FeeType != "" {
		query += ` AND fee_type = ?`
		args = append(args, f.FeeType)
	}
	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.SiteCode != "" {
		query += ` AND site_code = ?`
		args = append(args, f.SiteCode)
	}
	if f.Year != 0 {
		query += ` AND year = ?`
		args = append(args, f.Year)
	}
	var n int
	if err := r.q.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count 线索裁定: %w", err)
	}
	return n, nil
}
