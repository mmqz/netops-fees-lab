package sim

// Forward+inverse round-trips of the changeset machinery (§4): every
// applied op stores a decodable inverse; delete_* ops capture their
// pre-state into the typed Restore*Op payloads (camelCase JSON per the
// contract); restore ops themselves carry delete inverses (undo-of-undo);
// and the engine's applyInverses re-applies captured restores to the
// exact prior state — the machinery P2-T3's undo builds on.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mmqz/netops-lab/internal/domain"
)

// decodeInverse decodes one changeset op row's inverse_json via the
// engine's inverse decoder and fails the test on any error.
func decodeInverse(t *testing.T, raw string) internalOp {
	t.Helper()
	op, err := decodeInverseOp(raw)
	if err != nil {
		t.Fatalf("decodeInverseOp(%s): %v", raw, err)
	}
	return op
}

func TestInverseOps(t *testing.T) {
	svc, db, res := seededEngine(t)
	ctx := context.Background()
	ws := res.WorkspaceID

	// ---- Part A: forward ops and their computed inverses ----
	// The last materialized ids: devices vd-1..vd-27, interfaces
	// if-1..if-40, links ln-1..ln-20 — created entities continue after
	// them deterministically.
	cs := applyOK(t, svc, ws, []Op{
		{Op: domain.OpCreateDevice, Code: "NEW-1", Name: "new device"},
		{Op: domain.OpCreateInterface, DeviceID: "vd-28", Name: "eth0"},
		{Op: domain.OpConfigInterfaceIPv4, InterfaceID: "if-41", IPv4Addr: strPtr("10.9.0.1"), IPv4Prefix: intPtr(24)},
		{Op: domain.OpCreateLink, AInterfaceID: "if-41", ZInterfaceID: "if-15", Label: "NEW-LINK"},
	})
	rows, err := db.ChangeSets.GetOps(ctx, cs.ChangeSetID)
	if err != nil {
		t.Fatalf("GetOps(%s): %v", cs.ChangeSetID, err)
	}
	if len(rows) != 4 {
		t.Fatalf("changeset %s has %d ops, want 4", cs.ChangeSetID, len(rows))
	}
	wantCodes := []string{domain.OpCreateDevice, domain.OpCreateInterface, domain.OpConfigInterfaceIPv4, domain.OpCreateLink}
	wantInverseCodes := []string{domain.OpDeleteDevice, domain.OpDeleteInterface, domain.OpConfigInterfaceIPv4, domain.OpDeleteLink}
	for i, row := range rows {
		if row.OpCode != wantCodes[i] {
			t.Errorf("op row %d code = %q, want %q", i, row.OpCode, wantCodes[i])
		}
		if row.InverseJSON == "" {
			t.Fatalf("op row %d (%s) has no inverse_json", i, row.OpCode)
		}
		inv := decodeInverse(t, row.InverseJSON)
		if inv.isRestore() {
			t.Fatalf("op row %d inverse is a restore op, want a forward inverse", i)
		}
		if inv.forward.Op != wantInverseCodes[i] {
			t.Errorf("op row %d inverse code = %q, want %q", i, inv.forward.Op, wantInverseCodes[i])
		}
	}

	// Inverse payloads carry the concrete ids of the created entities.
	if inv := decodeInverse(t, rows[0].InverseJSON); inv.forward.DeviceID != "vd-28" {
		t.Errorf("create_device inverse deviceId = %q, want vd-28", inv.forward.DeviceID)
	}
	if inv := decodeInverse(t, rows[1].InverseJSON); inv.forward.InterfaceID != "if-41" {
		t.Errorf("create_interface inverse interfaceId = %q, want if-41", inv.forward.InterfaceID)
	}
	// The config inverse restores the PREVIOUS values (both null — the
	// interface was unconfigured before the op).
	if inv := decodeInverse(t, rows[2].InverseJSON); inv.forward.IPv4Addr != nil || inv.forward.IPv4Prefix != nil {
		t.Errorf("config inverse = %+v, want both null (previous state unconfigured)", inv.forward)
	}
	if inv := decodeInverse(t, rows[3].InverseJSON); inv.forward.LinkID != "ln-21" {
		t.Errorf("create_link inverse linkId = %q, want ln-21", inv.forward.LinkID)
	}

	// ---- Part B: delete_* inverses capture the typed pre-state ----
	hashAfterCreate := mustHash(t, svc, ws)

	// delete_link → restore_link {link}
	csDel := applyOK(t, svc, ws, []Op{{Op: domain.OpDeleteLink, LinkID: "ln-21"}})
	delRows, err := db.ChangeSets.GetOps(ctx, csDel.ChangeSetID)
	if err != nil || len(delRows) != 1 {
		t.Fatalf("GetOps(%s): %v (%d rows)", csDel.ChangeSetID, err, len(delRows))
	}
	inv := decodeInverse(t, delRows[0].InverseJSON)
	if inv.restoreLink == nil {
		t.Fatalf("delete_link inverse = %q, want a restore_link payload", delRows[0].InverseJSON)
	}
	if !reflect.DeepEqual(*inv.restoreLink, RestoreLinkOp{
		Op: domain.OpRestoreLink,
		Link: LinkSnap{
			ID: "ln-21", AInterfaceID: "if-41", ZInterfaceID: "if-15",
			Status: "up", Label: "NEW-LINK", ResourceID: "",
		},
	}) {
		t.Fatalf("restore_link inverse = %+v", *inv.restoreLink)
	}
	// restore_link JSON field names are camelCase per contract.
	var linkRaw map[string]any
	if err := json.Unmarshal([]byte(delRows[0].InverseJSON), &linkRaw); err != nil {
		t.Fatalf("inverse json: %v", err)
	}
	for _, key := range []string{"op", "link"} {
		if _, ok := linkRaw[key]; !ok {
			t.Errorf("restore_link json misses key %q: %v", key, linkRaw)
		}
	}
	if link, ok := linkRaw["link"].(map[string]any); ok {
		for _, key := range []string{"id", "aInterfaceId", "zInterfaceId", "status", "label", "resourceId"} {
			if _, ok := link[key]; !ok {
				t.Errorf("restore_link json link misses camelCase key %q: %v", key, link)
			}
		}
	} else {
		t.Errorf("restore_link json link is not an object: %v", linkRaw["link"])
	}

	// delete_device → restore_device {device, interfaces, links} with the
	// captured cascade (device + eth0 + the link touching eth0).
	csDelDev := applyOK(t, svc, ws, []Op{{Op: domain.OpDeleteDevice, DeviceID: "vd-28"}})
	devRows, err := db.ChangeSets.GetOps(ctx, csDelDev.ChangeSetID)
	if err != nil || len(devRows) != 1 {
		t.Fatalf("GetOps(%s): %v (%d rows)", csDelDev.ChangeSetID, err, len(devRows))
	}
	invDev := decodeInverse(t, devRows[0].InverseJSON)
	if invDev.restoreDevice == nil {
		t.Fatalf("delete_device inverse = %q, want a restore_device payload", devRows[0].InverseJSON)
	}
	wantRestore := RestoreDeviceOp{
		Op: domain.OpRestoreDevice,
		Device: DeviceSnap{
			ID: "vd-28", Code: "NEW-1", Name: "new device", Status: "up", ResourceID: "",
		},
		Interfaces: []InterfaceSnap{{
			ID: "if-41", DeviceID: "vd-28", Name: "eth0", Status: "up",
			IPv4Addr: strPtr("10.9.0.1"), IPv4Prefix: intPtr(24),
		}},
		// Links: the captured cascade is empty — ln-21 was deleted by the
		// delete_link changeset above, so it no longer touches the device.
		Links: []LinkSnap{},
	}
	if !reflect.DeepEqual(*invDev.restoreDevice, wantRestore) {
		t.Fatalf("restore_device inverse = %+v, want %+v", *invDev.restoreDevice, wantRestore)
	}

	// delete_interface → restore_interface {interface, links} captures a
	// link touching the interface. DEV-008 carries two circuits (CKT-007
	// on if-14/eth0, CKT-008 on if-15/eth1); deleting if-15 cascades ln-8.
	ln8, err := db.SimLinks.GetByID(ctx, "ln-8")
	if err != nil {
		t.Fatalf("GetByID(ln-8): %v", err)
	}
	csDelIface := applyOK(t, svc, ws, []Op{{Op: domain.OpDeleteInterface, InterfaceID: "if-15"}})
	ifaceRows, err := db.ChangeSets.GetOps(ctx, csDelIface.ChangeSetID)
	if err != nil || len(ifaceRows) != 1 {
		t.Fatalf("GetOps(%s): %v (%d rows)", csDelIface.ChangeSetID, err, len(ifaceRows))
	}
	invIface := decodeInverse(t, ifaceRows[0].InverseJSON)
	if invIface.restoreInterface == nil {
		t.Fatalf("delete_interface inverse = %q, want a restore_interface payload", ifaceRows[0].InverseJSON)
	}
	wantIface := RestoreInterfaceOp{
		Op: domain.OpRestoreInterface,
		Interface: InterfaceSnap{
			ID: "if-15", DeviceID: "vd-8", Name: "eth1", Status: "up",
		},
		Links: []LinkSnap{{
			ID: "ln-8", AInterfaceID: "if-15", ZInterfaceID: "if-16",
			Status: "up", Label: "CKT-008", ResourceID: ln8.ResourceID,
		}},
	}
	if !reflect.DeepEqual(*invIface.restoreInterface, wantIface) {
		t.Fatalf("restore_interface inverse = %+v, want %+v", *invIface.restoreInterface, wantIface)
	}

	// ---- Part C: the engine applies the captured restores (undo path) ----
	// Reverse order: restore the interface (with its link), then the
	// device (with its interface — the device was deleted after the link
	// was re-created? No: replay exactly the inverse of the delete
	// changesets in reverse changeset order).
	hashAfterDeletes := mustHash(t, svc, ws)
	if hashAfterDeletes == hashAfterCreate {
		t.Fatalf("the deletes must have changed the state")
	}
	inverses := []internalOp{
		invIface, // inverse of delete_interface (re-creates if-15 + ln-8)
	}
	undoRes, err := svc.applyInverses(ctx, ws, inverses, domain.ChangeSetTriggerUndo, "undo of "+csDelIface.ChangeSetID, false, "")
	if err != nil {
		t.Fatalf("applyInverses(iface): %v", err)
	}
	if undoRes.Trigger != domain.ChangeSetTriggerUndo.String() || undoRes.OpsApplied != 1 {
		t.Fatalf("undo result = %+v, want trigger undo / 1 op", undoRes)
	}
	// The restore changeset's own inverse is a delete (undo-of-undo works).
	undoRows, err := db.ChangeSets.GetOps(ctx, undoRes.ChangeSetID)
	if err != nil || len(undoRows) != 1 {
		t.Fatalf("GetOps(undo %s): %v", undoRes.ChangeSetID, err)
	}
	invOfRestore := decodeInverse(t, undoRows[0].InverseJSON)
	if invOfRestore.isRestore() || invOfRestore.forward.Op != domain.OpDeleteInterface || invOfRestore.forward.InterfaceID != "if-15" {
		t.Fatalf("inverse of restore_interface = %+v, want delete_interface if-15", invOfRestore.forward)
	}

	// Full replay back to the post-create state: restore the device, then
	// re-create its link (the forward inverse of the delete_link step was
	// restore_link — apply it as well, in reverse order of the deletes:
	// delete_device → restore_device; delete_link → restore_link).
	if _, err := svc.applyInverses(ctx, ws, []internalOp{invDev, inv}, domain.ChangeSetTriggerUndo, "undo of deletes", false, ""); err != nil {
		t.Fatalf("applyInverses(device, link): %v", err)
	}
	if hash := mustHash(t, svc, ws); hash != hashAfterCreate {
		t.Fatalf("state after replaying all inverses = %s, want the exact post-create hash %s", hash, hashAfterCreate)
	}
	// Entities are back with their ORIGINAL ids.
	if _, err := db.VirtualDevices.GetByID(ctx, "vd-28"); err != nil {
		t.Fatalf("device vd-28 not restored: %v", err)
	}
	if _, err := db.SimInterfaces.GetByID(ctx, "if-41"); err != nil {
		t.Fatalf("interface if-41 not restored: %v", err)
	}
	if _, err := db.SimLinks.GetByID(ctx, "ln-21"); err != nil {
		t.Fatalf("link ln-21 not restored: %v", err)
	}
}
