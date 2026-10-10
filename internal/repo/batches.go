package repo

// extend via new methods; do not rewrite.

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// BatchRepo persists import batches.
type BatchRepo struct{ q DBTX }

// batchColumns is the canonical SELECT column list of import_batches.
const batchColumns = `id, workbook_name, workbook_sha256, status, mapping, rule_version,
rows_total, rows_imported, rows_skipped, rows_failed, error, started_at, finished_at`

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanBatch scans one import_batches row into a domain.ImportBatch.
func scanBatch(s scanner) (domain.ImportBatch, error) {
	var (
		b             domain.ImportBatch
		status        string
		errMsg        sql.NullString
		startedAtStr  string
		finishedAtStr sql.NullString
	)
	err := s.Scan(&b.ID, &b.WorkbookName, &b.WorkbookSHA256, &status, &b.Mapping, &b.RuleVersion,
		&b.RowsTotal, &b.RowsImported, &b.RowsSkipped, &b.RowsFailed,
		&errMsg, &startedAtStr, &finishedAtStr)
	if err != nil {
		return domain.ImportBatch{}, err
	}
	b.Status = domain.BatchStatus(status)
	b.Error = stringFromNull(errMsg)
	startedAt, err := parseTime(startedAtStr)
	if err != nil {
		return domain.ImportBatch{}, err
	}
	b.StartedAt = startedAt
	finishedAt, err := timePtrFromNull(finishedAtStr)
	if err != nil {
		return domain.ImportBatch{}, err
	}
	b.FinishedAt = finishedAt
	return b, nil
}

// Create inserts a new import batch. b.Mapping may be empty; it is stored
// as "{}" then. b.FinishedAt is normally nil on creation.
func (r *BatchRepo) Create(ctx context.Context, b domain.ImportBatch) error {
	mapping := b.Mapping
	if mapping == "" {
		mapping = "{}"
	}
	_, err := r.q.ExecContext(ctx, `INSERT INTO import_batches
(id, workbook_name, workbook_sha256, status, mapping, rule_version,
 rows_total, rows_imported, rows_skipped, rows_failed, error, started_at, finished_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.WorkbookName, b.WorkbookSHA256, b.Status.String(), mapping, b.RuleVersion,
		b.RowsTotal, b.RowsImported, b.RowsSkipped, b.RowsFailed,
		nullString(b.Error), formatTime(b.StartedAt), nullableTimePtr(b.FinishedAt))
	if err != nil {
		return fmt.Errorf("repo: create batch %s: %w", b.ID, err)
	}
	return nil
}

// Get returns the batch with the given id, or an error wrapping
// domain.ErrNotFound.
func (r *BatchRepo) Get(ctx context.Context, id string) (domain.ImportBatch, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM import_batches WHERE id = ?`, id)
	b, err := scanBatch(row)
	if err != nil {
		return domain.ImportBatch{}, notFound("batch", id, err)
	}
	return b, nil
}

// List returns batches newest-first (started_at DESC; same-second creations
// tie-break by insertion order via rowid DESC, so "latest batch" is
// deterministic — batch ids are random UUIDs and cannot order) with the
// total count ignoring limit/offset. limit <= 0 means unlimited; offset < 0
// means 0.
func (r *BatchRepo) List(ctx context.Context, limit, offset int) ([]domain.ImportBatch, int, error) {
	var total int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_batches`).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count batches: %w", err)
	}
	limit, offset = paging(limit, offset)
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+batchColumns+` FROM import_batches ORDER BY started_at DESC, rowid DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list batches: %w", err)
	}
	defer rows.Close()
	batches := make([]domain.ImportBatch, 0)
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("repo: scan batch: %w", err)
		}
		batches = append(batches, b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list batches: %w", err)
	}
	return batches, total, nil
}

// BatchCounts is the row-accounting snapshot of a batch.
type BatchCounts struct {
	Total    int
	Imported int
	Skipped  int
	Failed   int
}

// UpdateCounts overwrites the four row counters of a batch.
func (r *BatchRepo) UpdateCounts(ctx context.Context, id string, c BatchCounts) error {
	res, err := r.q.ExecContext(ctx,
		`UPDATE import_batches SET rows_total = ?, rows_imported = ?, rows_skipped = ?, rows_failed = ? WHERE id = ?`,
		c.Total, c.Imported, c.Skipped, c.Failed, id)
	if err != nil {
		return fmt.Errorf("repo: update counts of batch %s: %w", id, err)
	}
	return requireUpdated(res, "batch", id)
}

// UpdateStatus transitions a batch to status. Entering a terminal state
// (completed, failed, rolled_back) stamps finished_at exactly once
// (COALESCE), so later transitions such as rolled_back keep the original
// finish time.
func (r *BatchRepo) UpdateStatus(ctx context.Context, id string, status domain.BatchStatus) error {
	if !status.IsValid() {
		return fmt.Errorf("repo: update status of batch %s: invalid status %q", id, status)
	}
	var (
		res sql.Result
		err error
	)
	if status.IsTerminal() {
		res, err = r.q.ExecContext(ctx,
			`UPDATE import_batches SET status = ?, finished_at = COALESCE(finished_at, ?) WHERE id = ?`,
			status.String(), formatTime(time.Now().UTC()), id)
	} else {
		res, err = r.q.ExecContext(ctx, `UPDATE import_batches SET status = ? WHERE id = ?`, status.String(), id)
	}
	if err != nil {
		return fmt.Errorf("repo: update status of batch %s: %w", id, err)
	}
	return requireUpdated(res, "batch", id)
}

