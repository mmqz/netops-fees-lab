package repo

// workspaceoverview.go — the batched read paths of the workspace-overview
// and batch-detail surfaces (#102): the HTTP handlers used to issue one
// point query PER RESULT ROW (GET /api/workspaces ran 8 queries per row —
// 800 for a single page; the checks list loaded every result row of every
// run just to count severities; the batch detail materialized every row
// result; the set-level issue count materialized the whole issues table).
// Every method here answers ONE grouped query per CALL, independent of the
// row count, with result-shape parity to the per-item methods it replaces
// (the equivalence is test-pinned).

import (
	"context"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
)

// WorkspaceCountSummary carries the inventory counts of one workspace that
// the list endpoint renders: the three status maps the per-item
// CountByWorkspace calls returned, plus the four plain counts.
type WorkspaceCountSummary struct {
	DeviceStatus    map[domain.EntityStatus]int
	InterfaceStatus map[domain.EntityStatus]int
	LinkStatus      map[domain.EntityStatus]int
	ChangeSets      int
	Checkpoints     int
	LayerNodes      int
	LayerLinks      int
}

// CountSummariesOfWorkspaces answers the inventory counts of every listed
// workspace in a FIXED number of grouped queries (seven per call,
// independent of len(ids) — the per-row shape cost 7 queries per row).
// Unknown ids simply miss from the map; empty input answers an empty map
// without touching the database.
func (r *WorkspaceRepo) CountSummariesOfWorkspaces(ctx context.Context, ids []string) (map[string]WorkspaceCountSummary, error) {
	out := make(map[string]WorkspaceCountSummary, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ph := "(" + placeholders(len(ids)) + ")"
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	// The three status-keyed maps (devices / interfaces / links).
	type statusDest struct{ table string }
	for _, t := range []statusDest{{"virtual_devices"}, {"interfaces"}, {"links"}} {
		rows, err := r.q.QueryContext(ctx,
			"SELECT workspace_id, status, COUNT(*) FROM "+t.table+" WHERE workspace_id IN "+ph+" GROUP BY workspace_id, status", args...)
		if err != nil {
			return nil, fmt.Errorf("repo: count %s of workspaces: %w", t.table, err)
		}
		for rows.Next() {
			var wsID, status string
			var n int
			if err := rows.Scan(&wsID, &status, &n); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("repo: scan %s count: %w", t.table, err)
			}
			s := out[wsID]
			if s.DeviceStatus == nil {
				s.DeviceStatus = map[domain.EntityStatus]int{}
			}
			if s.InterfaceStatus == nil {
				s.InterfaceStatus = map[domain.EntityStatus]int{}
			}
			if s.LinkStatus == nil {
				s.LinkStatus = map[domain.EntityStatus]int{}
			}
			switch t.table {
			case "virtual_devices":
				s.DeviceStatus[domain.EntityStatus(status)] += n
			case "interfaces":
				s.InterfaceStatus[domain.EntityStatus(status)] += n
			case "links":
				s.LinkStatus[domain.EntityStatus(status)] += n
			}
			out[wsID] = s
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("repo: count %s of workspaces: %w", t.table, err)
		}
		_ = rows.Close()
	}

	// The four plain counts (the physical table names: layer members live
	// in virtual_nodes / virtual_layer_links — the repo's own CountBy
	// Workspace uses the same).
	plain := []struct {
		table string
		into  func(*WorkspaceCountSummary, int)
	}{
		{"changesets", func(s *WorkspaceCountSummary, n int) { s.ChangeSets = n }},
		{"checkpoints", func(s *WorkspaceCountSummary, n int) { s.Checkpoints = n }},
		{"virtual_nodes", func(s *WorkspaceCountSummary, n int) { s.LayerNodes = n }},
		{"virtual_layer_links", func(s *WorkspaceCountSummary, n int) { s.LayerLinks = n }},
	}
	for _, t := range plain {
		rows, err := r.q.QueryContext(ctx,
			"SELECT workspace_id, COUNT(*) FROM "+t.table+" WHERE workspace_id IN "+ph+" GROUP BY workspace_id", args...)
		if err != nil {
			return nil, fmt.Errorf("repo: count %s of workspaces: %w", t.table, err)
		}
		for rows.Next() {
			var wsID string
			var n int
			if err := rows.Scan(&wsID, &n); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("repo: scan %s count: %w", t.table, err)
			}
			s := out[wsID]
			t.into(&s, n)
			out[wsID] = s
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("repo: count %s of workspaces: %w", t.table, err)
		}
		_ = rows.Close()
	}
	return out, nil
}

