package api

// traverse.go — the W22 graph-query HTTP surface (design data-model-v1.md
// §7.3 last two endpoints + §10): the vertical traverse, the impact
// (root-cause / impact-surface) and the one-shot AI context assembly. The
// engines live in internal/topology (layer-delta BFS); this file binds them
// to the repository and owns ONLY parameter validation and response
// projection. The legacy GET /resources/{id}/relations endpoint stays
// untouched (deprecated in docs, §10.10).

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/query"
	"github.com/mmqz/netops-lab/internal/topology"
)

// Impact parameter default (§10.5/§10.6): impact walks collect the whole
// chain — depth budget 8. The traverse budgets live in the topology
// package (shared with the engine's zero-value hardening).
const defaultImpactMaxDepth = 8

// graphStore binds the repository to the engines' TraverseStore contract:
// node payloads via ResourceRepo.GetByID (one read per resource, the
// engine's info cache dedupes), incident edges via RelationRepo.
// ListByResource — the per-node load whose EXPLAIN QUERY PLAN resolves to
// MULTI-INDEX OR over the UNIQUE(from,to,type) prefix and idx_relations_to
// (the §10.7 indexes), i.e. genuinely O(V+E) incremental loading.
func (s *Server) graphStore() topology.StoreFuncs {
	return topology.StoreFuncs{
		NodeFn: func(ctx context.Context, id string) (topology.NodeInfo, error) {
			r, err := s.db.Resources.GetByID(ctx, id)
			if err != nil {
				return topology.NodeInfo{}, err
			}
			return topology.NodeInfoOf(r), nil
		},
		EdgeFn: func(ctx context.Context, id string) ([]domain.Relation, error) {
			return s.db.Relations.ListByResource(ctx, id)
		},
	}
}

