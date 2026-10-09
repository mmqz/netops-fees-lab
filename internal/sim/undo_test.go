package sim

// ACC-P2-8 — undo LIFO semantics of the three-tier undo (tier 2, changeset
// inversion): reverting the older of two applied changesets is blocked
// while the newer is live; undoing the newer frees the older (undo
// changesets never block); a failed changeset never blocks and cannot be
// undone; undo-of-undo works through the recorded inverses of the undo
// changeset itself.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// undoSetup materializes the seeded workspace and applies two changesets:
// cs-1 sets a device status, cs-2 configures an address and brings a link
// down. Returns the engine, the repo handle, workspace id and the three hash
// checkpoints (materialize baseline, post-cs-1, post-cs-2).
func undoSetup(t *testing.T) (*Service, *repo.DB, string, string, string, string) {
	t.Helper()
	svc, db, res := seededEngine(t)
	ws := res.WorkspaceID
	hash0 := mustHash(t, svc, ws) // materialize baseline

	cs1 := applyOK(t, svc, ws, []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-2", Status: "down"},
	})
	hash1 := mustHash(t, svc, ws)
	if hash1 == hash0 {
		t.Fatalf("cs1 must change the state")
	}

	cs2 := applyOK(t, svc, ws, []Op{
		{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-1", IPv4Addr: strPtr("10.0.0.1"), IPv4Prefix: intPtr(24)},
		{Op: domain.OpSetLinkStatus, LinkID: "ln-2", Status: "down"},
	})
	hash2 := mustHash(t, svc, ws)
	_ = hash2 // returned to the caller for its own assertions
	if hash2 == hash1 {
		t.Fatalf("cs2 must change the state")
	}

	if cs1.ChangeSetID != "cs-1" || cs2.ChangeSetID != "cs-2" {
		t.Fatalf("changeset ids = %s/%s, want cs-1/cs-2 (deterministic sequences)", cs1.ChangeSetID, cs2.ChangeSetID)
	}
	return svc, db, ws, hash0, hash1, hash2
}

