package repo

// extend via new methods; do not rewrite.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// ResourceRepo persists normalized resources. Insert is deliberately the
// only write path (no upsert): the importer decides between insert and
// skip, which is what makes re-import idempotent.
type ResourceRepo struct{ q DBTX }

// resourceColumns is the canonical SELECT column list of resources. The
// W22 run-state snapshot columns (run_state/run_state_at, migration v17)
// are READ here; the INSERT column list below deliberately omits them —
// imports never write run state (§12-B1: 台账与运行状态分立).
const resourceColumns = `id, resource_type, natural_key, name, attributes, status, first_batch_id, created_at, updated_at, run_state, run_state_at`

// scanResource scans one resources row into a domain.Resource.
func scanResource(s scanner) (domain.Resource, error) {
	var (
		res       domain.Resource
		rtype     string
		attrsJSON string
		status    sql.NullString
		createdAt string
		updatedAt string
		runState  sql.NullString
		runAt     sql.NullString
	)
	err := s.Scan(&res.ID, &rtype, &res.NaturalKey, &res.Name, &attrsJSON, &status,
		&res.FirstBatchID, &createdAt, &updatedAt, &runState, &runAt)
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
	// Run-state snapshot: NULL (no observation) scans to ""/zero time.
	if runState.Valid {
		if _, err := parseRunStateStrict(runState.String); err != nil {
			return domain.Resource{}, err
		}
		res.RunState = runState.String
	}
	if runAt.Valid {
		if res.RunStateAt, err = parseTime(runAt.String); err != nil {
			return domain.Resource{}, err
		}
	}
	return res, nil
}

// Insert adds a new resource. A conflicting (resource_type, natural_key)
// returns an error wrapping domain.ErrConflict. The run-state snapshot
// columns are NOT written (they default to NULL = no observation): run
// state enters only through ResourceStatusRepo.UpsertStatus — the
// importer's identity semantics stay byte-identical to pre-W22.
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

// ListByType returns every resource of the given type, ordered by
// (natural_key, id) for deterministic iteration. The importer's rename
// detection (W6-D) uses it to enumerate the candidate pool of a type.
func (r *ResourceRepo) ListByType(ctx context.Context, resourceType domain.ResourceType) ([]domain.Resource, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+resourceColumns+` FROM resources WHERE resource_type = ? ORDER BY natural_key, id`,
		resourceType.String())
	if err != nil {
		return nil, fmt.Errorf("repo: list resources of type %s: %w", resourceType, err)
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
		return nil, fmt.Errorf("repo: list resources of type %s: %w", resourceType, err)
	}
	return out, nil
}

// UpdateNaturalKey rewrites the natural key of the resource with the given
// id and refreshes updated_at. It is the key write of the rename apply flow
// (renames service): the caller has verified inside the SAME DB.Do
// transaction that the new key's resource (if any) is already deleted, so
// the UNIQUE (resource_type, natural_key) constraint cannot fire. An
// unknown id answers domain.ErrNotFound.
func (r *ResourceRepo) UpdateNaturalKey(ctx context.Context, id, naturalKey string, updatedAt time.Time) error {
	res, err := r.q.ExecContext(ctx, `UPDATE resources SET natural_key = ?, updated_at = ? WHERE id = ?`,
		naturalKey, formatTime(updatedAt), id)
	if err != nil {
		return fmt.Errorf("repo: update natural key of resource %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("repo: update natural key of resource %s: rows affected: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("repo: update natural key of resource %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// UpdateName rewrites the display name of the resource with the given id
// and refreshes updated_at. It is the W9-D name-sync write of the two apply
// flows: resources.name is the projection of the type's NameField value, so
// a confirmed rename (renames service — the name follows the merged key
// components) and a confirmed change ON the name field itself (changes
// service — base_station_name, device_name, …) must both keep it aligned; a
// stale name would contradict the attribute it was derived from. An unknown
// id answers domain.ErrNotFound.
func (r *ResourceRepo) UpdateName(ctx context.Context, id, name string, updatedAt time.Time) error {
	res, err := r.q.ExecContext(ctx, `UPDATE resources SET name = ?, updated_at = ? WHERE id = ?`,
		name, formatTime(updatedAt), id)
	if err != nil {
		return fmt.Errorf("repo: update name of resource %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("repo: update name of resource %s: rows affected: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("repo: update name of resource %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// DeleteByID deletes one resource by id and returns whether a row was
// deleted. The rename apply flow (renames service) uses it to remove the
// short-lived new resource after its relations were retargeted — its
// pending attribute change proposals vanish via the resource_id ON DELETE
// CASCADE (documented behavior). Relations still referencing the id (none
// after a retarget) would be removed by the relations-side CASCADE, the
// same semantics the rollback delete chain relies on.
func (r *ResourceRepo) DeleteByID(ctx context.Context, id string) (bool, error) {
	res, err := r.q.ExecContext(ctx, `DELETE FROM resources WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("repo: delete resource %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("repo: delete resource %s: rows affected: %w", id, err)
	}
	return n > 0, nil
}

// UpdateAttributes overwrites the attributes JSON of the resource with the
// given id and refreshes updated_at. It is the write side of the attribute
// change apply flow (changes service): the caller has verified the
// current-value precondition inside the SAME DB.Do transaction, so the
// read-modify-write cannot race. An unknown id answers domain.ErrNotFound.
func (r *ResourceRepo) UpdateAttributes(ctx context.Context, id string, attrs map[string]string, updatedAt time.Time) error {
	attrsJSON, err := mapJSON(attrs)
	if err != nil {
		return err
	}
	res, err := r.q.ExecContext(ctx, `UPDATE resources SET attributes = ?, updated_at = ? WHERE id = ?`,
		attrsJSON, formatTime(updatedAt), id)
	if err != nil {
		return fmt.Errorf("repo: update attributes of resource %s: %w", id, err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("repo: update attributes of resource %s: rows affected: %w", id, err)
	} else if n == 0 {
		return fmt.Errorf("repo: update attributes of resource %s: %w", id, domain.ErrNotFound)
	}
	return nil
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
