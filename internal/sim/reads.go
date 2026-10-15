package sim

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// TopologyNode is one node of the workspace topology payload: a virtual
// device with its interfaces (sorted by name). GeoLat/GeoLng carry the
// OPTIONAL geographic position (nil ⇒ omitted from the JSON) — pure
// presentation metadata for map views; the canonical workspace state hash
// deliberately excludes them.
type TopologyNode struct {
	ID         string              `json:"id"`
	Code       string              `json:"code"`
	Name       string              `json:"name"`
	Status     string              `json:"status"`
	GeoLat     *float64            `json:"geoLat,omitempty"`
	GeoLng     *float64            `json:"geoLng,omitempty"`
	Interfaces []TopologyInterface `json:"interfaces"`
	// ResourceId is the device's lineage resource (issue #51: the canvas
	// impact analysis anchors on it). "" = manually created, no lineage.
	ResourceID string `json:"resourceId,omitempty"`
}

// TopologyInterface is one interface of a topology node. IPv4Addr "" and
// IPv4Prefix -1 mean "unconfigured" (the payload representation differs
// deliberately from the state hash, which renders nulls — §7 vs §3).
type TopologyInterface struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	IPv4Addr   string `json:"ipv4Addr"`
	IPv4Prefix int    `json:"ipv4Prefix"`
}

// TopologyEdge is one link of the workspace topology payload, resolved to
// both endpoint devices (ids and codes) and interfaces.
type TopologyEdge struct {
	ID           string `json:"id"`
	AInterfaceID string `json:"aInterfaceId"`
	ADeviceID    string `json:"aDeviceId"`
	ADeviceCode  string `json:"aDeviceCode"`
	ZInterfaceID string `json:"zInterfaceId"`
	ZDeviceID    string `json:"zDeviceId"`
	ZDeviceCode  string `json:"zDeviceCode"`
	Status       string `json:"status"`
	Label        string `json:"label"`
	// ResourceId is the link's lineage resource (circuit or optical_cable
	// of the corridor hop — issue #51: the canvas impact analysis anchors
	// on it). "" = synthetic, no lineage.
	ResourceID string `json:"resourceId,omitempty"`
}

// Topology is the full workspace topology response: nodes sorted by code,
// interfaces by name, edges by numeric id (creation order).
type Topology struct {
	WorkspaceID   string         `json:"workspaceId"`
	WorkspaceHash string         `json:"workspaceHash"`
	Nodes         []TopologyNode `json:"nodes"`
	Edges         []TopologyEdge `json:"edges"`
	// Layers is the read-only overlay (issue #51): L0/L4/L5/L6 nodes
	// (base stations aggregated per anchor device) + L3/L4/L5/L6 semantic
	// edges. nil (omitted) for pre-v0.12 workspaces.
	Layers *TopologyLayers `json:"layers,omitempty"`
}

// topology renders the API payload from the canonical state.
func (s *simState) topology(workspaceID, hash string) Topology {
	topo := Topology{
		WorkspaceID:   workspaceID,
		WorkspaceHash: hash,
		Nodes:         make([]TopologyNode, 0, len(s.devices)),
		Edges:         make([]TopologyEdge, 0, len(s.links)),
	}
	for _, d := range s.devices {
		node := TopologyNode{
			ID:         d.Dev.ID,
			Code:       d.Dev.Code,
			Name:       d.Dev.Name,
			Status:     d.Dev.Status.String(),
			GeoLat:     d.Dev.GeoLat,
			GeoLng:     d.Dev.GeoLng,
			Interfaces: make([]TopologyInterface, 0, len(d.Interfaces)),
			ResourceID: d.Dev.ResourceID,
		}
		for _, i := range d.Interfaces {
			node.Interfaces = append(node.Interfaces, TopologyInterface{
				ID:         i.ID,
				Name:       i.Name,
				Status:     i.Status.String(),
				IPv4Addr:   i.IPv4Addr,
				IPv4Prefix: i.IPv4Prefix,
			})
		}
		topo.Nodes = append(topo.Nodes, node)
	}
	for _, l := range s.links {
		a, z := s.linkEnds(l)
		topo.Edges = append(topo.Edges, TopologyEdge{
			ID:           l.ID,
			AInterfaceID: l.AInterfaceID,
			ADeviceID:    a.deviceID,
			ADeviceCode:  a.deviceCode,
			ZInterfaceID: l.ZInterfaceID,
			ZDeviceID:    z.deviceID,
			ZDeviceCode:  z.deviceCode,
			Status:       l.Status.String(),
			Label:        l.Label,
			ResourceID:   l.ResourceID,
		})
	}
	return topo
}