// TestUndoLIFO is ACC-P2-8 (plus the undo-of-undo machinery and the
// auto-check attachment).
func TestUndoLIFO(t *testing.T) {
	svc, db, ws, hash0, hash1, hash2 := undoSetup(t)
	ctx := context.Background()
	if got := mustHash(t, svc, ws); got != hash2 {
		t.Fatalf("pre-undo hash = %s, want the post-cs-2 hash %s", got, hash2)
	}

	// ---- reverting the older of two live changesets: blocked (ErrNotLatest).
	_, err := svc.Undo(ctx, ws, "cs-1", false)
	if !errors.Is(err, domain.ErrNotLatest) {
		t.Fatalf("undo cs-1 with cs-2 live: err = %v, want domain.ErrNotLatest", err)
	}

	// ---- undo cs-2 (the latest applied): success.
	res, err := svc.Undo(ctx, ws, "cs-2", true)
	if err != nil {
		t.Fatalf("undo cs-2: %v", err)
	}
	if res.Reverted != "cs-2" || res.Trigger != domain.ChangeSetTriggerUndo.String() {
		t.Fatalf("undo result meta = %+v, want reverted cs-2 / trigger undo", res)
	}
	if res.OpsApplied != 2 {
		t.Fatalf("undo applied %d ops, want 2 (the inverses of cs-2's ops)", res.OpsApplied)
	}
	if res.WorkspaceHash != hash1 {
		t.Fatalf("post-undo hash = %s, want the post-cs-1 hash %s", res.WorkspaceHash, hash1)
	}
	if got := mustHash(t, svc, ws); got != hash1 {
		t.Fatalf("current hash after undo = %s, want post-cs-1 %s", got, hash1)
	}
	// The state itself is reverted: address unconfigured, link up again.
	iface, err := db.SimInterfaces.GetByID(ctx, "if-1")
	if err != nil || iface.IPv4Addr != "" || iface.IPv4Prefix != -1 {
		t.Fatalf("if-1 after undo = %+v (err %v), want unconfigured", iface, err)
	}
	link, err := db.SimLinks.GetByID(ctx, "ln-2")
	if err != nil || link.Status != domain.EntityStatusUp {
		t.Fatalf("ln-2 after undo = %+v (err %v), want up", link, err)
	}
	// The undo changeset exists with trigger undo, description "undo of cs-2"
	// and recorded inverse op rows (undo-of-undo machinery).
	undoCS, err := db.ChangeSets.GetByID(ctx, res.ChangeSetID)
	if err != nil {
		t.Fatalf("undo changeset %s: %v", res.ChangeSetID, err)
	}
	if undoCS.Trigger != domain.ChangeSetTriggerUndo || undoCS.Status != domain.ChangeSetStatusApplied {
		t.Fatalf("undo changeset = %+v, want trigger undo / applied", undoCS)
	}
	if undoCS.Description != "undo of cs-2" {
		t.Fatalf("undo changeset description = %q, want %q", undoCS.Description, "undo of cs-2")
	}
	undoRows, err := db.ChangeSets.GetOps(ctx, res.ChangeSetID)
	if err != nil {
		t.Fatalf("GetOps(%s): %v", res.ChangeSetID, err)
	}
	if len(undoRows) != 2 {
		t.Fatalf("undo changeset has %d op rows, want 2", len(undoRows))
	}
	for i, row := range undoRows {
		if row.InverseJSON == "" {
			t.Fatalf("undo op row %d (%s) has no recorded inverse (undo-of-undo must work)", i, row.OpCode)
		}
		if _, err := decodeInverseOp(row.InverseJSON); err != nil {
			t.Fatalf("undo op row %d inverse does not decode: %v", i, err)
		}
	}
	// The inverses of the undo changeset are the FORWARD re-applications:
	// seq 1 = set_link_status down (re-applies cs-2's link op)…
	if undoRows[0].OpCode != domain.OpSetLinkStatus {
		t.Fatalf("undo op row 1 code = %q, want set_link_status (reverse seq order)", undoRows[0].OpCode)
	}
	// …and seq 2 = config_interface_ipv4 back to null.
	if undoRows[1].OpCode != domain.OpConfigInterfaceIPv4 {
		t.Fatalf("undo op row 2 code = %q, want config_interface_ipv4", undoRows[1].OpCode)
	}
	// cs-2 itself is marked reverted with reverted_at set.
	target, err := db.ChangeSets.GetByID(ctx, "cs-2")
	if err != nil {
		t.Fatalf("GetByID(cs-2): %v", err)
	}
	if !target.Reverted() || target.RevertedAt.IsZero() {
		t.Fatalf("cs-2 after undo = %+v, want reverted with reverted_at", target)
	}
	if !target.RevertedAt.After(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("cs-2 reverted_at = %v, want a real timestamp", target.RevertedAt)
	}
	// Auto-checks ran and are attached to the UNDO changeset.
	if res.Checks == nil {
		t.Fatalf("runChecks=true must attach a check summary to the undo result")
	}
	run, _, err := svc.GetRun(ctx, ws, res.Checks.RunID)
	if err != nil {
		t.Fatalf("GetRun(%s): %v", res.Checks.RunID, err)
	}
	if run.ChangesetID != res.ChangeSetID || run.Trigger != domain.CheckTriggerAutoAfterOps {
		t.Fatalf("auto run = %+v, want changeset %s / auto_after_ops", run, res.ChangeSetID)
	}

	// ---- LIFO freed: cs-1 is the newest still-effective forward change.
	res, err = svc.Undo(ctx, ws, "cs-1", false)
	if err != nil {
		t.Fatalf("undo cs-1 after cs-2 was undone: %v", err)
	}
	if res.WorkspaceHash != hash0 {
		t.Fatalf("hash after undoing cs-1 = %s, want the materialize baseline %s", res.WorkspaceHash, hash0)
	}
	if got := mustHash(t, svc, ws); got != hash0 {
		t.Fatalf("current hash = %s, want baseline %s", got, hash0)
	}
	// The device is back up.
	dev, err := db.VirtualDevices.GetByID(ctx, "vd-2")
	if err != nil || dev.Status != domain.EntityStatusUp {
		t.Fatalf("vd-2 after full undo = %+v (err %v), want up", dev, err)
	}

	// ---- undoing an already-reverted changeset: conflict, nothing applied.
	csCount, err := db.ChangeSets.CountByWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("count changesets: %v", err)
	}
	_, err = svc.Undo(ctx, ws, "cs-1", false)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("undo cs-1 again: err = %v, want domain.ErrConflict (already reverted)", err)
	}
	_, err = svc.Undo(ctx, ws, "cs-2", false)
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("undo cs-2 again: err = %v, want domain.ErrConflict (already reverted)", err)
	}
	if got, _ := db.ChangeSets.CountByWorkspace(ctx, ws); got != csCount {
		t.Fatalf("failed undo attempts must not create changesets: %d → %d", csCount, got)
	}

	// ---- unknown changeset / other workspace's changeset: not found.
	if _, err := svc.Undo(ctx, ws, "cs-99", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("undo cs-99: err = %v, want domain.ErrNotFound", err)
	}
	other, err := svc.Materialize(ctx, "other-undo", "", nil)
	if err != nil {
		t.Fatalf("Materialize(other-undo): %v", err)
	}
	applyOK(t, svc, other.WorkspaceID, []Op{
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-28", Status: "down"},
	})
	if _, err := svc.Undo(ctx, other.WorkspaceID, "cs-1", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("undo another workspace's changeset: err = %v, want domain.ErrNotFound", err)
	}

	// ---- undo of a FAILED changeset: error, nothing to undo. A trailing
	// failed changeset also must NOT block the next undo (the undo-of-undo
	// step right below proves it end-to-end).
	if _, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpSetLinkStatus, LinkID: "ln-99", Status: "down"},
	}, "doomed", false); !errors.Is(err, domain.ErrOpFailed) {
		t.Fatalf("ApplyOps(doomed): %v, want domain.ErrOpFailed", err)
	}
	all, _, err := db.ChangeSets.ListByWorkspace(ctx, ws, 1, 0)
	if err != nil || len(all) == 0 {
		t.Fatalf("list changesets: %v", err)
	}
	failedCS := all[0] // newest first: the doomed one
	if failedCS.Status != domain.ChangeSetStatusFailed {
		t.Fatalf("newest changeset = %+v, want the failed one", failedCS)
	}
	if _, err := svc.Undo(ctx, ws, failedCS.ID, false); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("undo failed changeset: err = %v, want domain.ErrConflict", err)
	}

	// ---- undo-of-undo: the latest applied is the undo changeset of cs-1;
	// undoing it re-applies cs-1's effect (state == post-cs-1). The trailing
	// failed changeset above must not block this.
	res, err = svc.Undo(ctx, ws, "", false) // "" ⇒ latest applied = undo-of-cs-1
	if err != nil {
		t.Fatalf("undo the undo changeset: %v", err)
	}
	if res.Reverted == failedCS.ID {
		t.Fatalf("the failed changeset must never be the default undo target")
	}
	if res.WorkspaceHash != hash1 {
		t.Fatalf("hash after undo-of-undo = %s, want the post-cs-1 hash %s (cs1 re-applied)", res.WorkspaceHash, hash1)
	}
	if got := mustHash(t, svc, ws); got != hash1 {
		t.Fatalf("current hash after undo-of-undo = %s, want post-cs-1 %s", got, hash1)
	}
	dev, err = db.VirtualDevices.GetByID(ctx, "vd-2")
	if err != nil || dev.Status != domain.EntityStatusDown {
		t.Fatalf("vd-2 after undo-of-undo = %+v (err %v), want down (cs-1 re-applied)", dev, err)
	}

	// ---- default target with no applied changeset at all: not found.
	fresh, _, freshRes := seededEngine(t)
	if _, err := fresh.Undo(ctx, freshRes.WorkspaceID, "", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("undo on a workspace without changesets: err = %v, want domain.ErrNotFound", err)
	}
}

