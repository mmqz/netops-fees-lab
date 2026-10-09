package sim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// applyOne validates the op's preconditions against the transaction's view
// of the state, mutates the state, and returns the computed inverse JSON —
// computed BEFORE the mutation for deletes (captured pre-state per
// PROJECT_BRIEF_P2 §4). Failures are semantic (unknown entity, duplicate
// code, self loop …) and roll the whole changeset back.
func (s *Service) applyOne(ctx context.Context, tx *repo.Tx, workspaceID string, op internalOp) (string, error) {
	switch {
	case op.restoreDevice != nil:
		return s.applyRestoreDevice(ctx, tx, workspaceID, op.restoreDevice)
	case op.restoreInterface != nil:
		return s.applyRestoreInterface(ctx, tx, workspaceID, op.restoreInterface)
	case op.restoreLink != nil:
		return s.applyRestoreLink(ctx, tx, workspaceID, op.restoreLink)
	}

	switch op.forward.Op {
	case domain.OpCreateDevice:
		return s.applyCreateDevice(ctx, tx, workspaceID, op.forward)
	case domain.OpSetDeviceStatus:
		return s.applySetDeviceStatus(ctx, tx, workspaceID, op.forward)
	case domain.OpDeleteDevice:
		return s.applyDeleteDevice(ctx, tx, workspaceID, op.forward)
	case domain.OpCreateInterface:
		return s.applyCreateInterface(ctx, tx, workspaceID, op.forward)
	case domain.OpSetInterfaceStatus:
		return s.applySetInterfaceStatus(ctx, tx, workspaceID, op.forward)
	case domain.OpConfigInterfaceIPv4:
		return s.applyConfigInterfaceIPv4(ctx, tx, workspaceID, op.forward)
	case domain.OpDeleteInterface:
		return s.applyDeleteInterface(ctx, tx, workspaceID, op.forward)
	case domain.OpCreateLink:
		return s.applyCreateLink(ctx, tx, workspaceID, op.forward)
	case domain.OpSetLinkStatus:
		return s.applySetLinkStatus(ctx, tx, workspaceID, op.forward)
	case domain.OpDeleteLink:
		return s.applyDeleteLink(ctx, tx, workspaceID, op.forward)
	default:
		return "", fmt.Errorf("unknown op code %q", op.forward.Op)
	}
}

// forwardInverse marshals a forward op as the inverse_json value. A set
// -but-empty inverse (never happens for applied ops) is an error.
func forwardInverse(op Op) (string, error) {
	if op.Op == "" {
		return "", fmt.Errorf("inverse op has no code")
	}
	b, err := json.Marshal(op)
	if err != nil {
		return "", fmt.Errorf("sim: marshal inverse op %s: %w", op.Op, err)
	}
	return string(b), nil
}

// statusOrDefault maps the optional status of create_* ops to the default
// "up" when absent (validated non-empty values pass through).
func statusOrDefault(status string) domain.EntityStatus {
	if status == "" {
		return domain.EntityStatusUp
	}
	return domain.EntityStatus(status)
}

// getDevice loads a device and verifies it belongs to the workspace
// (cross-workspace references behave like "not found"). A genuine storage
// error is propagated, not masked as a missing device.
func getDevice(ctx context.Context, tx *repo.Tx, workspaceID, deviceID string) (domain.VirtualDevice, error) {
	d, err := tx.VirtualDevices.GetByID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.VirtualDevice{}, fmt.Errorf("device not found: %s", deviceID)
		}
		return domain.VirtualDevice{}, fmt.Errorf("load device %s: %w", deviceID, err)
	}
	if d.WorkspaceID != workspaceID {
		return domain.VirtualDevice{}, fmt.Errorf("device not found: %s", deviceID)
	}
	return d, nil
}

