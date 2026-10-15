package sim

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// materialize_layers.go — issue #51 (W33): the interface materialization
// from REAL port resources and the read-only LAYER OVERLAY (virtual_nodes +
// virtual_layer_links), the canvas projection of the unified resource graph.
//
// Two materialization inputs joined here:
//
//  1. REAL INTERFACES — port resources whose port→board→device chain
//     (mounted_on + installed_in) resolves to a workspace device become
//     sim interfaces NAMED by the port's own name (xgei-1/1/1), replacing
//     the legacy reverse-inference ("2 per link, eth<n>") whenever the
//     dataset carries ports. Datasets WITHOUT ports — the Phase 1
//     fixtures — keep the legacy synthetic path byte-identically. Link
//     endpoints bind REAL interfaces first (deterministic lowest order),
//     spilling to synthetic eth<n> only when a device's real ports are
//     exhausted.
//
//  2. THE OVERLAY — L0/L3/L4/L5/L6 projections (see domain/layers.go for
//     the boundary): logical carrying edges (circuit/otn_och), access
//     nodes (base_station clustered by uplink device, olt/onu/bbu/rru),
//     grouping nodes (ring/igp/vpn/protection_group/ip_pool) and business
//     nodes (customer/service/leased_line) with their relation edges. The
//     overlay is written once, never mutated by ops, excluded from the
//     state hash — presentation, not drill state.

// ifaceAllocator hands out interfaces for link endpoints: real port
// interfaces first (in materialization order), then synthetic eth<n>.
type ifaceAllocator struct {
	// real maps device sim id → real (port-derived) interface ids not yet
	// bound to a link, in deterministic order.
	real map[string][]string
	// portIface maps port resource id → the interface id materialized from
	// it (the in_vpn group edges reference interfaces by this mapping).
	portIface map[string]string
	// next is the legacy synthetic eth<n> counter per device.
	next map[string]int
	// synthetic counts the eth<n> interfaces created (reporting only).
	synthetic int
}

func newIfaceAllocator() *ifaceAllocator {
	return &ifaceAllocator{real: map[string][]string{}, portIface: map[string]string{}, next: map[string]int{}}
}

// materializePortInterfaces materializes the in-scope port resources as
// real interfaces: port → mounted_on → board → installed_in → device.
// Ports whose attribution chain breaks (missing board ref, board without
// device, device out of scope) are counted and skipped — nothing is
// silently invented. The interfaces are inserted in port natural-key
// order (Search order), so every device's real list is ordered by port
// name. Returns the number created.
func (s *Service) materializePortInterfaces(
	ctx context.Context,
	tx *repo.Tx,
	workspaceID string,
	scopeSet map[string]bool,
	deviceByResource map[string]domain.VirtualDevice,
	alloc *ifaceAllocator,
	now time.Time,
) (int, error) {
	portResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypePort})
	if err != nil {
		return 0, fmt.Errorf("sim: materialize: list port resources: %w", err)
	}
	boardResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeBoard})
	if err != nil {
		return 0, fmt.Errorf("sim: materialize: list board resources: %w", err)
	}
	// board → device attribution (installed_in), scope-filtered.
	boardDevice := make(map[string]domain.VirtualDevice, len(boardResources))
	for _, board := range boardResources {
		if !scopeSet[board.FirstBatchID] {
			continue
		}
		rels, err := tx.Relations.ListByResource(ctx, board.ID)
		if err != nil {
			return 0, fmt.Errorf("sim: materialize: relations of board %s: %w (AUD1-5)", board.NaturalKey, err)
		}
		for _, rel := range rels {
			if rel.Type == domain.RelationTypeInstalledIn {
				if dev, ok := deviceByResource[rel.ToResourceID]; ok {
					boardDevice[board.ID] = dev
				}
			}
		}
	}
	created := 0
	for _, res := range portResources {
		if !scopeSet[res.FirstBatchID] {
			continue
		}
		// Attribution: the port's mounted_on board.
		rels, err := tx.Relations.ListByResource(ctx, res.ID)
		if err != nil {
			return 0, fmt.Errorf("sim: materialize: relations of port %s: %w (AUD1-5)", res.NaturalKey, err)
		}
		var dev domain.VirtualDevice
		ok := false
		for _, rel := range rels {
			if rel.Type == domain.RelationTypeMountedOn {
				if d, found := boardDevice[rel.ToResourceID]; found {
					dev, ok = d, true
				}
			}
		}
		if !ok {
			continue // unattributable port (no board ref / broken chain) — skipped, counted
		}
		status := domain.EntityStatusUp
		if strings.EqualFold(res.Attributes["oper_status"], "down") {
			status = domain.EntityStatusDown
		}
		// The port's own name: the attrs value, else the resource NAME
		// (NameFieldOverride pins a port resource's name to port_name).
		name := res.Attributes["port_name"]
		if name == "" {
			name = res.Name
		}
		if name == "" {
			continue // port without a name cannot render an interface — skipped
		}
		n, err := tx.Sequences.Allocate(ctx, domain.SeqInterface)
		if err != nil {
			return 0, fmt.Errorf("sim: materialize: allocate interface id: %w", err)
		}
		ifaceID := domain.FormatSimID(domain.SeqInterface, n)
		if err := tx.SimInterfaces.Insert(ctx, domain.SimInterface{
			ID: ifaceID, WorkspaceID: workspaceID, DeviceID: dev.ID,
			Name: name, Status: status,
			IPv4Addr: "", IPv4Prefix: -1,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return 0, fmt.Errorf("sim: materialize: insert interface %s: %w", ifaceID, err)
		}
		alloc.real[dev.ID] = append(alloc.real[dev.ID], ifaceID)
		alloc.portIface[res.ID] = ifaceID
		created++
	}
	return created, nil
}