// MarkFailed marks a batch failed with an error message and stamps
// finished_at (once).
func (r *BatchRepo) MarkFailed(ctx context.Context, id string, msg string) error {
	res, err := r.q.ExecContext(ctx,
		`UPDATE import_batches SET status = ?, error = ?, finished_at = COALESCE(finished_at, ?) WHERE id = ?`,
		domain.BatchStatusFailed.String(), nullString(msg), formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("repo: mark batch %s failed: %w", id, err)
	}
	return requireUpdated(res, "batch", id)
}

// SetMapping stores the confirmed mapping JSON on a batch.
func (r *BatchRepo) SetMapping(ctx context.Context, id string, mappingJSON string) error {
	if mappingJSON == "" {
		mappingJSON = "{}"
	}
	res, err := r.q.ExecContext(ctx, `UPDATE import_batches SET mapping = ? WHERE id = ?`, mappingJSON, id)
	if err != nil {
		return fmt.Errorf("repo: set mapping of batch %s: %w", id, err)
	}
	return requireUpdated(res, "batch", id)
}

// MarkInterruptedImportsFailed resets every batch stuck in a TRANSIENT
// pipeline state (analyzing, importing — the crash windows of the import
// pipeline) to failed with the given reason, stamps finished_at once
// (COALESCE) and returns the number of reset batches. It is the
// crash-window self-heal (R1-D6) invoked at serve startup and at the CLI
// import entry (via importer.Service.RecoverInterrupted).
//
// awaiting_mapping is deliberately NOT in the target set: it is the stable
// "user has not confirmed the mapping yet" state, not a crash window, and
// must survive process restarts. Terminal statuses are excluded by the same
// WHERE clause, so the heal is idempotent (a second run updates 0 rows)
// and can never touch finished imports. A healed batch that was in fact an
// in-flight import of another process gets its real terminal status
// written afterwards by that import's own transaction (execute.go).
func (r *BatchRepo) MarkInterruptedImportsFailed(ctx context.Context, reason string) (int64, error) {
	res, err := r.q.ExecContext(ctx,
		`UPDATE import_batches
SET status = ?, error = ?, finished_at = COALESCE(finished_at, ?)
WHERE status IN (?, ?)`,
		domain.BatchStatusFailed.String(), nullString(reason), formatTime(time.Now().UTC()),
		domain.BatchStatusAnalyzing.String(), domain.BatchStatusImporting.String())
	if err != nil {
		return 0, fmt.Errorf("repo: mark interrupted imports failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: mark interrupted imports failed: rows affected: %w", err)
	}
	return n, nil
}

// requireUpdated verifies that an UPDATE matched exactly one row and
// returns domain.ErrNotFound otherwise.
func requireUpdated(res sql.Result, kind, key string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("repo: %s %s: rows affected: %w", kind, key, err)
	}
	if n == 0 {
		return fmt.Errorf("repo: %s %s: %w", kind, key, domain.ErrNotFound)
	}
	return nil
}

// SetWorkbookPath records the file path of the workbook the batch was
// analyzed from, so the import service can re-read the workbook when the
// confirmed mapping is executed (column added by migration v4).
func (r *BatchRepo) SetWorkbookPath(ctx context.Context, id, path string) error {
	res, err := r.q.ExecContext(ctx, `UPDATE import_batches SET workbook_path = ? WHERE id = ?`, path, id)
	if err != nil {
		return fmt.Errorf("repo: set workbook path of batch %s: %w", id, err)
	}
	return requireUpdated(res, "batch", id)
}

// WorkbookPath returns the recorded workbook file path of the batch, or ""
// when no path is stored (batches predating migration v4). A missing batch
// returns an error wrapping domain.ErrNotFound.
func (r *BatchRepo) WorkbookPath(ctx context.Context, id string) (string, error) {
	var path sql.NullString
	err := r.q.QueryRowContext(ctx, `SELECT workbook_path FROM import_batches WHERE id = ?`, id).Scan(&path)
	if err != nil {
		return "", notFound("batch", id, err)
	}
	return stringFromNull(path), nil
}

// ListWorkbookPaths returns the workbook_path of EVERY batch in the
// database, regardless of status — completed, failed, rolled_back,
// awaiting_mapping, all of it. It is the reference set of the uploads
// garbage collector (W5-D, v0.4.0): a batch's workbook is evidence (issue
// source refs re-read it), so any file it references must survive GC even
// when the batch itself failed or was rolled back. Only the never-referenced
// files under <dataDir>/uploads are collectable.
func (r *BatchRepo) ListWorkbookPaths(ctx context.Context) ([]string, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT workbook_path FROM import_batches
WHERE workbook_path IS NOT NULL AND workbook_path != ''`)
	if err != nil {
		return nil, fmt.Errorf("repo: list workbook paths: %w", err)
	}
	defer rows.Close()
	paths := make([]string, 0)
	for rows.Next() {
		var path sql.NullString
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("repo: scan workbook path: %w", err)
		}
		paths = append(paths, stringFromNull(path))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list workbook paths: %w", err)
	}
	return paths, nil
}
