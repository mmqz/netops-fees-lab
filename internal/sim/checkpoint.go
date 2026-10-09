package sim

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// checkpointDir is the directory (relative to the lab DB directory) that
// holds the workspace-scoped snapshot databases of the Online Backup API.
const checkpointDir = "checkpoints"

// CheckpointResult is the response of a successful CreateCheckpoint.
type CheckpointResult struct {
	CheckpointID  string    `json:"checkpointId"`
	Label         string    `json:"label"`
	WorkspaceHash string    `json:"workspaceHash"`
	CreatedAt     time.Time `json:"createdAt"`
}

// CreateCheckpoint captures the current workspace state as a checkpoint
// (PROJECT_BRIEF_P2 §4, undo tier 3): the SQLite Online Backup API copies
// the whole database into <dbdir>/checkpoints/<workspaceID>/<cpId>.db
// (repo.DB.Backup — never a raw file copy), and a checkpoints row records
// the label, the snapshot path RELATIVE to the lab DB directory and the
// canonical workspace state hash at creation time.
//
// An empty label is rejected with an error wrapping domain.ErrInvalidOp
// ("label required") — the single sentinel for bad checkpoint input; the
// HTTP layer maps it to 400 (documented for P2-T4). An unknown workspace
// yields domain.ErrNotFound.
//
// Crash safety: the id allocation, the backup file write and the marker row
// insert are three steps — the backup file write cannot join a transaction.
// A crash after a successful backup but before the insert leaves an orphan
// snapshot file that nothing references (harmless; it is not visible
// through ListCheckpoints). A crash between the id allocation and the
// backup burns a checkpoint id (gap in cp-<n>, no state impact). The state
// hash is read BEFORE the backup; the supported single-writer mode makes
// the two consistent (a concurrent writer between them would record a hash
// that no longer matches the snapshot bytes — the restore path trusts the
// SNAPSHOT state, not the recorded hash).
func (s *Service) CreateCheckpoint(ctx context.Context, workspaceID, label string) (CheckpointResult, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return CheckpointResult{}, err
	}
	if label == "" {
		return CheckpointResult{}, fmt.Errorf("sim: create checkpoint: label required: %w", domain.ErrInvalidOp)
	}
	hash, err := s.WorkspaceHash(ctx, workspaceID)
	if err != nil {
		return CheckpointResult{}, err
	}

	// Allocate the checkpoint id in its own transaction (Allocate must not
	// run in autocommit mode; a crash here burns at most one id).
	var cpID string
	err = s.db.Do(ctx, func(tx *repo.Tx) error {
		n, err := tx.Sequences.Allocate(ctx, domain.SeqCheckpoint)
		if err != nil {
			return fmt.Errorf("allocate checkpoint id: %w", err)
		}
		cpID = domain.FormatSimID(domain.SeqCheckpoint, n)
		return nil
	})
	if err != nil {
		return CheckpointResult{}, fmt.Errorf("sim: create checkpoint: %w", err)
	}

	relPath := filepath.Join(checkpointDir, workspaceID, cpID+".db")
	dst := filepath.Join(filepath.Dir(s.db.Path()), relPath)
	// The Online Backup API over a pooled connection (P2-0 verified the
	// modernc.org/sqlite surface; repo.Backup wraps it).
	if err := s.db.Backup(ctx, dst); err != nil {
		return CheckpointResult{}, fmt.Errorf("sim: create checkpoint %s: %w", cpID, err)
	}

	// Marker row AFTER the successful backup (see the crash-safety note).
	created := s.clock()
	cp := domain.Checkpoint{
		ID:            cpID,
		WorkspaceID:   workspaceID,
		Label:         label,
		FilePath:      relPath,
		WorkspaceHash: hash,
		CreatedAt:     created,
	}
	err = s.db.Do(ctx, func(tx *repo.Tx) error {
		return tx.Checkpoints.Insert(ctx, cp)
	})
	if err != nil {
		return CheckpointResult{}, fmt.Errorf("sim: create checkpoint %s: record marker: %w", cpID, err)
	}
	return CheckpointResult{
		CheckpointID:  cpID,
		Label:         label,
		WorkspaceHash: hash,
		CreatedAt:     created,
	}, nil
}

// ListCheckpoints returns every checkpoint of the workspace, newest first
// (ids are globally monotonic cp-<n>). An unknown workspace yields
// domain.ErrNotFound.
func (s *Service) ListCheckpoints(ctx context.Context, workspaceID string) ([]domain.Checkpoint, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return nil, err
	}
	cps, err := s.db.Checkpoints.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("sim: list checkpoints of workspace %s: %w", workspaceID, err)
	}
	return cps, nil
}

