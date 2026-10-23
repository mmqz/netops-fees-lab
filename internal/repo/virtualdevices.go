package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
)

// VirtualDeviceRepo persists the virtual devices of the simulation
// workspaces. ResourceID "" is stored as SQL NULL (no lineage resource);
// the DDL turns a deleted resource into NULL via ON DELETE SET NULL, which
// reads back as "". GeoLat/GeoLng nil are stored as SQL NULL (no position).
type VirtualDeviceRepo struct{ q DBTX }

// virtualDeviceColumns is the canonical SELECT column list of
// virtual_devices.
const virtualDeviceColumns = `id, workspace_id, resource_id, code, name, status, geo_lat, geo_lng, created_at, updated_at`

// scanVirtualDevice scans one virtual_devices row into a
// domain.VirtualDevice.
func scanVirtualDevice(s scanner) (domain.VirtualDevice, error) {
	var (
		d          domain.VirtualDevice
		resourceID sql.NullString
		geoLat     sql.NullFloat64
		geoLng     sql.NullFloat64
		status     string
		createdAt  string
		updatedAt  string
	)
	if err := s.Scan(&d.ID, &d.WorkspaceID, &resourceID, &d.Code, &d.Name, &status,
		&geoLat, &geoLng, &createdAt, &updatedAt); err != nil {
		return domain.VirtualDevice{}, err
	}
	d.ResourceID = stringFromNull(resourceID)
	d.Status = domain.EntityStatus(status)
	d.GeoLat = floatPtrFromNull(geoLat)
	d.GeoLng = floatPtrFromNull(geoLng)
	var err error
	if d.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.VirtualDevice{}, err
	}
	if d.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.VirtualDevice{}, err
	}
	return d, nil
}

// Insert adds a virtual device. ResourceID "" and nil GeoLat/GeoLng are
// stored as NULL. A duplicate (workspace_id, code) returns an error
// wrapping domain.ErrConflict; an unknown workspace or resource fails on
// the foreign keys.
func (r *VirtualDeviceRepo) Insert(ctx context.Context, d domain.VirtualDevice) error {
	if !d.Status.IsValid() {
		return fmt.Errorf("repo: insert virtual device %s: invalid status %q", d.ID, d.Status)
	}
	_, err := r.q.ExecContext(ctx, `INSERT INTO virtual_devices
(id, workspace_id, resource_id, code, name, status, geo_lat, geo_lng, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.WorkspaceID, nullString(d.ResourceID), d.Code, d.Name, d.Status.String(),
		nullFloatPtr(d.GeoLat), nullFloatPtr(d.GeoLng),
		formatTime(d.CreatedAt), formatTime(d.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert virtual device %s (%s/%s): %w: %w",
				d.ID, d.WorkspaceID, d.Code, domain.ErrConflict, err)
		}
		return fmt.Errorf("repo: insert virtual device %s: %w", d.ID, err)
	}
	return nil
}

// GetByID returns the virtual device with the given id, or an error
// wrapping domain.ErrNotFound.
func (r *VirtualDeviceRepo) GetByID(ctx context.Context, id string) (domain.VirtualDevice, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+virtualDeviceColumns+` FROM virtual_devices WHERE id = ?`, id)
	d, err := scanVirtualDevice(row)
	if err != nil {
		return domain.VirtualDevice{}, notFound("virtual device", id, err)
	}
	return d, nil
}

// GetByCode returns the device of workspaceID with the given code (unique
// per workspace), or an error wrapping domain.ErrNotFound.
func (r *VirtualDeviceRepo) GetByCode(ctx context.Context, workspaceID, code string) (domain.VirtualDevice, error) {
	row := r.q.QueryRowContext(ctx,
		`SELECT `+virtualDeviceColumns+` FROM virtual_devices WHERE workspace_id = ? AND code = ?`,
		workspaceID, code)
	d, err := scanVirtualDevice(row)
	if err != nil {
		return domain.VirtualDevice{}, notFound("virtual device", workspaceID+"/"+code, err)
	}
	return d, nil
}

// ListByWorkspace returns every device of the workspace, ordered by code
// ascending.
func (r *VirtualDeviceRepo) ListByWorkspace(ctx context.Context, workspaceID string) ([]domain.VirtualDevice, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+virtualDeviceColumns+` FROM virtual_devices WHERE workspace_id = ? ORDER BY code`,
		workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repo: list virtual devices of workspace %s: %w", workspaceID, err)
	}
	defer rows.Close()
	out := make([]domain.VirtualDevice, 0)
	for rows.Next() {
		d, err := scanVirtualDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan virtual device: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list virtual devices of workspace %s: %w", workspaceID, err)
	}
	return out, nil
}

// CountByWorkspace returns the number of devices of the workspace per
// entity status. Absent keys mean zero; the total is the sum of the values.
func (r *VirtualDeviceRepo) CountByWorkspace(ctx context.Context, workspaceID string) (map[domain.EntityStatus]int, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM virtual_devices WHERE workspace_id = ? GROUP BY status`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repo: count virtual devices of workspace %s: %w", workspaceID, err)
	}
	defer rows.Close()
	counts := make(map[domain.EntityStatus]int)
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, fmt.Errorf("repo: scan count: %w", err)
		}
		counts[domain.EntityStatus(s)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: count virtual devices of workspace %s: %w", workspaceID, err)
	}
	return counts, nil
}

// UpdateStatus sets the device status and stamps updated_at to now. A
// missing device returns an error wrapping domain.ErrNotFound.
func (r *VirtualDeviceRepo) UpdateStatus(ctx context.Context, id string, status domain.EntityStatus) error {
	if !status.IsValid() {
		return fmt.Errorf("repo: update status of virtual device %s: invalid status %q", id, status)
	}
	res, err := r.q.ExecContext(ctx,
		`UPDATE virtual_devices SET status = ?, updated_at = ? WHERE id = ?`,
		status.String(), formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("repo: update status of virtual device %s: %w", id, err)
	}
	return requireUpdated(res, "virtual device", id)
}

// Touch stamps updated_at to now without changing any other column. A
// missing device returns an error wrapping domain.ErrNotFound.
func (r *VirtualDeviceRepo) Touch(ctx context.Context, id string) error {
	res, err := r.q.ExecContext(ctx, `UPDATE virtual_devices SET updated_at = ? WHERE id = ?`,
		formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("repo: touch virtual device %s: %w", id, err)
	}
	return requireUpdated(res, "virtual device", id)
}

// Delete removes the device. Its interfaces and the links touching them are
// removed by the DDL ON DELETE CASCADEs.
func (r *VirtualDeviceRepo) Delete(ctx context.Context, id string) error {
	res, err := r.q.ExecContext(ctx, `DELETE FROM virtual_devices WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("repo: delete virtual device %s: %w", id, err)
	}
	return requireUpdated(res, "virtual device", id)
}
