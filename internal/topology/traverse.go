package topology

// traverse.go — the W22 layer-delta traversal engine (design
// data-model-v1.md §10.3/§10.4, issue #19). It is a NEW engine parallel to
// the legacy Build/Neighbors code (undirected, depth ≤ 2) which stays
// untouched for the deprecated GET /resources/{id}/relations endpoint
// (§10.10: legacy endpoints keep their behavior).
//
// Semantics (the §12 "W22 落地处置" resolution of §10.2's own
// inconsistency): REACHABILITY is layer-delta driven, not
// stored-orientation driven. From any visited node, an edge is expandable
// when
//
//   - direction=down and the candidate's layer ≤ the current layer
//     (descend or lateral),
//   - direction=up and the candidate's layer ≥ the current layer
//     (ascend or lateral),
//   - direction=both: always.
//
// Lateral edges (same layer) therefore expand in EVERY mode. The STORED
// from→to orientation (member→container, upper→lower — the dependency
// semantics of §10.2) stays in the response edges for AI reasoning, but it
// no longer decides reachability: e.g. `serves` (device→customer) walks
// from the device UP to the customer under direction=up even though the
// stored from-side is the device.
//
// Convergence guarantees (§10.4):
//   - Rule 1: nodes are visited once (first visit fixes depth + parent).
//   - Rule 2: edges are deduped by the (from, to, relation) STORED triple.
//   - Rule 3: layer-order BFS with a FIFO queue; maxDepth is a PRUNE
//     (nodes beyond it are never enqueued), never a post-filter.
//   - Rule 4 (ring guard): the visited-set itself IS the ring guard — a
//     ring node re-encountered is an edge endpoint only, never re-expanded;
//     rules 1+2 subsume the separate "expand member_of_ring only on first
//     ring entry" rule the design sketched.
//   - Rule 5: direction=both is explicit; the default is down (§10.8 #4).
//
// Performance (§10.4's critique of the legacy code): the engine is O(V+E) —
// it loads the incident edges of every EXPANDED node incrementally through
// the TraverseStore (one MULTI-INDEX OR query per node: the
// UNIQUE(from,to,type) prefix serves the from side, idx_relations_to the to
// side — the query plan was verified with EXPLAIN QUERY PLAN), never the
// whole graph; node payloads are fetched at most once per resource (info
// cache).
//
// Determinism: the per-node expansion order is the canonical
// (neighbor type, natural key, relation, endpoints) sort, the queue is
// FIFO, and output lists are re-sorted — the same database state always
// yields byte-equal JSON (modulo stats.elapsedMs, the one wall-clock field,
// the same exemption discipline as the governance summary's generatedAt).

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
)

// Direction values of the traverse/impact endpoints.
const (
	DirectionDown = "down"
	DirectionUp   = "up"
	DirectionBoth = "both"
)

// Budget defaults and caps of the traverse engine (§10.3). The HTTP layer
// and the engine share these values — the engine treats a zero budget as
// the default (zero-value hardening for direct callers).
const (
	DefaultTraverseMaxDepth = 6
	DefaultTraverseMaxNodes = 200
	MaxTraverseMaxDepth     = 8
	MaxTraverseMaxNodes     = 500
)

// NodeInfo is the payload the engine needs per node. RunState is the W22
// snapshot value ("" = no observation — NULL, distinct from the explicit
// "unknown"); Status is the free-text ledger status.
type NodeInfo struct {
	ID         string
	Type       domain.ResourceType
	NaturalKey string
	Name       string
	Status     string
	RunState   string
}

// NodeInfoOf converts a domain.Resource into a NodeInfo (the shared
// projection the HTTP layer and the engine tests bind over repo reads).
func NodeInfoOf(r domain.Resource) NodeInfo {
	return NodeInfo{
		ID: r.ID, Type: r.Type, NaturalKey: r.NaturalKey, Name: r.Name,
		Status: r.Status, RunState: r.RunState,
	}
}

// TraverseStore is the data-access contract of the traverse/impact engines:
// NodeInfo resolves one node payload (an unknown id answers an error
// wrapping domain.ErrNotFound); IncidentRelations returns every stored
// relation touching id at either endpoint. StoreFuncs binds the two as
// closures so the algorithm package stays storage-agnostic (the HTTP layer
// and the engine tests bind them to *repo.DB).
type TraverseStore interface {
	NodeInfo(ctx context.Context, id string) (NodeInfo, error)
	IncidentRelations(ctx context.Context, id string) ([]domain.Relation, error)
}