// WorkspaceHash returns the canonical workspace state hash of
// PROJECT_BRIEF_P2 §3 (sha256 hex over the canonical state JSON).
func (s *Service) WorkspaceHash(ctx context.Context, workspaceID string) (string, error) {
	if _, err := s.mustWorkspace(ctx, workspaceID); err != nil {
		return "", err
	}
	var hash string
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		state, err := loadState(ctx, storeOfTx(tx), workspaceID)
		if err != nil {
			return err
		}
		hash, err = state.hash()
		return err
	})
	if err != nil {
		return "", err
	}
	return hash, nil
}

// ---------------------------------------------------------------------------
// Layer overlay read path (issue #51, W33)
// ---------------------------------------------------------------------------

// TopologyLayers is the overlay section of the topology payload: the
// read-only canvas projection of the unified graph, scoped to the
// workspace's lineage. base_station nodes arrive AGGREGATED — one cluster
// node per anchor device with the member count and a code sample; the
// per-station drill-down lives on the layer-members endpoint.
type TopologyLayers struct {
	Nodes   []LayerNodePayload `json:"nodes"`
	Edges   []LayerEdgePayload `json:"edges"`
	Summary LayersSummary      `json:"summary"`
}

// LayerNodePayload is one overlay node (or cluster aggregate) of the
// canvas. Aggregated is true for the synthesized cluster nodes (ID
// "agg-<anchor device id>", MemberCount members, Sample codes).
type LayerNodePayload struct {
	ID             string          `json:"id"`
	ResourceID     string          `json:"resourceId,omitempty"`
	Layer          string          `json:"layer"`
	Kind           string          `json:"kind"`
	Code           string          `json:"code"`
	Name           string          `json:"name"`
	Status         string          `json:"status,omitempty"`
	AnchorDeviceID string          `json:"anchorDeviceId,omitempty"`
	GeoLat         *float64        `json:"geoLat,omitempty"`
	GeoLng         *float64        `json:"geoLng,omitempty"`
	MemberCount    int             `json:"memberCount,omitempty"`
	Sample         []string        `json:"sample,omitempty"`
	Aggregated     bool            `json:"aggregated,omitempty"`
	Detail         json.RawMessage `json:"detail,omitempty"`
}

// LayerEdgePayload is one overlay edge. aKind/zKind ∈ device|interface|node
// with aRef/zRef the corresponding id; aCode/zCode carry the resolved
// display codes for labels.
type LayerEdgePayload struct {
	ID         string `json:"id"`
	Layer      string `json:"layer"`
	Sem        string `json:"sem"`
	Label      string `json:"label"`
	ResourceID string `json:"resourceId,omitempty"`
	AKind      string `json:"aKind"`
	ARef       string `json:"aRef"`
	ACode      string `json:"aCode,omitempty"`
	ZKind      string `json:"zKind"`
	ZRef       string `json:"zRef"`
	ZCode      string `json:"zCode,omitempty"`
}

// LayersSummary counts per layer: nodes after aggregation, edges, and the
// L4 clustering shape (clusters + the individual stations behind them).
type LayersSummary struct {
	ByLayer map[string]LayerCount `json:"byLayer"`
}

// LayerCount is the per-layer tally of the overlay payload.
type LayerCount struct {
	Nodes          int `json:"nodes"`
	Edges          int `json:"edges"`
	Clusters       int `json:"clusters"`
	ClusteredNodes int `json:"clusteredNodes"`
}

// clusterSampleCap bounds the sample code list of an aggregate node.
const clusterSampleCap = 5

