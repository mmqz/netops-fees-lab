// Package topology builds the resource graph of NetOps Lab from the
// normalized resources and their confirmed relations, and answers the two
// Stage 0 topology questions (PROJECT_BRIEF §0 item 5):
//
//   - the undirected neighborhood of a resource (BFS, depth 1 or 2) over the
//     FULL resource graph (devices, pools and circuits are all nodes), used
//     by the resource relations endpoint;
//   - deterministic reachability between two DEVICES over the device-level
//     topology (a circuit is a link between its a_end and z_end devices —
//     the network view), bounded by a hop limit, used by the topology check
//     endpoint (§9 hops are {deviceId, deviceCode}).
//
// Traversal is UNDIRECTED everywhere: a relation may be followed in both
// directions (ip_pool --assigned_to--> device can be walked from the device
// to the pool and back; a circuit links its two devices in either order).
// Determinism is guaranteed by sorting every node by (resource type, natural
// key): adjacency lists are built in that order, so BFS expansion order,
// parent assignment and therefore the returned paths and node/edge
// sequences are stable across identical database states.
package topology

import (
	"context"
	"sort"

	"github.com/mmqz/netops-lab/internal/domain"
)

// Node is one vertex of the graph: a normalized resource.
type Node struct {
	// ID is the resource id.
	ID string `json:"id"`
	// Type is the resource type (device, ip_pool, circuit).
	Type domain.ResourceType `json:"type"`
	// NaturalKey is the resource's natural key.
	NaturalKey string `json:"naturalKey"`
	// Name is the resource display name.
	Name string `json:"name"`
}

// Edge is one directed record of a stored relation. Traversal treats edges
// as undirected; the JSON shape keeps the stored direction for the API
// consumer (§9: {id, relationType, fromId, toId}).
type Edge struct {
	// ID is the relation id.
	ID string `json:"id"`
	// Type is the relation type (assigned_to, a_end, z_end, belongs_to).
	Type domain.RelationType `json:"relationType"`
	// FromID is the from endpoint resource id.
	FromID string `json:"fromId"`
	// ToID is the to endpoint resource id.
	ToID string `json:"toId"`
}

// less orders two nodes by (type, natural key) — the canonical deterministic
// order of every node list this package produces.
func less(a, b Node) bool {
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.NaturalKey < b.NaturalKey
}

// edgeLess orders two edges by (relation type, from natural key, to natural
// key, relation id) — the canonical deterministic order of every edge list
// this package produces.
func (g *Graph) edgeLess(a, b Edge) bool {
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	ka, kb := g.keyOf[a.FromID], g.keyOf[b.FromID]
	if ka != kb {
		return ka < kb
	}
	ka, kb = g.keyOf[a.ToID], g.keyOf[b.ToID]
	if ka != kb {
		return ka < kb
	}
	return a.ID < b.ID
}

// Graph is the immutable undirected resource graph. Build it from the
// current database state (all resources + all relations); it holds no locks
// and can be shared by concurrent readers.
type Graph struct {
	// nodes maps resource id → node.
	nodes map[string]Node
	// adj maps resource id → neighbor ids, each list sorted by
	// (neighbor type, natural key).
	adj map[string][]string
	// devAdj maps device id → device ids linked by a circuit (a_end ↔ z_end
	// of every circuit), each list sorted by natural key. This is
	// the device-level topology Reachable operates on.
	devAdj map[string][]string
	// circuitEnds collects, per circuit id, the device endpoints of its
	// a_end and z_end relations; Build pairs them into devAdj.
	circuitEnds map[string][]string
	// edgeIndex maps resource id → indices (into edges) of the edges
	// touching it at either endpoint.
	edgeIndex map[string][]int
	// edges holds every relation as an Edge, in deterministic
	// (relation type, from key, to key, id) order.
	edges []Edge
	// keyOf maps resource id → natural key.
	keyOf map[string]string
}

