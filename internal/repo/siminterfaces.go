package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// SimInterfaceRepo persists the simulated interfaces (table `interfaces` —
// the Go type is named SimInterface to avoid the standard library clash).
//
// Nullable convention: IPv4Addr "" is stored as SQL NULL (unconfigured) and
// IPv4Prefix -1 as SQL NULL. When the address is configured, UpdateIPv4 and
// Insert require the prefix to be in 0..32; an empty address always stores
// both columns NULL (a stray prefix value is ignored, the engine never
// produces addr-empty/prefix-set pairs).
type SimInterfaceRepo struct{ q DBTX }

// simInterfaceColumns is the canonical SELECT column list of interfaces.
const simInterfaceColumns = `id, workspace_id, device_id, name, status, ipv4_addr, ipv4_prefix, created_at, updated_at`

// scanSimInterface scans one interfaces row into a domain.SimInterface.
func scanSimInterface(s scanner) (domain.SimInterface, error) {
	var (
		i         domain.SimInterface
		status    string
		addr      sql.NullString
		prefix    sql.NullInt64
		createdAt string
		updatedAt string
	)
	if err := s.Scan(&i.ID, &i.WorkspaceID, &i.DeviceID, &i.Name, &status, &addr, &prefix, &createdAt, &updatedAt); err != nil {
		return domain.SimInterface{}, err
	}
	i.Status = domain.EntityStatus(status)
	i.IPv4Addr = stringFromNull(addr)
	if prefix.Valid {
		i.IPv4Prefix = int(prefix.Int64)
	} else {
		i.IPv4Prefix = -1
	}
	var err error
	if i.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.SimInterface{}, err
	}
	if i.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.SimInterface{}, err
	}
	return i, nil
}

// simInterfaceIPArgs maps the domain IPv4 representation onto SQL values:
// (NULL, NULL) when addr is empty, (addr, prefix) otherwise. A configured
// address must carry a prefix of 0..32.
func simInterfaceIPArgs(addr string, prefix int) (any, any, error) {
	if addr == "" {
		return nil, nil, nil
	}
	if prefix < 0 || prefix > 32 {
		return nil, nil, fmt.Errorf("invalid prefix %d for address %q (want 0..32, or empty address for unconfigured)", prefix, addr)
	}
	return addr, prefix, nil
}

// Insert adds an interface. A duplicate (workspace_id, device_id, name)
// returns an error wrapping domain.ErrConflict; an unknown device or
// workspace fails on the foreign keys. See the repo header for the
// NULL⇔zero-value convention of the IPv4 fields.
func (r *SimInterfaceRepo) Insert(ctx context.Context, i domain.SimInterface) error {
	if !i.Status.IsValid() {
		return fmt.Errorf("repo: insert interface %s: invalid status %q", i.ID, i.Status)
	}
	addr, prefix, err := simInterfaceIPArgs(i.IPv4Addr, i.IPv4Prefix)
	if err != nil {
		return fmt.Errorf("repo: insert interface %s: %w", i.ID, err)
	}
	_, err = r.q.ExecContext(ctx, `INSERT INTO interfaces
(id, workspace_id, device_id, name, status, ipv4_addr, ipv4_prefix, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
		i.ID, i.WorkspaceID, i.DeviceID, i.Name, i.Status.String(), addr, prefix,
		formatTime(i.CreatedAt), formatTime(i.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert interface %s (%s/%s): %w: %w",
				i.ID, i.DeviceID, i.Name, domain.ErrConflict, err)
		}
		return fmt.Errorf("repo: insert interface %s: %w", i.ID, err)
	}
	return nil
}

// GetByID returns the interface with the given id, or an error wrapping
// domain.ErrNotFound.
func (r *SimInterfaceRepo) GetByID(ctx context.Context, id string) (domain.SimInterface, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+simInterfaceColumns+` FROM interfaces WHERE id = ?`, id)
	i, err := scanSimInterface(row)
	if err != nil {
		return domain.SimInterface{}, notFound("interface", id, err)
	}
	return i, nil
}

// ListByWorkspace returns every interface of the workspace, ordered by
// (device_id, name) ascending.
func (r *SimInterfaceRepo) ListByWorkspace(ctx context.Context, workspaceID string) ([]domain.SimInterface, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+simInterfaceColumns+` FROM interfaces WHERE workspace_id = ? ORDER BY device_id, name`,
		workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repo: list interfaces of workspace %s: %w", workspaceID, err)
	}
	defer rows.Close()
	out := make([]domain.SimInterface, 0)
	for rows.Next() {
		i, err := scanSimInterface(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan interface: %w", err)
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list interfaces of workspace %s: %w", workspaceID, err)
	}
	return out, nil
}

// ListByDevice returns every interface of the device, ordered by name
// ascending.
func (r *SimInterfaceRepo) ListByDevice(ctx context.Context, deviceID string) ([]domain.SimInterface, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+simInterfaceColumns+` FROM interfaces WHERE device_id = ? ORDER BY name`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("repo: list interfaces of device %s: %w", deviceID, err)
	}
	defer rows.Close()
	out := make([]domain.SimInterface, 0)
	for rows.Next() {
		i, err := scanSimInterface(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan interface: %w", err)
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list interfaces of device %s: %w", deviceID, err)
	}
	return out, nil
}

// CountByWorkspace returns the number of interfaces of the workspace per
// entity status. Absent keys mean zero; the total is the sum of the values.
func (r *SimInterfaceRepo) CountByWorkspace(ctx context.Context, workspaceID string) (map[domain.EntityStatus]int, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM interfaces WHERE workspace_id = ? GROUP BY status`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repo: count interfaces of workspace %s: %w", workspaceID, err)
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
		return nil, fmt.Errorf("repo: count interfaces of workspace %s: %w", workspaceID, err)
	}
	return counts, nil
}