// StoreFuncs adapts two loader functions to TraverseStore.
type StoreFuncs struct {
	NodeFn func(ctx context.Context, id string) (NodeInfo, error)
	EdgeFn func(ctx context.Context, id string) ([]domain.Relation, error)
}

// NodeInfo implements TraverseStore.
func (s StoreFuncs) NodeInfo(ctx context.Context, id string) (NodeInfo, error) {
	return s.NodeFn(ctx, id)
}

// IncidentRelations implements TraverseStore.
func (s StoreFuncs) IncidentRelations(ctx context.Context, id string) ([]domain.Relation, error) {
	return s.EdgeFn(ctx, id)
}

// TraverseParams is the validated parameter set of the traverse engine.
// Defaults (direction=down, maxDepth=6, maxNodes=200) and caps
// (maxDepth ≤ 8, maxNodes ≤ 500) are enforced by the HTTP layer (§10.3);
// the engine trusts what it is given. Layers/Relations nil = no filter.
type TraverseParams struct {
	Direction string
	Layers    map[int]bool
	Relations map[domain.RelationType]bool
	MaxDepth  int
	MaxNodes  int
	PathOnly  bool
}

// TraverseNode is one node of the response: {id, type, name, layer, depth,
// status, runState}. Root carries depth 0; nodes ALWAYS include the root
// (the maxNodes count contract).
type TraverseNode struct {
	ID       string              `json:"id"`
	Type     domain.ResourceType `json:"type"`
	Name     string              `json:"name"`
	Layer    int                 `json:"layer"`
	Depth    int                 `json:"depth"`
	Status   string              `json:"status"`
	RunState string              `json:"runState"`
}

// TraverseRoot is the root projection: {id, type, name, layer}.
type TraverseRoot struct {
	ID    string              `json:"id"`
	Type  domain.ResourceType `json:"type"`
	Name  string              `json:"name"`
	Layer int                 `json:"layer"`
}

// TraverseEdge is one deduped edge in STORED orientation with the
// INSTANCE-level cross-layer flag (the two endpoints' layers differ).
type TraverseEdge struct {
	From       string              `json:"from"`
	To         string              `json:"to"`
	Relation   domain.RelationType `json:"relation"`
	CrossLayer bool                `json:"crossLayer"`
}

// TraverseStats is the bookkeeping block: visitedEdges = the number of
// deduped edges emitted; elapsedMs is the wall clock of the query (the ONE
// non-deterministic field — the determinism guarantee exempts it).
type TraverseStats struct {
	VisitedEdges int   `json:"visitedEdges"`
	ElapsedMs    int64 `json:"elapsedMs"`
}

// TraverseResult is the traverse endpoint payload: nodes (root included,
// ordered by (depth, layer, type, id)), edges (deduped, ordered by
// (from, to, relation)), paths (ONLY when PathOnly — the one shortest
// root→deepest-node path; see deepestNode), truncated (the BFS stopped
// because visited hit MaxNodes) and stats.
type TraverseResult struct {
	Root      TraverseRoot   `json:"root"`
	Nodes     []TraverseNode `json:"nodes"`
	Edges     []TraverseEdge `json:"edges"`
	Paths     [][]string     `json:"paths,omitempty"`
	Truncated bool           `json:"truncated"`
	Stats     TraverseStats  `json:"stats"`
}

// visitState is the engine's per-node bookkeeping.
type visitState struct {
	info  NodeInfo
	layer int
	depth int
	// parentID/parentEdge record how the node was FIRST reached (nil for
	// the root) — the BFS shortest-path chain of pathOnly and impact's
	// viaPath. parentEdge is the STORED relation (orientation preserved).
	parentID   string
	parentEdge *domain.Relation
}

// traversal bundles the JSON result with the internal per-node states (the
// impact engine consumes both).
type traversal struct {
	result  *TraverseResult
	visited map[string]*visitState
}

