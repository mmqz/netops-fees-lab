package repo

// extend via new methods; do not rewrite.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
)

// ResourceRepo persists normalized resources. Insert is deliberately the
// only write path (no upsert): the importer decides between insert and
// skip, which is what makes re-import idempotent.
type ResourceRepo struct{ q DBTX }

// resourceColumns is the canonical SELECT column list of resources.
const resourceColumns = `id, resource_type, natural_key, name, attributes, status, first_batch_id, created_at, updated_at`

// scanResource scans one resources row into a domain.Resource.
func scanResource(s scanner) (domain.Resource, error) {
	var (
		res       domain.Resource
		rtype     string
		attrsJSON string
		status    sql.NullString
		createdAt string
		updatedAt string
	)
	err := s.Scan(&res.ID, &rtype, &res.NaturalKey, &res.Name, &attrsJSON, &status,
		&res.FirstBatchID, &createdAt, &updatedAt)
	if err != nil {
		return domain.Resource{}, err
	}
	res.Type = domain.ResourceType(rtype)
	res.Status = stringFromNull(status)
	res.Attributes, err = mapFromJSON(attrsJSON)
	if err != nil {
		return domain.Resource{}, err
	}
	if res.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Resource{}, err
	}
	if res.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.Resource{}, err
	}
	return res, nil
}

// Insert adds a new resource. A conflicting (resource_type, natural_key)
// returns an error wrapping domain.ErrConflict.
func (r *ResourceRepo) Insert(ctx context.Context, res domain.Resource) error {
	attrs, err := mapJSON(res.Attributes)
	if err != nil {
		return err
	}
	_, err = r.q.ExecContext(ctx, `INSERT INTO resources
(id, resource_type, natural_key, name, attributes, status, first_batch_id, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
		res.ID, res.Type.String(), res.NaturalKey, res.Name, attrs,
		nullString(res.Status), res.FirstBatchID, formatTime(res.CreatedAt), formatTime(res.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert resource %s (%s/%s): %w: %w",
				res.ID, res.Type, res.NaturalKey, domain.ErrConflict, err)
		}
		return fmt.Errorf("repo: insert resource %s: %w", res.ID, err)
	}
	return nil
}

// GetByID returns the resource with the given id, or an error wrapping
// domain.ErrNotFound.
func (r *ResourceRepo) GetByID(ctx context.Context, id string) (domain.Resource, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM resources WHERE id = ?`, id)
	res, err := scanResource(row)
	if err != nil {
		return domain.Resource{}, notFound("resource", id, err)
	}
	return res, nil
}

// GetByTypeKey returns the resource of the given type with the given
// natural key, or an error wrapping domain.ErrNotFound. This is the
// idempotency probe used on every data row before insert.
func (r *ResourceRepo) GetByTypeKey(ctx context.Context, resourceType domain.ResourceType, naturalKey string) (domain.Resource, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+resourceColumns+` FROM resources WHERE resource_type = ? AND natural_key = ?`,
		resourceType.String(), naturalKey)
	res, err := scanResource(row)
	if err != nil {
		return domain.Resource{}, notFound("resource", resourceType.String()+"/"+naturalKey, err)
	}
	return res, nil
}

// ResourceSearchFilter selects resources. Empty fields mean "no filter".
// Q is a case-insensitive contains match over natural_key, name and the
// attributes JSON text (LIKE wildcards in Q are escaped, so they match
// literally). Limit <= 0 means unlimited; offset < 0 means 0.
type ResourceSearchFilter struct {
	Type   domain.ResourceType
	Q      string
	Status string
	Limit  int
	Offset int
}

// Search returns resources matching f plus the total number of matches
// (ignoring limit/offset). Results are ordered by (resource_type,
// natural_key) for deterministic pagination.
func (r *ResourceRepo) Search(ctx context.Context, f ResourceSearchFilter) ([]domain.Resource, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.Type != "" {
		if !f.Type.IsValid() {
			return nil, 0, fmt.Errorf("repo: search resources: invalid type %q", f.Type)
		}
		where = append(where, "resource_type = ?")
		args = append(args, f.Type.String())
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Q != "" {
		pat := likePattern(f.Q)
		where = append(where, `(lower(natural_key) LIKE ? ESCAPE '\'
OR lower(name) LIKE ? ESCAPE '\'
OR lower(attributes) LIKE ? ESCAPE '\')`)
		args = append(args, pat, pat, pat)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM resources WHERE `+whereSQL, args...).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count resources: %w", err)
	}
	limit, offset := paging(f.Limit, f.Offset)
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+resourceColumns+` FROM resources WHERE `+whereSQL+` ORDER BY resource_type, natural_key LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: search resources: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Resource, 0)
	for rows.Next() {
		res, err := scanResource(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("repo: scan resource: %w", err)
		}
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: search resources: %w", err)
	}
	return out, total, nil
}

// CountByType returns the number of resources per resource type.
func (r *ResourceRepo) CountByType(ctx context.Context) (map[domain.ResourceType]int, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT resource_type, COUNT(*) FROM resources GROUP BY resource_type`)
	if err != nil {
		return nil, fmt.Errorf("repo: count resources by type: %w", err)
	}
	defer rows.Close()
	counts := make(map[domain.ResourceType]int)
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, fmt.Errorf("repo: scan count: %w", err)
		}
		counts[domain.ResourceType(t)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: count resources by type: %w", err)
	}
	return counts, nil
}

// CountByStatus returns the number of resources per status value.
// Resources without a status (NULL) are not included.
func (r *ResourceRepo) CountByStatus(ctx context.Context) (map[string]int, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT status, COUNT(*) FROM resources WHERE status IS NOT NULL GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("repo: count resources by status: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, fmt.Errorf("repo: scan count: %w", err)
		}
		counts[s] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: count resources by status: %w", err)
	}
	return counts, nil
}

// ListByBatch returns all resources whose first_batch_id equals batchID,
// ordered by (resource_type, natural_key).
func (r *ResourceRepo) ListByBatch(ctx context.Context, batchID string) ([]domain.Resource, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+resourceColumns+` FROM resources WHERE first_batch_id = ? ORDER BY resource_type, natural_key`,
		batchID)
	if err != nil {
		return nil, fmt.Errorf("repo: list resources by batch %s: %w", batchID, err)
	}
	defer rows.Close()
	out := make([]domain.Resource, 0)
	for rows.Next() {
		res, err := scanResource(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan resource: %w", err)
		}
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list resources by batch %s: %w", batchID, err)
	}
	return out, nil
}

// CountByBatch returns the number of resources whose first_batch_id equals
// batchID (COUNT aggregate — same scope as ListByBatch). The rollback
// route's delete-response accounting uses it instead of a full List.
func (r *ResourceRepo) CountByBatch(ctx context.Context, batchID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM resources WHERE first_batch_id = ?`, batchID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count resources of batch %s: %w", batchID, err)
	}
	return n, nil
}

// DeleteByBatch deletes all resources created by batchID and returns the
// number of deleted rows. Relations referencing those resources are removed
// by the foreign key ON DELETE CASCADE; relations owned by the batch but
// pointing at other batches' resources must be deleted separately via
// RelationRepo.DeleteByBatch.
func (r *ResourceRepo) DeleteByBatch(ctx context.Context, batchID string) (int64, error) {
	res, err := r.q.ExecContext(ctx, `DELETE FROM resources WHERE first_batch_id = ?`, batchID)
	if err != nil {
		return 0, fmt.Errorf("repo: delete resources of batch %s: %w", batchID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: delete resources of batch %s: rows affected: %w", batchID, err)
	}
	return n, nil
}
