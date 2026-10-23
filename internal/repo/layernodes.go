package repo

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// LayerNodeRepo persists the read-only overlay nodes (issue #51). Insert
// semantics follow VirtualDeviceRepo: ResourceID "" is not valid for nodes
// (every overlay node has lineage), AnchorDeviceID "" and nil geo are
// stored as SQL NULL. A duplicate (workspace_id, code) wraps
// domain.ErrConflict.
type LayerNodeRepo struct{ q DBTX }

// layerNodeColumns is the canonical SELECT column list of virtual_nodes.
const layerNodeColumns = `id, workspace_id, resource_id, layer, kind, code, name, status,
anchor_device_id, cluster_key, geo_lat, geo_lng, detail_json, created_at, updated_at`

// scanLayerNode scans one virtual_nodes row into a domain.LayerNode.
func scanLayerNode(s scanner) (domain.LayerNode, error) {
	var (
		n              domain.LayerNode
		anchorDeviceID sql.NullString
		geoLat         sql.NullFloat64
		geoLng         sql.NullFloat64
		createdAt      string
		updatedAt      string
	)
	if err := s.Scan(&n.ID, &n.WorkspaceID, &n.ResourceID, &n.Layer, &n.Kind,
		&n.Code, &n.Name, &n.Status, &anchorDeviceID, &n.ClusterKey,
		&geoLat, &geoLng, &n.DetailJSON, &createdAt, &updatedAt); err != nil {
		return domain.LayerNode{}, err
	}
	n.AnchorDeviceID = stringFromNull(anchorDeviceID)
	n.GeoLat = floatPtrFromNull(geoLat)
	n.GeoLng = floatPtrFromNull(geoLng)
	var err error
	if n.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.LayerNode{}, err
	}
	if n.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.LayerNode{}, err
	}
	return n, nil
}

// Insert adds an overlay node. AnchorDeviceID "" and nil geo are stored as
// NULL; a duplicate (workspace_id, code) returns an error wrapping
// domain.ErrConflict; the layer/kind vocabularies are the materializer's
// contract (storage CHECKs are the physical twin).
func (r *LayerNodeRepo) Insert(ctx context.Context, n domain.LayerNode) error {
	if !domain.IsValidOverlayLayer(n.Layer) {
		return fmt.Errorf("repo: insert layer node %s: invalid layer %q", n.ID, n.Layer)
	}
	_, err := r.q.ExecContext(ctx, `INSERT INTO virtual_nodes
(id, workspace_id, resource_id, layer, kind, code, name, status, anchor_device_id, cluster_key, geo_lat, geo_lng, detail_json, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.WorkspaceID, n.ResourceID, n.Layer, n.Kind, n.Code, n.Name, n.Status,
		nullString(n.AnchorDeviceID), n.ClusterKey, nullFloatPtr(n.GeoLat), nullFloatPtr(n.GeoLng),
		n.DetailJSON, formatTime(n.CreatedAt), formatTime(n.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert layer node %s (%s/%s): %w: %w",
				n.ID, n.WorkspaceID, n.Code, domain.ErrConflict, err)
		}
		return fmt.Errorf("repo: insert layer node %s: %w", n.ID, err)
	}
	return nil
}

// ListByWorkspace returns every overlay node of the workspace, ordered by
// (layer, kind, code) — the deterministic materialization order.
func (r *LayerNodeRepo) ListByWorkspace(ctx context.Context, workspaceID string) ([]domain.LayerNode, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+layerNodeColumns+` FROM virtual_nodes WHERE workspace_id = ? ORDER BY layer, kind, code`,
		workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repo: list layer nodes of workspace %s: %w", workspaceID, err)
	}
	defer rows.Close()
	out := make([]domain.LayerNode, 0)
	for rows.Next() {
		n, err := scanLayerNode(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan layer node: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list layer nodes of workspace %s: %w", workspaceID, err)
	}
	return out, nil
}

// ListByCluster returns the overlay nodes of one cluster (cluster_key),
// paged and ordered by code — the member drill-down of a canvas cluster.
// pageSize <= 0 means the repo default (20); page is 1-based.
func (r *LayerNodeRepo) ListByCluster(ctx context.Context, workspaceID, clusterKey string, page, pageSize int) ([]domain.LayerNode, int, error) {
	if pageSize <= 0 {
		pageSize = 20
	}
	if page <= 0 {
		page = 1
	}
	var total int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM virtual_nodes WHERE workspace_id = ? AND cluster_key = ?`,
		workspaceID, clusterKey).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("repo: count cluster %s of workspace %s: %w", clusterKey, workspaceID, err)
	}
	if total == 0 || (page-1)*pageSize >= total {
		return []domain.LayerNode{}, total, nil
	}
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+layerNodeColumns+` FROM virtual_nodes
WHERE workspace_id = ? AND cluster_key = ? ORDER BY code LIMIT ? OFFSET ?`,
		workspaceID, clusterKey, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("repo: list cluster %s of workspace %s: %w", clusterKey, workspaceID, err)
	}
	defer rows.Close()
	out := make([]domain.LayerNode, 0)
	for rows.Next() {
		n, err := scanLayerNode(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("repo: scan layer node: %w", err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("repo: list cluster %s of workspace %s: %w", clusterKey, workspaceID, err)
	}
	return out, total, nil
}

// CountByWorkspace returns the overlay node count of the workspace.
func (r *LayerNodeRepo) CountByWorkspace(ctx context.Context, workspaceID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM virtual_nodes WHERE workspace_id = ?`, workspaceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count layer nodes of workspace %s: %w", workspaceID, err)
	}
	return n, nil
}
