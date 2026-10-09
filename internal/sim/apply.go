package sim

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// internalOp is the engine's typed op representation: exactly one payload
// is set according to code(). Forward ops carry the external Op payload;
// restore ops carry their typed snapshot payloads (engine-internal only —
// they never pass an external boundary).
type internalOp struct {
	forward          Op
	restoreDevice    *RestoreDeviceOp
	restoreInterface *RestoreInterfaceOp
	restoreLink      *RestoreLinkOp
}

// forwardOp wraps an external forward op payload as an internalOp.
func forwardOp(op Op) internalOp {
	return internalOp{forward: op}
}

// code returns the op code of the payload.
func (o internalOp) code() string {
	switch {
	case o.restoreDevice != nil:
		return o.restoreDevice.code()
	case o.restoreInterface != nil:
		return o.restoreInterface.code()
	case o.restoreLink != nil:
		return o.restoreLink.code()
	default:
		return o.forward.Op
	}
}

// isRestore reports whether the payload is one of the three engine-internal
// restore ops.
func (o internalOp) isRestore() bool {
	return o.restoreDevice != nil || o.restoreInterface != nil || o.restoreLink != nil
}

// opJSON returns the canonical typed JSON of the payload — the value stored
// in changeset_ops.op_json (the forward op as received, or the restore
// payload of an undo changeset).
func (o internalOp) opJSON() (string, error) {
	switch {
	case o.restoreDevice != nil:
		return o.restoreDevice.opJSON()
	case o.restoreInterface != nil:
		return o.restoreInterface.opJSON()
	case o.restoreLink != nil:
		return o.restoreLink.opJSON()
	default:
		b, err := json.Marshal(o.forward)
		if err != nil {
			return "", fmt.Errorf("sim: marshal op %s: %w", o.forward.Op, err)
		}
		return string(b), nil
	}
}

// decodeInverseOp decodes one inverse_json value of a changeset op row back
// into an internalOp (forward inverses decode into the Op payload; restore
// inverses decode into their typed snapshot structs). The undo path (P2-T3)
// uses it to rebuild the op sequence it reverses.
func decodeInverseOp(raw string) (internalOp, error) {
	if raw == "" {
		return internalOp{}, fmt.Errorf("sim: decode inverse: empty JSON")
	}
	var head struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal([]byte(raw), &head); err != nil {
		return internalOp{}, fmt.Errorf("sim: decode inverse: %w", err)
	}
	switch head.Op {
	case domain.OpRestoreDevice:
		var r RestoreDeviceOp
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return internalOp{}, fmt.Errorf("sim: decode restore_device: %w", err)
		}
		if r.Op != domain.OpRestoreDevice {
			return internalOp{}, fmt.Errorf("sim: decode restore_device: op code %q", r.Op)
		}
		return internalOp{restoreDevice: &r}, nil
	case domain.OpRestoreInterface:
		var r RestoreInterfaceOp
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return internalOp{}, fmt.Errorf("sim: decode restore_interface: %w", err)
		}
		if r.Op != domain.OpRestoreInterface {
			return internalOp{}, fmt.Errorf("sim: decode restore_interface: op code %q", r.Op)
		}
		return internalOp{restoreInterface: &r}, nil
	case domain.OpRestoreLink:
		var r RestoreLinkOp
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return internalOp{}, fmt.Errorf("sim: decode restore_link: %w", err)
		}
		if r.Op != domain.OpRestoreLink {
			return internalOp{}, fmt.Errorf("sim: decode restore_link: op code %q", r.Op)
		}
		return internalOp{restoreLink: &r}, nil
	default:
		var op Op
		dec := newDecoder([]byte(raw))
		if err := dec.Decode(&op); err != nil {
			return internalOp{}, fmt.Errorf("sim: decode inverse op %q: %w", head.Op, err)
		}
		if !domain.IsForwardOp(op.Op) {
			return internalOp{}, fmt.Errorf("sim: decode inverse: op code %q is neither forward nor restore", op.Op)
		}
		return internalOp{forward: op}, nil
	}
}

// ApplyResult is the response of a successful ApplyOps (and of the internal
// undo/checkpoint-restore paths that reuse the changeset machinery).
type ApplyResult struct {
	ChangeSetID   string      `json:"changesetId"`
	Trigger       string      `json:"trigger"`
	OpsApplied    int         `json:"opsApplied"`
	WorkspaceHash string      `json:"workspaceHash"`
	Checks        *RunSummary `json:"checks"`
}

// RunSummary is the compact check-run summary embedded in responses.
type RunSummary struct {
	RunID  string    `json:"runId"`
	Counts SevCounts `json:"counts"`
	Score  int       `json:"score"`
}

