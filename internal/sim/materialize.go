package sim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// statusInNet is the imported device status that materializes as an up
// virtual device (PROJECT_BRIEF_P2 §6; the Phase 1 enum lives in the
// mapping package — sim keeps its own pinned constant to stay
// layer-clean). Everything else (停用/在建/退网/empty/unknown) maps to down.
//
// BOUNDARY (issue #24 / data-model-v1.md §12-B3, ADR-0011): the lab is a
// DEVICE-LEVEL connectivity sandbox — it materializes device + circuit
// only, from the LEDGER status (台账 status), deliberately NOT from the
// runtime state (run_state, W22). The unified graph (resources/relations,
// traverse/impact endpoints) is the full resource model for analysis.
// One-liner for AI agents: analysis (impact/root-cause/business) uses the
// unified graph; simulation operations (disconnect/restore drills) use
// the lab. This boundary is deliberate — do not "fix" the ledger-status
// mapping here when run_state lands.
const statusInNet = "在网"

// MaterializeResult is the outcome of a successful Materialize.
type MaterializeResult struct {
	WorkspaceID    string   `json:"workspaceId"`
	Name           string   `json:"name"`
	SourceBatchIDs []string `json:"sourceBatchIds"`
	Counts         struct {
		Devices    int `json:"devices"`
		Interfaces int `json:"interfaces"`
		Links      int `json:"links"`
		// Layer overlay (issue #51, W33): the read-only canvas
		// projection — overlay nodes/edges and the distinct source
		// resource types that fed the workspace (drill + overlay).
		LayerNodes  int      `json:"layerNodes"`
		LayerLinks  int      `json:"layerLinks"`
		SourceTypes []string `json:"sourceTypes"`
	} `json:"counts"`
	WorkspaceHash string `json:"workspaceHash"`
}

