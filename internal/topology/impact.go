package topology

// impact.go — the W22 root-cause / impact-surface engine (design
// data-model-v1.md §10.5/§10.6, issue #22's run-state model as the verdict
// input). Impact reuses the traverse engine to collect the subgraph, then
// classifies every visited node by its run-state snapshot:
//
//   - abnormal := run_state ∈ {down, degraded}. A MISSING run_state (NULL)
//     is NOT abnormal — no data, no verdict (§12 "W22 落地处置").
//
//   - candidates (§10.5 根因候选): abnormal nodes whose reachable
//     downstream along the TRAVERSAL direction (within this subgraph)
//     contains NO other abnormal node — the deepest abnormality, the point
//     everything else hangs from. An abnormal node with an abnormal
//     downstream is an intermediate conductor, not a candidate. The ROOT is
//     included when it is itself abnormal with an abnormal-free downstream
//     (§10.6 ③: 根因候选 = {DEV-015 自身}).
//
//   - impacted (§10.5 受影响): NORMAL nodes reachable from an abnormal
//     node along the REVERSE of the canonical downward/dependency
//     orientation — i.e. reachable by ASCENDING (candidate layer ≥ current
//     layer; lateral included). For direction=up from an abnormal device
//     that is the reached customers (the device's dependents); for
//     direction=down from a customer complaint that is the normal chain
//     between the root and the root-cause candidate (the dependents of the
//     broken cable). A normal root reachable from the abnormality is listed
//     too — the complaining customer IS impacted.
//
// Both lists are ordered by (layer, type, natural key) — deterministic.

import (
	"context"
	"fmt"
	"sort"

	"github.com/mmqz/netops-lab/internal/domain"
)

// ImpactMaxNodes is the internal node budget of the impact engine (§10.8
// #7): the impact contract exposes only direction and maxDepth, the node
// cap stays engine-side.
const ImpactMaxNodes = 500

// ImpactNode is the {id, type, name, layer, runState} projection of one
// classified node.
type ImpactNode struct {
	ID       string              `json:"id"`
	Type     domain.ResourceType `json:"type"`
	Name     string              `json:"name"`
	Layer    int                 `json:"layer"`
	RunState string              `json:"runState"`
}

// ImpactedItem is one normal node reachable from an abnormality: distance
// is the BFS depth from the root; viaPath is the root→node id chain (the
// BFS parent chain).
type ImpactedItem struct {
	Node     ImpactNode `json:"node"`
	Distance int        `json:"distance"`
	ViaPath  []string   `json:"viaPath"`
}

// CandidateItem is one root-cause candidate: an abnormal node with an
// abnormal-free traversal-direction downstream. Reason is the human
// verdict summary (Chinese, §10.5 style).
type CandidateItem struct {
	Node     ImpactNode `json:"node"`
	Distance int        `json:"distance"`
	Reason   string     `json:"reason"`
}

// ImpactResult is the impact endpoint payload. Truncated propagates the
// traverse budget verdict — an honest "the classification ran on an
// incomplete subgraph".
type ImpactResult struct {
	Root       ImpactNode      `json:"root"`
	Impacted   []ImpactedItem  `json:"impacted"`
	Candidates []CandidateItem `json:"candidates"`
	Truncated  bool            `json:"truncated"`
}

