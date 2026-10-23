package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// ResourceStatusRepo persists the W22 run-state model (migration v17,
// §12-B1 方案 C): the resource_status HISTORY table (value semantics — the
// same (resource_id, ts) upserts, history accumulates for flap analysis) and
// the resources.run_state / run_state_at SNAPSHOT columns. Both sides of one
// observation are written by UpsertStatus inside the SAME transaction — call
// it through DB.Do so the two writes are atomic.
//
// 台账 status (resources.status, the import channel's free text) is STRICTLY
// out of scope here: no method of this repo reads or writes it (B1's core
// separation — the W005 flow, sim materialize and the lab stay untouched).
type ResourceStatusRepo struct{ q DBTX }

// statusColumns is the canonical SELECT column list of resource_status.
const statusColumns = `resource_id, ts, run_state, source`

// scanStatus scans one resource_status row into a domain.ResourceStatus.
func scanStatus(s scanner) (domain.ResourceStatus, error) {
	var (
		st       domain.ResourceStatus
		runState string
		source   string
		ts       string
	)
	if err := s.Scan(&st.ResourceID, &ts, &runState, &source); err != nil {
		return domain.ResourceStatus{}, err
	}
	if _, err := parseRunStateStrict(runState); err != nil {
		return domain.ResourceStatus{}, err
	}
	if _, err := parseStatusSourceStrict(source); err != nil {
		return domain.ResourceStatus{}, err
	}
	st.RunState = domain.RunState(runState)
	st.Source = domain.StatusSource(source)
	var err error
	if st.TS, err = parseTime(ts); err != nil {
		return domain.ResourceStatus{}, err
	}
	return st, nil
}

// parseRunStateStrict converts a stored run_state string into a
// domain.RunState, rejecting values outside the closed enum (defense in
// depth on top of the v17 CHECK constraint).
func parseRunStateStrict(s string) (domain.RunState, error) {
	v := domain.RunState(s)
	if !v.IsValid() {
		return "", fmt.Errorf("repo: stored run_state %q outside the closed enum", s)
	}
	return v, nil
}

// parseStatusSourceStrict converts a stored source string into a
// domain.StatusSource, rejecting values outside the closed enum.
func parseStatusSourceStrict(s string) (domain.StatusSource, error) {
	v := domain.StatusSource(s)
	if !v.IsValid() {
		return "", fmt.Errorf("repo: stored status source %q outside the closed enum", s)
	}
	return v, nil
}

// UpsertStatus writes ONE observation:
//
//   - the history row — INSERT OR REPLACE (value semantics: the same
//     (resource_id, ts) overwrites, exactly like the metrics tables of
//     ADR-0014 decision 2; a different ts appends a new row), and
//   - the resources snapshot — run_state/run_state_at of the NEWEST
//     observation: the snapshot moves only when the written ts is >= the
//     projected one (or no observation exists yet). Writing an older
//     timestamp therefore corrects history WITHOUT regressing the snapshot.
//
// resources.updated_at is deliberately untouched: it tracks the ledger's
// data freshness (import/confirmation writes), not run-state telemetry.
//
// The resource must exist: an unknown id answers domain.ErrNotFound. Both
// statements run on q — call through DB.Do for the single-transaction
// guarantee.
func (r *ResourceStatusRepo) UpsertStatus(ctx context.Context, st domain.ResourceStatus) error {
	if !st.RunState.IsValid() {
		return fmt.Errorf("repo: upsert run state: invalid run_state %q", st.RunState)
	}
	if !st.Source.IsValid() {
		return fmt.Errorf("repo: upsert run state: invalid source %q", st.Source)
	}

	// Existence check + current snapshot in one read (the update decision
	// needs the projected ts anyway).
	var current sql.NullString
	err := r.q.QueryRowContext(ctx,
		`SELECT run_state_at FROM resources WHERE id = ?`, st.ResourceID).Scan(&current)
	if err != nil {
		return notFound("resource", st.ResourceID, err)
	}

	if _, err := r.q.ExecContext(ctx,
		`INSERT OR REPLACE INTO resource_status (resource_id, ts, run_state, source) VALUES (?,?,?,?)`,
		st.ResourceID, formatTime(st.TS), st.RunState.String(), st.Source.String()); err != nil {
		return fmt.Errorf("repo: upsert run-state history of %s: %w", st.ResourceID, err)
	}

	// Move the snapshot only for the newest observation (equal ts overwrites
	// — value semantics; older ts leaves the snapshot alone).
	if !current.Valid || current.String <= formatTime(st.TS) {
		if _, err := r.q.ExecContext(ctx,
			`UPDATE resources SET run_state = ?, run_state_at = ? WHERE id = ?`,
			st.RunState.String(), formatTime(st.TS), st.ResourceID); err != nil {
			return fmt.Errorf("repo: update run-state snapshot of %s: %w", st.ResourceID, err)
		}
	}
	return nil
}

