// Package governance implements the governance overview aggregation of
// NetOps Lab (W14-D): ONE read-only endpoint's worth of shape — the pending
// confirmation queues (attribute changes, rename proposals), the decision
// audit counters, the issue distribution (by severity, by rule), and the
// resource / relation / batch inventory shapes. It is the entry view for
// governance work: "what is waiting for me, what have I decided, what is
// wrong with the data, what does the inventory look like".
//
// Every count is a SQL COUNT aggregate (GROUP BY) served by the repository
// layer — no full-table loads, no len() over fetched rows. The package
// performs no writes.
package governance

import (
	"context"
	"fmt"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// TopRuleLimit pins the number of rules in issues.byRule (the top of the
// by-rule distribution — count DESC, rule ASC, capped at 10).
const TopRuleLimit = 10

// Service aggregates the governance summary.
type Service struct {
	db *repo.DB
}

// New returns a Service bound to db.
func New(db *repo.DB) *Service {
	return &Service{db: db}
}

// RuleCount is one entry of issues.byRule: {rule, count}.
type RuleCount struct {
	Rule  string `json:"rule"`
	Count int    `json:"count"`
}

// PendingCounts is the pending block: the two human-confirmation queues.
type PendingCounts struct {
	AttributeChanges int `json:"attributeChanges"`
	RenameProposals  int `json:"renameProposals"`
}

// DecidedCounts is the decision-audit block: terminal-state counters of the
// two confirmation flows. The maps always carry the four fixed keys
// (applied/dismissed for changes, applied/rejected for renames — 0 when
// absent) so the shape is stable for an empty database.
type DecidedCounts struct {
	AttributeChanges map[string]int `json:"attributeChanges"`
	RenameProposals  map[string]int `json:"renameProposals"`
}

// IssuesSummary is the issue distribution over the WHOLE table (row-phase
// and set-level issues alike): bySeverity always carries the three known
// severities (0 when absent); byRule is the deterministic top list
// (count DESC, rule ASC, TopRuleLimit entries) and is [] on an empty table.
type IssuesSummary struct {
	BySeverity map[string]int `json:"bySeverity"`
	ByRule     []RuleCount    `json:"byRule"`
}

// ResourcesSummary is the resource inventory shape: the total and the
// per-type counts (keys = type literals actually present; {} on an empty
// database).
type ResourcesSummary struct {
	Total  int            `json:"total"`
	ByType map[string]int `json:"byType"`
}

// RelationsSummary is the relation inventory shape: the total and the
// per-relation-type counts (keys = type literals actually present).
type RelationsSummary struct {
	Total  int            `json:"total"`
	ByType map[string]int `json:"byType"`
}

// BatchesSummary is the import-batch shape: the total and the per-status
// counts (keys = status literals actually present).
type BatchesSummary struct {
	Total    int            `json:"total"`
	ByStatus map[string]int `json:"byStatus"`
}

// Summary is the shape of GET /api/governance/summary / `query governance`:
// {pending, decided, issues, resources, relations, batches, generatedAt}.
//
// Determinism: every field except GeneratedAt is a pure function of the
// database state — two summaries of the same database are field-by-field
// equal (maps are Go maps, whose JSON encoding sorts keys, so even the map
// serialization is deterministic). GeneratedAt is time.Now().UTC() by
// contract (a wall-clock stamp, deliberately NOT asserted by the
// determinism tests).
type Summary struct {
	Pending     PendingCounts    `json:"pending"`
	Decided     DecidedCounts    `json:"decided"`
	Issues      IssuesSummary    `json:"issues"`
	Resources   ResourcesSummary `json:"resources"`
	Relations   RelationsSummary `json:"relations"`
	Batches     BatchesSummary   `json:"batches"`
	GeneratedAt time.Time        `json:"generatedAt"`
}

// Summary computes the governance aggregate. An empty database answers the
// all-zero structure (scalars 0, fixed map keys 0, empty dynamic maps,
// byRule []) — never an error and never a 204/404.
func (s *Service) Summary(ctx context.Context) (Summary, error) {
	out := Summary{
		Decided: DecidedCounts{
			AttributeChanges: map[string]int{
				domain.ChangeStatusApplied.String():   0,
				domain.ChangeStatusDismissed.String(): 0,
			},
			RenameProposals: map[string]int{
				domain.RenameStatusApplied.String():  0,
				domain.RenameStatusRejected.String(): 0,
			},
		},
		Issues: IssuesSummary{
			BySeverity: map[string]int{
				domain.SeverityError.String():   0,
				domain.SeverityWarning.String(): 0,
				domain.SeverityInfo.String():    0,
			},
			ByRule: []RuleCount{},
		},
		Resources: ResourcesSummary{ByType: map[string]int{}},
		Relations: RelationsSummary{ByType: map[string]int{}},
		Batches:   BatchesSummary{ByStatus: map[string]int{}},
	}

	// The two confirmation queues: pending counters + decision audit.
	changesByStatus, err := s.db.AttributeChanges.CountByStatus(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("governance: count attribute changes by status: %w", err)
	}
	out.Pending.AttributeChanges = changesByStatus[domain.ChangeStatusPending]
	out.Decided.AttributeChanges[domain.ChangeStatusApplied.String()] = changesByStatus[domain.ChangeStatusApplied]
	out.Decided.AttributeChanges[domain.ChangeStatusDismissed.String()] = changesByStatus[domain.ChangeStatusDismissed]

	renamesByStatus, err := s.db.RenameProposals.CountByStatus(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("governance: count rename proposals by status: %w", err)
	}
	out.Pending.RenameProposals = renamesByStatus[domain.RenameStatusPending]
	out.Decided.RenameProposals[domain.RenameStatusApplied.String()] = renamesByStatus[domain.RenameStatusApplied]
	out.Decided.RenameProposals[domain.RenameStatusRejected.String()] = renamesByStatus[domain.RenameStatusRejected]

	// Issue distribution: whole table (nil batchID = all issues, row-phase
	// and set-level alike); byRule capped and ordered by the repo method.
	issuesBySeverity, err := s.db.Issues.CountBySeverity(ctx, nil)
	if err != nil {
		return Summary{}, fmt.Errorf("governance: count issues by severity: %w", err)
	}
	for sev, n := range issuesBySeverity {
		out.Issues.BySeverity[sev.String()] = n
	}
	byRule, err := s.db.Issues.CountByRule(ctx, TopRuleLimit)
	if err != nil {
		return Summary{}, fmt.Errorf("governance: count issues by rule: %w", err)
	}
	for _, rc := range byRule {
		out.Issues.ByRule = append(out.Issues.ByRule, RuleCount{Rule: rc.RuleCode, Count: rc.Count})
	}

	// Inventory shapes: per-type maps + the total as the sum of the map.
	resByType, err := s.db.Resources.CountByType(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("governance: count resources by type: %w", err)
	}
	for t, n := range resByType {
		out.Resources.ByType[t.String()] = n
		out.Resources.Total += n
	}

	relByType, err := s.db.Relations.CountByType(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("governance: count relations by type: %w", err)
	}
	for t, n := range relByType {
		out.Relations.ByType[t.String()] = n
		out.Relations.Total += n
	}

	batchesByStatus, err := s.db.Batches.CountByStatus(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("governance: count batches by status: %w", err)
	}
	for st, n := range batchesByStatus {
		out.Batches.ByStatus[st.String()] = n
		out.Batches.Total += n
	}

	out.GeneratedAt = time.Now().UTC()
	return out, nil
}
