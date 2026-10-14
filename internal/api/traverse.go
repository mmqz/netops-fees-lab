package api

// traverse.go — the W22 graph-query HTTP surface (design data-model-v1.md
// §7.3 last two endpoints + §10): the vertical traverse, the impact
// (root-cause / impact-surface) and the one-shot AI context assembly. The
// engines live in internal/topology (layer-delta BFS); this file binds them
// to the repository and owns ONLY parameter validation and response
// projection. The legacy GET /resources/{id}/relations endpoint stays
// untouched (deprecated in docs, §10.10).

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/query"
	"github.com/mmqz/netops-lab/internal/topology"
)

// Impact parameter default (§10.5/§10.6): impact walks collect the whole
// chain — depth budget 8. The traverse budgets live in the topology
// package (shared with the engine's zero-value hardening).
const defaultImpactMaxDepth = 8

// graphStore returns the composition root's repository binding of the
// engines' TraverseStore contract (services.Services.GraphStore — issue
// #41: the binding moved to the single composition point when the CLI
// graph channel needed the SAME store; the doc comment lives there).
func (s *Server) graphStore() topology.StoreFuncs {
	return s.svc.GraphStore()
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

	// Issue #41: the assembly itself lives in the composition root
	// (Services.GraphContext) — the HTTP handler and the CLI `query
	// context` channel answer the byte-identical document from ONE
	// implementation (the cross-channel e2e test pins equivalence).
	resp, err := s.svc.GraphContext(r.Context(), r.PathValue("id"), depth, withMetrics)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, resp)
}
