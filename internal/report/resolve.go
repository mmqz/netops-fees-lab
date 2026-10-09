package report

import (
	"context"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// ResolveIssues converts stored domain issues into report.Issue values with
// their source provenance resolved exactly the way the report generator
// resolves them (the T6 pattern):
//
//   - a non-nil source_ref_id resolves via the source_refs row (row-phase
//     warnings on imported rows);
//   - otherwise a row-phase failure (batch_id set) carries its origin in the
//     evidence (sheet + row_number) plus the workbook name of its batch;
//   - everything else is application-generated (set-level issues).
//
// The HTTP API's GET /api/issues uses this so its items carry the identical
// JSON shape and resolution semantics as the issues inside GET /api/report.
// Issues keep their repository order (the caller's pagination order); only
// the provenance is enriched.
func ResolveIssues(ctx context.Context, db *repo.DB, issues []domain.Issue) ([]Issue, error) {
	g := &Generator{db: db}

	batches, _, err := db.Batches.List(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	workbooks := make(map[string]string, len(batches))
	for _, b := range batches {
		workbooks[b.ID] = b.WorkbookName
	}

	resources, _, err := db.Resources.Search(ctx, repo.ResourceSearchFilter{})
	if err != nil {
		return nil, err
	}
	entityKeys := make(map[string]string, len(resources))
	for _, res := range resources {
		entityKeys[res.ID] = res.NaturalKey
	}

	out := make([]Issue, 0, len(issues))
	for _, iss := range issues {
		src := g.resolveSource(ctx, iss, workbooks)
		out = append(out, Issue{
			ID:                   iss.ID,
			RuleCode:             iss.RuleCode,
			Severity:             iss.Severity.String(),
			Message:              iss.Message,
			EntityKind:           iss.EntityKind.String(),
			EntityID:             iss.EntityID,
			EntityKey:            entityKeys[iss.EntityID],
			Source:               src,
			ApplicationGenerated: src == nil,
			Evidence:             iss.Evidence,
		})
	}
	return out, nil
}