// ListHistory returns the run-state observations of resourceID with ts in
// [from, to] (inclusive; "" = unbounded; RFC3339 strings as stored), ordered
// by ts ASCENDING — the flap-analysis read. The resource's existence is NOT
// re-checked (the history of an unknown id is honestly empty; the HTTP layer
// 404s on the resource itself before calling).
func (r *ResourceStatusRepo) ListHistory(ctx context.Context, resourceID, from, to string) ([]domain.ResourceStatus, error) {
	where := []string{"resource_id = ?"}
	args := []any{resourceID}
	if from != "" {
		where = append(where, "ts >= ?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "ts <= ?")
		args = append(args, to)
	}
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+statusColumns+` FROM resource_status WHERE `+strings.Join(where, " AND ")+` ORDER BY ts ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("repo: list run-state history of %s: %w", resourceID, err)
	}
	defer rows.Close()
	out := make([]domain.ResourceStatus, 0)
	for rows.Next() {
		st, err := scanStatus(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan run-state history: %w", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list run-state history of %s: %w", resourceID, err)
	}
	return out, nil
}

// ResourceIDsBySource lists the DISTINCT resources that ever received an
// observation from source (the W23 detection engine's recovery pass: which
// resources have an alert-sourced run_state row — those are the up-write
// candidates when their alerts are all cleared). Ordered by resource_id —
// deterministic.
func (r *ResourceStatusRepo) ResourceIDsBySource(ctx context.Context, source domain.StatusSource) ([]string, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT DISTINCT resource_id FROM resource_status WHERE source = ? ORDER BY resource_id`,
		source.String())
	if err != nil {
		return nil, fmt.Errorf("repo: list resource ids of source %s: %w", source, err)
	}
	defer rows.Close()
	out := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("repo: scan resource id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list resource ids of source %s: %w", source, err)
	}
	return out, nil
}

// CountByRunState counts the resources per SNAPSHOT run_state (the
// whole-DB stats read — a GROUP BY over resources, no history join). The
// second return is the count of resources with NO observation (NULL —
// distinct from the explicit 'unknown' bucket); no-data-no-verdict.
func (r *ResourceStatusRepo) CountByRunState(ctx context.Context) (map[domain.RunState]int, int, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT run_state, COUNT(*) FROM resources GROUP BY run_state`)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: count resources by run_state: %w", err)
	}
	defer rows.Close()
	counts := make(map[domain.RunState]int)
	nulls := 0
	for rows.Next() {
		var state sql.NullString
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, 0, fmt.Errorf("repo: scan count: %w", err)
		}
		if !state.Valid {
			nulls = n
			continue
		}
		v, err := parseRunStateStrict(state.String)
		if err != nil {
			return nil, 0, err
		}
		counts[v] = n
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: count resources by run_state: %w", err)
	}
	return counts, nulls, nil
}
