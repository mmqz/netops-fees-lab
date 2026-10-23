package repo

// Metric batches of the separate metric ingest channel (migration v14,
// W20 — ADR-0011 D1: deliberately NOT import_batches, "不与资源批次混表").
// The accounting differs from the resource channel on two documented
// points: there are three counters (total/imported/failed — no skipped: a
// metric row either lands as points or fails resolution/parsing; re-import
// of the same data OVERWRITES the same points instead of skipping, value
// semantics per ADR-0014 decision 2) and there is NO row-level results
// table (the counters are the row accounting; metrics.batch_id is the
// per-point lineage).
//
// extend via new methods; do not rewrite.

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// MetricBatchRepo persists metric batches.
type MetricBatchRepo struct{ q DBTX }

// metricBatchColumns is the canonical SELECT column list of metric_batches.
const metricBatchColumns = `id, workbook_name, workbook_sha256, status, mapping,
rows_total, rows_imported, rows_failed, error, started_at, finished_at`

// scanMetricBatch scans one metric_batches row into a domain.MetricBatch.
func scanMetricBatch(s scanner) (domain.MetricBatch, error) {
	var (
		b             domain.MetricBatch
		status        string
		errMsg        sql.NullString
		startedAtStr  string
		finishedAtStr sql.NullString
	)
	err := s.Scan(&b.ID, &b.WorkbookName, &b.WorkbookSHA256, &status, &b.Mapping,
		&b.RowsTotal, &b.RowsImported, &b.RowsFailed,
		&errMsg, &startedAtStr, &finishedAtStr)
	if err != nil {
		return domain.MetricBatch{}, err
	}
	b.Status = domain.BatchStatus(status)
	b.Error = stringFromNull(errMsg)
	startedAt, err := parseTime(startedAtStr)
	if err != nil {
		return domain.MetricBatch{}, err
	}
	b.StartedAt = startedAt
	finishedAt, err := timePtrFromNull(finishedAtStr)
	if err != nil {
		return domain.MetricBatch{}, err
	}
	b.FinishedAt = finishedAt
	return b, nil
}