// RestoreResult is the response of RestoreCheckpoint. In the no-op case
// (current state already equals the checkpoint state) ChangeSetID is "",
// OpsApplied 0 and Note "already at checkpoint state" — no changeset is
// created (the API layer renders changesetId as null).
type RestoreResult struct {
	CheckpointID   string      `json:"checkpointId"`
	Label          string      `json:"label"`
	ChangeSetID    string      `json:"changesetId"` // "" = no-op restore (already at that state)
	Note           string      `json:"note,omitempty"`
	OpsApplied     int         `json:"opsApplied"`
	RestoredToHash string      `json:"restoredToHash"`
	WorkspaceHash  string      `json:"workspaceHash"`
	Checks         *RunSummary `json:"checks"`
}

// RestoreCheckpoint transforms the current workspace state into the
// checkpoint's captured state (PROJECT_BRIEF_P2 §4, workspace-scoped): the
// snapshot database is opened READ-ONLY (repo.OpenReadOnly — mode=ro, no
// journal pragma, no migrations), the workspace's (devices, interfaces,
// links) are loaded from it with the same typed state loader the state hash
// uses, and the difference to the current state is applied as ONE normal
// changeset of FORWARD ops only (trigger "checkpoint_restore" — no raw
// table replacement, no restore_* ops). If the current hash equals the
// snapshot hash the restore is a no-op.
//
// The op plan's dependency-safe order (deletes before creates, parents
// before children):
//  1. delete links present now but not in the snapshot,
//  2. delete interfaces present now but not in the snapshot,
//  3. delete devices present now but not in the snapshot,
//  4. create devices missing now,
//  5. create interfaces missing now (with their snapshot status/addr),
//  6. create links missing now (with their snapshot status/label),
//  7. set statuses / configure addresses that differ.
//
// Matching keys: devices by CODE, interfaces by (device code, name), links
// by (aDevice, aInterface, zDevice, zInterface). Entities created by phases
// 4–6 are referenced by the phases that follow — create_device has no
// status field and create_interface/create_link reference ids, so the plan
// predicts the ids the engine's apply transaction WILL allocate (the
// sequences are deterministic: the k-th create in the plan is the k-th
// Allocate of the changeset). Sound in the supported single-writer mode —
// same reasoning as the materialization determinism guarantee.
//
// Errors: unknown workspace/checkpoint (or one of another workspace) ⇒
// domain.ErrNotFound; a missing snapshot file ⇒ an error wrapping
// domain.ErrConflict whose message contains "checkpoint file missing" —
// P2-T4 maps that exact case to HTTP 409 checkpoint_missing_file.
// Semantic op failures wrap domain.ErrOpFailed like every apply.
func (s *Service) RestoreCheckpoint(ctx context.Context, workspaceID, checkpointID string, runChecks bool) (RestoreResult, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return RestoreResult{}, err
	}
	cp, err := s.db.Checkpoints.GetByID(ctx, checkpointID)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("sim: restore checkpoint %s: %w", checkpointID, err)
	}
	if cp.WorkspaceID != workspaceID {
		return RestoreResult{}, fmt.Errorf("sim: restore checkpoint %s: %w", checkpointID, domain.ErrNotFound)
	}

	snapshotPath := filepath.Join(filepath.Dir(s.db.Path()), cp.FilePath)
	if _, err := os.Stat(snapshotPath); err != nil {
		// 409 checkpoint_missing_file at the API (brief §7) — see doc comment.
		return RestoreResult{}, fmt.Errorf("sim: restore checkpoint %s: checkpoint file missing: %s: %w: %v",
			checkpointID, snapshotPath, domain.ErrConflict, err)
	}
	snapDB, err := repo.OpenReadOnly(snapshotPath)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("sim: restore checkpoint %s: open snapshot: %w", checkpointID, err)
	}
	defer func() { _ = snapDB.Close() }()

	// The snapshot's state is the ground truth of the restore (its hash must
	// equal the recorded one — both were captured in CreateCheckpoint).
	snapState, err := loadState(ctx, storeOfDB(snapDB), workspaceID)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("sim: restore checkpoint %s: %w", checkpointID, err)
	}
	snapHash, err := snapState.hash()
	if err != nil {
		return RestoreResult{}, fmt.Errorf("sim: restore checkpoint %s: %w", checkpointID, err)
	}

	// Current state + sequence bases, read in one transaction.
	var (
		curState *simState
		vdBase   int
		ifBase   int
	)
	err = s.db.Do(ctx, func(tx *repo.Tx) error {
		st, err := loadState(ctx, storeOfTx(tx), workspaceID)
		if err != nil {
			return err
		}
		curState = st
		if vdBase, err = tx.Sequences.Value(ctx, domain.SeqDevice); err != nil {
			return fmt.Errorf("read device sequence: %w", err)
		}
		if ifBase, err = tx.Sequences.Value(ctx, domain.SeqInterface); err != nil {
			return fmt.Errorf("read interface sequence: %w", err)
		}
		return nil
	})
	if err != nil {
		return RestoreResult{}, fmt.Errorf("sim: restore checkpoint %s: %w", checkpointID, err)
	}
	curHash, err := curState.hash()
	if err != nil {
		return RestoreResult{}, fmt.Errorf("sim: restore checkpoint %s: %w", checkpointID, err)
	}

	result := RestoreResult{
		CheckpointID:   cp.ID,
		Label:          cp.Label,
		RestoredToHash: snapHash,
		WorkspaceHash:  curHash,
	}
	if curHash == snapHash {
		// No-op: already at the checkpoint state (brief §4).
		result.Note = "already at checkpoint state"
		return result, nil
	}

	ops := restoreDiffOps(curState, snapState, vdBase, ifBase)
	if len(ops) == 0 {
		// Hashes differ but no forward op can express the difference — the
		// op catalog cannot rename devices/interfaces or relabel links, and
		// none of those differences is reachable through engine ops. Report
		// the mismatch instead of silently claiming success.
		return RestoreResult{}, fmt.Errorf("sim: restore checkpoint %s: state differs (hash %s vs %s) but no expressible op diff: %w",
			checkpointID, curHash, snapHash, domain.ErrConflict)
	}

	applied, err := s.applyInternal(ctx, workspaceID, ops, false,
		domain.ChangeSetTriggerCheckpointRestore,
		fmt.Sprintf("restore checkpoint %s %q", cp.ID, cp.Label), runChecks)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("sim: restore checkpoint %s: %w", checkpointID, err)
	}
	result.ChangeSetID = applied.ChangeSetID
	result.OpsApplied = applied.OpsApplied
	result.WorkspaceHash = applied.WorkspaceHash
	result.Checks = applied.Checks
	return result, nil
}

