package sim

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// store bundles the sim repositories a state read needs; both *repo.DB and
// *repo.Tx provide them, so the same loader serves autocommit reads and
// reads inside a transaction (checks must see the not-yet-committed state
// of the apply they run in — no, checks run in their own transaction; the
// loader's tx mode exists for consistent multi-table snapshots).
type store struct {
	devices    *repo.VirtualDeviceRepo
	interfaces *repo.SimInterfaceRepo
	links      *repo.SimLinkRepo
}

// storeOfDB extracts the store of the DB handle (autocommit mode).
func storeOfDB(db *repo.DB) store {
	return store{devices: db.VirtualDevices, interfaces: db.SimInterfaces, links: db.SimLinks}
}

// storeOfTx extracts the store of a transaction.
func storeOfTx(tx *repo.Tx) store {
	return store{devices: tx.VirtualDevices, interfaces: tx.SimInterfaces, links: tx.SimLinks}
}

// stateDevice is one device of the canonical state with its interfaces
// (sorted by name).
type stateDevice struct {
	Dev        domain.VirtualDevice
	Interfaces []domain.SimInterface
}

// simState is the canonical typed state of one workspace — the single
// source of truth for the topology payload, the workspace state hash, the
// check computations, the connectivity BFS and the run edge snapshot.
// Ordering: devices by code, interfaces by (device code, name), links by
// numeric id (ids are globally monotonic ln-<n>, so this is creation
// order; plain lexicographic SQL ORDER BY would put ln-10 before ln-2).
type simState struct {
	workspaceID string
	devices     []stateDevice
	links       []domain.SimLink

	ifaceDevice map[string]string // interface id → device id
	deviceByID  map[string]domain.VirtualDevice
	ifaceByID   map[string]domain.SimInterface
}

// loadState reads the full workspace state through the given store (either
// the DB handle or a live transaction) in canonical order.
func loadState(ctx context.Context, st store, workspaceID string) (*simState, error) {
	devices, err := st.devices.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("sim: load devices of workspace %s: %w", workspaceID, err)
	}
	ifaces, err := st.interfaces.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("sim: load interfaces of workspace %s: %w", workspaceID, err)
	}
	links, err := st.links.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("sim: load links of workspace %s: %w", workspaceID, err)
	}
	sort.SliceStable(links, func(i, j int) bool {
		return simIDLess(links[i].ID, links[j].ID)
	})

	s := &simState{
		workspaceID: workspaceID,
		links:       links,
		ifaceDevice: make(map[string]string, len(ifaces)),
		deviceByID:  make(map[string]domain.VirtualDevice, len(devices)),
		ifaceByID:   make(map[string]domain.SimInterface, len(ifaces)),
	}

	// Devices arrive sorted by code; index them.
	for _, d := range devices {
		s.deviceByID[d.ID] = d
		s.devices = append(s.devices, stateDevice{Dev: d})
	}
	// Map interfaces to devices (each device's list sorted by name below).
	ifacesByDevice := make(map[string][]domain.SimInterface, len(devices))
	for _, i := range ifaces {
		s.ifaceByID[i.ID] = i
		s.ifaceDevice[i.ID] = i.DeviceID
		ifacesByDevice[i.DeviceID] = append(ifacesByDevice[i.DeviceID], i)
	}
	for idx := range s.devices {
		list := ifacesByDevice[s.devices[idx].Dev.ID]
		sort.SliceStable(list, func(a, b int) bool { return list[a].Name < list[b].Name })
		s.devices[idx].Interfaces = list
	}
	return s, nil
}

// simIDLess orders "<kind>-<n>" ids numerically (vd-2 < vd-10), which plain
// lexicographic comparison would get wrong.
func simIDLess(a, b string) bool {
	ka, na, oka := splitSimID(a)
	kb, nb, okb := splitSimID(b)
	if oka && okb {
		if ka != kb {
			return ka < kb
		}
		return na < nb
	}
	return a < b
}

// splitSimID splits "<kind>-<n>"; ok is false for malformed ids.
func splitSimID(id string) (kind string, n int, ok bool) {
	i := strings.LastIndexByte(id, '-')
	if i < 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil {
		return "", 0, false
	}
	return id[:i], n, true
}

// sortLinksNumeric sorts links by numeric id ascending in place.
func sortLinksNumeric(links []domain.SimLink) {
	sort.SliceStable(links, func(i, j int) bool {
		return simIDLess(links[i].ID, links[j].ID)
	})
}