// UpdateStatus sets the interface status and stamps updated_at to now. A
// missing interface returns an error wrapping domain.ErrNotFound.
func (r *SimInterfaceRepo) UpdateStatus(ctx context.Context, id string, status domain.EntityStatus) error {
	if !status.IsValid() {
		return fmt.Errorf("repo: update status of interface %s: invalid status %q", id, status)
	}
	res, err := r.q.ExecContext(ctx,
		`UPDATE interfaces SET status = ?, updated_at = ? WHERE id = ?`,
		status.String(), formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("repo: update status of interface %s: %w", id, err)
	}
	return requireUpdated(res, "interface", id)
}

// UpdateIPv4 reconfigures the interface address. An empty addr unconfigures
// the interface (both columns NULL, any prefix value is ignored); a
// non-empty addr must carry a prefix of 0..32. updated_at is stamped to
// now. A missing interface returns an error wrapping domain.ErrNotFound.
func (r *SimInterfaceRepo) UpdateIPv4(ctx context.Context, id, addr string, prefix int) error {
	addrArg, prefixArg, err := simInterfaceIPArgs(addr, prefix)
	if err != nil {
		return fmt.Errorf("repo: update ipv4 of interface %s: %w", id, err)
	}
	res, err := r.q.ExecContext(ctx,
		`UPDATE interfaces SET ipv4_addr = ?, ipv4_prefix = ?, updated_at = ? WHERE id = ?`,
		addrArg, prefixArg, formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("repo: update ipv4 of interface %s: %w", id, err)
	}
	return requireUpdated(res, "interface", id)
}

// Touch stamps updated_at to now without changing any other column. A
// missing interface returns an error wrapping domain.ErrNotFound.
func (r *SimInterfaceRepo) Touch(ctx context.Context, id string) error {
	res, err := r.q.ExecContext(ctx, `UPDATE interfaces SET updated_at = ? WHERE id = ?`,
		formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("repo: touch interface %s: %w", id, err)
	}
	return requireUpdated(res, "interface", id)
}

// Delete removes the interface. Links touching it are removed by the DDL
// ON DELETE CASCADE.
func (r *SimInterfaceRepo) Delete(ctx context.Context, id string) error {
	res, err := r.q.ExecContext(ctx, `DELETE FROM interfaces WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("repo: delete interface %s: %w", id, err)
	}
	return requireUpdated(res, "interface", id)
}
