package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mmqz/netops-lab/internal/changes"
	"github.com/mmqz/netops-lab/internal/domain"
)

// handleAttributeChangesList implements
// GET /api/attribute-changes?status=pending|applied|dismissed|all&resource_id=&batch_id=&limit=&offset=
// → {"items":[{id,resourceId,resourceType,naturalKey,batchId,field,oldValue,newValue,status,sheet,rowNumber,createdAt,appliedAt}],"total":n}.
// The pagination bounds follow the §9 list-endpoint conventions (limit
// defaults to 20, clamps at 100; offset defaults to 0, must be >= 0).
// status=all (or absent) lists every status; an unknown status is a 400.
func (s *Server) handleAttributeChangesList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := changes.Filter{
		ResourceID: q.Get("resource_id"),
		BatchID:    q.Get("batch_id"),
		Limit:      changes.DefaultPageSize,
		Offset:     0,
	}
	if raw := q.Get("status"); raw != "" && raw != "all" {
		st, err := domain.ParseChangeStatus(raw)
		if err != nil {
			respondErrorf(w, http.StatusBadRequest, "bad_request", "status 参数 %q 不是 pending/applied/dismissed/all", raw)
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
		if v > changes.MaxPageSize {
			v = changes.MaxPageSize // clamp, never fail (§9: max 100)
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
	result, err := s.changes.List(r.Context(), filter)
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, result)
}

// handleAttributeChangeApply implements POST /api/attribute-changes/{id}/apply:
// 200 with the change-after-apply object on success; 409 conflict when the
// proposal is no longer pending; 409 attribute_conflict (StaleError — the
// message names the field and both values) when the resource's current
// value has drifted from the recorded old value; 404 for an unknown id or a
// vanished resource.
func (s *Server) handleAttributeChangeApply(w http.ResponseWriter, r *http.Request) {
	item, err := s.changes.Apply(r.Context(), r.PathValue("id"))
	if err != nil {
		var stale *changes.StaleError
		if errors.As(err, &stale) {
			// 409 with the dedicated code; the message carries the field
			// name and both values (StaleError.Error is the user-facing copy).
			respondError(w, http.StatusConflict, "attribute_conflict", stale.Error())
			return
		}
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, item)
}

// handleAttributeChangeDismiss implements POST
// /api/attribute-changes/{id}/dismiss: 200 with the change-after-dismiss
// object; 409 conflict when the proposal is no longer pending; 404 for an
// unknown id.
func (s *Server) handleAttributeChangeDismiss(w http.ResponseWriter, r *http.Request) {
	item, err := s.changes.Dismiss(r.Context(), r.PathValue("id"))
	if err != nil {
		s.respondErrorFor(w, r, err, "")
		return
	}
	respondJSON(w, http.StatusOK, item)
}