// ApplyOps validates and applies forward ops as ONE changeset in ONE
// transaction (trigger "user"). restore_* ops are rejected here with
// domain.ErrInvalidOp — they are engine-internal inverses (P2-T3's undo and
// checkpoint restore call applyInverses instead).
//
// Payload problems (unknown code, strict field usage, bad values) fail
// before any transaction with domain.ErrInvalidOp. A semantic failure
// during application (unknown entity, duplicate code, self loop …) rolls
// the whole transaction back — the workspace state is untouched — and
// returns an error wrapping domain.ErrOpFailed that carries the failed op as
// *OpError; the failed changeset is then recorded in a second transaction
// per PROJECT_BRIEF_P2 §4 step 2 (ops_applied 0, failing op "failed", the
// other ops "not_attempted", inverse_json NULL for all rows). When
// runChecks is true (the default at call sites) and the changeset applied,
// checks run immediately (trigger auto_after_ops, changeset_id set).
func (s *Service) ApplyOps(ctx context.Context, workspaceID string, ops []Op, description string, runChecks bool) (ApplyResult, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return ApplyResult{}, err
	}
	internal := make([]internalOp, 0, len(ops))
	for i, op := range ops {
		if !domain.IsForwardOp(op.Op) {
			return ApplyResult{}, fmt.Errorf("sim: op %d: %q: not a forward op code (restore_* ops are engine-internal): %w",
				i, op.Op, domain.ErrInvalidOp)
		}
		if err := ValidateOp(op); err != nil {
			return ApplyResult{}, fmt.Errorf("sim: op %d: %w", i, err)
		}
		internal = append(internal, forwardOp(op))
	}
	return s.applyInternal(ctx, workspaceID, internal, false, domain.ChangeSetTriggerUser, description, runChecks)
}

// applyInternal is the single mutation path of the engine: it applies ops
// as ONE changeset in ONE db.Do transaction (brief §4 step 1), records the
// failed changeset on failure (step 2, second transaction) and runs the
// auto-checks (step 3). allowRestore permits the engine-internal restore
// ops — only the undo (P2-T3) and checkpoint-restore paths set it.
func (s *Service) applyInternal(ctx context.Context, workspaceID string, ops []internalOp, allowRestore bool,
	trigger domain.ChangeSetTrigger, description string, runChecks bool) (ApplyResult, error) {
	// Payload validation of forward ops (restore ops are engine-produced
	// and trusted).
	for i, op := range ops {
		if op.isRestore() {
			if !allowRestore {
				return ApplyResult{}, fmt.Errorf("sim: op %d: %s: restore ops are engine-internal: %w",
					i, op.code(), domain.ErrInvalidOp)
			}
			continue
		}
		if !domain.IsForwardOp(op.code()) {
			return ApplyResult{}, fmt.Errorf("sim: op %d: %q: unknown op code: %w", i, op.code(), domain.ErrInvalidOp)
		}
		if err := ValidateOp(op.forward); err != nil {
			return ApplyResult{}, fmt.Errorf("sim: op %d: %w", i, err)
		}
	}
	if !trigger.IsValid() {
		return ApplyResult{}, fmt.Errorf("sim: invalid changeset trigger %q", trigger)
	}

	var (
		result ApplyResult
		opErr  *OpError
	)
	// op_json of every submitted op is computed BEFORE the transaction:
	// the failed-changeset record (§4 step 2) must carry the full op
	// payload of every op, not just the ones already reached when the
	// failure hit.
	csJSON := make([]string, len(ops))
	for i, op := range ops {
		opJSON, err := op.opJSON()
		if err != nil {
			return ApplyResult{}, fmt.Errorf("sim: op %d (%s): %w", i, op.code(), err)
		}
		csJSON[i] = opJSON
	}

	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		// Allocate the changeset id inside the transaction: a rollback
		// discards the increment (no burned ids on failures).
		n, err := tx.Sequences.Allocate(ctx, domain.SeqChangeSet)
		if err != nil {
			return fmt.Errorf("sim: allocate changeset id: %w", err)
		}
		csID := domain.FormatSimID(domain.SeqChangeSet, n)
		now := s.clock()

		rows := make([]domain.ChangeSetOp, 0, len(ops))
		for i, op := range ops {
			inverseJSON, err := s.applyOne(ctx, tx, workspaceID, op)
			if err != nil {
				opErr = &OpError{Index: i, Code: op.code(), Message: err.Error()}
				return fmt.Errorf("sim: apply op %d (%s): %w", i, op.code(), err)
			}
			rows = append(rows, domain.ChangeSetOp{
				ChangeSetID: csID,
				Seq:         i + 1,
				OpCode:      op.code(),
				OpJSON:      csJSON[i],
				InverseJSON: inverseJSON,
				OpResult:    domain.OpResultApplied,
			})
		}
		cs := domain.ChangeSet{
			ID:          csID,
			WorkspaceID: workspaceID,
			Description: description,
			Trigger:     trigger,
			Status:      domain.ChangeSetStatusApplied,
			OpsApplied:  len(ops),
			CreatedAt:   now,
		}
		if err := tx.ChangeSets.Insert(ctx, cs, rows); err != nil {
			return fmt.Errorf("sim: record changeset %s: %w", csID, err)
		}
		result = ApplyResult{ChangeSetID: csID, Trigger: trigger.String(), OpsApplied: len(ops)}
		return nil
	})
	if err != nil {
		if opErr == nil {
			return ApplyResult{}, fmt.Errorf("sim: apply changeset: %w", err)
		}
		// Second transaction: record the failed changeset (state is
		// untouched — the first transaction rolled back).
		csID, recErr := s.recordFailedChangeSet(ctx, workspaceID, ops, csJSON, opErr, trigger, description)
		if recErr != nil {
			return ApplyResult{}, fmt.Errorf("sim: %w; additionally, recording the failed changeset failed: %w", opErr, recErr)
		}
		return ApplyResult{}, fmt.Errorf("sim: %w [changeset %s recorded as failed]", opErr, csID)
	}

	// Auto-checks (brief §4 step 3) run after commit as their own
	// transaction, attached to this changeset.
	if runChecks {
		run, err := s.runChecksWith(ctx, workspaceID, domain.CheckTriggerAutoAfterOps, result.ChangeSetID)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("sim: auto-checks after changeset %s: %w", result.ChangeSetID, err)
		}
		result.Checks = &RunSummary{RunID: run.RunID, Counts: run.Counts, Score: run.Score}
		result.WorkspaceHash = run.WorkspaceHash
		return result, nil
	}
	hash, err := s.WorkspaceHash(ctx, workspaceID)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("sim: hash after changeset %s: %w", result.ChangeSetID, err)
	}
	result.WorkspaceHash = hash
	return result, nil
}

