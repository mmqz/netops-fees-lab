package repo

// extend via new methods; do not rewrite.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
)

// SourceRefRepo persists the source evidence of resources and relations.
type SourceRefRepo struct{ q DBTX }

// sourceRefColumns is the canonical SELECT column list of source_refs.
const sourceRefColumns = `id, entity_kind, entity_id, batch_id, workbook, sheet, row_number, raw`

// scanSourceRef scans one source_refs row into a domain.SourceRef.
func scanSourceRef(s scanner) (domain.SourceRef, error) {
	var (
		ref     domain.SourceRef
		kind    string
		rawJSON string
		rowNum  int
	)
	err := s.Scan(&ref.ID, &kind, &ref.EntityID, &ref.BatchID, &ref.Workbook, &ref.Sheet, &rowNum, &rawJSON)
	if err != nil {
		return domain.SourceRef{}, err
	}
	ref.EntityKind = domain.EntityKind(kind)
	ref.RowNumber = rowNum
	ref.Raw, err = mapFromJSON(rawJSON)
	if err != nil {
		return domain.SourceRef{}, err
	}
	return ref, nil
}

// Insert adds a new source ref. A conflicting (entity_kind, entity_id,
// batch_id) returns an error wrapping domain.ErrConflict.
func (r *SourceRefRepo) Insert(ctx context.Context, ref domain.SourceRef) error {
	raw, err := mapJSON(ref.Raw)
	if err != nil {
		return err
	}
	_, err = r.q.ExecContext(ctx, `INSERT INTO source_refs
(id, entity_kind, entity_id, batch_id, workbook, sheet, row_number, raw)
VALUES (?,?,?,?,?,?,?,?)`,
		ref.ID, ref.EntityKind.String(), ref.EntityID, ref.BatchID, ref.Workbook, ref.Sheet,
		ref.RowNumber, raw)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert source ref %s: %w: %w", ref.ID, domain.ErrConflict, err)
		}
		return fmt.Errorf("repo: insert source ref %s: %w", ref.ID, err)
	}
	return nil
}

// ListByEntity returns all source refs recorded for the given entity,
// ordered by insertion order (rowid, the chronological version order).
//
// v0.5 W7-D: the order used to be (batch_id, row_number), but batch_id is a
// random UUID — with re-imports now appending one version row per batch
// (importer skip path), a batch_id lexicographic order interleaves versions
// meaninglessly. rowid is assigned monotonically by SQLite on insert
// (one larger than the current maximum), so it equals the temporal order the
// versions were recorded in; rolling back a batch deletes rows but never
// renumbers the survivors, so the remaining order is stable. row_number is
// kept as a defensive tiebreaker (rowid is already unique per row).
func (r *SourceRefRepo) ListByEntity(ctx context.Context, kind domain.EntityKind, entityID string) ([]domain.SourceRef, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+sourceRefColumns+` FROM source_refs
WHERE entity_kind = ? AND entity_id = ?
ORDER BY rowid, row_number`,
		kind.String(), entityID)
	if err != nil {
		return nil, fmt.Errorf("repo: list source refs of %s %s: %w", kind, entityID, err)
	}
	defer rows.Close()
	out := make([]domain.SourceRef, 0)
	for rows.Next() {
		ref, err := scanSourceRef(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan source ref: %w", err)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list source refs of %s %s: %w", kind, entityID, err)
	}
	return out, nil
}

// maxINListVars bounds the number of placeholders of one ListByEntityIDs
// WHERE IN query. SQLite's compiled variable limit is far higher on every
// supported build (modernc.org/sqlite: 32766), but the chunk keeps the method
// correct regardless of the driver build and keeps the query plan shape
// stable. Callers page resources well below the chunk size (the MCP search
// caps at 100), so the common case is exactly ONE query.
const maxINListVars = 500

// ListByEntityIDs returns the source refs of EVERY listed entity, grouped by
// entity id, each group in rowid (version) order — the same per-entity order
// ListByEntity guarantees. It is the batched read of the W9-D N+1 fix: the
// MCP resource_search provenance resolution used to issue one ListByEntity
// per result row; the query service now resolves a whole result page with
// this single call.
//
// ids larger than maxINListVars are transparently split into several queries
// (merged into one map); empty ids returns an empty map without touching the
// database. Entities without refs are simply absent from the result map —
// callers distinguish "no provenance" from "empty list" by the key lookup.
func (r *SourceRefRepo) ListByEntityIDs(ctx context.Context, kind domain.EntityKind, ids []string) (map[string][]domain.SourceRef, error) {
	out := make(map[string][]domain.SourceRef, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	for start := 0; start < len(ids); start += maxINListVars {
		end := start + maxINListVars
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		placeholders := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)+1)
		args = append(args, kind.String())
		for i, id := range chunk {
			placeholders[i] = "?"
			args = append(args, id)
		}
		query := `SELECT ` + sourceRefColumns + ` FROM source_refs
WHERE entity_kind = ? AND entity_id IN (` + strings.Join(placeholders, ",") + `)
ORDER BY rowid, row_number`
		rows, err := r.q.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("repo: list source refs of %d %s entities: %w", len(chunk), kind, err)
		}
		for rows.Next() {
			ref, err := scanSourceRef(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("repo: scan source ref: %w", err)
			}
			out[ref.EntityID] = append(out[ref.EntityID], ref)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("repo: list source refs of %d %s entities: %w", len(chunk), kind, err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("repo: list source refs of %d %s entities: %w", len(chunk), kind, err)
		}
	}
	return out, nil
}

// DeleteByBatch deletes all source refs recorded by batchID and returns
// the number of deleted rows.
func (r *SourceRefRepo) DeleteByBatch(ctx context.Context, batchID string) (int64, error) {
	res, err := r.q.ExecContext(ctx, `DELETE FROM source_refs WHERE batch_id = ?`, batchID)
	if err != nil {
		return 0, fmt.Errorf("repo: delete source refs of batch %s: %w", batchID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: delete source refs of batch %s: rows affected: %w", batchID, err)
	}
	return n, nil
}

// CountByBatch returns the number of source refs recorded by batchID. Used
// by rollback reporting and the import atomicity tests.
func (r *SourceRefRepo) CountByBatch(ctx context.Context, batchID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM source_refs WHERE batch_id = ?`, batchID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count source refs of batch %s: %w", batchID, err)
	}
	return n, nil
}

// GetByID returns the source ref with the given id, or an error wrapping
// domain.ErrNotFound. The report generator resolves an issue's source_ref_id
// into its workbook/sheet/row evidence with this lookup.
func (r *SourceRefRepo) GetByID(ctx context.Context, id string) (domain.SourceRef, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+sourceRefColumns+` FROM source_refs WHERE id = ?`, id)
	ref, err := scanSourceRef(row)
	if err != nil {
		return domain.SourceRef{}, notFound("source ref", id, err)
	}
	return ref, nil
}
