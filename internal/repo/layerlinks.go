package repo

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mmqz/netops-lab/internal/domain"
)

// LayerLinkRepo persists the read-only overlay edges (issue #51): logical
// carrying (L3), service, access and grouping relations projected from the
// unified graph. ResourceID "" (a derived edge) is stored as SQL NULL.
type LayerLinkRepo struct{ q DBTX }

// layerLinkColumns is the canonical SELECT column list of
// virtual_layer_links.
const layerLinkColumns = `id, workspace_id, layer, sem, label, resource_id, a_kind, a_ref, z_kind, z_ref, created_at, updated_at`

// scanLayerLink scans one virtual_layer_links row into a domain.LayerLink.
func scanLayerLink(s scanner) (domain.LayerLink, error) {
	var (
		l          domain.LayerLink
		resourceID sql.NullString
		createdAt  string
		updatedAt  string
	)
	if err := s.Scan(&l.ID, &l.WorkspaceID, &l.Layer, &l.Sem, &l.Label, &resourceID,
		&l.AKind, &l.ARef, &l.ZKind, &l.ZRef, &createdAt, &updatedAt); err != nil {
		return domain.LayerLink{}, err
	}
	l.ResourceID = stringFromNull(resourceID)
	var err error
	if l.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.LayerLink{}, err
	}
	if l.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.LayerLink{}, err
	}
	return l, nil
}

// Insert adds an overlay edge. The layer/sem/endpoint vocabularies are
// validated first (domain), the storage CHECKs are the physical twin.
func (r *LayerLinkRepo) Insert(ctx context.Context, l domain.LayerLink) error {
	if !domain.IsValidOverlayLayer(l.Layer) || l.Layer == domain.LayerSpatial {
		return fmt.Errorf("repo: insert layer link %s: invalid layer %q", l.ID, l.Layer)
	}
	if !domain.IsValidLayerLinkSem(l.Sem) {
		return fmt.Errorf("repo: insert layer link %s: invalid sem %q", l.ID, l.Sem)
	}
	if !domain.IsValidLayerEndpointKind(l.AKind) || !domain.IsValidLayerEndpointKind(l.ZKind) {
		return fmt.Errorf("repo: insert layer link %s: invalid endpoint kinds %q/%q", l.ID, l.AKind, l.ZKind)
	}
	_, err := r.q.ExecContext(ctx, `INSERT INTO virtual_layer_links
(id, workspace_id, layer, sem, label, resource_id, a_kind, a_ref, z_kind, z_ref, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		l.ID, l.WorkspaceID, l.Layer, l.Sem, l.Label, nullString(l.ResourceID),
		l.AKind, l.ARef, l.ZKind, l.ZRef, formatTime(l.CreatedAt), formatTime(l.UpdatedAt))
	if err != nil {
		return fmt.Errorf("repo: insert layer link %s: %w", l.ID, err)
	}
	return nil
}

// ListByWorkspace returns every overlay edge of the workspace, ordered by
// (layer, sem, id) — the deterministic materialization order (ids are
// sequence-allocated in creation order).
func (r *LayerLinkRepo) ListByWorkspace(ctx context.Context, workspaceID string) ([]domain.LayerLink, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+layerLinkColumns+` FROM virtual_layer_links WHERE workspace_id = ? ORDER BY layer, sem, id`,
		workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repo: list layer links of workspace %s: %w", workspaceID, err)
	}
	defer rows.Close()
	out := make([]domain.LayerLink, 0)
	for rows.Next() {
		l, err := scanLayerLink(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan layer link: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list layer links of workspace %s: %w", workspaceID, err)
	}
	return out, nil
}

// CountByWorkspace returns the overlay edge count of the workspace.
func (r *LayerLinkRepo) CountByWorkspace(ctx context.Context, workspaceID string) (int, error) {
	var n int
	if err := r.q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM virtual_layer_links WHERE workspace_id = ?`, workspaceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repo: count layer links of workspace %s: %w", workspaceID, err)
	}
	return n, nil
}