// ---- canonical state hash (PROJECT_BRIEF_P2 §3) ----

// hashDevice / hashInterface / hashLink / hashState mirror the exact JSON
// shape of the state hash: devices sorted by code, interfaces by name,
// links by (aDevice, aInterface, zDevice, zInterface, label); unconfigured
// addresses render as null (pointers, no omitempty); field order exactly
// as written in the brief.
type hashDevice struct {
	Code       string          `json:"code"`
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	Interfaces []hashInterface `json:"interfaces"`
}

type hashInterface struct {
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	IPv4Addr   *string `json:"ipv4Addr"`
	IPv4Prefix *int    `json:"ipv4Prefix"`
}

type hashLink struct {
	ADevice    string `json:"aDevice"`
	AInterface string `json:"aInterface"`
	ZDevice    string `json:"zDevice"`
	ZInterface string `json:"zInterface"`
	Status     string `json:"status"`
	Label      string `json:"label"`
}

type hashState struct {
	Devices []hashDevice `json:"devices"`
	Links   []hashLink   `json:"links"`
}

// hashValue renders the state as the canonical hash JSON (devices already
// arrive code-sorted; links are sorted here per §3).
func (s *simState) hashValue() (string, error) {
	out := hashState{Devices: make([]hashDevice, 0, len(s.devices)), Links: make([]hashLink, 0, len(s.links))}
	for _, d := range s.devices {
		hd := hashDevice{Code: d.Dev.Code, Name: d.Dev.Name, Status: d.Dev.Status.String(),
			Interfaces: make([]hashInterface, 0, len(d.Interfaces))}
		for _, i := range d.Interfaces {
			hd.Interfaces = append(hd.Interfaces, hashInterface{
				Name: i.Name, Status: i.Status.String(),
				IPv4Addr: hashAddr(i.IPv4Addr), IPv4Prefix: hashPrefix(i.IPv4Prefix),
			})
		}
		out.Devices = append(out.Devices, hd)
	}
	for _, l := range s.links {
		a, z := s.linkEnds(l)
		out.Links = append(out.Links, hashLink{
			ADevice: a.deviceCode, AInterface: a.ifaceName,
			ZDevice: z.deviceCode, ZInterface: z.ifaceName,
			Status: l.Status.String(), Label: l.Label,
		})
	}
	sort.SliceStable(out.Links, func(i, j int) bool {
		a, b := out.Links[i], out.Links[j]
		if a.ADevice != b.ADevice {
			return a.ADevice < b.ADevice
		}
		if a.AInterface != b.AInterface {
			return a.AInterface < b.AInterface
		}
		if a.ZDevice != b.ZDevice {
			return a.ZDevice < b.ZDevice
		}
		if a.ZInterface != b.ZInterface {
			return a.ZInterface < b.ZInterface
		}
		return a.Label < b.Label
	})
	b, err := json.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("sim: marshal state hash: %w", err)
	}
	return string(b), nil
}

// hash returns the canonical workspace state hash: sha256 hex over the
// canonical state JSON (PROJECT_BRIEF_P2 §3).
func (s *simState) hash() (string, error) {
	v, err := s.hashValue()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:]), nil
}

// hashAddr maps the domain representation ("" = unconfigured) onto the
// hash representation (nil ⇒ JSON null).
func hashAddr(addr string) *string {
	if addr == "" {
		return nil
	}
	return &addr
}

// hashPrefix maps the domain representation (-1 = unconfigured) onto the
// hash representation (nil ⇒ JSON null).
func hashPrefix(prefix int) *int {
	if prefix < 0 {
		return nil
	}
	return &prefix
}

// linkEnd resolves a link's two endpoints to (device, interface) pairs.
func (s *simState) linkEnds(l domain.SimLink) (linkEnd, linkEnd) {
	a := s.endOf(l.AInterfaceID)
	z := s.endOf(l.ZInterfaceID)
	return a, z
}

// endOf resolves one interface id to its device code and name (plus the
// device and interface statuses for the run edge snapshot's full-predicate
// extension).
func (s *simState) endOf(ifaceID string) linkEnd {
	var e linkEnd
	if iface, ok := s.ifaceByID[ifaceID]; ok {
		e.ifaceName = iface.Name
		e.ifaceStatus = iface.Status.String()
		if dev, ok := s.deviceByID[iface.DeviceID]; ok {
			e.deviceID = dev.ID
			e.deviceCode = dev.Code
			e.deviceStatus = dev.Status.String()
		}
	}
	return e
}

