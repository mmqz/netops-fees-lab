// Package query implements the read-side query services of NetOps Lab over
// the repository layer: resource search with pagination, resource detail
// database statistics, and the deterministic topology reachability check
// (PROJECT_BRIEF §9). The package performs no writes.
package query

import (
	"context"
	"fmt"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/repo"
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
// updatedAt}). RunState is the W22 operational-state snapshot ("" = no
// observation — NULL; the explicit "unknown" is a recorded state).
type ResourceItem struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	NaturalKey   string            `json:"naturalKey"`
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	RunState     string            `json:"runState"`
	Attributes   map[string]string `json:"attributes"`
	FirstBatchID string            `json:"firstBatchId"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	// GeoLat/GeoLng expose the v20 REAL columns (issue #45): null when the
	// resource carries no coordinates — the same nil semantics as the
	// domain struct, so clients can branch without a sentinel value.
	GeoLat *float64 `json:"geoLat"`
	GeoLng *float64 `json:"geoLng"`
}

// newResourceItem converts a domain resource; Attributes is never nil so the
// JSON object is always present.
// NewResourceItem converts a domain resource for external consumers (the
// CLI query nearest channel of issue #45 shares this shape with the HTTP
// list endpoints — one conversion, zero drift).
func NewResourceItem(r domain.Resource) ResourceItem {
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
		RunState:     r.RunState,
		Attributes:   attrs,
		FirstBatchID: r.FirstBatchID,
		UpdatedAt:    r.UpdatedAt,
		GeoLat:       r.GeoLat,
		GeoLng:       r.GeoLng,
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
		items = append(items, NewResourceItem(r))
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
		item := ResourceWithSource{ResourceItem: NewResourceItem(r)}
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
		ResourceItem:  NewResourceItem(r),
		SourceRefs:    refItems,
		RelationCount: len(relations),
	}, nil
}

// Stats is the /api/stats aggregate (§9): resource counts by type and
// status, relation counts by type, issue counts by severity, the W22
// run-state snapshot counts and the newest batch id. The three legacy
// resource types, the four known relation types, the severities and the six
// run-state buckets (up/down/degraded/maintenance/unknown + "null" for
// resources with NO observation) are always present (0 when absent) so UI
// charts render stable axes; newer resource types (port, base_station,
// site, machine_room) and byStatus list only the values actually present.
// LatestBatchID is "" when no batch exists.
type Stats struct {
	TotalResources   int            `json:"totalResources"`
	ByType           map[string]int `json:"byType"`
	ByStatus         map[string]int `json:"byStatus"`
	ByRelationType   map[string]int `json:"byRelationType"`
	ByRunState       map[string]int `json:"byRunState"`
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

	byRunState, nulls, err := s.db.ResourceStatus.CountByRunState(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("query: count resources by run_state: %w", err)
	}
	runStateJSON := map[string]int{
		string(domain.RunStateUp):          0,
		string(domain.RunStateDown):        0,
		string(domain.RunStateDegraded):    0,
		string(domain.RunStateMaintenance): 0,
		string(domain.RunStateUnknown):     0,
		"null":                             0,
	}
	for st, n := range byRunState {
		runStateJSON[st.String()] = n
	}
	runStateJSON["null"] = nulls

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
		ByRunState:       runStateJSON,
		IssuesBySeverity: issuesJSON,
		LatestBatchID:    latest,
	}, nil
}
