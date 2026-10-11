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

// RetargetResource moves every relation endpoint referencing newID over to
// oldID — the identity-merge step of the rename apply flow (W6-D): the new
// resource's relations (e.g. an ip_pool's assigned_to edges) must survive the
// merge, now anchored at the identity keeper. Retargeting may collide with an
// existing relation of the old resource: UNIQUE (from, to, relation_type)
// would reject the moved tuple as a duplicate — a duplicate edge is exactly
// what "same asset recorded twice" produces, so the new-side edge is DELETED
// (keeping the old side) instead of failing the apply. The loop walks the
// new resource's relations in ListByResource order (deterministic) and
// updates/deletes one at a time inside the caller's transaction, so later
// iterations already see the retargeted state (two new-side edges converging
// on the same old-side tuple cannot both survive — the second is deleted as
// a collision).
//
// Returns the number of retargeted and deleted relations.
func (r *RelationRepo) RetargetResource(ctx context.Context, oldID, newID string) (int64, int64, error) {
	var retargeted, deleted int64
	rels, err := r.ListByResource(ctx, newID)
	if err != nil {
		return 0, 0, err
	}
	for _, rel := range rels {
		from, to := rel.FromResourceID, rel.ToResourceID
		moved := false
		if from == newID {
			from = oldID
			moved = true
		}
		if to == newID {
			to = oldID
			moved = true
		}
		if !moved {
			continue // defensive: ListByResource only returns participating rows
		}
		// A relation with the retargeted endpoints already exists (old side,
		// or an earlier retargeted new-side edge): the merged identity owns
		// one such edge — drop the duplicate instead of failing.
		var collision bool
		if err := r.q.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM relations WHERE from_resource_id = ? AND to_resource_id = ? AND relation_type = ? AND id <> ?)`,
			from, to, rel.Type.String(), rel.ID).Scan(&collision); err != nil {
			return retargeted, deleted, fmt.Errorf("repo: retarget relation %s: %w", rel.ID, err)
		}
		if collision {
			res, err := r.q.ExecContext(ctx, `DELETE FROM relations WHERE id = ?`, rel.ID)
			if err != nil {
				return retargeted, deleted, fmt.Errorf("repo: delete colliding relation %s: %w", rel.ID, err)
			}
			n, err := res.RowsAffected()
			if err != nil {
				return retargeted, deleted, fmt.Errorf("repo: delete colliding relation %s: rows affected: %w", rel.ID, err)
			}
			deleted += n
			continue
		}
		if _, err := r.q.ExecContext(ctx,
			`UPDATE relations SET from_resource_id = ?, to_resource_id = ? WHERE id = ?`,
			from, to, rel.ID); err != nil {
			return retargeted, deleted, fmt.Errorf("repo: retarget relation %s: %w", rel.ID, err)
		}
		retargeted++
	}
	return retargeted, deleted, nil
}

// UpdateTarget retargets the to-endpoint of the resource's derived relation
// of the given type to newToResourceID: UPDATE relations SET to_resource_id
// WHERE from_resource_id = ? AND relation_type = ? (from and type stay
// fixed). It is the W13-D write of the attribute-change apply flow: a
// confirmed ref-field change (machine_room.site_id, ip_pool.assigned_device,
// circuit a/z_end …) moves the derived edge to the newly confirmed target
// inside the caller's apply transaction. The CALLER has verified the
// preconditions — the field is a ref field of the from-resource's schema,
// the new target exists (natural-key resolution, the same lookup the
// importer's E003 uses) and relationType is the derivation mapping's answer
// for that (type, field) pair (mapping.RelationTypeFor, direction from =
// the owning resource, exactly as the importer created the edge).
//
// Returns the number of updated rows. 0 means the resource has NO derived
// relation of that type — the apply then keeps the attribute-only
// semantics (e.g. a machine_room imported with an empty optional site_id:
// W001 orphan, no edge) and NEVER creates one: every relation row's
// first_batch_id anchors its workbook evidence, a human apply has no
// workbook row to anchor, so an evidence-free edge would break the
// provenance invariant. Retargeting is different — the edge already has
// lineage, the confirmation only corrects where it points (the change row
// itself is the audit line).
//
// UNIQUE (from, to, relation_type) safety argument: from and type are
// unchanged, only to moves. A conflict with an existing tuple requires a
// second edge with the SAME (from, type) already pointing at newTo — in
// the importer-derived world each single-valued ref field owns at most one
// edge per (from, type), and an edge already pointing at newTo would mean
// the attribute already carried the new value, which contradicts the W005
// old-value guard upstream (old != new is why the proposal exists). The
// one theoretical source of two same-(from, type) edges is a rename-merge
// (RetargetResource) of two resources whose same-type edges have different
// targets; retargeting both rows to one target then violates UNIQUE, the
// statement aborts, the caller's transaction rolls back — the apply fails
// atomically instead of corrupting the edge set.
//
// Lineage is untouched: first_batch_id, attributes and created_at keep
// their imported values (the relations table has no updated_at column —
// retargeting is recorded on the attribute-change row, not on the edge;
// the edge is not re-created). See docs/import-and-validation.md §14.
func (r *RelationRepo) UpdateTarget(ctx context.Context, fromResourceID string, relationType domain.RelationType, newToResourceID string) (int64, error) {
	res, err := r.q.ExecContext(ctx,
		`UPDATE relations SET to_resource_id = ? WHERE from_resource_id = ? AND relation_type = ?`,
		newToResourceID, fromResourceID, relationType.String())
	if err != nil {
		return 0, fmt.Errorf("repo: retarget relation of %s (%s → %s): %w",
			fromResourceID, relationType, newToResourceID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: retarget relation of %s (%s): rows affected: %w",
			fromResourceID, relationType, err)
	}
	return n, nil
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

// ListAll returns every relation of the database, ordered by
// (relation_type, from_resource_id, to_resource_id, id) for deterministic
// iteration. The topology graph builder consumes this to construct the full
// undirected adjacency of the dataset.
func (r *RelationRepo) ListAll(ctx context.Context) ([]domain.Relation, error) {
	rows, err := r.q.QueryContext(ctx, `SELECT `+relationColumns+` FROM relations
ORDER BY relation_type, from_resource_id, to_resource_id, id`)
	if err != nil {
		return nil, fmt.Errorf("repo: list all relations: %w", err)
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
		return nil, fmt.Errorf("repo: list all relations: %w", err)
	}
	return out, nil
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