// getInterface loads an interface and verifies workspace membership. A
// genuine storage error is propagated, not masked as a missing interface.
func getInterface(ctx context.Context, tx *repo.Tx, workspaceID, interfaceID string) (domain.SimInterface, error) {
	i, err := tx.SimInterfaces.GetByID(ctx, interfaceID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.SimInterface{}, fmt.Errorf("interface not found: %s", interfaceID)
		}
		return domain.SimInterface{}, fmt.Errorf("load interface %s: %w", interfaceID, err)
	}
	if i.WorkspaceID != workspaceID {
		return domain.SimInterface{}, fmt.Errorf("interface not found: %s", interfaceID)
	}
	return i, nil
}

// getLink loads a link and verifies workspace membership. A genuine storage
// error is propagated, not masked as a missing link.
func getLink(ctx context.Context, tx *repo.Tx, workspaceID, linkID string) (domain.SimLink, error) {
	l, err := tx.SimLinks.GetByID(ctx, linkID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.SimLink{}, fmt.Errorf("link not found: %s", linkID)
		}
		return domain.SimLink{}, fmt.Errorf("load link %s: %w", linkID, err)
	}
	if l.WorkspaceID != workspaceID {
		return domain.SimLink{}, fmt.Errorf("link not found: %s", linkID)
	}
	return l, nil
}

