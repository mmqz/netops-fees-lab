package repo

// feecodealiases.go — the fee_site_code_aliases persistence (migration
// v39, issue #12): the canonical↔alias bridge that lets the
// cross-dataset reconcile merge the two systems' site-code universes.
// Append-only (no update channel — the rules precedent); removal is a
// separate admin-tier act.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// FeeSiteCodeAliasRepo persists the mapping rows.
type FeeSiteCodeAliasRepo struct{ q DBTX }

// Insert adds one mapping row (UNIQUE (canonical, alias, source) is the
// DB floor; the service's pre-check rides the same transaction for the
// honest 422 text).
func (r *FeeSiteCodeAliasRepo) Insert(ctx context.Context, a domain.SiteCodeAlias) error {
	_, err := r.q.ExecContext(ctx, `INSERT INTO fee_site_code_aliases
(id, canonical, alias, source, confidence, created_by, created_at)
VALUES (?,?,?,?,?,?,?)`,
		a.ID, a.Canonical, a.Alias, string(a.Source), a.Confidence,
		a.CreatedBy, formatTime(a.CreatedAt))
	if err != nil {
		return fmt.Errorf("repo: insert site code alias %s: %w", a.ID, err)
	}
	return nil
}

// Exists answers whether the exact triple already exists (the honest
// duplicate refusal, rule-style).
func (r *FeeSiteCodeAliasRepo) Exists(ctx context.Context, canonical, alias string, source domain.FeeType) (string, error) {
	var id string
	err := r.q.QueryRowContext(ctx,
		`SELECT id FROM fee_site_code_aliases WHERE canonical = ? AND alias = ? AND source = ?`,
		canonical, alias, string(source)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("repo: alias exists probe: %w", err)
	}
	return id, nil
}

// List answers every mapping row (canonical first, then alias — the
// reconcile bridge's whole registry; it is evidence, read-open by
// nature).
func (r *FeeSiteCodeAliasRepo) List(ctx context.Context) ([]domain.SiteCodeAlias, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT id, canonical, alias, source, confidence, created_by, created_at
FROM fee_site_code_aliases ORDER BY canonical, alias, source`)
	if err != nil {
		return nil, fmt.Errorf("repo: list site code aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.SiteCodeAlias
	for rows.Next() {
		var a domain.SiteCodeAlias
		var createdAt sql.NullString
		if err := rows.Scan(&a.ID, &a.Canonical, &a.Alias, &a.Source, &a.Confidence,
			&a.CreatedBy, &createdAt); err != nil {
			return nil, err
		}
		t, err := parseTime(createdAt.String)
		if err != nil {
			return nil, err
		}
		a.CreatedAt = t
		out = append(out, a)
	}
	return out, rows.Err()
}

// Delete removes one mapping row (the admin-tier governance act — a
// wrong mapping must not poison the reconcile forever).
func (r *FeeSiteCodeAliasRepo) Delete(ctx context.Context, id string) error {
	res, err := r.q.ExecContext(ctx, `DELETE FROM fee_site_code_aliases WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("repo: delete site code alias %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("repo: delete site code alias %s: rows affected: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("repo: site code alias %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// AliasMap answers the alias→canonical redirect table of the given
// source universe (the reconcile's merge input; sources other than the
// asked one are ignored — each universe carries its own aliases).
func (r *FeeSiteCodeAliasRepo) AliasMap(ctx context.Context, source domain.FeeType) (map[string]string, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT alias, canonical FROM fee_site_code_aliases WHERE source = ?`, string(source))
	if err != nil {
		return nil, fmt.Errorf("repo: alias map %s: %w", source, err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var alias, canonical string
		if err := rows.Scan(&alias, &canonical); err != nil {
			return nil, err
		}
		// First row wins on a double redirect — the registry is unique
		// per (canonical, alias, source) but two rows may claim one
		// alias for different canonicals; that is a data conflict the
		// reconcile surfaces as-is (never silently resolved).
		if _, seen := out[alias]; !seen {
			out[alias] = canonical
		}
	}
	return out, rows.Err()
}

// unusedStrings keeps strings imported for the future filter helpers.
var _ = strings.TrimSpace
