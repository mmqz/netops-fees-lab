package importer

import (
	"context"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// Rollback reverses a completed batch (PROJECT_BRIEF §8): all resources,
// relations, source refs, row results and issues it created are deleted and
// the batch becomes rolled_back, which enables clean re-import workflows.
//
// Only completed batches can be rolled back. If any entity created by the
// batch is referenced (as a relation endpoint) by entities of other batches,
// the rollback is refused with an error wrapping domain.ErrConflict (the
// caller maps it to HTTP 409 batch_in_use) and nothing is deleted. The
// deletions themselves run in one transaction: either the whole batch is
// rolled back or nothing changes.
func (s *Service) Rollback(ctx context.Context, batchID string) error {
	batch, err := s.db.Batches.Get(ctx, batchID)
	if err != nil {
		return fmt.Errorf("importer: load batch %s: %w", batchID, err)
	}
	if batch.Status != domain.BatchStatusCompleted {
		return fmt.Errorf("importer: rollback batch %s: status %s, want %s: %w",
			batchID, batch.Status, domain.BatchStatusCompleted, domain.ErrConflict)
	}
	resources, err := s.db.Resources.ListByBatch(ctx, batchID)
	if err != nil {
		return fmt.Errorf("importer: list resources of batch %s: %w", batchID, err)
	}
	ids := make([]string, len(resources))
	for i, r := range resources {
		ids[i] = r.ID
	}
	inUse, err := s.db.Relations.ExistsFromOtherBatch(ctx, ids, batchID)
	if err != nil {
		return fmt.Errorf("importer: in-use check for batch %s: %w", batchID, err)
	}
	if inUse {
		return fmt.Errorf("importer: rollback batch %s: entities are referenced by relations of other batches: %w",
			batchID, domain.ErrConflict)
	}
	err = s.db.Do(ctx, func(tx *repo.Tx) error {
		// Relations first: this batch's relations may point at other
		// batches' resources (no cascade there). Deleting this batch's
		// resources then cascades any relations that survive — none, per
		// the in-use check above.
		if _, err := tx.Relations.DeleteByBatch(ctx, batchID); err != nil {
			return err
		}
		if _, err := tx.Resources.DeleteByBatch(ctx, batchID); err != nil {
			return err
		}
		if _, err := tx.SourceRefs.DeleteByBatch(ctx, batchID); err != nil {
			return err
		}
		if _, err := tx.RowResults.DeleteByBatch(ctx, batchID); err != nil {
			return err
		}
		if _, err := tx.Issues.DeleteByBatch(ctx, batchID); err != nil {
			return err
		}
		if err := tx.Batches.UpdateStatus(ctx, batchID, domain.BatchStatusRolledBack); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("importer: rollback batch %s: %w", batchID, err)
	}
	return nil
}
