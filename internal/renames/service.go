// Package renames implements the rename confirmation service (W6-D,
// migration v12): listing the suspected-rename proposals the importer
// records on new-key rows of name-keyed types (W006) and applying or
// rejecting them. This is the HUMAN channel of the "name change = identity
// change event" semantics — the identity merge only happens here. The MCP
// tool surface stays read-only (the fees-lab closed set of 11); an AI must
// never rewrite a resource's natural key or delete resources (the same
// reasoning that keeps every other resource-domain write behind HTTP/CLI).
//
// Fees-lab lineage note: the v12 table, this service's repo layer and the
// domain rules all survived the fees fork (ffc7139 kept migrations + repo
// + domain wholesale); the fork classified the services themselves as
// IPRAN-side and cut them. The fee line re-lands the service here because
// the fee reconciliation key is the SITE IDENTITY (fee_site_links,
// site-code-aliases, cross-reconcile all join on it): a renamed
// base_station/site that splits into two resources silently corrupts the
// per-site fee aggregation — the exact data-consistency hazard the R4
// analysis identified. docs/import-and-validation.md §16 (survived the
// fork) specifies this channel; this re-landing makes the docs true.
package renames

import (
	"context"
	"fmt"
	"time"

	"github.com/mmqz/netops-fees-lab/internal/domain"
	"github.com/mmqz/netops-fees-lab/internal/mapping"
	"github.com/mmqz/netops-fees-lab/internal/repo"
)

// DefaultPageSize / MaxPageSize follow the pagination contract bounds of
// PROJECT_BRIEF §9 (limit defaults to 20, clamps at 100) — the same bounds
// as the attribute change list.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Service applies and rejects pending rename proposals. All mutations run
// inside one DB.Do transaction each; reads are plain repo calls.
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

// RenameItem is the JSON shape of one rename proposal:
// {id, resourceId, resourceType, naturalKey, batchId, newResourceId, oldKey,
// newKey, matchedFields, status, sheet, rowNumber, createdAt, decidedAt}.
// resourceId/newResourceId keep their literal values after an apply (the
// new resource is deleted by the merge; the id stays as the audit trail).
type RenameItem struct {
	ID            string     `json:"id"`
	ResourceID    string     `json:"resourceId"`
	ResourceType  string     `json:"resourceType"`
	NaturalKey    string     `json:"naturalKey"`
	BatchID       string     `json:"batchId"`
	NewResourceID string     `json:"newResourceId"`
	OldKey        string     `json:"oldKey"`
	NewKey        string     `json:"newKey"`
	MatchedFields []string   `json:"matchedFields"`
	Status        string     `json:"status"`
	Sheet         string     `json:"sheet"`
	RowNumber     int        `json:"rowNumber"`
	CreatedAt     time.Time  `json:"createdAt"`
	DecidedAt     *time.Time `json:"decidedAt"`
}

// Filter selects rename proposals. Empty Status lists every status (the
// API's status=all); Limit/Offset pagination (limit defaults to 20, clamps
// at 100, offset >= 0). ResourceID/BatchID/ResourceType are exact-match
// filters; ResourceID matches the proposal's EITHER side (old resource or
// new resource — W11-D: the W006 issue anchors on the new resource, so the
// drawer-side query must surface the proposal from both sides).
type Filter struct {
	Status       domain.RenameStatus
	ResourceID   string
	ResourceType domain.ResourceType
	BatchID      string
	Limit        int
	Offset       int
}

// ListResult is the list envelope: {"items":[…],"total":n}.
type ListResult struct {
	Items []RenameItem `json:"items"`
	Total int          `json:"total"`
}