// Build constructs the graph from resources and relations. Relations whose
// endpoints are missing from resources are skipped (dangling edges cannot
// exist with foreign keys ON, but Build stays defensive). The context is
// accepted for signature stability; no I/O is performed.
func Build(_ context.Context, resources []domain.Resource, relations []domain.Relation) *Graph {
	g := &Graph{
		nodes:       make(map[string]Node, len(resources)),
		adj:         make(map[string][]string, len(resources)),
		devAdj:      make(map[string][]string, len(resources)),
		circuitEnds: make(map[string][]string, len(resources)),
		edgeIndex:   make(map[string][]int, len(resources)),
		keyOf:       make(map[string]string, len(resources)),
	}
	for _, r := range resources {
		g.nodes[r.ID] = Node{ID: r.ID, Type: r.Type, NaturalKey: r.NaturalKey, Name: r.Name}
		g.keyOf[r.ID] = r.NaturalKey
	}

	for _, rel := range relations {
		if _, ok := g.nodes[rel.FromResourceID]; !ok {
			continue
		}
		if _, ok := g.nodes[rel.ToResourceID]; !ok {
			continue
		}
		edge := Edge{ID: rel.ID, Type: rel.Type, FromID: rel.FromResourceID, ToID: rel.ToResourceID}
		idx := len(g.edges)
		g.edges = append(g.edges, edge)
		g.adj[edge.FromID] = append(g.adj[edge.FromID], edge.ToID)
		g.adj[edge.ToID] = append(g.adj[edge.ToID], edge.FromID)
		g.edgeIndex[edge.FromID] = append(g.edgeIndex[edge.FromID], idx)
		g.edgeIndex[edge.ToID] = append(g.edgeIndex[edge.ToID], idx)
		// Device-level topology bookkeeping: a circuit resource links
		// its a_end and z_end DEVICES, but each of those is a separate
		// circuit→device relation — remember both endpoints per circuit
		// and pair them below.
		if rel.Type == domain.RelationTypeAEnd || rel.Type == domain.RelationTypeZEnd {
			if circuit, ok := g.nodes[edge.FromID]; ok && circuit.Type == domain.ResourceTypeCircuit {
				if dev, ok := g.nodes[edge.ToID]; ok && dev.Type == domain.ResourceTypeDevice {
					g.circuitEnds[edge.FromID] = append(g.circuitEnds[edge.FromID], edge.ToID)
				}
			}
		}
	}
	// Pair every circuit's a_end and z_end devices into device-level
	// links (a_end ↔ z_end). Self-loops (W003, both ends identical) add
	// no link; a circuit with only one stored endpoint adds none either.
	for _, ends := range g.circuitEnds {
		uniq := make([]string, 0, len(ends))
		for _, e := range ends {
			dup := false
			for _, u := range uniq {
				if u == e {
					dup = true
					break
				}
			}
			if !dup {
				uniq = append(uniq, e)
			}
		}
		if len(uniq) != 2 {
			continue
		}
		a, b := uniq[0], uniq[1]
		g.devAdj[a] = append(g.devAdj[a], b)
		g.devAdj[b] = append(g.devAdj[b], a)
	}

	// Sort every edge list once — edges, adjacency expansion order and edge
	// index lists all become deterministic.
	sort.SliceStable(g.edges, func(i, j int) bool { return g.edgeLess(g.edges[i], g.edges[j]) })
	for id, nbs := range g.adj {
		sorted := make([]string, len(nbs))
		copy(sorted, nbs)
		sort.Slice(sorted, func(i, j int) bool { return less(g.nodes[sorted[i]], g.nodes[sorted[j]]) })
		g.adj[id] = sorted
	}
	// Rebuild edge indices against the sorted edges order.
	g.edgeIndex = make(map[string][]int, len(g.nodes))
	for i, edge := range g.edges {
		g.edgeIndex[edge.FromID] = append(g.edgeIndex[edge.FromID], i)
		g.edgeIndex[edge.ToID] = append(g.edgeIndex[edge.ToID], i)
	}
	for id, idxs := range g.edgeIndex {
		sorted := make([]int, len(idxs))
		copy(sorted, idxs)
		sort.Slice(sorted, func(i, j int) bool { return g.edgeLess(g.edges[sorted[i]], g.edges[sorted[j]]) })
		g.edgeIndex[id] = sorted
	}
	// Sort the device-level adjacency by natural key (deterministic BFS).
	for id, nbs := range g.devAdj {
		sorted := make([]string, len(nbs))
		copy(sorted, nbs)
		sort.Slice(sorted, func(i, j int) bool {
			return g.keyOf[sorted[i]] < g.keyOf[sorted[j]]
		})
		g.devAdj[id] = sorted
	}
	return g
}