// Create inserts a new metric batch. b.Mapping may be empty; it is stored
// as "{}" then.
func (r *MetricBatchRepo) Create(ctx context.Context, b domain.MetricBatch) error {
	mapping := b.Mapping
	if mapping == "" {
		mapping = "{}"
	}
	_, err := r.q.ExecContext(ctx, `INSERT INTO metric_batches
(id, workbook_name, workbook_sha256, status, mapping, rows_total, rows_imported, rows_failed, error, started_at, finished_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.WorkbookName, b.WorkbookSHA256, b.Status.String(), mapping,
		b.RowsTotal, b.RowsImported, b.RowsFailed,
		nullString(b.Error), formatTime(b.StartedAt), nullableTimePtr(b.FinishedAt))
	if err != nil {
		return fmt.Errorf("repo: create metric batch %s: %w", b.ID, err)
	}
	return nil
}

// Get returns the metric batch with the given id, or an error wrapping
// domain.ErrNotFound.
func (r *MetricBatchRepo) Get(ctx context.Context, id string) (domain.MetricBatch, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+metricBatchColumns+` FROM metric_batches WHERE id = ?`, id)
	b, err := scanMetricBatch(row)
	if err != nil {
		return domain.MetricBatch{}, notFound("metric batch", id, err)
	}
	return b, nil
}

// List returns metric batches newest-first (started_at DESC, tie-break by
// rowid DESC — the import_batches listing discipline; ids are random
// UUIDs and cannot order) with the total count ignoring limit/offset.
// limit <= 0 means unlimited; offset < 0 means 0.
func (r *MetricBatchRepo) List(ctx context.Context, limit, offset int) ([]domain.MetricBatch, int, error) {
	var total int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM metric_batches`).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count metric batches: %w", err)
	}
	limit, offset = paging(limit, offset)
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+metricBatchColumns+` FROM metric_batches ORDER BY started_at DESC, rowid DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list metric batches: %w", err)
	}
	defer rows.Close()
	batches := make([]domain.MetricBatch, 0)
	for rows.Next() {
		b, err := scanMetricBatch(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("repo: scan metric batch: %w", err)
		}
		batches = append(batches, b)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list metric batches: %w", err)
	}
	return batches, total, nil
}

// MetricBatchCounts is the row-accounting snapshot of a metric batch.
type MetricBatchCounts struct {
	Total    int
	Imported int
	Failed   int
}

// UpdateCounts overwrites the three row counters of a metric batch.
func (r *MetricBatchRepo) UpdateCounts(ctx context.Context, id string, c MetricBatchCounts) error {
	res, err := r.q.ExecContext(ctx,
		`UPDATE metric_batches SET rows_total = ?, rows_imported = ?, rows_failed = ? WHERE id = ?`,
		c.Total, c.Imported, c.Failed, id)
	if err != nil {
		return fmt.Errorf("repo: update counts of metric batch %s: %w", id, err)
	}
	return requireUpdated(res, "metric batch", id)
}

// UpdateStatus transitions a metric batch to status. Entering a terminal
// state (completed, failed, rolled_back) stamps finished_at exactly once
// (COALESCE) — the import_batches discipline.
func (r *MetricBatchRepo) UpdateStatus(ctx context.Context, id string, status domain.BatchStatus) error {
	if !status.IsValid() {
		return fmt.Errorf("repo: update status of metric batch %s: invalid status %q", id, status)
	}
	var (
		res sql.Result
		err error
	)
	if status.IsTerminal() {
		res, err = r.q.ExecContext(ctx,
			`UPDATE metric_batches SET status = ?, finished_at = COALESCE(finished_at, ?) WHERE id = ?`,
			status.String(), formatTime(time.Now().UTC()), id)
	} else {
		res, err = r.q.ExecContext(ctx, `UPDATE metric_batches SET status = ? WHERE id = ?`, status.String(), id)
	}
	if err != nil {
		return fmt.Errorf("repo: update status of metric batch %s: %w", id, err)
	}
	return requireUpdated(res, "metric batch", id)
}

// MarkFailed marks a metric batch failed with an error message and stamps
// finished_at (once).
func (r *MetricBatchRepo) MarkFailed(ctx context.Context, id string, msg string) error {
	res, err := r.q.ExecContext(ctx,
		`UPDATE metric_batches SET status = ?, error = ?, finished_at = COALESCE(finished_at, ?) WHERE id = ?`,
		domain.BatchStatusFailed.String(), nullString(msg), formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("repo: mark metric batch %s failed: %w", id, err)
	}
	return requireUpdated(res, "metric batch", id)
}

// MarkInterruptedImportsFailed heals the metric channel's crash window
// (#87, the RecoverInterrupted precedent from the resource channel): a
// metric ingest writes its terminal status INSIDE the ingest transaction,
// so a batch still in a transient state after a process death is debris of
// a crashed run and would stay importing forever — no other write would
// ever touch it. Resets every analyzing/importing metric batch to
// failed(interrupted) and stamps finished_at once.
//
// Safety mirrors the resource channel's ruling (batches.go):
//   - terminal statuses (completed / failed / rolled_back) are excluded by
//     the WHERE clause, so the heal is idempotent (second run: 0 rows)
//   - a false positive (another process's ingest genuinely in flight while
//     this runs) is tolerated by design: that ingest writes its own
//     terminal status inside its transaction afterwards, overwriting the heal
//   - call sites: only where no ingest of THIS process is in flight —
//     serve startup and the CLI entry, right next to
//     Importer.RecoverInterrupted. Never in repo.Open or the MCP path.
func (r *MetricBatchRepo) MarkInterruptedImportsFailed(ctx context.Context, reason string) (int64, error) {
	res, err := r.q.ExecContext(ctx,
		`UPDATE metric_batches
SET status = ?, error = ?, finished_at = COALESCE(finished_at, ?)
WHERE status IN (?, ?)`,
		domain.BatchStatusFailed.String(), nullString(reason), formatTime(time.Now().UTC()),
		domain.BatchStatusAnalyzing.String(), domain.BatchStatusImporting.String())
	if err != nil {
		return 0, fmt.Errorf("repo: mark interrupted metric batches failed: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: mark interrupted metric batches failed: rows affected: %w", err)
	}
	return n, nil
}
