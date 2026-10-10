package sim

import (
	"context"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
)

// UndoResult is the response of a successful Undo: the NEW changeset that
// carried the inverse ops (trigger "undo"), the ORIGINAL changeset it
// reverted, and the post-undo workspace hash plus the auto-check summary
// (when requested).
type UndoResult struct {
	ChangeSetID   string      `json:"changesetId"` // the NEW undo changeset
	Trigger       string      `json:"trigger"`     // "undo"
	Reverted      string      `json:"reverted"`    // the ORIGINAL changeset id
	OpsApplied    int         `json:"opsApplied"`
	WorkspaceHash string      `json:"workspaceHash"`
	Checks        *RunSummary `json:"checks"`
}

// Undo reverts one changeset of a workspace (PROJECT_BRIEF_P2 §4 "Undo",
// tier 2 of the three-tier undo): the target changeset's recorded inverse
// ops are applied in REVERSE seq order as ONE NEW changeset (trigger "undo",
// description "undo of <id>", restore ops allowed — the engine's
// applyInverses path), and the target is marked status 'reverted' with
// reverted_at = now IN THE SAME transaction as the undo changeset (AUD1-8:
// the marker used to land in a second transaction, leaving a crash window
// where the undo effects were live while the target still read 'applied';
// now the undo changeset and the marker commit atomically — a failure of
// either rolls both back). The undo changeset records its own computed
// inverses, so undo-of-undo works (undo the undo changeset itself).
//
// changesetID "" targets the workspace's latest applied changeset. Errors:
// unknown workspace/changeset (or a changeset of another workspace) ⇒ an
// error wrapping domain.ErrNotFound; no applied changeset at all ⇒
// domain.ErrNotFound; already-reverted or failed target ⇒ domain.ErrConflict
// (there is nothing to undo); a LATER APPLIED forward changeset exists ⇒
// domain.ErrNotLatest — undo is LIFO.
//
// LIFO rule: a later applied changeset with trigger "user" or
// "checkpoint_restore" blocks the undo (its effects may depend on the
// target's). Failed changesets never block (nothing was applied), reverted
// ones never block (already compensated), and undo changesets themselves
// never block: after undo(cs-2), the applied undo changeset cs-3 is merely
// the recorded reversion of cs-2, so cs-1 — now the newest still-effective
// forward change — is the LIFO head again and may be undone next. Undoing
// the undo changeset itself (it IS the latest applied) is how a revert is
// reverted: that re-applies the original ops.
//
// runChecks (default true at call sites) runs the auto-checks attached to
// the undo changeset (trigger auto_after_ops).
func (s *Service) Undo(ctx context.Context, workspaceID, changesetID string, runChecks bool) (UndoResult, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return UndoResult{}, err
	}

	// Resolve the target: "" ⇒ the latest applied changeset of the workspace.
	targetID := changesetID
	if targetID == "" {
		latest, err := s.db.ChangeSets.LatestApplied(ctx, workspaceID)
		if err != nil {
			return UndoResult{}, fmt.Errorf("sim: undo: no changeset to revert: %w", err)
		}
		targetID = latest.ID
	}
	target, err := s.db.ChangeSets.GetByID(ctx, targetID)
	if err != nil {
		return UndoResult{}, fmt.Errorf("sim: undo %s: %w", targetID, err)
	}
	if target.WorkspaceID != workspaceID {
		return UndoResult{}, fmt.Errorf("sim: undo %s: %w", targetID, domain.ErrNotFound)
	}
	switch target.Status {
	case domain.ChangeSetStatusReverted:
		return UndoResult{}, fmt.Errorf("sim: undo %s: changeset already reverted at %s: %w",
			targetID, target.RevertedAt.Format("2006-01-02T15:04:05Z"), domain.ErrConflict)
	case domain.ChangeSetStatusFailed:
		return UndoResult{}, fmt.Errorf("sim: undo %s: changeset failed, nothing was applied: %w",
			targetID, domain.ErrConflict)
	case domain.ChangeSetStatusApplied:
	default:
		return UndoResult{}, fmt.Errorf("sim: undo %s: unknown changeset status %q", targetID, target.Status)
	}

	// LIFO: no later still-effective FORWARD changeset may exist. Failed and
	// reverted changesets do not block; undo changesets do not block either
	// (they are the recorded reversions of already-dead forward changesets).
	if blocker, err := s.undoBlocker(ctx, workspaceID, targetID); err != nil {
		return UndoResult{}, err
	} else if blocker != "" {
		return UndoResult{}, fmt.Errorf("sim: undo %s: later applied changeset %s exists (undo is LIFO): %w",
			targetID, blocker, domain.ErrNotLatest)
	}

	// Decode the recorded inverses and apply them in REVERSE seq order as
	// one new changeset through the engine's own apply path.
	ops, err := s.db.ChangeSets.GetOps(ctx, targetID)
	if err != nil {
		return UndoResult{}, fmt.Errorf("sim: undo %s: load ops: %w", targetID, err)
	}
	if len(ops) == 0 {
		return UndoResult{}, fmt.Errorf("sim: undo %s: changeset has no op rows: %w", targetID, domain.ErrConflict)
	}
	inverses := make([]internalOp, 0, len(ops))
	for i := len(ops) - 1; i >= 0; i-- {
		row := ops[i]
		if row.InverseJSON == "" {
			return UndoResult{}, fmt.Errorf("sim: undo %s: op %d (%s) has no recorded inverse: %w",
				targetID, row.Seq, row.OpCode, domain.ErrConflict)
		}
		inv, err := decodeInverseOp(row.InverseJSON)
		if err != nil {
			return UndoResult{}, fmt.Errorf("sim: undo %s: op %d: %w", targetID, row.Seq, err)
		}
		inverses = append(inverses, inv)
	}

	applied, err := s.applyInverses(ctx, workspaceID, inverses, domain.ChangeSetTriggerUndo,
		fmt.Sprintf("undo of %s", targetID), runChecks, targetID)
	if err != nil {
		return UndoResult{}, fmt.Errorf("sim: undo %s: %w", targetID, err)
	}

	return UndoResult{
		ChangeSetID:   applied.ChangeSetID,
		Trigger:       applied.Trigger,
		Reverted:      targetID,
		OpsApplied:    applied.OpsApplied,
		WorkspaceHash: applied.WorkspaceHash,
		Checks:        applied.Checks,
	}, nil
}

// undoBlocker returns the id of the changeset that blocks the undo of
// targetID ("" when the LIFO rule is satisfied): the newest applied changeset
// with a FORWARD trigger (user / checkpoint_restore) and a numerically
// greater id than the target. Failed changesets never block (§4: "a trailing
// failed changeset does not block"), reverted ones never block, and undo
// changesets never block — see the Undo doc comment.
func (s *Service) undoBlocker(ctx context.Context, workspaceID, targetID string) (string, error) {
	changesets, _, err := s.db.ChangeSets.ListByWorkspace(ctx, workspaceID, 1, 0) // newest first, unlimited
	if err != nil {
		return "", fmt.Errorf("sim: undo: list changesets of workspace %s: %w", workspaceID, err)
	}
	var blocker string
	for _, cs := range changesets {
		if cs.Status != domain.ChangeSetStatusApplied {
			continue
		}
		if cs.Trigger == domain.ChangeSetTriggerUndo {
			continue
		}
		if simIDLess(targetID, cs.ID) { // cs.ID > targetID numerically
			blocker = cs.ID // newest first ⇒ first hit is the newest blocker
			break
		}
	}
	return blocker, nil
}
