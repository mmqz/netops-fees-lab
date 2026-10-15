package domain

import "time"

// Layer overlay entities (issue #51, W33): the workbench canvas grew from a
// device-level drill sandbox into the PRESENTATION of the full seven-layer
// platform. VirtualNode / LayerLink are read-only overlay materializations —
// the canvas view of the unified resource graph, scoped to the workspace's
// lineage batches.
//
// BOUNDARY (extends ADR-0011 deliberately — issue #51 supersedes the
// v0.5-era "device + circuit only" reading):
//   - DRILL entities (virtual_devices / sim_interfaces / sim_links) stay
//     the only mutable sandbox: ops, checks, changesets, the state hash.
//   - OVERLAY entities (virtual_nodes / virtual_layer_links) are written
//     ONCE by materialization and never mutated by ops; they are excluded
//     from the state hash and the check catalog on purpose.
//   - The unified resource graph (traverse/impact) remains the analysis
//     authority; the overlay is its canvas projection, so an impact result
//     can be highlighted on the workbench by resource id.
//
// Layer values (the /api/schema layer catalog is the naming authority):
//
//      L0 spatial  — site/room/rack/duct are containers (spec §B: not drawn);
//                    manhole/pole materialize as geo anchors
//      L3 logical  — circuits and OTN channels are EDGES (LayerLink), not nodes
//      L4 access   — base_station/olt/onu/bbu/rru nodes; base stations cluster
//                    by their uplink device (AnchorDeviceID is the cluster key)
//      L5 logical  — ring/igp_domain/vpn/protection_group/ip_pool group nodes
//      L6 business — customer/service/leased_line nodes
//
// L1/L2 need no overlay: they ARE the drill entities (devices, interfaces,
// physical corridor links).

// LayerNode is one read-only overlay node of a workspace: an access,
// business, grouping or spatial entity projected from the unified graph.
// AnchorDeviceID ("" = free-floating, SQL NULL) is the virtual device the
// node visually attaches to — the uplink target of a base station, the
// station device of an OLT, the depended device of a service. For base
// stations it doubles as the CLUSTER KEY: the read path aggregates all
// nodes sharing an anchor into one canvas cluster node. GeoLat/GeoLng are
// presentation metadata (nil = no position) and never enter the state hash.
type LayerNode struct {
	ID             string
	WorkspaceID    string
	ResourceID     string // lineage resource (never "" for materialized nodes)
	Layer          string // "L0" | "L4" | "L5" | "L6"
	Kind           string // node kind (see sim materializer for the closed set)
	Code           string // unique within the workspace
	Name           string
	Status         string // source lifecycle status (在网/停用/…), presentation only
	AnchorDeviceID string // "" = free-floating (SQL NULL)
	ClusterKey     string // aggregation key ("" = never clustered)
	GeoLat         *float64
	GeoLng         *float64
	DetailJSON     string // typed extras: {"ponPorts":[…]}, {"stationCount":N}, …
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// LayerLink is one read-only overlay edge: a logical carrying relation (L3
// circuit / OTN channel between devices), a service relation (serves /
// subscribed_by / depends_on), an access relation (ONU→PON, RRU→BBU) or a
// grouping membership (device→ring/IGP/VPN, pool→device, PG→circuit).
// Endpoints are either a drill device (aKind "device", aRef = virtual
// device id) or an overlay node (aKind "node", aRef = LayerNode id) or a
// drill interface (aKind "interface", aRef = sim interface id).
type LayerLink struct {
	ID          string
	WorkspaceID string
	Layer       string // "L3" | "L4" | "L5" | "L6"
	Sem         string // "logical" | "service" | "access" | "group"
	Label       string
	ResourceID  string // "" = no lineage resource (SQL NULL)
	AKind       string // "device" | "interface" | "node"
	ARef        string // virtual device id / sim interface id / LayerNode id
	ZKind       string
	ZRef        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Closed layer / sem / endpoint-kind vocabularies (storage CHECK twins).
const (
	LayerSpatial  = "L0"
	LayerLogical  = "L3"
	LayerAccess   = "L4"
	LayerGrouping = "L5"
	LayerBusiness = "L6"

	SemLogical = "logical"
	SemService = "service"
	SemAccess  = "access"
	SemGroup   = "group"

	LayerEndpointDevice    = "device"
	LayerEndpointInterface = "interface"
	LayerEndpointNode      = "node"
)

// AllOverlayLayers lists the overlay layer values (L1/L2 are drill entities).
var AllOverlayLayers = []string{LayerSpatial, LayerLogical, LayerAccess, LayerGrouping, LayerBusiness}

// IsValidOverlayLayer reports whether layer is one of the overlay values.
func IsValidOverlayLayer(layer string) bool {
	for _, l := range AllOverlayLayers {
		if l == layer {
			return true
		}
	}
	return false
}

// AllLayerLinkSems lists the edge semantics vocabulary.
var AllLayerLinkSems = []string{SemLogical, SemService, SemAccess, SemGroup}

// IsValidLayerLinkSem reports whether sem is one of the closed vocabulary.
func IsValidLayerLinkSem(sem string) bool {
	for _, s := range AllLayerLinkSems {
		if s == sem {
			return true
		}
	}
	return false
}

// IsValidLayerEndpointKind reports whether kind is a valid link endpoint kind.
func IsValidLayerEndpointKind(kind string) bool {
	switch kind {
	case LayerEndpointDevice, LayerEndpointInterface, LayerEndpointNode:
		return true
	}
	return false
}
