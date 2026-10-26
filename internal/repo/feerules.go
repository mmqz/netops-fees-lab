package repo

// feerules.go — the fee_risk_rules / fee_risk_rule_audit persistence
// (migration v37, issue #2 phase 1). Every mutation writes its audit row
// — the SERVICE wraps rule-write + audit-write in one DB.Do transaction
// (the ledger is the point, not a side effect).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// FeeRiskRuleRepo persists the L1 configurable rules.
type FeeRiskRuleRepo struct{ q DBTX }

const feeRiskRuleColumns = `id, name, fee_type, metric, aggregation, dimension, comparator, threshold, window_months, severity, description, enabled, created_at, updated_at`

// Create inserts one rule row (enabled=false — the conservative
// observation default; the caller flips it via SetEnabled after review).
// The 'created' audit row is the caller's Do-transaction sibling.
func (r *FeeRiskRuleRepo) Create(ctx context.Context, rule domain.FeeRiskRule) error {
	_, err := r.q.ExecContext(ctx, `INSERT INTO fee_risk_rules
(id, name, fee_type, metric, aggregation, dimension, comparator, threshold, window_months, severity, description, enabled, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,0,?,?)`,
		rule.ID, rule.Name, string(rule.FeeType), rule.Metric, rule.Aggregation, rule.Dimension,
		rule.Comparator, rule.Threshold, rule.WindowMonths, rule.Severity, rule.Description,
		formatTime(rule.CreatedAt), formatTime(rule.UpdatedAt))
	if err != nil {
		return fmt.Errorf("repo: create fee risk rule %s: %w", rule.ID, err)
	}
	return nil
}

// InsertAudit writes one action ledger row (called inside the same
// transaction as the mutation it records).
func (r *FeeRiskRuleRepo) InsertAudit(ctx context.Context, ruleID, action, actor, changes string) error {
	_, err := r.q.ExecContext(ctx, `INSERT INTO fee_risk_rule_audit (rule_id, action, actor, changes, at)
VALUES (?,?,?,?,?)`, ruleID, action, actor, changes, formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("repo: audit fee risk rule %s %s: %w", ruleID, action, err)
	}
	return nil
}

// SetEnabled flips the enabled flag (the caller writes the audit row in
// the same transaction).
func (r *FeeRiskRuleRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	res, err := r.q.ExecContext(ctx, `UPDATE fee_risk_rules SET enabled = ?, updated_at = ? WHERE id = ?`,
		boolInt(enabled), formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("repo: set fee risk rule %s enabled=%v: %w", id, enabled, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("repo: set fee risk rule %s: rows affected: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("repo: fee risk rule %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// NameExists answers whether a rule already carries the given display
// name (the service's pre-check inside the SAME Do-transaction — the
// SQLite UNIQUE stays as the floor, this makes the refusal honest
// 422-operator-facing text instead of a raw constraint 500).
func (r *FeeRiskRuleRepo) NameExists(ctx context.Context, name string) (bool, error) {
	var one int
	if err := r.q.QueryRowContext(ctx, `SELECT 1 FROM fee_risk_rules WHERE name = ?`, name).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("repo: name-exists fee risk rule: %w", err)
	}
	return true, nil
}

// List answers the rules (enabled-only when onlyEnabled).
func (r *FeeRiskRuleRepo) List(ctx context.Context, onlyEnabled bool) ([]domain.FeeRiskRule, error) {
	query := `SELECT ` + feeRiskRuleColumns + ` FROM fee_risk_rules`
	if onlyEnabled {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY created_at, id`
	rows, err := r.q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("repo: list fee risk rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.FeeRiskRule
	for rows.Next() {
		rule, err := scanFeeRiskRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

// GetByID answers one rule or an error wrapping domain.ErrNotFound.
func (r *FeeRiskRuleRepo) GetByID(ctx context.Context, id string) (domain.FeeRiskRule, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+feeRiskRuleColumns+` FROM fee_risk_rules WHERE id = ?`, id)
	rule, err := scanFeeRiskRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.FeeRiskRule{}, fmt.Errorf("repo: fee risk rule %s: %w", id, domain.ErrNotFound)
	}
	return rule, err
}

// AuditFor answers the action ledger of one rule (newest first).
func (r *FeeRiskRuleRepo) AuditFor(ctx context.Context, ruleID string) ([]domain.FeeRiskRuleAudit, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT id, rule_id, action, actor, changes, at
FROM fee_risk_rule_audit WHERE rule_id = ? ORDER BY at DESC, id DESC`, ruleID)
	if err != nil {
		return nil, fmt.Errorf("repo: audit fee risk rule %s: %w", ruleID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.FeeRiskRuleAudit
	for rows.Next() {
		var a domain.FeeRiskRuleAudit
		var at string
		if err := rows.Scan(&a.ID, &a.RuleID, &a.Action, &a.Actor, &a.Changes, &at); err != nil {
			return nil, fmt.Errorf("repo: scan fee risk rule audit: %w", err)
		}
		t, err := parseTime(at)
		if err != nil {
			return nil, err
		}
		a.At = t
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanFeeRiskRule(s scanner) (domain.FeeRiskRule, error) {
	var (
		r         domain.FeeRiskRule
		enabled   int
		createdAt string
		updatedAt string
	)
	err := s.Scan(&r.ID, &r.Name, &r.FeeType, &r.Metric, &r.Aggregation, &r.Dimension,
		&r.Comparator, &r.Threshold, &r.WindowMonths, &r.Severity, &r.Description,
		&enabled, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.FeeRiskRule{}, err
		}
		return domain.FeeRiskRule{}, fmt.Errorf("repo: scan fee risk rule: %w", err)
	}
	r.Enabled = enabled == 1
	if r.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.FeeRiskRule{}, err
	}
	if r.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.FeeRiskRule{}, err
	}
	return r, nil
}