// Materialize creates a workspace from imported resources
// (PROJECT_BRIEF_P2 §6): every device resource of the selected batches
// becomes a virtual device (status 在网 → up, anything else → down).
//
// LINKS follow the physical-layer-first rule (issue #49): when the
// dataset carries cable corridors (cable_segment + splice_box rows),
// links are the PER-HOP device interconnections along each optical
// cable's anchor chain — segments collapse between consecutive devices,
// so a hop spans one corridor span (~100-160km at province-trunk scale),
// never a circuit's end-to-end arc (the old behavior drew business
// circuits as device-direct lines averaging 448km, 96.9% over 200km —
// visually a ball of cross-province spaghetti). Datasets WITHOUT cable
// segments (the Phase 1 fixtures: devices + circuits only) fall back to
// the legacy circuit links unchanged. Circuits remain fully represented
// in the unified resource graph (traverse/impact) — the lab canvas is
// the physical drill sandbox, the graph is the business analysis view.
//
// batchIDs empty selects ALL completed batches (in import order). A
// duplicate name yields domain.ErrConflict, an unknown batch id
// domain.ErrNotFound, and a selection without any device resource
// domain.ErrNoSourceResources. Everything is written in ONE transaction —
// materialization is NOT a changeset; lineage lives in
// workspaces.source_json.
func (s *Service) Materialize(ctx context.Context, name, description string, batchIDs []string) (MaterializeResult, error) {
	if name == "" {
		return MaterializeResult{}, fmt.Errorf("sim: materialize: name must not be empty: %w", domain.ErrInvalidOp)
	}
	// Duplicate name pre-check; the UNIQUE constraint stays the final
	// authority inside the transaction.
	if _, err := s.db.Workspaces.GetByName(ctx, name); err == nil {
		return MaterializeResult{}, fmt.Errorf("sim: materialize %q: %w", name, domain.ErrConflict)
	} else if !isNotFound(err) {
		return MaterializeResult{}, fmt.Errorf("sim: materialize %q: %w", name, err)
	}

	var result MaterializeResult
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		// Resolve the batch scope. Explicit ids must all exist; the
		// default scope is every completed batch in import order
		// (Batches.List returns newest-first; reversed → ascending import
		// order, which is also the deterministic source_batch_ids order).
		scope := batchIDs
		if len(scope) == 0 {
			batches, _, err := tx.Batches.List(ctx, -1, 0)
			if err != nil {
				return fmt.Errorf("sim: materialize: list batches: %w", err)
			}
			scope = make([]string, 0, len(batches))
			for i := len(batches) - 1; i >= 0; i-- {
				if batches[i].Status == domain.BatchStatusCompleted {
					scope = append(scope, batches[i].ID)
				}
			}
		} else {
			for _, id := range scope {
				if _, err := tx.Batches.Get(ctx, id); err != nil {
					return fmt.Errorf("sim: materialize: batch %s: %w", id, err)
				}
			}
		}

		// Devices: every device resource whose first_batch_id is in scope
		// (Search orders by natural_key = device_code — the deterministic
		// creation order).
		devResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeDevice})
		if err != nil {
			return fmt.Errorf("sim: materialize: list device resources: %w", err)
		}
		scopeSet := make(map[string]bool, len(scope))
		for _, id := range scope {
			scopeSet[id] = true
		}
		var devices []domain.Resource
		for _, res := range devResources {
			if scopeSet[res.FirstBatchID] {
				devices = append(devices, res)
			}
		}
		if len(devices) == 0 {
			return fmt.Errorf("sim: materialize %q: %w", name, domain.ErrNoSourceResources)
		}

		// Workspace row (uuid id per PROJECT_BRIEF_P2 §3).
		workspaceID := uuid.NewString()
		sourceJSON, err := json.Marshal(domain.WorkspaceSource{BatchIDs: scope})
		if err != nil {
			return fmt.Errorf("sim: materialize: marshal source: %w", err)
		}
		now := s.clock()
		if err := tx.Workspaces.Insert(ctx, domain.Workspace{
			ID: workspaceID, Name: name, Description: description,
			Status: domain.WorkspaceStatusActive, SourceJSON: string(sourceJSON),
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("sim: materialize %q: %w", name, err)
		}
		if s.afterWrite != nil {
			if err := s.afterWrite("workspace"); err != nil {
				return fmt.Errorf("sim: materialize (fault injection after workspace): %w", err)
			}
		}

		// Virtual devices: code = natural key (the importer stores the
		// device_code as the resource natural key — §6's
		// "attributes.device_code" maps to it), name = device_name
		// attribute, status 在网 → up else down (empty/unknown ⇒ down).
		deviceByResource := make(map[string]domain.VirtualDevice, len(devices))
		for _, res := range devices {
			n, err := tx.Sequences.Allocate(ctx, domain.SeqDevice)
			if err != nil {
				return fmt.Errorf("sim: materialize: allocate device id: %w", err)
			}
			dev := domain.VirtualDevice{
				ID:          domain.FormatSimID(domain.SeqDevice, n),
				WorkspaceID: workspaceID,
				ResourceID:  res.ID,
				Code:        res.NaturalKey,
				Name:        res.Attributes["device_name"],
				Status:      deviceStatusOfResource(res),
				CreatedAt:   now, UpdatedAt: now,
			}
			// Optional geographic position from the resource
			// attributes (plain decimal strings written by the
			// importer's geo split). Invalid values are ignored
			// — import already validated them; this is defensive
			// only. Geo is presentation metadata and never
			// enters the state hash.
			dev.GeoLat, dev.GeoLng = geoOfResource(res)
			if dev.Name == "" {
				dev.Name = res.NaturalKey // defensive: device_name is a required field
			}
			if err := tx.VirtualDevices.Insert(ctx, dev); err != nil {
				return fmt.Errorf("sim: materialize: insert device %s: %w", dev.Code, err)
			}
			deviceByResource[res.ID] = dev
			result.Counts.Devices++
		}
		if s.afterWrite != nil {
			if err := s.afterWrite("devices"); err != nil {
				return fmt.Errorf("sim: materialize (fault injection after devices): %w", err)
			}
		}

		// Interfaces — REAL port resources first (issue #51): port→board→
		// device attribution; datasets without ports (the Phase 1 fixtures)
		// keep the synthetic eth<n> path byte-identically. Link endpoints
		// bind real interfaces first, spilling to synthetic only when a
		// device's real ports are exhausted.
		alloc := newIfaceAllocator()
		realIfaces, err := s.materializePortInterfaces(ctx, tx, workspaceID, scopeSet, deviceByResource, alloc, now)
		if err != nil {
			return err
		}
		if s.afterWrite != nil {
			if err := s.afterWrite("interfaces"); err != nil {
				return fmt.Errorf("sim: materialize (fault injection after interfaces): %w", err)
			}
		}

		// Links — physical layer first (issue #49): per-hop device links
		// along cable corridor anchor chains; circuits remain the fallback
		// for datasets without cable segments (Phase 1 fixtures).
		nLinks, err := s.materializePhysicalLinks(ctx, tx, workspaceID, scopeSet, deviceByResource, alloc, now)
		if err != nil {
			return err
		}
		if nLinks == 0 {
			nLinks, err = s.materializeCircuitLinks(ctx, tx, workspaceID, scopeSet, deviceByResource, alloc, now)
			if err != nil {
				return err
			}
		}
		if s.afterWrite != nil {
			if err := s.afterWrite("links"); err != nil {
				return fmt.Errorf("sim: materialize (fault injection after links): %w", err)
			}
		}

		// Layer overlay (issue #51, W33): the read-only canvas projection of
		// the unified graph — L0/L4/L5/L6 nodes + L3/L4/L5/L6 edges. Never
		// part of the drill state or the workspace hash.
		overlay, err := s.materializeLayers(ctx, tx, workspaceID, scopeSet, deviceByResource, alloc.portIface, now)
		if err != nil {
			return err
		}
		result.Counts.LayerNodes = overlay.Nodes
		result.Counts.LayerLinks = overlay.Links
		result.Counts.SourceTypes = append(
			[]string{domain.ResourceTypeDevice.String(), domain.ResourceTypePort.String()},
			overlay.SourceTypes...)

		result.Counts.Links = nLinks

		result.WorkspaceID = workspaceID
		result.Name = name
		result.SourceBatchIDs = scope
		// Interfaces = REAL port interfaces + synthetic endpoints bound by
		// links (issue #51: the count is no longer the 2×links formula).
		result.Counts.Interfaces = realIfaces + alloc.synthetic

		// Hash from the same typed state loader as every other read (one
		// source of truth), still inside the transaction.
		state, err := loadState(ctx, storeOfTx(tx), workspaceID)
		if err != nil {
			return err
		}
		hash, err := state.hash()
		if err != nil {
			return err
		}
		result.WorkspaceHash = hash
		return nil
	})
	if err != nil {
		return MaterializeResult{}, fmt.Errorf("sim: materialize: %w", err)
	}
	return result, nil
}

