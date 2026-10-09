package repo

// extend via new methods; do not rewrite.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
)

// RowResultRepo persists the per-row import accounting of batches.
type RowResultRepo struct{ q DBTX }

// rowResultChunkRows caps the number of rows per multi-row INSERT
// statement, keeping the bind-parameter count safely below SQLite's
// variable limit (7 columns × 1000 rows = 7000 < 32766).
const rowResultChunkRows = 1000

// InsertBatch inserts all results as multi-row INSERT statements (one
// statement for up to rowResultChunkRows rows), so a whole sheet's
// accounting is written with minimal round trips. An empty slice is a
// no-op. Status values are validated before writing.
func (r *RowResultRepo) InsertBatch(ctx context.Context, results []domain.RowResult) error {
	for i, rr := range results {
		if !rr.Status.IsValid() {
			return fmt.Errorf("repo: insert row results: results[%d]: invalid status %q", i, rr.Status)
		}
	}
	for start := 0; start < len(results); start += rowResultChunkRows {
		end := start + rowResultChunkRows
		if end > len(results) {
			end = len(results)
		}
		chunk := results[start:end]
		var sb strings.Builder
		sb.WriteString(`INSERT INTO import_row_results
(batch_id, sheet, row_number, status, entity_id, rule_code, message) VALUES `)
		args := make([]any, 0, len(chunk)*7)
		for i, rr := range chunk {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,?,?,?,?,?)")
			args = append(args, rr.BatchID, rr.Sheet, rr.RowNumber, rr.Status.String(),
				nullString(rr.EntityID), nullString(rr.RuleCode), nullString(rr.Message))
		}
		if _, err := r.q.ExecContext(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("repo: insert row results of batch %s (rows %d..%d): %w",
				results[start].BatchID, start, end-1, err)
		}
	}
	return nil
}

// RowResultFilter selects row results. Empty fields mean "no filter".
// Limit <= 0 means unlimited; offset < 0 means 0.
type RowResultFilter struct {
	BatchID string
	Status  domain.RowStatus
	Sheet   string
	Limit   int
	Offset  int
}

// List returns row results matching f plus the total number of matches
// (ignoring limit/offset), ordered by (sheet, row_number).
func (r *RowResultRepo) List(ctx context.Context, f RowResultFilter) ([]domain.RowResult, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.BatchID != "" {
		where = append(where, "batch_id = ?")
		args = append(args, f.BatchID)
	}
	if f.Status != "" {
		if !f.Status.IsValid() {
			return nil, 0, fmt.Errorf("repo: list row results: invalid status %q", f.Status)
		}
		where = append(where, "status = ?")
		args = append(args, f.Status.String())
	}
	if f.Sheet != "" {
		where = append(where, "sheet = ?")
		args = append(args, f.Sheet)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_row_results WHERE `+whereSQL, args...).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count row results: %w", err)
	}
	limit, offset := paging(f.Limit, f.Offset)
	rows, err := r.q.QueryContext(ctx,
		`SELECT id, batch_id, sheet, row_number, status, entity_id, rule_code, message
FROM import_row_results WHERE `+whereSQL+` ORDER BY sheet, row_number LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list row results: %w", err)
	}
	defer rows.Close()
	out := make([]domain.RowResult, 0)
	for rows.Next() {
		var (
			rr       domain.RowResult
			status   string
			entityID sql.NullString
			ruleCode sql.NullString
			message  sql.NullString
		)
		if err := rows.Scan(&rr.ID, &rr.BatchID, &rr.Sheet, &rr.RowNumber, &status,
			&entityID, &ruleCode, &message); err != nil {
			return nil, 0, fmt.Errorf("repo: scan row result: %w", err)
		}
		rr.Status = domain.RowStatus(status)
		rr.EntityID = stringFromNull(entityID)
		rr.RuleCode = stringFromNull(ruleCode)
		rr.Message = stringFromNull(message)
		out = append(out, rr)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list row results: %w", err)
	}
	return out, total, nil
}

// CountsByBatch returns the number of row results of batchID per row
// status.
func (r *RowResultRepo) CountsByBatch(ctx context.Context, batchID string) (map[domain.RowStatus]int, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM import_row_results WHERE batch_id = ? GROUP BY status`, batchID)
	if err != nil {
		return nil, fmt.Errorf("repo: count row results of batch %s: %w", batchID, err)
	}
	defer rows.Close()
	counts := make(map[domain.RowStatus]int)
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, fmt.Errorf("repo: scan count: %w", err)
		}
		counts[domain.RowStatus(s)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: count row results of batch %s: %w", batchID, err)
	}
	return counts, nil
}