// applyInverses applies decoded inverse ops as a NEW changeset — the undo
// path (P2-T3) and the checkpoint-restore path call it. The caller passes
// the inverses ALREADY REVERSED (undo applies the target changeset's
// inverse ops in reverse seq order). allowRestore is implicit: inverse ops
// may be restore ops. On success the returned ApplyResult carries trigger
// and the auto-check summary like ApplyOps.
func (s *Service) applyInverses(ctx context.Context, workspaceID string, inverses []internalOp,
	trigger domain.ChangeSetTrigger, description string, runChecks bool) (ApplyResult, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return ApplyResult{}, err
	}
	for i, op := range inverses {
		if op.isRestore() {
			continue // engine-produced payloads are trusted
		}
		if !domain.IsForwardOp(op.code()) {
			return ApplyResult{}, fmt.Errorf("sim: inverse op %d: %q is neither forward nor restore: %w",
				i, op.code(), domain.ErrInvalidOp)
		}
	}
	return s.applyInternal(ctx, workspaceID, inverses, true, trigger, description, runChecks)
}

// recordFailedChangeSet writes the failed changeset row with per-op
// results per PROJECT_BRIEF_P2 §4 step 2: ops_applied 0, the failing op
// "failed", every other op "not_attempted", inverse_json NULL for ALL
// rows. It allocates its own changeset id (the rolled-back allocation
// burned nothing, so the id matches what the aborted transaction would
// have used — deterministic across fresh databases).
func (s *Service) recordFailedChangeSet(ctx context.Context, workspaceID string, ops []internalOp, opJSONs []string,
	failed *OpError, trigger domain.ChangeSetTrigger, description string) (string, error) {
	var csID string
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		n, err := tx.Sequences.Allocate(ctx, domain.SeqChangeSet)
		if err != nil {
			return fmt.Errorf("sim: allocate failed-changeset id: %w", err)
		}
		csID = domain.FormatSimID(domain.SeqChangeSet, n)
		rows := make([]domain.ChangeSetOp, 0, len(ops))
		for i, op := range ops {
			result := domain.OpResultNotAttempted
			if i == failed.Index {
				result = domain.OpResultFailed
			}
			opJSON := ""
			if i < len(opJSONs) {
				opJSON = opJSONs[i]
			}
			rows = append(rows, domain.ChangeSetOp{
				ChangeSetID: csID,
				Seq:         i + 1,
				OpCode:      op.code(),
				OpJSON:      opJSON,
				InverseJSON: "", // failed changesets never store inverses
				OpResult:    result,
			})
		}
		cs := domain.ChangeSet{
			ID:          csID,
			WorkspaceID: workspaceID,
			Description: description,
			Trigger:     trigger,
			Status:      domain.ChangeSetStatusFailed,
			OpsApplied:  0,
			CreatedAt:   s.clock(),
		}
		if err := tx.ChangeSets.Insert(ctx, cs, rows); err != nil {
			return fmt.Errorf("sim: record failed changeset %s: %w", csID, err)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("sim: record failed changeset: %w", err)
	}
	return csID, nil
}