// materializePhysicalLinks derives PHYSICAL per-hop links from the
// cable corridors (issue #49) and returns how many were created. Zero
// (with nil error) means the dataset carries no in-scope cable segments
// — the caller then falls back to the legacy circuit links (Phase 1
// fixtures).
//
// Algorithm — corridor anchor-chain folding, fully deterministic:
//
//  1. Load cable_segment resources (scope-filtered) and splice_box
//     resources (geo-anchoring lookup).
//  2. Order segments by (cable natural key asc, segment_index asc).
//  3. Each segment end (a_end_box / z_end_box) resolves through the
//     splice_box geo to the NEAREST geo-carrying workspace device
//     (haversine; ties break on the lower device code — the -01 device
//     of a station wins). Box codes compare case-insensitively (the
//     importer normalizes both sides).
//  4. Walk each cable's segment sequence as a chain of device endpoints;
//     consecutive equal devices collapse (intermediate corridor anchors
//     without devices are skipped — the ledger's honest truth), and each
//     consecutive DISTINCT device pair becomes one hop link labeled with
//     the cable code, ResourceID = the optical_cable resource (cable-
//     level lineage: hops of one cable share it).
//  5. Every cable contributes its own hops (two parallel cables along
//     one corridor stay two links — the real redundancy shape); the
//     deterministic creation order is (cable code, chain position).
//
// Segments with an unresolvable end (missing box, box without geo, or a
// nil resolution) contribute no chain endpoint for that side — nothing
// is silently invented. A storage error aborts the materialize (AUD1-5).
func (s *Service) materializePhysicalLinks(
	ctx context.Context,
	tx *repo.Tx,
	workspaceID string,
	scopeSet map[string]bool,
	deviceByResource map[string]domain.VirtualDevice,
	alloc *ifaceAllocator,
	now time.Time,
) (int, error) {
	boxResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeSpliceBox})
	if err != nil {
		return 0, fmt.Errorf("sim: materialize: list splice boxes: %w", err)
	}
	boxGeo := make(map[string][2]float64, len(boxResources))
	for _, box := range boxResources {
		lat := parseGeoAttr(box.Attributes["geo_lat"], -90, 90)
		lng := parseGeoAttr(box.Attributes["geo_lng"], -180, 180)
		if lat == nil || lng == nil {
			continue
		}
		boxGeo[strings.ToUpper(box.NaturalKey)] = [2]float64{*lng, *lat}
	}

	type geoDev struct {
		dev      domain.VirtualDevice
		lng, lat float64
	}
	var geoDevices []geoDev
	for _, dev := range deviceByResource {
		if dev.GeoLat == nil || dev.GeoLng == nil {
			continue
		}
		geoDevices = append(geoDevices, geoDev{dev: dev, lng: *dev.GeoLng, lat: *dev.GeoLat})
	}
	if len(boxGeo) == 0 || len(geoDevices) == 0 {
		return 0, nil // no anchored corridors / no positioned devices → fallback
	}
	sort.Slice(geoDevices, func(i, j int) bool { return geoDevices[i].dev.Code < geoDevices[j].dev.Code })

	segResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeCableSegment})
	if err != nil {
		return 0, fmt.Errorf("sim: materialize: list cable segments: %w", err)
	}
	type segRow struct {
		cableCode  string
		index      int
		aBox, zBox string
	}
	var segs []segRow
	inScope := 0
	for _, seg := range segResources {
		if !scopeSet[seg.FirstBatchID] {
			continue
		}
		inScope++
		idx, _ := strconv.Atoi(seg.Attributes["segment_index"])
		segs = append(segs, segRow{
			cableCode: seg.Attributes["cable_code"],
			index:     idx,
			aBox:      strings.ToUpper(seg.Attributes["a_end_box"]),
			zBox:      strings.ToUpper(seg.Attributes["z_end_box"]),
		})
	}
	if inScope == 0 {
		return 0, nil // Phase 1 dataset — circuits take over
	}
	sort.Slice(segs, func(i, j int) bool {
		if segs[i].cableCode != segs[j].cableCode {
			return segs[i].cableCode < segs[j].cableCode
		}
		return segs[i].index < segs[j].index
	})

	resolveEnd := func(box string) *domain.VirtualDevice {
		geo, ok := boxGeo[box]
		if !ok {
			return nil
		}
		var best *domain.VirtualDevice
		bestKm := math.Inf(1)
		for _, gd := range geoDevices {
			km := haversineKm(geo[1], geo[0], gd.lat, gd.lng)
			if km < bestKm-1e-9 { // strictly nearer; pool order breaks ties
				bestKm = km
				d := gd.dev
				best = &d
			}
		}
		return best
	}

	cableResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeOpticalCable})
	if err != nil {
		return 0, fmt.Errorf("sim: materialize: list optical cables: %w", err)
	}
	cableByCode := make(map[string]domain.Resource, len(cableResources))
	for _, cab := range cableResources {
		if scopeSet[cab.FirstBatchID] {
			cableByCode[strings.ToUpper(cab.NaturalKey)] = cab
		}
	}

	type hop struct {
		cableCode  string
		pos        int
		devA, devZ domain.VirtualDevice
	}
	var hops []hop
	var chain []domain.VirtualDevice // resolved endpoints of the current cable
	var curCable string
	flushCable := func() {
		for i := 0; i+1 < len(chain); i++ {
			if chain[i].ID == chain[i+1].ID {
				continue // collapsed intermediate anchor without devices
			}
			hops = append(hops, hop{cableCode: curCable, pos: i, devA: chain[i], devZ: chain[i+1]})
		}
		chain = chain[:0]
	}
	for _, seg := range segs {
		if seg.cableCode != curCable {
			flushCable()
			curCable = seg.cableCode
		}
		if devA := resolveEnd(seg.aBox); devA != nil {
			chain = append(chain, *devA)
		}
		if devZ := resolveEnd(seg.zBox); devZ != nil {
			chain = append(chain, *devZ)
		}
	}
	flushCable()

	sort.Slice(hops, func(i, j int) bool {
		if hops[i].cableCode != hops[j].cableCode {
			return hops[i].cableCode < hops[j].cableCode
		}
		return hops[i].pos < hops[j].pos
	})
	created := 0
	for _, h := range hops {
		aIfaceID, err := alloc.allocate(ctx, tx, workspaceID, h.devA.ID, now)
		if err != nil {
			return 0, err
		}
		zIfaceID, err := alloc.allocate(ctx, tx, workspaceID, h.devZ.ID, now)
		if err != nil {
			return 0, err
		}
		n, err := tx.Sequences.Allocate(ctx, domain.SeqLink)
		if err != nil {
			return 0, fmt.Errorf("sim: materialize: allocate link id: %w", err)
		}
		label := h.cableCode
		resourceID := ""
		if cab, ok := cableByCode[strings.ToUpper(h.cableCode)]; ok {
			resourceID = cab.ID
			if label == "" {
				label = cab.NaturalKey
			}
		}
		if label == "" {
			label = fmt.Sprintf("hop#%d", h.pos+1)
		}
		if err := tx.SimLinks.Insert(ctx, domain.SimLink{
			ID: domain.FormatSimID(domain.SeqLink, n), WorkspaceID: workspaceID,
			AInterfaceID: aIfaceID, ZInterfaceID: zIfaceID,
			Status: domain.EntityStatusUp, Label: label, ResourceID: resourceID,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return 0, fmt.Errorf("sim: materialize: insert link %s: %w", label, err)
		}
		created++
	}
	return created, nil
}