// TestUndoUnknownWorkspace pins the workspace guard of the undo entry point.
func TestUndoUnknownWorkspace(t *testing.T) {
	svc, _, _ := seededEngine(t)
	if _, err := svc.Undo(context.Background(), "ws-none", "", false); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Undo unknown workspace: err = %v, want domain.ErrNotFound", err)
	}
}

// TestUndoMixedInverseKinds walks a changeset whose ops produce BOTH kinds
// of inverses (forward inverses and engine-internal restore ops): undo must
// re-create the deleted cascade through the restore payloads and land on
// the exact pre-changeset hash.
func TestUndoMixedInverseKinds(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID
	hash0 := mustHash(t, svc, ws)

	// Delete a device (restore_device inverse with the full cascade: the
	// device, its interfaces, and the links touching them), then flip a
	// status (plain forward inverse). DEV-008 carries CKT-007 (if-14/eth0)
	// and CKT-008 (if-15/eth1).
	cs, err := svc.ApplyOps(ctx, ws, []Op{
		{Op: domain.OpDeleteDevice, DeviceID: "vd-8"},
		{Op: domain.OpSetDeviceStatus, DeviceID: "vd-9", Status: "down"},
	}, "delete cascade + flip", false)
	if err != nil {
		t.Fatalf("ApplyOps: %v", err)
	}
	if got := mustHash(t, svc, ws); got == hash0 {
		t.Fatalf("the deletes must change the state")
	}
	if _, err := db.VirtualDevices.GetByID(ctx, "vd-8"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("vd-8 must be gone before the undo")
	}

	undoRes, err := svc.Undo(ctx, ws, cs.ChangeSetID, false)
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if undoRes.OpsApplied != 2 {
		t.Fatalf("undo applied %d ops, want 2", undoRes.OpsApplied)
	}
	if got := mustHash(t, svc, ws); got != hash0 {
		t.Fatalf("hash after undo = %s, want the original %s", got, hash0)
	}
	// The cascade is back with the ORIGINAL ids.
	if _, err := db.VirtualDevices.GetByID(ctx, "vd-8"); err != nil {
		t.Fatalf("vd-8 not restored: %v", err)
	}
	if _, err := db.SimInterfaces.GetByID(ctx, "if-14"); err != nil {
		t.Fatalf("if-14 not restored: %v", err)
	}
	if _, err := db.SimLinks.GetByID(ctx, "ln-7"); err != nil {
		t.Fatalf("ln-7 (CKT-007) not restored: %v", err)
	}
	if dev, err := db.VirtualDevices.GetByID(ctx, "vd-9"); err != nil || dev.Status != domain.EntityStatusUp {
		t.Fatalf("vd-9 after undo = %+v (err %v), want up", dev, err)
	}
	// The undo changeset's own inverses make it undoable again (tier-2
	// recursion): undoing it re-applies the delete + flip.
	again, err := svc.Undo(ctx, ws, undoRes.ChangeSetID, false)
	if err != nil {
		t.Fatalf("undo the restore changeset: %v", err)
	}
	if again.Reverted != undoRes.ChangeSetID {
		t.Fatalf("second undo reverted %q, want the first undo changeset %q", again.Reverted, undoRes.ChangeSetID)
	}
	if got := mustHash(t, svc, ws); got == hash0 {
		t.Fatalf("undo-of-undo must re-apply the deletes (state must differ from baseline)")
	}
}