// Neighbors returns the BFS neighborhood of the resource id within depth
// device-hops (depth < 1 is clamped to 1) and the subgraph edges among the
// visited set (center included). found is false for an unknown id.
//
// Hop counting follows the network view: stepping into a DEVICE node
// consumes one hop; stepping into a circuit or ip_pool node is free (those
// resources "hang off" their anchor device). The center's own attached
// circuits and pools are therefore part of every neighborhood, a device one
// link away appears at depth 1, a device two links away at depth 2 — for
// the seeded fixtures, DEV-001 sees DEV-002 (1 hop) and DEV-003 (2 hops)
// plus every circuit and pool attached along the way.
//
// Nodes are sorted by (type, natural key); edges by
// (relation type, from key, to key, id).
func (g *Graph) Neighbors(id string, depth int) (nodes []Node, edges []Edge, found bool) {
	if _, ok := g.nodes[id]; !ok {
		return nil, nil, false
	}
	if depth < 1 {
		depth = 1
	}

	// 0/1-weighted BFS (fixpoint relaxation): every edge is traversed in
	// both directions; entering a device costs 1, entering a circuit or
	// pool costs 0. Shortest distances in an undirected graph are unique,
	// so the visited set (and therefore the result) is deterministic.
	cost := map[string]int{id: 0}
	for changed := true; changed; {
		changed = false
		for cur, curCost := range cost {
			for _, nb := range g.adj[cur] {
				step := 1
				if g.nodes[nb].Type != domain.ResourceTypeDevice {
					step = 0
				}
				nc := curCost + step
				if nc > depth {
					continue
				}
				if old, seen := cost[nb]; !seen || nc < old {
					cost[nb] = nc
					changed = true
				}
			}
		}
	}

	nodes = make([]Node, 0, len(cost)-1)
	for nid := range cost {
		if nid != id {
			nodes = append(nodes, g.nodes[nid])
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return less(nodes[i], nodes[j]) })

	// Subgraph edges: every stored edge whose BOTH endpoints are in the
	// visited set (center included).
	edges = make([]Edge, 0)
	for _, edge := range g.edges {
		if _, ok := cost[edge.FromID]; ok {
			if _, ok := cost[edge.ToID]; ok {
				edges = append(edges, edge)
			}
		}
	}
	return nodes, edges, true
}

// Reachable reports whether the DEVICE to is reachable from the DEVICE from
// within maxHops over the device-level topology (devices linked by
// circuits), and returns the BFS shortest path: hops is the ordered device
// node sequence from → to (inclusive) and pathLength the number of device
// links (= len(hops) − 1). For the seeded fixtures, DEV-001 → DEV-003 is 2
// hops with path [DEV-001, DEV-002, DEV-003].
//
// When not reachable, hops is nil and pathLength 0. from == to (the same
// device) is reachable with a single-node path (pathLength 0). maxHops < 0
// is treated as 0. Unknown or non-device endpoints yield reachable = false
// (the API layer maps unknown device codes to 404 before calling).
func (g *Graph) Reachable(from, to string, maxHops int) (reachable bool, hops []Node, pathLength int) {
	fromNode, ok := g.nodes[from]
	if !ok || fromNode.Type != domain.ResourceTypeDevice {
		return false, nil, 0
	}
	toNode, ok := g.nodes[to]
	if !ok || toNode.Type != domain.ResourceTypeDevice {
		return false, nil, 0
	}
	if from == to {
		return true, []Node{fromNode}, 0
	}
	if maxHops < 0 {
		maxHops = 0
	}

	parent := map[string]string{from: ""}
	depthOf := map[string]int{from: 0}
	queue := []string{from}
	found := false
	for len(queue) > 0 && !found {
		cur := queue[0]
		queue = queue[1:]
		curDepth := depthOf[cur]
		if curDepth >= maxHops {
			continue
		}
		for _, nb := range g.devAdj[cur] {
			if _, seen := parent[nb]; seen {
				continue
			}
			parent[nb] = cur
			depthOf[nb] = curDepth + 1
			if nb == to {
				found = true
				break
			}
			queue = append(queue, nb)
		}
	}
	if !found {
		return false, nil, 0
	}

	// Walk the parent chain back from to, then reverse.
	path := make([]Node, 0, depthOf[to]+1)
	for cur := to; cur != ""; cur = parent[cur] {
		path = append(path, g.nodes[cur])
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return true, path, len(path) - 1
}

// NodeByID returns the node for a resource id (found reports existence).
func (g *Graph) NodeByID(id string) (Node, bool) {
	n, ok := g.nodes[id]
	return n, ok
}

// NodeByKey returns the node of the given type with the given natural key.
func (g *Graph) NodeByKey(resourceType domain.ResourceType, naturalKey string) (Node, bool) {
	for _, n := range g.nodes {
		if n.Type == resourceType && n.NaturalKey == naturalKey {
			return n, true
		}
	}
	return Node{}, false
}