// Traverse runs the layer-order BFS from rootID. An unknown root answers
// an error wrapping domain.ErrNotFound; unknown node types are impossible
// in a migrated database (the registry covers all types) and are skipped
// defensively on edges (the edge is dropped, never a panic).
func Traverse(ctx context.Context, store TraverseStore, rootID string, p TraverseParams) (*TraverseResult, error) {
	tr, err := runTraverse(ctx, store, rootID, p)
	if err != nil {
		return nil, err
	}
	return tr.result, nil
}

// runTraverse is Traverse plus the visited states (impact's input).
func runTraverse(ctx context.Context, store TraverseStore, rootID string, p TraverseParams) (*traversal, error) {
	start := time.Now()

	// Zero-value hardening: direct engine callers (tests, the impact
	// engine) may leave the budget fields unset — 0 means "default", NOT
	// "budget of zero" (the HTTP layer applies the same defaults; the
	// engine must not depend on that).
	if p.MaxDepth <= 0 {
		p.MaxDepth = DefaultTraverseMaxDepth
	}
	if p.MaxNodes <= 0 {
		p.MaxNodes = DefaultTraverseMaxNodes
	}

	rootInfo, err := store.NodeInfo(ctx, rootID)
	if err != nil {
		return nil, fmt.Errorf("topology: traverse root %s: %w", rootID, err)
	}
	rootLayer, err := mapping.LayerOf(rootInfo.Type)
	if err != nil {
		return nil, fmt.Errorf("topology: traverse root %s: %w", rootID, err)
	}

	visited := map[string]*visitState{
		rootID: {info: rootInfo, layer: rootLayer, depth: 0},
	}
	// infoCache dedupes node-payload loads (at most one per resource).
	infoCache := map[string]NodeInfo{rootID: rootInfo}
	loadInfo := func(id string) (NodeInfo, bool) {
		if info, ok := infoCache[id]; ok {
			return info, true
		}
		info, err := store.NodeInfo(ctx, id)
		if err != nil {
			// Unknown endpoint resource (should be impossible with FKs on):
			// skip defensively, do not fail the whole traversal.
			return NodeInfo{}, false
		}
		infoCache[id] = info
		return info, true
	}

	emitted := map[string]bool{}
	// make(…, 0): a single-node traversal (or an edges-filtered one) must
	// serialize edges as [] — never null (audit §2.6, same contract the
	// impact arrays got).
	edges := make([]TraverseEdge, 0)
	queue := []string{rootID}
	truncated := false

	for len(queue) > 0 && !truncated {
		cur := queue[0]
		queue = queue[1:]
		curState := visited[cur]
		if curState.depth >= p.MaxDepth {
			continue // PRUNE: nothing beyond maxDepth is enqueued (rule 3)
		}
		rels, err := store.IncidentRelations(ctx, cur)
		if err != nil {
			return nil, fmt.Errorf("topology: traverse edges of %s: %w", cur, err)
		}

		// Candidate steps: (neighbor info, layer, stored edge) pairs passing
		// the relation filter, the layers filter and the direction delta.
		type candidate struct {
			info  NodeInfo
			layer int
			rel   domain.Relation
		}
		cands := make([]candidate, 0, len(rels))
		for _, rel := range rels {
			if p.Relations != nil && !p.Relations[rel.Type] {
				continue
			}
			other := rel.ToResourceID
			if other == cur {
				other = rel.FromResourceID
			}
			info, ok := loadInfo(other)
			if !ok {
				continue
			}
			layer, err := mapping.LayerOf(info.Type)
			if err != nil {
				continue // unknown type: skip the edge defensively
			}
			if p.Layers != nil && !p.Layers[layer] {
				continue
			}
			if !expandable(p.Direction, curState.layer, layer) {
				continue
			}
			cands = append(cands, candidate{info: info, layer: layer, rel: rel})
		}
		// Deterministic expansion order: (neighbor type, natural key,
		// relation, stored endpoints) — first-visit parent/depth assignment
		// and the truncation point are stable across identical data.
		sort.Slice(cands, func(i, j int) bool {
			a, b := cands[i], cands[j]
			if a.info.Type != b.info.Type {
				return a.info.Type < b.info.Type
			}
			if a.info.NaturalKey != b.info.NaturalKey {
				return a.info.NaturalKey < b.info.NaturalKey
			}
			if a.rel.Type != b.rel.Type {
				return a.rel.Type < b.rel.Type
			}
			if a.rel.FromResourceID != b.rel.FromResourceID {
				return a.rel.FromResourceID < b.rel.FromResourceID
			}
			return a.rel.ToResourceID < b.rel.ToResourceID
		})

		for _, c := range cands {
			key := edgeKey(c.rel)
			if emitted[key] {
				continue // rule 2: (from, to, relation) dedup
			}
			nid := c.info.ID
			if _, seen := visited[nid]; !seen {
				if len(visited) >= p.MaxNodes {
					// The BFS STOPS here: the frontier exists but the node
					// budget is exhausted — report honestly.
					truncated = true
					break
				}
				rel := c.rel
				visited[nid] = &visitState{
					info: c.info, layer: c.layer, depth: curState.depth + 1,
					parentID: cur, parentEdge: &rel,
				}
				queue = append(queue, nid)
			}
			emitted[key] = true
			edges = append(edges, TraverseEdge{
				From: c.rel.FromResourceID, To: c.rel.ToResourceID,
				Relation:   c.rel.Type,
				CrossLayer: curState.layer != c.layer,
			})
		}
	}

	result := &TraverseResult{
		Root:      TraverseRoot{ID: rootInfo.ID, Type: rootInfo.Type, Name: rootInfo.Name, Layer: rootLayer},
		Truncated: truncated,
		Stats:     TraverseStats{VisitedEdges: len(edges)},
	}

	// Nodes: root included, ordered by (depth, layer, type, id).
	nodes := make([]TraverseNode, 0, len(visited))
	for _, st := range visited {
		nodes = append(nodes, TraverseNode{
			ID: st.info.ID, Type: st.info.Type, Name: st.info.Name,
			Layer: st.layer, Depth: st.depth, Status: st.info.Status, RunState: st.info.RunState,
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		return nodeLess(a, b)
	})
	result.Nodes = nodes

	// Edges: deduped already; deterministic (from, to, relation) order.
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Relation < edges[j].Relation
	})
	result.Edges = edges

	// pathOnly: ONE shortest path root → the deepest node, walked along the
	// BFS parent chain (first-visit edges = fewest hops by BFS layer order).
	if p.PathOnly {
		deepest := deepestNode(visited)
		result.Paths = [][]string{visitPath(visited, deepest)}
	}

	result.Stats.ElapsedMs = time.Since(start).Milliseconds()
	return &traversal{result: result, visited: visited}, nil
}