// LatestByWorkspaces answers the newest check run of every listed workspace
// (the batched Latest): one query, the per-workspace top-1 by the SAME
// simIDOrderDesc ordering the per-item method uses. Workspaces without a
// run simply miss from the map (the per-item Latest answered ErrNotFound,
// which the handler treated as "no run yet" — map-miss carries that).
func (r *CheckRunRepo) LatestByWorkspaces(ctx context.Context, ids []string) (map[string]domain.CheckRun, error) {
	out := make(map[string]domain.CheckRun, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	ph := "(" + placeholders(len(ids)) + ")"
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	// Top-1 per workspace: no run of the same workspace is strictly newer
	// under (LENGTH(id), id) DESC — exactly Latest's ORDER BY as a
	// correlated NOT EXISTS (SQLite window functions are available, but the
	// NOT EXISTS keeps the same ORDER BY expression semantics verbatim).
	rows, err := r.q.QueryContext(ctx,
		"SELECT "+checkRunColumns+" FROM check_runs c WHERE c.workspace_id IN "+ph+
			" AND NOT EXISTS (SELECT 1 FROM check_runs c2 WHERE c2.workspace_id = c.workspace_id"+
			" AND (LENGTH(c2.id) > LENGTH(c.id) OR (LENGTH(c2.id) = LENGTH(c.id) AND c2.id > c.id)))", args...)
	if err != nil {
		return nil, fmt.Errorf("repo: latest check runs of workspaces: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		run, err := scanCheckRun(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan latest check run: %w", err)
		}
		out[run.WorkspaceID] = run
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: latest check runs of workspaces: %w", err)
	}
	return out, nil
}

// SeverityCountsByRuns answers the FAILED-result severity counts of every
// listed run in ONE grouped query (the per-row alternative loads every
// result row of every run — GetResults — just to count severities; passed
// rows never count, so the passed = 0 filter reproduces sevCountsOfResults
// exactly). Runs without failed results miss from the map (zero counts).
func (r *CheckRunRepo) SeverityCountsByRuns(ctx context.Context, runIDs []string) (map[string]map[domain.Severity]int, error) {
	out := make(map[string]map[domain.Severity]int, len(runIDs))
	if len(runIDs) == 0 {
		return out, nil
	}
	ph := "(" + placeholders(len(runIDs)) + ")"
	args := make([]any, len(runIDs))
	for i, id := range runIDs {
		args[i] = id
	}
	rows, err := r.q.QueryContext(ctx,
		"SELECT run_id, severity, COUNT(*) FROM check_results WHERE run_id IN "+ph+
			" AND passed = 0 GROUP BY run_id, severity", args...)
	if err != nil {
		return nil, fmt.Errorf("repo: severity counts of check runs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var runID, sev string
		var n int
		if err := rows.Scan(&runID, &sev, &n); err != nil {
			return nil, fmt.Errorf("repo: scan severity count: %w", err)
		}
		if out[runID] == nil {
			out[runID] = map[domain.Severity]int{}
		}
		out[runID][domain.Severity(sev)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: severity counts of check runs: %w", err)
	}
	return out, nil
}

// SheetStatusCount is one (sheet, status) cell of a batch's row-result
// distribution — the GROUP BY pushdown of the batch-detail per-sheet
// summary (#102-C: the handler materialized EVERY row result of the batch,
// up to 100k rows, to count four numbers per sheet).
type SheetStatusCount struct {
	Sheet  string
	Status domain.RowStatus
	Count  int
}

// CountBySheetStatus answers the (sheet, status) distribution of one
// batch's row results in one grouped query.
func (r *RowResultRepo) CountBySheetStatus(ctx context.Context, batchID string) ([]SheetStatusCount, error) {
	rows, err := r.q.QueryContext(ctx,
		"SELECT sheet, status, COUNT(*) FROM import_row_results WHERE batch_id = ? GROUP BY sheet, status", batchID)
	if err != nil {
		return nil, fmt.Errorf("repo: count row results of batch %s by sheet/status: %w", batchID, err)
	}
	defer rows.Close()
	out := []SheetStatusCount{}
	for rows.Next() {
		var c SheetStatusCount
		var status string
		if err := rows.Scan(&c.Sheet, &status, &c.Count); err != nil {
			return nil, fmt.Errorf("repo: scan row result count of batch %s: %w", batchID, err)
		}
		c.Status = domain.RowStatus(status)
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: count row results of batch %s by sheet/status: %w", batchID, err)
	}
	return out, nil
}

// CountSetLevelBySeverity answers the severity distribution of the SET-LEVEL
// issues (batch_id IS NULL) in one grouped query (#102-D: both the HTTP
// validate summary and the CLI twin materialized the WHOLE issues table and
// filtered in memory).
func (r *IssueRepo) CountSetLevelBySeverity(ctx context.Context) (map[domain.Severity]int, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT severity, COUNT(*) FROM issues WHERE batch_id IS NULL GROUP BY severity`)
	if err != nil {
		return nil, fmt.Errorf("repo: count set-level issues by severity: %w", err)
	}
	defer rows.Close()
	counts := make(map[domain.Severity]int)
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, fmt.Errorf("repo: scan set-level issue count: %w", err)
		}
		counts[domain.Severity(s)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: count set-level issues by severity: %w", err)
	}
	return counts, nil
}