// applyCreateDevice inserts a new device; the inverse is
// delete_device {deviceId}. A duplicate code in the workspace is a semantic
// failure (op_failed, 422 at the API).
func (s *Service) applyCreateDevice(ctx context.Context, tx *repo.Tx, workspaceID string, op Op) (string, error) {
	if _, err := tx.VirtualDevices.GetByCode(ctx, workspaceID, op.Code); err == nil {
		return "", fmt.Errorf("device code already exists in workspace: %s", op.Code)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return "", fmt.Errorf("check device code %s: %w", op.Code, err)
	}
	n, err := tx.Sequences.Allocate(ctx, domain.SeqDevice)
	if err != nil {
		return "", fmt.Errorf("allocate device id: %w", err)
	}
	deviceID := domain.FormatSimID(domain.SeqDevice, n)
	now := s.clock()
	if err := tx.VirtualDevices.Insert(ctx, domain.VirtualDevice{
		ID: deviceID, WorkspaceID: workspaceID,
		Code: op.Code, Name: op.Name,
		Status:    domain.EntityStatusUp, // create_device has no status field (§4)
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("insert device %s: %w", deviceID, err)
	}
	return forwardInverse(Op{Op: domain.OpDeleteDevice, DeviceID: deviceID})
}

// applySetDeviceStatus flips the device status; the inverse restores the
// previous status.
func (s *Service) applySetDeviceStatus(ctx context.Context, tx *repo.Tx, workspaceID string, op Op) (string, error) {
	d, err := getDevice(ctx, tx, workspaceID, op.DeviceID)
	if err != nil {
		return "", err
	}
	prev := d.Status
	if err := tx.VirtualDevices.UpdateStatus(ctx, d.ID, domain.EntityStatus(op.Status)); err != nil {
		return "", fmt.Errorf("update device status: %w", err)
	}
	return forwardInverse(Op{Op: domain.OpSetDeviceStatus, DeviceID: d.ID, Status: prev.String()})
}

// applyDeleteDevice captures the device, its interfaces and the links
// touching them BEFORE the mutation, deletes the device (the DDL cascades
// the interfaces and links), and returns the restore_device inverse with
// the captured pre-state.
func (s *Service) applyDeleteDevice(ctx context.Context, tx *repo.Tx, workspaceID string, op Op) (string, error) {
	d, err := getDevice(ctx, tx, workspaceID, op.DeviceID)
	if err != nil {
		return "", err
	}
	ifaces, err := tx.SimInterfaces.ListByDevice(ctx, d.ID)
	if err != nil {
		return "", fmt.Errorf("list interfaces of device %s: %w", d.ID, err)
	}
	links, err := linksTouching(ctx, tx, ifaces)
	if err != nil {
		return "", err
	}
	if err := tx.VirtualDevices.Delete(ctx, d.ID); err != nil {
		return "", fmt.Errorf("delete device %s: %w", d.ID, err)
	}
	restore := &RestoreDeviceOp{
		Op: domain.OpRestoreDevice,
		Device: DeviceSnap{
			ID: d.ID, Code: d.Code, Name: d.Name, Status: d.Status.String(), ResourceID: d.ResourceID,
		},
		Interfaces: make([]InterfaceSnap, 0, len(ifaces)),
		Links:      make([]LinkSnap, 0, len(links)),
	}
	for _, i := range ifaces {
		restore.Interfaces = append(restore.Interfaces, snapOfInterface(i))
	}
	for _, l := range links {
		restore.Links = append(restore.Links, snapOfLink(l))
	}
	return restore.opJSON()
}

// applyCreateInterface inserts an interface on a device; the inverse is
// delete_interface {interfaceId}.
func (s *Service) applyCreateInterface(ctx context.Context, tx *repo.Tx, workspaceID string, op Op) (string, error) {
	d, err := getDevice(ctx, tx, workspaceID, op.DeviceID)
	if err != nil {
		return "", err
	}
	existing, err := tx.SimInterfaces.ListByDevice(ctx, d.ID)
	if err != nil {
		return "", fmt.Errorf("list interfaces of device %s: %w", d.ID, err)
	}
	for _, i := range existing {
		if i.Name == op.Name {
			return "", fmt.Errorf("interface name already exists on device %s: %s", d.Code, op.Name)
		}
	}
	addr, prefix, err := addrPrefixOf(op.IPv4Addr, op.IPv4Prefix)
	if err != nil {
		return "", err
	}
	n, err := tx.Sequences.Allocate(ctx, domain.SeqInterface)
	if err != nil {
		return "", fmt.Errorf("allocate interface id: %w", err)
	}
	ifaceID := domain.FormatSimID(domain.SeqInterface, n)
	now := s.clock()
	if err := tx.SimInterfaces.Insert(ctx, domain.SimInterface{
		ID: ifaceID, WorkspaceID: workspaceID, DeviceID: d.ID,
		Name: op.Name, Status: statusOrDefault(op.Status),
		IPv4Addr: addr, IPv4Prefix: prefix,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("insert interface %s: %w", ifaceID, err)
	}
	return forwardInverse(Op{Op: domain.OpDeleteInterface, InterfaceID: ifaceID})
}

// applySetInterfaceStatus flips the interface status; the inverse restores
// the previous status.
func (s *Service) applySetInterfaceStatus(ctx context.Context, tx *repo.Tx, workspaceID string, op Op) (string, error) {
	i, err := getInterface(ctx, tx, workspaceID, op.InterfaceID)
	if err != nil {
		return "", err
	}
	prev := i.Status
	if err := tx.SimInterfaces.UpdateStatus(ctx, i.ID, domain.EntityStatus(op.Status)); err != nil {
		return "", fmt.Errorf("update interface status: %w", err)
	}
	return forwardInverse(Op{Op: domain.OpSetInterfaceStatus, InterfaceID: i.ID, Status: prev.String()})
}

// applyConfigInterfaceIPv4 sets or clears the interface address; the
// inverse restores the previous address/prefix (nil = previously
// unconfigured ⇒ the inverse unconfigures).
func (s *Service) applyConfigInterfaceIPv4(ctx context.Context, tx *repo.Tx, workspaceID string, op Op) (string, error) {
	i, err := getInterface(ctx, tx, workspaceID, op.InterfaceID)
	if err != nil {
		return "", err
	}
	prevAddr, prevPrefix := i.IPv4Addr, i.IPv4Prefix
	addr, prefix, err := addrPrefixOf(op.IPv4Addr, op.IPv4Prefix)
	if err != nil {
		return "", err
	}
	if err := tx.SimInterfaces.UpdateIPv4(ctx, i.ID, addr, prefix); err != nil {
		return "", fmt.Errorf("update ipv4 of interface %s: %w", i.ID, err)
	}
	inverse := Op{Op: domain.OpConfigInterfaceIPv4, InterfaceID: i.ID}
	if prevAddr != "" {
		// Round-trip the previous values through pointers so the inverse
		// marshals them (nil would mean "unconfigure").
		a, p := prevAddr, prevPrefix
		inverse.IPv4Addr, inverse.IPv4Prefix = &a, &p
	}
	return forwardInverse(inverse)
}

// applyDeleteInterface captures the interface and the links touching it
// BEFORE the mutation, deletes it (the DDL cascades the links), and returns
// the restore_interface inverse with the captured pre-state.
func (s *Service) applyDeleteInterface(ctx context.Context, tx *repo.Tx, workspaceID string, op Op) (string, error) {
	i, err := getInterface(ctx, tx, workspaceID, op.InterfaceID)
	if err != nil {
		return "", err
	}
	links, err := linksTouching(ctx, tx, []domain.SimInterface{i})
	if err != nil {
		return "", err
	}
	if err := tx.SimInterfaces.Delete(ctx, i.ID); err != nil {
		return "", fmt.Errorf("delete interface %s: %w", i.ID, err)
	}
	restore := &RestoreInterfaceOp{
		Op:        domain.OpRestoreInterface,
		Interface: snapOfInterface(i),
		Links:     make([]LinkSnap, 0, len(links)),
	}
	for _, l := range links {
		restore.Links = append(restore.Links, snapOfLink(l))
	}
	return restore.opJSON()
}

// applyCreateLink inserts a link between two interfaces; both must exist
// in the workspace, be distinct and sit on DIFFERENT devices (a self loop
// is a semantic failure). The inverse is delete_link {linkId}.
func (s *Service) applyCreateLink(ctx context.Context, tx *repo.Tx, workspaceID string, op Op) (string, error) {
	a, err := getInterface(ctx, tx, workspaceID, op.AInterfaceID)
	if err != nil {
		return "", err
	}
	z, err := getInterface(ctx, tx, workspaceID, op.ZInterfaceID)
	if err != nil {
		return "", err
	}
	if a.ID == z.ID {
		return "", fmt.Errorf("link endpoints must be distinct interfaces: %s", a.ID)
	}
	if a.DeviceID == z.DeviceID {
		return "", fmt.Errorf("self loop: both interfaces are on device %s", a.DeviceID)
	}
	n, err := tx.Sequences.Allocate(ctx, domain.SeqLink)
	if err != nil {
		return "", fmt.Errorf("allocate link id: %w", err)
	}
	linkID := domain.FormatSimID(domain.SeqLink, n)
	now := s.clock()
	if err := tx.SimLinks.Insert(ctx, domain.SimLink{
		ID: linkID, WorkspaceID: workspaceID,
		AInterfaceID: a.ID, ZInterfaceID: z.ID,
		Status: statusOrDefault(op.Status), Label: op.Label,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("insert link %s: %w", linkID, err)
	}
	return forwardInverse(Op{Op: domain.OpDeleteLink, LinkID: linkID})
}

// applySetLinkStatus flips the link status; the inverse restores the
// previous status.
func (s *Service) applySetLinkStatus(ctx context.Context, tx *repo.Tx, workspaceID string, op Op) (string, error) {
	l, err := getLink(ctx, tx, workspaceID, op.LinkID)
	if err != nil {
		return "", err
	}
	prev := l.Status
	if err := tx.SimLinks.UpdateStatus(ctx, l.ID, domain.EntityStatus(op.Status)); err != nil {
		return "", fmt.Errorf("update link status: %w", err)
	}
	return forwardInverse(Op{Op: domain.OpSetLinkStatus, LinkID: l.ID, Status: prev.String()})
}

// applyDeleteLink captures the link row BEFORE the mutation, deletes it,
// and returns the restore_link inverse with the captured pre-state.
func (s *Service) applyDeleteLink(ctx context.Context, tx *repo.Tx, workspaceID string, op Op) (string, error) {
	l, err := getLink(ctx, tx, workspaceID, op.LinkID)
	if err != nil {
		return "", err
	}
	if err := tx.SimLinks.Delete(ctx, l.ID); err != nil {
		return "", fmt.Errorf("delete link %s: %w", l.ID, err)
	}
	restore := &RestoreLinkOp{Op: domain.OpRestoreLink, Link: snapOfLink(l)}
	return restore.opJSON()
}

// ---- restore op application (engine-internal, undo path) ----

// applyRestoreDevice re-inserts the captured device, interfaces and links
// with their ORIGINAL ids (FKs are valid because undo applies inverse ops
// in reverse seq order). Its own inverse is delete_device {deviceId}, so
// undo changesets are themselves undoable.
func (s *Service) applyRestoreDevice(ctx context.Context, tx *repo.Tx, workspaceID string, r *RestoreDeviceOp) (string, error) {
	now := s.clock()
	if err := tx.VirtualDevices.Insert(ctx, domain.VirtualDevice{
		ID: r.Device.ID, WorkspaceID: workspaceID,
		ResourceID: r.Device.ResourceID, Code: r.Device.Code, Name: r.Device.Name,
		Status:    domain.EntityStatus(r.Device.Status),
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("restore device %s: %w", r.Device.ID, err)
	}
	for _, i := range r.Interfaces {
		addr, prefix, err := snapAddrPrefix(i)
		if err != nil {
			return "", err
		}
		if err := tx.SimInterfaces.Insert(ctx, domain.SimInterface{
			ID: i.ID, WorkspaceID: workspaceID, DeviceID: i.DeviceID,
			Name: i.Name, Status: domain.EntityStatus(i.Status),
			IPv4Addr: addr, IPv4Prefix: prefix,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return "", fmt.Errorf("restore interface %s: %w", i.ID, err)
		}
	}
	for _, l := range r.Links {
		if err := tx.SimLinks.Insert(ctx, domain.SimLink{
			ID: l.ID, WorkspaceID: workspaceID,
			AInterfaceID: l.AInterfaceID, ZInterfaceID: l.ZInterfaceID,
			Status: domain.EntityStatus(l.Status), Label: l.Label, ResourceID: l.ResourceID,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return "", fmt.Errorf("restore link %s: %w", l.ID, err)
		}
	}
	return forwardInverse(Op{Op: domain.OpDeleteDevice, DeviceID: r.Device.ID})
}

// applyRestoreInterface re-inserts the captured interface and its links
// with their original ids. Its own inverse is
// delete_interface {interfaceId}.
func (s *Service) applyRestoreInterface(ctx context.Context, tx *repo.Tx, workspaceID string, r *RestoreInterfaceOp) (string, error) {
	now := s.clock()
	i := r.Interface
	addr, prefix, err := snapAddrPrefix(i)
	if err != nil {
		return "", err
	}
	if err := tx.SimInterfaces.Insert(ctx, domain.SimInterface{
		ID: i.ID, WorkspaceID: workspaceID, DeviceID: i.DeviceID,
		Name: i.Name, Status: domain.EntityStatus(i.Status),
		IPv4Addr: addr, IPv4Prefix: prefix,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("restore interface %s: %w", i.ID, err)
	}
	for _, l := range r.Links {
		if err := tx.SimLinks.Insert(ctx, domain.SimLink{
			ID: l.ID, WorkspaceID: workspaceID,
			AInterfaceID: l.AInterfaceID, ZInterfaceID: l.ZInterfaceID,
			Status: domain.EntityStatus(l.Status), Label: l.Label, ResourceID: l.ResourceID,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return "", fmt.Errorf("restore link %s: %w", l.ID, err)
		}
	}
	return forwardInverse(Op{Op: domain.OpDeleteInterface, InterfaceID: i.ID})
}

// applyRestoreLink re-inserts the captured link row with its original id.
// Its own inverse is delete_link {linkId}.
func (s *Service) applyRestoreLink(ctx context.Context, tx *repo.Tx, workspaceID string, r *RestoreLinkOp) (string, error) {
	l := r.Link
	now := s.clock()
	if err := tx.SimLinks.Insert(ctx, domain.SimLink{
		ID: l.ID, WorkspaceID: workspaceID,
		AInterfaceID: l.AInterfaceID, ZInterfaceID: l.ZInterfaceID,
		Status: domain.EntityStatus(l.Status), Label: l.Label, ResourceID: l.ResourceID,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("restore link %s: %w", l.ID, err)
	}
	return forwardInverse(Op{Op: domain.OpDeleteLink, LinkID: l.ID})
}

// ---- shared helpers ----

// linksTouching collects the links touching any of the interfaces, deduped
// and ordered by numeric id (a link cannot touch two interfaces of the same
// device — same-device endpoints are impossible — but the dedupe stays as
// a defensive invariant).
func linksTouching(ctx context.Context, tx *repo.Tx, ifaces []domain.SimInterface) ([]domain.SimLink, error) {
	seen := map[string]bool{}
	var links []domain.SimLink
	for _, i := range ifaces {
		touching, err := tx.SimLinks.ListTouchingInterface(ctx, i.ID)
		if err != nil {
			return nil, fmt.Errorf("list links touching interface %s: %w", i.ID, err)
		}
		for _, l := range touching {
			if !seen[l.ID] {
				seen[l.ID] = true
				links = append(links, l)
			}
		}
	}
	// ListTouchingInterface orders by id per call; the merged order needs
	// a global numeric sort.
	sortLinksNumeric(links)
	return links, nil
}

// snapOfInterface captures the pre-state of an interface.
func snapOfInterface(i domain.SimInterface) InterfaceSnap {
	snap := InterfaceSnap{
		ID: i.ID, DeviceID: i.DeviceID, Name: i.Name, Status: i.Status.String(),
	}
	if i.IPv4Addr != "" {
		addr, prefix := i.IPv4Addr, i.IPv4Prefix
		snap.IPv4Addr, snap.IPv4Prefix = &addr, &prefix
	}
	return snap
}

// snapOfLink captures the pre-state of a link.
func snapOfLink(l domain.SimLink) LinkSnap {
	return LinkSnap{
		ID: l.ID, AInterfaceID: l.AInterfaceID, ZInterfaceID: l.ZInterfaceID,
		Status: l.Status.String(), Label: l.Label, ResourceID: l.ResourceID,
	}
}

// snapAddrPrefix maps an InterfaceSnap's address representation onto the
// domain representation ("" / -1 = unconfigured).
func snapAddrPrefix(i InterfaceSnap) (string, int, error) {
	if i.IPv4Addr == nil || i.IPv4Prefix == nil {
		if i.IPv4Addr != nil || i.IPv4Prefix != nil {
			return "", 0, fmt.Errorf("restore interface %s: ipv4Addr and ipv4Prefix must both be set or both null", i.ID)
		}
		return "", -1, nil
	}
	return *i.IPv4Addr, *i.IPv4Prefix, nil
}

// addrPrefixOf maps the op's IPv4 pointers onto the domain representation,
// canonicalizing the address text (already validated by ValidateOp).
func addrPrefixOf(addr *string, prefix *int) (string, int, error) {
	if addr == nil || prefix == nil {
		if addr != nil || prefix != nil {
			return "", 0, fmt.Errorf("ipv4Addr and ipv4Prefix must both be set or both null")
		}
		return "", -1, nil
	}
	canonical, err := parseIPv4(*addr)
	if err != nil {
		return "", 0, err
	}
	return canonical, *prefix, nil
}
