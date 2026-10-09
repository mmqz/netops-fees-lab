package repo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
)

// SequenceRepo allocates the globally unique monotonic ids documented in
// PROJECT_BRIEF_P2 §3 (vd-, if-, ln-, cs-, run-, cp-<n>; one sequence row
// per kind, NOT workspace-scoped — the simple `id TEXT PRIMARY KEY` of
// every sim table stays valid when multiple workspaces coexist). Every
// Allocate MUST run inside the caller's DB.Do transaction together with the
// rows that carry the allocated id, so a rollback discards the sequence
// increment as well (no burned ids on failed changesets). Running Allocate
// on the DB handle directly is a programming error: the increment would
// commit immediately, detached from the mutation it belongs to.
type SequenceRepo struct{ q DBTX }

// Allocate reserves and returns the next value of the (kind) sequence. The
// first allocation for a kind returns 1 (the row is seeded idempotently; a
// rare seeding race is resolved by retrying the UPDATE once). kind must be
// one of the domain.Seq* constants.
func (r *SequenceRepo) Allocate(ctx context.Context, kind string) (int, error) {
	switch kind {
	case domain.SeqDevice, domain.SeqInterface, domain.SeqLink,
		domain.SeqChangeSet, domain.SeqCheckRun, domain.SeqCheckpoint:
	default:
		return 0, fmt.Errorf("repo: allocate sequence %s: invalid kind %q", kind, kind)
	}

	// UPDATE … RETURNING keeps the increment and the read in one atomic
	// statement, which matters because database/sql cannot hold a row lock
	// across statements. modernc.org/sqlite v1.60.x supports RETURNING.
	var next int
	err := r.q.QueryRowContext(ctx,
		`UPDATE sequences SET last_value = last_value + 1
WHERE kind = ? RETURNING last_value`,
		kind).Scan(&next)
	if err == nil {
		return next, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("repo: allocate sequence %s: %w", kind, err)
	}

	// No row yet: seed the sequence with 1. Inside the same transaction a
	// second seeding is impossible (the UPDATE above would have found the
	// row), so the conflict branch only guards against concurrent seeders.
	_, err = r.q.ExecContext(ctx,
		`INSERT INTO sequences (kind, last_value) VALUES (?,1)`, kind)
	if err != nil {
		if isUniqueViolation(err) {
			if err := r.q.QueryRowContext(ctx,
				`UPDATE sequences SET last_value = last_value + 1
WHERE kind = ? RETURNING last_value`,
				kind).Scan(&next); err != nil {
				return 0, fmt.Errorf("repo: allocate sequence %s (retry): %w", kind, err)
			}
			return next, nil
		}
		return 0, fmt.Errorf("repo: seed sequence %s: %w", kind, err)
	}
	return 1, nil
}

// Value returns the current last_value of the (kind) sequence (0 when no
// row exists yet), without incrementing it. Useful for tests and
// diagnostics only — never for id allocation.
func (r *SequenceRepo) Value(ctx context.Context, kind string) (int, error) {
	var n int
	err := r.q.QueryRowContext(ctx,
		`SELECT last_value FROM sequences WHERE kind = ?`, kind).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("repo: read sequence %s: %w", kind, err)
	}
	return n, nil
}
