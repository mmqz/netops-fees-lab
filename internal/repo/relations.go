package repo

// extend via new methods; do not rewrite.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
)

// RelationRepo persists typed relations between resources.
type RelationRepo struct{ q DBTX }

// relationColumns is the canonical SELECT column list of relations.
const relationColumns = `id, relation_type, from_resource_id, to_resource_id, attributes, first_batch_id, created_at`

// scanRelation scans one relations row into a domain.Relation.
func scanRelation(s scanner) (domain.Relation, error) {
	var (
		rel       domain.Relation
		rtype     string
		attrsJSON string
		createdAt string
	)
	err := s.Scan(&rel.ID, &rtype, &rel.FromResourceID, &rel.ToResourceID, &attrsJSON,
		&rel.FirstBatchID, &createdAt)
	if err != nil {
		return domain.Relation{}, err
	}
	rel.Type = domain.RelationType(rtype)
	rel.Attributes, err = mapFromJSON(attrsJSON)
	if err != nil {
		return domain.Relation{}, err
	}
	if rel.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Relation{}, err
	}
	return rel, nil
}

// Insert adds a new relation. A conflicting (from_resource_id,
// to_resource_id, relation_type) returns an error wrapping
// domain.ErrConflict.
func (r *RelationRepo) Insert(ctx context.Context, rel domain.Relation) error {
	attrs, err := mapJSON(rel.Attributes)
	if err != nil {
		return err
	}
	_, err = r.q.ExecContext(ctx, `INSERT INTO relations
(id, relation_type, from_resource_id, to_resource_id, attributes, first_batch_id, created_at)
VALUES (?,?,?,?,?,?,?)`,
		rel.ID, rel.Type.String(), rel.FromResourceID, rel.ToResourceID, attrs,
		rel.FirstBatchID, formatTime(rel.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert relation %s (%s %s->%s): %w: %w",
				rel.ID, rel.Type, rel.FromResourceID, rel.ToResourceID, domain.ErrConflict, err)
		}
		return fmt.Errorf("repo: insert relation %s: %w", rel.ID, err)
	}
	return nil
}

// ListByResource returns all relations in which resourceID participates as
// either the from or the to endpoint, ordered by (relation_type, id). The
// caller can tell the direction by comparing FromResourceID/ToResourceID
// with the queried id.
func (r *RelationRepo) ListByResource(ctx context.Context, resourceID string) ([]domain.Relation, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+relationColumns+` FROM relations
WHERE from_resource_id = ? OR to_resource_id = ?
ORDER BY relation_type, id`,
		resourceID, resourceID)
	if err != nil {
		return nil, fmt.Errorf("repo: list relations of resource %s: %w", resourceID, err)
	}
	defer rows.Close()
	out := make([]domain.Relation, 0)
	for rows.Next() {
		rel, err := scanRelation(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan relation: %w", err)
		}
		out = append(out, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list relations of resource %s: %w", resourceID, err)
	}
	return out, nil
}

// DeleteByBatch deletes all relations created by batchID and returns the
// number of deleted rows.
func (r *RelationRepo) DeleteByBatch(ctx context.Context, batchID string) (int64, error) {
	res, err := r.q.ExecContext(ctx, `DELETE FROM relations WHERE first_batch_id = ?`, batchID)
	if err != nil {
		return 0, fmt.Errorf("repo: delete relations of batch %s: %w", batchID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: delete relations of batch %s: rows affected: %w", batchID, err)
	}
	return n, nil
}

// ExistsFromOtherBatch reports whether any relation NOT created by batchID
// has one of resourceIDs as an endpoint. This is the rollback-in-use
// check: a batch cannot be rolled back while entities it created are
// referenced by entities of other batches.
func (r *RelationRepo) ExistsFromOtherBatch(ctx context.Context, resourceIDs []string, batchID string) (bool, error) {
	if len(resourceIDs) == 0 {
		return false, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(resourceIDs)), ",")
	args := make([]any, 0, 2*len(resourceIDs)+1)
	for _, id := range resourceIDs {
		args = append(args, id)
	}
	for _, id := range resourceIDs {
		args = append(args, id)
	}
	args = append(args, batchID)
	var exists bool
	err := r.q.QueryRowContext(ctx, `SELECT EXISTS(
SELECT 1 FROM relations
WHERE (from_resource_id IN (`+ph+`) OR to_resource_id IN (`+ph+`))
  AND first_batch_id <> ?)`, args...).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("repo: relations in use check for batch %s: %w", batchID, err)
	}
	return exists, nil
}

// CountByType returns the number of relations per relation type.
func (r *RelationRepo) CountByType(ctx context.Context) (map[domain.RelationType]int, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT relation_type, COUNT(*) FROM relations GROUP BY relation_type`)
	if err != nil {
		return nil, fmt.Errorf("repo: count relations by type: %w", err)
	}
	defer rows.Close()
	counts := make(map[domain.RelationType]int)
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, fmt.Errorf("repo: scan count: %w", err)
		}
		counts[domain.RelationType(t)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: count relations by type: %w", err)
	}
	return counts, nil
}

// CountByBatch returns the number of relations created by batchID. Used by
// rollback reporting and the import atomicity tests.
func (r *RelationRepo) CountByBatch(ctx context.Context, batchID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM relations WHERE first_batch_id = ?`, batchID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count relations of batch %s: %w", batchID, err)
	}
	return n, nil
}

// CountByResourceIDs returns, for each id of resourceIDs, the number of
// relations in which the resource participates as either the from or the to
// endpoint. IDs absent from the result map participate in zero relations.
// The validation engine uses this to find unlinked devices (I001); a single
// grouped query avoids one round trip per device.
func (r *RelationRepo) CountByResourceIDs(ctx context.Context, resourceIDs []string) (map[string]int, error) {
	if len(resourceIDs) == 0 {
		return map[string]int{}, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(resourceIDs)), ",")
	args := make([]any, 0, 2*len(resourceIDs))
	for range 2 {
		for _, id := range resourceIDs {
			args = append(args, id)
		}
	}
	rows, err := r.q.QueryContext(ctx, `SELECT endpoint, COUNT(*) FROM (
SELECT from_resource_id AS endpoint FROM relations WHERE from_resource_id IN (`+ph+`)
UNION ALL
SELECT to_resource_id AS endpoint FROM relations WHERE to_resource_id IN (`+ph+`)
) GROUP BY endpoint`, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: count relations by resource ids: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int, len(resourceIDs))
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("repo: scan count: %w", err)
		}
		counts[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: count relations by resource ids: %w", err)
	}
	return counts, nil
}