// materializeCircuitLinks is the legacy link source (the Phase 1 shape):
// every circuit whose a_end/z_end relations both resolve to workspace
// devices becomes a link with deterministic eth<n> port allocation
// (circuits in ascending natural_key order, lowest free port index per
// endpoint device). Self-referencing circuits are skipped (mirrors the
// Phase 1 W003 row). Circuit status is ignored — links start up.
func (s *Service) materializeCircuitLinks(
	ctx context.Context,
	tx *repo.Tx,
	workspaceID string,
	scopeSet map[string]bool,
	deviceByResource map[string]domain.VirtualDevice,
	alloc *ifaceAllocator,
	now time.Time,
) (int, error) {
	circuitResources, _, err := tx.Resources.Search(ctx, repo.ResourceSearchFilter{Type: domain.ResourceTypeCircuit})
	if err != nil {
		return 0, fmt.Errorf("sim: materialize: list circuit resources: %w", err)
	}
	created := 0
	for _, res := range circuitResources {
		if !scopeSet[res.FirstBatchID] {
			continue
		}
		aEnd, zEnd, ok, err := circuitEndpoints(ctx, tx, res.ID)
		if err != nil {
			return 0, fmt.Errorf("sim: materialize: endpoints of circuit %s: %w (AUD1-5)", res.NaturalKey, err)
		}
		if !ok {
			continue // missing endpoint relation(s)
		}
		devA, okA := deviceByResource[aEnd]
		devZ, okZ := deviceByResource[zEnd]
		if !okA || !okZ {
			continue // endpoint not a workspace device (out of scope)
		}
		if devA.ID == devZ.ID {
			continue // self-referencing circuit (Phase 1 W003) — skipped
		}
		aIfaceID, err := alloc.allocate(ctx, tx, workspaceID, devA.ID, now)
		if err != nil {
			return 0, err
		}
		zIfaceID, err := alloc.allocate(ctx, tx, workspaceID, devZ.ID, now)
		if err != nil {
			return 0, err
		}
		n, err := tx.Sequences.Allocate(ctx, domain.SeqLink)
		if err != nil {
			return 0, fmt.Errorf("sim: materialize: allocate link id: %w", err)
		}
		if err := tx.SimLinks.Insert(ctx, domain.SimLink{
			ID: domain.FormatSimID(domain.SeqLink, n), WorkspaceID: workspaceID,
			AInterfaceID: aIfaceID, ZInterfaceID: zIfaceID,
			Status: domain.EntityStatusUp, Label: res.NaturalKey, ResourceID: res.ID,
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return 0, fmt.Errorf("sim: materialize: insert link %s: %w", res.NaturalKey, err)
		}
		created++
	}
	return created, nil
}

// haversineKm is the great-circle distance in kilometers (the same
// formula the realdata pipeline uses for nearest-device uplinks).
func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}