// List returns one window of rename proposals (ordered by created_at, id —
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
	rps, total, err := s.db.RenameProposals.List(ctx, repo.RenameFilter{
		Status:       f.Status,
		ResourceID:   f.ResourceID,
		ResourceType: f.ResourceType,
		BatchID:      f.BatchID,
		Limit:        limit,
		Offset:       offset,
	})
	if err != nil {
		return ListResult{}, fmt.Errorf("renames: list: %w", err)
	}
	// Join the old resource's side (type + current natural key) for display.
	// A proposal whose old resource vanished (rollback cascade) keeps the
	// literal ids and the recorded keys.
	ids := make([]string, 0, len(rps))
	seen := make(map[string]struct{}, len(rps))
	for _, rp := range rps {
		if _, dup := seen[rp.ResourceID]; !dup {
			seen[rp.ResourceID] = struct{}{}
			ids = append(ids, rp.ResourceID)
		}
	}
	resByID := make(map[string]domain.Resource, len(ids))
	for _, id := range ids {
		if res, err := s.db.Resources.GetByID(ctx, id); err == nil {
			resByID[id] = res
		}
	}
	items := make([]RenameItem, 0, len(rps))
	for _, rp := range rps {
		items = append(items, newRenameItem(rp, resByID[rp.ResourceID]))
	}
	return ListResult{Items: items, Total: total}, nil
}

// newRenameItem converts a domain proposal; res is the zero Resource when
// the join found nothing (deleted old resource), leaving the joined display
// fields empty — the proposal's own old/new keys still tell the story.
func newRenameItem(rp domain.RenameProposal, res domain.Resource) RenameItem {
	item := RenameItem{
		ID:            rp.ID,
		ResourceID:    rp.ResourceID,
		BatchID:       rp.BatchID,
		NewResourceID: rp.NewResourceID,
		OldKey:        rp.OldKey,
		NewKey:        rp.NewKey,
		MatchedFields: rp.MatchedFields,
		Status:        rp.Status.String(),
		Sheet:         rp.Sheet,
		RowNumber:     rp.RowNumber,
		CreatedAt:     rp.CreatedAt,
		DecidedAt:     rp.DecidedAt,
	}
	if res.ID != "" {
		item.ResourceType = res.Type.String()
		item.NaturalKey = res.NaturalKey
	} else {
		item.ResourceType = rp.ResourceType.String()
	}
	return item
}

// TargetMissingError reports the apply refusal of the rename confirmation
// flow: the proposal's NEW resource no longer exists while the proposal
// itself is still pending. Honest reachability note (W11-D): the STANDARD
// import-batch rollback deletes this batch's pending proposals BEFORE the
// resources (importer.Rollback → DeletePendingByBatch), so a rolled-back
// batch leaves no pending proposal behind and a later apply answers 404,
// not this error. This guard fires only when the new resource vanished
// through some other path while the proposal survived (e.g. a direct
// resource delete). Either way re-pointing the old resource at a key whose
// resource never materialized would be a merge into nothing, so the apply
// refuses and recommends rejecting the proposal. The HTTP layer maps it to
// 409 target_missing.
type TargetMissingError struct {
	ProposalID    string
	NewResourceID string
}

func (e *TargetMissingError) Error() string {
	return fmt.Sprintf("renames: 提案 %s 的新资源 %s 已不存在而提案仍在（标准批次回滚会先删本批提案、apply 将得 404；此情形多为新资源被其它路径直接删除）；建议驳回该提案后按现状重新导入",
		e.ProposalID, e.NewResourceID)
}

