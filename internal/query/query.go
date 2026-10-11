// Package query implements the read-side query services of NetOps Lab over
// the repository layer: resource search with pagination, resource detail
// with source provenance, the relations subgraph (via internal/topology),
// database statistics, and the deterministic topology reachability check
// (PROJECT_BRIEF §9). The package performs no writes.
package query

import (
	"context"
	"fmt"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
	"github.com/mmqz/netops-lab/internal/topology"
)

// DefaultPageSize / MaxPageSize pin the pagination contract of §9.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Service answers read queries against the database.
type Service struct {
	db *repo.DB
}

// New returns a Service bound to db.
func New(db *repo.DB) *Service {
	return &Service{db: db}
}

// ResourceItem is the JSON shape of one resource in list/detail responses
// (§9: {id, type, naturalKey, name, status, attributes, firstBatchId,
// updatedAt}).
type ResourceItem struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	NaturalKey   string            `json:"naturalKey"`
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	Attributes   map[string]string `json:"attributes"`
	FirstBatchID string            `json:"firstBatchId"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

// newResourceItem converts a domain resource; Attributes is never nil so the
// JSON object is always present.
func newResourceItem(r domain.Resource) ResourceItem {
	attrs := r.Attributes
	if attrs == nil {
		attrs = map[string]string{}
	}
	return ResourceItem{
		ID:           r.ID,
		Type:         r.Type.String(),
		NaturalKey:   r.NaturalKey,
		Name:         r.Name,
		Status:       r.Status,
		Attributes:   attrs,
		FirstBatchID: r.FirstBatchID,
		UpdatedAt:    r.UpdatedAt,
	}
}

// SearchFilter selects resources. Type/Q/Status semantics follow
// repo.ResourceSearchFilter (q = case-insensitive contains over natural_key,
// name and attributes). Page is 1-based; PageSize defaults to
// DefaultPageSize and is capped at MaxPageSize.
type SearchFilter struct {
	Type     domain.ResourceType
	Q        string
	Status   string
	Page     int
	PageSize int
}

// PageResult is the pagination envelope of every list endpoint
// (§9: {"items", "total", "page", "pageSize"}).
type PageResult[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

// SearchResources returns one page of matching resources plus the total
// number of matches.
func (s *Service) SearchResources(ctx context.Context, f SearchFilter) (PageResult[ResourceItem], error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PageSize < 1 {
		f.PageSize = DefaultPageSize
	}
	if f.PageSize > MaxPageSize {
		f.PageSize = MaxPageSize
	}
	if f.Type != "" && !f.Type.IsValid() {
		return PageResult[ResourceItem]{}, fmt.Errorf("query: search resources: invalid type %q: %w", f.Type, domain.ErrNotFound)
	}
	resources, total, err := s.db.Resources.Search(ctx, repo.ResourceSearchFilter{
		Type:   f.Type,
		Q:      f.Q,
		Status: f.Status,
		Limit:  f.PageSize,
		Offset: (f.Page - 1) * f.PageSize,
	})
	if err != nil {
		return PageResult[ResourceItem]{}, fmt.Errorf("query: search resources: %w", err)
	}
	items := make([]ResourceItem, 0, len(resources))
	for _, r := range resources {
		items = append(items, newResourceItem(r))
	}
	return PageResult[ResourceItem]{Items: items, Total: total, Page: f.Page, PageSize: f.PageSize}, nil
}

// SourceSearchFilter selects resources the way the MCP resource_search tool
// pages: raw Limit/Offset (0-based offset, limit 0..N — the caller validates
// and caps; the repo treats Limit <= 0 as unlimited) instead of the 1-based
// Page/PageSize envelope of SearchFilter. Q/Type semantics are identical to
// SearchFilter.
type SourceSearchFilter struct {
	Type   domain.ResourceType
	Q      string
	Limit  int
	Offset int
}

// ResourceSource is the resolved provenance of one resource: the workbook,
// sheet and 1-based row of its CREATING import.
type ResourceSource struct {
	Workbook  string `json:"workbook"`
	Sheet     string `json:"sheet"`
	RowNumber int    `json:"rowNumber"`
}

// ResourceWithSource pairs one resource item with its resolved creating
// source. Source is nil — and ApplicationGenerated true — exactly when the
// resource has no source refs at all (e.g. simulation materialization).
type ResourceWithSource struct {
	ResourceItem
	Source               *ResourceSource `json:"source,omitempty"`
	ApplicationGenerated bool            `json:"applicationGenerated"`
}

// SearchResourcesWithSource runs the resource search and resolves, in ONE
// batched source_refs query (repo SourceRefs.ListByEntityIDs), the CREATING
// source of every hit: the ref whose batch matches the resource's
// FirstBatchID, falling back to the earliest recorded ref (rowid order) when
// no ref matches (W7-D version refs make the fallback the earliest version).
// The per-item semantics are byte-identical to the pre-W9-D MCP
// implementation, which resolved them one ListByEntity query per row (N+1);
// this method is the single-query replacement the MCP tool now calls. Total
// counts the matches ignoring limit/offset.
func (s *Service) SearchResourcesWithSource(ctx context.Context, f SourceSearchFilter) ([]ResourceWithSource, int, error) {
	if f.Type != "" && !f.Type.IsValid() {
		return nil, 0, fmt.Errorf("query: search resources with source: invalid type %q: %w", f.Type, domain.ErrNotFound)
	}
	resources, total, err := s.db.Resources.Search(ctx, repo.ResourceSearchFilter{
		Type:   f.Type,
		Q:      f.Q,
		Limit:  f.Limit,
		Offset: f.Offset,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("query: search resources with source: %w", err)
	}
	ids := make([]string, len(resources))
	for i, r := range resources {
		ids[i] = r.ID
	}
	refsByID, err := s.db.SourceRefs.ListByEntityIDs(ctx, domain.EntityKindResource, ids)
	if err != nil {
		return nil, 0, fmt.Errorf("query: resolve creating sources: %w", err)
	}
	items := make([]ResourceWithSource, 0, len(resources))
	for _, r := range resources {
		item := ResourceWithSource{ResourceItem: newResourceItem(r)}
		for _, ref := range refsByID[r.ID] {
			if ref.BatchID == r.FirstBatchID {
				item.Source = &ResourceSource{Workbook: ref.Workbook, Sheet: ref.Sheet, RowNumber: ref.RowNumber}
				break
			}
		}
		if item.Source == nil && len(refsByID[r.ID]) > 0 {
			ref := refsByID[r.ID][0]
			item.Source = &ResourceSource{Workbook: ref.Workbook, Sheet: ref.Sheet, RowNumber: ref.RowNumber}
		}
		if item.Source == nil {
			item.ApplicationGenerated = true
		}
		items = append(items, item)
	}
	return items, total, nil
}

// SourceRefItem is the source provenance of one imported row: the workbook,
// sheet and 1-based Excel row that produced an entity, plus the raw row as
// {header: value} pairs.
type SourceRefItem struct {
	ID        string            `json:"id"`
	BatchID   string            `json:"batchId"`
	Workbook  string            `json:"workbook"`
	Sheet     string            `json:"sheet"`
	RowNumber int               `json:"rowNumber"`
	Raw       map[string]string `json:"raw"`
}

// ResourceDetail is the detail view of one resource: the resource itself
// (flat, same field names as list items) plus its source refs and the number
// of relations it participates in.
type ResourceDetail struct {
	ResourceItem
	SourceRefs    []SourceRefItem `json:"sourceRefs"`
	RelationCount int             `json:"relationCount"`
}

// ResourceDetail loads one resource with its source refs and relation count.
// An unknown id returns an error wrapping domain.ErrNotFound.
func (s *Service) ResourceDetail(ctx context.Context, id string) (ResourceDetail, error) {
	r, err := s.db.Resources.GetByID(ctx, id)
	if err != nil {
		return ResourceDetail{}, fmt.Errorf("query: resource %s: %w", id, err)
	}
	refs, err := s.db.SourceRefs.ListByEntity(ctx, domain.EntityKindResource, id)
	if err != nil {
		return ResourceDetail{}, fmt.Errorf("query: source refs of resource %s: %w", id, err)
	}
	refItems := make([]SourceRefItem, 0, len(refs))
	for _, ref := range refs {
		raw := ref.Raw
		if raw == nil {
			raw = map[string]string{}
		}
		refItems = append(refItems, SourceRefItem{
			ID: ref.ID, BatchID: ref.BatchID, Workbook: ref.Workbook,
			Sheet: ref.Sheet, RowNumber: ref.RowNumber, Raw: raw,
		})
	}
	relations, err := s.db.Relations.ListByResource(ctx, id)
	if err != nil {
		return ResourceDetail{}, fmt.Errorf("query: relations of resource %s: %w", id, err)
	}
	return ResourceDetail{
		ResourceItem:  newResourceItem(r),
		SourceRefs:    refItems,
		RelationCount: len(relations),
	}, nil
}

// RelationsResult is the relations subgraph of one resource (§9):
// {"center", "nodes", "edges"} — the center resource, its neighbors within
// depth device-hops and the stored relations among them.
type RelationsResult struct {
	Center topology.Node   `json:"center"`
	Nodes  []topology.Node `json:"nodes"`
	Edges  []topology.Edge `json:"edges"`
}

// MaxRelationDepth pins the depth limit of the relations endpoint (§9).
const MaxRelationDepth = 2

// graph loads the whole resource/relation inventory and builds the topology
// graph. Phase 1 datasets are small (tens to hundreds of resources); a
// per-request build is deterministic and avoids cache invalidation concerns.
func (s *Service) graph(ctx context.Context) (*topology.Graph, error) {
	resources, _, err := s.db.Resources.Search(ctx, repo.ResourceSearchFilter{})
	if err != nil {
		return nil, fmt.Errorf("query: load resources: %w", err)
	}
	relations, err := s.db.Relations.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("query: load relations: %w", err)
	}
	return topology.Build(ctx, resources, relations), nil
}

// Relations returns the BFS neighborhood subgraph of the resource id within
// depth device-hops (depth must be 1 or 2; MaxRelationDepth caps it). An
// unknown id returns an error wrapping domain.ErrNotFound.
func (s *Service) Relations(ctx context.Context, id string, depth int) (RelationsResult, error) {
	if depth < 1 || depth > MaxRelationDepth {
		return RelationsResult{}, fmt.Errorf("query: relations depth %d out of range [1, %d]", depth, MaxRelationDepth)
	}
	g, err := s.graph(ctx)
	if err != nil {
		return RelationsResult{}, err
	}
	center, ok := g.NodeByID(id)
	if !ok {
		return RelationsResult{}, fmt.Errorf("query: resource %s: %w", id, domain.ErrNotFound)
	}
	nodes, edges, found := g.Neighbors(id, depth)
	if !found {
		return RelationsResult{}, fmt.Errorf("query: resource %s: %w", id, domain.ErrNotFound)
	}
	if nodes == nil {
		nodes = []topology.Node{}
	}
	if edges == nil {
		edges = []topology.Edge{}
	}
	return RelationsResult{Center: center, Nodes: nodes, Edges: edges}, nil
}

// Stats is the /api/stats aggregate (§9): resource counts by type and
// status, relation counts by type, issue counts by severity and the newest
// batch id. The three legacy resource types, the four known relation types
// and the severities are always present (0 when absent) so UI charts render
// stable axes; newer resource types (port, base_station, site, machine_room)
// and byStatus list only the values actually present. LatestBatchID is ""
// when no batch exists.
type Stats struct {
	TotalResources   int            `json:"totalResources"`
	ByType           map[string]int `json:"byType"`
	ByStatus         map[string]int `json:"byStatus"`
	ByRelationType   map[string]int `json:"byRelationType"`
	IssuesBySeverity map[string]int `json:"issuesBySeverity"`
	LatestBatchID    string         `json:"latestBatchId"`
}

// Stats computes the database aggregates.
func (s *Service) Stats(ctx context.Context) (Stats, error) {
	byType, err := s.db.Resources.CountByType(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("query: count resources by type: %w", err)
	}
	total := 0
	byTypeJSON := map[string]int{
		domain.ResourceTypeDevice.String():  0,
		domain.ResourceTypeIPPool.String():  0,
		domain.ResourceTypeCircuit.String(): 0,
	}
	for t, n := range byType {
		byTypeJSON[t.String()] = n
		total += n
	}

	byStatus, err := s.db.Resources.CountByStatus(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("query: count resources by status: %w", err)
	}
	byStatusJSON := make(map[string]int, len(byStatus))
	for st, n := range byStatus {
		byStatusJSON[st] = n
	}

	byRelation, err := s.db.Relations.CountByType(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("query: count relations by type: %w", err)
	}
	byRelationJSON := map[string]int{
		domain.RelationTypeAssignedTo.String(): 0,
		domain.RelationTypeAEnd.String():       0,
		domain.RelationTypeZEnd.String():       0,
		domain.RelationTypeBelongsTo.String():  0,
	}
	for t, n := range byRelation {
		byRelationJSON[t.String()] = n
	}

	issuesBySeverity, err := s.db.Issues.CountBySeverity(ctx, nil)
	if err != nil {
		return Stats{}, fmt.Errorf("query: count issues by severity: %w", err)
	}
	issuesJSON := map[string]int{
		domain.SeverityError.String():   0,
		domain.SeverityWarning.String(): 0,
		domain.SeverityInfo.String():    0,
	}
	for sev, n := range issuesBySeverity {
		issuesJSON[sev.String()] = n
	}

	latest := ""
	batches, _, err := s.db.Batches.List(ctx, 1, 0)
	if err != nil {
		return Stats{}, fmt.Errorf("query: list batches: %w", err)
	}
	if len(batches) > 0 {
		latest = batches[0].ID
	}

	return Stats{
		TotalResources:   total,
		ByType:           byTypeJSON,
		ByStatus:         byStatusJSON,
		ByRelationType:   byRelationJSON,
		IssuesBySeverity: issuesJSON,
		LatestBatchID:    latest,
	}, nil
}

// TopologyHop is one device on the reachability path (§9:
// {deviceId, deviceCode}).
type TopologyHop struct {
	DeviceID   string `json:"deviceId"`
	DeviceCode string `json:"deviceCode"`
}

// CheckResult is the /api/topology/check response (§9).
type CheckResult struct {
	Reachable  bool          `json:"reachable"`
	Hops       []TopologyHop `json:"hops"`
	PathLength int           `json:"pathLength"`
}

// TopologyCheck answers whether toDevice is reachable from fromDevice within
// maxHops device links, returning the BFS shortest path. Devices are
// identified by device_code; an unknown code returns an error wrapping
// domain.ErrNotFound (mapped to HTTP 404 by the API layer).
func (s *Service) TopologyCheck(ctx context.Context, fromDevice, toDevice string, maxHops int) (CheckResult, error) {
	from, err := s.db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, fromDevice)
	if err != nil {
		return CheckResult{}, fmt.Errorf("query: device %s: %w", fromDevice, err)
	}
	to, err := s.db.Resources.GetByTypeKey(ctx, domain.ResourceTypeDevice, toDevice)
	if err != nil {
		return CheckResult{}, fmt.Errorf("query: device %s: %w", toDevice, err)
	}
	g, err := s.graph(ctx)
	if err != nil {
		return CheckResult{}, err
	}
	reachable, nodes, length := g.Reachable(from.ID, to.ID, maxHops)
	if !reachable {
		return CheckResult{Reachable: false, Hops: []TopologyHop{}, PathLength: 0}, nil
	}
	hops := make([]TopologyHop, 0, len(nodes))
	for _, n := range nodes {
		hops = append(hops, TopologyHop{DeviceID: n.ID, DeviceCode: n.NaturalKey})
	}
	return CheckResult{Reachable: true, Hops: hops, PathLength: length}, nil
}
