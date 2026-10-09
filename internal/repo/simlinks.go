package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
)

// SimLinkRepo persists the simulated links between two interfaces. ResourceID
// "" is stored as SQL NULL (no lineage circuit resource). The DDL enforces
// a_interface_id <> z_interface_id (self loops are rejected at the storage
// boundary as well as by the engine).
type SimLinkRepo struct{ q DBTX }

// simLinkColumns is the canonical SELECT column list of links.
const simLinkColumns = `id, workspace_id, a_interface_id, z_interface_id, status, label, resource_id, created_at, updated_at`

// scanSimLink scans one links row into a domain.SimLink.
func scanSimLink(s scanner) (domain.SimLink, error) {
	var (
		l          domain.SimLink
		status     string
		resourceID sql.NullString
		createdAt  string
		updatedAt  string
	)
	if err := s.Scan(&l.ID, &l.WorkspaceID, &l.AInterfaceID, &l.ZInterfaceID, &status, &l.Label,
		&resourceID, &createdAt, &updatedAt); err != nil {
		return domain.SimLink{}, err
	}
	l.Status = domain.EntityStatus(status)
	l.ResourceID = stringFromNull(resourceID)
	var err error
	if l.CreatedAt, err = parseTime(createdAt); err != nil {
		return domain.SimLink{}, err
	}
	if l.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return domain.SimLink{}, err
	}
	return l, nil
}

// Insert adds a link between two interfaces of the same workspace. The DDL
// rejects a link with AInterfaceID == ZInterfaceID (CHECK constraint) and
// unknown endpoints (foreign keys); both surface as plain errors.
func (r *SimLinkRepo) Insert(ctx context.Context, l domain.SimLink) error {
	if !l.Status.IsValid() {
		return fmt.Errorf("repo: insert link %s: invalid status %q", l.ID, l.Status)
	}
	_, err := r.q.ExecContext(ctx, `INSERT INTO links
(id, workspace_id, a_interface_id, z_interface_id, status, label, resource_id, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?)`,
		l.ID, l.WorkspaceID, l.AInterfaceID, l.ZInterfaceID, l.Status.String(), l.Label,
		nullString(l.ResourceID), formatTime(l.CreatedAt), formatTime(l.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("repo: insert link %s: %w: %w", l.ID, domain.ErrConflict, err)
		}
		return fmt.Errorf("repo: insert link %s: %w", l.ID, err)
	}
	return nil
}

// GetByID returns the link with the given id, or an error wrapping
// domain.ErrNotFound.
func (r *SimLinkRepo) GetByID(ctx context.Context, id string) (domain.SimLink, error) {
	row := r.q.QueryRowContext(ctx, `SELECT `+simLinkColumns+` FROM links WHERE id = ?`, id)
	l, err := scanSimLink(row)
	if err != nil {
		return domain.SimLink{}, notFound("link", id, err)
	}
	return l, nil
}

// ListByWorkspace returns every link of the workspace, ordered by id
// ascending. Ids are globally monotonic (ln-<n>), so this is also
// creation order.
func (r *SimLinkRepo) ListByWorkspace(ctx context.Context, workspaceID string) ([]domain.SimLink, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+simLinkColumns+` FROM links WHERE workspace_id = ? ORDER BY id`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repo: list links of workspace %s: %w", workspaceID, err)
	}
	defer rows.Close()
	out := make([]domain.SimLink, 0)
	for rows.Next() {
		l, err := scanSimLink(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan link: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list links of workspace %s: %w", workspaceID, err)
	}
	return out, nil
}

// ListTouchingInterface returns every link whose A or Z endpoint is the
// given interface, ordered by id ascending.
func (r *SimLinkRepo) ListTouchingInterface(ctx context.Context, interfaceID string) ([]domain.SimLink, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT `+simLinkColumns+` FROM links
WHERE a_interface_id = ? OR z_interface_id = ? ORDER BY id`,
		interfaceID, interfaceID)
	if err != nil {
		return nil, fmt.Errorf("repo: list links touching interface %s: %w", interfaceID, err)
	}
	defer rows.Close()
	out := make([]domain.SimLink, 0)
	for rows.Next() {
		l, err := scanSimLink(rows)
		if err != nil {
			return nil, fmt.Errorf("repo: scan link: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repo: list links touching interface %s: %w", interfaceID, err)
	}
	return out, nil
}

// CountByWorkspace returns the number of links of the workspace per entity
// status. Absent keys mean zero; the total is the sum of the values.
func (r *SimLinkRepo) CountByWorkspace(ctx context.Context, workspaceID string) (map[domain.EntityStatus]int, error) {
	rows, err := r.q.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM links WHERE workspace_id = ? GROUP BY status`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("repo: count links of workspace %s: %w", workspaceID, err)
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
		return nil, fmt.Errorf("repo: count links of workspace %s: %w", workspaceID, err)
	}
	return counts, nil
}

// UpdateStatus sets the link status and stamps updated_at to now. A missing
// link returns an error wrapping domain.ErrNotFound.
func (r *SimLinkRepo) UpdateStatus(ctx context.Context, id string, status domain.EntityStatus) error {
	if !status.IsValid() {
		return fmt.Errorf("repo: update status of link %s: invalid status %q", id, status)
	}
	res, err := r.q.ExecContext(ctx,
		`UPDATE links SET status = ?, updated_at = ? WHERE id = ?`,
		status.String(), formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("repo: update status of link %s: %w", id, err)
	}
	return requireUpdated(res, "link", id)
}

// Touch stamps updated_at to now without changing any other column. A
// missing link returns an error wrapping domain.ErrNotFound.
func (r *SimLinkRepo) Touch(ctx context.Context, id string) error {
	res, err := r.q.ExecContext(ctx, `UPDATE links SET updated_at = ? WHERE id = ?`,
		formatTime(time.Now().UTC()), id)
	if err != nil {
		return fmt.Errorf("repo: touch link %s: %w", id, err)
	}
	return requireUpdated(res, "link", id)
}

// Delete removes the link.
func (r *SimLinkRepo) Delete(ctx context.Context, id string) error {
	res, err := r.q.ExecContext(ctx, `DELETE FROM links WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("repo: delete link %s: %w", id, err)
	}
	return requireUpdated(res, "link", id)
}