// Apply confirms a pending rename proposal: the identity merge runs inside
// ONE DB.Do transaction —
//
//  1. the new resource's relations are retargeted to the old resource id
//     (both endpoints; a retarget that would collide with an existing
//     old-side relation UNIQUE (from, to, type) deletes the new-side edge —
//     a duplicate edge is what a double-recorded asset produces);
//  2. the new resource's KEY-COMPONENT attributes (the natural-key fields
//     plus the fallback fields, mapping.TypeSpec.KeyComponentNames) are
//     copied onto the old resource — only where the new side carries a
//     non-empty value (an unmapped column on the new sheet must not erase
//     the stored value). The rename rewrites the identity key, so the
//     key-bearing attributes (base_station_name of a fallback-key row,
//     device_name of a port) must follow; leaving the old value would make
//     the key contradict its own source attribute and every re-import of
//     the renamed source would fire a W005 diff on that field. The display
//     NAME follows the same doctrine (W9-D, W8-Q P3-5): resources.name is
//     the projection of the type's NameField value, so the merged resource
//     keeps showing the NEW name — before W9-D a port/ptp_port rename left
//     the old display name on the merged resource forever;
//  3. the new resource is deleted (its pending attribute change proposals
//     vanish via the v11 resource_id CASCADE — documented behavior; its
//     SourceRef rows are deliberately left in place, see the honest-gap
//     note in docs/natural-keys.md §6);
//  4. the old resource's natural_key is rewritten to the proposal's new key
//     (the UNIQUE constraint cannot fire: the key's previous owner was
//     deleted in this same transaction) and updated_at is refreshed;
//  5. the proposal flips to applied with decided_at.
//
// The old resource's proxy id res_x, its relations and its SourceRefs are
// untouched — the merge is a key re-point, not a row swap.
//
// Non-pending proposals answer domain.ErrConflict (409, message naming the
// current status); an unknown id or a vanished OLD resource answers
// domain.ErrNotFound (404); a vanished NEW resource answers
// *TargetMissingError (409 target_missing, reject recommended — honest
// reachability: a standard batch rollback takes the pending proposal with
// it and answers 404 instead, so this fires only when the new resource was
// removed by some other path, e.g. a direct delete). Returns the
// proposal-after-apply item.
func (s *Service) Apply(ctx context.Context, id string) (RenameItem, error) {
	var item RenameItem
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		rp, err := tx.RenameProposals.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("renames: apply %s: %w", id, err)
		}
		if rp.Status != domain.RenameStatusPending {
			return fmt.Errorf("renames: apply %s: status %s, want pending: %w",
				id, rp.Status, domain.ErrConflict)
		}
		old, err := tx.Resources.GetByID(ctx, rp.ResourceID)
		if err != nil {
			return fmt.Errorf("renames: apply %s: old resource %s: %w", id, rp.ResourceID, err)
		}
		newRes, err := tx.Resources.GetByID(ctx, rp.NewResourceID)
		if err != nil {
			// Defensive guard (W11-D honest reachability note):
			// the standard rollback path cannot reach here — it
			// deletes this batch's pending proposals FIRST
			// (DeletePendingByBatch), so a rolled-back proposal
			// answers 404 at the Get above. This branch fires
			// only when the new resource vanished through some
			// other path (e.g. a direct resource delete) while
			// the pending proposal survived.
			return fmt.Errorf("renames: apply %s: %w", id, &TargetMissingError{ProposalID: id, NewResourceID: rp.NewResourceID})
		}
		now := s.clock()
		if _, _, err := tx.Relations.RetargetResource(ctx, old.ID, rp.NewResourceID); err != nil {
			return fmt.Errorf("renames: apply %s: retarget relations: %w", id, err)
		}
		syncKeyComponentAttributes(old, newRes)
		if err := tx.Resources.UpdateAttributes(ctx, old.ID, old.Attributes, now); err != nil {
			return fmt.Errorf("renames: apply %s: sync key attributes: %w", id, err)
		}
		if name := displayNameOf(old.Type, newRes); name != "" {
			old.Name = name
			if err := tx.Resources.UpdateName(ctx, old.ID, old.Name, now); err != nil {
				return fmt.Errorf("renames: apply %s: sync display name: %w", id, err)
			}
		}
		if _, err := tx.Resources.DeleteByID(ctx, rp.NewResourceID); err != nil {
			return fmt.Errorf("renames: apply %s: delete new resource: %w", id, err)
		}
		if err := tx.Resources.UpdateNaturalKey(ctx, old.ID, rp.NewKey, now); err != nil {
			return fmt.Errorf("renames: apply %s: rewrite natural key: %w", id, err)
		}
		if err := tx.RenameProposals.MarkApplied(ctx, id, now); err != nil {
			return fmt.Errorf("renames: apply %s: %w", id, err)
		}
		rp.Status = domain.RenameStatusApplied
		rp.DecidedAt = &now
		old.NaturalKey = rp.NewKey
		old.UpdatedAt = now
		item = newRenameItem(rp, old)
		return nil
	})
	if err != nil {
		return RenameItem{}, err
	}
	return item, nil
}