// layerNodePayload renders one overlay node (non-aggregated path).
func layerNodePayload(n domain.LayerNode) LayerNodePayload {
	var detail json.RawMessage
	if n.DetailJSON != "" && n.DetailJSON != "{}" {
		detail = json.RawMessage(n.DetailJSON)
	}
	return LayerNodePayload{
		ID: n.ID, ResourceID: n.ResourceID, Layer: n.Layer, Kind: n.Kind,
		Code: n.Code, Name: n.Name, Status: n.Status, AnchorDeviceID: n.AnchorDeviceID,
		GeoLat: n.GeoLat, GeoLng: n.GeoLng, Detail: detail,
	}
}

// buildLayersPayload folds the overlay nodes/links into the payload shape:
// base_station nodes with a cluster key collapse into one aggregate per
// anchor device (issue #51 spec D — 1,198 stations never render
// individually by default), every other node passes through as-is. Edge
// endpoints resolve to display codes via the device/node id maps.
func buildLayersPayload(
	nodes []domain.LayerNode,
	links []domain.LayerLink,
	deviceCode func(id string) (string, bool),
) TopologyLayers {
	out := TopologyLayers{
		Nodes:   make([]LayerNodePayload, 0, len(nodes)),
		Edges:   make([]LayerEdgePayload, 0, len(links)),
		Summary: LayersSummary{ByLayer: map[string]LayerCount{}},
	}
	nodeCode := make(map[string]string, len(nodes))
	nodeLayer := make(map[string]string, len(nodes))

	// Pass 1: aggregate base_station clusters (ordered by first-seen
	// cluster key; members were listed in code order).
	type clusterAgg struct {
		anchor string
		count  int
		sample []string
		geoLat *float64
		geoLng *float64
	}
	aggOrder := []string{}
	aggs := map[string]*clusterAgg{}
	passThrough := make([]domain.LayerNode, 0, len(nodes))
	for _, n := range nodes {
		nodeCode[n.ID] = n.Code
		nodeLayer[n.ID] = n.Layer
		if n.Kind == "base_station" && n.ClusterKey != "" {
			agg, seen := aggs[n.ClusterKey]
			if !seen {
				agg = &clusterAgg{anchor: n.ClusterKey}
				aggs[n.ClusterKey] = agg
				aggOrder = append(aggOrder, n.ClusterKey)
			}
			agg.count++
			if len(agg.sample) < clusterSampleCap {
				agg.sample = append(agg.sample, n.Code)
			}
			if agg.geoLat == nil && n.GeoLat != nil {
				agg.geoLat, agg.geoLng = n.GeoLat, n.GeoLng
			}
			continue
		}
		passThrough = append(passThrough, n)
	}
	// Cluster aggregates first (deterministic: anchor order), then every
	// other node in (layer, kind, code) order — the repo list order.
	for _, key := range aggOrder {
		agg := aggs[key]
		devCode, _ := deviceCode(key)
		name := "接入簇"
		if devCode != "" {
			name = devCode + " 接入簇"
		}
		node := LayerNodePayload{
			ID: "agg-" + key, Layer: domain.LayerAccess, Kind: "cluster",
			Code: "CLUSTER-" + devCode, Name: name, AnchorDeviceID: key,
			GeoLat: agg.geoLat, GeoLng: agg.geoLng,
			MemberCount: agg.count, Sample: agg.sample, Aggregated: true,
		}
		if node.Code == "CLUSTER-" { // anchor device vanished (deleted)
			node.Code = "CLUSTER-" + key
		}
		out.Nodes = append(out.Nodes, node)
	}
	for _, n := range passThrough {
		out.Nodes = append(out.Nodes, layerNodePayload(n))
	}

	resolve := func(kind, ref string) (string, bool) { // (code, ok)
		switch kind {
		case domain.LayerEndpointDevice:
			return deviceCode(ref)
		case domain.LayerEndpointNode:
			code := nodeCode[ref]
			return code, code != ""
		}
		return "", false // interfaces resolve at the frontend (node.interfaces)
	}
	for _, l := range links {
		edge := LayerEdgePayload{
			ID: l.ID, Layer: l.Layer, Sem: l.Sem, Label: l.Label, ResourceID: l.ResourceID,
			AKind: l.AKind, ARef: l.ARef, ZKind: l.ZKind, ZRef: l.ZRef,
		}
		if code, ok := resolve(l.AKind, l.ARef); ok {
			edge.ACode = code
		}
		if code, ok := resolve(l.ZKind, l.ZRef); ok {
			edge.ZCode = code
		}
		out.Edges = append(out.Edges, edge)
	}

	// Summary per layer (aggregates count as L4 nodes; clustered stations
	// reported separately so the UI can show "125 簇 / 1,198 站").
	for _, n := range out.Nodes {
		c := out.Summary.ByLayer[n.Layer]
		c.Nodes++
		out.Summary.ByLayer[n.Layer] = c
	}
	for _, l := range out.Edges {
		c := out.Summary.ByLayer[l.Layer]
		c.Edges++
		out.Summary.ByLayer[l.Layer] = c
	}
	l4 := out.Summary.ByLayer[domain.LayerAccess]
	l4.Clusters = len(aggOrder)
	l4.ClusteredNodes = 0
	for _, agg := range aggs {
		l4.ClusteredNodes += agg.count
	}
	out.Summary.ByLayer[domain.LayerAccess] = l4
	return out
}