// expandable reports whether an edge from a node at layer `from` into a
// candidate at layer `to` may be walked in the given direction: down =
// descend-or-lateral (to ≤ from), up = ascend-or-lateral (to ≥ from),
// both = always. Lateral edges expand in every mode.
func expandable(direction string, from, to int) bool {
	switch direction {
	case DirectionDown:
		return to <= from
	case DirectionUp:
		return to >= from
	default: // both — the HTTP layer validated the value
		return true
	}
}

// edgeKey is the rule-2 dedup key of one stored relation.
func edgeKey(rel domain.Relation) string {
	return rel.FromResourceID + "|" + rel.ToResourceID + "|" + rel.Type.String()
}

// nodeLess orders two same-depth nodes by (layer, type, id).
func nodeLess(a, b TraverseNode) bool {
	if a.Layer != b.Layer {
		return a.Layer < b.Layer
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.ID < b.ID
}

// deepestNode picks THE deepest visited node: max depth; ties resolved by
// lowest layer, then (type, natural key) order. This is the documented
// resolution of §10.3's "最短路径" (one canonical root→deepest path).
func deepestNode(visited map[string]*visitState) *visitState {
	var best *visitState
	for _, st := range visited {
		if best == nil || st.depth > best.depth {
			best = st
			continue
		}
		if st.depth == best.depth {
			if st.layer != best.layer {
				if st.layer < best.layer {
					best = st
				}
				continue
			}
			if st.info.Type != best.info.Type {
				if st.info.Type < best.info.Type {
					best = st
				}
				continue
			}
			if st.info.NaturalKey < best.info.NaturalKey {
				best = st
			}
		}
	}
	return best
}

// visitPath walks the parent chain from st back to the root and returns
// the node ids root → st (inclusive). The root yields [root].
func visitPath(visited map[string]*visitState, st *visitState) []string {
	if st == nil {
		return nil
	}
	path := make([]string, 0, st.depth+1)
	for cur := st; cur != nil; cur = visited[cur.parentID] {
		path = append(path, cur.info.ID)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}
