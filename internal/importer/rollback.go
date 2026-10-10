package importer

import (
	"context"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// ErrBatchInUse reports the §8/§9 rollback refusal: entities created by the
// batch are referenced (as relation endpoints) by entities of other batches.
// It wraps domain.ErrConflict (so generic conflict handling keeps working)
// and lets the HTTP layer label exactly this case 409 batch_in_use — a
// non-completed batch answers a plain conflict instead.
var ErrBatchInUse = fmt.Errorf("batch entities are referenced by relations of other batches: %w", domain.ErrConflict)

// Rollback reverses a completed batch (PROJECT_BRIEF §8): all resources,
// relations, source refs, row results and issues it created are deleted and
// the batch becomes rolled_back, which enables clean re-import workflows.
//
// Only completed batches can be rolled back (any other status refuses with
// an error wrapping domain.ErrConflict). If any entity created by the
// batch is referenced (as a relation endpoint) by entities of other batches,
// the rollback is refused with ErrBatchInUse (wrapping domain.ErrConflict;
// the caller maps it to HTTP 409 batch_in_use) and nothing is deleted.
//
// The status check, the in-use check and the deletions all run inside ONE
// transaction (BEGIN IMMEDIATE via DB.Do — see repo.Open). The write lock is
// taken before the status and in-use checks and held until commit, so a
// concurrent import cannot insert a relation referencing this batch's
// entities between the check and the deletions: check and delete are
// strictly serialized with every other write transaction (this closes the
// check-outside-transaction TOCTOU window, R2-P2-1). Either the whole batch
// is rolled back or nothing changes.
func (s *Service) Rollback(ctx context.Context, batchID string) error {
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		batch, err := tx.Batches.Get(ctx, batchID)
		if err != nil {
			return fmt.Errorf("importer: load batch %s: %w", batchID, err)
		}
		if batch.Status != domain.BatchStatusCompleted {
			return fmt.Errorf("importer: rollback batch %s: status %s, want %s: %w",
				batchID, batch.Status, domain.BatchStatusCompleted, domain.ErrConflict)
		}
		resources, err := tx.Resources.ListByBatch(ctx, batchID)
		if err != nil {
			return fmt.Errorf("importer: list resources of batch %s: %w", batchID, err)
		}
		ids := make([]string, len(resources))
		for i, r := range resources {
			ids[i] = r.ID
		}
		inUse, err := tx.Relations.ExistsFromOtherBatch(ctx, ids, batchID)
		if err != nil {
			return fmt.Errorf("importer: in-use check for batch %s: %w", batchID, err)
		}
		// Test-only observation point (see export_test.go): fires right
		// after the in-use check, while the write transaction (and its
		// BEGIN IMMEDIATE lock) is held. Nil in production.
		if s.rollbackCheckHook != nil {
			if err := s.rollbackCheckHook(); err != nil {
				return err
			}
		}
		if inUse {
			return fmt.Errorf("importer: rollback batch %s: %w", batchID, ErrBatchInUse)
		}
		// Relations first: this batch's relations may point at other
		// batches' resources (no cascade there). Deleting this batch's
		// resources then cascades any relations that survive — none, per
		// the in-use check above.
		if _, err := tx.Relations.DeleteByBatch(ctx, batchID); err != nil {
			return fmt.Errorf("importer: rollback batch %s: %w", batchID, err)
		}
		if _, err := tx.Resources.DeleteByBatch(ctx, batchID); err != nil {
			return fmt.Errorf("importer: rollback batch %s: %w", batchID, err)
		}
		if _, err := tx.SourceRefs.DeleteByBatch(ctx, batchID); err != nil {
			return fmt.Errorf("importer: rollback batch %s: %w", batchID, err)
		}
		if _, err := tx.RowResults.DeleteByBatch(ctx, batchID); err != nil {
			return fmt.Errorf("importer: rollback batch %s: %w", batchID, err)
		}
		if _, err := tx.Issues.DeleteByBatch(ctx, batchID); err != nil {
			return fmt.Errorf("importer: rollback batch %s: %w", batchID, err)
		}
		if err := tx.Batches.UpdateStatus(ctx, batchID, domain.BatchStatusRolledBack); err != nil {
			return fmt.Errorf("importer: rollback batch %s: %w", batchID, err)
		}
		return nil
	})
	// fn errors are already fully contextualized (and DB.Do preserves the
	// original error, including its sentinels, through a rollback).
	if err != nil {
		return err
	}
	return nil
}