// allocate binds one endpoint interface of a link on the device: the next
// free REAL port interface, else the synthetic eth<next> (the Phase 1
// path). Deterministic per device in call order.
func (a *ifaceAllocator) allocate(ctx context.Context, tx *repo.Tx, workspaceID, deviceID string, now time.Time) (string, error) {
	if free := a.real[deviceID]; len(free) > 0 {
		id := free[0]
		a.real[deviceID] = free[1:]
		return id, nil
	}
	idx := a.next[deviceID]
	a.next[deviceID] = idx + 1
	a.synthetic++
	n, err := tx.Sequences.Allocate(ctx, domain.SeqInterface)
	if err != nil {
		return "", fmt.Errorf("sim: materialize: allocate interface id: %w", err)
	}
	ifaceID := domain.FormatSimID(domain.SeqInterface, n)
	if err := tx.SimInterfaces.Insert(ctx, domain.SimInterface{
		ID: ifaceID, WorkspaceID: workspaceID, DeviceID: deviceID,
		Name: fmt.Sprintf("eth%d", idx), Status: domain.EntityStatusUp,
		IPv4Addr: "", IPv4Prefix: -1,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("sim: materialize: insert interface %s: %w", ifaceID, err)
	}
	return ifaceID, nil
}

// overlayResult reports the layer overlay materialization outcome.
type overlayResult struct {
	Nodes       int      // virtual_nodes rows
	Links       int      // virtual_layer_links rows
	SourceTypes []string // distinct source resource types that fed the overlay (sorted)
}

// materializeLayers builds the read-only overlay (issue #51): L0/L4/L5/L6
// nodes + L3/L4/L5/L6 edges projected from the unified resource graph,
// scoped to the lineage batches. Everything is deterministic: resources
// come ordered by natural key, relations by the ListAll order, devices by
// code; ids are sequence-allocated in that order.
//
// NOTHING here mutates drill state or enters the workspace state hash.
func (s *Service) materializeLayers(
	ctx context.Context,
	tx *repo.Tx,
	workspaceID string,
	scopeSet map[string]bool,
	deviceByResource map[string]domain.VirtualDevice,
	portIface map[string]string,
	now time.Time,
) (overlayResult, error) {
	var out overlayResult

	// —— relation index (one ListAll scan; deterministic order) ——
	relsByFrom := map[string][]domain.Relation{}
	relations, err := tx.Relations.ListAll(ctx)
	if err != nil {
		return out, fmt.Errorf("sim: materialize: list relations: %w", err)
	}
	for _, rel := range relations {
		relsByFrom[rel.FromResourceID] = append(relsByFrom[rel.FromResourceID], rel)
	}
	firstRel := func(from string, types ...domain.RelationType) string {
		for _, rel := range relsByFrom[from] {
			for _, t := range types {
				if rel.Type == t {
					return rel.ToResourceID
				}
			}
		}
		return ""
	}

	// —— device resources in natural-key order (scope-filtered) ——
	devResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeDevice})
	if err != nil {
		return out, fmt.Errorf("sim: materialize: list device resources (overlay): %w", err)
	}
	type devRow struct {
		res domain.Resource
		dev domain.VirtualDevice
	}
	devRows := make([]devRow, 0, len(deviceByResource))
	for _, res := range devResources {
		if !scopeSet[res.FirstBatchID] {
			continue
		}
		if dev, ok := deviceByResource[res.ID]; ok {
			devRows = append(devRows, devRow{res: res, dev: dev})
		}
	}

	// —— site → lowest-code device (olt/onu anchoring, och Z-ends) ——
	siteDevice := map[string]domain.VirtualDevice{}
	mrSite := map[string]string{} // machine_room resource id → site resource id
	mrResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeMachineRoom})
	if err != nil {
		return out, fmt.Errorf("sim: materialize: list machine rooms: %w", err)
	}
	for _, mr := range mrResources {
		if site := firstRel(mr.ID, domain.RelationTypeBelongsTo); site != "" {
			mrSite[mr.ID] = site
		}
	}
	for _, row := range devRows {
		mr := firstRel(row.res.ID, domain.RelationTypeHousedIn)
		if mr == "" {
			continue
		}
		site, ok := mrSite[mr]
		if !ok {
			continue
		}
		if _, exists := siteDevice[site]; !exists {
			siteDevice[site] = row.dev // devRows is natural-key ascending → first wins
		}
	}

	// —— insert helpers ——
	typeCount := map[string]bool{}
	nodeIDByResource := map[string]string{} // resource id → overlay node id
	insertNode := func(res domain.Resource, layer, kind, anchorDeviceID, clusterKey, detail string) (string, error) {
		n, err := tx.Sequences.Allocate(ctx, domain.SeqLayerNode)
		if err != nil {
			return "", fmt.Errorf("sim: materialize: allocate layer node id: %w", err)
		}
		id := domain.FormatSimID(domain.SeqLayerNode, n)
		node := domain.LayerNode{
			ID: id, WorkspaceID: workspaceID, ResourceID: res.ID,
			Layer: layer, Kind: kind, Code: res.NaturalKey, Name: res.Name,
			Status: res.Status, AnchorDeviceID: anchorDeviceID, ClusterKey: clusterKey,
			DetailJSON: detail, CreatedAt: now, UpdatedAt: now,
		}
		node.GeoLat, node.GeoLng = geoOfResource(res)
		if err := tx.LayerNodes.Insert(ctx, node); err != nil {
			return "", fmt.Errorf("sim: materialize: insert layer node %s: %w", res.NaturalKey, err)
		}
		out.Nodes++
		typeCount[res.Type.String()] = true
		nodeIDByResource[res.ID] = id
		return id, nil
	}
	insertLink := func(layer, sem, label, resourceID, aKind, aRef, zKind, zRef string) error {
		n, err := tx.Sequences.Allocate(ctx, domain.SeqLayerLink)
		if err != nil {
			return fmt.Errorf("sim: materialize: allocate layer link id: %w", err)
		}
		if err := tx.LayerLinks.Insert(ctx, domain.LayerLink{
			ID: domain.FormatSimID(domain.SeqLayerLink, n), WorkspaceID: workspaceID,
			Layer: layer, Sem: sem, Label: label, ResourceID: resourceID,
			AKind: aKind, ARef: aRef, ZKind: zKind, ZRef: zRef,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("sim: materialize: insert layer link %s: %w", label, err)
		}
		out.Links++
		return nil
	}
	searchType := func(rt domain.ResourceType) ([]domain.Resource, error) {
		resources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: rt})
		if err != nil {
			return nil, fmt.Errorf("sim: materialize: list %s resources: %w", rt, err)
		}
		scoped := make([]domain.Resource, 0, len(resources))
		for _, res := range resources {
			if scopeSet[res.FirstBatchID] {
				scoped = append(scoped, res)
			}
		}
		return scoped, nil
	}

	// ================= L0 — spatial anchors (manhole/pole) =================
	// site/machine_room/duct/rack stay containers (spec §B: not drawn).
	for _, rt := range []domain.ResourceType{domain.ResourceTypeManhole, domain.ResourceTypePole} {
		list, err := searchType(rt)
		if err != nil {
			return out, err
		}
		for _, res := range list {
			if _, err := insertNode(res, domain.LayerSpatial, rt.String(), "", "", ""); err != nil {
				return out, err
			}
		}
	}

	// ================= L4 — access nodes =================
	// base_station: anchor = uplink_to device; the anchor doubles as the
	// CLUSTER KEY — the read path aggregates stations per anchor device.
	stationAnchor := map[string]string{} // station resource id → anchor device id
	stationServes := map[string]int{}    // customer resource id → station serves count
	stations, err := searchType(domain.ResourceTypeBaseStation)
	if err != nil {
		return out, err
	}
	for _, res := range stations {
		anchor := ""
		if dev, ok := deviceByResource[firstRel(res.ID, domain.RelationTypeUplinkTo)]; ok {
			anchor = dev.ID
		}
		if customer := firstRel(res.ID, domain.RelationTypeServes); customer != "" {
			stationServes[customer]++
		}
		stationAnchor[res.ID] = anchor
		if _, err := insertNode(res, domain.LayerAccess, "base_station", anchor, anchor, ""); err != nil {
			return out, err
		}
	}

	// olt: anchor via its machine_room (plain-string attr, an id value) →
	// site → the site's lowest-code device. PON ports ride as node detail.
	ponPortsByOLT := map[string][]string{}
	{
		pons, err := searchType(domain.ResourceTypePONPort)
		if err != nil {
			return out, err
		}
		for _, res := range pons {
			olt := res.Attributes["olt_code"]
			if olt == "" {
				continue
			}
			if port := res.Attributes["port_no"]; port != "" {
				ponPortsByOLT[olt] = append(ponPortsByOLT[olt], port)
			}
		}
	}
	oltAnchor := map[string]string{} // olt resource id → anchor device id
	olts, err := searchType(domain.ResourceTypeOLT)
	if err != nil {
		return out, err
	}
	for _, res := range olts {
		anchor := ""
		if mr := res.Attributes["machine_room_id"]; mr != "" {
			for _, m := range mrResources {
				if strings.EqualFold(m.NaturalKey, mr) {
					if site, ok := mrSite[m.ID]; ok {
						if dev, ok := siteDevice[site]; ok {
							anchor = dev.ID
						}
					}
					break
				}
			}
		}
		oltAnchor[res.ID] = anchor
		detail := ""
		if ports := ponPortsByOLT[res.NaturalKey]; len(ports) > 0 {
			sort.Strings(ports)
			if b, err := json.Marshal(map[string]any{"ponPorts": ports}); err == nil {
				detail = string(b)
			}
		}
		if _, err := insertNode(res, domain.LayerAccess, "olt", anchor, "", detail); err != nil {
			return out, err
		}
	}

	// onu: anchor = the OLT's anchor (connects_onu → pon_port's olt_code,
	// or the plain olt_code attr — both are id values, never names).
	onus, err := searchType(domain.ResourceTypeONU)
	if err != nil {
		return out, err
	}
	onuOLT := map[string]string{} // onu resource id → olt natural key
	for _, res := range onus {
		oltKey := ""
		if pon := firstRel(res.ID, domain.RelationTypeConnectsONU); pon != "" {
			pons, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypePONPort})
			if err != nil {
				return out, fmt.Errorf("sim: materialize: list pon ports (onu anchor): %w", err)
			}
			for _, p := range pons {
				if p.ID == pon {
					oltKey = p.Attributes["olt_code"]
					break
				}
			}
		}
		if oltKey == "" {
			oltKey = res.Attributes["olt_code"]
		}
		anchor := ""
		if oltKey != "" {
			for _, o := range olts {
				if o.NaturalKey == oltKey {
					anchor = oltAnchor[o.ID]
					break
				}
			}
		}
		onuOLT[res.ID] = oltKey
		if _, err := insertNode(res, domain.LayerAccess, "onu", anchor, "", ""); err != nil {
			return out, err
		}
	}

	// bbu: anchor = the station's anchor (所属基站 plain-string attr = the
	// station's natural key — an id value, not name inference).
	bbus, err := searchType(domain.ResourceTypeBBU)
	if err != nil {
		return out, err
	}
	stationByKey := map[string]domain.Resource{}
	for _, st := range stations {
		stationByKey[st.NaturalKey] = st
	}
	for _, res := range bbus {
		anchor := ""
		if st, ok := stationByKey[res.Attributes["base_station_code"]]; ok {
			anchor = stationAnchor[st.ID]
		}
		if _, err := insertNode(res, domain.LayerAccess, "bbu", anchor, "", ""); err != nil {
			return out, err
		}
	}

	// rru: anchor via covers → station.
	rrus, err := searchType(domain.ResourceTypeRRU)
	if err != nil {
		return out, err
	}
	for _, res := range rrus {
		anchor := ""
		if st := firstRel(res.ID, domain.RelationTypeCovers); st != "" {
			anchor = stationAnchor[st]
		}
		if _, err := insertNode(res, domain.LayerAccess, "rru", anchor, "", ""); err != nil {
			return out, err
		}
	}

	// ================= L5 — grouping nodes =================
	groupTypes := []domain.ResourceType{
		domain.ResourceTypeRing, domain.ResourceTypeIGPDomain, domain.ResourceTypeVPNInstance,
		domain.ResourceTypeProtectionGroup, domain.ResourceTypeIPPool,
	}
	groupNode := map[string]string{} // resource id → node id
	for _, rt := range groupTypes {
		list, err := searchType(rt)
		if err != nil {
			return out, err
		}
		for _, res := range list {
			id, err := insertNode(res, domain.LayerGrouping, rt.String(), "", "", "")
			if err != nil {
				return out, err
			}
			groupNode[res.ID] = id
		}
	}

	// ================= L6 — business nodes =================
	customerNode := map[string]string{}
	customers, err := searchType(domain.ResourceTypeCustomer)
	if err != nil {
		return out, err
	}
	for _, res := range customers {
		detail := ""
		if b, err := json.Marshal(map[string]int{"stationServes": stationServes[res.ID]}); err == nil {
			detail = string(b)
		}
		id, err := insertNode(res, domain.LayerBusiness, "customer", "", "", detail)
		if err != nil {
			return out, err
		}
		customerNode[res.ID] = id
	}
	serviceNode := map[string]string{}
	services, err := searchType(domain.ResourceTypeService)
	if err != nil {
		return out, err
	}
	for _, res := range services {
		anchor := ""
		if dev, ok := deviceByResource[firstRel(res.ID, domain.RelationTypeDependsOn)]; ok {
			anchor = dev.ID
		}
		id, err := insertNode(res, domain.LayerBusiness, "service", anchor, "", "")
		if err != nil {
			return out, err
		}
		serviceNode[res.ID] = id
	}
	leasedLines, err := searchType(domain.ResourceTypeLeasedLine)
	if err != nil {
		return out, err
	}
	for _, res := range leasedLines {
		if _, err := insertNode(res, domain.LayerBusiness, "leased_line", "", "", ""); err != nil {
			return out, err
		}
	}

	// ================= L3 — logical carrying edges =================
	// circuit: a_end/z_end devices (direct relations).
	circuits, err := searchType(domain.ResourceTypeCircuit)
	if err != nil {
		return out, err
	}
	circuitByKey := map[string]domain.Resource{}
	for _, res := range circuits {
		circuitByKey[res.ID] = res
	}
	for _, res := range circuits {
		aDev, aOK := deviceByResource[firstRel(res.ID, domain.RelationTypeAEnd)]
		zDev, zOK := deviceByResource[firstRel(res.ID, domain.RelationTypeZEnd)]
		if !aOK || !zOK || aDev.ID == zDev.ID {
			continue
		}
		if err := insertLink(domain.LayerLogical, domain.SemLogical, res.NaturalKey, res.ID,
			domain.LayerEndpointDevice, aDev.ID, domain.LayerEndpointDevice, zDev.ID); err != nil {
			return out, err
		}
	}
	// otn_och: routed_over → cable; A = cable's connects_device (else the
	// A-site device), Z = the Z-site's lowest-code device.
	ochs, err := searchType(domain.ResourceTypeOTNOch)
	if err != nil {
		return out, err
	}
	for _, res := range ochs {
		cable := firstRel(res.ID, domain.RelationTypeRoutedOver)
		if cable == "" {
			continue
		}
		aDev, aOK := deviceByResource[firstRel(cable, domain.RelationTypeConnectsDevice)]
		if !aOK {
			if site := firstRel(cable, domain.RelationTypeCableAEnd); site != "" {
				if dev, ok := siteDevice[site]; ok {
					aDev, aOK = dev, true
				}
			}
		}
		zDev, zOK := deviceByResource[firstRel(cable, domain.RelationTypeConnectsDevice)]
		if site := firstRel(cable, domain.RelationTypeCableZEnd); site != "" {
			if dev, ok := siteDevice[site]; ok {
				zDev, zOK = dev, true
			}
		}
		if !aOK || !zOK || aDev.ID == zDev.ID {
			continue
		}
		if err := insertLink(domain.LayerLogical, domain.SemLogical, res.NaturalKey, res.ID,
			domain.LayerEndpointDevice, aDev.ID, domain.LayerEndpointDevice, zDev.ID); err != nil {
			return out, err
		}
	}

	// ================= L4 — access edges =================
	// onu → its OLT node (via connects_onu → pon_port → olt_code).
	for _, res := range onus {
		oltKey := onuOLT[res.ID]
		if oltKey == "" {
			continue
		}
		var oltNodeID string
		for _, o := range olts {
			if o.NaturalKey == oltKey {
				oltNodeID = nodeIDByResource[o.ID]
				break
			}
		}
		onuNodeID := nodeIDByResource[res.ID]
		if oltNodeID == "" || onuNodeID == "" {
			continue
		}
		label := oltKey
		if pon := res.Attributes["connect_port"]; pon != "" {
			label = fmt.Sprintf("%s|%s", oltKey, pon)
		}
		if err := insertLink(domain.LayerAccess, domain.SemAccess, label, res.ID,
			domain.LayerEndpointNode, onuNodeID, domain.LayerEndpointNode, oltNodeID); err != nil {
			return out, err
		}
	}
	// rru → home bbu.
	for _, res := range rrus {
		bbu := firstRel(res.ID, domain.RelationTypeHomeBBU)
		if bbu == "" {
			continue
		}
		bbuNodeID, ok := nodeIDByResource[bbu]
		if !ok {
			continue
		}
		if rruNodeID, ok := nodeIDByResource[res.ID]; ok {
			if err := insertLink(domain.LayerAccess, domain.SemAccess, res.NaturalKey, res.ID,
				domain.LayerEndpointNode, rruNodeID, domain.LayerEndpointNode, bbuNodeID); err != nil {
				return out, err
			}
		}
	}

	// ================= L5 — group membership edges =================
	for _, row := range devRows {
		for _, want := range []struct {
			relType domain.RelationType
			label   string
		}{
			{domain.RelationTypeMemberOfRing, "member_of_ring"},
			{domain.RelationTypeInDomain, "in_domain"},
		} {
			if group := firstRel(row.res.ID, want.relType); group != "" {
				if nodeID, ok := groupNode[group]; ok {
					if err := insertLink(domain.LayerGrouping, domain.SemGroup, want.label, "",
						domain.LayerEndpointDevice, row.dev.ID, domain.LayerEndpointNode, nodeID); err != nil {
						return out, err
					}
				}
			}
		}
	}
	// port in_vpn (interface endpoint — only materialized ports point here).
	{
		portResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypePort})
		if err != nil {
			return out, fmt.Errorf("sim: materialize: list ports (in_vpn): %w", err)
		}
		for _, res := range portResources {
			if !scopeSet[res.FirstBatchID] {
				continue
			}
			vpn := firstRel(res.ID, domain.RelationTypeInVPN)
			if vpn == "" {
				continue
			}
			iface, ok := portIface[res.ID]
			if !ok {
				continue // port not materialized as an interface — nothing to point at
			}
			if nodeID, ok := groupNode[vpn]; ok {
				if err := insertLink(domain.LayerGrouping, domain.SemGroup, "in_vpn", "",
					domain.LayerEndpointInterface, iface, domain.LayerEndpointNode, nodeID); err != nil {
					return out, err
				}
			}
		}
	}
	// protection_group protects → the circuit's a-end device.
	{
		pgs, err := searchType(domain.ResourceTypeProtectionGroup)
		if err != nil {
			return out, err
		}
		for _, res := range pgs {
			cir := firstRel(res.ID, domain.RelationTypeProtects)
			if cir == "" {
				continue
			}
			nodeID, ok := groupNode[res.ID]
			if !ok {
				continue
			}
			label := "保护"
			if circuit, found := circuitByKey[cir]; found {
				label = fmt.Sprintf("保护 %s", circuit.NaturalKey)
			}
			if dev, ok := deviceByResource[firstRel(cir, domain.RelationTypeAEnd)]; ok {
				if err := insertLink(domain.LayerGrouping, domain.SemGroup, label, res.ID,
					domain.LayerEndpointNode, nodeID, domain.LayerEndpointDevice, dev.ID); err != nil {
					return out, err
				}
			}
		}
	}
	// ip_pool assigned_to → device.
	{
		pools, err := searchType(domain.ResourceTypeIPPool)
		if err != nil {
			return out, err
		}
		for _, res := range pools {
			dev, ok := deviceByResource[firstRel(res.ID, domain.RelationTypeAssignedTo)]
			if !ok {
				continue
			}
			if nodeID, ok := groupNode[res.ID]; ok {
				if err := insertLink(domain.LayerGrouping, domain.SemGroup, "assigned_to", res.ID,
					domain.LayerEndpointNode, nodeID, domain.LayerEndpointDevice, dev.ID); err != nil {
					return out, err
				}
			}
		}
	}

	// ================= L6 — service edges =================
	// device serves → customer.
	for _, row := range devRows {
		customer := firstRel(row.res.ID, domain.RelationTypeServes)
		if customer == "" {
			continue
		}
		if nodeID, ok := customerNode[customer]; ok {
			if err := insertLink(domain.LayerBusiness, domain.SemService, "serves", "",
				domain.LayerEndpointDevice, row.dev.ID, domain.LayerEndpointNode, nodeID); err != nil {
				return out, err
			}
		}
	}
	// service: subscribed_by → customer; depends_on → device;
	// provided_by → the circuit's a-end device (label carries the circuit).
	for _, res := range services {
		svcNode := serviceNode[res.ID]
		if svcNode == "" {
			continue
		}
		if customer := firstRel(res.ID, domain.RelationTypeSubscribedBy); customer != "" {
			if nodeID, ok := customerNode[customer]; ok {
				if err := insertLink(domain.LayerBusiness, domain.SemService, "subscribed_by", res.ID,
					domain.LayerEndpointNode, svcNode, domain.LayerEndpointNode, nodeID); err != nil {
					return out, err
				}
			}
		}
		if dev, ok := deviceByResource[firstRel(res.ID, domain.RelationTypeDependsOn)]; ok {
			if err := insertLink(domain.LayerBusiness, domain.SemService, "depends_on", res.ID,
				domain.LayerEndpointNode, svcNode, domain.LayerEndpointDevice, dev.ID); err != nil {
				return out, err
			}
		}
		if cir := firstRel(res.ID, domain.RelationTypeProvidedBy); cir != "" {
			if dev, ok := deviceByResource[firstRel(cir, domain.RelationTypeAEnd)]; ok {
				label := "提供电路"
				if circuit, found := circuitByKey[cir]; found {
					label = fmt.Sprintf("提供电路 %s", circuit.NaturalKey)
				}
				if err := insertLink(domain.LayerBusiness, domain.SemService, label, res.ID,
					domain.LayerEndpointNode, svcNode, domain.LayerEndpointDevice, dev.ID); err != nil {
					return out, err
				}
			}
		}
	}
	// leased_line: subscribed_by → customer.
	for _, res := range leasedLines {
		customer := firstRel(res.ID, domain.RelationTypeSubscribedBy)
		if customer == "" {
			continue
		}
		custNode, ok := customerNode[customer]
		if !ok {
			continue
		}
		if llNode, ok := nodeIDByResource[res.ID]; ok {
			if err := insertLink(domain.LayerBusiness, domain.SemService, "subscribed_by", res.ID,
				domain.LayerEndpointNode, llNode, domain.LayerEndpointNode, custNode); err != nil {
				return out, err
			}
		}
	}

	out.SourceTypes = make([]string, 0, len(typeCount))
	for t := range typeCount {
		out.SourceTypes = append(out.SourceTypes, t)
	}
	sort.Strings(out.SourceTypes)
	return out, nil
}