// Topology now also reads the layer overlay in the same snapshot (issue
// #51): the response gains a `layers` section with the aggregated overlay
// nodes, the semantic edges and the per-layer summary. Workspaces
// materialized before v0.12 simply return an empty overlay section.
func (s *Service) Topology(ctx context.Context, workspaceID string) (Topology, error) {
	var topo Topology
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		if _, err := tx.Workspaces.GetByID(ctx, workspaceID); err != nil {
			return fmt.Errorf("sim: workspace %s: %w", workspaceID, err)
		}
		state, err := loadState(ctx, storeOfTx(tx), workspaceID)
		if err != nil {
			return err
		}
		hash, err := state.hash()
		if err != nil {
			return err
		}
		topo = state.topology(workspaceID, hash)

		layerNodes, err := tx.LayerNodes.ListByWorkspace(ctx, workspaceID)
		if err != nil {
			return err
		}
		layerLinks, err := tx.LayerLinks.ListByWorkspace(ctx, workspaceID)
		if err != nil {
			return err
		}
		deviceCode := func(id string) (string, bool) {
			dev, ok := state.deviceByID[id]
			if !ok {
				return "", false
			}
			return dev.Code, true
		}
		layers := buildLayersPayload(layerNodes, layerLinks, deviceCode)
		topo.Layers = &layers
		return nil
	})
	if err != nil {
		return Topology{}, err
	}
	return topo, nil
}

// LayerMember is one cluster member of the layer-members payload: the
// per-station drill-down behind a canvas cluster node (issue #51 spec D:
// members never render on the canvas by default; the panel pages them).
type LayerMember struct {
	ID         string   `json:"id"`
	ResourceID string   `json:"resourceId,omitempty"`
	Kind       string   `json:"kind"`
	Code       string   `json:"code"`
	Name       string   `json:"name"`
	Status     string   `json:"status,omitempty"`
	GeoLat     *float64 `json:"geoLat,omitempty"`
	GeoLng     *float64 `json:"geoLng,omitempty"`
}

// LayerMembers pages the overlay nodes of one cluster (the clusterKey is
// the anchor virtual device id of a base_station aggregate). page is
// 1-based; pageSize defaults to 20 and caps at 100.
func (s *Service) LayerMembers(ctx context.Context, workspaceID, clusterKey string, page, pageSize int) ([]LayerMember, int, error) {
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	if page <= 0 {
		page = 1
	}
	var members []LayerMember
	var total int
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		if _, err := tx.Workspaces.GetByID(ctx, workspaceID); err != nil {
			return fmt.Errorf("sim: workspace %s: %w", workspaceID, err)
		}
		nodes, n, err := tx.LayerNodes.ListByCluster(ctx, workspaceID, clusterKey, page, pageSize)
		if err != nil {
			return err
		}
		total = n
		members = make([]LayerMember, 0, len(nodes))
		for _, node := range nodes {
			members = append(members, LayerMember{
				ID: node.ID, ResourceID: node.ResourceID, Kind: node.Kind,
				Code: node.Code, Name: node.Name, Status: node.Status,
				GeoLat: node.GeoLat, GeoLng: node.GeoLng,
			})
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return members, total, nil
}