// Impact classifies the subgraph of rootID collected with the given
// direction (up = impact collection, §10.6 ② default; down = root-cause
// tracing, §10.5 step 3) within maxDepth. direction must be "up" or "down"
// (the HTTP layer validates and defaults; "both" is a traverse-only mode).
// An unknown root answers an error wrapping domain.ErrNotFound.
func Impact(ctx context.Context, store TraverseStore, rootID, direction string, maxDepth int) (*ImpactResult, error) {
	tr, err := runTraverse(ctx, store, rootID, TraverseParams{
		Direction: direction,
		MaxDepth:  maxDepth,
		MaxNodes:  ImpactMaxNodes,
	})
	if err != nil {
		return nil, err
	}
	visited := tr.visited
	edges := tr.result.Edges

	rootState := visited[rootID]
	result := &ImpactResult{
		Root:      impactNodeOf(rootState),
		Truncated: tr.result.Truncated,
	}

	// Directed step adjacencies over the emitted (deduped) edge set:
	//   traversalSteps follow the endpoint's direction parameter;
	//   ascendingSteps follow the reverse of the canonical downward
	//   orientation (layer(v) ≥ layer(u), lateral included).
	traversalSteps := buildSteps(visited, edges, func(fromLayer, toLayer int) bool {
		return expandable(direction, fromLayer, toLayer)
	})
	ascendingSteps := buildSteps(visited, edges, func(fromLayer, toLayer int) bool {
		return toLayer >= fromLayer
	})

	abnormal := func(st *visitState) bool {
		return st.info.RunState == string(domain.RunStateDown) ||
			st.info.RunState == string(domain.RunStateDegraded)
	}

	// Candidates: abnormal nodes (root included, §10.6 ③) with an
	// abnormal-free traversal-direction downstream. make(…, 0): the empty
	// case must serialize as [] not null (audit §2.6 — a nil slice would
	// project to JSON null and crash len()/iteration on strict clients).
	candidates := make([]CandidateItem, 0)
	for id, st := range visited {
		if !abnormal(st) {
			continue
		}
		downstream := closure(traversalSteps, id)
		abnormalDownstream := false
		for did := range downstream {
			if abnormal(visited[did]) {
				abnormalDownstream = true
				break
			}
		}
		if abnormalDownstream {
			continue
		}
		candidates = append(candidates, CandidateItem{
			Node:     impactNodeOf(st),
			Distance: st.depth,
			Reason:   fmt.Sprintf("run_state=%s, 下游无异常", st.info.RunState),
		})
	}

	// Impacted: normal nodes reachable from ANY abnormal node by ascending.
	impactedIDs := map[string]bool{}
	for id, st := range visited {
		if !abnormal(st) {
			continue
		}
		for reached := range closure(ascendingSteps, id) {
			if !abnormal(visited[reached]) {
				impactedIDs[reached] = true
			}
		}
	}
	// Same null-vs-[] contract (audit §2.6): initialized, not nil.
	impacted := make([]ImpactedItem, 0)
	for id := range impactedIDs {
		st := visited[id]
		impacted = append(impacted, ImpactedItem{
			Node:     impactNodeOf(st),
			Distance: st.depth,
			ViaPath:  visitPath(visited, st),
		})
	}

	// Deterministic ordering: (layer, type, natural key).
	sort.Slice(candidates, func(i, j int) bool { return impactLess(candidates[i].Node, candidates[j].Node) })
	sort.Slice(impacted, func(i, j int) bool { return impactLess(impacted[i].Node, impacted[j].Node) })
	result.Candidates = candidates
	result.Impacted = impacted
	return result, nil
}

// impactNodeOf projects one visit state into the impact node shape.
func impactNodeOf(st *visitState) ImpactNode {
	return ImpactNode{ID: st.info.ID, Type: st.info.Type, Name: st.info.Name, Layer: st.layer, RunState: st.info.RunState}
}

// impactLess orders impact nodes by (layer, type, natural key) — the
// documented deterministic order of both result lists.
func impactLess(a, b ImpactNode) bool {
	if a.Layer != b.Layer {
		return a.Layer < b.Layer
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.ID < b.ID
}

// buildSteps derives the directed u→v adjacency over the subgraph edges for
// which the layer predicate holds on the (u, v) side. Lateral edges yield
// both directions when the predicate is symmetric in equality.
func buildSteps(visited map[string]*visitState, edges []TraverseEdge, pred func(fromLayer, toLayer int) bool) map[string][]string {
	adj := map[string][]string{}
	for _, e := range edges {
		from, okF := visited[e.From]
		to, okT := visited[e.To]
		if !okF || !okT {
			continue // defensive: emitted edges always have both endpoints
		}
		if pred(from.layer, to.layer) {
			adj[e.From] = append(adj[e.From], e.To)
		}
		if pred(to.layer, from.layer) {
			adj[e.To] = append(adj[e.To], e.From)
		}
	}
	return adj
}

// closure computes the set of nodes reachable from start over adj (start
// itself excluded).
func closure(adj map[string][]string, start string) map[string]bool {
	seen := map[string]bool{}
	stack := []string{start}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, next := range adj[cur] {
			if next == start || seen[next] {
				continue
			}
			seen[next] = true
			stack = append(stack, next)
		}
	}
	return seen
}