// linkEnd is one resolved link endpoint.
type linkEnd struct {
	deviceID     string
	deviceCode   string
	ifaceName    string
	deviceStatus string // up/down of the endpoint's device ("" when unresolvable)
	ifaceStatus  string // up/down of the endpoint interface ("" when unresolvable)
}

// statusPtr wraps a status string as a pointer, mapping "" (endpoint
// unresolvable — links always resolve through loadState, so this never
// fires in practice) to a nil pointer read back as up by the diff.
func statusPtr(status string) *string {
	if status == "" {
		return nil
	}
	s := status
	return &s
}

// ---- run edge snapshot (check_runs.topology_json, §5) ----

// topoLink is one entry of the canonical edge snapshot: ALL links (up and
// down) in the same sort as the state-hash links array, camelCase fields
// aDevice/aInterface/zDevice/zInterface/status/label. Since v0.2.1 the
// snapshot also records the STATUS of both endpoints' devices and
// interfaces (AUD1-2): the run diff needs the full §5 connectivity
// predicate (link up AND both interfaces up AND both devices up), which the
// original six-field shape could not express. The four status fields are
// POINTERS: v0.2.0 rows (legacy snapshot format) lack them entirely, and a
// missing value reads back as nil — the diff treats nil as up, so legacy
// runs keep their historical link-only diff behavior byte-for-byte.
type topoLink struct {
	ADevice    string `json:"aDevice"`
	AInterface string `json:"aInterface"`
	ZDevice    string `json:"zDevice"`
	ZInterface string `json:"zInterface"`
	Status     string `json:"status"`
	Label      string `json:"label"`

	// Endpoint statuses (full-predicate extension, nil on legacy rows).
	ADeviceStatus    *string `json:"aDeviceStatus,omitempty"`
	AInterfaceStatus *string `json:"aInterfaceStatus,omitempty"`
	ZDeviceStatus    *string `json:"zDeviceStatus,omitempty"`
	ZInterfaceStatus *string `json:"zInterfaceStatus,omitempty"`
}

// topologyJSON renders the run edge snapshot as a JSON array of topoLink
// (canonical sort). The workspace STATE HASH is unaffected by this format:
// it is computed from the entity state (hashValue), never from the run
// snapshot.
func (s *simState) topologyJSON() (string, error) {
	links := make([]topoLink, 0, len(s.links))
	for _, l := range s.links {
		a, z := s.linkEnds(l)
		links = append(links, topoLink{
			ADevice: a.deviceCode, AInterface: a.ifaceName,
			ZDevice: z.deviceCode, ZInterface: z.ifaceName,
			Status: l.Status.String(), Label: l.Label,
			ADeviceStatus:    statusPtr(a.deviceStatus),
			AInterfaceStatus: statusPtr(a.ifaceStatus),
			ZDeviceStatus:    statusPtr(z.deviceStatus),
			ZInterfaceStatus: statusPtr(z.ifaceStatus),
		})
	}
	sort.SliceStable(links, func(i, j int) bool {
		a, b := links[i], links[j]
		if a.ADevice != b.ADevice {
			return a.ADevice < b.ADevice
		}
		if a.AInterface != b.AInterface {
			return a.AInterface < b.AInterface
		}
		if a.ZDevice != b.ZDevice {
			return a.ZDevice < b.ZDevice
		}
		if a.ZInterface != b.ZInterface {
			return a.ZInterface < b.ZInterface
		}
		return a.Label < b.Label
	})
	b, err := json.Marshal(links)
	if err != nil {
		return "", fmt.Errorf("sim: marshal topology snapshot: %w", err)
	}
	return string(b), nil
}

// parseTopoLinks decodes a stored topology_json snapshot (used by the run
// diff to recompute reachability without replaying ops; legacy rows simply
// leave the four endpoint-status pointers nil).
func parseTopoLinks(raw string) ([]topoLink, error) {
	if raw == "" {
		return nil, nil
	}
	var links []topoLink
	if err := json.Unmarshal([]byte(raw), &links); err != nil {
		return nil, fmt.Errorf("sim: parse topology snapshot: %w", err)
	}
	return links, nil
}
