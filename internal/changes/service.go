// Package changes implements the attribute change confirmation service of
// NetOps Lab (W4-D, migration v11): listing the attribute change proposals
// the importer recorded on duplicate-key rows (W005) and applying or
// dismissing them. This is the HUMAN channel of the update-on-diff import
// mode — the proposals only take effect here. The MCP tool surface stays
// frozen at the five read-only tools; an AI must never mutate resource
// attributes (the same reasoning that keeps every other resource-domain
// write behind HTTP/CLI).
package changes

import (
	"context"
	"fmt"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/repo"
)

// DefaultPageSize / MaxPageSize follow the pagination contract bounds of
// PROJECT_BRIEF §9 (limit defaults to 20, clamps at 100).
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Service applies and dismisses pending attribute changes. All mutations
// run inside one DB.Do transaction each; reads are plain repo calls.
type Service struct {
	db *repo.DB

	// Now is the clock hook; tests may replace it for deterministic
	// timestamps. New installs time.Now; a nil hook also falls back to
	// time.Now.
	Now func() time.Time
}

// New returns a Service bound to db.
func New(db *repo.DB) *Service {
	return &Service{db: db, Now: time.Now}
}

func (s *Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// ChangeItem is the JSON shape of one attribute change proposal:
// {id, resourceId, resourceType, naturalKey, batchId, field, oldValue,
// newValue, status, sheet, rowNumber, createdAt, appliedAt}.
type ChangeItem struct {
	ID           string     `json:"id"`
	ResourceID   string     `json:"resourceId"`
	ResourceType string     `json:"resourceType"`
	NaturalKey   string     `json:"naturalKey"`
	BatchID      string     `json:"batchId"`
	Field        string     `json:"field"`
	OldValue     string     `json:"oldValue"`
	NewValue     string     `json:"newValue"`
	Status       string     `json:"status"`
	Sheet        string     `json:"sheet"`
	RowNumber    int        `json:"rowNumber"`
	CreatedAt    time.Time  `json:"createdAt"`
	AppliedAt    *time.Time `json:"appliedAt"`
}

// Filter selects change proposals. Empty Status lists every status (the
// API's status=all); Limit/Offset pagination (limit defaults to 20, clamps
// at 100, offset >= 0). ResourceID/BatchID are exact-match filters.
type Filter struct {
	Status     domain.ChangeStatus
	ResourceID string
	BatchID    string
	Limit      int
	Offset     int
}

// ListResult is the list envelope: {"items":[…],"total":n}.
type ListResult struct {
	Items []ChangeItem `json:"items"`
	Total int          `json:"total"`
}

// List returns one window of change proposals (ordered by created_at, id —
// the chronological queue order) plus the total number of matches.
func (s *Service) List(ctx context.Context, f Filter) (ListResult, error) {
	limit, offset := f.Limit, f.Offset
	if limit < 1 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	chs, total, err := s.db.AttributeChanges.List(ctx, repo.ChangeFilter{
		Status:     f.Status,
		ResourceID: f.ResourceID,
		BatchID:    f.BatchID,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		return ListResult{}, fmt.Errorf("changes: list: %w", err)
	}
	// Join the resource side (type + natural key) for display. A proposal
	// whose resource vanished (rollback cascade) keeps the literal ids.
	ids := make([]string, 0, len(chs))
	seen := make(map[string]struct{}, len(chs))
	for _, ch := range chs {
		if _, dup := seen[ch.ResourceID]; !dup {
			seen[ch.ResourceID] = struct{}{}
			ids = append(ids, ch.ResourceID)
		}
	}
	resByID := make(map[string]domain.Resource, len(ids))
	for _, id := range ids {
		if res, err := s.db.Resources.GetByID(ctx, id); err == nil {
			resByID[id] = res
		}
	}
	items := make([]ChangeItem, 0, len(chs))
	for _, ch := range chs {
		items = append(items, newChangeItem(ch, resByID[ch.ResourceID]))
	}
	return ListResult{Items: items, Total: total}, nil
}

// newChangeItem converts a domain change; res is the zero Resource when the
// join found nothing (deleted resource), leaving type/key empty.
func newChangeItem(ch domain.AttributeChange, res domain.Resource) ChangeItem {
	return ChangeItem{
		ID:           ch.ID,
		ResourceID:   ch.ResourceID,
		ResourceType: res.Type.String(),
		NaturalKey:   res.NaturalKey,
		BatchID:      ch.BatchID,
		Field:        ch.Field,
		OldValue:     ch.OldValue,
		NewValue:     ch.NewValue,
		Status:       ch.Status.String(),
		Sheet:        ch.Sheet,
		RowNumber:    ch.RowNumber,
		CreatedAt:    ch.CreatedAt,
		AppliedAt:    ch.AppliedAt,
	}
}

// isDisplayNameField reports whether field is the resource type's
// display-name field (mapping.TypeSpec.NameField — the override-aware
// heuristic the importer itself used to set resources.name at insert time).
// A confirmed change on that field must also rewrite the stored name: the
// name is the projection of the attribute's value.
func isDisplayNameField(t domain.ResourceType, field string) bool {
	spec, ok := mapping.SpecByType(t)
	if !ok {
		return false // defensive: the closed set always resolves
	}
	return spec.NameField() == field
}

// StaleError reports the apply refusal of the W005 confirmation flow: the
// stored attribute value no longer equals the proposal's old value, i.e.
// another change (an earlier apply on a coexisting proposal, or a manual
// update) modified the field after this proposal was recorded. The HTTP
// layer maps it to 409 attribute_conflict.
type StaleError struct {
	Field   string
	Current string
	Old     string
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("changes: 字段 %s 的当前值 %q 与待确认变更记录的旧值 %q 不一致（该字段已被其它变更修改），请驳回后重新导入",
		e.Field, e.Current, e.Old)
}

// Apply confirms a pending change proposal: it verifies the resource's
// current attribute value still equals OldValue (otherwise StaleError →
// 409), writes NewValue into the attributes, refreshes the resource's
// updated_at and flips the proposal to applied — all inside ONE DB.Do
// transaction, so a mid-flight failure leaves both the resource and the
// proposal untouched. W9-D: when the changed field IS the type's display-name
// field (mapping.TypeSpec.NameField — base_station_name, device_name, …),
// resources.name follows the new value; the name is the projection of that
// attribute and a stale name would contradict the applied change.
//
// Non-pending proposals (already applied/dismissed) answer
// domain.ErrConflict (409); an unknown id or a vanished resource answers
// domain.ErrNotFound (404). Returns the change-after-apply object.
func (s *Service) Apply(ctx context.Context, id string) (ChangeItem, error) {
	var item ChangeItem
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		ch, err := tx.AttributeChanges.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("changes: apply %s: %w", id, err)
		}
		if ch.Status != domain.ChangeStatusPending {
			return fmt.Errorf("changes: apply %s: status %s, want pending: %w",
				id, ch.Status, domain.ErrConflict)
		}
		res, err := tx.Resources.GetByID(ctx, ch.ResourceID)
		if err != nil {
			return fmt.Errorf("changes: apply %s: resource %s: %w", id, ch.ResourceID, err)
		}
		if cur := res.Attributes[ch.Field]; cur != ch.OldValue {
			return &StaleError{Field: ch.Field, Current: cur, Old: ch.OldValue}
		}
		now := s.clock()
		attrs := res.Attributes
		if attrs == nil {
			attrs = map[string]string{}
		}
		attrs[ch.Field] = ch.NewValue
		if err := tx.Resources.UpdateAttributes(ctx, res.ID, attrs, now); err != nil {
			return fmt.Errorf("changes: apply %s: %w", id, err)
		}
		if isDisplayNameField(res.Type, ch.Field) {
			if err := tx.Resources.UpdateName(ctx, res.ID, ch.NewValue, now); err != nil {
				return fmt.Errorf("changes: apply %s: sync display name: %w", id, err)
			}
			res.Name = ch.NewValue
		}
		if err := tx.AttributeChanges.MarkApplied(ctx, id, now); err != nil {
			return fmt.Errorf("changes: apply %s: %w", id, err)
		}
		res.Attributes = attrs
		res.UpdatedAt = now
		ch.Status = domain.ChangeStatusApplied
		ch.AppliedAt = &now
		item = newChangeItem(ch, res)
		return nil
	})
	if err != nil {
		return ChangeItem{}, err
	}
	return item, nil
}

