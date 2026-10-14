package services

// Graph query bindings of the composition root (issue #41: the CLI graph
// channel traverse/impact/context + issue #45's nearest). The HTTP handlers
// used to be the only construction site of the topology store binding and
// the only assembler of the §7.3 context document — the CLI channel needs
// the SAME instances and the SAME assembly, so both moved here: one
// construction, zero drift between the HTTP and CLI surfaces (the
// cross-channel e2e test pins JSON equivalence).

import (
	"context"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/metrics"
	"github.com/mmqz/netops-lab/internal/query"
	"github.com/mmqz/netops-lab/internal/topology"
)

// GraphStore returns the repository binding of the topology engines'
// TraverseStore contract: node payloads via ResourceRepo.GetByID (one read
// per resource, the engine's info cache dedupes), incident edges via
// RelationRepo.ListByResource — the per-node load whose EXPLAIN QUERY PLAN
// resolves to MULTI-INDEX OR over the UNIQUE(from,to,type) prefix and
// idx_relations_to (the §10.7 indexes), i.e. genuinely O(V+E) incremental
// loading. The HTTP traverse/impact handlers and the CLI query channel
// consume this ONE binding.
func (s *Services) GraphStore() topology.StoreFuncs {
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

// GraphContextNeighbors is the neighbors summary of the context response:
// the legacy depth-N relations subgraph minus the center (the center IS the
// inlined resource).
type GraphContextNeighbors struct {
	Nodes []topology.Node `json:"nodes"`
	Edges []topology.Edge `json:"edges"`
}

// GraphContextMetric is ONE latest point of ONE series of the resource.
type GraphContextMetric struct {
	MetricKey string  `json:"metricKey"`
	Unit      string  `json:"unit"`
	TS        string  `json:"ts"`
	Value     float64 `json:"value"`
	Quality   string  `json:"quality"`
}

// GraphContextResult is the §7.3 one-shot AI context assembly: the resource
// detail (the GET /api/resources/{id} shape, runState included, flattened),
// the legacy relations neighbors summary, the latest point of every metric
// series of the resource and the anomalies-scan items filtered to it.
// Field set and JSON tags are the contract the HTTP endpoint has served
// since W22 — the struct moved here unchanged so the CLI channel answers
// the byte-identical shape.
type GraphContextResult struct {
	query.ResourceDetail
	Neighbors       GraphContextNeighbors `json:"neighbors"`
	Metrics         []GraphContextMetric  `json:"metrics"`
	RecentAnomalies []metrics.AnomalyItem `json:"recentAnomalies"`
}

// GraphContext assembles the §7.3 context document of one resource:
// ResourceDetail + depth-N neighbors (the legacy relations BFS,
// query.Relations with its MaxRelationDepth=2 cap — the caller validated)
// + the latest point of every metric series (withMetrics=false skips) +
// the default anomalies scan filtered to this resource. An unknown id
// returns an error wrapping domain.ErrNotFound. Both the HTTP handler and
// the CLI `query context` subcommand call THIS method — one assembly.
func (s *Services) GraphContext(ctx context.Context, id string, depth int, withMetrics bool) (*GraphContextResult, error) {
	detail, err := s.Query.ResourceDetail(ctx, id)
	if err != nil {
		return nil, err
	}
	rels, err := s.Query.Relations(ctx, id, depth)
	if err != nil {
		return nil, err
	}

	// All collection fields initialize to empty — never nil (audit §2.6:
	// null vs [] breaks strict clients; the contract is []).
	nodes, edges := rels.Nodes, rels.Edges
	if nodes == nil {
		nodes = make([]topology.Node, 0)
	}
	if edges == nil {
		edges = make([]topology.Edge, 0)
	}
	resp := &GraphContextResult{
		ResourceDetail: detail,
		Neighbors:      GraphContextNeighbors{Nodes: nodes, Edges: edges},
		Metrics:        make([]GraphContextMetric, 0),
	}
	if withMetrics {
		series, err := s.Metrics.ResourceSeries(ctx, id, nil, "", "", metrics.AggRaw)
		if err != nil {
			return nil, err
		}
		resp.Metrics = make([]GraphContextMetric, 0, len(series))
		for _, sr := range series {
			if len(sr.Points) == 0 {
				continue
			}
			last := sr.Points[len(sr.Points)-1]
			resp.Metrics = append(resp.Metrics, GraphContextMetric{
				MetricKey: sr.MetricKey, Unit: sr.Unit, TS: last.TS, Value: last.Value,
				Quality: string(last.Quality),
			})
		}
	}
	// The anomalies scan with default parameters (metricKey="", unbounded
	// window, default threshold, any direction) filtered to this resource —
	// "the /api/metrics/anomalies result filtered to this resource".
	// Initialized (not nil) so the empty case serializes as [] (audit §2.6).
	resp.RecentAnomalies = make([]metrics.AnomalyItem, 0)
	items, err := s.Metrics.Anomalies(ctx, "", "", "", 0, "")
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.ResourceID == id {
			resp.RecentAnomalies = append(resp.RecentAnomalies, it)
		}
	}
	return resp, nil
}