// circuitEndpoints resolves a circuit resource's a_end and z_end relations
// to device resource ids. ok is false when an endpoint relation is missing;
// err is non-nil when the relation read itself failed (propagated — never
// masked as a missing endpoint, AUD1-5).
func circuitEndpoints(ctx context.Context, tx *repo.Tx, circuitID string) (aEnd, zEnd string, ok bool, err error) {
	rels, err := tx.Relations.ListByResource(ctx, circuitID)
	if err != nil {
		return "", "", false, fmt.Errorf("list relations of circuit %s: %w", circuitID, err)
	}
	for _, rel := range rels {
		switch rel.Type {
		case domain.RelationTypeAEnd:
			aEnd = rel.ToResourceID
		case domain.RelationTypeZEnd:
			zEnd = rel.ToResourceID
		}
	}
	if aEnd == "" || zEnd == "" {
		return "", "", false, nil
	}
	return aEnd, zEnd, true, nil
}

// deviceStatusOfResource maps the resource status onto the entity status:
// 在网 → up; empty/退网/停用/在建/anything else → down (§6).
func deviceStatusOfResource(res domain.Resource) domain.EntityStatus {
	if res.Status == statusInNet {
		return domain.EntityStatusUp
	}
	return domain.EntityStatusDown
}

// geoOfResource parses the optional geo_lat/geo_lng device attributes into
// the virtual device position pointers. Each coordinate is adopted only
// when it parses AND is in range (lat [-90,90], lng [-180,180]); absent,
// unparsable or out-of-range values yield nil (import already validated the
// columns — ignoring here is defensive, never a hard failure). The two
// coordinates are independent: a partial pair still carries the valid half.
func geoOfResource(res domain.Resource) (lat, lng *float64) {
	lat = parseGeoAttr(res.Attributes["geo_lat"], -90, 90)
	lng = parseGeoAttr(res.Attributes["geo_lng"], -180, 180)
	return lat, lng
}

// parseGeoAttr parses one geo attribute as a float within [min, max]; nil
// when absent, unparsable (NaN/Inf included) or out of range.
func parseGeoAttr(v string, min, max float64) *float64 {
	if v == "" {
		return nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < min || f > max {
		return nil
	}
	return &f
}

// isNotFound reports whether err wraps domain.ErrNotFound.
func isNotFound(err error) bool {
	return errors.Is(err, domain.ErrNotFound)
}