// ---- restore op planning ----

// ifaceKey is the interface matching key of the restore diff: (device code,
// interface name).
func ifaceKey(deviceCode, ifaceName string) string {
	return deviceCode + "\x00" + ifaceName
}

// linkKey is the link matching key of the restore diff: the resolved
// (aDevice, aInterface, zDevice, zInterface) 4-tuple.
func linkKey(l domain.SimLink, state *simState) string {
	a, z := state.linkEnds(l)
	return a.deviceCode + "\x00" + a.ifaceName + "\x00" + z.deviceCode + "\x00" + z.ifaceName
}

// restoreDiffOps builds the forward-op plan transforming cur into snap.
// vdBase/ifBase are the sequence values BEFORE the changeset — the plan
// predicts that the engine allocates vd-(vdBase+k) / if-(ifBase+k) for the
// k-th device/interface create, in plan order (phases 4 and 5 are sorted the
// same way the ops are emitted).
func restoreDiffOps(cur, snap *simState, vdBase, ifBase int) []internalOp {
	ops := make([]internalOp, 0, 32)

	// Interface/link lookup tables of both states.
	curIface := map[string]domain.SimInterface{} // key → current interface (with its live id)
	snapIface := map[string]domain.SimInterface{}
	for _, d := range cur.devices {
		for _, i := range d.Interfaces {
			curIface[ifaceKey(d.Dev.Code, i.Name)] = i
		}
	}
	for _, d := range snap.devices {
		for _, i := range d.Interfaces {
			snapIface[ifaceKey(d.Dev.Code, i.Name)] = i
		}
	}
	curLink := map[string]domain.SimLink{}
	snapLink := map[string]domain.SimLink{}
	for _, l := range cur.links {
		curLink[linkKey(l, cur)] = l
	}
	for _, l := range snap.links {
		snapLink[linkKey(l, snap)] = l
	}

	// Phase 1: delete extra links (before their interfaces go). cur.links is
	// in canonical numeric-id order — deterministic, and duplicate endpoint
	// pairs (pathological, unreachable through engine ops) are handled
	// naturally because every link row is visited.
	for _, l := range cur.links {
		if _, ok := snapLink[linkKey(l, cur)]; !ok {
			ops = append(ops, forwardOp(Op{Op: domain.OpDeleteLink, LinkID: l.ID}))
		}
	}
	// Phase 2: delete extra interfaces (device code order, then name).
	for _, d := range cur.devices {
		for _, i := range d.Interfaces {
			if _, ok := snapIface[ifaceKey(d.Dev.Code, i.Name)]; !ok {
				ops = append(ops, forwardOp(Op{Op: domain.OpDeleteInterface, InterfaceID: i.ID}))
			}
		}
	}
	// Phase 3: delete extra devices.
	for _, d := range cur.devices {
		if _, ok := snap.deviceByCode(d.Dev.Code); !ok {
			ops = append(ops, forwardOp(Op{Op: domain.OpDeleteDevice, DeviceID: d.Dev.ID}))
		}
	}

	// Phase 4: create missing devices. deviceIdOf maps a snapshot device
	// code to the id its interfaces must reference: the current id when the
	// device exists, otherwise the PREDICTED id of the create below.
	deviceIDOf := map[string]string{}
	vdSeq := 0
	for _, d := range snap.devices {
		if curDev, ok := cur.deviceByCode(d.Dev.Code); ok {
			deviceIDOf[d.Dev.Code] = curDev.ID
			continue
		}
		vdSeq++
		deviceIDOf[d.Dev.Code] = domain.FormatSimID(domain.SeqDevice, vdBase+vdSeq)
		ops = append(ops, forwardOp(Op{Op: domain.OpCreateDevice, Code: d.Dev.Code, Name: d.Dev.Name}))
	}

	// Phase 5: create missing interfaces with their snapshot business fields.
	// ifaceIDOf maps a snapshot interface key to the id links must reference:
	// the current id when matched, otherwise the PREDICTED id of the create.
	ifaceIDOf := map[string]string{}
	ifSeq := 0
	for _, d := range snap.devices {
		for _, i := range d.Interfaces {
			key := ifaceKey(d.Dev.Code, i.Name)
			if curI, ok := curIface[key]; ok {
				ifaceIDOf[key] = curI.ID
				continue
			}
			ifSeq++
			predicted := domain.FormatSimID(domain.SeqInterface, ifBase+ifSeq)
			ifaceIDOf[key] = predicted
			op := Op{Op: domain.OpCreateInterface, DeviceID: deviceIDOf[d.Dev.Code], Name: i.Name, Status: i.Status.String()}
			if i.IPv4Addr != "" {
				addr, prefix := i.IPv4Addr, i.IPv4Prefix
				op.IPv4Addr, op.IPv4Prefix = &addr, &prefix
			}
			ops = append(ops, forwardOp(op))
		}
	}

	// Phase 6: create missing links with their snapshot status and label
	// (snap.links is in canonical numeric-id order).
	for _, l := range snap.links {
		if _, ok := curLink[linkKey(l, snap)]; ok {
			continue
		}
		a, z := snap.linkEnds(l)
		op := Op{
			Op:           domain.OpCreateLink,
			AInterfaceID: ifaceIDOf[ifaceKey(a.deviceCode, a.ifaceName)],
			ZInterfaceID: ifaceIDOf[ifaceKey(z.deviceCode, z.ifaceName)],
			Status:       l.Status.String(),
			Label:        l.Label,
		}
		ops = append(ops, forwardOp(op))
	}

	// Phase 7: status/address differences of matched (or just-created)
	// entities. create_interface/create_link already carry their snapshot
	// fields, so only MATCHED entities need set ops — except devices:
	// create_device has no status field, so a missing device that was DOWN
	// in the snapshot needs set_device_status with its predicted id.
	for _, d := range snap.devices {
		curDev, ok := cur.deviceByCode(d.Dev.Code)
		curStatus := domain.EntityStatusUp // freshly created devices start up
		if ok {
			curStatus = curDev.Status
		}
		if curStatus != d.Dev.Status {
			ops = append(ops, forwardOp(Op{
				Op:       domain.OpSetDeviceStatus,
				DeviceID: deviceIDOf[d.Dev.Code],
				Status:   d.Dev.Status.String(),
			}))
		}
	}
	for _, d := range snap.devices {
		for _, i := range d.Interfaces {
			curI, ok := curIface[ifaceKey(d.Dev.Code, i.Name)]
			if !ok {
				continue // created in phase 5 with its snapshot fields
			}
			if curI.Status != i.Status {
				ops = append(ops, forwardOp(Op{
					Op:          domain.OpSetInterfaceStatus,
					InterfaceID: curI.ID,
					Status:      i.Status.String(),
				}))
			}
			if curI.IPv4Addr != i.IPv4Addr || curI.IPv4Prefix != i.IPv4Prefix {
				op := Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: curI.ID}
				if i.IPv4Addr != "" {
					addr, prefix := i.IPv4Addr, i.IPv4Prefix
					op.IPv4Addr, op.IPv4Prefix = &addr, &prefix
				}
				ops = append(ops, forwardOp(op))
			}
		}
	}
	for _, l := range snap.links {
		if curL, ok := curLink[linkKey(l, snap)]; ok && curL.Status != l.Status {
			ops = append(ops, forwardOp(Op{
				Op:     domain.OpSetLinkStatus,
				LinkID: curL.ID,
				Status: l.Status.String(),
			}))
		}
	}
	return ops
}
