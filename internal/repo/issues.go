package repo

// extend via new methods; do not rewrite.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
)

// IssueRepo persists data-quality issues.
type IssueRepo struct{ q DBTX }

// issueColumns is the canonical SELECT column list of issues.
const issueColumns = `id, batch_id, rule_code, rule_version, severity, entity_kind, entity_id, source_ref_id, message, evidence, created_at`

// scanIssue scans one issues row into a domain.Issue.
func scanIssue(s scanner) (domain.Issue, error) {
	var (
		iss          domain.Issue
		batchID      sql.NullString
		severity     string
		entityKind   sql.NullString
		entityID     sql.NullString
		sourceRefID  sql.NullString
		evidenceJSON string
		createdAt    string
	)
	err := s.Scan(&iss.ID, &batchID, &iss.RuleCode, &iss.RuleVersion, &severity, &entityKind,
		&entityID, &sourceRefID, &iss.Message, &evidenceJSON, &createdAt)
	if err != nil {
		return domain.Issue{}, err
	}
	iss.BatchID = stringPtrFromNull(batchID)
	iss.Severity = domain.Severity(severity)
	iss.EntityKind = domain.EntityKind(stringFromNull(entityKind))
	iss.EntityID = stringFromNull(entityID)
	iss.SourceRefID = stringPtrFromNull(sourceRefID)
	iss.Evidence, err = mapFromJSON(evidenceJSON)
	if err != nil {
		return domain.Issue{}, err
	}
	if iss.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.Issue{}, err
	}
	return iss, nil
}

// Insert adds a new issue. EntityKind/EntityID are stored as NULL when
// empty; BatchID and SourceRefID as NULL when nil.
func (r *IssueRepo) Insert(ctx context.Context, iss domain.Issue) error {
	evidence, err := mapJSON(iss.Evidence)
	if err != nil {
		return err
	}
	_, err = r.q.ExecContext(ctx, `INSERT INTO issues
(id, batch_id, rule_code, rule_version, severity, entity_kind, entity_id, source_ref_id, message, evidence, created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		iss.ID, nullablePtr(iss.BatchID), iss.RuleCode, iss.RuleVersion, iss.Severity.String(),
		nullString(iss.EntityKind.String()), nullString(iss.EntityID), nullablePtr(iss.SourceRefID),
		iss.Message, evidence, formatTime(iss.CreatedAt))
	if err != nil {
		return fmt.Errorf("repo: insert issue %s: %w", iss.ID, err)
	}
	return nil
}

// DeleteByBatch deletes all issues attributed to batchID (row-phase
// issues) and returns the number of deleted rows. Set-level issues
// (batch_id NULL) are untouched; use DeleteSetLevel for those.
func (r *IssueRepo) DeleteByBatch(ctx context.Context, batchID string) (int64, error) {
	res, err := r.q.ExecContext(ctx, `DELETE FROM issues WHERE batch_id = ?`, batchID)
	if err != nil {
		return 0, fmt.Errorf("repo: delete issues of batch %s: %w", batchID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: delete issues of batch %s: rows affected: %w", batchID, err)
	}
	return n, nil
}

// DeleteSetLevel deletes all set-level issues (batch_id NULL) whose
// rule_code is one of domain.SetLevelRuleCodes (E005, E006, I001) and
// returns the number of deleted rows. The validation engine calls this
// before re-inserting fresh set-level results.
func (r *IssueRepo) DeleteSetLevel(ctx context.Context) (int64, error) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(domain.SetLevelRuleCodes)), ",")
	res, err := r.q.ExecContext(ctx,
		`DELETE FROM issues WHERE batch_id IS NULL AND rule_code IN (`+ph+`)`,
		stringsToAny(domain.SetLevelRuleCodes)...)
	if err != nil {
		return 0, fmt.Errorf("repo: delete set-level issues: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("repo: delete set-level issues: rows affected: %w", err)
	}
	return n, nil
}

// IssueFilter selects issues. Empty fields mean "no filter"; a nil BatchID
// means "all batches including set-level issues"; a non-nil BatchID
// restricts to that batch. Limit <= 0 means unlimited; offset < 0 means 0.
type IssueFilter struct {
	BatchID  *string
	Severity domain.Severity
	RuleCode string
	Limit    int
	Offset   int
}

// List returns issues matching f plus the total number of matches
// (ignoring limit/offset), ordered by (rule_code, id).
func (r *IssueRepo) List(ctx context.Context, f IssueFilter) ([]domain.Issue, int, error) {
	where := []string{"1=1"}
	args := []any{}
	if f.BatchID != nil {
		where = append(where, "batch_id = ?")
		args = append(args, *f.BatchID)
	}
	if f.Severity != "" {
		if !f.Severity.IsValid() {
			return nil, 0, fmt.Errorf("repo: list issues: invalid severity %q", f.Severity)
		}
		where = append(where, "severity = ?")
		args = append(args, f.Severity.String())
	}
	if f.RuleCode != "" {
		where = append(where, "rule_code = ?")
		args = append(args, f.RuleCode)
	}
	whereSQL := strings.Join(where, " AND ")

	var total int
	if err := r.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM issues WHERE `+whereSQL, args...).
		Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count issues: %w", err)
	}
	limit, offset := paging(f.Limit, f.Offset)
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+issueColumns+` FROM issues WHERE `+whereSQL+` ORDER BY rule_code, id LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list issues: %w", err)
	}
	defer rows.Close()
	out := make([]domain.Issue, 0)
	for rows.Next() {
		iss, err := scanIssue(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("repo: scan issue: %w", err)
		}
		out = append(out, iss)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list issues: %w", err)
	}
	return out, total, nil
}

// CountBySeverity returns the number of issues per severity. A nil batchID
// counts all issues (including set-level); a non-nil batchID counts only
// that batch's issues.
func (r *IssueRepo) CountBySeverity(ctx context.Context, batchID *string) (map[domain.Severity]int, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if batchID != nil {
		rows, err = r.q.QueryContext(ctx,
			`SELECT severity, COUNT(*) FROM issues WHERE batch_id = ? GROUP BY severity`, *batchID)
	} else {
		rows, err = r.q.QueryContext(ctx, `SELECT severity, COUNT(*) FROM issues GROUP BY severity`)
	}
	if err != nil {
		return nil, fmt.Errorf("repo: count issues by severity: %w", err)
	}
	defer rows.Close()
	counts := make(map[domain.Severity]int)
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, fmt.Errorf("repo: scan count: %w", err)
		}
		counts[domain.Severity(s)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: count issues by severity: %w", err)
	}
	return counts, nil
}

// CountByBatch returns the number of row-phase issues attributed to
// batchID (COUNT aggregate; set-level issues with batch_id NULL are
// excluded — same scope as List with IssueFilter{BatchID}). The rollback
// route's delete-response accounting uses it instead of a full List.
func (r *IssueRepo) CountByBatch(ctx context.Context, batchID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM issues WHERE batch_id = ?`, batchID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count issues of batch %s: %w", batchID, err)
	}
	return n, nil
}

// stringsToAny converts []string to []any for query arguments.
func stringsToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