// syncKeyComponentAttributes is the Apply step-2 helper: for every
// key-component field of the proposal's type (natural key fields +
// fallback fields, mapping.TypeSpec.KeyComponentNames), a non-empty value
// on the NEW resource overwrites the old resource's attribute; an
// empty/absent new-side value keeps the stored one (an unmapped column is
// not evidence of an empty attribute). Only these fields are synced: every
// OTHER attribute is provably equal — that equality is the match
// precondition that raised the proposal.
func syncKeyComponentAttributes(old, newRes domain.Resource) {
	spec, ok := mapping.SpecByType(old.Type)
	if !ok {
		return // defensive: the closed set always resolves
	}
	for _, name := range spec.KeyComponentNames() {
		if v := newRes.Attributes[name]; v != "" {
			old.Attributes[name] = v
		}
	}
}

// displayNameOf resolves the merged display name from the NEW resource: the
// value is looked up in the new resource's ATTRIBUTES first (the NameField
// semantics — base_station_name is an ordinary attribute), with the new
// resource's own Name as the fallback: key-class name fields (port_name of
// port/ptp_port, pool_name of ip_pool) never enter the attributes (the
// importer excludes natural-key fields), but the importer computed the new
// resource's Name from exactly that field at insert time — so newRes.Name
// carries the renamed value there. An empty result means "no new-side name
// evidence" and the caller keeps the stored name (the same non-empty-wins
// doctrine as the key-component sync).
func displayNameOf(t domain.ResourceType, newRes domain.Resource) string {
	spec, ok := mapping.SpecByType(t)
	if !ok {
		return newRes.Name // defensive: the closed set always resolves
	}
	name := newRes.Attributes[spec.NameField()]
	if name == "" {
		name = newRes.Name
	}
	return name
}

// Reject refuses a pending rename proposal (pending → rejected): the two
// resources stay exactly as the import left them — independent identities,
// the pre-confirmation default. decided_at is stamped; the proposal remains
// as the audit trail of the deliberate decision. Non-pending proposals
// answer domain.ErrConflict (409); an unknown id answers
// domain.ErrNotFound (404). Returns the proposal-after-reject item.
func (s *Service) Reject(ctx context.Context, id string) (RenameItem, error) {
	var item RenameItem
	err := s.db.Do(ctx, func(tx *repo.Tx) error {
		rp, err := tx.RenameProposals.Get(ctx, id)
		if err != nil {
			return fmt.Errorf("renames: reject %s: %w", id, err)
		}
		if rp.Status != domain.RenameStatusPending {
			return fmt.Errorf("renames: reject %s: status %s, want pending: %w",
				id, rp.Status, domain.ErrConflict)
		}
		now := s.clock()
		if err := tx.RenameProposals.MarkRejected(ctx, id, now); err != nil {
			return fmt.Errorf("renames: reject %s: %w", id, err)
		}
		rp.Status = domain.RenameStatusRejected
		rp.DecidedAt = &now
		res, err := tx.Resources.GetByID(ctx, rp.ResourceID)
		if err != nil {
			return fmt.Errorf("renames: reject %s: old resource %s: %w", id, rp.ResourceID, err)
		}
		item = newRenameItem(rp, res)
		return nil
	})
	if err != nil {
		return RenameItem{}, err
	}
	return item, nil
}
