package repo

// templates.go — the mapping template storage of the unattended import
// channel (W24, migration v19, §12-B7 方案 A, issue #28). All SQL of the
// template surface lives here; the fingerprint canonicalization lives in
// internal/mapping (pure), the unattended flow lives in internal/importer.

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// TemplateRepo persists mapping templates.
type TemplateRepo struct{ q DBTX }

// templateColumns is the canonical SELECT column list of mapping_templates.
const templateColumns = `template_id, source_name, header_fingerprint, resource_type, mapping_json,
sheet_name, confirmed_at, confirmed_batch`

// scanTemplate scans one mapping_templates row into a domain.MappingTemplate.
func scanTemplate(s scanner) (domain.MappingTemplate, error) {
	var (
		t               domain.MappingTemplate
		resourceType    string
		confirmedAtStr  string
		confirmedBatchN sql.NullString
	)
	if err := s.Scan(&t.TemplateID, &t.SourceName, &t.HeaderFingerprint, &resourceType, &t.MappingJSON,
		&t.SheetName, &confirmedAtStr, &confirmedBatchN); err != nil {
		return domain.MappingTemplate{}, err
	}
	t.ResourceType = domain.ResourceType(resourceType)
	confirmedAt, err := parseTime(confirmedAtStr)
	if err != nil {
		return domain.MappingTemplate{}, err
	}
	t.ConfirmedAt = confirmedAt
	t.ConfirmedBatch = stringFromNull(confirmedBatchN)
	return t, nil
}

// UpsertTemplate stores t idempotently by its identity
// (header_fingerprint, resource_type): a NEW identity INSERTS (created =
// true); a re-confirmation of the same identity UPDATES mapping_json,
// source_name, sheet_name, confirmed_at and confirmed_batch in place
// (created = false — the template row is replaced, not duplicated). The
// template_id of an updated row stays the ORIGINAL id (the upsert does not
// churn ids of an identity that consumers may already reference).
//
// The created-vs-updated decision is one SELECT EXISTS followed by the
// INSERT … ON CONFLICT DO UPDATE: the exists probe is informational only
// (the ON CONFLICT clause is the correctness anchor), so a concurrent
// capture of the same identity cannot corrupt anything — worst case both
// calls report created=false.
func (r *TemplateRepo) UpsertTemplate(ctx context.Context, t domain.MappingTemplate) (bool, error) {
	var exists int
	if err := r.q.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM mapping_templates WHERE header_fingerprint = ? AND resource_type = ?)`,
		t.HeaderFingerprint, string(t.ResourceType)).Scan(&exists); err != nil {
		return false, fmt.Errorf("repo: upsert template probe %s: %w", t.TemplateID, err)
	}
	if _, err := r.q.ExecContext(ctx,
		`INSERT INTO mapping_templates
(template_id, source_name, header_fingerprint, resource_type, mapping_json, sheet_name, confirmed_at, confirmed_batch)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT (header_fingerprint, resource_type) DO UPDATE SET
  source_name     = excluded.source_name,
  mapping_json    = excluded.mapping_json,
  sheet_name      = excluded.sheet_name,
  confirmed_at    = excluded.confirmed_at,
  confirmed_batch = excluded.confirmed_batch`,
		t.TemplateID, t.SourceName, t.HeaderFingerprint, string(t.ResourceType), t.MappingJSON,
		t.SheetName, formatTime(t.ConfirmedAt), nullString(t.ConfirmedBatch)); err != nil {
		return false, fmt.Errorf("repo: upsert template %s: %w", t.TemplateID, err)
	}
	return exists == 0, nil
}

// FindByFingerprint returns the template whose header fingerprint matches
// fp, or an error wrapping domain.ErrNotFound when none exists. When
// several templates share the fingerprint (the same sheet shape confirmed
// for DIFFERENT target types — possible because the identity also includes
// resource_type), the tie-break is the most recent confirmed_at, then
// template_id: deterministic across calls.
func (r *TemplateRepo) FindByFingerprint(ctx context.Context, fp string) (domain.MappingTemplate, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+templateColumns+` FROM mapping_templates
WHERE header_fingerprint = ?
ORDER BY confirmed_at DESC, template_id
LIMIT 1`, fp)
	t, err := scanTemplate(row)
	if err != nil {
		return domain.MappingTemplate{}, notFound("mapping template fingerprint", fp, err)
	}
	return t, nil
}

// GetTemplate loads one template by id; unknown id answers an error
// wrapping domain.ErrNotFound.
func (r *TemplateRepo) GetTemplate(ctx context.Context, id string) (domain.MappingTemplate, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+templateColumns+` FROM mapping_templates WHERE template_id = ?`, id)
	t, err := scanTemplate(row)
	if err != nil {
		return domain.MappingTemplate{}, notFound("mapping template", id, err)
	}
	return t, nil
}

// ListTemplates returns templates newest-first (confirmed_at DESC,
// template_id tie-break — deterministic), with the total count ignoring
// limit/offset. limit <= 0 means unlimited; offset < 0 means 0.
func (r *TemplateRepo) ListTemplates(ctx context.Context, limit, offset int) ([]domain.MappingTemplate, int, error) {
	var total int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM mapping_templates`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count mapping templates: %w", err)
	}
	limit, offset = paging(limit, offset)
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+templateColumns+` FROM mapping_templates
ORDER BY confirmed_at DESC, template_id
LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list mapping templates: %w", err)
	}
	defer rows.Close()
	out := make([]domain.MappingTemplate, 0)
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("repo: scan mapping template: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list mapping templates: %w", err)
	}
	return out, total, nil
}

// DeleteTemplate removes one template by id; unknown id answers an error
// wrapping domain.ErrNotFound. Deleting a template NEVER touches the batch
// that produced it (templates are confirmed artifacts, not batch data).
func (r *TemplateRepo) DeleteTemplate(ctx context.Context, id string) error {
	res, err := r.q.ExecContext(ctx, `DELETE FROM mapping_templates WHERE template_id = ?`, id)
	if err != nil {
		return fmt.Errorf("repo: delete mapping template %s: %w", id, err)
	}
	return requireUpdated(res, "mapping template", id)
}