// handleResourceTraverse implements
// GET /api/resources/{id}/traverse?direction=down|up|both&layers=0,1,2&relations=carried_by,uses_fiber&maxDepth=1..8&maxNodes=1..500&pathOnly=true|false.
//
// direction defaults to down (§10.8 #4 — 追根因); reachability is
// layer-delta driven (down = descend-or-lateral, up = ascend-or-lateral,
// both = always; lateral edges expand in every mode) while the response
// edges keep the STORED dependency orientation for AI reasoning. 404 for
// an unknown root; 400 for invalid parameter values (direction, layers
// outside 0..6, unknown relation types, maxDepth/maxNodes out of range,
// pathOnly not true/false). See the api.md section for the exact contract.
func (s *Server) handleResourceTraverse(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	direction := topology.DirectionDown
	if raw := q.Get("direction"); raw != "" {
		switch raw {
		case topology.DirectionDown, topology.DirectionUp, topology.DirectionBoth:
			direction = raw
		default:
			respondErrorf(w, http.StatusBadRequest, "bad_request", "direction 参数 %q 不是 down/up/both", raw)
			return
		}
	}

	var layers map[int]bool
	if raw := q.Get("layers"); raw != "" {
		layers = map[int]bool{}
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			v, err := strconv.Atoi(part)
			if err != nil || v < 0 || v > 6 {
				respondErrorf(w, http.StatusBadRequest, "bad_request",
					"layers 参数 %q 含非法层号 %q（层号必须是 0..6）", raw, part)
				return
			}
			layers[v] = true
		}
	}

	var relations map[domain.RelationType]bool
	if raw := q.Get("relations"); raw != "" {
		relations = map[domain.RelationType]bool{}
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			rt := domain.RelationType(part)
			if !rt.IsValid() {
				respondErrorf(w, http.StatusBadRequest, "bad_request",
					"relations 参数 %q 含未登记的关系类型 %q（合法值见 /api/schema 或 domain.AllRelationTypes）", raw, part)
				return
			}
			relations[rt] = true
		}
	}

	maxDepth := topology.DefaultTraverseMaxDepth
	if raw := q.Get("maxDepth"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > topology.MaxTraverseMaxDepth {
			respondErrorf(w, http.StatusBadRequest, "bad_request",
				"maxDepth 参数 %q 必须是 1..%d 的整数", raw, topology.MaxTraverseMaxDepth)
			return
		}
		maxDepth = v
	}
	maxNodes := topology.DefaultTraverseMaxNodes
	if raw := q.Get("maxNodes"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > topology.MaxTraverseMaxNodes {
			respondErrorf(w, http.StatusBadRequest, "bad_request",
				"maxNodes 参数 %q 必须是 1..%d 的整数", raw, topology.MaxTraverseMaxNodes)
			return
		}
		maxNodes = v
	}
	pathOnly := false
	if raw := q.Get("pathOnly"); raw != "" {
		switch raw {
		case "true":
			pathOnly = true
		case "false":
			pathOnly = false
		default:
			respondErrorf(w, http.StatusBadRequest, "bad_request", "pathOnly 参数 %q 必须是 true 或 false", raw)
			return
		}
	}

	result, err := topology.Traverse(r.Context(), s.graphStore(), r.PathValue("id"), topology.TraverseParams{
		Direction: direction,
		Layers:    layers,
		Relations: relations,
		MaxDepth:  maxDepth,
		MaxNodes:  maxNodes,
		PathOnly:  pathOnly,
	})
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// handleResourceImpact implements
// GET /api/resources/{id}/impact?direction=up|down&maxDepth=1..8.
//
// direction defaults to up (§10.6 ② 影响面收集) — down is allowed for the
// §10.5 step-3 root-cause walk; maxDepth defaults to 8 (cap 8). The node
// budget is engine-side (500). Candidates = abnormal nodes (run_state
// down/degraded, root included per §10.6 ③) with an abnormal-free
// traversal-direction downstream; impacted = normal nodes reachable from
// an abnormality by ascending; a missing run_state is NOT abnormal (no
// data, no verdict). 404/400 parity with traverse.
func (s *Server) handleResourceImpact(w http.ResponseWriter, r *http.Request) {
	direction := topology.DirectionUp
	if raw := r.URL.Query().Get("direction"); raw != "" {
		switch raw {
		case topology.DirectionUp, topology.DirectionDown:
			direction = raw
		default:
			respondErrorf(w, http.StatusBadRequest, "bad_request",
				"direction 参数 %q 不是 up/down（impact 不支持 both）", raw)
			return
		}
	}
	maxDepth := defaultImpactMaxDepth
	if raw := r.URL.Query().Get("maxDepth"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > topology.MaxTraverseMaxDepth {
			respondErrorf(w, http.StatusBadRequest, "bad_request",
				"maxDepth 参数 %q 必须是 1..%d 的整数", raw, topology.MaxTraverseMaxDepth)
			return
		}
		maxDepth = v
	}

	result, err := topology.Impact(r.Context(), s.graphStore(), r.PathValue("id"), direction, maxDepth)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// contextNeighbors is the neighbors summary of the context response: the
// legacy depth-N relations subgraph minus the center (the center IS the
// inlined resource).
type contextNeighbors struct {
	Nodes []topology.Node `json:"nodes"`
	Edges []topology.Edge `json:"edges"`
}

// contextMetric is ONE latest point of ONE series of the resource.
type contextMetric struct {
	MetricKey string  `json:"metricKey"`
	Unit      string  `json:"unit"`
	TS        string  `json:"ts"`
	Value     float64 `json:"value"`
	Quality   string  `json:"quality"`
}

// contextResponse is the §7.3 one-shot AI context assembly: the resource
// detail (the GET /api/resources/{id} shape, runState included, flattened),
// the legacy relations neighbors summary, the latest point of every metric
// series of the resource and the anomalies-scan items filtered to it.
type contextResponse struct {
	query.ResourceDetail
	Neighbors       contextNeighbors      `json:"neighbors"`
	Metrics         []contextMetric       `json:"metrics"`
	RecentAnomalies []metrics.AnomalyItem `json:"recentAnomalies"`
}

// handleResourceContext implements
// GET /api/resources/{id}/context?depth=1&metrics=latest.
//
// depth defaults to 1, capped at 2 — it reuses the LEGACY relations BFS
// (query.Relations; MaxRelationDepth = 2) rather than rebuilding a second
// subgraph engine. metrics=latest (default) inlines the last point of every
// series; metrics=none skips the block. recentAnomalies is the
// /api/metrics/anomalies result filtered to this resource (default scan
// window/threshold; W23's alert engine will join the alert state here —
// documented gap, see api.md). 404 for an unknown resource, 400 for
// invalid depth/metrics values.
func (s *Server) handleResourceContext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	depth := 1
	if raw := q.Get("depth"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > query.MaxRelationDepth {
			respondErrorf(w, http.StatusBadRequest, "bad_request",
				"depth 参数 %q 必须是 1 或 2", raw)
			return
		}
		depth = v
	}
	withMetrics := true
	if raw := q.Get("metrics"); raw != "" {
		switch raw {
		case "latest":
			withMetrics = true
		case "none":
			withMetrics = false
		default:
			respondErrorf(w, http.StatusBadRequest, "bad_request", "metrics 参数 %q 必须是 latest 或 none", raw)
			return
		}
	}

	id := r.PathValue("id")
	detail, err := s.query.ResourceDetail(r.Context(), id)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	rels, err := s.query.Relations(r.Context(), id, depth)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}

	resp := contextResponse{ResourceDetail: detail, Neighbors: contextNeighbors{Nodes: rels.Nodes, Edges: rels.Edges}}
	if withMetrics {
		series, err := s.metrics.ResourceSeries(r.Context(), id, nil, "", "", metrics.AggRaw)
		if err != nil {
			s.respondErrorFor(w, r, err, "")
			return
		}
		resp.Metrics = make([]contextMetric, 0, len(series))
		for _, sr := range series {
			if len(sr.Points) == 0 {
				continue
			}
			last := sr.Points[len(sr.Points)-1]
			resp.Metrics = append(resp.Metrics, contextMetric{
				MetricKey: sr.MetricKey, Unit: sr.Unit, TS: last.TS, Value: last.Value,
				Quality: string(last.Quality),
			})
		}
	}
	// The anomalies scan with default parameters (metricKey="", unbounded
	// window, default threshold, any direction) filtered to this resource —
	// "the /api/metrics/anomalies result filtered to this resource".
	items, err := s.metrics.Anomalies(r.Context(), "", "", "", 0, "")
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	resp.RecentAnomalies = make([]metrics.AnomalyItem, 0)
	for _, it := range items {
		if it.ResourceID == id {
			resp.RecentAnomalies = append(resp.RecentAnomalies, it)
		}
	}
	respondJSON(w, http.StatusOK, resp)
}