// Dismiss rejects a pending change proposal (pending → dismissed): the
// resource is never touched and applied_at stays NULL; the proposal remains
// as the audit trail of the deliberate "ignore". Non-pending proposals
// answer domain.ErrConflict (409); an unknown id answers
// domain.ErrNotFound (404). Returns the change-after-dismiss object.
func (s *Service) Dismiss(ctx context.Context, id string) (ChangeItem, error) {
	var item ChangeItem
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		ch, err := tx.AttributeChanges.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("changes: dismiss %s: %w", id, err)
		}
		if ch.Status != domain.ChangeStatusPending {
			return fmt.Errorf("changes: dismiss %s: status %s, want pending: %w",
				id, ch.Status, domain.ErrConflict)
		}
		if err := tx.AttributeChanges.MarkDismissed(ctx, id); err != nil {
			return fmt.Errorf("changes: dismiss %s: %w", id, err)
		}
		ch.Status = domain.ChangeStatusDismissed
		res, err := tx.Resources.GetByID(ctx, ch.ResourceID)
		if err != nil {
			return fmt.Errorf("changes: dismiss %s: resource %s: %w", id, ch.ResourceID, err)
		}
		item = newChangeItem(ch, res)
		return nil
	})
	if err != nil {
		return ChangeItem{}, err
	}
	return item, nil
}
