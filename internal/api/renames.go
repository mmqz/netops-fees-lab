package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/renames"
)

// handleRenamesList implements
// GET /api/renames?status=pending|applied|rejected|all&resource_id=&resource_type=&batch_id=&limit=&offset=
// → {"items":[{id,resourceId,resourceType,naturalKey,batchId,newResourceId,oldKey,newKey,matchedFields,status,sheet,rowNumber,createdAt,decidedAt}],"total":n}.
// The pagination bounds follow the §9 list-endpoint conventions (limit
// defaults to 20, clamps at 100; offset defaults to 0, must be >= 0) — the
// same contract as GET /api/attribute-changes. status=all (or absent) lists
// every status; an unknown status is a 400.
func (s *Server) handleRenamesList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := renames.Filter{
		ResourceID: q.Get("resource_id"),
		BatchID:    q.Get("batch_id"),
		Limit:      renames.DefaultPageSize,
		Offset:     0,
	}
	if raw := q.Get("resource_type"); raw != "" {
		t, err := domain.ParseResourceType(raw)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "resource_type 参数 %q 不是已知的资源类型", raw)
			return
		}
		filter.ResourceType = t
	}
	if raw := q.Get("status"); raw != "" && raw != "all" {
		st, err := domain.ParseRenameStatus(raw)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "status 参数 %q 不是 pending/applied/rejected/all", raw)
			return
		}
		filter.Status = st
	}
	if raw := q.Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "limit 参数 %q 不是正整数", raw)
			return
		}
		if v > renames.MaxPageSize {
			v = renames.MaxPageSize // clamp, never fail (§9: max 100)
		}
		filter.Limit = v
	}
	if raw := q.Get("offset"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "offset 参数 %q 不是非负整数", raw)
			return
		}
		filter.Offset = v
	}
	result, err := s.renames.List(r.Context(), filter)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// handleRenameApply implements POST /api/renames/{id}/apply: 200 with the
// proposal-after-apply object on success; 409 conflict when the proposal is
// no longer pending (message names the current status); 409 target_missing
// (TargetMissingError — the new resource vanished, typically with a rolled
// back batch; the message recommends rejecting) when the merge target is
// gone; 404 for an unknown id or a vanished old resource.
func (s *Server) handleRenameApply(w http.ResponseWriter, r *http.Request) {
	item, err := s.renames.Apply(r.Context(), r.PathValue("id"))
	if err != nil {
		var missing *renames.TargetMissingError
		if errors.As(err, &missing) {
			// 409 with the dedicated code; the message carries the reject
			// recommendation (TargetMissingError.Error is the user-facing
			// copy).
			respondError(w, http.StatusConflict, "target_missing", missing.Error())
			return
		}
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, item)
}

// handleRenameReject implements POST /api/renames/{id}/reject: 200 with the
// proposal-after-reject object (both resources stay independent); 409
// conflict when the proposal is no longer pending; 404 for an unknown id.
func (s *Server) handleRenameReject(w http.ResponseWriter, r *http.Request) {
	item, err := s.renames.Reject(r.Context(), r.PathValue("id"))
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, item)
}
